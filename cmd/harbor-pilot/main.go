// Command harbor-pilot runs the Harbor Pilot gateway and control plane and
// provides operational subcommands.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aurimasniekis/go-harbor-pilot/internal/app"
	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
	"github.com/aurimasniekis/go-harbor-pilot/internal/config"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/crypto"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/observability"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
	"github.com/aurimasniekis/go-harbor-pilot/migrations"
)

var version = "dev"

const usage = `Harbor Pilot connects coding agents to models and subscriptions, routes each
request through explicit policies, and explains what happened.

Usage:
  harbor-pilot serve [--config FILE] [--listen ADDR] [--data-dir DIR] [--public-url URL] [--demo] [--demo-clock RFC3339]
  harbor-pilot admin bootstrap [--config FILE]       print a one-time setup link for the first administrator
  harbor-pilot admin recover --email EMAIL [--config FILE]
                                                     print a one-time break-glass recovery link
  harbor-pilot keys generate                         print a new random master key (base64)
  harbor-pilot keys rotate --config FILE             re-wrap stored secrets under the current master key
  harbor-pilot config validate [--config FILE]
  harbor-pilot openapi                               print the control-plane OpenAPI document (YAML)
  harbor-pilot healthcheck [--url URL]
  harbor-pilot doctor [--config FILE] [--server URL]  check configuration, keys, storage and CLI bridges
  harbor-pilot harness preview|setup|uninstall KIND --url URL (--token-file F | --token-stdin | --token-env VAR) [--model M] [--yes]
                                                     configure claude, codex, opencode or pi reversibly
  harbor-pilot harness status
  harbor-pilot token issue --binding ID [--name N] [--scopes a,b] [--ttl 720h]
  harbor-pilot policy simulate --plan ID --model M [--protocol P] [--harness H]
                                                     (API commands read HP_API_TOKEN, --api-token-file or --api-token-stdin; HP_SERVER)
  harbor-pilot version

Secrets are never accepted as command-line arguments; use files, stdin or the environment.
`

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage)
		return nil
	}
	switch args[0] {
	case "serve":
		return cmdServe(args[1:], stderr)
	case "admin":
		return cmdAdmin(args[1:], stdout, stderr)
	case "keys":
		return cmdKeys(args[1:], stdout, stderr)
	case "config":
		return cmdConfig(args[1:], stdout)
	case "openapi":
		return cmdOpenAPI(stdout)
	case "healthcheck":
		return cmdHealthcheck(args[1:])
	case "harness":
		return cmdHarness(args[1:], os.Stdin, stdout)
	case "doctor":
		return cmdDoctor(args[1:], stdout)
	case "token":
		return cmdToken(args[1:], os.Stdin, stdout)
	case "policy":
		return cmdPolicy(args[1:], os.Stdin, stdout)
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, "harbor-pilot", version)
		return nil
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return nil
	}
	return fmt.Errorf("unknown command %q (see `harbor-pilot help`)", args[0])
}

type commonFlags struct {
	config string
	flags  config.Flags
}

func parseCommon(name string, args []string, extra func(fs *flag.FlagSet)) (*commonFlags, *flag.FlagSet, error) {
	c := &commonFlags{}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.StringVar(&c.config, "config", os.Getenv("HP_CONFIG"), "bootstrap configuration file")
	fs.StringVar(&c.flags.Listen, "listen", "", "listen address")
	fs.StringVar(&c.flags.DataDir, "data-dir", "", "data directory")
	fs.StringVar(&c.flags.PublicURL, "public-url", "", "public base URL")
	fs.BoolVar(&c.flags.Demo, "demo", false, "serve the deterministic synthetic demo")
	fs.StringVar(&c.flags.DemoClock, "demo-clock", "", "freeze the demo clock (RFC 3339)")
	if extra != nil {
		extra(fs)
	}
	return c, fs, fs.Parse(args)
}

func loadConfig(c *commonFlags) (config.Config, *slog.Logger, error) {
	cfg, err := config.Load(c.config, os.Getenv, c.flags)
	if err != nil {
		return cfg, nil, err
	}
	return cfg, observability.NewLogger(os.Stderr, observability.ParseLevel(cfg.Log.Level)), nil
}

func cmdServe(args []string, _ io.Writer) error {
	c, _, err := parseCommon("serve", args, nil)
	if err != nil {
		return err
	}
	cfg, log, err := loadConfig(c)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := app.New(ctx, app.Options{Config: cfg, Version: version, Log: log})
	if err != nil {
		return err
	}
	defer a.Close()
	if err := setupFeatures(ctx, a); err != nil {
		return err
	}
	return a.Run(ctx)
}

