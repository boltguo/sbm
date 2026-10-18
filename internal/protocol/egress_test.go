package protocol

import (
	"encoding/base64"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/boltguo/sbm/internal/model"
)

func testGateway(id string, slot int) model.EgressGateway {
	k, _ := GenerateWireGuardKeys()
	return model.EgressGateway{ID: id, Enabled: true, TunnelSlot: slot, Server: "203.0.113." + string(rune('0'+slot)), ServerPort: 51820, PrivateKey: k.Private, PeerPublicKey: k.Public, TrafficQuota: model.DefaultConfig().TrafficQuota, Reset: model.DefaultConfig().Reset, Geo: model.GatewayGeo{IP: "203.0.113." + string(rune('0'+slot)), CountryCode: "US", City: "Boardman"}, Marker: "AWS"}
}
func TestEgressNamingAndVariants(t *testing.T) {
	g := testGateway("aws", 1)
	for _, c := range []struct {
		g        model.EgressGateway
		want     string
		vless    string
		hysteria string
	}{
		{g, "US-Boardman-AWS", "US-Boardman-VLESS-AWS", "US-Boardman-HY2-AWS"},
		{func() model.EgressGateway { v := g; v.Marker = ""; return v }(), "US-Boardman", "US-Boardman-VLESS", "US-Boardman-HY2"},
		{func() model.EgressGateway { v := g; v.LocationOverride = "JP-Tokyo"; return v }(), "JP-Tokyo-AWS", "JP-Tokyo-VLESS-AWS", "JP-Tokyo-HY2-AWS"},
		{func() model.EgressGateway { v := g; v.Geo = model.GatewayGeo{}; return v }(), "AWS", "VLESS-AWS", "HY2-AWS"},
		{func() model.EgressGateway { v := g; v.Geo = model.GatewayGeo{}; v.Marker = ""; return v }(), "Gateway-aws", "Gateway-aws-VLESS", "Gateway-aws-HY2"},
	} {
		if got := GatewayName(c.g); got != c.want {
			t.Errorf("name=%s want=%s", got, c.want)
		}
		if got := gatewayName(c.g, "VLESS"); got != c.vless {
			t.Errorf("VLESS name=%s want=%s", got, c.vless)
		}
		if got := gatewayName(c.g, "HY2"); got != c.hysteria {
			t.Errorf("HY2 name=%s want=%s", got, c.hysteria)
		}
	}
	cfg := testConfig()
	cfg.Inbounds = []model.Inbound{
		{ID: "vless", Type: TypeVLESSReality, Name: "US-LosAngeles-VLESS", Enabled: true, Port: 443, VLESS: &model.VLESSOptions{UUID: "70d0c699-73a0-4d2a-a45d-4f46a661b4f2", SNI: "www.apple.com", PrivateKey: "private", PublicKey: "public", ShortID: "aabb"}},
		{ID: "hy2", Type: TypeHysteria2, Name: "US-LosAngeles-HY2", Enabled: true, Port: 443, Hysteria2: &model.Hysteria2Options{Password: "direct-password", Obfs: "salamander", ObfsPassword: "obfs-password"}},
	}
	cfg.EgressGateways = []model.EgressGateway{g, testGateway("jp", 2)}
	directV, directH := *cfg.Inbounds[0].VLESS, *cfg.Inbounds[1].Hysteria2
	if err := SyncEgressCredentials(&cfg); err != nil {
		t.Fatal(err)
	}
	original := append([]model.EgressCredential(nil), cfg.Inbounds[0].EgressCredentials...)
	if err := DefaultRegistry().ValidateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	for _, in := range cfg.Inbounds {
		d, _ := DefaultRegistry().Get(in.Type)
		for _, gateway := range cfg.EgressGateways {
			variant, ok := EgressVariant(in, gateway)
			if !ok {
				t.Fatal("missing variant")
			}
			link, err := d.ShareLink(variant, ShareContext{Domain: cfg.Domain})
			if err != nil {
				t.Fatal(err)
			}
			u, err := url.Parse(link)
			if err != nil {
				t.Fatal(err)
			}
			wantName := "US-Boardman-VLESS-AWS"
			if in.Type == TypeHysteria2 {
				wantName = "US-Boardman-HY2-AWS"
			}
			if u.Host != "node.example.com:443" || u.Fragment != wantName {
				t.Fatal("gateway link endpoint or name incorrect")
			}
			if in.VLESS != nil {
				if u.User.Username() == directV.UUID || u.Query().Get("sni") != directV.SNI || u.Query().Get("pbk") != directV.PublicKey || variant.VLESS.PrivateKey != directV.PrivateKey || u.Query().Get("flow") != "xtls-rprx-vision" {
					t.Fatal("VLESS variant did not preserve options")
				}
			} else {
				if u.User.Username() == directH.Password || u.Query().Get("obfs-password") != directH.ObfsPassword || u.Query().Get("sni") != cfg.Domain {
					t.Fatal("HY2 variant did not preserve options")
				}
			}
		}
	}
	cfg.EgressGateways[0].Enabled = false
	cfg.EgressGateways[0].Marker = "renamed"
	cfg.EgressGateways[0].Position = 99
	if err := SyncEgressCredentials(&cfg); err != nil {
		t.Fatal(err)
	}
	cfg.EgressGateways[0].Enabled = true
	if err := SyncEgressCredentials(&cfg); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, cfg.Inbounds[0].EgressCredentials) || *cfg.Inbounds[0].VLESS != directV || *cfg.Inbounds[1].Hysteria2 != directH {
		t.Fatal("rename/disable/reenable rotated credentials")
	}
	cfg.EgressGateways = cfg.EgressGateways[1:]
	if err := SyncEgressCredentials(&cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Inbounds[0].EgressCredentials) != 1 || cfg.Inbounds[0].EgressCredentials[0].GatewayID != "jp" {
		t.Fatal("deleted credentials retained")
	}
}
func TestEgressValidation(t *testing.T) {
	for _, mutate := range []func(*model.Config){
		func(c *model.Config) { c.EgressGateways[1].ID = c.EgressGateways[0].ID },
		func(c *model.Config) { c.EgressGateways[1].TunnelSlot = 1 },
		func(c *model.Config) { c.EgressGateways[1].Server = c.EgressGateways[0].Server },
		func(c *model.Config) { c.EgressGateways[0].PrivateKey = "secret-invalid" },
		func(c *model.Config) {
			c.EgressGateways[0].PeerPublicKey = base64.StdEncoding.EncodeToString(make([]byte, 31))
		},
		func(c *model.Config) {
			c.Inbounds[0].EgressCredentials = append(c.Inbounds[0].EgressCredentials, c.Inbounds[0].EgressCredentials[0])
		},
		func(c *model.Config) { c.Inbounds[0].EgressCredentials[0].Password = c.Inbounds[0].Hysteria2.Password },
		func(c *model.Config) { c.Inbounds[0].EgressCredentials = nil },
		func(c *model.Config) { c.EgressGateways[0].Server = "127.0.0.1" },
	} {
		cfg := testConfig()
		cfg.Inbounds = []model.Inbound{{ID: "hy2", Type: TypeHysteria2, Name: "HY2", Port: 443, Hysteria2: &model.Hysteria2Options{Password: "direct-password"}}}
		cfg.EgressGateways = []model.EgressGateway{testGateway("one", 1), testGateway("two", 2)}
		if err := SyncEgressCredentials(&cfg); err != nil {
			t.Fatal(err)
		}
		mutate(&cfg)
		if err := DefaultRegistry().ValidateConfig(cfg); err == nil {
			t.Fatal("invalid egress config accepted")
		} else if strings.Contains(err.Error(), "secret-invalid") {
			t.Fatal("key leaked")
		}
	}
}
func TestWireGuardKeysStrict(t *testing.T) {
	k, err := GenerateWireGuardKeys()
	if err != nil {
		t.Fatal(err)
	}
	pub, err := WireGuardPublicKey(k.Private)
	if err != nil || pub != k.Public {
		t.Fatal("keypair mismatch")
	}
	for _, value := range []string{"", k.Private + "\n", base64.StdEncoding.EncodeToString(make([]byte, 33))} {
		if validateWireGuardKey(value) == nil {
			t.Fatal("invalid WG key accepted")
		}
		if _, err := WireGuardPublicKey(value); err == nil {
			t.Fatal("public derivation accepted invalid key")
		}
	}
}
