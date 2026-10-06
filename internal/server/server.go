package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/boltguo/sbm/internal/auth"
	"github.com/boltguo/sbm/internal/core"
	"github.com/boltguo/sbm/internal/geo"
	"github.com/boltguo/sbm/internal/health"
	"github.com/boltguo/sbm/internal/model"
	"github.com/boltguo/sbm/internal/protocol"
	"github.com/boltguo/sbm/internal/releasecheck"
	"github.com/boltguo/sbm/internal/store"
	"github.com/boltguo/sbm/internal/systeminfo"
	"github.com/boltguo/sbm/internal/traffic"
	"golang.org/x/crypto/bcrypt"
)

type Server struct {
	Geo             geo.Lookup
	Config          *store.ConfigStore
	Traffic         *traffic.Tracker
	Core            *core.Manager
	Registry        *protocol.Registry
	Factory         protocol.Factory
	Clash           traffic.ClashClient
	System          *systeminfo.Collector
	Assets          fs.FS
	Limiter         *auth.Limiter
	Sessions        auth.Sessions
	PanelVersion    string
	Releases        releasecheck.Source
	AuditLog        *log.Logger
	CertificatePath string
	mutationMu      sync.Mutex
	healthMu        sync.Mutex
	healthSlow      []health.Check
	healthUntil     time.Time
	releaseMu       sync.Mutex
	releaseCache    *updateStatus
	releaseUntil    time.Time
	releaseRetryAt  time.Time
}

const releaseRetryDelay = 2 * time.Minute

type contextKey string

const sessionKey contextKey = "session"

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("POST /api/login", s.requireManagement(http.HandlerFunc(s.login)))
	mux.Handle("/api/", s.requireManagement(s.requireAuth(http.HandlerFunc(s.api))))
	mux.HandleFunc("GET /sub/{token}", s.subscription)
	mux.Handle("/", s.requireManagement(http.HandlerFunc(s.static)))
	return securityHeaders(mux)
}

func (s *Server) requireManagement(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Config != nil && !s.Config.Get().WebManagementEnabled {
			w.Header().Set("Cache-Control", "no-store")
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/sub/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.Limiter.Allow(r.RemoteAddr) {
		s.auditLogin(r, "blocked")
		writeError(w, http.StatusTooManyRequests, "登录尝试过于频繁，请稍后再试")
		return
	}
	var input struct{ Username, Password string }
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, 400, "请求格式无效")
		return
	}
	cfg := s.Config.Get()
	userOK := subtle.ConstantTimeCompare([]byte(input.Username), []byte(cfg.AdminUsername)) == 1
	passwordOK := bcrypt.CompareHashAndPassword([]byte(cfg.AdminPasswordHash), []byte(input.Password)) == nil
	if !userOK || !passwordOK {
		s.Limiter.Fail(r.RemoteAddr)
		s.auditLogin(r, "failed")
		time.Sleep(250 * time.Millisecond)
		writeError(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	s.Limiter.Success(r.RemoteAddr)
	csrf, err := protocol.RandomToken(24)
	if err != nil {
		writeError(w, 500, "无法创建会话")
		return
	}
	expires := time.Now().Add(s.Sessions.Lifetime)
	value, err := s.Sessions.Sign(auth.Session{Username: cfg.AdminUsername, CSRF: csrf, CredentialTag: credentialTag(cfg), ExpiresAt: expires.Unix()})
	if err != nil {
		writeError(w, 500, "无法创建会话")
		return
	}
	s.Sessions.SetCookie(w, value, expires)
	s.auditLogin(r, "succeeded")
	writeJSON(w, 200, map[string]any{"username": cfg.AdminUsername, "csrfToken": csrf})
}

func (s *Server) auditLogin(r *http.Request, result string) {
	message := fmt.Sprintf("audit event=login remote=%s result=%s", auth.ClientIP(r.RemoteAddr), result)
	if s.AuditLog != nil {
		s.AuditLog.Print(message)
		return
	}
	log.Print(message)
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(auth.CookieName)
		if err != nil {
			writeError(w, 401, "请先登录")
			return
		}
		session, err := s.Sessions.Verify(cookie.Value, time.Now())
		if err != nil {
			s.Sessions.ClearCookie(w)
			writeError(w, 401, "会话已失效，请重新登录")
			return
		}
		if subtle.ConstantTimeCompare([]byte(session.CredentialTag), []byte(credentialTag(s.Config.Get()))) != 1 {
			s.Sessions.ClearCookie(w)
			writeError(w, 401, "凭据已变更，请重新登录")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(session.CSRF)) != 1 {
				writeError(w, 403, "CSRF 校验失败")
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey, session)))
	})
}

