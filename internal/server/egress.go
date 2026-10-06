package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/boltguo/sbm/internal/geo"
	"github.com/boltguo/sbm/internal/model"
	"github.com/boltguo/sbm/internal/protocol"
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
	PeriodStartedAt            time.Time                 `json:"periodStartedAt"`
	NextResetAt                time.Time                 `json:"nextResetAt"`
	SampleHealth               model.GatewayTrafficState `json:"sampleHealth"`
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
		limit, _ := g.TrafficQuota.EffectiveBytes()
		tunnel := st.TX
		if st.RX > (1<<63-1)-tunnel {
			tunnel = 1<<63 - 1
		} else {
			tunnel += st.RX
		}
		used := multiplySaturating(tunnel, g.TrafficQuota.ProviderUsageFactor())
		progress := float64(0)
		if allowance > 0 {
			progress = min(100, float64(used)/float64(allowance)*100)
		}
		views = append(views, gatewayUsageView{ID: g.ID, Name: protocol.GatewayName(g), Marker: g.Marker, Location: protocol.GatewayLocation(g), Enabled: g.Enabled, TunnelTX: st.TX, TunnelRX: st.RX, TunnelBytes: tunnel, TrafficQuota: g.TrafficQuota, ProviderAllowanceBytes: allowance, EstimatedProviderUsedBytes: used, ProviderRemainingBytes: max(0, allowance-used), ProviderProgress: progress, Warning: limit > 0 && tunnel >= limit, PeriodStartedAt: st.PeriodStartedAt, NextResetAt: st.NextResetAt, SampleHealth: st})
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
