package quota

import (
	"net/http"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
)

func fp(v float64) *float64 { return &v }

func TestParseHeaders(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	t.Run("anthropic", func(t *testing.T) {
		h := http.Header{}
		h.Set("anthropic-ratelimit-requests-limit", "50")
		h.Set("anthropic-ratelimit-requests-remaining", "10")
		h.Set("anthropic-ratelimit-requests-reset", "2026-11-05T12:00:30Z")
		h.Set("anthropic-ratelimit-output-tokens-limit", "8000")
		h.Set("anthropic-ratelimit-output-tokens-remaining", "garbage")
		obs := parseAnthropicHeaders(h, "m1", now)
		if len(obs) != 2 {
			t.Fatalf("got %d observations", len(obs))
		}
		r := obs[0]
		if r.Spec.Key != "rl:requests:model:m1" || *r.Limit != 50 || *r.Remaining != 10 || *r.Used != 40 || r.ResetAt == nil || r.ResetAt.Sub(now) != 30*time.Second {
			t.Fatalf("requests bucket %+v", r)
		}
		if o := obs[1]; o.Remaining != nil || o.Used != nil || *o.Limit != 8000 {
			t.Fatal("unparseable remaining must stay unknown, not zero")
		}
		if f := r.RemainingFraction(); f == nil || *f != 0.2 {
			t.Fatalf("fraction %v", f)
		}
	})
	t.Run("openai", func(t *testing.T) {
		h := http.Header{}
		h.Set("x-ratelimit-limit-tokens", "1000")
		h.Set("x-ratelimit-remaining-tokens", "250")
		h.Set("x-ratelimit-reset-tokens", "6m0s")
		obs := parseOpenAIHeaders(h, "", now)
		if len(obs) != 1 || obs[0].Spec.Key != "rl:tokens" || obs[0].ResetAt.Sub(now) != 6*time.Minute {
			t.Fatalf("%+v", obs)
		}
	})
	t.Run("none", func(t *testing.T) {
		if obs := parseOpenAIHeaders(http.Header{}, "", now); len(obs) != 0 {
			t.Fatal("no headers must give no observations")
		}
	})
}

func TestBoardHealth(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	future, past := now.Add(time.Hour), now.Add(-time.Minute)
	global := domain.BucketSpec{Key: "five_hour", Label: "5-hour window", Global: true, Unit: domain.UnitPercent, Kind: domain.KindFixedWindow}
	model := domain.BucketSpec{Key: "weekly:opus", Unit: domain.UnitPercent, Kind: domain.KindFixedWindow, Model: "opus"}
	mk := func(spec domain.BucketSpec, rem *float64, reset *time.Time, age time.Duration) bucketState {
		return bucketState{owner: "qmn_1", monitorID: "qmn_1", accountID: "acc_1", source: sourceAdapter,
			obs: domain.BucketObservation{Spec: spec, Limit: fp(100), Remaining: rem, ResetAt: reset}, observedAt: now.Add(-age)}
	}
	tests := []struct {
		name      string
		below     float64
		buckets   []bucketState
		available bool
		remaining *float64
		stale     bool
	}{
		{"healthy", 0, []bucketState{mk(global, fp(60), &future, time.Minute)}, true, fp(0.6), false},
		{"exhausted until reset", 0, []bucketState{mk(global, fp(0), &future, time.Minute)}, false, fp(0), false},
		{"reset passed means unknown, not exhausted", 0, []bucketState{mk(global, fp(0), &past, time.Minute)}, true, nil, false},
		{"threshold", 0.1, []bucketState{mk(global, fp(5), &future, time.Minute)}, false, fp(0.05), false},
		{"model bucket never blocks the account", 0, []bucketState{mk(model, fp(0), &future, time.Minute)}, true, fp(0), false},
		{"unknown remaining", 0, []bucketState{mk(global, nil, &future, time.Minute)}, true, nil, false},
		{"stale observations do not decide", 0, []bucketState{mk(global, fp(0), &future, 2*time.Hour)}, true, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBoard()
			b.setLinks(map[string]monitorLink{"qmn_1": {accountID: "acc_1", exhaustedBelow: tt.below, staleAfter: 30 * time.Minute}})
			b.replaceOwner("qmn_1", tt.buckets)
			h := b.health("acc_1", now)
			if h.Available != tt.available || h.Stale != tt.stale {
				t.Fatalf("health %+v", h)
			}
			if (h.Remaining == nil) != (tt.remaining == nil) || (h.Remaining != nil && *h.Remaining != *tt.remaining) {
				t.Fatalf("remaining %v want %v", h.Remaining, tt.remaining)
			}
			if !h.Available && h.Until == nil {
				t.Fatal("exhaustion must carry the reset time")
			}
		})
	}
	t.Run("unlinked account", func(t *testing.T) {
		if h := newBoard().health("acc_x", now); !h.Available || h.Remaining != nil {
			t.Fatalf("%+v", h)
		}
	})
}
