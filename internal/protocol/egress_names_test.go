package protocol

import (
	"strings"
	"testing"

	"github.com/boltguo/sbm/internal/model"
)

func TestEgressNodeNamesUniqueAndStableAcrossDisplayOrder(t *testing.T) {
	cfg := model.DefaultConfig()
	cfg.EgressGateways = []model.EgressGateway{testGateway("aws1", 1), testGateway("aws2", 2)}
	cfg.Inbounds = []model.Inbound{
		{ID: "hy443", Type: TypeHysteria2, Name: "Direct-HY2", Enabled: true, Port: 443, Hysteria2: &model.Hysteria2Options{Password: "direct-one"}},
		{ID: "hy8443", Type: TypeHysteria2, Name: "Direct-HY2-8443", Enabled: true, Port: 8443, Hysteria2: &model.Hysteria2Options{Password: "direct-two"}},
	}
	if err := SyncEgressCredentials(&cfg); err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	seen := map[string]bool{}
	for _, node := range EgressNodes(cfg) {
		if seen[node.Inbound.Name] || !strings.HasSuffix(node.Inbound.Name, "-AWS") {
			t.Fatalf("collision or displaced marker: %s", node.Inbound.Name)
		}
		seen[node.Inbound.Name] = true
		names[node.Gateway.ID+"/"+node.Inbound.ID] = node.Inbound.Name
	}
	if len(names) != 4 {
		t.Fatal("missing variants")
	}
	// A user can give a Direct node the exact generated conflict name.
	cfg.Inbounds[0].Name = names["aws1/hy443"]
	seen = map[string]bool{cfg.Inbounds[0].Name: true, cfg.Inbounds[1].Name: true}
	names = map[string]string{}
	for _, node := range EgressNodes(cfg) {
		if seen[node.Inbound.Name] || !strings.HasSuffix(node.Inbound.Name, "-AWS") {
			t.Fatalf("generated name conflicts with Direct: %s", node.Inbound.Name)
		}
		seen[node.Inbound.Name] = true
		names[node.Gateway.ID+"/"+node.Inbound.ID] = node.Inbound.Name
	}
	cfg.EgressGateways[0].Position = 10
	for _, node := range EgressNodes(cfg) {
		if names[node.Gateway.ID+"/"+node.Inbound.ID] != node.Inbound.Name {
			t.Fatal("reorder renamed a node")
		}
	}
}
