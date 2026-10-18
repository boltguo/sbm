package server

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/boltguo/sbm/internal/geo"
	"github.com/boltguo/sbm/internal/model"
	"github.com/boltguo/sbm/internal/protocol"
	"github.com/boltguo/sbm/internal/traffic"
)

type egressNodeView struct {
	GatewayID string `json:"gatewayId"`
	Name      string `json:"name"`
	Link      string `json:"link"`
	Marker    string `json:"marker"`
	Location  string `json:"location"`
}
type gatewayUsageView struct {
	ID                         string                    `json:"id"`
	Name                       string                    `json:"name"`
	Marker                     string                    `json:"marker"`
	Location                   string                    `json:"location"`
	Enabled                    bool                      `json:"enabled"`
	TunnelTX                   int64                     `json:"tunnelTX"`
	TunnelRX                   int64                     `json:"tunnelRX"`
	TunnelBytes                int64                     `json:"tunnelBytes"`
	TrafficQuota               model.TrafficQuotaConfig  `json:"trafficQuota"`
	ProviderAllowanceBytes     int64                     `json:"providerAllowanceBytes"`
	EstimatedProviderUsedBytes int64                     `json:"estimatedProviderUsedBytes"`
	ProviderRemainingBytes     int64                     `json:"providerRemainingBytes"`
	ProviderProgress           float64                   `json:"providerProgress"`
	Warning                    bool                      `json:"warning"`
	Paused                     bool                      `json:"paused"`
	UsageCalibrated            bool                      `json:"usageCalibrated"`
	PeriodStartedAt            time.Time                 `json:"periodStartedAt"`
	NextResetAt                time.Time                 `json:"nextResetAt"`
	SampleHealth               model.GatewayTrafficState `json:"sampleHealth"`
	PersistenceHealth          traffic.SampleHealth      `json:"persistenceHealth"`
}
type gatewayView struct {
	model.EgressGateway
	PublicKey     string           `json:"publicKey"`
	Name          string           `json:"name"`
	Location      string           `json:"location"`
	TunnelAddress string           `json:"tunnelAddress"`
	PeerAddress   string           `json:"peerAddress"`
	Usage         gatewayUsageView `json:"usage"`
}

