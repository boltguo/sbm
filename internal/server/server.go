package server

import (
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
	"strconv"
	"strings"
	"sync"
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

type Server struct {
	Config         *store.ConfigStore
	Traffic        *traffic.Tracker
	Core           *core.Manager
	Registry       *protocol.Registry
	Factory        protocol.Factory
	Clash          traffic.ClashClient
	System         *systeminfo.Collector
	Assets         fs.FS
	Limiter        *auth.Limiter
	Sessions       auth.Sessions
	PanelVersion   string
	Releases       releasecheck.Source
	TrafficAudit   *traffic.NetworkAudit
	AuditLog       *log.Logger
	mutationMu     sync.Mutex
	releaseMu      sync.Mutex
	releaseCache   *updateStatus
	releaseUntil   time.Time
	releaseRetryAt time.Time
}

const releaseRetryDelay = 2 * time.Minute

type contextKey string

const sessionKey contextKey = "session"

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", s.login)
	mux.Handle("/api/", s.requireAuth(http.HandlerFunc(s.api)))
	mux.HandleFunc("GET /sub/{token}", s.subscription)
	mux.HandleFunc("/", s.static)
	return securityHeaders(mux)
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
	case r.Method == "GET" && r.URL.Path == "/api/inbounds":
		s.listInbounds(w, r)
	case r.Method == "POST" && r.URL.Path == "/api/inbounds":
		s.createInbound(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/inbounds/"):
		s.inboundByID(w, r)
	case r.Method == "GET" && r.URL.Path == "/api/settings":
		s.getSettings(w, r)
	case r.Method == "PUT" && r.URL.Path == "/api/settings":
		s.updateSettings(w, r)
	case r.Method == "PUT" && r.URL.Path == "/api/settings/traffic":
		s.updateTrafficSettings(w, r)
	case r.Method == "PUT" && r.URL.Path == "/api/settings/outbound":
		s.updateOutboundSettings(w, r)
	case r.Method == "PUT" && r.URL.Path == "/api/settings/wireguard":
		s.updateWireGuardSettings(w, r)
	case r.Method == "POST" && r.URL.Path == "/api/settings/wireguard/keypair":
		s.generateWireGuardKeypair(w, r)
	case r.Method == "POST" && r.URL.Path == "/api/settings/token":
		s.regenerateToken(w, r)
	case r.Method == "POST" && r.URL.Path == "/api/settings/password":
		s.changePassword(w, r)
	default:
		writeError(w, 404, "接口不存在")
	}
}

func (s *Server) serverStatus(w http.ResponseWriter, r *http.Request) {
	if s.System == nil {
		writeError(w, 503, "服务器状态采集不可用")
		return
	}
	writeJSON(w, 200, s.System.Snapshot(r.Context()))
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	session := r.Context().Value(sessionKey).(auth.Session)
	writeJSON(w, 200, map[string]any{"username": session.Username, "csrfToken": session.CSRF})
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	cfg, state := s.Config.Get(), s.Traffic.State()
	active, _ := s.Core.Active(r.Context())
	coreVersion := s.Core.Version(r.Context())
	trafficAudit := traffic.AuditResult{Status: "unavailable"}
	if s.TrafficAudit != nil {
		trafficAudit = s.TrafficAudit.Check(state.Total(), state.PeriodStartedAt)
	}
	remaining := int64(0)
	progress := float64(0)
	if cfg.TotalBytes > 0 {
		remaining = max(0, cfg.TotalBytes-state.Total())
		progress = min(100, float64(state.Total())/float64(cfg.TotalBytes)*100)
	}
	writeJSON(w, 200, map[string]any{
		"active": active, "version": coreVersion, "coreVersion": coreVersion, "panelVersion": s.PanelVersion, "upload": state.Upload, "download": state.Download, "used": state.Total(),
		"totalBytes": cfg.TotalBytes, "remaining": remaining, "progress": progress, "periodStartedAt": state.PeriodStartedAt,
		"nextResetAt": state.NextResetAt, "quotaExceeded": state.QuotaExceeded, "subscriptionURL": subscriptionURL(cfg), "trafficAudit": trafficAudit,
	})
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
		writeError(w, 409, "流量已超限，请先重置流量或提高限额")
		return
	}
	if err := s.captureTraffic(r.Context()); err != nil {
		writeError(w, 503, err.Error())
		return
	}
	if s.Traffic.State().QuotaExceeded {
		writeError(w, 409, "流量已超限，请先重置流量或提高限额")
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
	Link    string `json:"link"`
	Network string `json:"network"`
}

