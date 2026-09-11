package auth

import (
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }
func (c *fakeClock) advance(d time.Duration) {
	c.now = c.now.Add(d)
}

func newTestLimiter() (*Limiter, *fakeClock) {
	c := &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	l := NewLimiter()
	l.now = c.Now
	return l, c
}

func TestLimiterThresholdLocksOut(t *testing.T) {
	l, c := newTestLimiter()
	for i := 0; i < defaultThreshold-1; i++ {
		l.Failure("1.2.3.4")
		if ok, _ := l.Allow("1.2.3.4"); !ok {
			t.Fatalf("denied after %d failures, want allowed", i+1)
		}
	}
	l.Failure("1.2.3.4")
	ok, retry := l.Allow("1.2.3.4")
	if ok {
		t.Fatal("allowed after threshold failures")
	}
	if retry != defaultLockout {
		t.Fatalf("retry = %v, want %v", retry, defaultLockout)
	}
	c.advance(defaultLockout - time.Second)
	if ok, _ := l.Allow("1.2.3.4"); ok {
		t.Fatal("lockout expired early")
	}
	c.advance(time.Second)
	if ok, _ := l.Allow("1.2.3.4"); !ok {
		t.Fatal("still locked after lockout duration")
	}
	l.Failure("1.2.3.4")
	if ok, _ := l.Allow("1.2.3.4"); !ok {
		t.Fatal("counter must restart fresh after lockout expiry")
	}
}

func TestLimiterBlockedAttemptsDoNotExtendLockout(t *testing.T) {
	l, c := newTestLimiter()
	for i := 0; i < defaultThreshold; i++ {
		l.Failure("1.2.3.4")
	}
	c.advance(time.Minute)
	l.Failure("1.2.3.4")
	_, retry := l.Allow("1.2.3.4")
	if want := defaultLockout - time.Minute; retry != want {
		t.Fatalf("retry = %v, want %v (lockout must not extend)", retry, want)
	}
}

func TestLimiterSuccessResets(t *testing.T) {
	l, _ := newTestLimiter()
	for i := 0; i < defaultThreshold-1; i++ {
		l.Failure("1.2.3.4")
	}
	l.Success("1.2.3.4")
	for i := 0; i < defaultThreshold-1; i++ {
		l.Failure("1.2.3.4")
	}
	if ok, _ := l.Allow("1.2.3.4"); !ok {
		t.Fatal("pre-threshold failures after a success must not lock")
	}
}

func TestLimiterWindowExpiry(t *testing.T) {
	l, c := newTestLimiter()
	for i := 0; i < defaultThreshold-1; i++ {
		l.Failure("1.2.3.4")
	}
	c.advance(defaultWindow + time.Second)
	l.Failure("1.2.3.4")
	if ok, _ := l.Allow("1.2.3.4"); !ok {
		t.Fatal("stale failures must expire from the window")
	}
}

func TestLimiterRollingWindow(t *testing.T) {
	l, c := newTestLimiter()
	for i := 0; i < defaultThreshold-2; i++ {
		l.Failure("1.2.3.4")
	}
	c.advance(defaultWindow - time.Minute)
	for i := 0; i < 2; i++ {
		l.Failure("1.2.3.4")
	}
	if ok, _ := l.Allow("1.2.3.4"); ok {
		t.Fatal("threshold failures within the window must lock")
	}
}

func TestLimiterSeparateIPs(t *testing.T) {
	l, _ := newTestLimiter()
	for i := 0; i < defaultThreshold; i++ {
		l.Failure("1.2.3.4")
	}
	if ok, _ := l.Allow("1.2.3.4"); ok {
		t.Fatal("offending ip must be locked")
	}
	if ok, _ := l.Allow("5.6.7.8"); !ok {
		t.Fatal("other ip must be unaffected")
	}
}

func TestLimiterMaxIPs(t *testing.T) {
	l, _ := newTestLimiter()
	l.maxIPs = 3
	for _, ip := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"} {
		l.Failure(ip)
	}
	l.Failure("10.0.0.4")
	if len(l.entries) != l.maxIPs {
		t.Fatalf("entries = %d, want %d", len(l.entries), l.maxIPs)
	}
	if _, ok := l.entries["10.0.0.1"]; ok {
		t.Fatal("oldest entry must be evicted at capacity")
	}
	if _, ok := l.entries["10.0.0.4"]; !ok {
		t.Fatal("newest entry must be tracked at capacity")
	}
}

func TestLimiterConcurrent(t *testing.T) {
	l, _ := newTestLimiter()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				ip := "10.0.0." + string(rune('1'+g%9))
				l.Failure(ip)
				l.Allow(ip)
				if i%10 == 0 {
					l.Success(ip)
				}
			}
		}(g)
	}
	wg.Wait()
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		addr string
		want string
	}{
		{"1.2.3.4:5678", "1.2.3.4"},
		{"1.2.3.4", "1.2.3.4"},
		{"[::1]:8080", "::1"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("POST", "/", nil)
		r.RemoteAddr = c.addr
		if got := ClientIP(r); got != c.want {
			t.Fatalf("ClientIP(%q) = %q, want %q", c.addr, got, c.want)
		}
	}
}