func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == "GET" && r.URL.Path == "/api/me":
		s.me(w, r)
	case r.Method == "POST" && r.URL.Path == "/api/logout":
		s.Sessions.ClearCookie(w)
		writeJSON(w, 200, map[string]bool{"ok": true})
	case r.Method == "GET" && r.URL.Path == "/api/dashboard":
		s.dashboard(w, r)
	case r.Method == "GET" && r.URL.Path == "/api/update":
		s.checkUpdate(w, r)
	case r.Method == "GET" && r.URL.Path == "/api/server":
		s.serverStatus(w, r)
	case r.Method == "POST" && r.URL.Path == "/api/core/restart":
		s.restart(w, r)
	case r.Method == "POST" && r.URL.Path == "/api/traffic/reset":
		s.resetTraffic(w, r)
	case r.Method == "GET" && r.URL.Path == "/api/traffic/history":
		s.trafficHistory(w, r)
	case r.Method == "GET" && r.URL.Path == "/api/inbounds":
		s.listInbounds(w, r)
	case r.Method == "POST" && r.URL.Path == "/api/inbounds":
		s.createInbound(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/inbounds/"):
		s.inboundByID(w, r)
	case r.Method == "GET" && r.URL.Path == "/api/egress":
		s.listGateways(w, r)
	case r.Method == "POST" && r.URL.Path == "/api/egress":
		s.createGateway(w, r)
	case r.Method == "POST" && r.URL.Path == "/api/egress/keypair":
		s.generateWireGuardKeypair(w, r)
	case r.Method == "POST" && r.URL.Path == "/api/egress/public-key":
		s.wireGuardPublicKey(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/egress/"):
		s.gatewayByID(w, r)
	case r.Method == "GET" && r.URL.Path == "/api/settings":
		s.getSettings(w, r)
	case r.Method == "PUT" && r.URL.Path == "/api/settings/traffic":
		s.updateTrafficSettings(w, r)
	case r.Method == "PUT" && r.URL.Path == "/api/settings/outbound":
		s.updateOutboundSettings(w, r)
	case r.Method == "POST" && r.URL.Path == "/api/settings/token":
		s.regenerateToken(w, r)
	case r.Method == "POST" && r.URL.Path == "/api/settings/password":
		s.changePassword(w, r)
	default:
		writeError(w, 404, "接口不存在")
	}
}

func (s *Server) serverStatus(w http.ResponseWriter, r *http.Request) {
	snapshot := systeminfo.Snapshot{CollectedAt: time.Now()}
	if s.System != nil {
		snapshot = s.System.Snapshot(r.Context())
	}
	report := s.healthReport(r.Context(), snapshot)
	writeJSON(w, 200, struct {
		systeminfo.Snapshot
		Health healthReport `json:"health"`
	}{Snapshot: snapshot, Health: report})
}

type healthReport struct {
	Overall   health.Status  `json:"overall"`
	Checks    []health.Check `json:"checks"`
	CheckedAt time.Time      `json:"checkedAt"`
}

func (s *Server) healthReport(ctx context.Context, snapshot systeminfo.Snapshot) healthReport {
	now := time.Now().UTC()
	checks := make([]health.Check, 0)
	quotaExceeded := s.Traffic != nil && s.Traffic.State().QuotaExceeded
	coreKnown, coreActive := false, false
	if s.Core == nil {
		checks = append(checks, health.Check{ID: "core", Kind: "core", Status: health.StatusUnknown, Reason: "state_unavailable", CheckedAt: now})
	} else if active, err := s.Core.Active(ctx); err != nil {
		checks = append(checks, health.Check{ID: "core", Kind: "core", Status: health.StatusUnknown, Reason: "state_unavailable", CheckedAt: now})
	} else {
		coreKnown, coreActive = true, active
		switch {
		case quotaExceeded && active:
			checks = append(checks, health.Check{ID: "core", Kind: "core", Status: health.StatusError, Reason: "quota_enforcement_failed", CheckedAt: now})
		case quotaExceeded:
			checks = append(checks, health.Check{ID: "core", Kind: "core", Status: health.StatusWarning, Reason: "quota_paused", CheckedAt: now})
		case active:
			checks = append(checks, health.Check{ID: "core", Kind: "core", Status: health.StatusOK, Reason: "running", CheckedAt: now})
		default:
			checks = append(checks, health.Check{ID: "core", Kind: "core", Status: health.StatusError, Reason: "stopped", CheckedAt: now})
		}
	}

	checks = append(checks, s.trafficHealth(now, coreKnown, coreActive))
	checks = append(checks, s.slowHealthChecks(ctx, now)...)
	checks = append(checks, s.listenerHealth(now, quotaExceeded && coreKnown && !coreActive)...)
	checks = append(checks, health.Disk(snapshot.DiskPercent, snapshot.DiskTotal, now))
	checks = append(checks, s.resetHealth(now))

	return healthReport{
		Overall: health.Overall(checks), Checks: checks, CheckedAt: now,
	}
}

func (s *Server) trafficHealth(now time.Time, coreKnown, coreActive bool) health.Check {
	check := health.Check{ID: "traffic", Kind: "traffic", Status: health.StatusUnknown, Reason: "sample_waiting", CheckedAt: now}
	if s.Traffic == nil {
		check.Reason = "sample_unavailable"
		return check
	}
	state, sample := s.Traffic.State(), s.Traffic.SampleHealth()
	if state.QuotaExceeded && !s.Traffic.UsesVnStat() {
		if coreKnown && coreActive {
			check.Status, check.Reason = health.StatusError, "quota_enforcement_failed"
		} else {
			check.Status, check.Reason = health.StatusWarning, "quota_paused"
		}
		check.LastSuccessAt = timePointer(sample.LastSuccessAt)
		return check
	}
	check.LastSuccessAt, check.FailureSince = timePointer(sample.LastSuccessAt), timePointer(sample.FailureSince)
	switch sample.Status {
	case traffic.SampleStatusHealthy:
		check.Status, check.Reason = health.StatusOK, "sample_healthy"
	case traffic.SampleStatusInterrupted:
		check.Status, check.Reason = health.StatusError, "sample_interrupted"
	}
	return check
}

func (s *Server) resetHealth(now time.Time) health.Check {
	check := health.Check{ID: "reset", Kind: "reset", Status: health.StatusOK, Reason: "reset_disabled", CheckedAt: now}
	if s.Config == nil {
		check.Status, check.Reason = health.StatusUnknown, "reset_unavailable"
		return check
	}
	cfg := s.Config.Get()
	check.Timezone = cfg.Reset.Timezone
	if cfg.Reset.Mode != "monthly" {
		return check
	}
	next := time.Time{}
	if s.Traffic != nil {
		next = s.Traffic.State().NextResetAt
		if s.Traffic.UsesVnStat() {
			next = s.Traffic.NetworkState(traffic.EntryNetworkScope).NextResetAt
		}
	}
	if next.IsZero() {
		check.Status, check.Reason = health.StatusError, "reset_schedule_missing"
		return check
	}
	check.Reason, check.NextResetAt = "reset_scheduled", timePointer(next)
	return check
}

func timePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copy := value
	return &copy
}

func (s *Server) slowHealthChecks(ctx context.Context, now time.Time) []health.Check {
	s.healthMu.Lock()
	defer s.healthMu.Unlock()
	if now.Before(s.healthUntil) && len(s.healthSlow) > 0 {
		return append([]health.Check(nil), s.healthSlow...)
	}
	checks := make([]health.Check, 0)
	configCheck := health.Check{ID: "config", Kind: "config", Status: health.StatusUnknown, Reason: "config_unavailable", CheckedAt: now}
	if s.Core != nil {
		checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := s.Core.CheckConfig(checkCtx)
		cancel()
		switch {
		case err == nil:
			configCheck.Status, configCheck.Reason = health.StatusOK, "config_valid"
		case errors.Is(err, core.ErrConfigCheckTimeout):
			configCheck.Status, configCheck.Reason = health.StatusError, "config_timeout"
		default:
			configCheck.Status, configCheck.Reason = health.StatusError, "config_invalid"
		}
	}
	checks = append(checks, configCheck, health.Certificate(s.CertificatePath, now))

	s.healthSlow = append([]health.Check(nil), checks...)
	s.healthUntil = now.Add(15 * time.Second)
	return checks
}

// Listener state is intentionally not cached. Reading /proc is cheap, and a
// stale result right after a restart makes the Doctor report the wrong state.
// During a planned quota pause, enabled proxy inbounds are expected not to
// listen, so only the management listener remains meaningful.
func (s *Server) listenerHealth(now time.Time, quotaPaused bool) []health.Check {
	if s.Config == nil {
		return nil
	}
	procRoot := "/proc"
	if s.System != nil && s.System.ProcRoot != "" {
		procRoot = s.System.ProcRoot
	}
	cfg := s.Config.Get()
	checks := make([]health.Check, 0)
	endpoints := []health.Endpoint{{ID: "listener-panel", Kind: "listener_panel", Protocol: "tcp", Port: cfg.PanelPort}}
	if !quotaPaused {
		for i, inbound := range cfg.Inbounds {
			if !inbound.Enabled {
				continue
			}
			if s.Registry == nil {
				checks = append(checks, health.Check{ID: fmt.Sprintf("listener-inbound-%d", i+1), Kind: "listener_inbound", Status: health.StatusUnknown, Reason: "listener_unavailable", CheckedAt: now, Port: inbound.Port})
				continue
			}
			driver, ok := s.Registry.Get(inbound.Type)
			if !ok {
				checks = append(checks, health.Check{ID: fmt.Sprintf("listener-inbound-%d", i+1), Kind: "listener_inbound", Status: health.StatusUnknown, Reason: "listener_unavailable", CheckedAt: now, Port: inbound.Port})
				continue
			}
			endpoints = append(endpoints, health.Endpoint{ID: fmt.Sprintf("listener-inbound-%d", i+1), Kind: "listener_inbound", Protocol: driver.Network(), Port: inbound.Port})
		}
	}
	return append(checks, health.Listeners(procRoot, endpoints, now)...)
}

func (s *Server) invalidateHealth() {
	s.healthMu.Lock()
	s.healthSlow, s.healthUntil = nil, time.Time{}
	s.healthMu.Unlock()
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	session := r.Context().Value(sessionKey).(auth.Session)
	writeJSON(w, 200, map[string]any{"username": session.Username, "csrfToken": session.CSRF})
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	cfg, state := s.Config.Get(), s.Traffic.State()
	active, activeErr := s.Core.Active(r.Context())
	coreStatus := "unknown"
	if activeErr == nil && active {
		coreStatus = "running"
	} else if activeErr == nil {
		coreStatus = "stopped"
	}
	coreVersion := s.Core.Version(r.Context())
	limit, err := cfg.EffectiveTrafficLimitBytes()
	if err != nil {
		writeError(w, 500, "计算流量限额失败")
		return
	}
	allowance, err := cfg.TrafficQuota.AllowanceBytes()
	if err != nil {
		writeError(w, 500, "计算套餐流量失败")
		return
	}
	factor := cfg.TrafficQuota.ProviderUsageFactor()
	estimatedProviderUsed := multiplySaturating(state.Total(), factor)
	providerStop := multiplySaturating(limit, factor)
	providerRemaining := int64(0)
	progress := float64(0)
	if allowance > 0 {
		providerRemaining = max(0, allowance-estimatedProviderUsed)
		progress = min(100, float64(estimatedProviderUsed)/float64(allowance)*100)
	}
	upload, download := state.Upload, state.Download
	source, networkInterface, networkReason := "sing-box", "", ""
	networkAvailable, networkPartial := false, false
	periodStarted, nextReset := state.PeriodStartedAt, state.NextResetAt
	sampleHealth := s.Traffic.SampleHealth()
	if s.Traffic.UsesVnStat() {
		st := state.Network[traffic.EntryNetworkScope]
		source, networkInterface, networkReason = "vnstat", st.Interface, st.Reason
		networkAvailable, networkPartial = st.Available, st.Partial
		upload, download = st.TX, st.RX
		limit, _ = cfg.TrafficQuota.NetworkStopBytes()
		providerStop = limit
		estimatedProviderUsed = cfg.TrafficQuota.NetworkUsage(st.RX, st.TX)
		providerRemaining = max(0, allowance-estimatedProviderUsed)
		progress = 0
		if allowance > 0 {
			progress = min(100, float64(estimatedProviderUsed)/float64(allowance)*100)
		}
		periodStarted, nextReset = st.PeriodStartedAt, st.NextResetAt
		sampleHealth = traffic.SampleHealth{Status: st.Status, LastSuccessAt: st.UpdatedAt}
		if sampleHealth.Status == "" {
			sampleHealth.Status = "waiting"
		}
	}
	if state.QuotaExceeded && !s.Traffic.UsesVnStat() {
		sampleHealth.Status = "paused"
	}
	writeJSON(w, 200, map[string]any{
		"egressGateways": s.gatewayUsage(cfg),
		"coreStatus":     coreStatus, "coreVersion": coreVersion, "panelVersion": s.PanelVersion,
		"upload": upload, "download": download, "proxyUsedBytes": state.Total(),
		"trafficSource": source, "networkInterface": networkInterface, "networkReason": networkReason, "networkAvailable": networkAvailable, "networkPartial": networkPartial,
		"trafficQuota": cfg.TrafficQuota, "effectiveLimitBytes": limit,
		"providerAllowanceBytes": allowance, "estimatedProviderUsedBytes": estimatedProviderUsed,
		"providerStopBytes": providerStop, "providerRemainingBytes": providerRemaining, "providerProgress": progress,
		"periodStartedAt": periodStarted, "nextResetAt": nextReset,
		"quotaExceeded": state.QuotaExceeded, "sampleHealth": sampleHealth,
		"persistenceHealth": s.Traffic.PersistenceHealth(),
		"subscriptionURL":   subscriptionURL(cfg), "subscriptionName": subscriptionName(cfg),
	})
}

