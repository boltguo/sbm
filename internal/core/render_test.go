package core

import (
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
