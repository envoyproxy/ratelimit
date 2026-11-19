package utils

import (
	"sync"
	"time"
)

// LogLimiter rate-limits a log line that may be emitted on every request, so
// that a burst of failures produces one line per interval rather than one per
// request.
type LogLimiter struct {
	interval   time.Duration
	mu         sync.Mutex
	last       time.Time
	suppressed uint64
}

func NewLogLimiter(interval time.Duration) *LogLimiter {
	return &LogLimiter{interval: interval}
}

// Allow reports whether the caller should log now and, if so, how many log
// lines were suppressed since the last one. A nil LogLimiter allows every line.
func (l *LogLimiter) Allow() (bool, uint64) {
	if l == nil {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if !l.last.IsZero() && now.Sub(l.last) < l.interval {
		l.suppressed++
		return false, 0
	}
	l.last = now
	suppressed := l.suppressed
	l.suppressed = 0
	return true, suppressed
}