func multiplySaturating(value, factor int64) int64 {
	if value <= 0 || factor <= 0 {
		return 0
	}
	if value > (1<<63-1)/factor {
		return 1<<63 - 1
	}
	return value * factor
}

type updateStatus struct {
	CurrentVersion  string    `json:"currentVersion"`
	LatestVersion   string    `json:"latestVersion"`
	UpdateAvailable bool      `json:"updateAvailable"`
	ReleaseURL      string    `json:"releaseURL"`
	CheckedAt       time.Time `json:"checkedAt"`
}

func (s *Server) checkUpdate(w http.ResponseWriter, r *http.Request) {
	if s.Releases == nil {
		writeError(w, http.StatusServiceUnavailable, "版本检查不可用")
		return
	}
	s.releaseMu.Lock()
	defer s.releaseMu.Unlock()
	now := time.Now()
	if s.releaseCache != nil && now.Before(s.releaseUntil) {
		writeJSON(w, http.StatusOK, *s.releaseCache)
		return
	}
	// GitHub allows 60 unauthenticated calls per hour per address. Without a
	// backoff, a rate-limited or unreachable API would be retried on every
	// click and keep the panel pinned at the limit.
	if now.Before(s.releaseRetryAt) {
		writeError(w, http.StatusBadGateway, "无法从 GitHub 获取最新版本")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	info, err := s.Releases.Latest(ctx)
	if err != nil {
		s.releaseRetryAt = now.Add(releaseRetryDelay)
		writeError(w, http.StatusBadGateway, "无法从 GitHub 获取最新版本")
		return
	}
	status := updateStatus{
		CurrentVersion:  s.PanelVersion,
		LatestVersion:   info.TagName,
		UpdateAvailable: releasecheck.IsNewer(info.TagName, s.PanelVersion),
		ReleaseURL:      info.URL,
		CheckedAt:       now.UTC(),
	}
	s.releaseCache = &status
	s.releaseUntil = now.Add(15 * time.Minute)
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) restart(w http.ResponseWriter, r *http.Request) {
	if s.Traffic.State().QuotaExceeded {
		writeError(w, 409, "已达到代理安全阈值，请先重置流量或提高限额")
		return
	}
	if err := s.captureTraffic(r.Context()); err != nil {
		writeError(w, 503, err.Error())
		return
	}
	if s.Traffic.State().QuotaExceeded {
		writeError(w, 409, "已达到代理安全阈值，请先重置流量或提高限额")
		return
	}
	if err := s.Traffic.Persist(); err != nil {
		writeError(w, 500, "保存流量状态失败")
		return
	}
	if err := s.Core.Restart(r.Context()); err != nil {
		writeError(w, 500, "重启 sing-box 失败")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) resetTraffic(w http.ResponseWriter, r *http.Request) {
	if !s.Traffic.State().QuotaExceeded {
		if err := s.captureTraffic(r.Context()); err != nil {
			writeError(w, 503, err.Error())
			return
		}
	}
	if err := s.Traffic.Reset(r.Context()); err != nil {
		writeError(w, 500, "重置流量失败")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

type inboundView struct {
	model.Inbound
	Link        string           `json:"link"`
	Network     string           `json:"network"`
	EgressNodes []egressNodeView `json:"egressNodes"`
}

func (s *Server) listInbounds(w http.ResponseWriter, _ *http.Request) {
	cfg := s.Config.Get()
	result := make([]inboundView, 0, len(cfg.Inbounds))
	for _, in := range cfg.Inbounds {
		d, _ := s.Registry.Get(in.Type)
		link, _ := d.ShareLink(in, protocol.ShareContext{Domain: cfg.Domain})
		nodes := make([]egressNodeView, 0)
		if in.Enabled {
			for _, g := range model.OrderedGateways(cfg.EgressGateways) {
				if !g.Enabled {
					continue
				}
				if variant, ok := protocol.EgressVariant(in, g); ok {
					if link, err := d.ShareLink(variant, protocol.ShareContext{Domain: cfg.Domain}); err == nil {
						nodes = append(nodes, egressNodeView{GatewayID: g.ID, Name: variant.Name, Link: link, Marker: g.Marker, Location: protocol.GatewayLocation(g)})
					}
				}
			}
		}
		result = append(result, inboundView{Inbound: in, Link: link, Network: d.Network(), EgressNodes: nodes})
	}
	writeJSON(w, 200, result)
}

func (s *Server) createInbound(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Type, Name string
		Port       int
	}
	if decodeJSON(r, &input) != nil {
		writeError(w, 400, "请求格式无效")
		return
	}
	inbound, err := s.Factory.New(r.Context(), input.Type, strings.TrimSpace(input.Name), input.Port)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := s.mutate(r.Context(), func(cfg *model.Config) { cfg.Inbounds = append(cfg.Inbounds, inbound) }); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 201, inbound)
}

func (s *Server) inboundByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/inbounds/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, 404, "协议不存在")
		return
	}
	switch r.Method {
	case http.MethodPut:
		var input model.Inbound
		if decodeJSON(r, &input) != nil {
			writeError(w, 400, "请求格式无效")
			return
		}
		input.ID = id
		oldType := ""
		for _, item := range s.Config.Get().Inbounds {
			if item.ID == id {
				oldType = item.Type
				break
			}
		}
		if oldType == "" {
			writeError(w, 404, "协议不存在")
			return
		}
		if err := s.mutate(r.Context(), func(cfg *model.Config) {
			for i := range cfg.Inbounds {
				if cfg.Inbounds[i].ID == id {
					oldType = cfg.Inbounds[i].Type
					input.Type = oldType
					input.EgressCredentials = cfg.Inbounds[i].EgressCredentials
					cfg.Inbounds[i] = input
					return
				}
			}
		}); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, input)
	case http.MethodDelete:
		found := false
		for _, item := range s.Config.Get().Inbounds {
			if item.ID == id {
				found = true
				break
			}
		}
		if !found {
			writeError(w, 404, "协议不存在")
			return
		}
		if err := s.mutate(r.Context(), func(cfg *model.Config) {
			result := cfg.Inbounds[:0]
			for _, item := range cfg.Inbounds {
				if item.ID == id {
					found = true
					continue
				}
				result = append(result, item)
			}
			cfg.Inbounds = result
		}); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	default:
		writeError(w, 405, "请求方法不支持")
	}
}

