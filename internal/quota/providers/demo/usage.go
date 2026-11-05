package demo

import (
	"fmt"
	"math"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/money"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
)

// demoModel is a synthetic model with synthetic prices (USD per million).
type demoModel struct {
	name, upstream    string
	share             float64
	inPrice, outPrice string
	cachePrice        string
	outPerReq         float64
	cacheShare        float64
}

// Synthetic model names and prices: illustrative demo data, not provider facts.
var demoModels = []demoModel{
	{name: "demo/sonnet-class", upstream: "anthropic", share: 0.52, inPrice: "3", outPrice: "15", cachePrice: "0.3", outPerReq: 1400, cacheShare: 0.45},
	{name: "demo/gpt-mini-class", upstream: "openai", share: 0.26, inPrice: "0.25", outPrice: "2", cachePrice: "0.025", outPerReq: 900, cacheShare: 0.2},
	{name: "demo/gemini-flash-class", upstream: "google", share: 0.16, inPrice: "0.3", outPrice: "2.5", cachePrice: "0.03", outPerReq: 800, cacheShare: 0.1},
	{name: "demo/codestral-class", upstream: "mistral", share: 0.06, inPrice: "0.3", outPrice: "0.9", cachePrice: "", outPerReq: 600, cacheShare: 0},
}

// requestScale converts activity into requests/hour per demo nexos account.
var requestScale = map[domain.AccountID]float64{
	"nexos-prod-agents": 105, "nexos-research": 45, "nexos-sandbox": 10,
}

// Workloads attributed by client instrumentation on production-agents.
var demoWorkloads = []struct {
	name, agent string
	share       float64
}{
	{"repository-maintenance", "claude-code", 0.45}, {"code-review", "codex-cli", 0.33}, {"ci-triage", "custom-agent", 0.22},
}

// DemoPrices returns the synthetic price records used to estimate costs of
// instrumented demo events.
func DemoPrices(effective time.Time) []domain.PriceRecord {
	var out []domain.PriceRecord
	for _, m := range demoModels {
		for cat, p := range map[string]string{"input": m.inPrice, "output": m.outPrice, "cache_read": m.cachePrice} {
			if p == "" {
				continue
			}
			a, _ := money.ParseDecimal(p)
			out = append(out, domain.PriceRecord{ProviderID: ProviderNexos, Model: m.name, Category: cat, PerMillion: a,
				Currency: "USD", EffectiveFrom: effective, Source: "demo"})
		}
	}
	return out
}

// KeyIDFor is the demo provider key ID of a demo nexos account.
func KeyIDFor(id domain.AccountID) string { return "demo-key-" + string(id) }

type modelHour struct {
	m                   demoModel
	req, in, out, cache int64
	cost                money.Amount
}

func price(s string) money.Amount {
	if s == "" {
		return 0
	}
	a, _ := money.ParseDecimal(s)
	return a
}

// hourUsage computes the deterministic per-model usage of an account hour.
func (s *Simulator) hourUsage(sc scenario, hour time.Time) []modelHour {
	scale, ok := requestScale[sc.id]
	if !ok {
		return nil
	}
	act := s.activity(sc, hour.Add(30*time.Minute))
	off := hour.Sub(s.start)
	spiking := sc.spikeExtra > 0 && off >= sc.spikeFrom-time.Hour && off < sc.spikeTo
	var out []modelHour
	for i, m := range demoModels {
		share := m.share
		outPer := m.outPerReq
		if spiking {
			// The spike is dominated by one workload on the sonnet-class model
			// with unusually long outputs.
			if i == 0 {
				share = 0.78
				outPer *= 1.7
			} else {
				share *= 0.45
			}
		}
		n := unit(sc.seed+uint64(i)*31, hour.Unix()/3600)
		req := int64(math.Round(scale * act * share * (0.85 + 0.3*n)))
		if req <= 0 {
			continue
		}
		in := req * int64(5200+1800*n)
		cache := int64(float64(in) * m.cacheShare)
		outT := int64(float64(req) * outPer * (0.8 + 0.4*n))
		// Demo semantics: cache reads are reported separately from input.
		c1, _ := money.CostForTokens(in, price(m.inPrice))
		c2, _ := money.CostForTokens(outT, price(m.outPrice))
		c3, _ := money.CostForTokens(cache, price(m.cachePrice))
		cost, _ := money.Sum(c1, c2, c3)
		out = append(out, modelHour{m: m, req: req, in: in, out: outT, cache: cache, cost: cost})
	}
	return out
}

func mk(v int64) *int64 { return &v }

func hourAgg(breakdown string, d domain.UsageDimensions, hour, now time.Time, req, in, out, cache int64, cost money.Amount) domain.UsageAggregate {
	return domain.UsageAggregate{
		Origin: domain.OriginDemo, Breakdown: breakdown, Dimensions: d, IntervalStart: hour, IntervalEnd: hour.Add(time.Hour),
		// nexos does not report error counts, so the demo leaves them unknown.
		Granularity: domain.GranHour, Requests: mk(req),
		Tokens:     domain.TokenCounts{Input: mk(in), Output: mk(out), CacheRead: mk(cache)},
		ActualCost: &money.Money{Amount: cost, Currency: "USD"}, CurrencyVerified: true,
		Partial: now.Before(hour.Add(time.Hour)), Quality: domain.QualityDemo, Demo: true,
	}
}

