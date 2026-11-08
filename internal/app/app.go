// Package app is the composition root: it loads bootstrap configuration,
// opens storage, constructs services, mounts the API, gateway and UI, and
// runs background workers.
package app

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/aurimasniekis/go-harbor-pilot/internal/accounting"
	"github.com/aurimasniekis/go-harbor-pilot/internal/accounting/usageapi"
	"github.com/aurimasniekis/go-harbor-pilot/internal/api"
	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
	"github.com/aurimasniekis/go-harbor-pilot/internal/auth/authapi"
	"github.com/aurimasniekis/go-harbor-pilot/internal/compat"
	"github.com/aurimasniekis/go-harbor-pilot/internal/compat/compatapi"
	"github.com/aurimasniekis/go-harbor-pilot/internal/config"
	"github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/controlplane/cpapi"
	"github.com/aurimasniekis/go-harbor-pilot/internal/demo"
	"github.com/aurimasniekis/go-harbor-pilot/internal/events/eventsapi"
	"github.com/aurimasniekis/go-harbor-pilot/internal/events/relay"
	"github.com/aurimasniekis/go-harbor-pilot/internal/executor"
	"github.com/aurimasniekis/go-harbor-pilot/internal/executor/httpapi"
	"github.com/aurimasniekis/go-harbor-pilot/internal/gateway"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/notify"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/clock"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/crypto"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/netpolicy"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/observability"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/reqmeta"
	"github.com/aurimasniekis/go-harbor-pilot/internal/privacy"
	"github.com/aurimasniekis/go-harbor-pilot/internal/privacy/privacyapi"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/polling"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/quotaapi"
	"github.com/aurimasniekis/go-harbor-pilot/internal/routing"
	"github.com/aurimasniekis/go-harbor-pilot/internal/routing/routingapi"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
	"github.com/aurimasniekis/go-harbor-pilot/internal/telemetry"
	"github.com/aurimasniekis/go-harbor-pilot/internal/telemetry/activityapi"
	"github.com/aurimasniekis/go-harbor-pilot/internal/telemetry/statsapi"
	"github.com/aurimasniekis/go-harbor-pilot/internal/traces"
	"github.com/aurimasniekis/go-harbor-pilot/internal/traces/tracesapi"
	"github.com/aurimasniekis/go-harbor-pilot/internal/web"
	"github.com/aurimasniekis/go-harbor-pilot/migrations"
)

// Options configure New.
type Options struct {
	Config config.Config
	// TelemetryQueue bounds the activity writer queue (0 = default); tests
	// shrink it to exercise back-pressure.
	TelemetryQueue int
	Version        string
	// Commit is the source revision the binary was built from; empty falls
	// back to the Go build info.
	Commit string
	Log    *slog.Logger
	Env    func(string) string
	// QuotaTransport overrides the quota adapters' outbound transport
	// (tests point adapters at local fakes).
	QuotaTransport http.RoundTripper
	// QuotaScheduler tunes quota polling (tests).
	QuotaScheduler polling.Options
	// Clock overrides the application clock (tests).
	Clock clock.Clock
	// PulseConnector overrides how Pulse publishers connect (tests).
	PulseConnector relay.PulseConnector
	// BridgeEnv is appended to CLI bridge environments (tests point the CLI
	// at a synthetic upstream).
	BridgeEnv []string
}

// App is the running application.
type App struct {
	Cfg     config.Config
	Version string
	Commit  string
	Log     *slog.Logger
	Clock   clock.Clock
	Metrics *observability.Metrics
	Keyring *crypto.Keyring
	DB      *store.DB
	Traces  *store.DB
	Keys    *store.ServerKeys
	Auth    *auth.Service
	CP      *controlplane.Service
	Acct    *accounting.Service
	Tel     *telemetry.Writer
	Gateway *gateway.Gateway
	Quota   *quota.Service
	Privacy *privacy.Service
	// TraceSvc stores short-lived trace content in traces.db.
	TraceSvc *traces.Service
	Compat   *compat.Manager
	Watch    *compat.Watch
	Relay    *relay.Relay
	Notify   *notify.Engine
	Budgets  *accounting.Budgets
	Started  time.Time
	// DemoUpstreams are the synthetic providers (demo mode only, else nil).
	DemoUpstreams *demo.Upstreams

	unlock  func()
	closers []func()

	bridgeMu     sync.Mutex
	bridgeStatus []*BridgeStatus
	bgWG         sync.WaitGroup
	bg           []func(ctx context.Context)
	routes       []func(r chi.Router)
	apiRegs      []func(a *api.API)
	ready        bool
	readyMu      sync.RWMutex
	metricsT     credentials.Secret
}

