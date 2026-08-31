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
