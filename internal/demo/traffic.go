package demo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/synthetic"
)

// Upstreams are the synthetic providers served under /_demo/upstream/.
type Upstreams struct {
	Anthropic *synthetic.Upstream
	OpenAI    *synthetic.Upstream
	Local     *synthetic.Upstream
}

// NewUpstreams creates deterministic synthetic providers.
func NewUpstreams() *Upstreams {
	u := &Upstreams{Anthropic: synthetic.New("anthropic"), OpenAI: synthetic.New("openai"), Local: synthetic.New("openai")}
	// Delays give the activity views realistic time-to-first-token figures.
	u.Anthropic.Default = synthetic.Reply{Text: "[synthetic] I reviewed the change and it looks correct.", InputTok: 1800, OutputTok: 240, CacheRead: 12000,
		Delay: 420 * time.Millisecond}
	u.OpenAI.Default = synthetic.Reply{Text: "[synthetic] Done.", InputTok: 900, OutputTok: 120, CacheRead: 2000, Delay: 260 * time.Millisecond}
	u.Local.Default = synthetic.Reply{Text: "[synthetic] Local model answer.", InputTok: 400, OutputTok: 80, Delay: 90 * time.Millisecond}
	return u
}

// Routes mounts the synthetic upstreams (demo mode only).
func (u *Upstreams) Routes(r chi.Router) {
	r.Mount("/_demo/upstream/anthropic", http.StripPrefix("/_demo/upstream/anthropic", u.Anthropic))
	r.Mount("/_demo/upstream/openai", http.StripPrefix("/_demo/upstream/openai", u.OpenAI))
	r.Mount("/_demo/upstream/local", http.StripPrefix("/_demo/upstream/local", u.Local))
}

// Tokens holds demo gateway tokens (synthetic, demo.db only).
type Tokens map[string]string

func (s *seeder) create(kind cp.Kind, name string, spec any, secrets cp.SecretInput) string {
	if s.err != nil {
		return ""
	}
	if o, err := s.cp.Get(s.ctx, kind, name); err == nil {
		return o.ID
	}
	raw, _ := json.Marshal(spec)
	o, err := s.cp.Create(s.ctx, kind, name, raw, secrets, cp.Mutation{ManagedBy: cp.ManagedDemo})
	if err != nil {
		s.err = fmt.Errorf("demo %s %s: %w", kind.KindName(), name, err)
		return ""
	}
	return o.ID
}

type seeder struct {
	ctx context.Context
	cp  *cp.Service
	err error
}

func secret(v string) *string { return &v }

