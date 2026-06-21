package relay

import (
	"context"
	"sync"
)

// Limiter enforces a per-user daily turn quota. A "turn" is one fresh user
// message; tool-result continuations and failovers do not count again.
// Implementations must be safe for concurrent use.
//
// Swap the in-memory default for a shared store (Redis, Postgres, ...) when you
// run more than one instance, so the cap holds across the fleet.
type Limiter interface {
	// Allow atomically reserves one turn for user on the given UTC date
	// (formatted YYYY-MM-DD). It returns false when the user is already at or
	// above cap. A cap of 0 means unlimited and must always return true.
	Allow(ctx context.Context, user, date string, cap int) (bool, error)
}

// MemoryLimiter is an in-process Limiter backed by a map. It resets implicitly
// as dates roll over (old dates are simply never read again). Suitable for a
// single instance; use a shared store behind a load balancer.
type MemoryLimiter struct {
	mu     sync.Mutex
	counts map[string]map[string]int // date -> user -> count
}

// NewMemoryLimiter returns an empty in-memory limiter.
func NewMemoryLimiter() *MemoryLimiter {
	return &MemoryLimiter{counts: make(map[string]map[string]int)}
}

// Allow reserves a turn, incrementing the per-user count for the date.
func (m *MemoryLimiter) Allow(_ context.Context, user, date string, cap int) (bool, error) {
	if cap <= 0 {
		return true, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	day := m.counts[date]
	if day == nil {
		// Drop stale dates so the map doesn't grow unbounded across days.
		m.counts = map[string]map[string]int{date: {}}
		day = m.counts[date]
	}
	if day[user] >= cap {
		return false, nil
	}
	day[user]++
	return true, nil
}
