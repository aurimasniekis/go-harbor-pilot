package forecasting

import (
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/analytics"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
)

var t0 = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

func fp(v float64) *float64     { return &v }
func tp(t time.Time) *time.Time { return &t }

func rate(v float64) *Rate {
	return &Rate{PerHour: v, Unit: "pp/h", Source: "test", Confidence: domain.ConfidenceHigh}
}

func spec(sem domain.ResetSemantics, period time.Duration) domain.BucketSpec {
	return domain.BucketSpec{Unit: domain.UnitPercent, Kind: domain.KindFixedWindow, Window: 5 * time.Hour, ResetSemantics: sem, Period: period}
}

func TestResetRestoresDifferenceNotFull100(t *testing.T) {
	pts := []analytics.Point{{T: t0, Remaining: fp(40), Limit: fp(100), ResetAt: tp(t0.Add(2 * time.Hour))}}
	r := Forecast(spec(domain.ResetProviderTimestamp, 0), pts, t0, rate(10), Options{Horizon: 6 * time.Hour})
	if r.NextResetAt == nil || !r.NextResetAt.Equal(t0.Add(2*time.Hour)) {
		t.Fatalf("next reset = %v", r.NextResetAt)
	}
	// 40 - 2h*10 = 20 remaining before reset; restored 80, not 100.
	if r.RestoredAmount == nil || *r.RestoredAmount != 80 {
		t.Fatalf("restored = %v", r.RestoredAmount)
	}
	if r.ExhaustionAt != nil {
		t.Fatalf("no exhaustion expected within horizon, got %v", r.ExhaustionAt)
	}
	// The reset produces a vertical step: two points at the reset instant.
	var atReset []Point
	for _, p := range r.Points {
		if p.T.Equal(t0.Add(2 * time.Hour)) {
			atReset = append(atReset, p)
		}
	}
	if len(atReset) < 2 || atReset[len(atReset)-1].Remaining != 100 {
		t.Fatalf("reset step points: %+v", atReset)
	}
	for _, c := range r.Caveats {
		if c == "" {
			t.Fatal("empty caveat")
		}
	}
}

func TestZeroFloorAndDowntimeUntilReset(t *testing.T) {
	pts := []analytics.Point{{T: t0, Remaining: fp(15), Limit: fp(100), ResetAt: tp(t0.Add(3 * time.Hour))}}
	r := Forecast(spec(domain.ResetProviderTimestamp, 0), pts, t0, rate(10), Options{Horizon: 5 * time.Hour})
	if r.ExhaustionAt == nil || !r.ExhaustionAt.Equal(t0.Add(90*time.Minute)) {
		t.Fatalf("exhaustion = %v", r.ExhaustionAt)
	}
	if !r.ExhaustsBeforeReset {
		t.Fatal("should exhaust before the reset")
	}
	if len(r.Downtime) != 1 || r.Downtime[0].To == nil || !r.Downtime[0].To.Equal(t0.Add(3*time.Hour)) {
		t.Fatalf("downtime: %+v", r.Downtime)
	}
	for _, p := range r.Points {
		if p.Remaining < 0 || p.Low < 0 || p.High < 0 {
			t.Fatalf("negative remaining at %v: %+v", p.T, p)
		}
		if p.Remaining > 100 || p.High > 100 {
			t.Fatalf("capacity ceiling exceeded: %+v", p)
		}
	}
	if *r.RestoredAmount != 100 {
		t.Fatalf("restored after exhaustion = %v", *r.RestoredAmount)
	}
}

func TestRepeatedExhaustionAndRecoveryWithPeriodicResets(t *testing.T) {
	pts := []analytics.Point{{T: t0, Remaining: fp(50), Limit: fp(100), ResetAt: tp(t0.Add(time.Hour))}}
	r := Forecast(spec(domain.ResetFixedPeriodic, 5*time.Hour), pts, t0, rate(40), Options{Horizon: 24 * time.Hour})
	var resets, exh int
	for _, e := range r.Events {
		switch e.Kind {
		case "reset":
			resets++
		case "exhaustion":
			exh++
		}
	}
	// Resets at 1h, 6h, 11h, 16h, 21h; each window exhausts after 2.5h.
	if resets != 5 || exh != 5 {
		t.Fatalf("resets=%d exhaustions=%d events=%+v", resets, exh, r.Events)
	}
	if len(r.Downtime) != 5 || r.Downtime[4].To != nil {
		t.Fatalf("downtime intervals: %+v", r.Downtime)
	}
}

func TestProviderTimestampDoesNotInventLaterResets(t *testing.T) {
	pts := []analytics.Point{{T: t0, Remaining: fp(50), Limit: fp(100), ResetAt: tp(t0.Add(time.Hour))}}
	r := Forecast(spec(domain.ResetProviderTimestamp, 5*time.Hour), pts, t0, rate(40), Options{Horizon: 24 * time.Hour})
	resets := 0
	for _, e := range r.Events {
		if e.Kind == "reset" {
			resets++
		}
	}
	if resets != 1 {
		t.Fatalf("invented resets: %d", resets)
	}
	if len(r.Downtime) != 1 || r.Downtime[0].To != nil {
		t.Fatalf("downtime end must be unknown: %+v", r.Downtime)
	}
}

func TestRollingWindowLimitedForecast(t *testing.T) {
	s := domain.BucketSpec{Unit: domain.UnitPercent, Kind: domain.KindRollingWindow, Window: 5 * time.Hour, ResetSemantics: domain.ResetRolling}
	pts := []analytics.Point{{T: t0, Remaining: fp(80), Limit: fp(100)}}
	r := Forecast(s, pts, t0, rate(5), Options{Horizon: 72 * time.Hour})
	if r.Method != MethodLimitedRolling || r.Confidence != domain.ConfidenceLow || len(r.Caveats) == 0 {
		t.Fatalf("rolling forecast: %+v", r)
	}
	if last := r.Points[len(r.Points)-1]; last.T.After(t0.Add(5 * time.Hour)) {
		t.Fatalf("rolling forecast exceeds one window: %v", last.T)
	}
}

func TestUnavailableCases(t *testing.T) {
	if r := Forecast(spec(domain.ResetUnknown, 0), nil, t0, rate(1), Options{}); r.Unavailable == nil {
		t.Fatal("no observations must be unavailable")
	}
	pts := []analytics.Point{{T: t0.Add(-100 * time.Hour), Remaining: fp(50)}}
	if r := Forecast(spec(domain.ResetUnknown, 0), pts, t0, rate(1), Options{Horizon: 72 * time.Hour}); r.Unavailable == nil {
		t.Fatal("ancient observation must be unavailable")
	}
	if r := Forecast(spec(domain.ResetUnknown, 0), []analytics.Point{{T: t0, Remaining: fp(50)}}, t0, nil, Options{}); r.Unavailable == nil {
		t.Fatal("unknown rate must be unavailable")
	}
}

func TestStaleStartIsFlagged(t *testing.T) {
	pts := []analytics.Point{{T: t0.Add(-3 * time.Hour), Remaining: fp(50), Limit: fp(100)}}
	r := Forecast(spec(domain.ResetUnknown, 0), pts, t0, rate(1), Options{StaleAfter: 30 * time.Minute})
	if !r.Stale || r.Confidence != domain.ConfidenceMedium || !r.Start.Equal(t0.Add(-3*time.Hour)) {
		t.Fatalf("stale forecast: %+v", r)
	}
}