func (s *Server) mutate(ctx context.Context, change func(*model.Config)) error {
	return s.mutateChange(ctx, func(cfg *model.Config) error { change(cfg); return nil })
}
func (s *Server) mutateChange(ctx context.Context, change func(*model.Config) error) error {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	releaseEgress := s.Traffic.BeginEgressChange(ctx)
	defer releaseEgress()
	if err := s.captureTraffic(ctx); err != nil {
		return err
	}
	old := s.Config.Get()
	next := s.Config.Get()
	if err := change(&next); err != nil {
		return err
	}
	if err := protocol.SyncEgressCredentials(&next); err != nil {
		return err
	}
	if err := s.Registry.ValidateConfig(next); err != nil {
		return err
	}
	if err := s.Config.Replace(next); err != nil {
		return errors.New("保存业务配置失败")
	}
	if err := s.Traffic.Persist(); err != nil {
		_ = s.Config.Replace(old)
		return errors.New("保存流量状态失败")
	}
	// Applying a core configuration intentionally restarts sing-box. When the
	// administrator reaches the panel through that same proxy, the restart
	// cancels the HTTP request. Finish the already-persisted transaction with
	// its own deadline so a dropped client connection cannot cause a rollback.
	applyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	before, _ := s.Core.Renderer.Render(old)
	after, _ := s.Core.Renderer.Render(next)
	if !bytes.Equal(before, after) || !equalDirectInbounds(old.Inbounds, next.Inbounds) {
		if err := s.Core.Apply(applyCtx, next, s.Traffic.State().QuotaExceeded); err != nil {
			_ = s.Config.Replace(old)
			return err
		}
	}
	// Accounting failure is a statistics warning, never grounds to undo a
	// successfully running proxy or stop Direct/other gateways.
	if err := s.Traffic.CommitEgressChange(applyCtx, old, next); err != nil {
		log.Print("egress: accounting or state persistence unavailable")
	}

	s.syncHostFirewall(old, next)
	s.invalidateHealth()
	return nil
}

func equalDirectInbounds(old, next []model.Inbound) bool {
	// Draft egress credentials are persisted before activation but do not
	// change the running core. Keep the existing Direct recovery behavior for
	// inbound edits, including display names, without restarting for drafts.
	direct := func(inbounds []model.Inbound) []model.Inbound {
		if inbounds == nil {
			return nil
		}
		result := make([]model.Inbound, len(inbounds))
		copy(result, inbounds)
		for i := range result {
			result[i].EgressCredentials = nil
		}
		return result
	}
	return reflect.DeepEqual(direct(old), direct(next))
}

func (s *Server) captureTraffic(ctx context.Context) error {
	if s.Clash.URL == "" || s.Traffic.State().QuotaExceeded {
		return nil
	}
	// A stopped service has no final counter left to read. Recovery actions must
	// remain available even though the Clash API disappeared with the process.
	if s.Core != nil {
		active, err := s.Core.Active(ctx)
		if err == nil && !active {
			return nil
		}
	}
	_, err := s.Traffic.Sample(ctx, s.Clash)
	if errors.Is(err, traffic.ErrSampleUnavailable) {
		return errors.New("无法读取核心流量，请稍后重试")
	}
	return err
}

func (s *Server) saveConfig(change func(*model.Config)) error {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	old := s.Config.Get()
	next := old
	next.Inbounds = append([]model.Inbound(nil), old.Inbounds...)
	change(&next)
	if err := s.Registry.ValidateConfig(next); err != nil {
		return err
	}
	if err := s.Config.Replace(next); err != nil {
		return errors.New("保存业务配置失败")
	}
	return nil
}

