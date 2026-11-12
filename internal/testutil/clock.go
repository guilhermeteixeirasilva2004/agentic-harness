package testutil

import (
	"sync"
	"time"
)

// Clock é um relógio controlável: o tempo só anda com Advance/Set.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

// NewClock cria um relógio parado em start; com start zero, começa em uma data
// fixa e determinística (2024-01-01T00:00:00Z).
func NewClock(start time.Time) *Clock {
	if start.IsZero() {
		start = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return &Clock{now: start}
}

// Now devolve a hora atual do relógio.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance avança o relógio em d.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Set posiciona o relógio em t.
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}
