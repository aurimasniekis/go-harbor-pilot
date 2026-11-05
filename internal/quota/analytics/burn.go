package analytics

import (
	"math"
	"sort"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
)

// Burn methods.
const (
	MethodGross        = "gross_depletion"
	MethodNet          = "net_depletion"
	MethodCounter      = "counter"
	MethodInsufficient = "insufficient_data"
)

// RateUnit returns the explicit unit of a burn rate for a bucket. Percent
// buckets burn in percentage points per hour ("pp/h"), never "%/h".
func RateUnit(spec domain.BucketSpec) string {
	switch spec.Unit {
	case domain.UnitPercent:
		return "pp/h"
	case domain.UnitTokens:
		return "tokens/h"
	case domain.UnitRequests:
		return "requests/h"
	case domain.UnitCurrency:
		if spec.Currency != "" {
			return spec.Currency + "/h"
		}
		return "currency/h"
	case domain.UnitCredits:
		return "credits/h"
	default:
		return "units/h"
	}
}

// Method returns the burn method for a bucket.
func Method(spec domain.BucketSpec) string {
	if spec.Kind == domain.KindRollingWindow {
		return MethodNet
	}
	if spec.Kind == domain.KindBudget {
		return MethodCounter
	}
	return MethodGross
}

// slot accumulates depletion and observed time.
type slot struct {
	depletion float64
	covered   time.Duration
}

// distribute spreads each in-segment pair's depletion over fixed-size slots
// in [from, to). Gross methods count only decreases; the net method counts
// signed changes (replenishment offsets usage).
func distribute(segs []Segment, method string, from, to time.Time, size time.Duration) map[int64]*slot {
	out := map[int64]*slot{}
	for _, s := range segs {
		for i := 1; i < len(s.Points); i++ {
			a, b := s.Points[i-1], s.Points[i]
			if !b.T.After(from) || !a.T.Before(to) {
				continue
			}
			av, _ := a.value()
			bv, _ := b.value()
			d := av - bv
			if method != MethodNet && d < 0 {
				d = 0
			}
			span := b.T.Sub(a.T)
			if span <= 0 {
				continue
			}
			start, end := a.T, b.T
			if start.Before(from) {
				start = from
			}
			if end.After(to) {
				end = to
			}
			for t := start; t.Before(end); {
				k := t.Truncate(size).UnixNano()
				next := time.Unix(0, k).Add(size)
				if next.After(end) {
					next = end
				}
				part := next.Sub(t)
				sl := out[k]
				if sl == nil {
					sl = &slot{}
					out[k] = sl
				}
				sl.covered += part
				sl.depletion += d * float64(part) / float64(span)
				t = next
			}
		}
	}
	return out
}

// BurnOptions configure ComputeBurn.
type BurnOptions struct {
	Horizon     time.Duration
	Segment     SegmentOptions
	MinCoverage float64
}

// BurnResult is a burn rate over a horizon.
type BurnResult struct {
	Rate       *float64
	Unit       string
	Method     string
	Coverage   float64
	Confidence domain.Confidence
	Samples    int
	Depletion  float64
	Covered    time.Duration
	// Segments counts segments overlapping the horizon (resets, gaps).
	Segments int
}

