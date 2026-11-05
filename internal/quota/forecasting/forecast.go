// Package forecasting projects bucket remaining quota forward in time using
// an explicit consumption rate and the known reset events, with a zero floor
// and a capacity ceiling. Future resets are only used when the provider
// reported them or documents a fixed period; they are never invented.
package forecasting

import (
	"encoding/json"
	"math"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/analytics"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
)

// Methods.
const (
	MethodEventSimulation = "event_simulation"
	MethodLimitedRolling  = "limited_rolling_window"
)

// Options configure a forecast.
type Options struct {
	Horizon     time.Duration
	Step        time.Duration
	OutputEvery time.Duration
	// StaleAfter: when the latest observation is older than this the
	// forecast is still produced from it but flagged stale.
	StaleAfter time.Duration
	// BandFactor sets the scenario band: rate × (1 ± BandFactor).
	BandFactor float64
}

func (o *Options) defaults() {
	if o.Horizon <= 0 {
		o.Horizon = 72 * time.Hour
	}
	if o.Step <= 0 {
		o.Step = 5 * time.Minute
	}
	if o.OutputEvery <= 0 {
		o.OutputEvery = 15 * time.Minute
	}
	if o.BandFactor <= 0 {
		o.BandFactor = 0.5
	}
}

// Rate is the assumed consumption rate.
type Rate struct {
	PerHour    float64           `json:"per_hour"`
	Unit       string            `json:"unit"`
	Source     string            `json:"source"` // e.g. "EWMA of hourly burn (6h)"
	Confidence domain.Confidence `json:"confidence"`
}

// Point is a forecast value. Low/High are the scenario band (high
// consumption gives the low remaining value).
type Point struct {
	T         time.Time `json:"t"`
	Remaining float64   `json:"remaining"`
	Low       float64   `json:"low"`
	High      float64   `json:"high"`
}

// Event is a modelled discrete event.
type Event struct {
	T        time.Time `json:"t"`
	Kind     string    `json:"kind"` // reset | exhaustion
	Before   float64   `json:"before"`
	After    float64   `json:"after"`
	Restored *float64  `json:"restored"`
}

// Interval is a predicted unavailability window. To is nil when the end is
// unknown (no known reset within the horizon).
type Interval struct {
	From time.Time  `json:"from"`
	To   *time.Time `json:"to"`
}

// Result is a forecast.
type Result struct {
	Start               time.Time         `json:"start"`
	StartValue          float64           `json:"start_value"`
	Limit               *float64          `json:"limit"`
	Points              []Point           `json:"points"`
	Events              []Event           `json:"events"`
	ExhaustionAt        *time.Time        `json:"exhaustion_at"`
	NextResetAt         *time.Time        `json:"next_reset_at"`
	RestoredAmount      *float64          `json:"restored_amount"`
	ExhaustsBeforeReset bool              `json:"exhausts_before_reset"`
	Downtime            []Interval        `json:"downtime"`
	Rate                *Rate             `json:"rate"`
	Method              string            `json:"method"`
	Confidence          domain.Confidence `json:"confidence"`
	Stale               bool              `json:"stale"`
	Caveats             []string          `json:"caveats"`
	Unavailable         *string           `json:"unavailable_reason"`
}

func unavailable(reason string) Result {
	return Result{Unavailable: &reason, Confidence: domain.ConfidenceNone, Caveats: []string{}, Points: []Point{}, Events: []Event{}, Downtime: []Interval{}}
}

// ResetSchedule returns the known reset times in (start, end].
func ResetSchedule(spec domain.BucketSpec, last analytics.Point, start, end time.Time) []time.Time {
	return resetSchedule(spec, last, start, end)
}

// resetSchedule returns known reset times in (start, end].
func resetSchedule(spec domain.BucketSpec, last analytics.Point, start, end time.Time) []time.Time {
	if spec.ResetSemantics == domain.ResetRolling || spec.ResetSemantics == domain.ResetNone || last.ResetAt == nil {
		return nil
	}
	var out []time.Time
	r := *last.ResetAt
	if r.After(start) && !r.After(end) {
		out = append(out, r)
	}
	if spec.ResetSemantics == domain.ResetFixedPeriodic && spec.Period > 0 {
		for t := r.Add(spec.Period); !t.After(end); t = t.Add(spec.Period) {
			if t.After(start) {
				out = append(out, t)
			}
		}
	}
	return out
}

