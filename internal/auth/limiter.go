package auth

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultThreshold   = 5
	defaultIPThreshold = 30
	defaultWindow      = 10 * time.Minute
	defaultLockout     = 15 * time.Minute
	defaultMaxIPs      = 10000
	ipKeyPrefix        = "ip:"
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
	ipThresh  int
	window    time.Duration
	lockout   time.Duration
	maxIPs    int
	now       func() time.Time
}

func NewLimiter() *Limiter {
	return &Limiter{
		entries:   make(map[string]*limitEntry),
		threshold: defaultThreshold,
		ipThresh:  defaultIPThreshold,
		window:    defaultWindow,
		lockout:   defaultLockout,
		maxIPs:    defaultMaxIPs,
		now:       time.Now,
	}
}

func (l *Limiter) AllowIP(ip string) (bool, time.Duration) {
	return l.Allow(ipKeyPrefix + ip)
}

func (l *Limiter) FailureIP(ip string) {
	l.Failure(ipKeyPrefix + ip)
}

func (l *Limiter) thresholdFor(key string) int {
	if strings.HasPrefix(key, ipKeyPrefix) {
		return l.ipThresh
	}
	return l.threshold
}

func (l *Limiter) Allow(ip string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[ip]
	now := l.now()
	if e == nil {
		if rem, full := l.fullLocked(now); full {
			return false, rem
		}
		return true, 0
	}
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
	if len(e.failures) >= l.thresholdFor(ip) {
		e.lockedUntil = now.Add(l.lockout)
		e.failures = nil
	}
}

func (l *Limiter) Success(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, ip)
}

func (l *Limiter) SuccessIP(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e, ok := l.entries[ipKeyPrefix+ip]; ok {
		e.lastSeen = l.now()
	}
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
		for ip, e := range l.entries {
			if now.Before(e.lockedUntil) {
				continue
			}
			if oldestIP == "" || e.lastSeen.Before(oldest) {
				oldestIP, oldest = ip, e.lastSeen
			}
		}
		if oldestIP == "" {
			return
		}
		delete(l.entries, oldestIP)
	}
}

func (l *Limiter) fullLocked(now time.Time) (time.Duration, bool) {
	if len(l.entries) < l.maxIPs {
		return 0, false
	}
	var soonest time.Duration
	for _, e := range l.entries {
		rem := e.lockedUntil.Sub(now)
		if rem <= 0 {
			return 0, false
		}
		if soonest == 0 || rem < soonest {
			soonest = rem
		}
	}
	return soonest, true
}

var trustedProxies atomic.Pointer[[]netip.Prefix]

func SetTrustedProxies(spec string) error {
	var prefixes []netip.Prefix
	for _, f := range strings.FieldsFunc(spec, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
		if p, err := netip.ParsePrefix(f); err == nil {
			prefixes = append(prefixes, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(f)
		if err != nil {
			return fmt.Errorf("trusted proxy %q: not an IP or CIDR", f)
		}
		a = a.Unmap()
		prefixes = append(prefixes, netip.PrefixFrom(a, a.BitLen()))
	}
	trustedProxies.Store(&prefixes)
	return nil
}

func isTrustedProxy(a netip.Addr) bool {
	p := trustedProxies.Load()
	if p == nil {
		return false
	}
	for _, pfx := range *p {
		if pfx.Contains(a) {
			return true
		}
	}
	return false
}

func parseHop(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	if a, err := netip.ParseAddr(s); err == nil {
		return a.Unmap(), true
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().Unmap(), true
	}
	return netip.Addr{}, false
}

func limitKey(a netip.Addr) string {
	if a.Is6() {
		return netip.PrefixFrom(a, 64).Masked().String()
	}
	return a.String()
}

func ClientIP(r *http.Request) string {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = h
	}
	peer, ok := parseHop(host)
	if !ok {
		return host
	}
	client := peer
	if isTrustedProxy(peer) {
		var hops []string
		for _, v := range r.Header.Values("X-Forwarded-For") {
			hops = append(hops, strings.Split(v, ",")...)
		}
		for i := len(hops) - 1; i >= 0; i-- {
			a, ok := parseHop(hops[i])
			if !ok {
				break
			}
			client = a
			if !isTrustedProxy(a) {
				break
			}
		}
	}
	return limitKey(client)
}

func WriteRetryAfter(w http.ResponseWriter, retry time.Duration) {
	secs := int((retry + time.Second - 1) / time.Second)
	w.Header().Set("Retry-After", strconv.Itoa(secs))
}
