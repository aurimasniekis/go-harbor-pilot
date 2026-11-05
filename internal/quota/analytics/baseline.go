package analytics

import (
	"math"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
)

// Baseline states.
const (
	BaselineLearning = "learning"
	BaselineReady    = "ready"
)

// BaselineOptions configure robust baseline estimation.
type BaselineOptions struct {
	Lookback time.Duration
	// Exclude is the most recent window left out so a candidate anomaly
	// never inflates its own baseline.
	Exclude          time.Duration
	MinSamples       int
	MinCoverageHours float64
	// MinHourCoverage is the fraction of an hour that must be observed for
	// the hour to count as a sample.
	MinHourCoverage float64
	// MinBaselineRate guards ratios against near-zero baselines (bucket
	// units per hour). Zero means derive from the bucket unit/limit.
	MinBaselineRate float64
	// MADFloor guards robust z-scores against zero MAD (bucket units/hour).
	MADFloor float64
	Segment  SegmentOptions
}

func (o *BaselineOptions) defaults() {
	if o.Lookback <= 0 {
		o.Lookback = 14 * 24 * time.Hour
	}
	if o.Exclude <= 0 {
		o.Exclude = 2 * time.Hour
	}
	if o.MinSamples <= 0 {
		o.MinSamples = 24
	}
	if o.MinCoverageHours <= 0 {
		o.MinCoverageHours = 24
	}
	if o.MinHourCoverage <= 0 {
		o.MinHourCoverage = 0.5
	}
}

// Baseline is a robust summary of historical hourly burn.
type Baseline struct {
	State         string
	Median        *float64
	MAD           *float64
	P75           *float64
	Samples       int
	CoverageHours float64
	// MinRate and MADFloor are the guards used for comparisons.
	MinRate  float64
	MADFloor float64
}

// MinRateFor derives a minimum meaningful baseline rate for a bucket.
func MinRateFor(spec domain.BucketSpec, limit *float64) float64 {
	if spec.Unit == domain.UnitPercent {
		return 0.5 // pp/h
	}
	if limit != nil && *limit > 0 {
		return *limit * 0.005
	}
	return 0
}

// ComputeBaseline estimates the hourly burn distribution over the lookback,
// excluding the most recent window.
func ComputeBaseline(pts []Point, spec domain.BucketSpec, now time.Time, o BaselineOptions) Baseline {
	o.defaults()
	to := now.Add(-o.Exclude).Truncate(time.Hour)
	from := now.Add(-o.Lookback).Truncate(time.Hour)
	samples := HourlyRates(pts, spec, from, to, o.Segment, o.MinHourCoverage)
	var limit *float64
	for i := len(pts) - 1; i >= 0; i-- {
		if pts[i].Limit != nil {
			limit = pts[i].Limit
			break
		}
	}
	b := Baseline{Samples: len(samples), CoverageHours: float64(len(samples)), State: BaselineLearning}
	b.MinRate = o.MinBaselineRate
	if b.MinRate <= 0 {
		b.MinRate = MinRateFor(spec, limit)
	}
	if len(samples) == 0 {
		return b
	}
	med, mad, p75 := Median(samples), MAD(samples), Quantile(samples, 0.75)
	b.Median, b.MAD, b.P75 = &med, &mad, &p75
	b.MADFloor = math.Max(o.MADFloor, math.Max(0.1*med, b.MinRate*0.5))
	if b.Samples >= o.MinSamples && b.CoverageHours >= o.MinCoverageHours {
		b.State = BaselineReady
	}
	return b
}

// Deviation compares a rate with the baseline.
type Deviation struct {
	Ratio   *float64
	RobustZ *float64
}

// Compare returns guarded ratio and robust z-score. Both are nil while the
// baseline is learning or no meaningful minimum rate is known.
func (b Baseline) Compare(rate float64) Deviation {
	if b.State != BaselineReady || b.Median == nil {
		return Deviation{}
	}
	var d Deviation
	if b.MinRate > 0 {
		r := rate / math.Max(*b.Median, b.MinRate)
		d.Ratio = &r
	}
	scale := 1.4826 * math.Max(*b.MAD, b.MADFloor)
	if scale > 0 {
		z := (rate - *b.Median) / scale
		d.RobustZ = &z
	}
	return d
}