func (s *Server) getSettings(w http.ResponseWriter, _ *http.Request) {
	cfg := s.Config.Get()
	outboundStrategy := cfg.OutboundStrategy
	if outboundStrategy == "" {
		outboundStrategy = model.OutboundStrategyAuto
	}
	writeJSON(w, 200, map[string]any{
		"domain": cfg.Domain, "panelPort": cfg.PanelPort, "trafficQuota": cfg.TrafficQuota, "reset": cfg.Reset, "vnstatInterface": cfg.VnStatInterface,
		"outboundStrategy": outboundStrategy,
		"subscriptionURL":  subscriptionURL(cfg),
	})
}

func (s *Server) updateTrafficSettings(w http.ResponseWriter, r *http.Request) {
	var input struct {
		TrafficQuota    model.TrafficQuotaConfig `json:"trafficQuota"`
		VnStatInterface *string                  `json:"vnstatInterface"`
		Reset           model.ResetConfig        `json:"reset"`
	}
	if decodeJSON(r, &input) != nil {
		writeError(w, 400, "请求格式无效")
		return
	}
	if err := s.saveConfig(func(cfg *model.Config) {
		cfg.TrafficQuota = input.TrafficQuota
		if input.VnStatInterface != nil {
			cfg.VnStatInterface = strings.TrimSpace(*input.VnStatInterface)
		}
		cfg.Reset = input.Reset
	}); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := s.Traffic.UpdateSchedule(); err != nil {
		writeError(w, 500, "更新重置周期失败")
		return
	}
	if s.Traffic.UsesVnStat() {
		_ = s.Traffic.SampleNetwork(r.Context(), traffic.EntryNetworkScope)
	}
	if err := s.Traffic.ReconcileQuota(r.Context()); err != nil {
		writeError(w, 500, "应用流量限额失败")
		return
	}
	limit, _ := input.TrafficQuota.EffectiveBytes()
	if s.Traffic.UsesVnStat() {
		limit, _ = input.TrafficQuota.NetworkStopBytes()
	}
	writeJSON(w, 200, map[string]any{"ok": true, "effectiveLimitBytes": limit, "trafficQuota": input.TrafficQuota})
}

func (s *Server) updateOutboundSettings(w http.ResponseWriter, r *http.Request) {
	var input struct {
		OutboundStrategy string `json:"outboundStrategy"`
	}
	if decodeJSON(r, &input) != nil {
		writeError(w, 400, "请求格式无效")
		return
	}
	if input.OutboundStrategy == "" {
		input.OutboundStrategy = model.OutboundStrategyAuto
	}
	current := s.Config.Get()
	currentStrategy := current.OutboundStrategy
	if currentStrategy == "" {
		currentStrategy = model.OutboundStrategyAuto
	}
	if currentStrategy == input.OutboundStrategy {
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	if err := s.mutate(r.Context(), func(cfg *model.Config) {
		cfg.OutboundStrategy = input.OutboundStrategy
	}); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) regenerateToken(w http.ResponseWriter, r *http.Request) {
	token, err := protocol.RandomToken(32)
	if err != nil {
		writeError(w, 500, "生成 Token 失败")
		return
	}
	if err := s.saveConfig(func(cfg *model.Config) { cfg.SubscriptionToken = token }); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"subscriptionURL": subscriptionURL(s.Config.Get())})
}
func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var input struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if decodeJSON(r, &input) != nil {
		writeError(w, 400, "请求格式无效")
		return
	}
	if len(input.NewPassword) < 12 || len(input.NewPassword) > 128 {
		writeError(w, 400, "新密码长度必须为 12 到 128 个字符")
		return
	}
	cfg := s.Config.Get()
	if bcrypt.CompareHashAndPassword([]byte(cfg.AdminPasswordHash), []byte(input.CurrentPassword)) != nil {
		writeError(w, 403, "当前密码错误")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, 500, "密码处理失败")
		return
	}
	if err := s.saveConfig(func(cfg *model.Config) { cfg.AdminPasswordHash = string(hash) }); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	s.Sessions.ClearCookie(w)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) subscription(w http.ResponseWriter, r *http.Request) {
	cfg, state := s.Config.Get(), s.Traffic.State()
	provided := r.PathValue("token")
	if subtle.ConstantTimeCompare([]byte(provided), []byte(cfg.SubscriptionToken)) != 1 {
		writeError(w, 404, "订阅不存在")
		return
	}
	if state.QuotaExceeded {
		writeError(w, 403, "已达到代理安全阈值，请等待重置")
		return
	}
	links := make([]string, 0, len(cfg.Inbounds))
	for _, inbound := range cfg.Inbounds {
		if !inbound.Enabled {
			continue
		}
		d, _ := s.Registry.Get(inbound.Type)
		link, err := d.ShareLink(inbound, protocol.ShareContext{Domain: cfg.Domain})
		if err == nil {
			links = append(links, link)
		}
	}

	for _, g := range model.OrderedGateways(cfg.EgressGateways) {
		if !g.Enabled {
			continue
		}
		for _, in := range cfg.Inbounds {
			if !in.Enabled {
				continue
			}
			if variant, ok := protocol.EgressVariant(in, g); ok {
				d, _ := s.Registry.Get(in.Type)
				if link, err := d.ShareLink(variant, protocol.ShareContext{Domain: cfg.Domain}); err == nil {
					links = append(links, link)
				}
			}
		}
	}
	payload := base64.StdEncoding.EncodeToString([]byte(strings.Join(links, "\n")))
	effectiveLimit, err := cfg.EffectiveTrafficLimitBytes()
	if err != nil {
		writeError(w, 500, "计算流量限额失败")
		return
	}
	upload, download := state.Upload, state.Download
	if s.Traffic.UsesVnStat() {
		st := state.Network[traffic.EntryNetworkScope]
		upload, download = st.TX, st.RX
		if cfg.TrafficQuota.BillingMode == model.TrafficBillingSingle {
			download = 0
		}
		effectiveLimit, _ = cfg.TrafficQuota.NetworkStopBytes()
	}
	w.Header().Set("Subscription-Userinfo", fmt.Sprintf("upload=%d; download=%d; total=%d; expire=0", upload, download, effectiveLimit))
	w.Header().Set("Profile-Update-Interval", "12")
	w.Header().Set("Profile-Title", "base64:"+base64.StdEncoding.EncodeToString([]byte(subscriptionName(cfg))))
	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if strings.HasPrefix(strings.ToLower(r.Header.Get("Accept-Language")), "zh") {
			_, _ = fmt.Fprintf(w, "<!doctype html><html lang=zh-CN><meta charset=utf-8><meta name=viewport content='width=device-width'><title>SBM 订阅</title><style>body{font:16px sans-serif;max-width:600px;margin:12vh auto;padding:24px;background:#111;color:#eee}small{color:#aaa}</style><h1>订阅可用</h1><p>%d 个已启用节点</p><small>请将地址复制到支持 Base64 订阅的代理客户端中。</small>", len(links))
		} else {
			_, _ = fmt.Fprintf(w, "<!doctype html><html lang=en><meta charset=utf-8><meta name=viewport content='width=device-width'><title>SBM subscription</title><style>body{font:16px sans-serif;max-width:600px;margin:12vh auto;padding:24px;background:#111;color:#eee}small{color:#aaa}</style><h1>Subscription ready</h1><p>%d enabled nodes</p><small>Copy this URL into a proxy client that supports Base64 subscriptions.</small>", len(links))
		}
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(payload))
}