func (s *Server) gatewayUsage(cfg model.Config) []gatewayUsageView {
	state := s.Traffic.State()
	paused := traffic.GatewaysOverHardLimit(cfg, state)
	views := make([]gatewayUsageView, 0, len(cfg.EgressGateways))
	for _, g := range model.OrderedGateways(cfg.EgressGateways) {
		st := state.Egress[g.ID]
		if st.Status == "" {
			st.Status = "waiting"
		}
		if !g.Enabled {
			st.Status = "disabled"
		}
		allowance, _ := g.TrafficQuota.AllowanceBytes()
		stop, _ := g.TrafficQuota.NetworkStopBytes()
		tunnel := addSaturating(st.TX, st.RX)
		used := st.ProviderUsage(g.TrafficQuota)
		progress := float64(0)
		if allowance > 0 {
			progress = float64(used) / float64(allowance) * 100
		}
		views = append(views, gatewayUsageView{ID: g.ID, Name: protocol.GatewayName(g), Marker: g.Marker, Location: protocol.GatewayLocation(g), Enabled: g.Enabled, TunnelTX: st.TX, TunnelRX: st.RX, TunnelBytes: tunnel, TrafficQuota: g.TrafficQuota, ProviderAllowanceBytes: allowance, EstimatedProviderUsedBytes: used, ProviderRemainingBytes: max(0, allowance-used), ProviderProgress: progress, Warning: stop > 0 && used >= stop, Paused: paused[g.ID], UsageCalibrated: st.Adjustment.Active(g.TrafficQuota.BillingMode), PeriodStartedAt: st.PeriodStartedAt, NextResetAt: st.NextResetAt, SampleHealth: st, PersistenceHealth: s.Traffic.PersistenceHealth()})
	}
	return views
}
func (s *Server) listGateways(w http.ResponseWriter, _ *http.Request) {
	cfg := s.Config.Get()
	usage := s.gatewayUsage(cfg)
	views := make([]gatewayView, 0, len(cfg.EgressGateways))
	for i, g := range model.OrderedGateways(cfg.EgressGateways) {
		public, _ := protocol.WireGuardPublicKey(g.PrivateKey)
		views = append(views, gatewayView{EgressGateway: g, PublicKey: public, Name: protocol.GatewayName(g), Location: protocol.GatewayLocation(g), TunnelAddress: g.TunnelAddress(), PeerAddress: g.PeerAddress(), Usage: usage[i]})
	}
	writeJSON(w, 200, views)
}
func normalizeGateway(g model.EgressGateway) model.EgressGateway {
	g.Marker = strings.TrimSpace(g.Marker)
	g.LocationOverride = strings.TrimSpace(g.LocationOverride)
	g.Server = strings.TrimSpace(g.Server)
	g.PrivateKey = strings.TrimSpace(g.PrivateKey)
	g.PeerPublicKey = strings.TrimSpace(g.PeerPublicKey)
	if g.ServerPort == 0 {
		g.ServerPort = 51820
	}
	if g.TrafficQuota.Unit == "" {
		g.TrafficQuota.Unit = model.TrafficUnitGB
	}
	if g.TrafficQuota.BillingMode == "" {
		g.TrafficQuota.BillingMode = model.TrafficBillingSingle
	}
	if g.Reset.Mode == "" {
		g.Reset = model.DefaultConfig().Reset
	}
	return g
}
func (s *Server) detectGatewayGeo(ctx context.Context, g *model.EgressGateway) bool {
	if g.Server == "" {
		g.Geo = model.GatewayGeo{}
		return false
	}
	lookup := s.Geo
	if lookup == nil {
		lookup = geo.Client{}
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	detected, err := lookup.Lookup(lookupCtx, g.Server)
	if err != nil {
		// A stale location from the previous IP must not name the new exit.
		if g.Geo.IP != g.Server {
			g.Geo = model.GatewayGeo{}
		}
		return false
	}
	g.Geo = detected
	return true
}
func (s *Server) createGateway(w http.ResponseWriter, r *http.Request) {
	var input model.EgressGateway
	if decodeJSON(r, &input) != nil {
		writeError(w, 400, "请求格式无效")
		return
	}
	input = normalizeGateway(input)
	id, err := protocol.RandomHex(8)
	if err != nil {
		writeError(w, 500, "生成中继出口 ID 失败")
		return
	}
	input.ID = id
	input.Geo = model.GatewayGeo{}
	detected := false
	err = s.mutateChange(r.Context(), func(cfg *model.Config) error {
		used := map[int]bool{}
		for _, g := range cfg.EgressGateways {
			used[g.TunnelSlot] = true
		}
		input.TunnelSlot = 0
		for slot := 1; slot <= 254; slot++ {
			if !used[slot] {
				input.TunnelSlot = slot
				break
			}
		}
		if input.TunnelSlot == 0 {
			return errors.New("中继出口地址槽已用完")
		}
		if err := protocol.ValidateGateway(input); err != nil {
			return err
		}
		detected = s.detectGatewayGeo(r.Context(), &input)
		cfg.EgressGateways = append(cfg.EgressGateways, input)
		return nil
	})
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "geoDetected": detected})
}
func (s *Server) gatewayByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/egress/")
	parts := strings.Split(path, "/")
	if len(parts) > 2 || parts[0] == "" {
		writeError(w, 404, "中继出口不存在")
		return
	}
	id := parts[0]
	if len(parts) == 2 && parts[1] == "calibrate" && r.Method == http.MethodPost {
		var input usageCalibration
		if decodeJSON(r, &input) != nil || (!input.Clear && input.UsedBytes < 0) {
			writeError(w, 400, "请求格式无效")
			return
		}
		s.mutationMu.Lock()
		err := s.Traffic.CalibrateGateway(r.Context(), id, input.UsedBytes, input.Clear)
		s.mutationMu.Unlock()
		if err != nil {
			writeError(w, 400, "校准出口用量失败")
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	if len(parts) == 2 && parts[1] == "reset" && r.Method == http.MethodPost {
		s.mutationMu.Lock()
		err := s.Traffic.ResetGateway(r.Context(), id)
		s.mutationMu.Unlock()
		if err != nil {
			writeError(w, 400, "重置出口流量失败")
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	redetect := len(parts) == 2 && parts[1] == "geo" && r.Method == http.MethodPost
	if len(parts) == 2 && !redetect {
		writeError(w, 404, "接口不存在")
		return
	}
	var input model.EgressGateway
	if r.Method == http.MethodPut {
		if decodeJSON(r, &input) != nil {
			writeError(w, 400, "请求格式无效")
			return
		}
		input = normalizeGateway(input)
	} else if r.Method != http.MethodDelete && !redetect {
		writeError(w, 405, "请求方法不支持")
		return
	}
	detected := true
	err := s.mutateChange(r.Context(), func(cfg *model.Config) error {
		for i, g := range cfg.EgressGateways {
			if g.ID != id {
				continue
			}
			if r.Method == http.MethodDelete {
				cfg.EgressGateways = append(cfg.EgressGateways[:i], cfg.EgressGateways[i+1:]...)
				return nil
			}
			if redetect {
				input = g
			} else {
				input.ID = g.ID
				input.TunnelSlot = g.TunnelSlot
				input.Geo = g.Geo
			}
			if err := protocol.ValidateGateway(input); err != nil {
				return err
			}
			if redetect || input.Server != g.Server {
				detected = s.detectGatewayGeo(r.Context(), &input)
			}
			cfg.EgressGateways[i] = input
			return nil
		}
		return errors.New("中继出口不存在")
	})
	if err != nil {
		status := 400
		if err.Error() == "中继出口不存在" {
			status = 404
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true, "geoDetected": detected})
}
func (s *Server) generateWireGuardKeypair(w http.ResponseWriter, _ *http.Request) {
	keys, err := protocol.GenerateWireGuardKeys()
	if err != nil {
		writeError(w, 500, "无法生成 WireGuard 密钥")
		return
	}
	writeJSON(w, 200, keys)
}
func (s *Server) wireGuardPublicKey(w http.ResponseWriter, r *http.Request) {
	var input struct {
		PrivateKey string `json:"privateKey"`
	}
	if decodeJSON(r, &input) != nil {
		writeError(w, 400, "请求格式无效")
		return
	}
	public, err := protocol.WireGuardPublicKey(input.PrivateKey)
	if err != nil {
		writeError(w, 400, "WireGuard 私钥格式无效")
		return
	}
	writeJSON(w, 200, map[string]string{"publicKey": public})
}

type appRouteGatewayOption struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Location string `json:"location"`
	Enabled  bool   `json:"enabled"`
	// Paused gateways reject the apps pinned to them.
	Paused bool `json:"paused"`
}

type appRouteRuleView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	DomainCount int    `json:"domainCount"`
	HasCIDRs    bool   `json:"hasCidrs"`
	Target      string `json:"target"`
}

type appRoutesView struct {
	Gateways []appRouteGatewayOption `json:"gateways"`
	Apps     []appRouteRuleView      `json:"apps"`
	Routes   map[string]string       `json:"routes"`
}

func buildAppRoutesView(cfg model.Config, paused map[string]bool) appRoutesView {
	ordered := model.OrderedGateways(cfg.EgressGateways)
	gateways := make([]appRouteGatewayOption, 0, len(ordered))
	validGateways := make(map[string]bool, len(ordered))
	for _, g := range ordered {
		validGateways[g.ID] = true
		gateways = append(gateways, appRouteGatewayOption{
			ID:       g.ID,
			Name:     protocol.GatewayName(g),
			Location: protocol.GatewayLocation(g),
			Enabled:  g.Enabled,
			Paused:   paused[g.ID],
		})
	}
	specs := traffic.AppRuleSpecs()
	known := make(map[string]bool, len(specs))
	for _, spec := range specs {
		known[spec.ID] = true
	}
	routes := make(map[string]string, len(cfg.AppRoutes))
	for k, v := range cfg.AppRoutes {
		// A key whose rule is no longer loaded stays saved but inactive.
		if !known[k] && !strings.HasPrefix(k, "site:") {
			continue
		}
		if v == model.AppRouteDirect || validGateways[v] {
			routes[k] = v
		}
	}
	apps := make([]appRouteRuleView, 0, len(specs))
	for _, spec := range specs {
		apps = append(apps, appRouteRuleView{
			ID:          spec.ID,
			Name:        spec.Name,
			Category:    spec.Category,
			DomainCount: len(spec.Domains) + len(spec.Subdomains),
			HasCIDRs:    len(spec.CIDRs) > 0,
			Target:      routes[spec.ID],
		})
	}
	return appRoutesView{
		Gateways: gateways,
		Apps:     apps,
		Routes:   routes,
	}
}

func (s *Server) appRoutesView() appRoutesView {
	cfg := s.Config.Get()
	return buildAppRoutesView(cfg, s.Traffic.DesiredGatewayPauses(cfg))
}

func (s *Server) getAppRoutes(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, s.appRoutesView())
}

