package core

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/boltguo/sbm/internal/model"
	"github.com/boltguo/sbm/internal/protocol"
)

func multiEgressConfig(t *testing.T) model.Config {
	t.Helper()
	cfg := validRenderConfig()
	reality, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Inbounds = []model.Inbound{
		{ID: "vless", Type: protocol.TypeVLESSReality, Name: "US-LosAngeles-VLESS", Enabled: true, Port: 443, VLESS: &model.VLESSOptions{UUID: "70d0c699-73a0-4d2a-a45d-4f46a661b4f2", SNI: "www.apple.com", PrivateKey: base64.RawURLEncoding.EncodeToString(reality.Bytes()), PublicKey: base64.RawURLEncoding.EncodeToString(reality.PublicKey().Bytes()), ShortID: "aabb"}},
		{ID: "hy2", Type: protocol.TypeHysteria2, Name: "US-LosAngeles-HY2", Enabled: true, Port: 443, Hysteria2: &model.Hysteria2Options{Password: "direct-password", Obfs: "salamander", ObfsPassword: "obfs-password"}},
	}
	for i, id := range []string{"aws", "jp", "sg"} {
		k, err := protocol.GenerateWireGuardKeys()
		if err != nil {
			t.Fatal(err)
		}
		cfg.EgressGateways = append(cfg.EgressGateways, model.EgressGateway{ID: id, Enabled: true, TunnelSlot: i + 1, Server: "203.0.113." + string(rune('1'+i)), ServerPort: 51820, PrivateKey: k.Private, PeerPublicKey: k.Public, TrafficQuota: cfg.TrafficQuota, Reset: cfg.Reset})
	}
	if err := protocol.SyncEgressCredentials(&cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}
func decodeRendered(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}
func TestMultipleEgressRender(t *testing.T) {
	cfg := multiEgressConfig(t)
	renderer := Renderer{Registry: protocol.DefaultRegistry()}
	for _, strategy := range []string{"auto", "prefer_ipv4", "prefer_ipv6", "ipv4_only", "ipv6_only"} {
		cfg.OutboundStrategy = strategy
		data, err := renderer.Render(cfg)
		if err != nil {
			t.Fatal(err)
		}
		doc := decodeRendered(t, data)
		endpoints := doc["endpoints"].([]any)
		if len(endpoints) != 3 {
			t.Fatal("wrong endpoint count")
		}
		tags, addresses := map[string]bool{}, map[string]bool{}
		for _, item := range endpoints {
			ep := item.(map[string]any)
			tag := ep["tag"].(string)
			address := ep["address"].([]any)[0].(string)
			if tags[tag] || addresses[address] || ep["system"] != false || ep["mtu"] != float64(1408) {
				t.Fatal("invalid/duplicate endpoint")
			}
			tags[tag] = true
			addresses[address] = true
		}
		auths := map[string]bool{}
		for i, item := range doc["inbounds"].([]any) {
			users := item.(map[string]any)["users"].([]any)
			if len(users) != 4 {
				t.Fatal("wrong user count")
			}
			direct := users[0].(map[string]any)
			if i == 0 && direct["uuid"] != cfg.Inbounds[i].VLESS.UUID {
				t.Fatal("direct UUID changed")
			}
			if i == 1 && direct["password"] != cfg.Inbounds[i].Hysteria2.Password {
				t.Fatal("direct password changed")
			}
			for _, item := range users {
				user := item.(map[string]any)["name"].(string)
				if auths[user] {
					t.Fatal("duplicate auth user")
				}
				auths[user] = true
			}
		}
		rules := doc["route"].(map[string]any)["rules"].([]any)
		if len(rules) != 6 {
			t.Fatal("wrong rule count")
		}
		for i, g := range cfg.EgressGateways {
			resolve, route := rules[i*2].(map[string]any), rules[i*2+1].(map[string]any)
			if resolve["strategy"] != "ipv4_only" || route["outbound"] != g.EndpointTag() {
				t.Fatal("gateway route or resolution incorrect")
			}
			for _, u := range route["auth_user"].([]any) {
				if !auths[u.(string)] {
					t.Fatal("route has unknown user")
				}
			}
		}
		if strategy != "auto" && doc["outbounds"].([]any)[0].(map[string]any)["domain_resolver"].(map[string]any)["strategy"] != strategy {
			t.Fatal("direct strategy changed")
		}
	}
	cfg.EgressGateways[0].Enabled = false
	data, err := renderer.Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "egress-wg-aws") || strings.Contains(string(data), "egress-aws-") {
		t.Fatal("disabled gateway rendered")
	}
	if len(cfg.Inbounds[0].EgressCredentials) != 3 {
		t.Fatal("disabled credential removed")
	}
	cfg.Inbounds[0].Enabled = false
	data, err = renderer.Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "egress-jp-vless") || strings.Contains(string(data), "in-vless") {
		t.Fatal("disabled inbound rendered")
	}
}
func TestNoGatewayRenderUnchanged(t *testing.T) {
	cfg := multiEgressConfig(t)
	cfg.EgressGateways = nil
	for i := range cfg.Inbounds {
		cfg.Inbounds[i].EgressCredentials = nil
	}
	renderer := Renderer{Registry: protocol.DefaultRegistry()}
	// A fully disabled optional layer must render exactly the original document.
	optional := multiEgressConfig(t)
	optional.Inbounds = append([]model.Inbound(nil), cfg.Inbounds...)
	for i := range optional.EgressGateways {
		optional.EgressGateways[i].Enabled = false
	}
	if err := protocol.SyncEgressCredentials(&optional); err != nil {
		t.Fatal(err)
	}
	for _, strategy := range []string{"auto", "prefer_ipv4", "prefer_ipv6", "ipv4_only", "ipv6_only"} {
		cfg.OutboundStrategy, optional.OutboundStrategy = strategy, strategy
		before, err := renderer.Render(cfg)
		if err != nil {
			t.Fatal(err)
		}
		after, err := renderer.Render(optional)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Fatalf("disabled gateways changed direct configuration for %s", strategy)
		}
	}
}
func testCertificate(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "node.example.com"}, DNSNames: []string{"node.example.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: private}), 0600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}
func TestSingBoxEgressIntegration(t *testing.T) {
	binary := os.Getenv("SBM_TEST_SING_BOX")
	if binary == "" {
		t.Skip("set SBM_TEST_SING_BOX to the official sing-box 1.13.14 binary")
	}
	version, err := exec.Command(binary, "version").Output()
	if err != nil || !strings.HasPrefix(string(version), "sing-box version 1.13.14\n") {
		t.Fatal("integration needs sing-box 1.13.14")
	}
	cert, key := testCertificate(t)
	renderer := Renderer{Registry: protocol.DefaultRegistry(), BuildContext: protocol.BuildContext{CertificatePath: cert, KeyPath: key}}
	for _, count := range []int{0, 1, 2, 3} {
		for _, strategy := range []string{"auto", "prefer_ipv4", "prefer_ipv6", "ipv4_only", "ipv6_only"} {
			cfg := multiEgressConfig(t)
			cfg.EgressGateways = cfg.EgressGateways[:count]
			cfg.OutboundStrategy = strategy
			if err := protocol.SyncEgressCredentials(&cfg); err != nil {
				t.Fatal(err)
			}
			data, err := renderer.Render(cfg)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := exec.Command(binary, "check", "-c", path).Run(); err != nil {
				t.Fatalf("sing-box check failed: gateways=%d strategy=%s", count, strategy)
			}
		}
	}
}