// ComputeBurn computes depletion per hour over [now-horizon, now].
// Insufficient coverage yields Method insufficient_data and a nil rate.
func ComputeBurn(pts []Point, spec domain.BucketSpec, now time.Time, o BurnOptions) BurnResult {
	if o.MinCoverage <= 0 {
		o.MinCoverage = 0.5
	}
	res := BurnResult{Unit: RateUnit(spec), Method: Method(spec), Confidence: domain.ConfidenceNone}
	from := now.Add(-o.Horizon)
	prep := Prepare(pts)
	segs := Split(prep.Points, spec, o.Segment)
	for _, s := range segs {
		if len(s.Points) > 0 && s.Points[len(s.Points)-1].T.After(from) && !s.Points[0].T.After(now) {
			res.Segments++
		}
		for _, p := range s.Points {
			if p.T.After(from) && !p.T.After(now) {
				res.Samples++
			}
		}
	}
	slots := distribute(segs, res.Method, from, now, o.Horizon)
	for _, s := range slots {
		res.Depletion += s.depletion
		res.Covered += s.covered
	}
	res.Coverage = float64(res.Covered) / float64(o.Horizon)
	if res.Coverage < o.MinCoverage || res.Samples < 2 || res.Covered <= 0 {
		res.Method = MethodInsufficient
		return res
	}
	rate := res.Depletion / res.Covered.Hours()
	if rate < 0 {
		// Net replenishment exceeded usage; usage cannot be negative.
		rate = 0
	}
	res.Rate = &rate
	switch {
	case res.Coverage >= 0.9:
		res.Confidence = domain.ConfidenceHigh
	case res.Coverage >= 0.65:
		res.Confidence = domain.ConfidenceMedium
	default:
		res.Confidence = domain.ConfidenceLow
	}
	if res.Method == MethodNet {
		// Rolling windows only reveal net depletion, never gross usage.
		res.Confidence = domain.ConfidenceLow
	}
	return res
}

// HourlyRates returns per-hour burn samples in [from, to) for hours with at
// least minCoverage observed time.
func HourlyRates(pts []Point, spec domain.BucketSpec, from, to time.Time, seg SegmentOptions, minCoverage float64) []float64 {
	prep := Prepare(pts)
	segs := Split(prep.Points, spec, seg)
	slots := distribute(segs, Method(spec), from, to, time.Hour)
	keys := make([]int64, 0, len(slots))
	for k := range slots {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	var out []float64
	for _, k := range keys {
		s := slots[k]
		if s.covered.Hours() < minCoverage {
			continue
		}
		r := s.depletion / s.covered.Hours()
		if r < 0 {
			r = 0
		}
		out = append(out, r)
	}
	return out
}

// Median of values (copying).
func Median(v []float64) float64 {
	if len(v) == 0 {
		return math.NaN()
	}
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	n := len(c)
	if n%2 == 1 {
		return c[n/2]
	}
	return (c[n/2-1] + c[n/2]) / 2
}

// MAD is the median absolute deviation from the median.
func MAD(v []float64) float64 {
	if len(v) == 0 {
		return math.NaN()
	}
	m := Median(v)
	d := make([]float64, len(v))
	for i, x := range v {
		d[i] = math.Abs(x - m)
	}
	return Median(d)
}

// Quantile returns the q-quantile (0..1) using linear interpolation.
func Quantile(v []float64, q float64) float64 {
	if len(v) == 0 {
		return math.NaN()
	}
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	pos := q * float64(len(c)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return c[lo]
	}
	return c[lo] + (c[hi]-c[lo])*(pos-float64(lo))
}

// EWMA returns the exponentially weighted moving average (oldest first).
func EWMA(v []float64, alpha float64) float64 {
	if len(v) == 0 {
		return math.NaN()
	}
	s := v[0]
	for _, x := range v[1:] {
		s = alpha*x + (1-alpha)*s
	}
	return s
}

// HourlyRateMap returns per-hour burn keyed by hour start for hours with at
// least minCoverage observed (fraction of the hour).
func HourlyRateMap(pts []Point, spec domain.BucketSpec, from, to time.Time, seg SegmentOptions, minCoverage float64) map[time.Time]float64 {
	prep := Prepare(pts)
	segs := Split(prep.Points, spec, seg)
	slots := distribute(segs, Method(spec), from, to, time.Hour)
	out := make(map[time.Time]float64, len(slots))
	for k, s := range slots {
		if s.covered.Hours() < minCoverage {
			continue
		}
		r := s.depletion / s.covered.Hours()
		if r < 0 {
			r = 0
		}
		out[time.Unix(0, k).UTC()] = r
	}
	return out
}

// Resets returns the instants at which segments start because of a reset.
func Resets(pts []Point, spec domain.BucketSpec, seg SegmentOptions) []time.Time {
	prep := Prepare(pts)
	var out []time.Time
	for _, s := range Split(prep.Points, spec, seg) {
		if s.StartReason == ReasonReset && len(s.Points) > 0 {
			out = append(out, s.Points[0].T)
		}
	}
	return out
}