// SeedConfig creates the synthetic configuration (idempotent) and returns
// demo gateway tokens. origin is the instance's public origin.
func SeedConfig(ctx context.Context, svc *cp.Service, origin string) (Tokens, error) {
	ctx = auth.WithPrincipal(ctx, auth.System)
	s := &seeder{ctx: ctx, cp: svc}
	ws := s.create(cp.WorkspaceDef, "acme", cp.WorkspaceSpec{DisplayName: "Acme (synthetic)", Description: "Synthetic demo workspace"}, nil)
	web := s.create(cp.ProjectDef, "web-app", cp.ProjectSpec{WorkspaceID: ws, DisplayName: "Web app", ExternalRefs: map[string]string{"git.repo": "example.test/acme/web"}}, nil)
	data := s.create(cp.ProjectDef, "data-pipeline", cp.ProjectSpec{WorkspaceID: ws, DisplayName: "Data pipeline", ExternalRefs: map[string]string{}}, nil)
	price := func(in, out, cache string) *cp.ModelPricing {
		return &cp.ModelPricing{Currency: "USD", InputPerMTok: in, OutputPerMTok: out, CacheReadPerMTok: cache, PriceVersion: "demo-2026-11"}
	}
	ant := s.create(cp.ProviderDef, "anthropic-demo", cp.ProviderSpec{Type: cp.ProviderAnthropic, DisplayName: "Anthropic (synthetic)",
		BaseURL: origin + "/_demo/upstream/anthropic", AllowPrivateNetwork: true, Models: []cp.ModelSpec{
			{ID: "demo-sonnet", DisplayName: "Demo Sonnet", ContextWindow: 200000, MaxOutputTokens: 16000, Capabilities: []string{"streaming", "tools", "parallel_tools", "vision", "reasoning", "prompt_cache"}, Pricing: price("3", "15", "0.3")},
			{ID: "demo-haiku", DisplayName: "Demo Haiku", ContextWindow: 200000, MaxOutputTokens: 8000, Capabilities: []string{"streaming", "tools", "prompt_cache"}, Pricing: price("1", "5", "0.1")},
		}}, nil)
	oai := s.create(cp.ProviderDef, "openai-demo", cp.ProviderSpec{Type: cp.ProviderOpenAI, DisplayName: "OpenAI (synthetic)",
		BaseURL: origin + "/_demo/upstream/openai", AllowPrivateNetwork: true, Models: []cp.ModelSpec{
			{ID: "demo-gpt", DisplayName: "Demo GPT", ContextWindow: 400000, MaxOutputTokens: 64000, Capabilities: []string{"streaming", "tools", "parallel_tools", "reasoning", "structured_output"}, Pricing: price("1.25", "10", "0.125")},
			{ID: "demo-gpt-mini", DisplayName: "Demo GPT mini", ContextWindow: 400000, MaxOutputTokens: 32000, Capabilities: []string{"streaming", "tools", "structured_output"}, Pricing: price("0.25", "2", "0.025")},
		}}, nil)
	local := s.create(cp.ProviderDef, "local-demo", cp.ProviderSpec{Type: cp.ProviderOpenAICompatible, DisplayName: "Local model (synthetic)",
		BaseURL: origin + "/_demo/upstream/local", AllowPrivateNetwork: true, Classification: "local", Models: []cp.ModelSpec{
			{ID: "demo-local-coder", DisplayName: "Demo local coder", ContextWindow: 32000, MaxOutputTokens: 4000, Capabilities: []string{"streaming", "tools"}},
		}}, nil)
	aAnt1 := s.create(cp.AccountDef, "anthropic-team-1", cp.AccountSpec{ProviderID: ant, DisplayName: "Team key 1", Billing: cp.BillingMetered, Enabled: true, MaxConcurrency: 8, Labels: map[string]string{"tier": "team"}}, cp.SecretInput{"credential": secret("demo-synthetic-key-1")})
	aAnt2 := s.create(cp.AccountDef, "anthropic-team-2", cp.AccountSpec{ProviderID: ant, DisplayName: "Team key 2", Billing: cp.BillingMetered, Enabled: true, MaxConcurrency: 8, Labels: map[string]string{"tier": "team"}}, cp.SecretInput{"credential": secret("demo-synthetic-key-2")})
	aOAI := s.create(cp.AccountDef, "openai-team", cp.AccountSpec{ProviderID: oai, DisplayName: "OpenAI team", Billing: cp.BillingMetered, Enabled: true, MaxConcurrency: 8, Labels: map[string]string{}}, cp.SecretInput{"credential": secret("demo-synthetic-key-3")})
	aLocal := s.create(cp.AccountDef, "workstation-gpu", cp.AccountSpec{ProviderID: local, DisplayName: "Workstation GPU", Billing: cp.BillingLocal, Enabled: true, MaxConcurrency: 2, Labels: map[string]string{}}, cp.SecretInput{"credential": secret("none")})
	s.create(cp.EntitlementDef, "anthropic-team-1-plan", cp.EntitlementSpec{AccountID: aAnt1, PlanName: "Synthetic team plan", Fee: "0", Currency: "USD", Period: "monthly", StartsOn: "2026-01-01", Windows: []cp.WindowSpec{}}, nil)
	pDefault := s.create(cp.PoolDef, "coding-default", cp.PoolSpec{DisplayName: "Coding default", Strategy: "ordered", Affinity: "session", Members: []cp.PoolMember{
		{AccountID: aAnt1, Model: "demo-sonnet", Priority: 0}, {AccountID: aAnt2, Model: "demo-sonnet", Priority: 1}, {AccountID: aOAI, Model: "demo-gpt", Priority: 2}}}, nil)
	pFast := s.create(cp.PoolDef, "coding-fast", cp.PoolSpec{DisplayName: "Coding fast", Strategy: "ordered", Affinity: "session", Members: []cp.PoolMember{
		{AccountID: aOAI, Model: "demo-gpt-mini", Priority: 0}, {AccountID: aAnt1, Model: "demo-haiku", Priority: 1}}}, nil)
	pLocal := s.create(cp.PoolDef, "local-private", cp.PoolSpec{DisplayName: "Local only", Strategy: "ordered", Affinity: "none", Members: []cp.PoolMember{
		{AccountID: aLocal, Model: "demo-local-coder", Priority: 0}}}, nil)
	alDefault := s.create(cp.AliasDef, "coding/default", cp.AliasSpec{Description: "Default coding model", PoolID: pDefault}, nil)
	alFast := s.create(cp.AliasDef, "coding/fast", cp.AliasSpec{Description: "Fast, cheap coding model", PoolID: pFast}, nil)
	s.create(cp.AliasDef, "coding/deep", cp.AliasSpec{Description: "Deep reasoning", AliasID: alDefault, RequiredCapabilities: []string{"reasoning"}}, nil)
	s.create(cp.AliasDef, "review/default", cp.AliasSpec{Description: "Code review", AliasID: alDefault}, nil)
	alLocal := s.create(cp.AliasDef, "local/private", cp.AliasSpec{Description: "Never leaves the workstation", PoolID: pLocal}, nil)
	plan := s.create(cp.PlanDef, "team-default", cp.PlanSpec{DisplayName: "Team default", Description: "Synthetic plan with paid fallback allowed"}, nil)
	private := s.create(cp.PlanDef, "private-only", cp.PlanSpec{DisplayName: "Private only", Description: "Local models only"}, nil)
	// Privacy and trace policies for the private plan: mask secrets and
	// personal data, keep masked traces for three days.
	privacy := s.create(cp.PrivacyPolicyDef, "mask-secrets", cp.PrivacyPolicySpec{Description: "Mask credentials and personal data before dispatch",
		PersonalData: []string{"email", "phone"}, RestoreInResponses: true}, nil)
	traces := s.create(cp.TracePolicyDef, "three-day-masked", cp.TracePolicySpec{Mode: cp.TraceMaskedFull, Retention: "3d"}, nil)
	// Event and notification routing. Everything is disabled: the demo
	// never contacts an outside endpoint.
	s.create(cp.DestinationDef, "audit-webhook", cp.DestinationSpec{Type: "webhook", Enabled: false, WebhookURL: "https://hooks.example.test/harbor-pilot",
		Filter: cp.EventFilter{Events: []string{"routing.*", "quota.*", "budget.*"}, MinSeverity: "info"}}, nil)
	ops := s.create(cp.ChannelDef, "ops-webhook", cp.ChannelSpec{Type: "webhook", Enabled: false, WebhookURL: "https://hooks.example.test/ops"}, nil)
	s.create(cp.RuleDef, "quota-and-budget-alerts", cp.NotificationRuleSpec{Enabled: false, ChannelIDs: []string{ops}, Recovery: true, CooldownSeconds: 1800,
		Filter: cp.EventFilter{Events: []string{"quota.*", "budget.*", "provider.account.*"}, MinSeverity: "warning"}}, nil)
	if s.err != nil {
		return nil, s.err
	}
	publish := func(planID string, doc cp.PlanDocument) error {
		if snap := svc.Snapshot(); snap.Plans[planID] != nil && snap.Plans[planID].Published != nil {
			return nil
		}
		raw, _ := json.Marshal(doc)
		v, err := svc.SaveDraft(ctx, planID, "", raw, "synthetic demo version", cp.Mutation{ManagedBy: cp.ManagedDemo})
		if err != nil {
			return err
		}
		_, err = svc.Publish(ctx, planID, v.ID, cp.Mutation{ManagedBy: cp.ManagedDemo})
		return err
	}
	if err := publish(plan, cp.PlanDocument{ModelMappings: []cp.ModelMapping{{Match: "claude-*", AliasID: alDefault}, {Match: "gpt-*", AliasID: alFast}},
		DefaultAliasID: alDefault, Fallback: cp.FallbackPolicy{Steps: []string{cp.StepAlternateAccount, cp.StepAlternateModel, cp.StepAlternateProvider, cp.StepStop},
			MaxAttempts: 3, AllowPaidFallback: true}}); err != nil {
		return nil, err
	}
	if err := publish(private, cp.PlanDocument{DefaultAliasID: alLocal, Restrictions: cp.PlanRestrictions{AllowedClassifications: []string{"local"}},
		Privacy:  cp.PlanPrivacy{PolicyID: privacy, TracePolicyID: traces},
		Fallback: cp.FallbackPolicy{Steps: []string{cp.StepStop}, MaxAttempts: 1}}); err != nil {
		return nil, err
	}
	bLaptop := s.create(cp.BindingDef, "claude-code-laptop", cp.BindingSpec{DisplayName: "Claude Code on a laptop", PlanID: plan, ProjectID: web,
		ExpectedHarness: "claude_code", Trust: cp.BindingTrust{SessionHints: true}, Enabled: true}, nil)
	bCI := s.create(cp.BindingDef, "codex-ci", cp.BindingSpec{DisplayName: "Codex in CI", PlanID: plan, ProjectID: data, ExpectedHarness: "codex",
		Limits: cp.BindingLimits{MaxConcurrency: 4}, Enabled: true, ExternalRefs: map[string]string{"ci.provider": "synthetic-ci"}}, nil)
	bSpace := s.create(cp.BindingDef, "spaceman-runs", cp.BindingSpec{DisplayName: "SpaceMan runs", PlanID: plan, ProjectID: web, ExpectedHarness: "claude_code",
		Enabled: true, ExternalRefs: map[string]string{"spaceman.space": "spc_synthetic"}}, nil)
	bPrivate := s.create(cp.BindingDef, "private-notebook", cp.BindingSpec{DisplayName: "Private notebook", PlanID: private, ExpectedHarness: "generic", Enabled: true}, nil)
	_ = alFast
	// Quota monitors read the synthetic quota simulator. Two feed routing
	// health of the team keys; the others show board states (exhausted
	// weekly window, failing credential, prepaid balance, team budgets).
	mon := func(name, adapter, scenario, account string) {
		s.create(cp.QuotaMonitorDef, name, cp.QuotaMonitorSpec{Adapter: adapter, AccountID: account, Enabled: true, PollIntervalSecs: 300,
			Options: map[string]any{"scenario": scenario}}, nil)
	}
	mon("team-1-subscription", "demo_claude", "claude-main", aAnt1)
	mon("team-2-subscription", "demo_claude", "claude-work-2", aAnt2)
	mon("weekly-exhausted", "demo_claude", "claude-work-3", "")
	mon("expired-credential", "demo_claude", "claude-work-4", "")
	mon("codex-team", "demo_codex", "codex-team", "")
	mon("agents-budget", "demo_nexos", "nexos-prod-agents", aOAI)
	mon("dev-balance", "demo_deepseek", "deepseek-dev", "")
	s.create(cp.BudgetDef, "acme-monthly", cp.BudgetSpec{Scope: cp.BudgetWorkspace, ScopeID: ws, Period: "month", Limit: "50", Currency: "USD",
		Enforcement: "warn", WarnAt: 0.8, Enabled: true}, nil)
	s.create(cp.BudgetDef, "ci-daily", cp.BudgetSpec{Scope: cp.BudgetBinding, ScopeID: bCI, Period: "day", Limit: "2", Currency: "USD",
		Enforcement: "block", WarnAt: 0.8, Enabled: true}, nil)
	if s.err != nil {
		return nil, s.err
	}
	toks := Tokens{}
	for name, id := range map[string]string{"laptop": bLaptop, "ci": bCI, "spaceman": bSpace, "private": bPrivate} {
		scopes := []string{cp.ScopeInvoke, cp.ScopeModels}
		if name == "spaceman" {
			scopes = append(scopes, cp.ScopeMint)
		}
		_, tok, err := svc.IssueCredential(ctx, id, cp.CredentialInput{Name: "demo " + name, Scopes: scopes, ManagedBy: cp.ManagedDemo})
		if err != nil {
			return nil, err
		}
		toks[name] = tok
	}
	return toks, nil
}