func subscriptionURL(cfg model.Config) string {
	u := &url.URL{
		Scheme:   "https",
		Host:     net.JoinHostPort(cfg.Domain, strconv.Itoa(cfg.PanelPort)),
		Path:     "/sub/" + cfg.SubscriptionToken,
		Fragment: subscriptionName(cfg),
	}
	return u.String()
}

func subscriptionName(cfg model.Config) string {
	for _, enabledOnly := range []bool{true, false} {
		for _, inbound := range cfg.Inbounds {
			if enabledOnly && !inbound.Enabled {
				continue
			}
			name := strings.TrimSpace(inbound.Name)
			if name == "" {
				continue
			}
			upperName := strings.ToUpper(name)
			for _, suffix := range []string{"-VLESS", "-HY2"} {
				if strings.HasSuffix(upperName, suffix) {
					if base := strings.TrimSpace(name[:len(name)-len(suffix)]); base != "" {
						return base
					}
				}
			}
			return name
		}
	}
	return cfg.Domain
}

func credentialTag(cfg model.Config) string {
	sum := sha256.Sum256([]byte(cfg.SessionSecret + "\x00" + cfg.AdminPasswordHash))
	return base64.RawURLEncoding.EncodeToString(sum[:16])
}

const firewallHelper = "/usr/local/lib/sbm/open-port.sh"

type endpoint struct {
	network string
	port    int
}

// firewallEndpoints lists the ports a config actually needs open. A disabled
// inbound is not listening, so its port is not included.
func (s *Server) firewallEndpoints(cfg model.Config) map[endpoint]bool {
	result := make(map[endpoint]bool, len(cfg.Inbounds))
	for _, inbound := range cfg.Inbounds {
		if !inbound.Enabled {
			continue
		}
		if driver, ok := s.Registry.Get(inbound.Type); ok {
			result[endpoint{driver.Network(), inbound.Port}] = true
		}
	}
	return result
}

// syncHostFirewall opens what the new config needs and revokes what it no
// longer uses, so a port change or a deleted inbound does not leave the host
// firewall open forever. Ports the panel itself depends on are never revoked.
func (s *Server) syncHostFirewall(old, next model.Config) {
	before, after := s.firewallEndpoints(old), s.firewallEndpoints(next)
	for item := range after {
		if !before[item] {
			s.hostFirewall("", item)
		}
	}
	for item := range before {
		if after[item] || isProtectedEndpoint(item, next) {
			continue
		}
		s.hostFirewall("--close", item)
	}
}

// TCP/80 keeps Let's Encrypt renewal working and the panel port keeps the
// operator from locking themselves out; neither is ever closed here.
func isProtectedEndpoint(item endpoint, cfg model.Config) bool {
	return item.network == "tcp" && (item.port == 80 || item.port == cfg.PanelPort)
}

func (s *Server) hostFirewall(action string, item endpoint) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	port := strconv.Itoa(item.port)
	args := []string{item.network, port}
	if action != "" {
		args = append([]string{action}, args...)
	}
	if info, err := os.Stat(firewallHelper); err == nil && info.Mode().IsRegular() && info.Mode()&0111 != 0 {
		if err := exec.CommandContext(ctx, firewallHelper, args...).Run(); err == nil {
			return
		} else {
			log.Printf("host firewall helper failed: action=%q network=%s port=%d error=%v", action, item.network, item.port, err)
		}
	}
	// Fallback for installs whose helper is missing or predates --close.
	status, err := exec.CommandContext(ctx, "ufw", "status").Output()
	if err != nil || !strings.Contains(string(status), "Status: active") {
		return
	}
	rule := fmt.Sprintf("%s/%s", port, item.network)
	if action == "--close" {
		_ = exec.CommandContext(ctx, "ufw", "delete", "allow", rule).Run()
		return
	}
	_ = exec.CommandContext(ctx, "ufw", "allow", rule).Run()
}