// openOffline opens the database for an offline administrative command. It
// does not take the data-directory lock: SQLite WAL allows a running server
// and a short CLI transaction to coexist.
func openOffline(ctx context.Context, cfg config.Config, log *slog.Logger) (*store.DB, *auth.Service, error) {
	kr, err := app.LoadKeyring(cfg, os.Getenv)
	if err != nil {
		return nil, nil, err
	}
	db, err := store.Open(ctx, store.Options{Path: cfg.DBPath(), Migrations: migrations.Main(), Log: log, BackupBeforeMigrate: true})
	if err != nil {
		return nil, nil, err
	}
	svc, err := auth.New(ctx, auth.Options{DB: db, Keys: store.NewServerKeys(db, kr), Keyring: kr, Log: log,
		SecureCookies: cfg.SecureCookies(), PublicOrigin: cfg.PublicOrigin()})
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	return db, svc, nil
}

func cmdAdmin(args []string, stdout, _ io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: harbor-pilot admin bootstrap|recover")
	}
	var email string
	c, _, err := parseCommon("admin "+args[0], args[1:], func(fs *flag.FlagSet) {
		if args[0] == "recover" {
			fs.StringVar(&email, "email", "", "email of the user to recover")
		}
	})
	if err != nil {
		return err
	}
	cfg, log, err := loadConfig(c)
	if err != nil {
		return err
	}
	ctx := auth.WithPrincipal(context.Background(), auth.System)
	db, svc, err := openOffline(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer db.Close()
	switch args[0] {
	case "bootstrap":
		required, err := svc.SetupRequired(ctx)
		if err != nil {
			return err
		}
		if !required {
			return errors.New("an administrator already exists; use `harbor-pilot admin recover` for break-glass access")
		}
		tok, exp, err := svc.IssueOneTime(ctx, auth.PurposeSetup, "", "", "", "cli")
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Open this one-time setup link within 30 minutes (expires %s):\n\n  %s/setup?token=%s\n\n",
			exp.UTC().Format(time.RFC3339), cfg.PublicOrigin(), tok)
		return nil
	case "recover":
		if email == "" {
			return errors.New("--email is required")
		}
		var userID string
		if err := db.R.QueryRowContext(ctx, `SELECT id FROM users WHERE email = ?`, strings.ToLower(strings.TrimSpace(email))).Scan(&userID); err != nil {
			return fmt.Errorf("no user with email %s", email)
		}
		tok, exp, err := svc.IssueOneTime(ctx, auth.PurposeRecovery, userID, "", "", "cli")
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Break-glass recovery link (single use, expires %s). Its use is audited and raises a\n"+
			"persistent security alert until an administrator acknowledges it:\n\n  %s/recover?token=%s\n\n",
			exp.UTC().Format(time.RFC3339), cfg.PublicOrigin(), tok)
		return nil
	}
	return fmt.Errorf("unknown admin command %q", args[0])
}

func cmdKeys(args []string, stdout, _ io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: harbor-pilot keys generate|rotate")
	}
	switch args[0] {
	case "generate":
		fmt.Fprintln(stdout, crypto.GenerateKEKString())
		return nil
	case "rotate":
		c, _, err := parseCommon("keys rotate", args[1:], nil)
		if err != nil {
			return err
		}
		cfg, log, err := loadConfig(c)
		if err != nil {
			return err
		}
		if len(cfg.Security.PreviousMasterKeyFiles) == 0 {
			return errors.New("set security.previous_master_key_files to the old key and master_key_file to the new key, then run rotate")
		}
		ctx := context.Background()
		kr, err := app.LoadKeyring(cfg, os.Getenv)
		if err != nil {
			return err
		}
		db, err := store.Open(ctx, store.Options{Path: cfg.DBPath(), Migrations: migrations.Main(), Log: log})
		if err != nil {
			return err
		}
		defer db.Close()
		n, err := rotateAll(ctx, db, kr)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "re-wrapped %d secrets under master key %s; previous keys can now be removed from configuration\n", n, kr.CurrentID())
		return nil
	}
	return fmt.Errorf("unknown keys command %q", args[0])
}

func cmdConfig(args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] != "validate" {
		return errors.New("usage: harbor-pilot config validate [--config FILE]")
	}
	c, _, err := parseCommon("config validate", args[1:], nil)
	if err != nil {
		return err
	}
	if _, _, err := loadConfig(c); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "configuration is valid")
	return nil
}

func cmdOpenAPI(stdout io.Writer) error {
	b, err := openAPIDocument()
	if err != nil {
		return err
	}
	_, err = stdout.Write(b)
	return err
}

// openAPIDocument builds the API with every operation registered but no live
// dependencies, and renders the OpenAPI document.
func openAPIDocument() ([]byte, error) {
	a := &app.App{}
	regs := specRegistrations(a)
	built := app.BuildAPI(chi.NewRouter(), version, regs)
	return built.Huma.OpenAPI().YAML()
}

func cmdHealthcheck(args []string) error {
	fs := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	url := fs.String("url", "http://127.0.0.1:8080/readyz", "readiness URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cl := &http.Client{Timeout: 3 * time.Second}
	resp, err := cl.Get(*url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("not ready: %s", resp.Status)
	}
	return nil
}
