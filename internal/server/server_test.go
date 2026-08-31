package server

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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

type serviceStateCommander struct {
	state string
	err   error
}

type recoveryCommander struct {
	isActiveCalls int
	restarts      int
	activate      bool
}

func (c *recoveryCommander) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	if name == "systemctl" && len(args) > 0 {
		switch args[0] {
		case "is-active":
			c.isActiveCalls++
			if c.activate && c.isActiveCalls > 1 {
				return []byte("active"), nil
			}
			return []byte("inactive"), errors.New("exit status 3")
		case "restart":
			c.restarts++
			return nil, nil
		}
	}
	return []byte("sing-box version 1.12.0"), nil
}

type countingCommander struct {
	successCommander
	checks int
}

func (c *countingCommander) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if len(args) > 0 && args[0] == "check" {
		c.checks++
	}
	return c.successCommander.Run(ctx, name, args...)
}

func (c serviceStateCommander) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	if name == "systemctl" && len(args) > 0 && args[0] == "is-active" {
		return []byte(c.state), c.err
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

func TestLockedWebManagementHidesUIAndAPIButKeepsSubscription(t *testing.T) {
	s, cfg := testServer(t)
	cfg.WebManagementEnabled = false
	if err := s.Config.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/", nil),
		httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"admin","password":"secret"}`)),
		httptest.NewRequest(http.MethodGet, "/api/dashboard", nil),
	} {
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s %s status=%d", request.Method, request.URL.Path, response.Code)
		}
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s cache-control=%q", request.Method, request.URL.Path, response.Header().Get("Cache-Control"))
		}
	}
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/sub/"+cfg.SubscriptionToken, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("subscription status=%d body=%s", response.Code, response.Body.String())
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
	if !strings.Contains(response.Body.String(), `"sampleHealth":{"status":"waiting"`) || !strings.Contains(response.Body.String(), `"coreStatus":"running"`) {
		t.Fatalf("runtime status missing: %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"subscriptionName":"香港 / HY2"`) {
		t.Fatalf("subscription name missing: %s", response.Body.String())
	}
}

func TestDashboardCoreStatusIsThreeState(t *testing.T) {
	tests := []struct {
		name, state, want string
		err               error
	}{
		{name: "running", state: "active", want: "running"},
		{name: "stopped", state: "inactive", err: errors.New("exit status 3"), want: "stopped"},
		{name: "unknown", err: errors.New("Failed to connect to bus"), want: "unknown"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s, _ := testServer(t)
			s.Core.Commands = serviceStateCommander{state: test.state, err: test.err}
			response := httptest.NewRecorder()
			s.Handler().ServeHTTP(response, authenticatedRequest(t, s, http.MethodGet, "/api/dashboard", nil))
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"coreStatus":"`+test.want+`"`) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestDashboardEstimatesProviderUsage(t *testing.T) {
	s, cfg := testServer(t)
	cfg.TrafficQuota = model.TrafficQuotaConfig{AmountGB: 1000, BillingMode: model.TrafficBillingBidirectional, HeadroomPercent: 10}
	if err := s.Config.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Traffic.ApplySample(context.Background(), 100, 50); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, authenticatedRequest(t, s, http.MethodGet, "/api/dashboard", nil))
	body := response.Body.String()
	for _, expected := range []string{`"proxyUsedBytes":150`, `"estimatedProviderUsedBytes":300`, `"providerAllowanceBytes":1000000000000`, `"providerStopBytes":900000000000`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("dashboard missing %s: %s", expected, body)
		}
	}
}

func TestServerHealthKeepsPartialResultsWithoutSecrets(t *testing.T) {
	s, cfg := testServer(t)
	s.CertificatePath = filepath.Join(t.TempDir(), "cert.pem")
	if err := os.WriteFile(s.CertificatePath, []byte("not a certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	s.System.ProcRoot = t.TempDir()
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, authenticatedRequest(t, s, http.MethodGet, "/api/server", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, expected := range []string{`"id":"core","kind":"core","status":"ok"`, `"reason":"config_valid"`, `"reason":"certificate_invalid"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("health response missing %s: %s", expected, body)
		}
	}
	if strings.Contains(body, `"kind":"panel"`) {
		t.Fatalf("health response retained the redundant panel check: %s", body)
	}
	for _, secret := range []string{cfg.ClashAPISecret, cfg.SubscriptionToken, cfg.Inbounds[0].Hysteria2.Password} {
		if secret != "" && strings.Contains(body, secret) {
			t.Fatalf("health response leaked secret %q", secret)
		}
	}
}

