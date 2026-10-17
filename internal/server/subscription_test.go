package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/boltguo/sbm/internal/model"
	"github.com/boltguo/sbm/internal/protocol"
)

func TestSubscriptionMultiFormatAndUserAgentDetection(t *testing.T) {
	s, cfg := testServer(t)
	cfg.Inbounds = []model.Inbound{
		{
			ID:      "vless-1",
			Type:    protocol.TypeVLESSReality,
			Name:    "JP-VLESS",
			Enabled: true,
			Port:    8443,
			VLESS: &model.VLESSOptions{
				UUID:       "11111111-2222-4333-8444-555555555555",
				SNI:        "www.microsoft.com",
				PrivateKey: "priv-key",
				PublicKey:  "pub-key",
				ShortID:    "01ab",
			},
			EgressCredentials: []model.EgressCredential{
				{GatewayID: "us1", UUID: "22222222-3333-4444-8555-666666666666"},
			},
		},
		{
			ID:      "hy2-1",
			Type:    protocol.TypeHysteria2,
			Name:    "JP-HY2",
			Enabled: true,
			Port:    443,
			Hysteria2: &model.Hysteria2Options{
				Password:     "hy2-secret-pass",
				Obfs:         "salamander",
				ObfsPassword: "obfs-secret-pass",
			},
			EgressCredentials: []model.EgressCredential{
				{GatewayID: "us1", Password: "hy2-us-password"},
			},
		},
	}
	cfg.EgressGateways = []model.EgressGateway{
		{
			ID:               "us1",
			Enabled:          true,
			LocationOverride: "美国",
			Marker:           "US",
			Server:           "198.51.100.10",
			ServerPort:       51820,
			PrivateKey:       "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=",
			PeerPublicKey:    "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC=",
			TrafficQuota:     model.TrafficQuotaConfig{Unit: model.TrafficUnitGB, BillingMode: model.TrafficBillingSingle, HeadroomPercent: 10},
			Reset:            model.ResetConfig{Mode: "none", Day: 1, Timezone: "UTC"},
		},
	}
	if err := s.Config.Replace(cfg); err != nil {
		t.Fatal(err)
	}

	t.Run("explicit target=clash returns Clash Meta YAML", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/sub/"+cfg.SubscriptionToken+"?target=clash", nil)
		req.Header.Set("Accept", "text/html") // explicit target overrides browser Accept header
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/yaml") {
			t.Fatalf("unexpected content-type %q", ct)
		}
		body := rec.Body.String()
		for _, want := range []string{
			"mixed-port: 7890",
			"allow-lan: false",
			`name: "JP-VLESS"`,
			"type: vless",
			"flow: xtls-rprx-vision",
			`public-key: "pub-key"`,
			`short-id: "01ab"`,
			`name: "JP-HY2"`,
			"type: hysteria2",
			`obfs: "salamander"`,
			`obfs-password: "obfs-secret-pass"`,
			`name: "美国-VLESS-US"`,
			`uuid: "22222222-3333-4444-8555-666666666666"`,
			`name: "美国-HY2-US"`,
			`password: "hy2-us-password"`,
			"proxy-groups:",
			"MATCH,🚀 节点选择",
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("clash yaml missing %q:\n%s", want, body)
			}
		}
	})

	t.Run("User-Agent clash-verge auto-detects Clash YAML", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/sub/"+cfg.SubscriptionToken, nil)
		req.Header.Set("User-Agent", "clash-verge/v1.7.7")
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "text/yaml") {
			t.Fatalf("status=%d ct=%q", rec.Code, rec.Header().Get("Content-Type"))
		}
		if !strings.Contains(rec.Body.String(), "proxies:") {
			t.Fatalf("expected clash yaml, got %s", rec.Body.String())
		}
	})

	t.Run("explicit target=singbox returns valid sing-box JSON", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/sub/"+cfg.SubscriptionToken+"?target=singbox", nil)
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Fatalf("unexpected content-type %q", ct)
		}
		var doc map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			t.Fatalf("invalid sing-box JSON: %v\n%s", err, rec.Body.String())
		}
		body := rec.Body.String()
		for _, want := range []string{
			`"tag": "🚀 节点选择"`,
			`"tag": "⚡ 自动优选"`,
			`"tag": "JP-VLESS"`,
			`"tag": "JP-HY2"`,
			`"tag": "美国-VLESS-US"`,
			`"tag": "美国-HY2-US"`,
			`"public_key": "pub-key"`,
			`"obfs":`,
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("sing-box json missing %q:\n%s", want, body)
			}
		}
	})

	t.Run("User-Agent SFA auto-detects sing-box JSON", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/sub/"+cfg.SubscriptionToken, nil)
		req.Header.Set("User-Agent", "SFA/1.11.0 (iOS; sing-box 1.11.0)")
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("status=%d ct=%q", rec.Code, rec.Header().Get("Content-Type"))
		}
	})

	t.Run("explicit target=v2ray returns base64 links even with Clash UA", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/sub/"+cfg.SubscriptionToken+"?target=v2ray", nil)
		req.Header.Set("User-Agent", "clash-verge/v1.7.7")
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "text/plain") {
			t.Fatalf("status=%d ct=%q", rec.Code, rec.Header().Get("Content-Type"))
		}
		decoded, err := base64.StdEncoding.DecodeString(rec.Body.String())
		if err != nil {
			t.Fatalf("base64 decode failed: %v", err)
		}
		lines := strings.Split(strings.TrimSpace(string(decoded)), "\n")
		if len(lines) != 4 {
			t.Fatalf("expected 4 share links, got %d: %s", len(lines), decoded)
		}
	})
}

