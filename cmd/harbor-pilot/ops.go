package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/app"
	"github.com/aurimasniekis/go-harbor-pilot/internal/harness"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/crypto"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
	"github.com/aurimasniekis/go-harbor-pilot/migrations"
)

// readSecret reads a secret from a file, stdin or an environment variable,
// never from argv. The first non-empty source wins.
func readSecret(file string, fromStdin bool, env string, stdin io.Reader) (string, error) {
	switch {
	case file != "":
		b, err := os.ReadFile(file) //nolint:gosec // operator-chosen secret file
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	case fromStdin:
		line, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		return strings.TrimSpace(line), nil
	case env != "":
		return strings.TrimSpace(os.Getenv(env)), nil
	}
	return "", nil
}

// ---------- harness ----------

func cmdHarness(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) < 1 {
		return errors.New("usage: harbor-pilot harness setup|preview|uninstall|status [KIND] [flags]")
	}
	sub := args[0]
	if sub == "status" {
		for _, k := range harness.Kinds {
			st, err := harness.GetStatus(harness.Options{Kind: k})
			if err != nil {
				return err
			}
			state := "not configured"
			if st.Managed {
				state = "configured by Harbor Pilot (" + st.AppliedAt.Format(time.RFC3339) + ")"
				if st.Modified {
					state += ", edited since"
				}
			}
			fmt.Fprintf(stdout, "%-9s %s  %s\n", k, state, st.Path)
		}
		return nil
	}
	if len(args) < 2 {
		return fmt.Errorf("usage: harbor-pilot harness %s KIND (one of %s)", sub, strings.Join(harness.Kinds, ", "))
	}
	kind := args[1]
	fs := flag.NewFlagSet("harness "+sub, flag.ContinueOnError)
	url := fs.String("url", os.Getenv("HP_PUBLIC_URL"), "Harbor Pilot public URL")
	tokenFile := fs.String("token-file", "", "file holding the gateway token (referenced, not copied, where the harness allows)")
	tokenStdin := fs.Bool("token-stdin", false, "read the gateway token from stdin and store it in a 0600 file")
	tokenEnv := fs.String("token-env", "", "environment variable the harness should read the token from")
	model := fs.String("model", "", "model or alias to select, e.g. coding/default")
	dir := fs.String("config-dir", "", "harness config directory (default: the harness's own)")
	yes := fs.Bool("yes", false, "apply the change (without it the diff is only shown)")
	restore := fs.Bool("restore-backup", false, "uninstall by restoring the pre-setup backup instead of removing keys")
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}
	o := harness.Options{Kind: kind, BaseURL: *url, TokenFile: *tokenFile, TokenEnv: *tokenEnv, Model: *model, ConfigDir: *dir}
	switch sub {
	case "setup", "preview":
		if *tokenStdin {
			tok, err := readSecret("", true, "", stdin)
			if err != nil || tok == "" {
				return errors.New("no token on stdin")
			}
			home, _ := os.UserHomeDir()
			path := filepath.Join(home, ".config", "harbor-pilot", "tokens", kind+".token")
			if sub == "setup" && *yes {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					return err
				}
				if err := os.WriteFile(path, []byte(tok+"\n"), 0o600); err != nil {
					return err
				}
			}
			o.TokenFile = path
		}
		c, err := harness.Plan(o)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s\n\n%s", c.Path, c.Diff)
		for _, n := range c.Notes {
			fmt.Fprintln(stdout, "note:", n)
		}
		if sub == "preview" || !*yes {
			fmt.Fprintln(stdout, "\nNothing written. Re-run `harbor-pilot harness setup` with --yes to apply.")
			return nil
		}
		backup, err := harness.Apply(c)
		if err != nil {
			return err
		}
		if backup != "" {
			fmt.Fprintln(stdout, "\nBacked up the previous file to", backup)
		}
		fmt.Fprintln(stdout, "Applied. Undo with `harbor-pilot harness uninstall "+kind+" --yes`.")
		return nil
	case "uninstall":
		c, err := harness.PlanUninstall(o, *restore)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s\n\n%s", c.Path, c.Diff)
		if !*yes {
			fmt.Fprintln(stdout, "\nNothing written. Re-run with --yes to apply.")
			return nil
		}
		if err := harness.ApplyUninstall(c); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "\nRemoved the Harbor Pilot configuration.")
		return nil
	}
	return fmt.Errorf("unknown harness command %q", sub)
}

