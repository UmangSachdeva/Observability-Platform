package utils

import (
	"sync"
	"time"
)

type keyIssueWindow struct {
	count int
	start time.Time
}

type KeyIssueLimiter struct {
	mu       sync.Mutex
	requests map[string]keyIssueWindow
	max      int
	window   time.Duration
}

func NewKeyIssueLimiter(max int, window time.Duration) *KeyIssueLimiter {
	return &KeyIssueLimiter{requests: make(map[string]keyIssueWindow), max: max, window: window}
}

func (l *KeyIssueLimiter) Allow(ip string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	for key, entry := range l.requests {
		if now.Sub(entry.start) >= l.window {
			delete(l.requests, key)
		}
	}
	entry := l.requests[ip]
	if now.Sub(entry.start) >= l.window {
		entry = keyIssueWindow{start: now}
	}
	if entry.count >= l.max {
		l.requests[ip] = entry
		return false
	}
	entry.count++
	l.requests[ip] = entry
	return true
}
