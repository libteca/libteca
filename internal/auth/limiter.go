package auth

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	defaultThreshold = 5
	defaultWindow    = 10 * time.Minute
	defaultLockout   = 15 * time.Minute
	defaultMaxIPs    = 10000
)

type limitEntry struct {
	failures    []time.Time
	lockedUntil time.Time
	lastSeen    time.Time
}

type Limiter struct {
	mu        sync.Mutex
	entries   map[string]*limitEntry
	threshold int
	window    time.Duration
	lockout   time.Duration
	maxIPs    int
	now       func() time.Time
}

func NewLimiter() *Limiter {
	return &Limiter{
		entries:   make(map[string]*limitEntry),
		threshold: defaultThreshold,
		window:    defaultWindow,
		lockout:   defaultLockout,
		maxIPs:    defaultMaxIPs,
		now:       time.Now,
	}
}

func (l *Limiter) Allow(ip string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[ip]
	if e == nil {
		return true, 0
	}
	now := l.now()
	if rem := e.lockedUntil.Sub(now); rem > 0 {
		return false, rem
	}
	l.pruneFailures(e, now)
	if len(e.failures) == 0 {
		delete(l.entries, ip)
	}
	return true, 0
}

func (l *Limiter) Failure(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	e := l.entries[ip]
	if e == nil {
		l.evictLocked(now)
		if len(l.entries) >= l.maxIPs {
			return
		}
		e = &limitEntry{}
		l.entries[ip] = e
	}
	if now.Before(e.lockedUntil) {
		return
	}
	e.lastSeen = now
	l.pruneFailures(e, now)
	e.failures = append(e.failures, now)
	if len(e.failures) >= l.threshold {
		e.lockedUntil = now.Add(l.lockout)
		e.failures = nil
	}
}

func (l *Limiter) Success(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, ip)
}

func (l *Limiter) pruneFailures(e *limitEntry, now time.Time) {
	keep := e.failures[:0]
	for _, t := range e.failures {
		if now.Sub(t) < l.window {
			keep = append(keep, t)
		}
	}
	e.failures = keep
}

func (l *Limiter) evictLocked(now time.Time) {
	for ip, e := range l.entries {
		if !now.Before(e.lockedUntil) {
			l.pruneFailures(e, now)
			if len(e.failures) == 0 {
				delete(l.entries, ip)
			}
		}
	}
	for len(l.entries) >= l.maxIPs {
		oldestIP := ""
		var oldest time.Time
		first := true
		for ip, e := range l.entries {
			if first || e.lastSeen.Before(oldest) {
				oldestIP, oldest, first = ip, e.lastSeen, false
			}
		}
		delete(l.entries, oldestIP)
	}
}

func ClientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func WriteRetryAfter(w http.ResponseWriter, retry time.Duration) {
	secs := int((retry + time.Second - 1) / time.Second)
	w.Header().Set("Retry-After", strconv.Itoa(secs))
}
