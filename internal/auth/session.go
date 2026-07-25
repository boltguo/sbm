package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const CookieName = "sbm_session"

type Session struct {
	Username      string `json:"u"`
	CSRF          string `json:"c"`
	CredentialTag string `json:"v"`
	ExpiresAt     int64  `json:"e"`
}

type Sessions struct {
	Secret   []byte
	Lifetime time.Duration
}

func (s Sessions) Sign(session Session) (string, error) {
	payload, err := json.Marshal(session)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, s.Secret)
	_, _ = mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
func (s Sessions) Verify(value string, now time.Time) (Session, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return Session{}, errors.New("invalid session")
	}
	provided, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Session{}, errors.New("invalid session")
	}
	mac := hmac.New(sha256.New, s.Secret)
	_, _ = mac.Write([]byte(parts[0]))
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return Session{}, errors.New("invalid session")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Session{}, errors.New("invalid session")
	}
	var session Session
	if json.Unmarshal(payload, &session) != nil || session.Username == "" || session.CSRF == "" || session.CredentialTag == "" || now.Unix() >= session.ExpiresAt {
		return Session{}, errors.New("expired session")
	}
	return session, nil
}
func (s Sessions) SetCookie(w http.ResponseWriter, value string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: value, Path: "/", Expires: expires, MaxAge: int(time.Until(expires).Seconds()), HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
}
func (s Sessions) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
}

type attempt struct {
	Count        int
	Window       time.Time
	BlockedUntil time.Time
}

// maxTrackedClients bounds the failed-login table. Entries are only created by
// failures, so without a bound a spray from many source addresses would pin
// memory for as long as the panel runs.
const maxTrackedClients = 4096

type Limiter struct {
	mu            sync.Mutex
	attempts      map[string]attempt
	Limit         int
	Window, Block time.Duration
	now           func() time.Time
}

func NewLimiter() *Limiter {
	return &Limiter{attempts: make(map[string]attempt), Limit: 5, Window: 15 * time.Minute, Block: 15 * time.Minute, now: time.Now}
}

// sweep drops entries that no longer restrict anyone. If a genuine distributed
// attempt keeps the table full of live entries, the oldest ones are evicted so
// the table stays bounded; those addresses simply get a fresh budget.
func (l *Limiter) sweep(now time.Time) {
	var oldest time.Time
	oldestKey := ""
	for key, item := range l.attempts {
		if now.After(item.BlockedUntil) && now.Sub(item.Window) >= l.Window {
			delete(l.attempts, key)
			continue
		}
		if oldestKey == "" || item.Window.Before(oldest) {
			oldest, oldestKey = item.Window, key
		}
	}
	if len(l.attempts) >= maxTrackedClients && oldestKey != "" {
		delete(l.attempts, oldestKey)
	}
}

func (l *Limiter) Allow(remote string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	key := ClientIP(remote)
	item := l.attempts[key]
	if now.Before(item.BlockedUntil) {
		return false
	}
	if item.Window.IsZero() || now.Sub(item.Window) >= l.Window {
		delete(l.attempts, key)
	}
	return true
}
func (l *Limiter) Fail(remote string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	key := ClientIP(remote)
	item, tracked := l.attempts[key]
	if !tracked && len(l.attempts) >= maxTrackedClients {
		l.sweep(now)
	}
	if item.Window.IsZero() || now.Sub(item.Window) >= l.Window {
		item = attempt{Window: now}
	}
	item.Count++
	if item.Count >= l.Limit {
		item.BlockedUntil = now.Add(l.Block)
	}
	l.attempts[key] = item
}
func (l *Limiter) Success(remote string) {
	l.mu.Lock()
	delete(l.attempts, ClientIP(remote))
	l.mu.Unlock()
}

func ClientIP(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err == nil {
		return host
	}
	return remote
}