// New wires the application.
func New(ctx context.Context, o Options) (*App, error) {
	if o.Env == nil {
		o.Env = os.Getenv
	}
	c := o.Config
	a := &App{Cfg: c, Version: o.Version, Commit: buildCommit(o.Commit), Log: o.Log, Metrics: observability.NewMetrics()}
	a.Clock = clock.Real{}
	if c.Demo.Clock != "" {
		t, _ := time.Parse(time.RFC3339, c.Demo.Clock)
		a.Clock = clock.Frozen{At: t}
	}
	if o.Clock != nil {
		a.Clock = o.Clock
	}
	// Uptime is reported against the same clock as server time (frozen in
	// demo mode).
	a.Started = a.Clock.Now()
	unlock, err := lockDataDir(c.Server.DataDir)
	if err != nil {
		return nil, err
	}
	a.unlock = unlock
	fail := func(err error) (*App, error) {
		a.Close()
		return nil, err
	}
	if a.Keyring, err = LoadKeyring(c, o.Env); err != nil {
		return fail(err)
	}
	if a.DB, err = store.Open(ctx, store.Options{Path: c.DBPath(), Migrations: migrations.Main(), Log: a.Log, BackupBeforeMigrate: true}); err != nil {
		return fail(fmt.Errorf("open %s: %w", c.DBPath(), err))
	}
	if a.Traces, err = store.Open(ctx, store.Options{Path: c.TracesDBPath(), Migrations: migrations.Traces(), Log: a.Log, Readers: 2}); err != nil {
		return fail(fmt.Errorf("open %s: %w", c.TracesDBPath(), err))
	}
	a.Keys = store.NewServerKeys(a.DB, a.Keyring)
	// Secret references may read only dedicated env vars and files outside
	// the data directory and key files (whoever sets a reference can send
	// its value upstream).
	credentials.SetPolicy(credentials.Policy{EnvPrefixes: c.Security.SecretEnvPrefixes, Protected: c.ProtectedPaths()})
	auth.SecretResolver = credentials.NewResolver(c.Security.SecretRoots)
	if a.Auth, err = auth.New(ctx, auth.Options{DB: a.DB, Keys: a.Keys, Keyring: a.Keyring, Clock: a.Clock, Log: a.Log,
		SecureCookies: c.SecureCookies(), PublicOrigin: c.PublicOrigin()}); err != nil {
		return fail(err)
	}
	if a.CP, err = controlplane.New(ctx, controlplane.Options{DB: a.DB, Keyring: a.Keyring, Keys: a.Keys, Clock: a.Clock, Log: a.Log,
		Resolver: auth.SecretResolver}); err != nil {
		return fail(err)
	}
	a.CP.OnSwap(func(s *controlplane.Snapshot) { a.Metrics.ConfigRevision.Set(float64(s.ConfigRevision)) })
	a.Metrics.ConfigRevision.Set(float64(a.CP.Snapshot().ConfigRevision))
	a.Quota = quota.New(quota.Options{DB: a.DB, CP: a.CP, Keyring: a.Keyring, Clock: a.Clock, Log: a.Log, Version: o.Version,
		Demo: c.Demo.Enabled, SecretRoots: c.Security.SecretRoots, Transport: o.QuotaTransport, Scheduler: o.QuotaScheduler})
	a.Acct = accounting.New(a.DB, a.Clock, a.Log)
	a.Budgets = &accounting.Budgets{Snapshot: a.CP.Snapshot}
	a.Acct.BudgetCheck = a.Budgets.Check
	a.Acct.OnBudgetExceeded = a.Budgets.Exceeded(a.DB.W)
	if _, err := a.Acct.RecoverStartup(ctx); err != nil {
		return fail(fmt.Errorf("accounting recovery: %w", err))
	}
	a.Tel = telemetry.NewWriter(a.DB, a.Log, a.Metrics, o.TelemetryQueue)
	sessKey, err := a.Keys.Get(ctx, store.KeySessionPepper)
	if err != nil {
		return fail(err)
	}
	a.Gateway = &gateway.Gateway{CP: a.CP, Acct: a.Acct, Tel: a.Tel, Sessions: telemetry.NewSessions(a.Tel, sessKey),
		HTTP: httpapi.New("harbor-pilot/" + o.Version), Bridges: map[string]executor.Executor{}, DB: a.DB, Clock: a.Clock, Log: a.Log,
		Metrics: a.Metrics, Limits: gateway.DefaultLimits}
	a.Gateway.AfterRequest = gateway.NewTransitions(a.DB.W).After
	a.Gateway.Budgets = a.Budgets
	a.Gateway.HealthOf = a.Quota.Health
	a.Privacy = privacy.NewService(a.CP, a.Clock.Now)
	a.Gateway.Privacy = a.Privacy
	controlplane.TraceNow = a.Clock.Now
	a.TraceSvc = traces.New(a.Traces, a.Keyring, a.Clock, a.Log)
	a.Gateway.Traces = a.TraceSvc
	a.AddBackground(a.TraceSvc.Run)
	if a.Compat, err = compat.NewManager(ctx, a.DB, a.Clock, compat.Options{}); err != nil {
		return fail(err)
	}
	a.Watch = compat.NewWatch(a.DB, a.Clock, a.Log)
	a.Compat.OnTransition = func(b compat.Breaker, from, to string) {
		if to == compat.Closed && from != compat.Closed {
			a.Gateway.BreakerClosed(b.Key, b.ProviderID, b.Path)
			_, up, _ := strings.Cut(b.Path, ">")
			a.Watch.Observe(compat.Signal{Direction: "response", Protocol: ir.Protocol(up), ProviderID: b.ProviderID, Route: b.Key,
				Action: map[string]any{"restored": true, "path": b.Path, "from": from}})
		}
	}
	a.Gateway.Compat, a.Gateway.Watch = a.Compat, a.Watch
	a.Gateway.Observe = func(o gateway.Observation) {
		if len(o.Unknown) > 0 {
			a.Watch.Observe(compat.Signal{Direction: o.Direction, Protocol: o.Protocol, ProviderID: o.ProviderID, Harness: o.Harness,
				HarnessVer: o.HarnessVer, Unknown: o.Unknown})
		}
	}
	a.Gateway.HTTP.Sample = func(p ir.Protocol, providerID string, raw []byte) {
		a.Watch.Observe(compat.Signal{Protocol: p, ProviderID: providerID, Raw: raw, Learn: true})
	}
	a.Gateway.HTTP.SampleEvery = 20
	a.AddBackground(a.Watch.Run)
	a.Relay = relay.New(a.DB, a.Clock, a.Log, a.CP.Snapshot)
	a.Relay.Secret = func(ctx context.Context, id, field string) (credentials.Secret, error) {
		return a.CP.ResolveSecret(ctx, controlplane.DestinationDef, id, field)
	}
	a.Relay.ReadFile = func(path string) ([]byte, error) {
		ref, err := auth.ParseSecretRef("file:" + path)
		if err != nil {
			return nil, err
		}
		sec, err := auth.SecretResolver.Resolve(ref)
		return []byte(sec.Reveal()), err
	}
	a.Relay.Pulse = o.PulseConnector
	if a.Relay.Pulse == nil {
		a.Relay.Pulse = relay.ConnectPulse
	}
	a.AddBackground(a.Relay.Run)
	a.Notify = notify.New(a.DB, a.Clock, a.Log, a.CP.Snapshot)
	a.Notify.Secret = func(ctx context.Context, id string) (credentials.Secret, error) {
		return a.CP.ResolveSecret(ctx, controlplane.ChannelDef, id, "secret")
	}
	a.AddBackground(a.Notify.Run)
	a.AddBackground(func(ctx context.Context) {
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				a.Privacy.Vault.Sweep()
			}
		}
	})
	a.setupBridges(o)
	a.Gateway.ObserveResponse = a.Quota.ObserveResponse
	a.AddBackground(a.Quota.Run)
	a.AddBackground(a.Acct.Run)
	a.AddBackground(a.Tel.Run)
	a.AddRoutes(a.Gateway.Routes)
	if c.Demo.Enabled {
		ups := demo.NewUpstreams()
		a.DemoUpstreams = ups
		a.AddRoutes(ups.Routes)
		base := "http://" + c.Server.Listen
		toks, err := demo.SeedConfig(ctx, a.CP, base)
		if err != nil {
			return fail(fmt.Errorf("demo seed: %w", err))
		}
		if err := a.Quota.BackfillDemo(ctx, 7*24*time.Hour); err != nil {
			return fail(fmt.Errorf("demo quota history: %w", err))
		}
		a.AddBackground(func(ctx context.Context) {
			var n int
			_ = a.DB.R.QueryRowContext(ctx, `SELECT count(*) FROM requests`).Scan(&n)
			if n > 0 || !demo.WaitReady(ctx, base) {
				return
			}
			demo.Traffic(ctx, base, toks, ups, a.Log)
		})
	}
	if c.Server.MetricsTokenFile != "" {
		b, err := os.ReadFile(c.Server.MetricsTokenFile)
		if err != nil {
			return fail(fmt.Errorf("metrics token file: %w", err))
		}
		a.metricsT = credentials.NewSecret(strings.TrimSpace(string(b)))
	}
	a.AddBackground(a.authJanitor)
	if c.Demo.Enabled {
		if err := demo.Seed(ctx, a.DB, a.Auth); err != nil {
			return fail(err)
		}
		a.Log.Info("demo.ready", "admin_email", demo.AdminEmail, "admin_password", "see https://aurimasniekis.github.io/go-harbor-pilot/guide/demo")
	}
	a.apiRegs = append(a.apiRegs, a.CoreAPI()...)
	return a, nil
}

