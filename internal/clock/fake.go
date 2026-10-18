package clock

import (
	"sort"
	"sync"
	"time"
)

// Fake is a manually advanced clock for tests.
type Fake struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

// NewFake returns a fake clock set to now.
func NewFake(now time.Time) *Fake { return &Fake{now: now} }

func (c *Fake) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *Fake) AfterFunc(d time.Duration, f func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{clock: c, at: c.now.Add(d), f: f}
	c.timers = append(c.timers, t)
	return t
}

// Advance moves time forward and runs every timer that became due, in order.
// Timer callbacks run on the calling goroutine, outside the clock's lock.
func (c *Fake) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	var due, rest []*fakeTimer
	for _, t := range c.timers {
		if !t.at.After(c.now) {
			due = append(due, t)
		} else {
			rest = append(rest, t)
		}
	}
	c.timers = rest
	c.mu.Unlock()

	sort.Slice(due, func(i, j int) bool { return due[i].at.Before(due[j].at) })
	for _, t := range due {
		t.f()
	}
}

// Jump moves time forward without firing timers, like a machine waking
// from suspend before its monotonic timers catch up.
func (c *Fake) Jump(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Pending reports how many timers are waiting.
func (c *Fake) Pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers)
}

type fakeTimer struct {
	clock *Fake
	at    time.Time
	f     func()
}

func (t *fakeTimer) Stop() bool {
	c := t.clock
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, other := range c.timers {
		if other == t {
			c.timers = append(c.timers[:i], c.timers[i+1:]...)
			return true
		}
	}
	return false
}
