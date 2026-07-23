package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/boltguo/sbm/internal/auth"
	"github.com/boltguo/sbm/internal/core"
	"github.com/boltguo/sbm/internal/model"
	"github.com/boltguo/sbm/internal/protocol"
	"github.com/boltguo/sbm/internal/releasecheck"
	"github.com/boltguo/sbm/internal/store"
	"github.com/boltguo/sbm/internal/systeminfo"
	"github.com/boltguo/sbm/internal/traffic"
	"golang.org/x/crypto/bcrypt"
)

type successCommander struct{}

func (successCommander) Run(context.Context, string, ...string) ([]byte, error) {
	return []byte("sing-box version 1.12.0"), nil
}

type fakeReleases struct {
	calls int
	info  releasecheck.Info
	err   error
}

func (f *fakeReleases) Latest(context.Context) (releasecheck.Info, error) {
	f.calls++
	return f.info, f.err
}

func testServer(t *testing.T) (*Server, model.Config) {
	t.Helper()
	dir := t.TempDir()
	secret := strings.Repeat("x", 43)
	hash, _ := bcrypt.GenerateFromPassword([]byte("old-password-123"), bcrypt.MinCost)
	cfg := model.DefaultConfig()
	cfg.Domain = "node.example.com"
	cfg.AdminPasswordHash = string(hash)
	cfg.SessionSecret = secret
	cfg.ClashAPISecret = secret
	cfg.SubscriptionToken = secret
	cfg.Inbounds = []model.Inbound{{ID: "hy2", Type: protocol.TypeHysteria2, Name: "香港 / HY2", Enabled: true, Port: 443, Hysteria2: &model.Hysteria2Options{Password: "password-123"}}}
	cfgStore := store.NewConfigStore(filepath.Join(dir, "config.json"), cfg)
	registry := protocol.DefaultRegistry()
	manager := &core.Manager{Binary: "/fake/sing-box", ConfigPath: filepath.Join(dir, "sing-box.json"), Service: "sing-box.service", Commands: successCommander{}, Renderer: core.Renderer{Registry: registry, BuildContext: protocol.BuildContext{CertificatePath: "/cert", KeyPath: "/key"}}}
	tracker := traffic.NewForTest(model.DefaultState(time.Now()), cfgStore, manager, time.Now)
	assets, _ := fs.Sub(fstest.MapFS{"dist/index.html": &fstest.MapFile{Data: []byte("index")}}, "dist")
	return &Server{Config: cfgStore, Traffic: tracker, Core: manager, Registry: registry, Factory: protocol.Factory{}, System: systeminfo.New(), Assets: assets, Limiter: auth.NewLimiter(), Sessions: auth.Sessions{Secret: []byte(secret), Lifetime: time.Hour}, PanelVersion: "0.1.0"}, cfg
}

func authenticatedRequest(t *testing.T, s *Server, method, target string, body any) *http.Request {
	t.Helper()
	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, target, &payload)
	value, err := s.Sessions.Sign(auth.Session{Username: "admin", CSRF: "csrf-token", CredentialTag: credentialTag(s.Config.Get()), ExpiresAt: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: value})
	req.Header.Set("X-CSRF-Token", "csrf-token")
	return req
}

func TestUnauthenticatedAPIRejected(t *testing.T) {
	s, _ := testServer(t)
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/dashboard", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestDashboardSeparatesPanelAndCoreVersions(t *testing.T) {
	s, _ := testServer(t)
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, authenticatedRequest(t, s, http.MethodGet, "/api/dashboard", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"panelVersion":"0.1.0"`) || !strings.Contains(response.Body.String(), `"coreVersion":"sing-box version 1.12.0"`) {
		t.Fatalf("panel and core versions missing: %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"trafficAudit":{"status":"unavailable"`) {
		t.Fatalf("traffic audit status missing: %s", response.Body.String())
	}
}

