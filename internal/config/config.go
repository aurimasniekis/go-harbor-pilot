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
	Gateway  Gateway  `yaml:"gateway"`
}

// Gateway holds data-plane settings.
type Gateway struct {
	// UsageHeaders reports usage to Anthropic Messages clients in
	// anthropic-ratelimit-unified-* response headers (D-048): "off" (the
	// default) or "auto".
	UsageHeaders string `yaml:"usage_headers"`
}

// Usage header modes.
const (
	UsageHeadersOff  = "off"
	UsageHeadersAuto = "auto"
)

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
	// SecretRoots confine file-based secret references. They may not
	// contain the data directory or the master key files.
	SecretRoots []string `yaml:"secret_roots"`
	// SecretEnvPrefixes limit env: secret references to variables with
	// these prefixes (default HP_SECRET_). Bootstrap variables such as
	// HP_MASTER_KEY are never readable.
	SecretEnvPrefixes []string `yaml:"secret_env_prefixes"`
}

// ProtectedPaths are the files and directories secret references may never
// read: the data directory and the master key files.
func (s Security) protectedWith(dataDir string) []string {
	out := []string{}
	for _, p := range append([]string{dataDir, s.MasterKeyFile}, s.PreviousMasterKeyFiles...) {
		if p != "" {
			out = append(out, filepath.Clean(p))
		}
	}
	return out
}

// ProtectedPaths returns the paths secret references may never read.
func (c Config) ProtectedPaths() []string { return c.Security.protectedWith(c.Server.DataDir) }

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
	// ShelfMaxPerAccount and ShelfMaxBytes bound the Claude Code sessions
	// kept after their process ended, for up to the cache TTL (D-045).
	ShelfMaxPerAccount int   `yaml:"shelf_max_per_account"`
	ShelfMaxBytes      int64 `yaml:"shelf_max_bytes"`
}

type Demo struct {
	Enabled bool `yaml:"enabled"`
	// Clock freezes time (RFC 3339) for deterministic demo output.
	Clock string `yaml:"clock"`
	// AllowPublic permits a non-loopback listen address in demo mode. The
	// demo administrator's password is published, so this is off.
	AllowPublic bool `yaml:"allow_public"`
}

// Defaults returns the built-in defaults.
func Defaults() Config {
	return Config{
		Server: Server{
			Listen:          "127.0.0.1:8080",
			DataDir:         "./.data",
			ShutdownTimeout: 30 * time.Second,
		},
		Log:     Log{Level: "info"},
		Gateway: Gateway{UsageHeaders: UsageHeadersOff},
	}
}

// Flags are command-line overrides (highest precedence).
type Flags struct {
	Listen          string
	DataDir         string
	PublicURL       string
	Demo            bool
	DemoClock       string
	DemoAllowPublic bool
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
	setStr(&c.Gateway.UsageHeaders, "HP_GATEWAY_USAGE_HEADERS")
	if v := strings.TrimSpace(env("HP_SECRET_ENV_PREFIXES")); v != "" {
		c.Security.SecretEnvPrefixes = nil
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				c.Security.SecretEnvPrefixes = append(c.Security.SecretEnvPrefixes, p)
			}
		}
	}
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
	if f.DemoAllowPublic {
		c.Demo.AllowPublic = true
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
	if c.Demo.Enabled && !c.Demo.AllowPublic && !loopbackListen(c.Server.Listen) {
		errs = append(errs, errors.New("demo mode listens only on a loopback address (its administrator password is public); use --demo-allow-public to override"))
	}
	if c.Demo.Clock != "" {
		if _, err := time.Parse(time.RFC3339, c.Demo.Clock); err != nil {
			errs = append(errs, errors.New("demo.clock must be RFC 3339"))
		}
		if !c.Demo.Enabled {
			errs = append(errs, errors.New("demo.clock requires demo mode"))
		}
	}
	for _, pre := range c.Security.SecretEnvPrefixes {
		if len(pre) < 3 || strings.HasPrefix("HP_MASTER_KEY", pre) || (strings.HasPrefix(pre, "HP_") && !strings.HasPrefix(pre, "HP_SECRET_")) {
			errs = append(errs, fmt.Errorf("security.secret_env_prefixes: %q is too broad; use a dedicated prefix such as HP_SECRET_", pre))
		}
	}
	for _, root := range c.Security.SecretRoots {
		r := filepath.Clean(root)
		for _, prot := range c.ProtectedPaths() {
			if prot == r || strings.HasPrefix(prot, r+string(filepath.Separator)) || r == string(filepath.Separator) {
				errs = append(errs, fmt.Errorf("security.secret_roots: %s contains the data directory or a master key file; use a dedicated secrets directory", root))
				break
			}
		}
	}
	switch c.Gateway.UsageHeaders {
	case "", UsageHeadersOff, UsageHeadersAuto:
	default:
		errs = append(errs, fmt.Errorf("gateway.usage_headers %q is not one of off|auto", c.Gateway.UsageHeaders))
	}
	switch strings.ToLower(c.Log.Level) {
	case "debug", "info", "warn", "warning", "error":
	default:
		errs = append(errs, fmt.Errorf("log.level %q is not one of debug|info|warn|error", c.Log.Level))
	}
	return errors.Join(errs...)
}

func loopbackListen(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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