// Traffic drives a deterministic set of requests through the real gateway.
func Traffic(ctx context.Context, base string, toks Tokens, ups *Upstreams, log *slog.Logger) {
	post := func(path, tok string, body any, hdr ...string) {
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tok)
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Warn("demo.traffic.failed", "error", err)
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	tool := []any{map[string]any{"name": "run_tests", "description": "Run the test suite", "input_schema": map[string]any{"type": "object", "properties": map[string]any{"pattern": map[string]any{"type": "string"}}}}}
	claude := func(session string, n int, stream bool, tools bool) {
		body := map[string]any{"model": "claude-sonnet-4-5", "max_tokens": 4096, "stream": stream,
			"messages": []any{map[string]any{"role": "user", "content": fmt.Sprintf("[synthetic] task %d", n)}}}
		if tools {
			body["tools"] = tool
		}
		post("/v1/messages", toks["laptop"], body, "User-Agent", "claude-cli/2.1.318 (external, cli)", "X-Claude-Code-Session-Id", session)
	}
	for i := range 6 {
		claude("demo-session-a", i, true, i%2 == 0)
	}
	ups.Anthropic.Push(synthetic.Reply{ToolName: "run_tests", ToolArgs: `{"pattern":"./..."}`, InputTok: 2100, OutputTok: 60, CacheRead: 14000})
	claude("demo-session-b", 1, true, true)
	// Rate-limited primary: the plan allows switching accounts, then models.
	ups.Anthropic.Push(synthetic.Reply{Status: 429, RetryAfter: 30})
	claude("demo-session-b", 2, true, false)
	ups.Anthropic.Push(synthetic.Reply{Status: 429, RetryAfter: 30}, synthetic.Reply{Status: 529})
	claude("demo-session-c", 1, false, false)
	for i := range 4 {
		post("/v1/responses", toks["ci"], map[string]any{"model": "gpt-5-codex", "stream": true, "input": fmt.Sprintf("[synthetic] CI job %d", i),
			"tools": []any{map[string]any{"type": "function", "name": "shell", "parameters": map[string]any{"type": "object"}}}},
			"User-Agent", "codex_cli_rs/0.166.0 (synthetic)")
	}
	// Two orchestrator runs, each with its own run token carrying a trusted
	// spaceman.run reference, so usage can be filtered and grouped per run.
	runTok := func(run string) string {
		raw, _ := json.Marshal(map[string]any{"name": "run " + run, "scopes": []string{cp.ScopeInvoke, cp.ScopeModels}, "ttl_seconds": 24 * 3600,
			"bound_refs": map[string]string{"spaceman.run": run}})
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/harbor/credentials", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+toks["spaceman"])
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Warn("demo.traffic.failed", "error", err)
			return toks["spaceman"]
		}
		defer resp.Body.Close()
		var out struct {
			Token string `json:"token"`
		}
		if resp.StatusCode != http.StatusCreated || json.NewDecoder(resp.Body).Decode(&out) != nil || out.Token == "" {
			log.Warn("demo.traffic.failed", "status", resp.StatusCode)
			return toks["spaceman"]
		}
		return out.Token
	}
	runs := map[string]string{"run_synthetic_1": runTok("run_synthetic_1"), "run_synthetic_2": runTok("run_synthetic_2")}
	for i, run := range []string{"run_synthetic_1", "run_synthetic_1", "run_synthetic_2"} {
		post("/v1/messages", runs[run], map[string]any{"model": "coding/deep", "max_tokens": 8000, "stream": true,
			"messages": []any{map[string]any{"role": "user", "content": fmt.Sprintf("[synthetic] run step %d", i)}}},
			"User-Agent", "claude-cli/2.1.318 (external, sdk-cli)", "X-HP-External-Ref", run)
	}
	for i := range 2 {
		post("/v1/chat/completions", toks["private"], map[string]any{"model": "anything", "messages": []any{map[string]any{"role": "user", "content": fmt.Sprintf("[synthetic] private %d", i)}}})
	}
	log.Info("demo.traffic.done")
}

// WaitReady polls base/readyz until ready or ctx ends.
func WaitReady(ctx context.Context, base string) bool {
	for range 100 {
		resp, err := http.Get(base + "/readyz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return true
			}
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(100 * time.Millisecond):
		}
	}
	return false
}
