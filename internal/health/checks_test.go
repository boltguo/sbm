package health

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCertificateThresholds(t *testing.T) {
	now := time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name       string
		notAfter   time.Time
		wantStatus Status
		wantReason string
	}{
		{name: "valid", notAfter: now.Add(31 * 24 * time.Hour), wantStatus: StatusOK, wantReason: "certificate_valid"},
		{name: "thirty days", notAfter: now.Add(30 * 24 * time.Hour), wantStatus: StatusWarning, wantReason: "certificate_expiring"},
		{name: "seven days", notAfter: now.Add(7 * 24 * time.Hour), wantStatus: StatusError, wantReason: "certificate_critical"},
		{name: "expired", notAfter: now.Add(-time.Second), wantStatus: StatusError, wantReason: "certificate_expired"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := writeCertificate(t, now.Add(-time.Hour), test.notAfter)
			check := Certificate(path, now)
			if check.Status != test.wantStatus || check.Reason != test.wantReason || check.ExpiresAt == nil || !check.ExpiresAt.Equal(test.notAfter) {
				t.Fatalf("check=%#v", check)
			}
		})
	}
}

func TestCertificateRejectsMalformedPEM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cert.pem")
	if err := os.WriteFile(path, []byte("not a certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	if check := Certificate(path, time.Now()); check.Status != StatusError || check.Reason != "certificate_invalid" {
		t.Fatalf("check=%#v", check)
	}
}

func TestListenersDistinguishTCPAndUDP(t *testing.T) {
	root := t.TempDir()
	netDir := filepath.Join(root, "net")
	if err := os.MkdirAll(netDir, 0700); err != nil {
		t.Fatal(err)
	}
	header := "  sl  local_address rem_address   st\n"
	tcp := header + "   0: 00000000:01BB 00000000:0000 0A\n"
	udp := header + "   1: 00000000:20FB 00000000:0000 07\n"
	for name, data := range map[string]string{"tcp": tcp, "tcp6": header, "udp": udp, "udp6": header} {
		if err := os.WriteFile(filepath.Join(netDir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	checks := Listeners(root, []Endpoint{
		{ID: "tcp-443", Kind: "listener_inbound", Protocol: "tcp", Port: 443},
		{ID: "udp-443", Kind: "listener_inbound", Protocol: "udp", Port: 443},
		{ID: "udp-8443", Kind: "listener_inbound", Protocol: "udp", Port: 8443},
	}, time.Now())
	if checks[0].Status != StatusOK || checks[1].Status != StatusError || checks[2].Status != StatusOK {
		t.Fatalf("checks=%#v", checks)
	}
}

func TestDiskThresholds(t *testing.T) {
	now := time.Now()
	for _, test := range []struct {
		percent float64
		status  Status
	}{
		{percent: 84.9, status: StatusOK},
		{percent: 85, status: StatusWarning},
		{percent: 94.9, status: StatusWarning},
		{percent: 95, status: StatusError},
	} {
		if got := Disk(test.percent, 100, now).Status; got != test.status {
			t.Fatalf("Disk(%v)=%s, want %s", test.percent, got, test.status)
		}
	}
}

func writeCertificate(t *testing.T, notBefore, notAfter time.Time) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: notBefore, NotAfter: notAfter}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cert.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