// CoreAPI returns the always-present control-plane registrations. It only
// captures a, so it can also be used to render the OpenAPI document without
// live dependencies.
func (a *App) CoreAPI() []func(*api.API) {
	return []func(*api.API){
		func(ap *api.API) { authapi.Register(ap, a.Auth, a.DB) },
		a.registerSystem,
		a.registerAttention,
		a.registerAccountChecks,
		func(ap *api.API) {
			cpapi.Register(ap, func() *controlplane.Service { return a.CP }, func() string { return a.Cfg.PublicOrigin() })
		},
		func(ap *api.API) {
			routingapi.Register(ap, routingapi.Deps{CP: func() *controlplane.Service { return a.CP },
				Health: func(id string) routing.Health {
					if a.Gateway == nil {
						return routing.Health{Available: true}
					}
					return a.Gateway.AccountHealth(id)
				},
				UpstreamFor: func(t string, p ir.Protocol) ir.Protocol {
					if a.Gateway == nil {
						return httpapi.UpstreamProtocol(t, p)
					}
					return a.Gateway.UpstreamProtocol(t, p)
				}})
		},
		func(ap *api.API) {
			quotaapi.Register(ap, quotaapi.Deps{Quota: func() *quota.Service { return a.Quota }, Budgets: a.budgets(),
				DB: func() *store.DB { return a.DB }, CP: func() *controlplane.Service { return a.CP }, Clock: a.clock(),
				Health: func(id string) routing.Health {
					if a.Gateway == nil {
						return routing.Health{Available: true}
					}
					return a.Gateway.AccountHealth(id)
				}, Origin: func() string { return a.Cfg.PublicOrigin() }})
			usageapi.Register(ap, usageapi.Deps{DB: func() *store.DB { return a.DB }, Snapshot: func() *controlplane.Snapshot { return a.CP.Snapshot() }, Clock: a.clock()})
		},
		func(ap *api.API) {
			statsapi.Register(ap, statsapi.Deps{DB: func() *store.DB { return a.DB }, Snapshot: func() *controlplane.Snapshot { return a.CP.Snapshot() }, Clock: a.clock()})
		},
		func(ap *api.API) {
			eventsapi.Register(ap, eventsapi.Deps{DB: func() *store.DB { return a.DB }, Relay: func() *relay.Relay { return a.Relay },
				Notify: func() *notify.Engine { return a.Notify }, Snapshot: func() *controlplane.Snapshot { return a.CP.Snapshot() }})
		},
		func(ap *api.API) {
			compatapi.Register(ap, compatapi.Deps{Compat: func() *compat.Manager { return a.Compat }, Watch: func() *compat.Watch { return a.Watch },
				Snapshot: func() *controlplane.Snapshot { return a.CP.Snapshot() }, Main: func() *store.DB { return a.DB },
				Probe: func(ctx context.Context, providerID, path string) (*ir.Error, error) {
					return a.Gateway.Probe(ctx, providerID, path)
				}})
		},
		func(ap *api.API) {
			tracesapi.Register(ap, tracesapi.Deps{Now: func() time.Time { return a.Clock.Now() }, Traces: func() *traces.Service { return a.TraceSvc }, Main: func() *store.DB { return a.DB },
				Snapshot: func() *controlplane.Snapshot { return a.CP.Snapshot() }})
		},
		func(ap *api.API) {
			privacyapi.Register(ap, func() *privacy.Service { return a.Privacy }, func() *controlplane.Snapshot { return a.CP.Snapshot() })
		},
		func(ap *api.API) {
			activityapi.Register(ap, func() *store.DB { return a.DB }, func() int64 {
				if a.Tel == nil {
					return 0
				}
				return a.Tel.Dropped()
			})
		},
	}
}

