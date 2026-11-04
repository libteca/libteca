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
	l, c := newTestLimiter()
	l.maxIPs = 3
	// Distinct timestamps: identical lastSeen values made eviction depend on
	// map iteration order and the test flake on the oldest-entry assertion.
	for _, ip := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"} {
		l.Failure(ip)
		c.advance(time.Minute)
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
		{"[::1]:8080", "::/64"},
		{"[2001:db8:1:2:aaaa::1]:443", "2001:db8:1:2::/64"},
		{"[::ffff:1.2.3.4]:80", "1.2.3.4"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("POST", "/", nil)
		r.RemoteAddr = c.addr
		if got := ClientIP(r); got != c.want {
			t.Fatalf("ClientIP(%q) = %q, want %q", c.addr, got, c.want)
		}
	}
}

func TestLimiterAggregateIPBucket(t *testing.T) {
	l, _ := newTestLimiter()
	for i := 0; i < 30; i++ {
		if ok, _ := l.AllowIP("9.9.9.9"); !ok {
			t.Fatalf("aggregate bucket locked after %d failures", i)
		}
		l.FailureIP("9.9.9.9")
	}
	if ok, retry := l.AllowIP("9.9.9.9"); ok {
		t.Fatal("aggregate bucket did not lock after 30 failures")
	} else if retry <= 0 {
		t.Fatal("locked aggregate bucket must report a retry duration")
	}
	if ok, _ := l.AllowIP("8.8.8.8"); !ok {
		t.Fatal("aggregate lockout leaked to another IP")
	}
}

func TestLimiterAggregateNotClearedBySuccess(t *testing.T) {
	l, _ := newTestLimiter()
	for i := 0; i < 29; i++ {
		l.FailureIP("9.9.9.9")
	}
	l.SuccessIP("9.9.9.9")
	if ok, _ := l.AllowIP("9.9.9.9"); !ok {
		t.Fatal("aggregate bucket locked too early")
	}
	l.FailureIP("9.9.9.9")
	if ok, _ := l.AllowIP("9.9.9.9"); ok {
		t.Fatal("success cleared the aggregate failure history")
	}
}

func TestLimiterPrincipalThresholdUnchanged(t *testing.T) {
	l, _ := newTestLimiter()
	for i := 0; i < 4; i++ {
		l.Failure("1.1.1.1|alice")
	}
	if ok, _ := l.Allow("1.1.1.1|alice"); !ok {
		t.Fatal("principal bucket must still lock at 5 failures")
	}
	l.Failure("1.1.1.1|alice")
	if ok, _ := l.Allow("1.1.1.1|alice"); ok {
		t.Fatal("principal bucket did not lock at 5 failures")
	}
}

func clientIPFor(remote string, xff ...string) string {
	r := httptest.NewRequest("POST", "/", nil)
	r.RemoteAddr = remote
	for _, v := range xff {
		r.Header.Add("X-Forwarded-For", v)
	}
	return ClientIP(r)
}

func TestClientIPIgnoresForwardedWithoutTrustedProxy(t *testing.T) {
	if err := SetTrustedProxies(""); err != nil {
		t.Fatal(err)
	}
	if got := clientIPFor("10.0.0.5:1234", "9.9.9.9"); got != "10.0.0.5" {
		t.Fatalf("got %q, want peer address", got)
	}
}

func TestClientIPTrustedProxyChain(t *testing.T) {
	if err := SetTrustedProxies("172.18.0.0/16, 10.1.1.1"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = SetTrustedProxies("") })
	cases := []struct {
		remote string
		xff    []string
		want   string
	}{
		{"172.18.0.2:5000", []string{"203.0.113.7"}, "203.0.113.7"},
		{"172.18.0.2:5000", []string{"1.1.1.1, 203.0.113.7"}, "203.0.113.7"},
		{"172.18.0.2:5000", []string{"1.1.1.1, 203.0.113.7, 10.1.1.1"}, "203.0.113.7"},
		{"172.18.0.2:5000", []string{"1.1.1.1", "203.0.113.7"}, "203.0.113.7"},
		{"172.18.0.2:5000", []string{"2001:db8:5:6::9"}, "2001:db8:5:6::/64"},
		{"172.18.0.2:5000", nil, "172.18.0.2"},
		{"172.18.0.2:5000", []string{"1.1.1.1, garbage"}, "172.18.0.2"},
		{"172.18.0.2:5000", []string{"10.1.1.1"}, "10.1.1.1"},
		{"203.0.113.9:5000", []string{"1.1.1.1"}, "203.0.113.9"},
	}
	for _, c := range cases {
		if got := clientIPFor(c.remote, c.xff...); got != c.want {
			t.Fatalf("ClientIP(%s, %v) = %q, want %q", c.remote, c.xff, got, c.want)
		}
	}
}

func TestSetTrustedProxiesRejectsGarbage(t *testing.T) {
	if err := SetTrustedProxies("10.0.0.0/8,nope"); err == nil {
		t.Fatal("expected error for invalid entry")
	}
	t.Cleanup(func() { _ = SetTrustedProxies("") })
}

func TestLimiterEvictionKeepsLockouts(t *testing.T) {
	l, c := newTestLimiter()
	l.maxIPs = 3
	for i := 0; i < l.threshold; i++ {
		l.Failure("locked")
	}
	c.advance(time.Second)
	l.Failure("a")
	c.advance(time.Second)
	l.Failure("b")
	c.advance(time.Second)
	l.Failure("c")
	if ok, _ := l.Allow("locked"); ok {
		t.Fatal("active lockout must survive eviction")
	}
	if _, ok := l.entries["a"]; ok {
		t.Fatal("oldest unlocked entry must be evicted first")
	}
}

func TestLimiterFullOfLockoutsRefusesNewKeys(t *testing.T) {
	l, c := newTestLimiter()
	l.maxIPs = 2
	for _, k := range []string{"x", "y"} {
		for i := 0; i < l.threshold; i++ {
			l.Failure(k)
		}
	}
	l.Failure("z")
	if _, ok := l.entries["z"]; ok {
		t.Fatal("new key must not displace an active lockout")
	}
	for _, k := range []string{"x", "y"} {
		if ok, _ := l.Allow(k); ok {
			t.Fatalf("lockout for %s dropped", k)
		}
	}
	if ok, retry := l.Allow("z"); ok || retry <= 0 {
		t.Fatalf("new key must be refused while table is full of lockouts, got ok=%v retry=%v", ok, retry)
	}
	c.advance(l.lockout + time.Second)
	if ok, _ := l.Allow("z"); !ok {
		t.Fatal("new key must be allowed once lockouts expire")
	}
}