func (s *Server) updateAppRoutes(w http.ResponseWriter, r *http.Request) {
	var input struct {
		AppID   *string           `json:"appId"`
		Target  string            `json:"target"`
		Changes map[string]string `json:"changes"`
		Routes  map[string]string `json:"routes"`
	}
	if decodeJSON(r, &input) != nil || (input.AppID == nil && len(input.Changes) == 0 && input.Routes == nil) {
		writeError(w, 400, "请求格式无效")
		return
	}
	err := s.mutateChange(r.Context(), func(cfg *model.Config) error {
		validGateways := make(map[string]bool, len(cfg.EgressGateways))
		for _, g := range cfg.EgressGateways {
			validGateways[g.ID] = true
		}
		if input.AppID != nil {
			return applyAppRouteChange(cfg, validGateways, *input.AppID, input.Target)
		}
		if len(input.Changes) > 0 {
			// Several edits in one request apply with one sing-box restart.
			keys := make([]string, 0, len(input.Changes))
			for k := range input.Changes {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			seen := make(map[string]string, len(keys))
			for _, k := range keys {
				target := strings.TrimSpace(input.Changes[k])
				if target == "default" {
					target = ""
				}
				normalized := protocol.NormalizeAppRouteKey(k)
				if prev, ok := seen[normalized]; ok && prev != target {
					return errors.New("应用分流规则重复")
				}
				seen[normalized] = target
				if err := applyAppRouteChange(cfg, validGateways, k, target); err != nil {
					return err
				}
			}
			return nil
		}
		next := make(map[string]string, len(input.Routes))
		for k, v := range input.Routes {
			k = protocol.NormalizeAppRouteKey(k)
			v = strings.TrimSpace(v)
			if v == "" || v == "default" {
				continue
			}
			if !protocol.ValidateAppRouteKey(k) {
				return errors.New("应用分流规则标识无效")
			}
			if v != model.AppRouteDirect && !validGateways[v] {
				return errors.New("应用分流目标出口不存在")
			}
			if prev, ok := next[k]; ok && prev != v {
				return errors.New("应用分流规则重复")
			}
			next[k] = v
		}
		cfg.AppRoutes = next
		return nil
	})
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, s.appRoutesView())
}

// applyAppRouteChange sets one app or site route, or clears it when target is
// empty or "default".
func applyAppRouteChange(cfg *model.Config, validGateways map[string]bool, key, target string) error {
	raw := strings.TrimSpace(key)
	appID := protocol.NormalizeAppRouteKey(raw)
	target = strings.TrimSpace(target)
	if target == "" || target == "default" {
		// Clearing must work for a key whose rule is no longer loaded.
		if !protocol.ValidateStoredAppRouteKey(raw) {
			return errors.New("应用分流规则标识无效")
		}
		delete(cfg.AppRoutes, raw)
		delete(cfg.AppRoutes, appID)
		return nil
	}
	if !protocol.ValidateAppRouteKey(appID) {
		return errors.New("应用分流规则标识无效")
	}
	if target != model.AppRouteDirect && !validGateways[target] {
		return errors.New("应用分流目标出口不存在")
	}
	if cfg.AppRoutes == nil {
		cfg.AppRoutes = make(map[string]string)
	}
	delete(cfg.AppRoutes, raw)
	cfg.AppRoutes[appID] = target
	return nil
}
