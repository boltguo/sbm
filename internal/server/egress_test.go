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
func TestDisabledGatewayDraftDoesNotRestartCore(t *testing.T) {
	s, _ := testServer(t)
	s.Geo = &testGeo{}
	commands := &gatewayCommander{}
	s.Core.Commands = commands
	input := gatewayInput(t, "203.0.113.1")
	input.Enabled = false
	gatewayRequest(t, s, "POST", "/api/egress", input, 201)
	cfg := s.Config.Get()
	g := cfg.EgressGateways[0]
	if len(cfg.Inbounds[0].EgressCredentials) != 1 {
		t.Fatal("disabled draft credentials were not saved")
	}
	g.Marker = "draft"
	gatewayRequest(t, s, "PUT", "/api/egress/"+g.ID, g, 200)
	gatewayRequest(t, s, "DELETE", "/api/egress/"+g.ID, nil, 200)
	if commands.checks != 0 || commands.restarts != 0 || commands.stops != 0 {
		t.Fatalf("disabled draft disrupted core: checks=%d restarts=%d stops=%d", commands.checks, commands.restarts, commands.stops)
	}
	if len(s.Config.Get().Inbounds[0].EgressCredentials) != 0 {
		t.Fatal("deleted draft retained credentials")
	}
	gatewayRequest(t, s, "POST", "/api/egress", input, 201)
	g = s.Config.Get().EgressGateways[0]
	g.Enabled = true
	gatewayRequest(t, s, "PUT", "/api/egress/"+g.ID, g, 200)
	if commands.checks != 1 || commands.restarts != 1 {
		t.Fatal("enabling draft did not apply the additional egress")
	}
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
	if len(links) != 6 || title != newTitle || links[2].Fragment != "US-Boardman-VLESS-AWS" || links[3].Fragment != "US-Boardman-HY2-AWS" || links[4].Fragment != "JP-Tokyo-VLESS-JP1" {
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
	// This edit actually swaps two enabled gateways; changing 0 to 0 did not.
	restarts := commander.restarts
	aws.Position = 2
	gatewayRequest(t, s, "PUT", "/api/egress/"+aws.ID, aws, 200)
	if commander.checks != checks || commander.restarts != restarts {
		t.Fatal("display reorder restarted core")
	}
	links, _ = gatewaySubscription(t, s)
	if links[2].Fragment != "JP-Tokyo-VLESS-JP1" {
		t.Fatal("display order did not change subscription")
	}
	aws.Position = 0
	gatewayRequest(t, s, "PUT", "/api/egress/"+aws.ID, aws, 200)
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

func TestAppRoutesAPIAndRendering(t *testing.T) {
	s, _ := testServer(t)
	s.Geo = &testGeo{}
	s.Core.Commands = &gatewayCommander{}

	gatewayRequest(t, s, "POST", "/api/egress", gatewayInput(t, "203.0.113.10"), 201)
	gwID := s.Config.Get().EgressGateways[0].ID

	// GET initial app-routes
	resp := gatewayRequest(t, s, "GET", "/api/egress/app-routes", nil, 200)
	var view appRoutesView
	if err := json.Unmarshal(resp.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Gateways) != 1 || view.Gateways[0].ID != gwID || len(view.Apps) == 0 {
		t.Fatalf("unexpected initial appRoutesView: gateways=%d apps=%d", len(view.Gateways), len(view.Apps))
	}

	// Set YouTube -> direct (entry machine itself), OpenAI -> gwID, Telegram -> gwID, and custom site:linux.do -> direct
	gatewayRequest(t, s, "PUT", "/api/egress/app-routes", map[string]any{"appId": "youtube", "target": "direct"}, 200)
	gatewayRequest(t, s, "PUT", "/api/egress/app-routes", map[string]any{"appId": "openai", "target": gwID}, 200)
	gatewayRequest(t, s, "PUT", "/api/egress/app-routes", map[string]any{"appId": "telegram", "target": gwID}, 200)
	gatewayRequest(t, s, "PUT", "/api/egress/app-routes", map[string]any{"appId": "site:linux.do", "target": "direct"}, 200)

	// Invalid app ID or gateway ID must be rejected
	gatewayRequest(t, s, "PUT", "/api/egress/app-routes", map[string]any{"appId": "nonexistent-app", "target": "direct"}, 400)
	gatewayRequest(t, s, "PUT", "/api/egress/app-routes", map[string]any{"appId": "youtube", "target": "missing-gw"}, 400)

	cfg := s.Config.Get()
	if cfg.AppRoutes["youtube"] != "direct" || cfg.AppRoutes["openai"] != gwID || cfg.AppRoutes["site:linux.do"] != "direct" {
		t.Fatalf("unexpected saved AppRoutes: %+v", cfg.AppRoutes)
	}

	// Verify rendered sing-box config places app route rules BEFORE auth_user gateway rules
	rendered, err := s.Core.Renderer.Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	renderedStr := string(rendered)
	youtubeIdx := strings.Index(renderedStr, "googlevideo.com")
	openaiIdx := strings.Index(renderedStr, "chatgpt.com")
	authUserIdx := strings.Index(renderedStr, "auth_user")
	if youtubeIdx < 0 || openaiIdx < 0 || authUserIdx < 0 {
		t.Fatalf("expected app domains and auth_user in rendered config:\n%s", renderedStr)
	}
	if youtubeIdx > authUserIdx || openaiIdx > authUserIdx {
		t.Fatalf("app route rules must appear before auth_user fallback rules")
	}
	// The tunnel is IPv4 only: Telegram's IPv4 ranges use the gateway and its
	// IPv6 ranges are rejected rather than leaving through the node's exit.
	cidrAction := func(data []byte, cidr string) string {
		t.Helper()
		var doc struct {
			Route struct {
				Rules []map[string]any `json:"rules"`
			} `json:"route"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		for _, rule := range doc.Route.Rules {
			items, _ := rule["ip_cidr"].([]any)
			for _, item := range items {
				if item == cidr {
					if rule["action"] == "route" {
						return rule["outbound"].(string)
					}
					return rule["action"].(string)
				}
			}
		}
		return ""
	}
	gatewayTag := cfg.EgressGateways[0].EndpointTag()
	if got := cidrAction(rendered, "91.108.4.0/22"); got != gatewayTag {
		t.Fatalf("telegram IPv4 range routed to %q, want %q", got, gatewayTag)
	}
	if got := cidrAction(rendered, "2001:b28:f23d::/48"); got != "reject" {
		t.Fatalf("telegram IPv6 range on a gateway route = %q, want reject", got)
	}
	directCfg := cfg
	directCfg.AppRoutes = map[string]string{"telegram": model.AppRouteDirect}
	directRendered, err := s.Core.Renderer.Render(directCfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := cidrAction(directRendered, "2001:b28:f23d::/48"); got != "direct" {
		t.Fatalf("telegram IPv6 range on a direct route = %q, want direct", got)
	}

	// Deleting the gateway must automatically clean up AppRoutes referencing gwID while preserving "direct" routes
	gatewayRequest(t, s, "DELETE", "/api/egress/"+gwID, nil, 200)
	cfgAfter := s.Config.Get()
	if _, exists := cfgAfter.AppRoutes["openai"]; exists {
		t.Fatalf("expected deleted gateway route to be pruned, got %+v", cfgAfter.AppRoutes)
	}
	if cfgAfter.AppRoutes["youtube"] != "direct" {
		t.Fatalf("expected direct route for youtube to be preserved, got %+v", cfgAfter.AppRoutes)
	}
}

func TestAppRoutesToleratesUnloadedRulesAndNormalizesSites(t *testing.T) {
	s, _ := testServer(t)
	s.Geo = &testGeo{}
	s.Core.Commands = &gatewayCommander{}
	gatewayRequest(t, s, "POST", "/api/egress", gatewayInput(t, "203.0.113.10"), 201)
	cfg := s.Config.Get()
	gwID := cfg.EgressGateways[0].ID

	// A route saved under a rule that a later rule file no longer contains,
	// plus a mixed-case site key written before keys were normalized.
	cfg.AppRoutes = map[string]string{"openai": gwID, "ghost-app": gwID, "site:Legacy.Example": model.AppRouteDirect}
	if err := s.Config.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	if err := protocol.DefaultRegistry().ValidateConfig(cfg); err != nil {
		t.Fatalf("an unloaded app rule must not block startup: %v", err)
	}
	rendered, err := s.Core.Renderer.Render(cfg)
	if err != nil || strings.Contains(string(rendered), "ghost") {
		t.Fatalf("render must ignore unloaded app rules: err=%v", err)
	}
	var view appRoutesView
	if err := json.Unmarshal(gatewayRequest(t, s, "GET", "/api/egress/app-routes", nil, 200).Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if _, shown := view.Routes["ghost-app"]; shown || view.Routes["openai"] != gwID {
		t.Fatalf("view must hide only unloaded rules: %+v", view.Routes)
	}

	// Unrelated changes keep working, and stale or mixed-case keys stay removable.
	gatewayRequest(t, s, "PUT", "/api/egress/app-routes", map[string]any{"appId": "youtube", "target": "direct"}, 200)
	gatewayRequest(t, s, "PUT", "/api/egress/app-routes", map[string]any{"appId": "ghost-app", "target": gwID}, 400)
	gatewayRequest(t, s, "PUT", "/api/egress/app-routes", map[string]any{"appId": "ghost-app", "target": "default"}, 200)
	gatewayRequest(t, s, "PUT", "/api/egress/app-routes", map[string]any{"appId": "site:Legacy.Example", "target": ""}, 200)
	gatewayRequest(t, s, "PUT", "/api/egress/app-routes", map[string]any{"appId": "site:Linux.DO", "target": gwID}, 200)
	want := map[string]string{"openai": gwID, "youtube": model.AppRouteDirect, "site:linux.do": gwID}
	if got := s.Config.Get().AppRoutes; !reflect.DeepEqual(got, want) {
		t.Fatalf("routes=%+v want=%+v", got, want)
	}

	gatewayRequest(t, s, "PUT", "/api/egress/app-routes", map[string]any{"routes": map[string]string{"site:A.example": "direct", "site:a.example": gwID}}, 400)
	gatewayRequest(t, s, "PUT", "/api/egress/app-routes", map[string]any{"routes": map[string]string{"site:A.example": "direct", "openai": gwID}}, 200)
	want = map[string]string{"openai": gwID, "site:a.example": model.AppRouteDirect}
	if got := s.Config.Get().AppRoutes; !reflect.DeepEqual(got, want) {
		t.Fatalf("bulk routes=%+v want=%+v", got, want)
	}
}
