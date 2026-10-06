package auth

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSessionSignatureAndExpiry(t *testing.T) {
	sessions := Sessions{Secret: []byte("a sufficiently long session secret"), Lifetime: time.Hour}
	now := time.Now()
	value, err := sessions.Sign(Session{Username: "admin", CSRF: "csrf", CredentialTag: "credential", ExpiresAt: now.Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Verify(value, now); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Verify(value+"tampered", now); err == nil {
		t.Fatal("tampered session accepted")
	}
	if _, err := sessions.Verify(value, now.Add(2*time.Hour)); err == nil {
		t.Fatal("expired session accepted")
	}
}

func TestLimiterReservesConcurrentBudget(t *testing.T) {
	l := NewLimiter()
	var accepted atomic.Int32
	var wg, reserved sync.WaitGroup
	ready, release := make(chan struct{}), make(chan struct{})
	for range 24 {
		wg.Add(1)
		reserved.Add(1)
		go func() {
			defer wg.Done()
			<-ready
			finish, ok := l.Begin("192.0.2.1:1234")
			reserved.Done()
			if !ok {
				return
			}
			accepted.Add(1)
			<-release
			finish(false)
			finish(false) // A deferred completion must not spend the budget twice.
		}()
	}
	close(ready)
	reserved.Wait()
	close(release)
	wg.Wait()
	if accepted.Load() != int32(l.Limit) {
		t.Fatalf("accepted %d concurrent attempts", accepted.Load())
	}
	if _, ok := l.Begin("192.0.2.1:5555"); ok {
		t.Fatal("failed budget was not blocked")
	}
	if l.active != 0 || len(l.inFlight) != 0 {
		t.Fatal("reservations leaked")
	}
}

func TestLimiterGlobalConcurrencyAndSuccessfulRecovery(t *testing.T) {
	l := NewLimiter()
	var finishers []func(bool)
	for n := range maxConcurrentLogins {
		finish, ok := l.Begin(fmt.Sprintf("192.0.2.%d:1234", n+1))
		if !ok {
			t.Fatal("early global limit")
		}
		finishers = append(finishers, finish)
	}
	if _, ok := l.Begin("198.51.100.1:1234"); ok {
		t.Fatal("global concurrency unbounded")
	}
	for _, finish := range finishers {
		finish(true)
	}
	l.Fail("192.0.2.1:1234")
	finish, ok := l.Begin("192.0.2.1:1234")
	if !ok {
		t.Fatal("success blocked")
	}
	finish(true)
	if !l.Allow("192.0.2.1:1234") || len(l.attempts) != 0 {
		t.Fatal("success did not reset failures")
	}
}

func TestLimiterBlocksAndRecovers(t *testing.T) {
	now := time.Now()
	limiter := NewLimiter()
	limiter.now = func() time.Time { return now }
	for range limiter.Limit {
		if !limiter.Allow("10.0.0.1:5000") {
			t.Fatal("blocked before reaching the limit")
		}
		limiter.Fail("10.0.0.1:5000")
	}
	if limiter.Allow("10.0.0.1:5000") {
		t.Fatal("the limit was not enforced")
	}
	if !limiter.Allow("10.0.0.2:5000") {
		t.Fatal("an unrelated address was blocked")
	}
	now = now.Add(limiter.Block + time.Second)
	if !limiter.Allow("10.0.0.1:5000") {
		t.Fatal("the block never expired")
	}
}

// Only failures create entries, so a spray from many addresses must not grow
// the table without bound.
func TestLimiterTableStaysBounded(t *testing.T) {
	now := time.Now()
	limiter := NewLimiter()
	limiter.now = func() time.Time { return now }
	for i := range maxTrackedClients * 3 {
		limiter.Fail(fmt.Sprintf("10.%d.%d.%d:5000", i>>16&0xff, i>>8&0xff, i&0xff))
	}
	if got := len(limiter.attempts); got > maxTrackedClients {
		t.Fatalf("tracked %d clients, want at most %d", got, maxTrackedClients)
	}

	// Once the failures age out, the table drains instead of staying full.
	now = now.Add(2 * (limiter.Window + limiter.Block))
	limiter.Fail("192.0.2.1:5000")
	if got := len(limiter.attempts); got > 2 {
		t.Fatalf("expired entries were kept: %d", got)
	}
}
