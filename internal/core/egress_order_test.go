package core

import (
	"bytes"
	"github.com/boltguo/sbm/internal/protocol"
	"testing"
)

func TestDisplayReorderingLeavesCoreUnchanged(t *testing.T) {
	cfg := multiEgressConfig(t)
	for i := range cfg.EgressGateways {
		cfg.EgressGateways[i].Position = i
	}
	renderer := Renderer{Registry: protocol.DefaultRegistry()}
	before, err := renderer.Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.EgressGateways[0].Position = 4
	after, err := renderer.Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("changing display order changes rendered core config")
	}
}
