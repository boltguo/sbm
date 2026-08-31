package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/boltguo/sbm/internal/model"
	"github.com/boltguo/sbm/internal/protocol"
)

type recordingCommander struct {
	mu     sync.Mutex
	calls  []string
	output []byte
}

func (c *recordingCommander) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	c.mu.Lock()
	c.calls = append(c.calls, name+" "+strings.Join(args, " "))
	c.mu.Unlock()
	return c.output, nil
}

func (c *recordingCommander) count(subcommand string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	total := 0
	for _, call := range c.calls {
		if strings.HasSuffix(call, " "+subcommand) {
			total++
		}
	}
	return total
}

// The dashboard polls every few seconds per open tab; executing the core binary
// on each of those calls is pure waste.
func TestVersionIsCachedAndInvalidatedByApply(t *testing.T) {
	commands := &recordingCommander{output: []byte("sing-box version 1.12.0\nsomething else")}
	manager := &Manager{Binary: "/usr/local/bin/sing-box", ConfigPath: filepath.Join(t.TempDir(), "config.json"), Service: "sing-box.service", Commands: commands, Renderer: Renderer{Registry: protocol.DefaultRegistry()}}

	for range 10 {
		if got := manager.Version(context.Background()); got != "sing-box version 1.12.0" {
			t.Fatalf("version=%q", got)
		}
	}
	if got := commands.count("version"); got != 1 {
		t.Fatalf("core binary executed %d times, want 1", got)
	}

	// A config apply may follow a core update, so the cache must not survive it.
	if err := manager.Apply(context.Background(), validConfig(), true); err != nil {
		t.Fatal(err)
	}
	manager.Version(context.Background())
	if got := commands.count("version"); got != 2 {
		t.Fatalf("version was not re-read after Apply: %d", got)
	}
}

func validConfig() model.Config {
	secret := strings.Repeat("x", 43)
	cfg := model.DefaultConfig()
	cfg.Domain = "node.example.com"
	cfg.AdminPasswordHash = "hash"
	cfg.SessionSecret, cfg.ClashAPISecret, cfg.SubscriptionToken = secret, secret, secret
	return cfg
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

type fixedCommander struct {
	output []byte
	err    error
}

type blockingCommander struct{}

func (blockingCommander) Run(ctx context.Context, _ string, _ ...string) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestCheckConfigReportsTimeoutWithoutCommandOutput(t *testing.T) {
	manager := &Manager{Binary: "/fake/sing-box", ConfigPath: "/secret/config.json", Commands: blockingCommander{}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := manager.CheckConfig(ctx); !errors.Is(err, ErrConfigCheckTimeout) {
		t.Fatalf("error=%v", err)
	}
}

func TestGenerationReadsSystemdInvocationSymlink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invocation:sing-box.service")
	const id = "0123456789abcdef0123456789ABCDEF"
	if err := os.Symlink(id, path); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{Service: "sing-box.service", InvocationPath: path}
	if got, err := manager.Generation(); err != nil || got != strings.ToLower(id) {
		t.Fatalf("generation=%q error=%v", got, err)
	}
}

func (c fixedCommander) Run(context.Context, string, ...string) ([]byte, error) {
	return c.output, c.err
}

// Quota enforcement only stops the core when it believes the core is up, so a
// state systemd could not report must surface as an error rather than as
// "inactive" — otherwise the stop is silently skipped.
func TestActiveSeparatesUnreadableStateFromInactive(t *testing.T) {
	exit3 := errors.New("exit status 3")
	for _, tc := range []struct {
		name       string
		output     string
		err        error
		active     bool
		wantErrMsg string
	}{
		{name: "active", output: "active\n", active: true},
		{name: "activating", output: "activating\n", err: exit3, active: true},
		{name: "inactive", output: "inactive\n", err: exit3},
		{name: "failed", output: "failed\n", err: exit3},
		{name: "bus error", err: errors.New("Failed to connect to bus"), wantErrMsg: "查询"},
		{name: "unknown unit", output: "unknown\n", err: errors.New("exit status 4"), wantErrMsg: "查询"},
		{name: "unrecognised state", output: "something-new\n", wantErrMsg: "无法识别"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := &Manager{Service: "sing-box.service", Commands: fixedCommander{output: []byte(tc.output), err: tc.err}}
			active, err := manager.Active(context.Background())
			if tc.wantErrMsg != "" {
				if err == nil {
					t.Fatalf("active = %v, want an error", active)
				}
				if !strings.Contains(err.Error(), tc.wantErrMsg) {
					t.Fatalf("error = %v, want it to mention %q", err, tc.wantErrMsg)
				}
				if active {
					t.Fatal("an unreadable state must not report the core as running")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if active != tc.active {
				t.Fatalf("active = %v, want %v", active, tc.active)
			}
		})
	}
}
