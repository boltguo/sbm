package server

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/boltguo/sbm/internal/core"
	"github.com/boltguo/sbm/internal/model"
	"github.com/boltguo/sbm/internal/protocol"
	"github.com/boltguo/sbm/internal/store"
	"github.com/boltguo/sbm/internal/traffic"
)

// This test launches genuine sing-box clients, A, and two WireGuard peers.
// It is opt-in and runs only inside a new network namespace; B peers use
// sing-box's userspace WireGuard so no host interfaces or kernel modules are
// required. Production B setup with wg-quick is documented separately.
func TestEgressRuntimeIntegration(t *testing.T) {
	if os.Getenv("SBM_TEST_RUNTIME") != "1" {
		t.Skip("isolated Linux runtime integration")
	}
	binary := os.Getenv("SBM_TEST_SING_BOX")
	if binary == "" || os.Geteuid() != 0 {
		t.Fatal("official sing-box and isolated root required")
	}
	for _, args := range [][]string{{"link", "set", "lo", "up"}, {"addr", "add", "203.0.113.1/32", "dev", "lo"}, {"addr", "add", "203.0.113.2/32", "dev", "lo"}, {"addr", "add", "198.18.0.100/32", "dev", "lo"}} {
		if err := exec.Command("ip", args...).Run(); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	if logDir := os.Getenv("SBM_TEST_LOG_DIR"); logDir != "" {
		t.Cleanup(func() {
			if !t.Failed() {
				return
			}
			_ = os.MkdirAll(logDir, 0700)
			paths, _ := filepath.Glob(filepath.Join(dir, "*.log"))
			for _, path := range paths {
				if data, err := os.ReadFile(path); err == nil {
					_ = os.WriteFile(filepath.Join(logDir, filepath.Base(path)), data, 0600)
				}
			}
		})
	}
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
	tlsCert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	tlsListener, err := tls.Listen("tcp4", "127.0.0.1:2443", &tls.Config{Certificates: []tls.Certificate{tlsCert}, MinVersion: tls.VersionTLS13, CurvePreferences: []tls.CurveID{tls.X25519}})
	if err != nil {
		t.Fatal(err)
	}
	tlsServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "handshake target") })}
	go func() { _ = tlsServer.Serve(tlsListener) }()
	t.Cleanup(func() { _ = tlsServer.Close() })
	target := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		fmt.Fprint(w, host)
	})}
	listener, err := net.Listen("tcp4", "198.18.0.100:28080")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = target.Serve(listener) }()
	t.Cleanup(func() { _ = target.Close() })
	cfg := model.DefaultConfig()
	cfg.Domain = "node.example.com"
	cfg.SessionSecret = strings.Repeat("s", 43)
	cfg.SubscriptionToken = cfg.SessionSecret
	cfg.ClashAPISecret = cfg.SessionSecret
	reality, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Inbounds = []model.Inbound{
		{ID: "vless", Type: protocol.TypeVLESSReality, Name: "Entry-VLESS", Enabled: true, Port: 28443, VLESS: &model.VLESSOptions{UUID: "70d0c699-73a0-4d2a-a45d-4f46a661b4f2", SNI: cfg.Domain, PrivateKey: base64.RawURLEncoding.EncodeToString(reality.Bytes()), PublicKey: base64.RawURLEncoding.EncodeToString(reality.PublicKey().Bytes()), ShortID: "aabb"}},
		{ID: "hy2", Type: protocol.TypeHysteria2, Name: "Entry-HY2", Enabled: true, Port: 28443, Hysteria2: &model.Hysteria2Options{Password: "direct-password", Obfs: "salamander", ObfsPassword: "obfs-password"}},
	}
	start := func(name string, doc any) {
		data, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, name+".json")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if err := exec.Command(binary, "check", "-c", path).Run(); err != nil {
			t.Fatalf("%s config invalid", name)
		}
		ctx, cancel := context.WithCancel(context.Background())
		command := exec.CommandContext(ctx, binary, "run", "-c", path)
		logFile, err := os.OpenFile(filepath.Join(dir, name+".log"), os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		command.Stdout = logFile
		command.Stderr = logFile
		if err := command.Start(); err != nil {
			cancel()
			t.Fatal(err)
		}
		t.Cleanup(func() { cancel(); _ = command.Wait(); _ = logFile.Close() })
	}
	for i, id := range []string{"aws", "jp"} {
		aKeys, err := protocol.GenerateWireGuardKeys()
		if err != nil {
			t.Fatal(err)
		}
		bKeys, err := protocol.GenerateWireGuardKeys()
		if err != nil {
			t.Fatal(err)
		}
		ip := "203.0.113." + strconv.Itoa(i+1)
		g := model.EgressGateway{ID: id, Enabled: true, TunnelSlot: i + 1, Server: ip, ServerPort: 28100 + i, PrivateKey: aKeys.Private, PeerPublicKey: bKeys.Public, TrafficQuota: cfg.TrafficQuota, Reset: cfg.Reset}
		cfg.EgressGateways = append(cfg.EgressGateways, g)
		start("peer-"+id, map[string]any{
			"log":       map[string]any{"level": "warn"},
			"endpoints": []any{map[string]any{"type": "wireguard", "tag": "wg-b", "system": false, "mtu": 1408, "listen_port": g.ServerPort, "address": []string{g.PeerAddress()}, "private_key": bKeys.Private, "peers": []any{map[string]any{"public_key": aKeys.Public, "allowed_ips": []string{g.TunnelAddress()}}}}},
			"outbounds": []any{map[string]any{"type": "direct", "tag": "direct", "inet4_bind_address": ip}}, "route": map[string]any{"final": "direct"},
		})
	}
	if err := protocol.SyncEgressCredentials(&cfg); err != nil {
		t.Fatal(err)
	}
	config := store.NewConfigStore(filepath.Join(dir, "business.json"), cfg)
	tracker := traffic.NewForTest(model.DefaultState(time.Now()), config, nil, time.Now)
	tracker.Gateways = &traffic.Accounting{}
	if err := tracker.ReconcileGateways(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, err := (core.Renderer{Registry: protocol.DefaultRegistry(), BuildContext: protocol.BuildContext{CertificatePath: certPath, KeyPath: keyPath}}).Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var aDoc map[string]any
	if err := json.Unmarshal(data, &aDoc); err != nil {
		t.Fatal(err)
	}
	// The isolation has no Internet/DNS. A local TLS 1.3 server stands in for the
	// configured Reality handshake destination; all client credentials and A's
	// generated routes/endpoints stay exactly as rendered.
	aDoc["inbounds"].([]any)[0].(map[string]any)["tls"].(map[string]any)["reality"].(map[string]any)["handshake"] = map[string]any{"server": "127.0.0.1", "server_port": 2443}
	aDoc["outbounds"].([]any)[0].(map[string]any)["inet4_bind_address"] = "127.0.0.1"
	start("entry", aDoc)
	inbounds, outbounds, rules := []any{}, []any{}, []any{}
	variants := append([]model.Inbound(nil), cfg.Inbounds...)
	for _, g := range cfg.EgressGateways {
		for _, in := range cfg.Inbounds {
			v, _ := protocol.EgressVariant(in, g)
			variants = append(variants, v)
		}
	}
	for i, in := range variants {
		tag := strconv.Itoa(i)
		inbounds = append(inbounds, map[string]any{"type": "socks", "tag": "socks-" + tag, "listen": "127.0.0.1", "listen_port": 29000 + i})
		var outbound map[string]any
		if in.VLESS != nil {
			outbound = map[string]any{"type": "vless", "tag": tag, "server": "127.0.0.1", "server_port": in.Port, "uuid": in.VLESS.UUID, "flow": "xtls-rprx-vision", "tls": map[string]any{"enabled": true, "server_name": in.VLESS.SNI, "utls": map[string]any{"enabled": true, "fingerprint": "chrome"}, "reality": map[string]any{"enabled": true, "public_key": in.VLESS.PublicKey, "short_id": in.VLESS.ShortID}}}
		} else {
			outbound = map[string]any{"type": "hysteria2", "tag": tag, "server": "127.0.0.1", "server_port": in.Port, "password": in.Hysteria2.Password, "obfs": map[string]any{"type": in.Hysteria2.Obfs, "password": in.Hysteria2.ObfsPassword}, "tls": map[string]any{"enabled": true, "server_name": cfg.Domain, "insecure": true}}
		}
		outbounds = append(outbounds, outbound)
		rules = append(rules, map[string]any{"inbound": []string{"socks-" + tag}, "action": "route", "outbound": tag})
	}
	start("clients", map[string]any{"log": map[string]any{"level": "warn"}, "inbounds": inbounds, "outbounds": outbounds, "route": map[string]any{"rules": rules, "final": "0"}})
	for i, in := range variants {
		socks := fmt.Sprintf("127.0.0.1:%d", 29000+i)
		transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return runtimeSOCKSDial(ctx, socks, address)
		}}
		client := &http.Client{Transport: transport, Timeout: 4 * time.Second}
		want := "127.0.0.1"
		if i >= 2 {
			want = "203.0.113." + strconv.Itoa((i-2)/2+1)
		}
		deadline := time.Now().Add(12 * time.Second)
		passed := false
		for time.Now().Before(deadline) {
			resp, err := client.Get("http://198.18.0.100:28080/")
			if err == nil {
				body, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if strings.TrimSpace(string(body)) == want {
					passed = true
					break
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		transport.CloseIdleConnections()
		if !passed {
			t.Fatalf("%s did not leave through expected IP %s", in.Type, want)
		}
		t.Logf("%s via SOCKS port %d -> observed exit %s", in.Type, 29000+i, want)
	}
	if err := tracker.SampleGateways(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, g := range cfg.EgressGateways {
		state := tracker.State().Egress[g.ID]
		if state.TX == 0 || state.RX == 0 {
			t.Fatalf("gateway %s has no actual WireGuard TX/RX", g.ID)
		}
		t.Logf("%s tunnel bytes: tx=%d rx=%d", g.ID, state.TX, state.RX)
	}
}
func runtimeSOCKSDial(ctx context.Context, socks, address string) (net.Conn, error) {
	c, err := (&net.Dialer{}).DialContext(ctx, "tcp", socks)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = c.Close()
		}
	}()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Write([]byte{5, 1, 0}); err != nil {
		return nil, err
	}
	var reply [2]byte
	if _, err := io.ReadFull(c, reply[:]); err != nil || reply != [2]byte{5, 0} {
		return nil, fmt.Errorf("SOCKS authentication failed")
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return nil, err
	}
	ip := net.ParseIP(host).To4()
	if ip == nil {
		return nil, fmt.Errorf("test target needs IPv4")
	}
	request := append([]byte{5, 1, 0, 1}, ip...)
	request = append(request, byte(port>>8), byte(port))
	if _, err := c.Write(request); err != nil {
		return nil, err
	}
	var response [4]byte
	if _, err := io.ReadFull(c, response[:]); err != nil || response[1] != 0 {
		return nil, fmt.Errorf("SOCKS connect failed")
	}
	n := 4
	if response[3] == 4 {
		n = 16
	} else if response[3] == 3 {
		var size [1]byte
		if _, err := io.ReadFull(c, size[:]); err != nil {
			return nil, err
		}
		n = int(size[0])
	}
	if _, err := io.CopyN(io.Discard, c, int64(n+2)); err != nil {
		return nil, err
	}
	_ = c.SetDeadline(time.Time{})
	ok = true
	return c, nil
}
