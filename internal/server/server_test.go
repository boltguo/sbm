package server

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
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

func (successCommander) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	if name == "systemctl" && len(args) > 0 && args[0] == "is-active" {
		return []byte("active"), nil
	}
	return []byte("sing-box version 1.12.0"), nil
}

type checkFailureCommander struct{ successCommander }

func (c checkFailureCommander) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if len(args) > 0 && args[0] == "check" {
		return []byte("invalid configuration"), errors.New("exit status 1")
	}
	return c.successCommander.Run(ctx, name, args...)
}

type disconnectOnRestartCommander struct {
	cancel context.CancelFunc
}

func (c disconnectOnRestartCommander) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if name == "systemctl" && len(args) > 0 {
		switch args[0] {
		case "restart":
			c.cancel()
			return nil, nil
		case "is-active":
			return []byte("active"), nil
		}
	}
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
	if !strings.Contains(response.Body.String(), `"subscriptionName":"香港 / HY2"`) {
		t.Fatalf("subscription name missing: %s", response.Body.String())
	}
}

func TestListInboundsIncludesEnabledWireGuardCompanion(t *testing.T) {
	s, cfg := testServer(t)
	cfg.WireGuardExit = testWireGuardExit(t)
	if err := protocol.EnsureWireGuardExitCredential(&cfg.Inbounds[0]); err != nil {
		t.Fatal(err)
	}
	if err := s.Config.Replace(cfg); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, authenticatedRequest(t, s, http.MethodGet, "/api/inbounds", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var inbounds []struct {
		WireGuardNode *companionNodeView `json:"wireGuardNode"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &inbounds); err != nil {
		t.Fatal(err)
	}
	if len(inbounds) != 1 || inbounds[0].WireGuardNode == nil {
		t.Fatalf("WireGuard companion missing: %s", response.Body.String())
	}
	if got := inbounds[0].WireGuardNode; got.Name != "香港 / HY2 · via GCP" || !strings.HasPrefix(got.Link, "hysteria2://") {
		t.Fatalf("unexpected WireGuard companion: %#v", got)
	}

	cfg.Inbounds[0].Enabled = false
	if err := s.Config.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	s.Handler().ServeHTTP(response, authenticatedRequest(t, s, http.MethodGet, "/api/inbounds", nil))
	if strings.Contains(response.Body.String(), `"wireGuardNode"`) {
		t.Fatalf("disabled inbound exposed a WireGuard companion: %s", response.Body.String())
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

// GitHub allows 60 unauthenticated calls per hour, so a failure must not be
// retried on every click.
func TestUpdateCheckBacksOffAfterFailure(t *testing.T) {
	s, _ := testServer(t)
	releases := &fakeReleases{err: errors.New("rate limited")}
	s.Releases = releases
	for range 5 {
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, authenticatedRequest(t, s, http.MethodGet, "/api/update", nil))
		if response.Code != http.StatusBadGateway {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	}
	if releases.calls != 1 {
		t.Fatalf("release API called %d times during backoff, want 1", releases.calls)
	}

	// Once the backoff expires the next click reaches GitHub again.
	s.releaseRetryAt = time.Now().Add(-time.Second)
	releases.err, releases.info = nil, releasecheck.Info{TagName: "v9.9.9", URL: "https://github.com/boltguo/sbm/releases/tag/v9.9.9"}
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, authenticatedRequest(t, s, http.MethodGet, "/api/update", nil))
	if response.Code != http.StatusOK || releases.calls != 2 {
		t.Fatalf("status=%d calls=%d", response.Code, releases.calls)
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

func TestSettingsUpdatesOutboundStrategyAndCoreConfig(t *testing.T) {
	s, cfg := testServer(t)
	cfg.TotalBytes = 123456789
	cfg.WireGuardExit = &model.WireGuardExitConfig{Server: "wireguard-draft"}
	if err := s.Config.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	req := authenticatedRequest(t, s, http.MethodPut, "/api/settings/outbound", map[string]any{
		"outboundStrategy": model.OutboundStrategyPreferIPv6,
	})
	s.Handler().ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if got := s.Config.Get().OutboundStrategy; got != model.OutboundStrategyPreferIPv6 {
		t.Fatalf("outbound strategy=%q", got)
	}
	if got := s.Config.Get(); got.TotalBytes != cfg.TotalBytes || got.WireGuardExit == nil || got.WireGuardExit.Server != "wireguard-draft" {
		t.Fatalf("outbound save overwrote unrelated settings: %#v", got)
	}
	coreConfig, err := os.ReadFile(s.Core.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(coreConfig), `"strategy": "prefer_ipv6"`) || !strings.Contains(string(coreConfig), `"type": "local"`) {
		t.Fatalf("core config missing IPv6 preference: %s", coreConfig)
	}
}

func TestTrafficSettingsOnlyUpdateTrafficFields(t *testing.T) {
	s, cfg := testServer(t)
	cfg.OutboundStrategy = model.OutboundStrategyPreferIPv6
	cfg.WireGuardExit = &model.WireGuardExitConfig{Server: "draft.example.invalid"}
	if err := s.Config.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	nextReset := model.ResetConfig{Mode: "monthly", Day: 8, Timezone: "Asia/Tokyo"}
	response := httptest.NewRecorder()
	req := authenticatedRequest(t, s, http.MethodPut, "/api/settings/traffic", map[string]any{
		"totalBytes": int64(42 * 1024 * 1024),
		"reset":      nextReset,
	})
	s.Handler().ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	stored := s.Config.Get()
	if stored.TotalBytes != 42*1024*1024 || stored.Reset != nextReset {
		t.Fatalf("traffic settings not stored: total=%d reset=%#v", stored.TotalBytes, stored.Reset)
	}
	if stored.OutboundStrategy != model.OutboundStrategyPreferIPv6 {
		t.Fatalf("traffic save overwrote outbound strategy: %q", stored.OutboundStrategy)
	}
	if stored.WireGuardExit == nil || stored.WireGuardExit.Server != "draft.example.invalid" {
		t.Fatalf("traffic save overwrote WireGuard draft: %#v", stored.WireGuardExit)
	}
}

func TestWireGuardDraftPreservesOutboundStrategy(t *testing.T) {
	s, cfg := testServer(t)
	cfg.OutboundStrategy = model.OutboundStrategyPreferIPv6
	if err := s.Config.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	draft := model.DefaultWireGuardExitConfig()
	draft.Server = "203.0.113.15"
	response := httptest.NewRecorder()
	req := authenticatedRequest(t, s, http.MethodPut, "/api/settings/wireguard", map[string]any{
		"wireGuardExit": draft,
	})
	s.Handler().ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	stored := s.Config.Get()
	if stored.OutboundStrategy != model.OutboundStrategyPreferIPv6 {
		t.Fatalf("disabled WireGuard draft changed outbound strategy: %q", stored.OutboundStrategy)
	}
	if stored.WireGuardExit == nil || stored.WireGuardExit.Server != draft.Server || stored.WireGuardExit.Enabled {
		t.Fatalf("WireGuard draft was not stored: %#v", stored.WireGuardExit)
	}
}

func TestWireGuardDraftAppliesBuiltInDefaults(t *testing.T) {
	s, _ := testServer(t)
	response := httptest.NewRecorder()
	req := authenticatedRequest(t, s, http.MethodPut, "/api/settings/wireguard", map[string]any{
		"wireGuardExit": map[string]any{
			"enabled": false,
			"server":  " 203.0.113.15 ",
		},
	})
	s.Handler().ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	stored := s.Config.Get().WireGuardExit
	if stored == nil || stored.Label != "GCP" || stored.Server != "203.0.113.15" || stored.ServerPort != 51820 {
		t.Fatalf("WireGuard defaults were not applied: %#v", stored)
	}
}

func TestSettingsReturnsAutomaticStrategyForLegacyConfig(t *testing.T) {
	s, cfg := testServer(t)
	cfg.OutboundStrategy = ""
	if err := s.Config.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, authenticatedRequest(t, s, http.MethodGet, "/api/settings", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"outboundStrategy":"auto"`) {
		t.Fatalf("legacy config was not normalized: %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"wireGuardExit":{"enabled":false`) || !strings.Contains(response.Body.String(), `"serverPort":51820`) {
		t.Fatalf("legacy WireGuard defaults missing: %s", response.Body.String())
	}
}

func TestGenerateWireGuardKeypair(t *testing.T) {
	s, _ := testServer(t)
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, authenticatedRequest(t, s, http.MethodPost, "/api/settings/wireguard/keypair", map[string]any{}))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var keys protocol.WireGuardKeys
	if err := json.Unmarshal(response.Body.Bytes(), &keys); err != nil {
		t.Fatal(err)
	}
	derived, err := protocol.WireGuardPublicKey(keys.Private)
	if err != nil || derived != keys.Public {
		t.Fatalf("invalid generated keypair: %#v, %v", keys, err)
	}
}

func TestSettingsEnablesWireGuardExitAndCoreConfig(t *testing.T) {
	s, cfg := testServer(t)
	cfg.TotalBytes = 987654321
	cfg.Reset = model.ResetConfig{Mode: "monthly", Day: 12, Timezone: "UTC"}
	if err := s.Config.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	exit := testWireGuardExit(t)
	response := httptest.NewRecorder()
	req := authenticatedRequest(t, s, http.MethodPut, "/api/settings/wireguard", map[string]any{
		"wireGuardExit": exit,
	})
	s.Handler().ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	stored := s.Config.Get().WireGuardExit
	if stored == nil || !stored.Enabled || stored.Server != exit.Server {
		t.Fatalf("WireGuard exit was not stored: %#v", stored)
	}
	if got := s.Config.Get(); got.OutboundStrategy != cfg.OutboundStrategy || got.TotalBytes != cfg.TotalBytes || got.Reset != cfg.Reset {
		t.Fatalf("WireGuard save did not preserve unrelated settings: %#v", got)
	}
	if got := s.Config.Get().Inbounds[0].Hysteria2.WireGuardExitPassword; got == "" {
		t.Fatal("WireGuard companion credential was not generated")
	}
	coreConfig, err := os.ReadFile(s.Core.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(coreConfig)
	for _, expected := range []string{`"type": "wireguard"`, `"tag": "exit-wireguard"`, `"final": "direct"`, `"allowed_ips"`, `"auth_user"`, `"action": "resolve"`, `"outbound": "exit-wireguard"`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("core config missing %s: %s", expected, text)
		}
	}
	if strings.Contains(text, `"default_domain_resolver"`) {
		t.Fatalf("WireGuard companion nodes changed the global resolver: %s", text)
	}
}

func TestWireGuardApplySurvivesProxyDisconnect(t *testing.T) {
	s, _ := testServer(t)
	req := authenticatedRequest(t, s, http.MethodPut, "/api/settings/wireguard", map[string]any{
		"wireGuardExit": testWireGuardExit(t),
	})
	requestCtx, cancel := context.WithCancel(req.Context())
	s.Core.Commands = disconnectOnRestartCommander{cancel: cancel}
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, req.WithContext(requestCtx))

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if stored := s.Config.Get().WireGuardExit; stored == nil || !stored.Enabled {
		t.Fatalf("client disconnect rolled back WireGuard settings: %#v", stored)
	}
}