func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, 405, "请求方法不支持")
		return
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "." || name == "" {
		name = "index.html"
	}
	data, err := fs.ReadFile(s.Assets, name)
	if err != nil {
		data, err = fs.ReadFile(s.Assets, "index.html")
		name = "index.html"
	}
	if err != nil {
		writeError(w, 404, "页面不存在")
		return
	}
	if kind := mime.TypeByExtension(path.Ext(name)); kind != "" {
		w.Header().Set("Content-Type", kind)
	} else {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	if strings.Contains(name, ".") && name != "index.html" {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-store")
	}
	_, _ = w.Write(data)
}

func decodeJSON(r *http.Request, target any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, (64<<10)+1))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("请求只能包含一个 JSON 值")
	}
	return nil
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, message string) {
	english := map[string]string{
		"sing-box 配置校验失败":              "The sing-box configuration check failed.",
		"sing-box 启动失败，读取旧配置备份失败":      "sing-box failed to start and the previous configuration backup could not be read.",
		"sing-box 启动失败，恢复旧配置失败":        "sing-box failed to start and the previous configuration could not be restored.",
		"sing-box 启动失败，旧配置已恢复但服务未恢复":   "The previous configuration was restored, but sing-box could not be restarted.",
		"sing-box 启动失败，候选配置已撤销":        "sing-box failed to start; the initial candidate configuration was removed.",
		"sing-box 启动失败，已恢复上一份配置":       "sing-box failed to start; the previous configuration and service were restored.",
		"保存业务配置失败":                     "Could not save the business configuration.",
		"中继出口不存在":                      "Gateway not found.",
		"生成中继出口 ID 失败":                 "Could not generate a gateway ID.",
		"中继出口地址槽已用完":                   "No free gateway tunnel slots remain.",
		"中继出口 ID 无效":                   "The gateway ID is invalid.",
		"中继出口隧道地址槽无效":                  "The gateway tunnel slot is invalid.",
		"出口标记和位置不得超过 80 字符或包含控制字符":     "Marker and location must be at most 80 characters without control characters.",
		"中继出口服务器必须是有效的公网 IPv4 地址":      "Enter a valid public IPv4 address for the gateway.",
		"WireGuard UDP 端口无效":           "The WireGuard UDP port is invalid.",
		"WireGuard 密钥必须是 32 字节 Base64": "WireGuard keys must be canonical Base64 encoding of 32 bytes.",
		"WireGuard 私钥格式无效":             "The WireGuard private key is invalid.",
		"无法生成 WireGuard 密钥":            "Could not generate WireGuard keys.",
		"中继出口 ID 或隧道地址槽重复":             "Gateway IDs and tunnel slots must be unique.",
		"中继出口 IPv4 和 UDP 端口组合必须唯一":     "Each gateway must use a unique IPv4 and UDP port pair.",
		"重置出口流量失败":                     "Could not reset gateway traffic.",
		"中继出口凭据重复":                     "Duplicate gateway credentials.",
		"中继出口凭据关联无效或重复":                "Gateway credential references are invalid or duplicated.",
		"中继出口 UUID 无效":                 "The gateway UUID is invalid.",
		"中继出口密码无效":                     "The gateway password is invalid.",
		"入站认证凭据必须唯一":                   "Inbound authentication credentials must be unique.",
		"认证用户重复":                       "Duplicate authentication users.",
		"缺少中继出口凭据":                     "Gateway credentials are missing.",
		"生成中继出口凭据失败":                   "Could not generate gateway credentials.",
		"不支持中继出口的协议":                   "This protocol does not support egress gateways.",
		"登录尝试过于频繁，请稍后再试":               "Too many sign-in attempts. Try again later.",
		"请求格式无效":                       "The request format is invalid.",
		"用户名或密码错误":                     "Incorrect username or password.",
		"无法创建会话":                       "Could not create a session.",
		"请先登录":                         "Sign in first.",
		"会话已失效，请重新登录":                  "Your session has expired. Sign in again.",
		"凭据已变更，请重新登录":                  "Your credentials changed. Sign in again.",
		"CSRF 校验失败":                    "CSRF validation failed.",
		"接口不存在":                        "API endpoint not found.",
		"已达到代理安全阈值，请先重置流量或提高限额":        "The proxy safety threshold has been reached. Reset traffic or increase the quota first.",
		"保存流量状态失败":                     "Could not save traffic state.",
		"流量历史查询范围无效":                   "Invalid traffic history range.",
		"流量历史记录未启用":                    "Traffic history is not enabled.",
		"读取流量历史失败":                     "Could not read traffic history.",
		"重启 sing-box 失败":               "Could not restart sing-box.",
		"重置流量失败":                       "Could not reset traffic.",
		"无法读取核心流量，请稍后重试":               "Could not read current core traffic. Try again shortly.",
		"协议不存在":                        "Protocol not found.",
		"请求方法不支持":                      "Method not allowed.",
		"更新重置周期失败":                     "Could not update the reset schedule.",
		"应用流量限额失败":                     "Could not apply the traffic quota.",
		"生成 Token 失败":                  "Could not generate a token.",
		"新密码长度必须为 12 到 128 个字符":        "The new password must be 12 to 128 characters long.",
		"当前密码错误":                       "The current password is incorrect.",
		"密码处理失败":                       "Could not process the password.",
		"订阅不存在":                        "Subscription not found.",
		"已达到代理安全阈值，请等待重置":              "The proxy safety threshold has been reached. Wait for the next reset.",
		"页面不存在":                        "Page not found.",
		"服务器状态采集不可用":                   "Server status collection is unavailable.",
	}
	translated := english[message]
	if translated == "" {
		if status >= 500 {
			translated = "An internal operation failed."
		} else {
			translated = "Configuration validation failed."
		}
	}
	writeJSON(w, status, map[string]string{"error": message, "errorEn": translated})
}