// Forecast projects a bucket. pts is the observation history (any order).
func Forecast(spec domain.BucketSpec, pts []analytics.Point, now time.Time, rate *Rate, o Options) Result {
	o.defaults()
	prep := analytics.Prepare(pts)
	if len(prep.Points) == 0 {
		return unavailable("no observations")
	}
	last := prep.Points[len(prep.Points)-1]
	if now.Sub(last.T) > o.Horizon {
		return unavailable("latest observation is older than the forecast horizon")
	}
	if rate == nil {
		return unavailable("insufficient history to estimate a consumption rate")
	}
	v0, _ := valueOf(last)
	var limit *float64
	if spec.Unit == domain.UnitPercent {
		l := 100.0
		limit = &l
	} else if last.Limit != nil {
		l := *last.Limit
		limit = &l
	}
	res := Result{Start: last.T, StartValue: v0, Limit: limit, Rate: rate, Method: MethodEventSimulation,
		Confidence: rate.Confidence, Caveats: []string{}, Points: []Point{}, Events: []Event{}, Downtime: []Interval{}}
	if o.StaleAfter > 0 && now.Sub(last.T) > o.StaleAfter {
		res.Stale = true
		res.Caveats = append(res.Caveats, "Latest observation is stale; the forecast starts from it, not from now.")
		res.Confidence = lower(res.Confidence)
	}
	horizon := o.Horizon
	if spec.Kind == domain.KindRollingWindow {
		res.Method = MethodLimitedRolling
		res.Confidence = domain.ConfidenceLow
		res.Caveats = append(res.Caveats, "Rolling window: replenishment of earlier usage cannot be separated from new usage, so the projection assumes the observed net depletion continues and is limited to one window length.")
		if spec.Window > 0 && spec.Window < horizon {
			horizon = spec.Window
		}
	}
	end := last.T.Add(horizon)
	resets := resetSchedule(spec, last, last.T, end)
	if len(resets) > 0 {
		r := resets[0]
		res.NextResetAt = &r
	} else if spec.ResetSemantics == domain.ResetProviderTimestamp || spec.ResetSemantics == domain.ResetUnknown {
		res.Caveats = append(res.Caveats, "No reset time is known within the horizon; none is assumed.")
	}
	if spec.ResetSemantics == domain.ResetProviderTimestamp && len(resets) > 0 {
		res.Caveats = append(res.Caveats, "Only the next provider-reported reset is modelled; later resets are not invented.")
	}
	if len(resets) > 0 && limit == nil {
		res.Caveats = append(res.Caveats, "Bucket limit is unknown, so the amount restored at reset cannot be modelled.")
		resets = nil
		res.NextResetAt = nil
	}

	runs := [3]float64{rate.PerHour, rate.PerHour * (1 + o.BandFactor), math.Max(0, rate.PerHour*(1-o.BandFactor))}
	var series [3][]Point
	for i, r := range runs {
		evs, pts, dt := simulate(v0, limit, r, last.T, end, resets, o)
		series[i] = pts
		if i == 0 {
			res.Events = evs
			res.Downtime = dt
			for _, e := range evs {
				if e.Kind == "exhaustion" && res.ExhaustionAt == nil {
					t := e.T
					res.ExhaustionAt = &t
				}
				if e.Kind == "reset" && res.RestoredAmount == nil {
					res.RestoredAmount = e.Restored
				}
			}
		}
	}
	for i, p := range series[0] {
		p.Low, p.High = series[1][i].Remaining, series[2][i].Remaining
		res.Points = append(res.Points, p)
	}
	if res.ExhaustionAt != nil && (res.NextResetAt == nil || res.ExhaustionAt.Before(*res.NextResetAt)) {
		res.ExhaustsBeforeReset = true
	}
	return res
}

func valueOf(p analytics.Point) (float64, bool) {
	if p.Remaining != nil {
		return *p.Remaining, true
	}
	if p.Limit != nil && p.Used != nil {
		return *p.Limit - *p.Used, true
	}
	return 0, false
}

func lower(c domain.Confidence) domain.Confidence {
	switch c {
	case domain.ConfidenceHigh:
		return domain.ConfidenceMedium
	case domain.ConfidenceMedium:
		return domain.ConfidenceLow
	}
	return c
}

// simulate walks forward in fixed steps, processing resets exactly. Points
// are emitted on a fixed grid aligned to start plus duplicate points at
// reset instants (pre/post) so the chart can draw a vertical step.
func simulate(v float64, limit *float64, perHour float64, start, end time.Time, resets []time.Time, o Options) ([]Event, []Point, []Interval) {
	var evs []Event
	var pts []Point
	var down []Interval
	if limit != nil && v > *limit {
		v = *limit
	}
	if v < 0 {
		v = 0
	}
	exhausted := v <= 0
	if exhausted {
		down = append(down, Interval{From: start})
	}
	t := start
	ri := 0
	var nextOut time.Time
	emit := func(at time.Time, val float64) {
		pts = append(pts, Point{T: at, Remaining: round(val)})
	}
	emit(start, v)
	nextOut = start.Add(o.OutputEvery)
	for t.Before(end) {
		next := t.Add(o.Step)
		if next.After(end) {
			next = end
		}
		if ri < len(resets) && !resets[ri].After(next) {
			next = resets[ri]
		}
		dt := next.Sub(t).Hours()
		if !exhausted && perHour > 0 {
			nv := v - perHour*dt
			if nv <= 0 {
				// Exact exhaustion instant within this step.
				at := t.Add(time.Duration(v / perHour * float64(time.Hour))).Round(time.Second)
				evs = append(evs, Event{T: at, Kind: "exhaustion", Before: round(v), After: 0})
				down = append(down, Interval{From: at})
				exhausted = true
				nv = 0
			}
			v = nv
		}
		t = next
		for !nextOut.After(t) && nextOut.Before(end.Add(time.Nanosecond)) {
			emit(nextOut, v)
			nextOut = nextOut.Add(o.OutputEvery)
		}
		if ri < len(resets) && t.Equal(resets[ri]) {
			before := v
			restored := *limit - before
			emit(t, before)
			v = *limit
			emit(t, v)
			r := round(restored)
			evs = append(evs, Event{T: t, Kind: "reset", Before: round(before), After: round(v), Restored: &r})
			if exhausted {
				at := t
				down[len(down)-1].To = &at
				exhausted = false
			}
			ri++
		}
	}
	return evs, pts, down
}

func round(v float64) float64 { return math.Round(v*1000) / 1000 }

// MarshalJSON emits empty lists instead of null so clients never have to
// guard list fields.
func (r Result) MarshalJSON() ([]byte, error) {
	type plain Result
	p := plain(r)
	p.Points, p.Events, p.Downtime, p.Caveats = nonNil(p.Points), nonNil(p.Events), nonNil(p.Downtime), nonNil(p.Caveats)
	return json.Marshal(p)
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
