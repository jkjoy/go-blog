package blog

import (
	"sync"
	"time"
)

type rateLimitEntry struct {
	count   int
	resetAt time.Time
}

type rateLimiter struct {
	mu         sync.Mutex
	entries    map[string]rateLimitEntry
	limit      int
	window     time.Duration
	maxEntries int
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{entries: make(map[string]rateLimitEntry), limit: limit, window: window, maxEntries: 10_000}
}

func (l *rateLimiter) Allow(key string) (bool, time.Duration) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	entry, exists := l.entries[key]
	if !exists || !now.Before(entry.resetAt) {
		if len(l.entries) >= l.maxEntries {
			l.cleanup(now)
		}
		entry = rateLimitEntry{resetAt: now.Add(l.window)}
	}
	entry.count++
	l.entries[key] = entry
	if entry.count <= l.limit {
		return true, 0
	}
	return false, time.Until(entry.resetAt)
}

func (l *rateLimiter) Reset(key string) {
	l.mu.Lock()
	delete(l.entries, key)
	l.mu.Unlock()
}

func (l *rateLimiter) cleanup(now time.Time) {
	for key, entry := range l.entries {
		if !now.Before(entry.resetAt) {
			delete(l.entries, key)
		}
	}
	if len(l.entries) < l.maxEntries {
		return
	}
	for key := range l.entries {
		delete(l.entries, key)
		if len(l.entries) < l.maxEntries {
			break
		}
	}
}
