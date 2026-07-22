package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/boltguo/sing-box/internal/model"
	"github.com/boltguo/sing-box/internal/protocol"
)

type recordingCommander struct {
	mu    sync.Mutex
	calls []string
}

func (c *recordingCommander) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	c.mu.Lock()
	c.calls = append(c.calls, name+" "+strings.Join(args, " "))
	c.mu.Unlock()
	return nil, nil
}

func TestApplyRunsCheckBeforeAtomicConfigInstall(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config.json")
	commands := &recordingCommander{}
	secret := strings.Repeat("x", 43)
	cfg := model.DefaultConfig()
	cfg.Domain = "node.example.com"
	cfg.AdminPasswordHash = "hash"
	cfg.SessionSecret = secret
	cfg.ClashAPISecret = secret
	cfg.SubscriptionToken = secret
	manager := &Manager{Binary: "/usr/local/bin/sing-box", ConfigPath: target, Service: "sing-box.service", Commands: commands, Renderer: Renderer{Registry: protocol.DefaultRegistry()}}
	if err := manager.Apply(context.Background(), cfg, true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"external_controller": "127.0.0.1:9090"`) {
		t.Fatal("generated config was not installed")
	}
	if len(commands.calls) != 1 || !strings.HasPrefix(commands.calls[0], "/usr/local/bin/sing-box check -c ") {
		t.Fatalf("unexpected commands: %v", commands.calls)
	}
	if info, err := os.Stat(target); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("config mode/info: %v %v", info, err)
	}
}
