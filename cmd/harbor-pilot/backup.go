package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/app"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ops"
)

func cmdBackup(args []string, stdout io.Writer) error {
	if len(args) > 0 && args[0] == "verify" {
		if len(args) < 2 {
			return errors.New("usage: harbor-pilot backup verify DIR")
		}
		m, err := ops.Verify(args[1])
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "backup is intact: created %s by %s, files:", m.CreatedAt.Format(time.RFC3339), m.ServerVersion)
		for _, f := range m.Files {
			fmt.Fprintf(stdout, " %s (schema %d)", f.Name, f.SchemaVersion)
		}
		fmt.Fprintf(stdout, "\nneeds master key ids: %s\n", strings.Join(m.MasterKeyIDs, ", "))
		return nil
	}
	var out string
	var traces bool
	c, _, err := parseCommon("backup", args, func(fs *flag.FlagSet) {
		fs.StringVar(&out, "out", "", "backup directory (default: <data-dir>/backups/<time>)")
		fs.BoolVar(&traces, "include-traces", false, "also back up short-lived trace content (excluded by default)")
	})
	if err != nil {
		return err
	}
	cfg, _, err := loadConfig(c)
	if err != nil {
		return err
	}
	if out == "" {
		out = filepath.Join(cfg.Server.DataDir, "backups", time.Now().UTC().Format("20060102T150405Z"))
	}
	m, err := ops.Backup(context.Background(), ops.Sources{DB: cfg.DBPath(), Traces: cfg.TracesDBPath()}, out, traces, version)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "backup written to %s (%d files, master key ids %s)\n", out, len(m.Files), strings.Join(m.MasterKeyIDs, ", "))
	if !traces {
		fmt.Fprintln(stdout, "trace content was excluded; pass --include-traces to keep it (it stays encrypted)")
	}
	return nil
}

func cmdRestore(args []string, stdout io.Writer) error {
	var from string
	var yes bool
	c, _, err := parseCommon("restore", args, func(fs *flag.FlagSet) {
		fs.StringVar(&from, "from", "", "backup directory")
		fs.BoolVar(&yes, "yes", false, "replace the current databases (the server must be stopped)")
	})
	if err != nil {
		return err
	}
	if from == "" {
		return errors.New("--from is required")
	}
	cfg, _, err := loadConfig(c)
	if err != nil {
		return err
	}
	m, err := ops.Verify(from)
	if err != nil {
		return err
	}
	if !yes {
		fmt.Fprintf(stdout, "backup %s is intact (created %s). Re-run with --yes to replace the databases in %s.\n", from, m.CreatedAt.Format(time.RFC3339), cfg.Server.DataDir)
		return nil
	}
	unlock, err := app.LockDataDir(cfg.Server.DataDir)
	if err != nil {
		return fmt.Errorf("%w (stop the server before restoring)", err)
	}
	defer unlock()
	kr, err := app.LoadKeyring(cfg, os.Getenv)
	if err != nil {
		return err
	}
	res, err := ops.Restore(from, ops.Sources{DB: cfg.DBPath(), Traces: cfg.TracesDBPath()}, kr)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "restored %s; previous files kept as:\n", from)
	for _, p := range res.MovedAside {
		fmt.Fprintln(stdout, "  "+p)
	}
	fmt.Fprintln(stdout, "Start the server to apply any newer migrations; in-flight reservations from the backup are recovered at startup.")
	return nil
}