func TestSubscriptionUserAgentDetection(t *testing.T) {
	for ua, want := range map[string]string{
		"clash-verge/v2.2.3":                subTargetClash,
		"ClashMetaForAndroid/2.11.1.Meta":   subTargetClash,
		"mihomo.party/v1.7.0 (clash.meta)":  subTargetClash,
		"Stash/2.4.6 Clash/1.0":             subTargetClash,
		"SFI/1.12.0 (iOS; sing-box 1.12.0)": subTargetSingBox,
		// Strings as the clients' own sources build them.
		"NekoBox/Android/1.3.9 (Prefer ClashMeta Format)":            subTargetClash,
		"HiddifyNext/2.5.7 (android) like ClashMeta v2ray sing-box":  subTargetClash,
		"SFA (sing-box 1.13.14; language zh_CN)":                     subTargetSingBox,
		"v2rayNG/1.9.30":                                             subTargetBase64,
		"Shadowrocket/2070 CFNetwork/1410.0.3 Darwin/22.6.0":         subTargetBase64,
		"Mozilla/5.0 (Windows NT 10.0) Chrome/120 SE 2.X MetaSr 1.0": subTargetBase64,
	} {
		req := httptest.NewRequest(http.MethodGet, "/sub/token", nil)
		req.Header.Set("User-Agent", ua)
		if got, explicit := resolveSubscriptionTarget(req); got != want || explicit {
			t.Errorf("UA %q: got %s explicit=%v, want %s", ua, got, explicit, want)
		}
	}
}

func singBoxSubscriptionNodes() []model.Inbound {
	return []model.Inbound{
		{ID: "v", Type: protocol.TypeVLESSReality, Name: "JP-VLESS", Enabled: true, Port: 443, VLESS: &model.VLESSOptions{UUID: "11111111-2222-4333-8444-555555555555", SNI: "www.microsoft.com", PrivateKey: "x", PublicKey: "jNXHt1yRo0vDuchQlIP6Z0ZvjT3KtzVI-T4E7RoLJS0", ShortID: "01ab"}},
		{ID: "h", Type: protocol.TypeHysteria2, Name: "JP-HY2", Enabled: true, Port: 443, Hysteria2: &model.Hysteria2Options{Password: "hy2-secret-pass", Obfs: "salamander", ObfsPassword: "obfs-secret-pass"}},
	}
}

func TestSingBoxSubscriptionAvoidsRemovedFields(t *testing.T) {
	body, err := renderSingBoxSubscription("node.example.com", singBoxSubscriptionNodes())
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		DNS struct {
			Servers []map[string]any `json:"servers"`
		} `json:"dns"`
		Inbounds []map[string]any `json:"inbounds"`
		Route    struct {
			DefaultDomainResolver string           `json:"default_domain_resolver"`
			Rules                 []map[string]any `json:"rules"`
		} `json:"route"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	for _, server := range doc.DNS.Servers {
		if _, legacy := server["address"]; legacy || server["type"] == nil {
			t.Fatalf("legacy DNS server: %v", server)
		}
	}
	for _, inbound := range doc.Inbounds {
		if _, legacy := inbound["sniff"]; legacy {
			t.Fatalf("legacy inbound sniff field: %v", inbound)
		}
	}
	if doc.Route.DefaultDomainResolver != "dns-direct" || len(doc.Route.Rules) == 0 || doc.Route.Rules[0]["action"] != "sniff" {
		t.Fatalf("route must sniff first and resolve node domains directly: %s", body)
	}
}

func TestSingBoxSubscriptionIntegration(t *testing.T) {
	binary := os.Getenv("SBM_TEST_SING_BOX")
	if binary == "" {
		t.Skip("set SBM_TEST_SING_BOX to the official sing-box 1.13.14 binary")
	}
	version, err := exec.Command(binary, "version").Output()
	if err != nil || !strings.HasPrefix(string(version), "sing-box version 1.13.14\n") {
		t.Fatal("integration needs sing-box 1.13.14")
	}
	for _, nodes := range [][]model.Inbound{singBoxSubscriptionNodes(), nil} {
		body, err := renderSingBoxSubscription("node.example.com", nodes)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "client.json")
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
		// A deprecation warning today is a removal in the next sing-box release.
		output, err := exec.Command(binary, "check", "-c", path).CombinedOutput()
		if err != nil || strings.Contains(strings.ToLower(string(output)), "deprecated") {
			t.Fatalf("sing-box check failed for %d nodes: %v\n%s", len(nodes), err, output)
		}
	}
}