func TestUpdateCheckFindsNewReleaseAndCachesResult(t *testing.T) {
	s, _ := testServer(t)
	releases := &fakeReleases{info: releasecheck.Info{TagName: "v0.2.0", URL: "https://github.com/boltguo/sbm/releases/tag/v0.2.0"}}
	s.Releases = releases
	for range 2 {
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, authenticatedRequest(t, s, http.MethodGet, "/api/update", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		if !strings.Contains(response.Body.String(), `"updateAvailable":true`) || !strings.Contains(response.Body.String(), `"latestVersion":"v0.2.0"`) {
			t.Fatalf("unexpected update response: %s", response.Body.String())
		}
	}
	if releases.calls != 1 {
		t.Fatalf("release API called %d times, want 1", releases.calls)
	}
}

func TestFailedLoginIsAuditedWithoutCredentials(t *testing.T) {
	s, _ := testServer(t)
	var audit bytes.Buffer
	s.AuditLog = log.New(&audit, "", 0)
	response := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"Username":"admin","Password":"do-not-log-this"}`))
	s.Handler().ServeHTTP(response, req)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(audit.String(), "event=login") || !strings.Contains(audit.String(), "result=failed") || strings.Contains(audit.String(), "do-not-log-this") {
		t.Fatalf("unexpected audit log %q", audit.String())
	}
}

func TestChangePassword(t *testing.T) {
	s, _ := testServer(t)
	response := httptest.NewRecorder()
	req := authenticatedRequest(t, s, http.MethodPost, "/api/settings/password", map[string]string{"currentPassword": "old-password-123", "newPassword": "new-password-456"})
	s.Handler().ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	hash := s.Config.Get().AdminPasswordHash
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("new-password-456")) != nil {
		t.Fatal("new password was not stored")
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("old-password-123")) == nil {
		t.Fatal("old password still accepted")
	}
}

func TestSubscriptionContentAndHeaders(t *testing.T) {
	s, cfg := testServer(t)
	response := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/sub/"+cfg.SubscriptionToken, nil)
	req.Header.Set("Accept", "text/plain")
	s.Handler().ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	decoded, err := base64.StdEncoding.DecodeString(response.Body.String())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(decoded), "hysteria2://") || !strings.Contains(string(decoded), "#%E9%A6%99%E6%B8%AF%20%2F%20HY2") {
		t.Fatalf("unexpected subscription %q", decoded)
	}
	if response.Header().Get("Subscription-Userinfo") == "" || response.Header().Get("Profile-Title") == "" {
		t.Fatal("subscription headers missing")
	}
}

func TestSubscriptionURLAndTitleUseNodeBaseName(t *testing.T) {
	s, cfg := testServer(t)
	cfg.Inbounds[0].Name = "Japan-Tokyo-HY2"
	if err := s.Config.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	wantFragment := "#Japan-Tokyo"
	if got := subscriptionURL(cfg); !strings.HasSuffix(got, wantFragment) {
		t.Fatalf("subscription URL %q does not end with %q", got, wantFragment)
	}

	response := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/sub/"+cfg.SubscriptionToken, nil)
	req.Header.Set("Accept", "text/plain")
	s.Handler().ServeHTTP(response, req)
	title, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(response.Header().Get("Profile-Title"), "base64:"))
	if err != nil || string(title) != "Japan-Tokyo" {
		t.Fatalf("unexpected profile title %q: %v", title, err)
	}
}

func TestCSRFRequired(t *testing.T) {
	s, _ := testServer(t)
	response := httptest.NewRecorder()
	req := authenticatedRequest(t, s, http.MethodPost, "/api/core/restart", map[string]string{})
	req.Header.Del("X-CSRF-Token")
	s.Handler().ServeHTTP(response, req)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestServerStatusAPI(t *testing.T) {
	s, _ := testServer(t)
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, authenticatedRequest(t, s, http.MethodGet, "/api/server", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"cpuCores"`) || !strings.Contains(response.Body.String(), `"diskTotal"`) {
		t.Fatal("server status fields missing")
	}
}
