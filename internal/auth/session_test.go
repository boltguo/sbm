package auth

import (
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
