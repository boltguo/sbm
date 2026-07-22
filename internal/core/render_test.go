package core

import (
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
