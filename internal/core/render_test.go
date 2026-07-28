package core

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/boltguo/sbm/internal/model"
	"github.com/boltguo/sbm/internal/protocol"
)

func TestRenderConfig(t *testing.T) {
	secret := strings.Repeat("s", 43)
	cfg := model.DefaultConfig()
	cfg.Domain = "node.example.com"
	cfg.AdminPasswordHash = "hash"
	cfg.SessionSecret = secret
	cfg.ClashAPISecret = secret
	cfg.SubscriptionToken = secret
	cfg.Inbounds = []model.Inbound{{ID: "hy2", Type: protocol.TypeHysteria2, Name: "HY2", Enabled: true, Port: 443, Hysteria2: &model.Hysteria2Options{Password: "password-123"}}}
	data, err := (Renderer{Registry: protocol.DefaultRegistry(), BuildContext: protocol.BuildContext{CertificatePath: "/cert.pem", KeyPath: "/key.pem"}}).Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, expected := range []string{`"external_controller": "127.0.0.1:9090"`, `"secret": "` + secret + `"`, `"certificate_path": "/cert.pem"`, `"type": "hysteria2"`} {
		if !strings.Contains(text, expected) {
			t.Errorf("render missing %s", expected)
		}
	}
}

func TestRenderOutboundStrategy(t *testing.T) {
	cfg := validRenderConfig()
	cfg.OutboundStrategy = model.OutboundStrategyPreferIPv6
	data, err := (Renderer{Registry: protocol.DefaultRegistry()}).Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var rendered struct {
		DNS struct {
			Servers []map[string]any `json:"servers"`
		} `json:"dns"`
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal(data, &rendered); err != nil {
		t.Fatal(err)
	}
	if len(rendered.DNS.Servers) != 1 || rendered.DNS.Servers[0]["type"] != "local" || rendered.DNS.Servers[0]["tag"] != "local" {
		t.Fatalf("unexpected DNS servers: %#v", rendered.DNS.Servers)
	}
	resolver, ok := rendered.Outbounds[0]["domain_resolver"].(map[string]any)
	if !ok || resolver["server"] != "local" || resolver["strategy"] != model.OutboundStrategyPreferIPv6 {
		t.Fatalf("unexpected domain resolver: %#v", rendered.Outbounds[0]["domain_resolver"])
	}
}

func TestRenderAutomaticOutboundStrategyNeedsNoDNSSection(t *testing.T) {
	for _, strategy := range []string{"", model.OutboundStrategyAuto} {
		cfg := validRenderConfig()
		cfg.OutboundStrategy = strategy
		data, err := (Renderer{Registry: protocol.DefaultRegistry()}).Render(cfg)
		if err != nil {
			t.Fatal(err)
		}
		var rendered map[string]any
		if err := json.Unmarshal(data, &rendered); err != nil {
			t.Fatal(err)
		}
		if _, exists := rendered["dns"]; exists {
			t.Fatalf("strategy %q unexpectedly rendered a DNS section", strategy)
		}
		outbounds := rendered["outbounds"].([]any)
		if _, exists := outbounds[0].(map[string]any)["domain_resolver"]; exists {
			t.Fatalf("strategy %q unexpectedly rendered a domain resolver", strategy)
		}
	}
}

func TestRenderWireGuardExit(t *testing.T) {
	cfg := validRenderConfig()
	cfg.WireGuardExit = validWireGuardExit()
	cfg.Inbounds = []model.Inbound{
		{
			ID: "vless", Type: protocol.TypeVLESSReality, Name: "VLESS", Enabled: true, Port: 443,
			VLESS: &model.VLESSOptions{
				UUID: "70d0c699-73a0-4d2a-a45d-4f46a661b4f2", WireGuardExitUUID: "c2130be7-66b8-4d32-8f0d-856cc438b656",
				SNI: "www.apple.com", PrivateKey: "private", PublicKey: "public", ShortID: "aabb",
			},
		},
		{
			ID: "hy2", Type: protocol.TypeHysteria2, Name: "HY2", Enabled: true, Port: 443,
			Hysteria2: &model.Hysteria2Options{Password: "direct-password", WireGuardExitPassword: "exit-password"},
		},
	}
	data, err := (Renderer{Registry: protocol.DefaultRegistry()}).Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var rendered struct {
		DNS struct {
			Servers []map[string]any `json:"servers"`
		} `json:"dns"`
		Endpoints []map[string]any `json:"endpoints"`
		Inbounds  []map[string]any `json:"inbounds"`
		Outbounds []map[string]any `json:"outbounds"`
		Route     map[string]any   `json:"route"`
	}
	if err := json.Unmarshal(data, &rendered); err != nil {
		t.Fatal(err)
	}
	if len(rendered.Endpoints) != 1 {
		t.Fatalf("endpoints = %#v", rendered.Endpoints)
	}
	endpoint := rendered.Endpoints[0]
	if endpoint["type"] != "wireguard" || endpoint["tag"] != "exit-wireguard" || endpoint["system"] != false || endpoint["private_key"] != cfg.WireGuardExit.PrivateKey {
		t.Fatalf("unexpected WireGuard endpoint: %#v", endpoint)
	}
	addresses := endpoint["address"].([]any)
	if endpoint["mtu"] != float64(model.WireGuardExitMTU) || len(addresses) != 1 || addresses[0] != model.WireGuardExitLocalAddress {
		t.Fatalf("unexpected built-in tunnel settings: %#v", endpoint)
	}
	peers, ok := endpoint["peers"].([]any)
	if !ok || len(peers) != 1 {
		t.Fatalf("unexpected peers: %#v", endpoint["peers"])
	}
	peer := peers[0].(map[string]any)
	if peer["address"] != cfg.WireGuardExit.Server ||
		peer["public_key"] != cfg.WireGuardExit.PeerPublicKey ||
		peer["persistent_keepalive_interval"] != float64(model.WireGuardExitPersistentKeepalive) {
		t.Fatalf("unexpected peer: %#v", peer)
	}
	if _, exists := peer["pre_shared_key"]; exists {
		t.Fatalf("unexpected pre-shared key: %#v", peer)
	}
	allowed := peer["allowed_ips"].([]any)
	if len(allowed) != 1 || allowed[0] != "0.0.0.0/0" {
		t.Fatalf("unexpected allowed IPs: %#v", allowed)
	}
	if rendered.Route["final"] != "direct" {
		t.Fatalf("route final = %#v", rendered.Route["final"])
	}
	rules, ok := rendered.Route["rules"].([]any)
	if !ok || len(rules) != 2 {
		t.Fatalf("unexpected route rules: %#v", rendered.Route["rules"])
	}
	resolveRule := rules[0].(map[string]any)
	routeRule := rules[1].(map[string]any)
	if resolveRule["action"] != "resolve" || resolveRule["server"] != "local" || resolveRule["strategy"] != model.OutboundStrategyIPv4Only {
		t.Fatalf("unexpected resolve rule: %#v", resolveRule)
	}
	if routeRule["action"] != "route" || routeRule["outbound"] != "exit-wireguard" {
		t.Fatalf("unexpected route rule: %#v", routeRule)
	}
	if _, exists := rendered.Route["default_domain_resolver"]; exists {
		t.Fatalf("global resolver unexpectedly changed: %#v", rendered.Route)
	}
	if len(rendered.DNS.Servers) != 1 || rendered.DNS.Servers[0]["type"] != "local" {
		t.Fatalf("unexpected DNS servers: %#v", rendered.DNS.Servers)
	}
	if _, exists := rendered.Outbounds[0]["domain_resolver"]; exists {
		t.Fatalf("direct outbound strategy unexpectedly changed: %#v", rendered.Outbounds[0])
	}
	for _, inbound := range rendered.Inbounds {
		users, ok := inbound["users"].([]any)
		if !ok || len(users) != 2 {
			t.Fatalf("companion user missing from inbound: %#v", inbound)
		}
		exitUser := users[1].(map[string]any)
		if !strings.HasPrefix(exitUser["name"].(string), "exit-wireguard-") {
			t.Fatalf("unexpected companion auth user: %#v", exitUser)
		}
	}
}

func TestRenderDisabledWireGuardHidesCompanionUsers(t *testing.T) {
	cfg := validRenderConfig()
	cfg.Inbounds = []model.Inbound{{
		ID: "hy2", Type: protocol.TypeHysteria2, Name: "HY2", Enabled: true, Port: 443,
		Hysteria2: &model.Hysteria2Options{Password: "direct-password", WireGuardExitPassword: "stored-exit-password"},
	}}
	data, err := (Renderer{Registry: protocol.DefaultRegistry()}).Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var rendered struct {
		Inbounds []map[string]any `json:"inbounds"`
	}
	if err := json.Unmarshal(data, &rendered); err != nil {
		t.Fatal(err)
	}
	users := rendered.Inbounds[0]["users"].([]any)
	if len(users) != 1 {
		t.Fatalf("disabled companion user was rendered: %#v", users)
	}
}

func validRenderConfig() model.Config {
	secret := strings.Repeat("s", 43)
	cfg := model.DefaultConfig()
	cfg.Domain = "node.example.com"
	cfg.AdminPasswordHash = "hash"
	cfg.SessionSecret = secret
	cfg.ClashAPISecret = secret
	cfg.SubscriptionToken = secret
	return cfg
}

func validWireGuardExit() *model.WireGuardExitConfig {
	key := func(value byte) string {
		return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{value}, 32))
	}
	return &model.WireGuardExitConfig{
		Enabled: true, Server: "203.0.113.10", ServerPort: 51820,
		PrivateKey: key(1), PeerPublicKey: key(2),
	}
}
