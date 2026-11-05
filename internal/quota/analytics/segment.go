// Package analytics derives burn rates, robust baselines and aggregate
// views from bucket observation history.
//
// Remaining-quota series are split into segments at resets, limit changes,
// polling gaps and provider corrections, so rates are only ever computed
// between observations that belong to the same quota period and policy.
package analytics

import (
	"sort"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
)

// Point is one observation of a bucket.
type Point struct {
	T         time.Time
	Remaining *float64
	Used      *float64
	Limit     *float64
	ResetAt   *time.Time
}

// value returns remaining in bucket units (deriving from limit-used).
func (p Point) value() (float64, bool) {
	if p.Remaining != nil {
		return *p.Remaining, true
	}
	if p.Limit != nil && p.Used != nil {
		return *p.Limit - *p.Used, true
	}
	return 0, false
}

// Boundary reasons.
const (
	ReasonStart       = "start"
	ReasonReset       = "reset"
	ReasonLimitChange = "limit_change"
	ReasonGap         = "gap"
	ReasonCorrection  = "correction"
)

// Segment is a run of observations within one quota period and policy.
type Segment struct {
	Points []Point
	// StartReason explains why this segment began.
	StartReason string
}

// SegmentOptions tune boundary detection.
type SegmentOptions struct {
	// GapThreshold breaks segments when observations are further apart.
	GapThreshold time.Duration
	// ResetJump is the minimum upward move (bucket units) treated as a reset
	// when no reset timestamp explains it. Smaller upward moves are
	// corrections.
	ResetJump float64
}

// Prepared is a cleaned series.
type Prepared struct {
	Points []Point
	// OutOfOrder counts observations that arrived out of order (re-sorted).
	OutOfOrder int
	// Duplicates counts dropped duplicate timestamps.
	Duplicates int
}

// Prepare sorts by time, drops duplicate timestamps (keeping the last
// reported value) and points without a derivable value.
func Prepare(pts []Point) Prepared {
	var p Prepared
	cp := make([]Point, 0, len(pts))
	for i, x := range pts {
		if i > 0 && x.T.Before(pts[i-1].T) {
			p.OutOfOrder++
		}
		if _, ok := x.value(); !ok {
			continue
		}
		cp = append(cp, x)
	}
	sort.SliceStable(cp, func(i, j int) bool { return cp[i].T.Before(cp[j].T) })
	out := cp[:0]
	for _, x := range cp {
		if n := len(out); n > 0 && out[n-1].T.Equal(x.T) {
			out[n-1] = x
			p.Duplicates++
			continue
		}
		out = append(out, x)
	}
	p.Points = out
	return p
}

// Split segments a prepared series for the given bucket semantics.
func Split(pts []Point, spec domain.BucketSpec, o SegmentOptions) []Segment {
	if o.GapThreshold <= 0 {
		o.GapThreshold = 30 * time.Minute
	}
	if o.ResetJump <= 0 {
		o.ResetJump = defaultResetJump(spec, pts)
	}
	var segs []Segment
	cur := Segment{StartReason: ReasonStart}
	for i, c := range pts {
		if i == 0 {
			cur.Points = append(cur.Points, c)
			continue
		}
		p := pts[i-1]
		if reason := boundary(p, c, spec, o); reason != "" {
			segs = append(segs, cur)
			cur = Segment{StartReason: reason}
		}
		cur.Points = append(cur.Points, c)
	}
	if len(cur.Points) > 0 {
		segs = append(segs, cur)
	}
	return segs
}

func defaultResetJump(spec domain.BucketSpec, pts []Point) float64 {
	if spec.Unit == domain.UnitPercent {
		return 5
	}
	for _, p := range pts {
		if p.Limit != nil && *p.Limit > 0 {
			return *p.Limit * 0.05
		}
	}
	return 1e-9 // any upward move of an unbounded balance is a top-up
}

func boundary(p, c Point, spec domain.BucketSpec, o SegmentOptions) string {
	if c.T.Sub(p.T) > o.GapThreshold {
		return ReasonGap
	}
	if p.Limit != nil && c.Limit != nil && *p.Limit != *c.Limit {
		return ReasonLimitChange
	}
	pv, _ := p.value()
	cv, _ := c.value()
	if spec.Kind == domain.KindRollingWindow {
		// Increases are continuous replenishment, not resets.
		return ""
	}
	if p.ResetAt != nil && !c.T.Before(*p.ResetAt) {
		// The previously announced reset time has passed.
		return ReasonReset
	}
	if p.ResetAt != nil && c.ResetAt != nil && c.ResetAt.After(p.ResetAt.Add(time.Minute)) && cv > pv {
		return ReasonReset
	}
	if cv-pv >= o.ResetJump {
		return ReasonReset
	}
	if cv > pv {
		return ReasonCorrection
	}
	return ""
}
