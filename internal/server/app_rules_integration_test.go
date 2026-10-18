package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/boltguo/sbm/internal/core"
	"github.com/boltguo/sbm/internal/model"
	"github.com/boltguo/sbm/internal/protocol"
	"github.com/boltguo/sbm/internal/traffic"
)

// Every built-in app rule must render into a config the official core
// accepts, whether it is pinned to Entry Direct, a gateway, or a paused one.
func TestSingBoxBuiltInAppRoutesIntegration(t *testing.T) {
	binary := os.Getenv("SBM_TEST_SING_BOX")
	if binary == "" {
		t.Skip("set SBM_TEST_SING_BOX to the official sing-box 1.13.14 binary")
	}
	if version, err := exec.Command(binary, "version").Output(); err != nil || !strings.HasPrefix(string(version), "sing-box version 1.13.14\n") {
		t.Fatal("integration needs sing-box 1.13.14")
	}
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "node.example.com"}, DNSNames: []string{"node.example.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
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
	s, cfg := testServer(t)
	for i, ip := range []string{"203.0.113.1", "203.0.113.2"} {
		g := gatewayInput(t, ip)
		g.ID, g.TunnelSlot = []string{"aaaa0000aaaa0000", "bbbb0000bbbb0000"}[i], i+1
		cfg.EgressGateways = append(cfg.EgressGateways, g)
	}
	if err := protocol.SyncEgressCredentials(&cfg); err != nil {
		t.Fatal(err)
	}
	specs := traffic.AppRuleSpecs()
	if len(specs) < 50 {
		t.Fatalf("built-in app rules not loaded: %d", len(specs))
	}
	cfg.AppRoutes = map[string]string{}
	for i, spec := range specs {
		cfg.AppRoutes[spec.ID] = []string{model.AppRouteDirect, cfg.EgressGateways[0].ID, cfg.EgressGateways[1].ID}[i%3]
	}
	renderer := core.Renderer{Registry: s.Registry, BuildContext: protocol.BuildContext{CertificatePath: certPath, KeyPath: keyPath}}
	for _, paused := range []map[string]bool{nil, {cfg.EgressGateways[1].ID: true}} {
		cfg.PausedGateways = paused
		data, err := renderer.Render(cfg)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		output, err := exec.Command(binary, "check", "-c", path).CombinedOutput()
		if err != nil || strings.Contains(strings.ToLower(string(output)), "deprecated") {
			t.Fatalf("sing-box check failed for %d app routes (paused=%v): %v\n%s", len(specs), paused, err, output)
		}
	}
}
