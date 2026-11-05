package demo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers"
)

var demoStart = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

func fetch(t *testing.T, sim *Simulator, id domain.AccountID, at time.Time) (*providers.FetchResult, error) {
	t.Helper()
	var acct domain.Account
	for _, a := range sim.Accounts() {
		if a.ID == id {
			acct = a
		}
	}
	if acct.ID == "" {
		t.Fatalf("no demo account %s", id)
	}
	for _, ad := range NewAdapters(sim) {
		if ad.Info().ID == acct.ProviderID {
			return ad.Fetch(context.Background(), providers.FetchRequest{Config: providers.AccountConfig{Account: acct}, Now: at})
		}
	}
	t.Fatalf("no adapter for %s", acct.ProviderID)
	return nil, nil
}

func bucket(t *testing.T, r *providers.FetchResult, key string) domain.BucketObservation {
	t.Helper()
	for _, b := range r.Buckets {
		if b.Spec.Key == key {
			return b
		}
	}
	t.Fatalf("bucket %s not found", key)
	return domain.BucketObservation{}
}

func TestDemoScenarios(t *testing.T) {
	sim := NewSimulator(demoStart)
	t.Run("seven day exhausted with full five hour", func(t *testing.T) {
		r, err := fetch(t, sim, "claude-work-3", demoStart)
		if err != nil {
			t.Fatal(err)
		}
		if got := *bucket(t, r, "seven_day").Remaining; got != 0 {
			t.Fatalf("7d remaining = %v, want 0", got)
		}
		if got := *bucket(t, r, "five_hour").Remaining; got != 100 {
			t.Fatalf("5h remaining = %v, want 100", got)
		}
		if got := *bucket(t, r, "seven_day:model:opus").Remaining; got != 100 {
			t.Fatalf("opus remaining = %v, want 100", got)
		}
	})
	t.Run("burn spike lowers five hour quickly", func(t *testing.T) {
		before, err := fetch(t, sim, "claude-work-2", demoStart.Add(-60*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		now, err := fetch(t, sim, "claude-work-2", demoStart)
		if err != nil {
			t.Fatal(err)
		}
		drop := *bucket(t, before, "five_hour").Remaining - *bucket(t, now, "five_hour").Remaining
		if drop < 12 {
			t.Fatalf("spike drop over last hour = %.1f pp, want >= 12", drop)
		}
	})
	t.Run("auth failure", func(t *testing.T) {
		_, err := fetch(t, sim, "claude-work-4", demoStart)
		var pe *providers.Error
		if !errors.As(err, &pe) || pe.Kind != domain.ErrAuthentication {
			t.Fatalf("want auth error, got %v", err)
		}
		if _, err := fetch(t, sim, "claude-work-4", demoStart.Add(-6*time.Hour)); err != nil {
			t.Fatalf("history before failure should succeed: %v", err)
		}
	})
	t.Run("stale account errors recently", func(t *testing.T) {
		if _, err := fetch(t, sim, "claude-work-5", demoStart); err == nil {
			t.Fatal("want error")
		}
	})
	t.Run("shared team budget identical for both members", func(t *testing.T) {
		a, err := fetch(t, sim, "nexos-prod-agents", demoStart)
		if err != nil {
			t.Fatal(err)
		}
		b, err := fetch(t, sim, "nexos-research", demoStart)
		if err != nil {
			t.Fatal(err)
		}
		ta, tb := bucket(t, a, "budget:team"), bucket(t, b, "budget:team")
		if *ta.Used != *tb.Used || ta.Spec.SharedScopeID == "" || ta.Spec.SharedScopeID != tb.Spec.SharedScopeID {
			t.Fatalf("shared budget mismatch: %v vs %v", *ta.Used, *tb.Used)
		}
	})
	t.Run("balance is currency and never percent", func(t *testing.T) {
		r, err := fetch(t, sim, "deepseek-dev", demoStart)
		if err != nil {
			t.Fatal(err)
		}
		b := bucket(t, r, "balance")
		if b.Spec.Unit != domain.UnitCurrency || b.RemainingAmount == nil || *b.Remaining <= 0 || b.Limit != nil {
			t.Fatalf("unexpected balance observation: %+v", b)
		}
	})
	t.Run("ci runner repeatedly exhausts", func(t *testing.T) {
		exhausted := 0
		for i := range 48 {
			r, err := fetch(t, sim, "codex-ci", demoStart.Add(-time.Duration(i)*30*time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if *bucket(t, r, "five_hour").Remaining == 0 {
				exhausted++
			}
		}
		if exhausted == 0 {
			t.Fatal("expected some exhausted observations")
		}
	})
}

func TestDemoDeterministic(t *testing.T) {
	a := NewSimulator(demoStart)
	b := NewSimulator(demoStart)
	at := demoStart.Add(-37 * time.Hour)
	for _, acct := range a.Accounts() {
		ra, ea := fetch(t, a, acct.ID, at)
		rb, eb := fetch(t, b, acct.ID, at)
		if (ea == nil) != (eb == nil) {
			t.Fatalf("%s: error mismatch", acct.ID)
		}
		if ea != nil {
			continue
		}
		for i := range ra.Buckets {
			x, y := ra.Buckets[i], rb.Buckets[i]
			if (x.Remaining == nil) != (y.Remaining == nil) || (x.Remaining != nil && *x.Remaining != *y.Remaining) {
				t.Fatalf("%s/%s not deterministic", acct.ID, x.Spec.Key)
			}
		}
	}
}

func TestDemoSnapshot(t *testing.T) {
	sim := NewSimulator(demoStart)
	for _, acct := range sim.Accounts() {
		r, err := fetch(t, sim, acct.ID, demoStart)
		if err != nil {
			t.Logf("%-20s error: %v", acct.ID, err)
			continue
		}
		for _, b := range r.Buckets {
			var rem float64
			if b.Remaining != nil {
				rem = *b.Remaining
			}
			t.Logf("%-20s %-22s remaining=%8.2f reset=%v", acct.ID, b.Spec.Key, rem, b.ResetAt)
		}
	}
}
