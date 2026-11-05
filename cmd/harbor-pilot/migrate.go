package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/app"
	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
	"github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/migrate/quotaharbor"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

func cmdMigrate(args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] != "quota-harbor" {
		return errors.New("usage: harbor-pilot migrate quota-harbor --qh-config FILE [--qh-db FILE] [--since 2160h] [--apply]")
	}
	var qhConfig, qhDB string
	var since time.Duration
	var apply, asJSON bool
	c, _, err := parseCommon("migrate quota-harbor", args[1:], func(fs *flag.FlagSet) {
		fs.StringVar(&qhConfig, "qh-config", "", "Quota Harbor configuration file (includes are followed)")
		fs.StringVar(&qhDB, "qh-db", "", "Quota Harbor database to import observation history from (optional)")
		fs.DurationVar(&since, "since", 90*24*time.Hour, "import history newer than this")
		fs.BoolVar(&apply, "apply", false, "write the plan (default: dry run, print it only)")
		fs.BoolVar(&asJSON, "json", false, "print the plan as JSON")
	})
	if err != nil {
		return err
	}
	if qhConfig == "" {
		return errors.New("--qh-config is required")
	}
	src, err := quotaharbor.Load(qhConfig)
	if err != nil {
		return fmt.Errorf("read Quota Harbor config: %w", err)
	}
	cfg, log, err := loadConfig(c)
	if err != nil {
		return err
	}
	unlock, err := app.LockDataDir(cfg.Server.DataDir)
	if err != nil {
		return fmt.Errorf("%w (stop the server before importing)", err)
	}
	defer unlock()
	ctx := auth.WithPrincipal(context.Background(), auth.System)
	db, _, err := openOffline(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer db.Close()
	kr, err := app.LoadKeyring(cfg, os.Getenv)
	if err != nil {
		return err
	}
	svc, err := controlplane.New(ctx, controlplane.Options{DB: db, Keyring: kr, Keys: store.NewServerKeys(db, kr), Log: log, Resolver: auth.SecretResolver})
	if err != nil {
		return err
	}
	// The quota module installs adapter validation for monitors.
	_ = quota.New(quota.Options{DB: db, CP: svc, Keyring: kr, Log: log, Version: version})
	plan := quotaharbor.Build(src, svc.Snapshot())
	var imported int64
	if apply {
		monitors, err := quotaharbor.Apply(ctx, svc, plan)
		if err != nil {
			return err
		}
		if qhDB != "" {
			if imported, err = quotaharbor.ImportHistory(ctx, db, qhDB, monitors, time.Now().Add(-since)); err != nil {
				return fmt.Errorf("import history: %w", err)
			}
		}
	}
	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"applied": apply, "items": plan.Items, "observations_imported": imported})
	}
	tw := tabwriter.NewWriter(stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "KIND\tNAME\tACTION\tNOTE")
	for _, it := range plan.Items {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", it.Kind, it.Name, it.Action, it.Reason)
	}
	_ = tw.Flush()
	switch {
	case !apply:
		fmt.Fprintln(stdout, "\nDry run: nothing was written. Re-run with --apply to import.")
	case qhDB != "":
		fmt.Fprintf(stdout, "\nImported %d observations of history.\n", imported)
	}
	return nil
}