func (s *Server) listInbounds(w http.ResponseWriter, _ *http.Request) {
	cfg := s.Config.Get()
	result := make([]inboundView, 0, len(cfg.Inbounds))
	for _, in := range cfg.Inbounds {
		d, _ := s.Registry.Get(in.Type)
		link, _ := d.ShareLink(in, protocol.ShareContext{Domain: cfg.Domain})
		result = append(result, inboundView{Inbound: in, Link: link, Network: d.Network()})
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
	if cfg := s.Config.Get(); cfg.WireGuardExit != nil && cfg.WireGuardExit.Enabled {
		if err := protocol.EnsureWireGuardExitCredential(&inbound); err != nil {
			writeError(w, 500, err.Error())
			return
		}
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
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	if err := s.captureTraffic(ctx); err != nil {
		return err
	}
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
	if err := s.Traffic.Persist(); err != nil {
		_ = s.Config.Replace(old)
		return errors.New("保存流量状态失败")
	}
	if err := s.Core.Apply(ctx, next, s.Traffic.State().QuotaExceeded); err != nil {
		_ = s.Config.Replace(old)
		return err
	}
	s.syncHostFirewall(old, next)
	return nil
}

func (s *Server) captureTraffic(ctx context.Context) error {
	if s.Clash.URL == "" || s.Traffic.State().QuotaExceeded {
		return nil
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

func cloneWireGuardExit(exit *model.WireGuardExitConfig) *model.WireGuardExitConfig {
	if exit == nil {
		return nil
	}
	copyExit := *exit
	return &copyExit
}

func normalizeWireGuardExit(exit *model.WireGuardExitConfig) *model.WireGuardExitConfig {
	if exit == nil {
		return nil
	}
	normalized := *exit
	normalized.Label = protocol.WireGuardExitLabel(normalized.Label)
	normalized.Server = strings.TrimSpace(normalized.Server)
	normalized.PrivateKey = strings.TrimSpace(normalized.PrivateKey)
	normalized.PeerPublicKey = strings.TrimSpace(normalized.PeerPublicKey)
	if normalized.ServerPort == 0 {
		normalized.ServerPort = model.DefaultWireGuardExitConfig().ServerPort
	}
	return &normalized
}

func ensureWireGuardExitCredentials(inbounds []model.Inbound) ([]model.Inbound, error) {
	result := append([]model.Inbound(nil), inbounds...)
	for i := range result {
		if result[i].VLESS != nil {
			options := *result[i].VLESS
			result[i].VLESS = &options
		}
		if result[i].Hysteria2 != nil {
			options := *result[i].Hysteria2
			result[i].Hysteria2 = &options
		}
		if err := protocol.EnsureWireGuardExitCredential(&result[i]); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func wireGuardExitCredentialsChanged(current, next []model.Inbound) bool {
	if len(current) != len(next) {
		return true
	}
	for i := range current {
		if current[i].VLESS != nil && next[i].VLESS != nil &&
			current[i].VLESS.WireGuardExitUUID != next[i].VLESS.WireGuardExitUUID {
			return true
		}
		if current[i].Hysteria2 != nil && next[i].Hysteria2 != nil &&
			current[i].Hysteria2.WireGuardExitPassword != next[i].Hysteria2.WireGuardExitPassword {
			return true
		}
	}
	return false
}

func wireGuardExitChangesCore(current, next *model.WireGuardExitConfig) bool {
	currentEnabled := current != nil && current.Enabled
	nextEnabled := next != nil && next.Enabled
	if !currentEnabled && !nextEnabled {
		return false
	}
	if currentEnabled != nextEnabled || current == nil || next == nil {
		return true
	}
	currentCore, nextCore := *current, *next
	currentCore.Label, nextCore.Label = "", ""
	return currentCore != nextCore
}

func (s *Server) getSettings(w http.ResponseWriter, _ *http.Request) {
	cfg := s.Config.Get()
	outboundStrategy := cfg.OutboundStrategy
	if outboundStrategy == "" {
		outboundStrategy = model.OutboundStrategyAuto
	}
	wireGuardExit := model.DefaultWireGuardExitConfig()
	if cfg.WireGuardExit != nil {
		wireGuardExit = *cfg.WireGuardExit
	}
	wireGuardExit = *normalizeWireGuardExit(&wireGuardExit)
	wireGuardLocalPublicKey, _ := protocol.WireGuardPublicKey(wireGuardExit.PrivateKey)
	writeJSON(w, 200, map[string]any{
		"domain": cfg.Domain, "panelPort": cfg.PanelPort, "totalBytes": cfg.TotalBytes, "reset": cfg.Reset,
		"outboundStrategy": outboundStrategy, "wireGuardExit": wireGuardExit, "wireGuardLocalPublicKey": wireGuardLocalPublicKey,
		"subscriptionURL": subscriptionURL(cfg),
	})
}
func (s *Server) updateSettings(w http.ResponseWriter, r *http.Request) {
	var input struct {
		TotalBytes       int64                      `json:"totalBytes"`
		Reset            model.ResetConfig          `json:"reset"`
		OutboundStrategy string                     `json:"outboundStrategy"`
		WireGuardExit    *model.WireGuardExitConfig `json:"wireGuardExit"`
	}
	if decodeJSON(r, &input) != nil {
		writeError(w, 400, "请求格式无效")
		return
	}
	if input.OutboundStrategy == "" {
		input.OutboundStrategy = model.OutboundStrategyAuto
	}
	current := s.Config.Get()
	if input.WireGuardExit == nil {
		input.WireGuardExit = cloneWireGuardExit(current.WireGuardExit)
	}
	input.WireGuardExit = normalizeWireGuardExit(input.WireGuardExit)
	nextInbounds := current.Inbounds
	if input.WireGuardExit != nil && input.WireGuardExit.Enabled {
		var err error
		nextInbounds, err = ensureWireGuardExitCredentials(nextInbounds)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
	}
	change := func(cfg *model.Config) {
		cfg.TotalBytes = input.TotalBytes
		cfg.Reset = input.Reset
		cfg.OutboundStrategy = input.OutboundStrategy
		cfg.WireGuardExit = cloneWireGuardExit(input.WireGuardExit)
		cfg.Inbounds = nextInbounds
	}
	currentStrategy := current.OutboundStrategy
	if currentStrategy == "" {
		currentStrategy = model.OutboundStrategyAuto
	}
	var err error
	if currentStrategy != input.OutboundStrategy || wireGuardExitChangesCore(current.WireGuardExit, input.WireGuardExit) || wireGuardExitCredentialsChanged(current.Inbounds, nextInbounds) {
		err = s.mutate(r.Context(), change)
	} else {
		err = s.saveConfig(change)
	}
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := s.Traffic.UpdateSchedule(); err != nil {
		writeError(w, 500, "更新重置周期失败")
		return
	}
	if err := s.Traffic.ReconcileQuota(r.Context()); err != nil {
		writeError(w, 500, "应用流量限额失败")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) updateTrafficSettings(w http.ResponseWriter, r *http.Request) {
	var input struct {
		TotalBytes int64             `json:"totalBytes"`
		Reset      model.ResetConfig `json:"reset"`
	}
	if decodeJSON(r, &input) != nil {
		writeError(w, 400, "请求格式无效")
		return
	}
	if err := s.saveConfig(func(cfg *model.Config) {
		cfg.TotalBytes = input.TotalBytes
		cfg.Reset = input.Reset
	}); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := s.Traffic.UpdateSchedule(); err != nil {
		writeError(w, 500, "更新重置周期失败")
		return
	}
	if err := s.Traffic.ReconcileQuota(r.Context()); err != nil {
		writeError(w, 500, "应用流量限额失败")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
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

func (s *Server) updateWireGuardSettings(w http.ResponseWriter, r *http.Request) {
	var input struct {
		WireGuardExit *model.WireGuardExitConfig `json:"wireGuardExit"`
	}
	if decodeJSON(r, &input) != nil || input.WireGuardExit == nil {
		writeError(w, 400, "请求格式无效")
		return
	}
	input.WireGuardExit = normalizeWireGuardExit(input.WireGuardExit)
	current := s.Config.Get()
	nextInbounds := current.Inbounds
	if input.WireGuardExit.Enabled {
		var err error
		nextInbounds, err = ensureWireGuardExitCredentials(nextInbounds)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
	}
	change := func(cfg *model.Config) {
		cfg.WireGuardExit = cloneWireGuardExit(input.WireGuardExit)
		cfg.Inbounds = nextInbounds
	}
	var err error
	if wireGuardExitChangesCore(current.WireGuardExit, input.WireGuardExit) || wireGuardExitCredentialsChanged(current.Inbounds, nextInbounds) {
		err = s.mutate(r.Context(), change)
	} else {
		err = s.saveConfig(change)
	}
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	stored := s.Config.Get()
	storedStrategy := stored.OutboundStrategy
	if storedStrategy == "" {
		storedStrategy = model.OutboundStrategyAuto
	}
	publicKey, _ := protocol.WireGuardPublicKey(input.WireGuardExit.PrivateKey)
	writeJSON(w, 200, map[string]any{
		"ok":                      true,
		"outboundStrategy":        storedStrategy,
		"wireGuardLocalPublicKey": publicKey,
	})
}

func (s *Server) generateWireGuardKeypair(w http.ResponseWriter, _ *http.Request) {
	keys, err := protocol.GenerateWireGuardKeys()
	if err != nil {
		writeError(w, 500, "无法生成 WireGuard 密钥")
		return
	}
	writeJSON(w, 200, keys)
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
		writeError(w, 403, "流量已用尽，请等待重置")
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
		if cfg.WireGuardExit != nil && cfg.WireGuardExit.Enabled {
			if variant, ok := protocol.WireGuardExitVariant(inbound, cfg.WireGuardExit.Label); ok {
				if link, err := d.ShareLink(variant, protocol.ShareContext{Domain: cfg.Domain}); err == nil {
					links = append(links, link)
				}
			}
		}
	}
	payload := base64.StdEncoding.EncodeToString([]byte(strings.Join(links, "\n")))
	w.Header().Set("Subscription-Userinfo", fmt.Sprintf("upload=%d; download=%d; total=%d; expire=0", state.Upload, state.Download, cfg.TotalBytes))
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
		"登录尝试过于频繁，请稍后再试":        "Too many sign-in attempts. Try again later.",
		"请求格式无效":                "The request format is invalid.",
		"用户名或密码错误":              "Incorrect username or password.",
		"无法创建会话":                "Could not create a session.",
		"请先登录":                  "Sign in first.",
		"会话已失效，请重新登录":           "Your session has expired. Sign in again.",
		"凭据已变更，请重新登录":           "Your credentials changed. Sign in again.",
		"CSRF 校验失败":             "CSRF validation failed.",
		"接口不存在":                 "API endpoint not found.",
		"流量已超限，请先重置流量或提高限额":     "The traffic quota is exhausted. Reset traffic or increase the quota first.",
		"保存流量状态失败":              "Could not save traffic state.",
		"重启 sing-box 失败":        "Could not restart sing-box.",
		"重置流量失败":                "Could not reset traffic.",
		"无法读取核心流量，请稍后重试":        "Could not read current core traffic. Try again shortly.",
		"协议不存在":                 "Protocol not found.",
		"请求方法不支持":               "Method not allowed.",
		"更新重置周期失败":              "Could not update the reset schedule.",
		"应用流量限额失败":              "Could not apply the traffic quota.",
		"无法生成 WireGuard 密钥":     "Could not generate WireGuard keys.",
		"生成 Token 失败":           "Could not generate a token.",
		"新密码长度必须为 12 到 128 个字符": "The new password must be 12 to 128 characters long.",
		"当前密码错误":                "The current password is incorrect.",
		"密码处理失败":                "Could not process the password.",
		"订阅不存在":                 "Subscription not found.",
		"流量已用尽，请等待重置":           "The traffic quota is exhausted. Wait for the next reset.",
		"页面不存在":                 "Page not found.",
		"服务器状态采集不可用":            "Server status collection is unavailable.",
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
