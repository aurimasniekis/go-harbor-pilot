// Package clock abstracts time so schedulers, analytics and tests can run
// against a deterministic fake clock instead of the wall clock.
package clock

import (
	"sync"
	"time"
)

// Clock is the time source used across the application.
type Clock interface {
	Now() time.Time
	// After returns a channel that fires once d has elapsed on this clock.
	After(d time.Duration) <-chan time.Time
}

// Real is the wall clock. Times are always returned in UTC.
type Real struct{}

// Now returns the current UTC time.
func (Real) Now() time.Time { return time.Now().UTC() }

// After wraps time.After.
func (Real) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Frozen is a clock that always reports the same instant but lets real time
// pass for timers. It is used by demo mode with --demo-clock so screenshots
// and E2E runs see identical data while background loops still tick.
type Frozen struct {
	At time.Time
}

// Now returns the frozen instant.
func (f Frozen) Now() time.Time { return f.At.UTC() }

// After uses real time so loops keep running.
func (Frozen) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Fake is a manually advanced clock for tests.
type Fake struct {
	mu      sync.Mutex
	now     time.Time
	waiters []fakeWaiter
}

type fakeWaiter struct {
	at time.Time
	ch chan time.Time
}

// NewFake returns a fake clock set to t.
func NewFake(t time.Time) *Fake {
	return &Fake{now: t.UTC()}
}

// Now returns the fake current time.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// After returns a channel that fires when the fake clock is advanced past d.
func (f *Fake) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan time.Time, 1)
	at := f.now.Add(d)
	if d <= 0 {
		ch <- f.now
		return ch
	}
	f.waiters = append(f.waiters, fakeWaiter{at: at, ch: ch})
	return ch
}

// Advance moves the clock forward and fires due timers.
func (f *Fake) Advance(d time.Duration) {
	f.Set(f.Now().Add(d))
}

// Set moves the clock to t and fires due timers.
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	f.now = t.UTC()
	remaining := f.waiters[:0]
	var due []fakeWaiter
	for _, w := range f.waiters {
		if !w.at.After(f.now) {
			due = append(due, w)
		} else {
			remaining = append(remaining, w)
		}
	}
	f.waiters = remaining
	now := f.now
	f.mu.Unlock()
	for _, w := range due {
		w.ch <- now
	}
}

// Waiters reports how many timers are pending; useful for synchronizing tests.
func (f *Fake) Waiters() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.waiters)
}
