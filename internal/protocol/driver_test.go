package protocol

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/boltguo/sbm/internal/model"
)

func testConfig() model.Config {
	secret := strings.Repeat("a", 43)
	cfg := model.DefaultConfig()
	cfg.Domain = "node.example.com"
	cfg.AdminPasswordHash = "hash"
	cfg.SessionSecret, cfg.ClashAPISecret, cfg.SubscriptionToken = secret, secret, secret
	return cfg
}

func TestVLESSShareLinkAndURLEncoding(t *testing.T) {
	in := model.Inbound{ID: "one", Type: TypeVLESSReality, Name: "上海 节点/01", Enabled: true, Port: 443, VLESS: &model.VLESSOptions{
		UUID: "70d0c699-73a0-4d2a-a45d-4f46a661b4f2", SNI: "www.apple.com", PrivateKey: "private", PublicKey: "public+/key", ShortID: "a1b2c3d4",
	}}
	link, err := (VLESSDriver{}).ShareLink(in, ShareContext{Domain: "node.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"vless://70d0c699-73a0-4d2a-a45d-4f46a661b4f2@node.example.com:443?", "flow=xtls-rprx-vision", "pbk=public%2B%2Fkey", "#%E4%B8%8A%E6%B5%B7%20%E8%8A%82%E7%82%B9%2F01"} {
		if !strings.Contains(link, expected) {
			t.Errorf("link %q missing %q", link, expected)
		}
	}
}

func TestHysteria2ShareLink(t *testing.T) {
	in := model.Inbound{ID: "two", Type: TypeHysteria2, Name: "HY2 香港", Enabled: true, Port: 8443, Hysteria2: &model.Hysteria2Options{Password: "pass@word/123", Obfs: "salamander", ObfsPassword: "obfs secret"}}
	link, err := (Hysteria2Driver{}).ShareLink(in, ShareContext{Domain: "node.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"hysteria2://pass%40word%2F123@node.example.com:8443?", "alpn=h3", "obfs=salamander", "obfs-password=obfs+secret", "#HY2%20%E9%A6%99%E6%B8%AF"} {
		if !strings.Contains(link, expected) {
			t.Errorf("link %q missing %q", link, expected)
		}
	}
}

func TestSubscriptionBase64(t *testing.T) {
	links := []string{"vless://one", "hysteria2://two"}
	encoded := base64.StdEncoding.EncodeToString([]byte(strings.Join(links, "\n")))
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || string(decoded) != "vless://one\nhysteria2://two" {
		t.Fatalf("unexpected subscription: %q, %v", decoded, err)
	}
}

func TestPortConflictsRespectNetwork(t *testing.T) {
	cfg := testConfig()
	cfg.Inbounds = []model.Inbound{
		{ID: "v1", Type: TypeVLESSReality, Name: "V1", Port: 443, VLESS: &model.VLESSOptions{UUID: "70d0c699-73a0-4d2a-a45d-4f46a661b4f2", SNI: "www.apple.com", PrivateKey: "p", PublicKey: "P", ShortID: "aabb"}},
		{ID: "h1", Type: TypeHysteria2, Name: "H1", Port: 443, Hysteria2: &model.Hysteria2Options{Password: "long-password"}},
	}
	if err := DefaultRegistry().ValidateConfig(cfg); err != nil {
		t.Fatalf("TCP and UDP 443 should coexist: %v", err)
	}
	cfg.Inbounds = append(cfg.Inbounds, model.Inbound{ID: "v2", Type: TypeVLESSReality, Name: "V2", Port: 443, VLESS: cfg.Inbounds[0].VLESS})
	if err := DefaultRegistry().ValidateConfig(cfg); err == nil || !strings.Contains(err.Error(), "端口冲突") {
		t.Fatalf("expected TCP conflict, got %v", err)
	}
}

func TestPanelPortConflict(t *testing.T) {
	cfg := testConfig()
	cfg.Inbounds = []model.Inbound{{ID: "v1", Type: TypeVLESSReality, Name: "V1", Port: 2096, VLESS: &model.VLESSOptions{UUID: "70d0c699-73a0-4d2a-a45d-4f46a661b4f2", SNI: "www.apple.com", PrivateKey: "p", PublicKey: "P", ShortID: "aabb"}}}
	if err := DefaultRegistry().ValidateConfig(cfg); err == nil || !strings.Contains(err.Error(), "面板端口冲突") {
		t.Fatalf("expected panel conflict, got %v", err)
	}
}
