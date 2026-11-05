// Package loadgen drives concurrent gateway requests and reports latency.
// It is used by cmd/hp-loadgen against a running server and by the load
// tests against an in-process one.
package loadgen

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Config describes one load run.
type Config struct {
	BaseURL     string        // gateway base, e.g. http://127.0.0.1:8080
	Token       string        // gateway token (never logged)
	Model       string        // requested model
	Concurrency int           // parallel clients
	Requests    int           // total requests
	Stream      bool          // request server-sent events
	ReadDelay   time.Duration // pause after each received line (slow client)
	MaxTokens   int
	Client      *http.Client
}

// Report summarizes a run. Durations are wall-clock per request.
type Report struct {
	Requests  int            `json:"requests"`
	OK        int            `json:"ok"`
	Errors    map[string]int `json:"errors"`
	Elapsed   time.Duration  `json:"elapsed"`
	PerSecond float64        `json:"per_second"`
	Latency   Percentiles    `json:"latency"`
	FirstByte Percentiles    `json:"first_byte"`
}

// Percentiles of a duration sample.
type Percentiles struct {
	P50 time.Duration `json:"p50"`
	P95 time.Duration `json:"p95"`
	P99 time.Duration `json:"p99"`
	Max time.Duration `json:"max"`
}

func percentiles(d []time.Duration) Percentiles {
	if len(d) == 0 {
		return Percentiles{}
	}
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	at := func(q float64) time.Duration { return d[min(len(d)-1, int(q*float64(len(d))))] }
	return Percentiles{P50: at(0.50), P95: at(0.95), P99: at(0.99), Max: d[len(d)-1]}
}

// Run executes the load and returns a report. It stops early when ctx ends.
func Run(ctx context.Context, c Config) Report {
	if c.Concurrency <= 0 {
		c.Concurrency = 1
	}
	if c.MaxTokens <= 0 {
		c.MaxTokens = 64
	}
	hc := c.Client
	if hc == nil {
		hc = &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: c.Concurrency, MaxConnsPerHost: 0}}
	}
	body, _ := json.Marshal(map[string]any{"model": c.Model, "max_tokens": c.MaxTokens, "stream": c.Stream,
		"messages": []any{map[string]any{"role": "user", "content": "Summarize the change in one line."}}})
	var next atomic.Int64
	var mu sync.Mutex
	rep := Report{Errors: map[string]int{}}
	var lat, fb []time.Duration
	start := time.Now()
	var wg sync.WaitGroup
	for range c.Concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for int(next.Add(1)) <= c.Requests && ctx.Err() == nil {
				l, f, err := one(ctx, hc, c, body)
				mu.Lock()
				rep.Requests++
				if err != "" {
					rep.Errors[err]++
				} else {
					rep.OK++
					lat, fb = append(lat, l), append(fb, f)
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	rep.Elapsed = time.Since(start)
	if s := rep.Elapsed.Seconds(); s > 0 {
		rep.PerSecond = float64(rep.Requests) / s
	}
	rep.Latency, rep.FirstByte = percentiles(lat), percentiles(fb)
	return rep
}

// one sends a request and reads the whole response; err is a short class.
func one(ctx context.Context, hc *http.Client, c Config, body []byte) (time.Duration, time.Duration, string) {
	t0 := time.Now()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/v1/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.Token)
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := hc.Do(req)
	if err != nil {
		return 0, 0, "transport"
	}
	defer resp.Body.Close()
	first := time.Since(t0)
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return 0, 0, fmt.Sprintf("http_%d", resp.StatusCode)
	}
	if !c.Stream {
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			return 0, 0, "read"
		}
		return time.Since(t0), first, ""
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	done := false
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, "message_stop") {
			done = true
		}
		if strings.HasPrefix(line, "event: error") {
			return 0, 0, "stream_error"
		}
		if c.ReadDelay > 0 && line != "" {
			time.Sleep(c.ReadDelay)
		}
	}
	if sc.Err() != nil || !done {
		return 0, 0, "incomplete_stream"
	}
	return time.Since(t0), first, ""
}

// String renders a short human-readable summary.
func (r Report) String() string {
	return fmt.Sprintf("%d requests (%d ok) in %s, %.1f/s; latency p50 %s p95 %s p99 %s max %s; first byte p50 %s p99 %s; errors %v",
		r.Requests, r.OK, r.Elapsed.Round(time.Millisecond), r.PerSecond, r.Latency.P50.Round(time.Millisecond), r.Latency.P95.Round(time.Millisecond),
		r.Latency.P99.Round(time.Millisecond), r.Latency.Max.Round(time.Millisecond), r.FirstByte.P50.Round(time.Millisecond),
		r.FirstByte.P99.Round(time.Millisecond), r.Errors)
}