func (a *App) budgets() *accounting.Budgets {
	if a.Budgets != nil {
		return a.Budgets
	}
	return &accounting.Budgets{Snapshot: func() *controlplane.Snapshot { return nil }}
}

func (a *App) clock() clock.Clock {
	if a.Clock != nil {
		return a.Clock
	}
	return clock.Real{}
}

// AddBackground registers a worker started by Run.
func (a *App) AddBackground(fn func(ctx context.Context)) { a.bg = append(a.bg, fn) }

// AddRoutes registers additional top-level routes (gateway data plane).
func (a *App) AddRoutes(fn func(r chi.Router)) { a.routes = append(a.routes, fn) }

// AddAPI registers control-plane operations.
func (a *App) AddAPI(fn func(a *api.API)) { a.apiRegs = append(a.apiRegs, fn) }

// Close releases resources.
func (a *App) Close() {
	for _, c := range a.closers {
		c()
	}
	if a.Traces != nil {
		_ = a.Traces.Close()
	}
	if a.DB != nil {
		_ = a.DB.Close()
	}
	if a.unlock != nil {
		a.unlock()
	}
}

func (a *App) authJanitor(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if err := a.Auth.PurgeExpired(ctx); err != nil && ctx.Err() == nil {
			a.Log.Warn("auth.purge.failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Handler builds the root HTTP handler.
func (a *App) Handler() http.Handler {
	trusted, _ := netpolicy.ParseCIDRs(a.Cfg.Server.TrustedProxies)
	r := chi.NewRouter()
	r.Use(api.RequestID, api.Recoverer(a.Log), reqmeta.Middleware(trusted), api.AccessLog(a.Log, a.Metrics))
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	r.Get("/readyz", a.readyz)
	r.Get("/metrics", a.metrics)
	for _, fn := range a.routes {
		fn(r)
	}
	r.Group(func(r chi.Router) {
		r.Use(api.SecurityHeaders)
		r.Route(api.Prefix, func(r chi.Router) {
			r.Use(api.NoStore, a.Auth.Middleware)
			BuildAPI(r, a.Version, a.apiRegs)
			r.NotFound(func(w http.ResponseWriter, r *http.Request) {
				WriteNotFound(w, r)
			})
			r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
				api2Error(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			})
		})
		r.Handle("/*", web.Handler())
	})
	return r
}

// BuildAPI mounts every registered operation on r.
func BuildAPI(r chi.Router, version string, regs []func(a *api.API)) *api.API {
	a := api.New(r, version)
	for _, fn := range regs {
		fn(a)
	}
	return a
}

// WriteNotFound writes the not-found envelope.
func WriteNotFound(w http.ResponseWriter, r *http.Request) {
	api2Error(w, r, http.StatusNotFound, "not_found", "unknown API route")
}

func api2Error(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	api.WriteError(w, r, api.Errorf(status, code, msg))
}

func (a *App) readyz(w http.ResponseWriter, r *http.Request) {
	a.readyMu.RLock()
	ready := a.ready
	a.readyMu.RUnlock()
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if !ready || a.DB.Ping(ctx) != nil || a.Traces.Ping(ctx) != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"not_ready"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ready"}`))
}

func (a *App) metrics(w http.ResponseWriter, r *http.Request) {
	if !a.metricsT.IsZero() {
		tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(tok), []byte(a.metricsT.Reveal())) != 1 {
			api.WriteError(w, r, api.Unauthorized())
			return
		}
	} else if !directLoopback(r, len(a.Cfg.Server.TrustedProxies) > 0) {
		api.WriteError(w, r, api.Forbidden("metrics require server.metrics_token_file except for direct loopback clients"))
		return
	}
	promhttp.HandlerFor(a.Metrics.Registry, promhttp.HandlerOpts{}).ServeHTTP(w, r)
}

// directLoopback reports whether a request comes straight from a loopback
// client. Behind a reverse proxy on the same host every client looks like
// loopback, so forwarded requests never qualify, and with trusted proxies
// configured a token is always required.
func directLoopback(r *http.Request, behindProxy bool) bool {
	if behindProxy || r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("Forwarded") != "" || r.Header.Get("X-Real-Ip") != "" {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.Unmap().IsLoopback()
}

// MarkReady flips the readiness probe.
func (a *App) MarkReady() {
	a.readyMu.Lock()
	a.ready = true
	a.readyMu.Unlock()
}

// StartBackground starts every registered background worker.
func (a *App) StartBackground(ctx context.Context) {
	for _, fn := range a.bg {
		a.bgWG.Add(1)
		go func() { defer a.bgWG.Done(); fn(ctx) }()
	}
}

// WaitBackground waits for background workers to stop.
func (a *App) WaitBackground() { a.bgWG.Wait() }

// Run serves HTTP until ctx is cancelled, then shuts down gracefully.
func (a *App) Run(ctx context.Context) error {
	bgCtx, cancelBg := context.WithCancel(context.Background())
	a.StartBackground(bgCtx)
	wg := &a.bgWG
	srv := &http.Server{
		Addr:              a.Cfg.Server.Listen,
		Handler:           a.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          slog.NewLogLogger(a.Log.Handler(), slog.LevelWarn),
	}
	ln, err := net.Listen("tcp", a.Cfg.Server.Listen)
	if err != nil {
		cancelBg()
		wg.Wait()
		return err
	}
	a.readyMu.Lock()
	a.ready = true
	a.readyMu.Unlock()
	a.Log.Info("server.started", "listen", ln.Addr().String(), "public_url", a.Cfg.Server.PublicURL, "version", a.Version,
		"demo", a.Cfg.Demo.Enabled)
	if !a.Cfg.SecureCookies() {
		a.Log.Warn("server.insecure_cookies", "hint", "public_url is plain http; session cookies are not marked Secure. Use https in production.")
	}
	if req, err := a.Auth.SetupRequired(ctx); err == nil && req {
		a.Log.Warn("setup.required", "hint", "run `harbor-pilot admin bootstrap` to create a one-time setup link for the first administrator")
	}
	errCh := make(chan error, 1)
	go func() {
		if a.Cfg.Server.TLS.CertFile != "" {
			errCh <- srv.ServeTLS(ln, a.Cfg.Server.TLS.CertFile, a.Cfg.Server.TLS.KeyFile)
		} else {
			errCh <- srv.Serve(ln)
		}
	}()
	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			cancelBg()
			wg.Wait()
			return err
		}
	}
	a.readyMu.Lock()
	a.ready = false
	a.readyMu.Unlock()
	a.Log.Info("server.stopping")
	sctx, cancel := context.WithTimeout(context.Background(), a.Cfg.Server.ShutdownTimeout)
	defer cancel()
	err = srv.Shutdown(sctx)
	cancelBg()
	wg.Wait()
	a.Log.Info("server.stopped")
	return err
}
