package analytics

import (
	"math"
	"testing"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
)

var t0 = time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)

func fp(v float64) *float64 { return &v }

func tp(t time.Time) *time.Time { return &t }

// series builds observations every step starting at t0 with given values.
func series(step time.Duration, vals ...float64) []Point {
	out := make([]Point, len(vals))
	for i, v := range vals {
		out[i] = Point{T: t0.Add(time.Duration(i) * step), Remaining: fp(v), Limit: fp(100)}
	}
	return out
}

var fixed5h = domain.BucketSpec{Key: "five_hour", Unit: domain.UnitPercent, Kind: domain.KindFixedWindow, Window: 5 * time.Hour,
	ResetSemantics: domain.ResetProviderTimestamp}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestBurnUnitsArePercentagePoints(t *testing.T) {
	pts := series(15*time.Minute, 100, 97, 94, 91, 88) // 12 pp over 1h
	b := ComputeBurn(pts, fixed5h, t0.Add(time.Hour), BurnOptions{Horizon: time.Hour})
	if b.Unit != "pp/h" || b.Rate == nil || !near(*b.Rate, 12) || b.Method != MethodGross {
		t.Fatalf("burn = %+v rate=%v", b, b.Rate)
	}
	if b.Confidence != domain.ConfidenceHigh {
		t.Fatalf("confidence = %s", b.Confidence)
	}
}

func TestBurnIgnoresFixedReset(t *testing.T) {
	// 60 → 50 then reset to 100 → 90: depletion is 10 + 10 = 20 over 3 intervals, never -40.
	pts := series(20*time.Minute, 60, 50, 100, 90)
	pts[0].ResetAt = tp(t0.Add(30 * time.Minute))
	pts[1].ResetAt = tp(t0.Add(30 * time.Minute))
	b := ComputeBurn(pts, fixed5h, t0.Add(time.Hour), BurnOptions{Horizon: time.Hour})
	if b.Rate == nil || !near(*b.Rate, 20/(40.0/60)) {
		t.Fatalf("rate across reset = %v (segments %d)", b.Rate, b.Segments)
	}
	segs := Split(Prepare(pts).Points, fixed5h, SegmentOptions{})
	if len(segs) != 2 || segs[1].StartReason != ReasonReset {
		t.Fatalf("segments: %+v", segs)
	}
}

func TestRollingWindowIsNetDepletionLowConfidence(t *testing.T) {
	spec := domain.BucketSpec{Unit: domain.UnitPercent, Kind: domain.KindRollingWindow, Window: 5 * time.Hour, ResetSemantics: domain.ResetRolling}
	// Usage 10, replenishment 4, usage 6: net 12 over 1h.
	pts := series(20*time.Minute, 80, 70, 74, 68)
	b := ComputeBurn(pts, spec, t0.Add(time.Hour), BurnOptions{Horizon: time.Hour})
	if b.Method != MethodNet || b.Confidence != domain.ConfidenceLow || b.Rate == nil || !near(*b.Rate, 12) {
		t.Fatalf("rolling burn = %+v rate=%v", b, b.Rate)
	}
	if len(Split(Prepare(pts).Points, spec, SegmentOptions{})) != 1 {
		t.Fatal("rolling replenishment must not split segments")
	}
}

func TestCounterResetForBudgets(t *testing.T) {
	spec := domain.BucketSpec{Unit: domain.UnitCurrency, Currency: "EUR", Kind: domain.KindBudget, ResetSemantics: domain.ResetProviderTimestamp}
	mk := func(i int, used float64) Point {
		return Point{T: t0.Add(time.Duration(i) * 30 * time.Minute), Used: fp(used), Limit: fp(400)}
	}
	// Used grows 390 → 398, month resets to 0 → 6.
	pts := []Point{mk(0, 390), mk(1, 398), mk(2, 0), mk(3, 6)}
	b := ComputeBurn(pts, spec, t0.Add(90*time.Minute), BurnOptions{Horizon: 90 * time.Minute})
	if b.Unit != "EUR/h" || b.Rate == nil || !near(*b.Rate, 14/1.0) {
		t.Fatalf("budget burn = %+v rate=%v", b, b.Rate)
	}
}

func TestLimitChangeSplitsSegment(t *testing.T) {
	spec := domain.BucketSpec{Unit: domain.UnitTokens, Kind: domain.KindFixedWindow, ResetSemantics: domain.ResetUnknown}
	pts := []Point{
		{T: t0, Remaining: fp(900), Limit: fp(1000)},
		{T: t0.Add(30 * time.Minute), Remaining: fp(800), Limit: fp(1000)},
		{T: t0.Add(60 * time.Minute), Remaining: fp(1800), Limit: fp(2000)}, // plan upgrade
		{T: t0.Add(90 * time.Minute), Remaining: fp(1700), Limit: fp(2000)},
	}
	segs := Split(pts, spec, SegmentOptions{})
	if len(segs) != 2 || segs[1].StartReason != ReasonLimitChange {
		t.Fatalf("segments: %+v", segs)
	}
	b := ComputeBurn(pts, spec, t0.Add(90*time.Minute), BurnOptions{Horizon: 90 * time.Minute})
	if b.Rate == nil || !near(*b.Rate, 200/1.0) || b.Unit != "tokens/h" {
		t.Fatalf("burn across limit change = %v", b.Rate)
	}
}