func TestSettingsRollsBackWireGuardExitWhenCoreCheckFails(t *testing.T) {
	s, _ := testServer(t)
	s.Core.Commands = checkFailureCommander{}
	response := httptest.NewRecorder()
	req := authenticatedRequest(t, s, http.MethodPut, "/api/settings/wireguard", map[string]any{
		"wireGuardExit": testWireGuardExit(t),
	})
	s.Handler().ServeHTTP(response, req)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if got := s.Config.Get().WireGuardExit; got != nil {
		t.Fatalf("failed WireGuard exit was not rolled back: %#v", got)
	}
	if got := s.Config.Get().OutboundStrategy; got != model.OutboundStrategyAuto {
		t.Fatalf("forced outbound strategy was not rolled back: %q", got)
	}
}

func TestSettingsRollsBackOutboundStrategyWhenCoreCheckFails(t *testing.T) {
	s, _ := testServer(t)
	s.Core.Commands = checkFailureCommander{}
	response := httptest.NewRecorder()
	req := authenticatedRequest(t, s, http.MethodPut, "/api/settings/outbound", map[string]any{
		"outboundStrategy": model.OutboundStrategyPreferIPv6,
	})
	s.Handler().ServeHTTP(response, req)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if got := s.Config.Get().OutboundStrategy; got != model.OutboundStrategyAuto {
		t.Fatalf("failed strategy change was not rolled back: %q", got)
	}
}

