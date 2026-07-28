package store

import (
	"path/filepath"
	"testing"

	"github.com/boltguo/sbm/internal/model"
)

func TestConfigStoreClonesWireGuardExit(t *testing.T) {
	cfg := model.DefaultConfig()
	cfg.WireGuardExit = &model.WireGuardExitConfig{Enabled: true, Server: "203.0.113.10"}
	store := NewConfigStore(filepath.Join(t.TempDir(), "config.json"), cfg)

	copyCfg := store.Get()
	copyCfg.WireGuardExit.Server = "198.51.100.20"

	if got := store.Get().WireGuardExit.Server; got != "203.0.113.10" {
		t.Fatalf("stored WireGuard exit was mutated through a copy: %q", got)
	}
}
