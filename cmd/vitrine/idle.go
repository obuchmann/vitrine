package main

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// idleTracker notes when the last request ended. A request that is still
// running, such as an open tab's stream or an agent's long poll, keeps the
// server busy.
type idleTracker struct {
	next http.Handler
	now  func() time.Time

	mu       sync.Mutex
	inflight int
	last     time.Time
}

func newIdleTracker(next http.Handler, now func() time.Time) *idleTracker {
	return &idleTracker{next: next, now: now, last: now()}
}

func (t *idleTracker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t.mu.Lock()
	t.inflight++
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		t.inflight--
		t.last = t.now()
		t.mu.Unlock()
	}()
	t.next.ServeHTTP(w, r)
}

// idleFor returns how long no request has been running.
func (t *idleTracker) idleFor() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.inflight > 0 {
		return 0
	}
	return t.now().Sub(t.last)
}

// wait returns true once the server has been idle for d, or false when ctx ends.
func (t *idleTracker) wait(ctx context.Context, d time.Duration) bool {
	tick := time.NewTicker(min(d/4, time.Minute))
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-tick.C:
			if t.idleFor() >= d {
				return true
			}
		}
	}
}