func TestOutOfOrderAndDuplicates(t *testing.T) {
	pts := series(15*time.Minute, 100, 97, 94, 91, 88)
	shuffled := []Point{pts[2], pts[0], pts[4], pts[1], pts[3], pts[3]}
	p := Prepare(shuffled)
	if p.OutOfOrder == 0 || p.Duplicates != 1 || len(p.Points) != 5 {
		t.Fatalf("prepare: %+v", p)
	}
	b := ComputeBurn(shuffled, fixed5h, t0.Add(time.Hour), BurnOptions{Horizon: time.Hour})
	if b.Rate == nil || !near(*b.Rate, 12) {
		t.Fatalf("burn = %v", b.Rate)
	}
}

func TestStaleGapExcludedFromCoverage(t *testing.T) {
	pts := []Point{
		{T: t0, Remaining: fp(90)}, {T: t0.Add(10 * time.Minute), Remaining: fp(88)},
		// 3h gap: the 88→40 drop spans the gap and is not attributed to any rate.
		{T: t0.Add(190 * time.Minute), Remaining: fp(40)}, {T: t0.Add(200 * time.Minute), Remaining: fp(38)},
	}
	segs := Split(pts, fixed5h, SegmentOptions{GapThreshold: 30 * time.Minute})
	if len(segs) != 2 || segs[1].StartReason != ReasonGap {
		t.Fatalf("segments: %+v", segs)
	}
	b := ComputeBurn(pts, fixed5h, t0.Add(4*time.Hour), BurnOptions{Horizon: 4 * time.Hour})
	if b.Method != MethodInsufficient || b.Rate != nil {
		t.Fatalf("expected insufficient coverage, got %+v", b)
	}
}

func TestBaselineLearningAndGuards(t *testing.T) {
	now := t0.Add(48 * time.Hour)
	// Only 10 hours of history: learning.
	var short []Point
	for i := 0; i <= 10*4; i++ {
		short = append(short, Point{T: now.Add(-12*time.Hour + time.Duration(i)*15*time.Minute), Remaining: fp(100 - float64(i)*0.25), Limit: fp(100)})
	}
	b := ComputeBaseline(short, fixed5h, now, BaselineOptions{})
	if b.State != BaselineLearning || b.Compare(50).Ratio != nil {
		t.Fatalf("expected learning baseline without comparisons: %+v", b)
	}
	// 40 hours of completely idle usage: median 0 and MAD 0.
	var idle []Point
	for i := 0; i <= 40*4; i++ {
		idle = append(idle, Point{T: now.Add(-42*time.Hour + time.Duration(i)*15*time.Minute), Remaining: fp(100), Limit: fp(100)})
	}
	b = ComputeBaseline(idle, fixed5h, now, BaselineOptions{})
	if b.State != BaselineReady || *b.Median != 0 || *b.MAD != 0 {
		t.Fatalf("idle baseline: %+v", b)
	}
	d := b.Compare(1)
	// A tiny 1 pp/h blip against a zero baseline must not look like a huge spike.
	if d.Ratio == nil || *d.Ratio > 2.01 {
		t.Fatalf("ratio against zero baseline not guarded: %v", d.Ratio)
	}
	if d.RobustZ == nil || math.IsInf(*d.RobustZ, 0) || *d.RobustZ > 5 {
		t.Fatalf("robust z against zero MAD not guarded: %v", d.RobustZ)
	}
}

func TestBaselineExcludesCandidateWindow(t *testing.T) {
	now := t0.Add(72 * time.Hour)
	var pts []Point
	v := 100.0
	for i := 0; i <= 60*4; i++ {
		ts := now.Add(-60*time.Hour + time.Duration(i)*15*time.Minute)
		rate := 2.0 // pp/h
		if now.Sub(ts) <= 2*time.Hour {
			rate = 30 // spike in the last 2 hours
		}
		v -= rate / 4
		if v < 5 {
			v = 100
		}
		pts = append(pts, Point{T: ts, Remaining: fp(v), Limit: fp(100)})
	}
	b := ComputeBaseline(pts, fixed5h, now, BaselineOptions{Exclude: 2 * time.Hour})
	if b.Median == nil || *b.Median > 2.5 {
		t.Fatalf("spike leaked into baseline: median %v", b.Median)
	}
	d := b.Compare(30)
	if d.Ratio == nil || *d.Ratio < 10 {
		t.Fatalf("ratio = %v", d.Ratio)
	}
}
