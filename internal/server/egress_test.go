package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/boltguo/sbm/internal/model"
	"github.com/boltguo/sbm/internal/protocol"
	"github.com/boltguo/sbm/internal/traffic"
)

type testGeo struct {
	calls int
	ips   []string
	fail  bool
}

func (g *testGeo) Lookup(_ context.Context, ip string) (model.GatewayGeo, error) {
	g.calls++
	g.ips = append(g.ips, ip)
	if g.fail {
		return model.GatewayGeo{}, errors.New("external geo failure")
	}
	return model.GatewayGeo{IP: ip, CountryCode: "US", Country: "United States", Region: "Oregon", City: "Boardman", UpdatedAt: time.Now()}, nil
}

type gatewayCommander struct {
	successCommander
	checks, restarts, stops int
	cancel                  context.CancelFunc
	fail                    bool
}

func (c *gatewayCommander) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if len(args) > 0 && args[0] == "check" {
		c.checks++
		if c.fail {
			return []byte("private secret must never appear"), errors.New("check failed")
		}
	}
	if name == "systemctl" && len(args) > 0 {
		if args[0] == "stop" {
			c.stops++
		}
		if args[0] == "restart" {
			c.restarts++
			if c.cancel != nil {
				c.cancel()
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
	}
	return c.successCommander.Run(ctx, name, args...)
}
func gatewayInput(t *testing.T, server string) model.EgressGateway {
	t.Helper()
	keys, err := protocol.GenerateWireGuardKeys()
	if err != nil {
		t.Fatal(err)
	}
	return model.EgressGateway{Enabled: true, Marker: "AWS", Server: server, ServerPort: 51820, PrivateKey: keys.Private, PeerPublicKey: keys.Public, TrafficQuota: model.DefaultConfig().TrafficQuota, Reset: model.ResetConfig{Mode: "monthly", Day: 1, Timezone: "UTC"}}
}
func gatewayRequest(t *testing.T, s *Server, method, path string, input any, want int) *httptest.ResponseRecorder {
	t.Helper()
	resp := httptest.NewRecorder()
	s.Handler().ServeHTTP(resp, authenticatedRequest(t, s, method, path, input))
	if resp.Code != want {
		t.Fatalf("%s %s status=%d want=%d error=%s", method, path, resp.Code, want, resp.Body.String())
	}
	return resp
}
func gatewaySubscription(t *testing.T, s *Server) ([]*url.URL, string) {
	t.Helper()
	cfg := s.Config.Get()
	resp := httptest.NewRecorder()
	s.Handler().ServeHTTP(resp, httptest.NewRequest("GET", "/sub/"+cfg.SubscriptionToken, nil))
	if resp.Code != 200 {
		t.Fatalf("subscription status=%d", resp.Code)
	}
	data, err := base64.StdEncoding.DecodeString(resp.Body.String())
	if err != nil {
		t.Fatal(err)
	}
	result := []*url.URL{}
	for _, line := range strings.Split(string(data), "\n") {
		u, err := url.Parse(line)
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, u)
	}
	return result, resp.Header().Get("Profile-Title")
}
func TestGatewayLifecycleAPIAndSubscription(t *testing.T) {
	s, _ := testServer(t)
	geo := &testGeo{}
	s.Geo = geo
	commander := &gatewayCommander{}
	s.Core.Commands = commander
	cfg := s.Config.Get()
	cfg.Inbounds = append([]model.Inbound{{ID: "vless", Type: protocol.TypeVLESSReality, Enabled: true, Name: "US-LosAngeles-VLESS", Port: 443, VLESS: &model.VLESSOptions{UUID: "70d0c699-73a0-4d2a-a45d-4f46a661b4f2", SNI: "www.apple.com", PrivateKey: "private", PublicKey: "public", ShortID: "aabb"}}}, cfg.Inbounds...)
	cfg.Inbounds[1].Name = "US-LosAngeles-HY2"
	if err := s.Config.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	originalDirect := *cfg.Inbounds[0].VLESS
	directPassword := cfg.Inbounds[1].Hysteria2.Password
	_, title := gatewaySubscription(t, s)
	gatewayRequest(t, s, "POST", "/api/egress", gatewayInput(t, "203.0.113.1"), 201)
	input := gatewayInput(t, "203.0.113.2")
	input.Marker = "JP1"
	input.LocationOverride = "JP-Tokyo"
	input.Position = 1
	gatewayRequest(t, s, "POST", "/api/egress", input, 201)
	cfg = s.Config.Get()
	aws, jp := cfg.EgressGateways[0], cfg.EgressGateways[1]
	credentials := append([]model.EgressCredential(nil), cfg.Inbounds[0].EgressCredentials...)
	links, newTitle := gatewaySubscription(t, s)
	if len(links) != 6 || title != newTitle || links[2].Fragment != "US-Boardman-AWS-VLESS" || links[3].Fragment != "US-Boardman-AWS-HY2" || links[4].Fragment != "JP-Tokyo-JP1-VLESS" {
		t.Fatal("subscription order/naming/title incorrect")
	}
	requests := geo.calls
	response := gatewayRequest(t, s, "GET", "/api/inbounds", nil, 200)
	var inbounds []inboundView
	if err := json.Unmarshal(response.Body.Bytes(), &inbounds); err != nil {
		t.Fatal(err)
	}
	if len(inbounds[0].EgressNodes) != 2 || inbounds[0].EgressNodes[0].GatewayID != aws.ID {
		t.Fatal("missing derived node views")
	}
	gatewayRequest(t, s, "GET", "/api/dashboard", nil, 200)
	if geo.calls != requests {
		t.Fatal("hot path made Geo request")
	}
	checks := commander.checks
	aws.Marker = "renamed"
	aws.Position = 0
	gatewayRequest(t, s, "PUT", "/api/egress/"+aws.ID, aws, 200)
	if commander.checks != checks {
		t.Fatal("display rename restarted core")
	}
	aws.Enabled = false
	gatewayRequest(t, s, "PUT", "/api/egress/"+aws.ID, aws, 200)
	links, _ = gatewaySubscription(t, s)
	if len(links) != 4 {
		t.Fatal("disabled gateway remains subscribed")
	}
	data, err := os.ReadFile(s.Core.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), aws.EndpointTag()) || strings.Contains(string(data), "egress-"+aws.ID+"-") {
		t.Fatal("disabled gateway remains in core")
	}
	aws.Enabled = true
	gatewayRequest(t, s, "PUT", "/api/egress/"+aws.ID, aws, 200)
	cfg = s.Config.Get()
	if !reflect.DeepEqual(credentials, cfg.Inbounds[0].EgressCredentials) || *cfg.Inbounds[0].VLESS != originalDirect || cfg.Inbounds[1].Hysteria2.Password != directPassword {
		t.Fatal("gateway changes rotated credentials")
	}
	// Editing a protocol with missing or forged derived credentials preserves
	// the server-owned credential set.
	in := cfg.Inbounds[0]
	in.Name = "entry-renamed-VLESS"
	in.EgressCredentials = nil
	gatewayRequest(t, s, "PUT", "/api/inbounds/"+in.ID, in, 200)
	if !reflect.DeepEqual(credentials, s.Config.Get().Inbounds[0].EgressCredentials) {
		t.Fatal("protocol edit replaced egress credentials")
	}
	aws.Server = "203.0.113.3"
	gatewayRequest(t, s, "PUT", "/api/egress/"+aws.ID, aws, 200)
	if geo.ips[len(geo.ips)-1] != "203.0.113.3" || s.Config.Get().EgressGateways[0].Geo.IP != aws.Server {
		t.Fatal("server IP change did not refresh Geo")
	}
	geo.fail = true
	aws.Server = "203.0.113.4"
	aws.LocationOverride = "SG-Singapore"
	gatewayRequest(t, s, "PUT", "/api/egress/"+aws.ID, aws, 200)
	cfg = s.Config.Get()
	if cfg.EgressGateways[0].Geo.IP != "" || protocol.GatewayLocation(cfg.EgressGateways[0]) != "SG-Singapore" {
		t.Fatal("failed Geo update kept stale IP location")
	}
	gatewayRequest(t, s, "POST", "/api/egress/"+aws.ID+"/geo", nil, 200)
	cfg = s.Config.Get()
	cfg.Inbounds[0].Enabled = false
	gatewayRequest(t, s, "PUT", "/api/inbounds/vless", cfg.Inbounds[0], 200)
	links, _ = gatewaySubscription(t, s)
	if len(links) != 3 {
		t.Fatal("disabled inbound variants subscribed")
	}
	gatewayRequest(t, s, "DELETE", "/api/egress/"+aws.ID, nil, 200)
	cfg = s.Config.Get()
	if len(cfg.EgressGateways) != 1 || cfg.EgressGateways[0].TunnelSlot != jp.TunnelSlot || len(cfg.Inbounds[0].EgressCredentials) != 1 || cfg.Inbounds[0].EgressCredentials[0].GatewayID != jp.ID {
		t.Fatal("delete failed cleanup or changed another slot")
	}
	if _, ok := s.Traffic.State().Egress[aws.ID]; ok {
		t.Fatal("deleted state retained")
	}
	gatewayRequest(t, s, "POST", "/api/egress", gatewayInput(t, "203.0.113.5"), 201)
	if s.Config.Get().EgressGateways[0].TunnelSlot != jp.TunnelSlot {
		t.Fatal("new gateway renumbered existing slot")
	}
}
func TestGatewayApplyFailureAndRequestCancellation(t *testing.T) {
	s, _ := testServer(t)
	s.Geo = &testGeo{}
	commander := &gatewayCommander{fail: true}
	s.Core.Commands = commander
	before := s.Config.Get()
	response := gatewayRequest(t, s, "POST", "/api/egress", gatewayInput(t, "203.0.113.1"), 400)
	if !reflect.DeepEqual(before, s.Config.Get()) || len(s.Traffic.State().Egress) != 0 {
		t.Fatal("failed apply did not roll back config/state")
	}
	if strings.Contains(response.Body.String(), "private secret") {
		t.Fatal("command output leaked")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	commander.fail = false
	commander.cancel = cancel
	request := authenticatedRequest(t, s, "POST", "/api/egress", gatewayInput(t, "203.0.113.1")).WithContext(ctx)
	response = httptest.NewRecorder()
	s.Handler().ServeHTTP(response, request)
	if response.Code != 201 || ctx.Err() == nil || len(s.Config.Get().EgressGateways) != 1 || len(s.Config.Get().Inbounds[0].EgressCredentials) != 1 {
		t.Fatal("request disconnect incorrectly rolled back gateway")
	}
}

type serverGatewaySampler struct {
	values map[string]traffic.GatewayCounters
	fail   bool
}

func (s *serverGatewaySampler) Sample(context.Context, []model.EgressGateway) (map[string]traffic.GatewayCounters, error) {
	if s.fail {
		return nil, errors.New("unavailable")
	}
	return s.values, nil
}
func TestGatewayWarningNeverStopsCoreAndAccountingFailureSaves(t *testing.T) {
	s, _ := testServer(t)
	s.Geo = &testGeo{}
	commander := &gatewayCommander{}
	s.Core.Commands = commander
	sampler := &serverGatewaySampler{values: map[string]traffic.GatewayCounters{}, fail: true}
	s.Traffic.Gateways = sampler
	input := gatewayInput(t, "203.0.113.1")
	input.TrafficQuota = model.TrafficQuotaConfig{Amount: 1, Unit: "GB", BillingMode: "bidirectional", HeadroomPercent: 10}
	gatewayRequest(t, s, "POST", "/api/egress", input, 201)
	g := s.Config.Get().EgressGateways[0]
	if s.Traffic.State().Egress[g.ID].Status != "interrupted" {
		t.Fatal("accounting failure not surfaced")
	}
	sampler.fail = false
	sampler.values[g.ID] = traffic.GatewayCounters{TXGeneration: "one", RXGeneration: "one"}
	_ = s.Traffic.SampleGateways(context.Background())
	sampler.values[g.ID] = traffic.GatewayCounters{TX: 300_000_000, RX: 200_000_000, TXGeneration: "one", RXGeneration: "one"}
	_ = s.Traffic.SampleGateways(context.Background())
	usage := s.gatewayUsage(s.Config.Get())[0]
	if !usage.Warning || usage.EstimatedProviderUsedBytes != 1_000_000_000 || usage.ProviderRemainingBytes != 0 || commander.stops != 0 || s.Traffic.State().QuotaExceeded {
		t.Fatal("gateway warning affected core or used wrong counters")
	}
	gatewayRequest(t, s, "POST", "/api/egress/"+g.ID+"/reset", nil, 200)
	if s.gatewayUsage(s.Config.Get())[0].TunnelBytes != 0 || commander.stops != 0 {
		t.Fatal("gateway reset affected core")
	}
}
func TestGatewayDraftValidationAndKeysAPI(t *testing.T) {
	s, _ := testServer(t)
	s.Geo = &testGeo{fail: true}
	input := gatewayInput(t, "")
	input.Enabled = false
	input.PeerPublicKey = ""
	gatewayRequest(t, s, "POST", "/api/egress", input, 201)
	response := gatewayRequest(t, s, "POST", "/api/egress/keypair", nil, 200)
	var keys protocol.WireGuardKeys
	_ = json.Unmarshal(response.Body.Bytes(), &keys)
	response = gatewayRequest(t, s, "POST", "/api/egress/public-key", map[string]string{"privateKey": keys.Private}, 200)
	var public struct{ PublicKey string }
	_ = json.Unmarshal(response.Body.Bytes(), &public)
	if public.PublicKey != keys.Public {
		t.Fatal("keypair API mismatch")
	}
	input.Enabled = true
	gatewayRequest(t, s, "POST", "/api/egress", input, 400)
	input = gatewayInput(t, "127.0.0.1")
	gatewayRequest(t, s, "POST", "/api/egress", input, 400)
	gatewayRequest(t, s, "POST", "/api/egress", gatewayInput(t, "203.0.113.2"), 201)
	gatewayRequest(t, s, "POST", "/api/egress", gatewayInput(t, "203.0.113.2"), 400)
}