// ---------- API client ----------

type apiClient struct {
	base  string
	token string
	hc    *http.Client
}

func newAPIClient(fs *flag.FlagSet, args []string, stdin io.Reader) (*apiClient, error) {
	server := fs.String("server", envOr("HP_SERVER", "http://127.0.0.1:8080"), "Harbor Pilot URL")
	tokFile := fs.String("api-token-file", "", "file holding a service-principal API token (hpa_…)")
	tokStdin := fs.Bool("api-token-stdin", false, "read the API token from stdin")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	tok, err := readSecret(*tokFile, *tokStdin, "HP_API_TOKEN", stdin)
	if err != nil {
		return nil, err
	}
	if tok == "" {
		return nil, errors.New("an API token is required: set HP_API_TOKEN, --api-token-file or --api-token-stdin")
	}
	return &apiClient{base: strings.TrimRight(*server, "/") + "/api/v1", token: tok, hc: &http.Client{Timeout: 30 * time.Second}}, nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func (c *apiClient) do(method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, r) //nolint:noctx // CLI request bounded by the client timeout
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 400 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(b, &e)
		return fmt.Errorf("%s %s: %s %s", method, path, resp.Status, e.Error.Message)
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

// ---------- token ----------

func cmdToken(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 || args[0] != "issue" {
		return errors.New("usage: harbor-pilot token issue --binding ID --name NAME [--scopes a,b] [--ttl 720h]")
	}
	fs := flag.NewFlagSet("token issue", flag.ContinueOnError)
	binding := fs.String("binding", "", "client binding id")
	name := fs.String("name", "cli", "credential name")
	scopes := fs.String("scopes", "", "comma-separated scopes (default: invoke and list models)")
	ttl := fs.Duration("ttl", 0, "expiry (0 = no expiry)")
	c, err := newAPIClient(fs, args[1:], stdin)
	if err != nil {
		return err
	}
	if *binding == "" {
		return errors.New("--binding is required")
	}
	body := map[string]any{"name": *name}
	if *scopes != "" {
		body["scopes"] = strings.Split(*scopes, ",")
	}
	if *ttl > 0 {
		body["ttl_seconds"] = int64(ttl.Seconds())
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := c.do(http.MethodPost, "/client-bindings/"+*binding+"/credentials", body, &out); err != nil {
		return err
	}
	fmt.Fprintln(stdout, out.Token)
	fmt.Fprintln(os.Stderr, "This token is shown once. Store it in a file or secret manager.")
	return nil
}

// ---------- policy simulate ----------

func cmdPolicy(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 || args[0] != "simulate" {
		return errors.New("usage: harbor-pilot policy simulate --plan ID --model MODEL [--protocol P] [--harness H] [--stream]")
	}
	fs := flag.NewFlagSet("policy simulate", flag.ContinueOnError)
	plan := fs.String("plan", "", "routing plan id")
	model := fs.String("model", "", "requested model or alias")
	proto := fs.String("protocol", "anthropic_messages", "inbound protocol")
	h := fs.String("harness", "", "harness to simulate (claude_code, codex, …)")
	stream := fs.Bool("stream", true, "streaming request")
	c, err := newAPIClient(fs, args[1:], stdin)
	if err != nil {
		return err
	}
	if *plan == "" || *model == "" {
		return errors.New("--plan and --model are required")
	}
	var out map[string]any
	if err := c.do(http.MethodPost, "/routing-plans/"+*plan+"/simulate", map[string]any{"model": *model, "protocol": *proto, "harness": *h, "stream": *stream}, &out); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Fprintln(stdout, string(b))
	return nil
}

// ---------- doctor ----------

type check struct {
	name, status, detail string
}

func cmdDoctor(args []string, stdout io.Writer) error {
	var server string
	c, _, err := parseCommon("doctor", args, func(fs *flag.FlagSet) {
		fs.StringVar(&server, "server", "", "also check a running server (readiness)")
	})
	if err != nil {
		return err
	}
	var checks []check
	add := func(name, status, detail string) { checks = append(checks, check{name, status, detail}) }
	cfg, log, err := loadConfig(c)
	if err != nil {
		add("configuration", "fail", err.Error())
		return report(stdout, checks)
	}
	add("configuration", "ok", "public URL "+cfg.Server.PublicURL)
	if !cfg.SecureCookies() {
		add("public URL", "warn", "plain http: cookies are not Secure; use https outside local development")
	}
	if err := os.MkdirAll(cfg.Server.DataDir, 0o700); err != nil {
		add("data directory", "fail", err.Error())
	} else if f, err := os.CreateTemp(cfg.Server.DataDir, ".doctor-*"); err != nil {
		add("data directory", "fail", "not writable: "+err.Error())
	} else {
		_ = f.Close()
		_ = os.Remove(f.Name())
		add("data directory", "ok", cfg.Server.DataDir)
	}
	kr, err := app.LoadKeyring(cfg, os.Getenv)
	if err != nil {
		add("master key", "fail", err.Error())
	} else {
		add("master key", "ok", "key id "+kr.CurrentID())
	}
	ctx := context.Background()
	if _, err := os.Stat(cfg.DBPath()); err == nil {
		db, err := store.Open(ctx, store.Options{Path: cfg.DBPath(), Migrations: migrations.Main(), Log: log})
		if err != nil {
			add("database", "fail", err.Error())
		} else {
			v, _ := db.SchemaVersion(ctx)
			add("database", "ok", fmt.Sprintf("%s schema %d", cfg.DBPath(), v))
			if kr != nil {
				var env string
				if err := db.R.QueryRowContext(ctx, `SELECT envelope FROM resource_secrets LIMIT 1`).Scan(&env); err == nil {
					if id, err := crypto.EnvelopeKEK(env); err == nil && !kr.Has(id) {
						add("master key", "fail", "stored secrets were sealed with master key "+id+", which is not configured")
					}
				}
			}
			_ = db.Close()
		}
	} else {
		add("database", "warn", "not created yet (first start creates it)")
	}
	for _, b := range []struct{ name, bin string }{{"claude CLI", cfg.Bridges.ClaudeCode.Binary}, {"codex CLI", cfg.Bridges.Codex.Binary}} {
		bin := b.bin
		short := strings.Fields(b.name)[0]
		if bin == "" {
			bin, _ = exec.LookPath(short)
		}
		if bin == "" {
			add(b.name, "warn", "not found; subscription bridge for "+short+" is unavailable")
			continue
		}
		out, err := exec.Command(bin, "--version").Output() //nolint:gosec // operator-configured binary
		if err != nil {
			add(b.name, "warn", bin+": "+err.Error())
			continue
		}
		add(b.name, "ok", bin+" "+strings.TrimSpace(string(out)))
	}
	for _, k := range harness.Kinds {
		st, err := harness.GetStatus(harness.Options{Kind: k})
		if err == nil && st.Managed {
			s := "ok"
			if st.Modified {
				s = "warn"
			}
			add("harness "+k, s, "configured at "+st.Path)
		}
	}
	if server != "" {
		cl := &http.Client{Timeout: 3 * time.Second}
		resp, err := cl.Get(strings.TrimRight(server, "/") + "/readyz")
		switch {
		case err != nil:
			add("server", "fail", err.Error())
		case resp.StatusCode != 200:
			_ = resp.Body.Close()
			add("server", "fail", resp.Status)
		default:
			_ = resp.Body.Close()
			add("server", "ok", "ready")
		}
	}
	return report(stdout, checks)
}

func report(stdout io.Writer, checks []check) error {
	failed := false
	for _, c := range checks {
		mark := map[string]string{"ok": "✓", "warn": "!", "fail": "✗"}[c.status]
		fmt.Fprintf(stdout, "%s %-16s %s\n", mark, c.name, c.detail)
		failed = failed || c.status == "fail"
	}
	if failed {
		return errors.New("doctor found problems")
	}
	return nil
}
