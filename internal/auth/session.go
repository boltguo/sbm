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

func (l *Limiter) Allow(remote string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	key := clientIP(remote)
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
	key := clientIP(remote)
	item := l.attempts[key]
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
	delete(l.attempts, clientIP(remote))
	l.mu.Unlock()
}

func clientIP(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err == nil {
		return host
	}
	return remote
}
