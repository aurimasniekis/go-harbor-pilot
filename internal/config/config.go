// Package config loads the process bootstrap configuration.
//
// Bootstrap configuration covers only what the process needs before it can
// open its database: listen address, public URL, data directory, master key
// location, TLS and logging. Every managed object (providers, plans,
// bindings, policies, channels) lives in the database and is changed through
// the control-plane API; there is no second file-based source of truth.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the bootstrap configuration.
type Config struct {
	Server   Server   `yaml:"server"`
	Security Security `yaml:"security"`
	Log      Log      `yaml:"log"`
	Demo     Demo     `yaml:"demo"`
	Bridges  Bridges  `yaml:"bridges"`
}

// Server settings.
type Server struct {
	Listen          string        `yaml:"listen"`
	PublicURL       string        `yaml:"public_url"`
	DataDir         string        `yaml:"data_dir"`
	TrustedProxies  []string      `yaml:"trusted_proxies"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
	TLS             TLS           `yaml:"tls"`
	// MetricsToken protects /metrics with a bearer token read from this file.
	MetricsTokenFile string `yaml:"metrics_token_file"`
}

// TLS terminates TLS in-process when both files are set.
type TLS struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

// Security settings.
type Security struct {
	// MasterKeyFile holds the 32-byte KEK (base64 or hex). HP_MASTER_KEY may
	// carry the value directly instead.
	MasterKeyFile string `yaml:"master_key_file"`
	// PreviousMasterKeyFiles are older KEKs kept only to unwrap until
	// `harbor-pilot keys rotate` finishes.
	PreviousMasterKeyFiles []string `yaml:"previous_master_key_files"`
	// SecretRoots confine file-based secret references.
	SecretRoots []string `yaml:"secret_roots"`
}

// Log settings.
type Log struct {
	Level string `yaml:"level"`
}

// Demo settings.
// Bridges configures CLI bridge executors.
type Bridges struct {
	ClaudeCode BridgeCLI `yaml:"claude_code"`
	Codex      BridgeCLI `yaml:"codex"`
}

// BridgeCLI configures one CLI bridge.
type BridgeCLI struct {
	// Binary is the CLI path; empty means the name on PATH.
	Binary string `yaml:"binary"`
	// Disabled turns the bridge off even when the binary is present.
	Disabled bool `yaml:"disabled"`
	// MaxProcesses and MaxPerAccount bound concurrent CLI processes.
	MaxProcesses  int `yaml:"max_processes"`
	MaxPerAccount int `yaml:"max_per_account"`
	// IdleTimeout ends parked processes waiting for a follow-up turn;
	// ToolTimeout ends processes waiting for client tool results.
	IdleTimeout time.Duration `yaml:"idle_timeout"`
	ToolTimeout time.Duration `yaml:"tool_timeout"`
}

type Demo struct {
	Enabled bool `yaml:"enabled"`
	// Clock freezes time (RFC 3339) for deterministic demo output.
	Clock string `yaml:"clock"`
}

// Defaults returns the built-in defaults.
func Defaults() Config {
	return Config{
		Server: Server{
			Listen:          "127.0.0.1:8080",
			DataDir:         "./.data",
			ShutdownTimeout: 30 * time.Second,
		},
		Log: Log{Level: "info"},
	}
}

// Flags are command-line overrides (highest precedence).
type Flags struct {
	Listen    string
	DataDir   string
	PublicURL string
	Demo      bool
	DemoClock string
}

// Load reads path (optional), applies HP_* environment overrides and flags,
// and validates the result.
func Load(path string, env func(string) string, f Flags) (Config, error) {
	c := Defaults()
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return c, fmt.Errorf("read config: %w", err)
		}
		dec := yaml.NewDecoder(bytes.NewReader(b))
		dec.KnownFields(true)
		if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
			return c, fmt.Errorf("parse config %s: %w", filepath.Base(path), err)
		}
	}
	if env == nil {
		env = os.Getenv
	}
	setStr := func(dst *string, key string) {
		if v := strings.TrimSpace(env(key)); v != "" {
			*dst = v
		}
	}
	setStr(&c.Server.Listen, "HP_LISTEN")
	setStr(&c.Server.DataDir, "HP_DATA_DIR")
	setStr(&c.Server.PublicURL, "HP_PUBLIC_URL")
	setStr(&c.Security.MasterKeyFile, "HP_MASTER_KEY_FILE")
	setStr(&c.Log.Level, "HP_LOG_LEVEL")
	setStr(&c.Bridges.ClaudeCode.Binary, "HP_CLAUDE_BINARY")
	setStr(&c.Bridges.Codex.Binary, "HP_CODEX_BINARY")
	if f.Listen != "" {
		c.Server.Listen = f.Listen
	}
	if f.DataDir != "" {
		c.Server.DataDir = f.DataDir
	}
	if f.PublicURL != "" {
		c.Server.PublicURL = f.PublicURL
	}
	if f.Demo {
		c.Demo.Enabled = true
	}
	if f.DemoClock != "" {
		c.Demo.Clock = f.DemoClock
	}
	if c.Server.PublicURL == "" {
		// A wildcard listen address is not a usable origin; assume local
		// access until public_url is set.
		hostport := c.Server.Listen
		if host, port, err := net.SplitHostPort(hostport); err == nil && (host == "" || host == "0.0.0.0" || host == "::") {
			hostport = net.JoinHostPort("localhost", port)
		}
		c.Server.PublicURL = "http://" + hostport
		if c.Server.TLS.CertFile != "" {
			c.Server.PublicURL = "https://" + hostport
		}
	}
	return c, c.Validate()
}

// Validate checks internal consistency.
func (c Config) Validate() error {
	var errs []error
	if _, _, err := net.SplitHostPort(c.Server.Listen); err != nil {
		errs = append(errs, fmt.Errorf("server.listen: %w", err))
	}
	u, err := url.Parse(c.Server.PublicURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		errs = append(errs, errors.New("server.public_url must be an absolute http(s) URL"))
	} else {
		if u.Path != "" && u.Path != "/" {
			errs = append(errs, errors.New("server.public_url must not contain a path"))
		}
	}
	if c.Server.DataDir == "" {
		errs = append(errs, errors.New("server.data_dir is required"))
	}
	if (c.Server.TLS.CertFile == "") != (c.Server.TLS.KeyFile == "") {
		errs = append(errs, errors.New("server.tls.cert_file and key_file must be set together"))
	}
	if c.Server.ShutdownTimeout <= 0 {
		errs = append(errs, errors.New("server.shutdown_timeout must be positive"))
	}
	if c.Demo.Clock != "" {
		if _, err := time.Parse(time.RFC3339, c.Demo.Clock); err != nil {
			errs = append(errs, errors.New("demo.clock must be RFC 3339"))
		}
		if !c.Demo.Enabled {
			errs = append(errs, errors.New("demo.clock requires demo mode"))
		}
	}
	switch strings.ToLower(c.Log.Level) {
	case "debug", "info", "warn", "warning", "error":
	default:
		errs = append(errs, fmt.Errorf("log.level %q is not one of debug|info|warn|error", c.Log.Level))
	}
	return errors.Join(errs...)
}

// PublicOrigin returns scheme://host of the public URL.
func (c Config) PublicOrigin() string {
	u, _ := url.Parse(c.Server.PublicURL)
	return u.Scheme + "://" + u.Host
}

// SecureCookies reports whether cookies carry Secure and the __Host- prefix.
// That requires an https public URL; plain http is only suitable for local
// development and demo use.
func (c Config) SecureCookies() bool { return strings.HasPrefix(c.Server.PublicURL, "https://") }

// DBPath returns the path of harbor.db (or demo.db in demo mode).
func (c Config) DBPath() string {
	if c.Demo.Enabled {
		return filepath.Join(c.Server.DataDir, "demo.db")
	}
	return filepath.Join(c.Server.DataDir, "harbor.db")
}

// TracesDBPath returns the path of the trace content database.
func (c Config) TracesDBPath() string {
	if c.Demo.Enabled {
		return filepath.Join(c.Server.DataDir, "demo-traces.db")
	}
	return filepath.Join(c.Server.DataDir, "traces.db")
}
