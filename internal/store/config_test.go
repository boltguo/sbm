package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/boltguo/sbm/internal/model"
)

func TestConfigStoreClonesInboundOptions(t *testing.T) {
	cfg := model.DefaultConfig()
	cfg.Inbounds = []model.Inbound{{VLESS: &model.VLESSOptions{UUID: "original"}}}
	store := NewConfigStore(filepath.Join(t.TempDir(), "config.json"), cfg)

	copyCfg := store.Get()
	copyCfg.Inbounds[0].VLESS.UUID = "mutated"

	if got := store.Get().Inbounds[0].VLESS.UUID; got != "original" {
		t.Fatalf("stored inbound was mutated through a copy: %q", got)
	}
}

func TestOpenConfigRejectsPreviousVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"totalBytes":536870912000}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenConfig(path); err == nil || !strings.Contains(err.Error(), "unsupported config version 1") {
		t.Fatalf("old config was not rejected explicitly: %v", err)
	}
}

func TestOpenConfigRejectsRemovedWireGuardVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"version":2,"wireGuardExit":{"enabled":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenConfig(path); err == nil || !strings.Contains(err.Error(), "unsupported config version 2") {
		t.Fatalf("WireGuard-era config was not rejected explicitly: %v", err)
	}
}

func TestConfigStoreClonesEgressAndAcceptsExistingV4(t *testing.T) {
	cfg := model.DefaultConfig()
	cfg.EgressGateways = []model.EgressGateway{{ID: "one", Marker: "AWS"}}
	cfg.Inbounds = []model.Inbound{{ID: "in", EgressCredentials: []model.EgressCredential{{GatewayID: "one", Password: "original"}}}}
	path := filepath.Join(t.TempDir(), "config.json")
	st := NewConfigStore(path, cfg)
	snapshot := st.Get()
	snapshot.EgressGateways[0].Marker = "changed"
	snapshot.Inbounds[0].EgressCredentials[0].Password = "changed"
	if current := st.Get(); current.EgressGateways[0].Marker != "AWS" || current.Inbounds[0].EgressCredentials[0].Password != "original" {
		t.Fatal("egress snapshot aliases live config")
	}
	existing := model.DefaultConfig()
	if err := NewJSONFile[model.Config](path).SaveWithoutBackup(existing); err != nil {
		t.Fatal(err)
	}
	loaded, err := OpenConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Get().EgressGateways) != 0 || loaded.Get().Version != 4 {
		t.Fatal("existing v4 no longer loads")
	}
}