func TestServerHealthTreatsQuotaPauseAsPlannedState(t *testing.T) {
	s, cfg := testServer(t)
	cfg.TrafficQuota = model.TrafficQuotaConfig{AmountGB: 0.000000001, BillingMode: model.TrafficBillingSingle}
	if err := s.Config.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Traffic.ApplySample(context.Background(), 1, 0); err != nil {
		t.Fatal(err)
	}
	s.Core.Commands = serviceStateCommander{state: "inactive", err: errors.New("exit status 3")}
	s.System.ProcRoot = procListeners(t, []int{cfg.PanelPort}, nil)

	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, authenticatedRequest(t, s, http.MethodGet, "/api/server", nil))
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, `"overall":"warning"`) {
		t.Fatalf("status=%d body=%s", response.Code, body)
	}
	if strings.Contains(body, `"kind":"listener_inbound"`) || strings.Contains(body, `"reason":"not_listening"`) {
		t.Fatalf("planned quota pause reported stopped inbounds as failures: %s", body)
	}
}

func TestServerHealthFlagsRunningCoreAfterQuotaExceeded(t *testing.T) {
	s, cfg := testServer(t)
	cfg.TrafficQuota = model.TrafficQuotaConfig{AmountGB: 0.000000001, BillingMode: model.TrafficBillingSingle}
	if err := s.Config.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Traffic.ApplySample(context.Background(), 1, 0); err != nil {
		t.Fatal(err)
	}
	s.System.ProcRoot = procListeners(t, []int{cfg.PanelPort}, []int{cfg.Inbounds[0].Port})

	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, authenticatedRequest(t, s, http.MethodGet, "/api/server", nil))
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, `"overall":"error"`) || !strings.Contains(body, `"reason":"quota_enforcement_failed"`) {
		t.Fatalf("status=%d body=%s", response.Code, body)
	}
}