// UsageAggregates returns provider-style hourly aggregates (total, model,
// api_key breakdowns) for [from, to) for a demo nexos account.
func (s *Simulator) UsageAggregates(id domain.AccountID, from, to, now time.Time) []domain.UsageAggregate {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.sims[id]
	if !ok {
		return nil
	}
	var out []domain.UsageAggregate
	for h := from.Truncate(time.Hour); h.Before(to); h = h.Add(time.Hour) {
		if h.After(now) {
			break
		}
		mh := s.hourUsage(a.sc, h)
		if len(mh) == 0 {
			continue
		}
		frac := 1.0
		if now.Before(h.Add(time.Hour)) {
			frac = float64(now.Sub(h)) / float64(time.Hour)
		}
		var tr, ti, to2, tc int64
		var tcost money.Amount
		for _, m := range mh {
			r, i, o, c := scale(m.req, frac), scale(m.in, frac), scale(m.out, frac), scale(m.cache, frac)
			cost := money.Amount(float64(m.cost) * frac)
			tr, ti, to2, tc = tr+r, ti+i, to2+o, tc+c
			tcost, _ = money.Add(tcost, cost)
			out = append(out, hourAgg("model", domain.UsageDimensions{RequestedModel: m.m.name, UpstreamProvider: m.m.upstream}, h, now, r, i, o, c, cost))
		}
		out = append(out, hourAgg("total", domain.UsageDimensions{}, h, now, tr, ti, to2, tc, tcost))
		out = append(out, hourAgg("api_key", domain.UsageDimensions{APIKeyID: KeyIDFor(id)}, h, now, tr, ti, to2, tc, tcost))
	}
	return out
}

func scale(v int64, f float64) int64 { return int64(math.Round(float64(v) * f)) }

// ClientEvents returns instrumented per-request events for production-agents
// in [from, to) observed up to now. Instrumentation covers ~80% of requests
// so part of the provider-reported usage stays unattributed.
func (s *Simulator) ClientEvents(from, to, now time.Time) []domain.UsageEvent {
	const id domain.AccountID = "nexos-prod-agents"
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.sims[id]
	if !ok {
		return nil
	}
	var out []domain.UsageEvent
	for h := from.Truncate(time.Hour); h.Before(to); h = h.Add(time.Hour) {
		off := h.Sub(s.start)
		spiking := off >= a.sc.spikeFrom-time.Hour && off < a.sc.spikeTo
		for mi, m := range s.hourUsage(a.sc, h) {
			covered := int64(math.Round(float64(m.req) * 0.8))
			for k := int64(0); k < covered; k++ {
				seq := uint64(h.Unix()/3600)*1000 + uint64(mi)*100 + uint64(k)
				u := unit(9001, int64(seq))
				at := h.Add(time.Duration(u * float64(time.Hour)))
				if at.After(now) || at.Before(from) || !at.Before(to) {
					continue
				}
				wl := demoWorkloads[0]
				pick := unit(4242, int64(seq))
				acc := 0.0
				for _, w := range demoWorkloads {
					acc += w.share
					if pick < acc {
						wl = w
						break
					}
				}
				if spiking && mi == 0 {
					wl = demoWorkloads[0]
				}
				in := m.in / m.req
				outT := m.out / m.req
				cache := m.cache / m.req
				lat := int64(700 + 5000*unit(77, int64(seq)))
				ttft := lat / 5
				outcome := domain.OutcomeSuccess
				errRate := 0.02
				if spiking {
					errRate = 0.07
				}
				var status *int
				retry := false
				if unit(99, int64(seq)) < errRate {
					outcome = domain.OutcomeError
					code := 529
					status = &code
					retry = true
				} else {
					code := 200
					status = &code
				}
				started := at.Add(-time.Duration(lat) * time.Millisecond)
				ev := domain.UsageEvent{
					SourceID: "ingest:demo-instrumentation", IdempotencyKey: fmt.Sprintf("demo-%d", seq),
					OperationID: fmt.Sprintf("op-%d", seq/3), AttemptID: fmt.Sprintf("att-%d", seq),
					StartedAt: &started, EndedAt: &at, ObservedAt: at, ProviderID: ProviderNexos, AccountID: id,
					Dimensions: domain.UsageDimensions{APIKeyID: KeyIDFor(id), RequestedModel: m.m.name, ResolvedModel: m.m.name,
						UpstreamProvider: m.m.upstream, Agent: wl.agent, Workload: wl.name, Environment: "production"},
					Outcome: outcome, HTTPStatus: status, Tokens: domain.TokenCounts{Input: mk(in), Output: mk(outT), CacheRead: mk(cache)},
					LatencyMS: mk(lat), TTFTMS: mk(ttft), Origin: domain.OriginClientReported, Quality: domain.QualityClientReported,
					Retry: retry, Demo: true, Streaming: true,
				}
				out = append(out, ev)
			}
		}
	}
	return out
}

// APIKeys returns demo key metadata for a nexos account.
func (s *Simulator) APIKeys(id domain.AccountID, now time.Time) []domain.APIKeyRecord {
	sc, ok := s.sims[id]
	if !ok || sc.sc.provider != ProviderNexos {
		return nil
	}
	st := "active"
	res, _ := s.observe(id, now)
	var used, lim *money.Amount
	if res != nil {
		for _, b := range res.Buckets {
			if b.Spec.Key == "budget:key" {
				used, lim = b.UsedAmount, b.LimitAmount
				if b.Remaining != nil && *b.Remaining <= 0 {
					st = "budget_exceeded"
				}
			}
		}
	}
	created := s.start.Add(-90 * 24 * time.Hour)
	last := now.Add(-3 * time.Minute)
	return []domain.APIKeyRecord{{
		ProviderKeyID: KeyIDFor(id), Name: sc.sc.alias, Status: &st, Project: "Platform team", CreatedAt: &created,
		ProviderLastUsedAt: &last, BudgetUsed: used, BudgetLimit: lim, BudgetCurrency: "EUR",
	}}
}