func testWireGuardExit(t *testing.T) *model.WireGuardExitConfig {
	t.Helper()
	localKeys, err := protocol.GenerateWireGuardKeys()
	if err != nil {
		t.Fatal(err)
	}
	peerKeys, err := protocol.GenerateWireGuardKeys()
	if err != nil {
		t.Fatal(err)
	}
	return &model.WireGuardExitConfig{
		Enabled: true, Label: "GCP", Server: "203.0.113.10", ServerPort: 51820,
		PrivateKey: localKeys.Private, PeerPublicKey: peerKeys.Public,
	}
}

func TestSettingsRejectsUnknownOutboundStrategy(t *testing.T) {
	s, _ := testServer(t)
	response := httptest.NewRecorder()
	req := authenticatedRequest(t, s, http.MethodPut, "/api/settings/outbound", map[string]any{
		"outboundStrategy": "fastest_magic",
	})
	s.Handler().ServeHTTP(response, req)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if got := s.Config.Get().OutboundStrategy; got != model.OutboundStrategyAuto {
		t.Fatalf("invalid outbound strategy was stored: %q", got)
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

func TestSubscriptionShowsWireGuardCompanionOnlyWhileEnabled(t *testing.T) {
	s, cfg := testServer(t)
	cfg.WireGuardExit = testWireGuardExit(t)
	if err := protocol.EnsureWireGuardExitCredential(&cfg.Inbounds[0]); err != nil {
		t.Fatal(err)
	}
	exitPassword := cfg.Inbounds[0].Hysteria2.WireGuardExitPassword
	if err := s.Config.Replace(cfg); err != nil {
		t.Fatal(err)
	}

	readLinks := func() []string {
		t.Helper()
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
		return strings.Split(string(decoded), "\n")
	}

	links := readLinks()
	if len(links) != 2 || !strings.Contains(links[1], exitPassword) || !strings.Contains(links[1], "via%20GCP") {
		t.Fatalf("unexpected enabled subscription: %#v", links)
	}
	cfg.WireGuardExit.Enabled = false
	if err := s.Config.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	links = readLinks()
	if len(links) != 1 || strings.Contains(links[0], exitPassword) {
		t.Fatalf("disabled companion remained in subscription: %#v", links)
	}
	if got := s.Config.Get().Inbounds[0].Hysteria2.WireGuardExitPassword; got != exitPassword {
		t.Fatalf("disabled companion credential was discarded: %q", got)
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

func TestFirewallReclaimsOnlyUnusedPorts(t *testing.T) {
	s, cfg := testServer(t)
	cfg.PanelPort = 2096
	vless := model.Inbound{ID: "vless", Type: protocol.TypeVLESSReality, Name: "node", Enabled: true, Port: 8443}
	hy2 := cfg.Inbounds[0] // udp/443

	cases := []struct {
		name       string
		old, next  []model.Inbound
		wantOpen   []endpoint
		wantClosed []endpoint
	}{
		{
			name:     "new inbound opens its port",
			old:      []model.Inbound{hy2},
			next:     []model.Inbound{hy2, vless},
			wantOpen: []endpoint{{"tcp", 8443}},
		},
		{
			name:       "port change releases the old port",
			old:        []model.Inbound{withPort(vless, 8443)},
			next:       []model.Inbound{withPort(vless, 9443)},
			wantOpen:   []endpoint{{"tcp", 9443}},
			wantClosed: []endpoint{{"tcp", 8443}},
		},
		{
			name:       "deleting an inbound releases its port",
			old:        []model.Inbound{hy2, vless},
			next:       []model.Inbound{hy2},
			wantClosed: []endpoint{{"tcp", 8443}},
		},
		{
			name:       "disabling an inbound releases its port",
			old:        []model.Inbound{withEnabled(vless, true)},
			next:       []model.Inbound{withEnabled(vless, false)},
			wantClosed: []endpoint{{"tcp", 8443}},
		},
		{
			name: "a port another inbound still uses is kept",
			old:  []model.Inbound{withPort(vless, 8443), withID(withPort(vless, 8443), "twin")},
			next: []model.Inbound{withID(withPort(vless, 8443), "twin")},
		},
		{
			name: "the panel port is never released",
			old:  []model.Inbound{withPort(vless, 2096)},
			next: []model.Inbound{},
		},
		{
			name: "TCP/80 is never released",
			old:  []model.Inbound{withPort(vless, 80)},
			next: []model.Inbound{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			old, next := cfg, cfg
			old.Inbounds, next.Inbounds = tc.old, tc.next
			before, after := s.firewallEndpoints(old), s.firewallEndpoints(next)

			var opened, closed []endpoint
			for item := range after {
				if !before[item] {
					opened = append(opened, item)
				}
			}
			for item := range before {
				if !after[item] && !isProtectedEndpoint(item, next) {
					closed = append(closed, item)
				}
			}
			assertEndpoints(t, "opened", opened, tc.wantOpen)
			assertEndpoints(t, "closed", closed, tc.wantClosed)
		})
	}
}

func withPort(in model.Inbound, port int) model.Inbound   { in.Port = port; return in }
func withID(in model.Inbound, id string) model.Inbound    { in.ID = id; return in }
func withEnabled(in model.Inbound, on bool) model.Inbound { in.Enabled = on; return in }

func assertEndpoints(t *testing.T, label string, got, want []endpoint) {
	t.Helper()
	slices.SortFunc(got, func(a, b endpoint) int { return cmp.Or(cmp.Compare(a.network, b.network), cmp.Compare(a.port, b.port)) })
	if len(got) != len(want) || !slices.Equal(got, want) {
		t.Fatalf("%s=%v want=%v", label, got, want)
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
