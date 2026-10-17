package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