func procListeners(t *testing.T, tcpPorts, udpPorts []int) string {
	t.Helper()
	root := t.TempDir()
	netDir := filepath.Join(root, "net")
	if err := os.MkdirAll(netDir, 0700); err != nil {
		t.Fatal(err)
	}
	header := "  sl  local_address rem_address   st\n"
	tables := map[string]string{"tcp": header, "tcp6": header, "udp": header, "udp6": header}
	for i, port := range tcpPorts {
		tables["tcp"] += fmt.Sprintf("%4d: 00000000:%04X 00000000:0000 0A\n", i, port)
	}
	for i, port := range udpPorts {
		tables["udp"] += fmt.Sprintf("%4d: 00000000:%04X 00000000:0000 07\n", i, port)
	}
	for name, data := range tables {
		if err := os.WriteFile(filepath.Join(netDir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestServerHealthCachesSlowConfigCheck(t *testing.T) {
	s, _ := testServer(t)
	commands := &countingCommander{}
	s.Core.Commands = commands
	for range 2 {
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, authenticatedRequest(t, s, http.MethodGet, "/api/server", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	}
	if commands.checks != 1 {
		t.Fatalf("config checked %d times, want one cached check", commands.checks)
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
	cfg.TrafficQuota = model.TrafficQuotaConfig{AmountGB: 123, BillingMode: model.TrafficBillingSingle, HeadroomPercent: 7}
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
	if got := s.Config.Get(); got.TrafficQuota != cfg.TrafficQuota {
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
	if err := s.Config.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	nextReset := model.ResetConfig{Mode: "monthly", Day: 8, Timezone: "Asia/Tokyo"}
	nextQuota := model.TrafficQuotaConfig{AmountGB: 1000, BillingMode: model.TrafficBillingBidirectional, HeadroomPercent: 10}
	response := httptest.NewRecorder()
	req := authenticatedRequest(t, s, http.MethodPut, "/api/settings/traffic", map[string]any{
		"trafficQuota": nextQuota,
		"reset":        nextReset,
	})
	s.Handler().ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	stored := s.Config.Get()
	if stored.TrafficQuota != nextQuota || stored.Reset != nextReset {
		t.Fatalf("traffic settings not stored: quota=%#v reset=%#v", stored.TrafficQuota, stored.Reset)
	}
	if !strings.Contains(response.Body.String(), `"effectiveLimitBytes":450000000000`) {
		t.Fatalf("calculated limit missing: %s", response.Body.String())
	}
	if stored.OutboundStrategy != model.OutboundStrategyPreferIPv6 {
		t.Fatalf("traffic save overwrote outbound strategy: %q", stored.OutboundStrategy)
	}
}

func TestTrafficSettingsRejectInvalidQuotaAndRemovedLegacyEndpoint(t *testing.T) {
	s, original := testServer(t)
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, authenticatedRequest(t, s, http.MethodPut, "/api/settings/traffic", map[string]any{
		"trafficQuota": map[string]any{"amountGB": 1, "unit": "TB", "billingMode": "both", "headroomPercent": 75},
		"reset":        original.Reset,
	}))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid quota status=%d body=%s", response.Code, response.Body.String())
	}
	if got := s.Config.Get().TrafficQuota; got != original.TrafficQuota {
		t.Fatalf("invalid quota was stored: %#v", got)
	}
	legacy := httptest.NewRecorder()
	s.Handler().ServeHTTP(legacy, authenticatedRequest(t, s, http.MethodPut, "/api/settings", map[string]any{"totalBytes": 1}))
	if legacy.Code != http.StatusNotFound {
		t.Fatalf("legacy settings endpoint status=%d body=%s", legacy.Code, legacy.Body.String())
	}
}

func TestSettingsReturnsAutomaticStrategyForEmptyConfig(t *testing.T) {
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
		t.Fatalf("empty strategy was not normalized: %s", response.Body.String())
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

func TestStoppedCoreCanRestartWhenTrafficAPIIsUnavailable(t *testing.T) {
	s, _ := testServer(t)
	requests := 0
	clash := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer clash.Close()
	s.Clash = traffic.ClashClient{URL: clash.URL, Client: clash.Client()}
	commands := &recoveryCommander{}
	s.Core.Commands = commands

	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, authenticatedRequest(t, s, http.MethodPost, "/api/core/restart", nil))
	if response.Code != http.StatusOK || commands.restarts != 1 || requests != 0 {
		t.Fatalf("status=%d restarts=%d clashRequests=%d body=%s", response.Code, commands.restarts, requests, response.Body.String())
	}
}

func TestStoppedCoreCanApplyProtocolChangeWhenTrafficAPIIsUnavailable(t *testing.T) {
	s, cfg := testServer(t)
	requests := 0
	clash := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer clash.Close()
	s.Clash = traffic.ClashClient{URL: clash.URL, Client: clash.Client()}
	commands := &recoveryCommander{activate: true}
	s.Core.Commands = commands
	inbound := cfg.Inbounds[0]
	inbound.Name = "recovered-node"

	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, authenticatedRequest(t, s, http.MethodPut, "/api/inbounds/"+inbound.ID, inbound))
	if response.Code != http.StatusOK || commands.restarts != 1 || requests != 0 {
		t.Fatalf("status=%d restarts=%d clashRequests=%d body=%s", response.Code, commands.restarts, requests, response.Body.String())
	}
	if got := s.Config.Get().Inbounds[0].Name; got != inbound.Name {
		t.Fatalf("protocol name=%q want=%q", got, inbound.Name)
	}
}

func TestSubscriptionContentAndHeaders(t *testing.T) {
	s, cfg := testServer(t)
	cfg.TrafficQuota = model.TrafficQuotaConfig{AmountGB: 1000, BillingMode: model.TrafficBillingBidirectional, HeadroomPercent: 10}
	if err := s.Config.Replace(cfg); err != nil {
		t.Fatal(err)
	}
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
	if userinfo := response.Header().Get("Subscription-Userinfo"); !strings.Contains(userinfo, "total=450000000000") || response.Header().Get("Profile-Title") == "" {
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
