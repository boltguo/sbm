package auth

import (
	"fmt"
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
