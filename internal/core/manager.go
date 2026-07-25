package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/boltguo/sbm/internal/model"
)

type Commander interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type ExecCommander struct{}

func (ExecCommander) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// versionTTL bounds how long a cached core version can be stale. The binary
// only changes on an update, which also restarts the service, so a short TTL is
// enough to pick that up without executing sing-box on every dashboard poll.
const versionTTL = 5 * time.Minute

type Manager struct {
	Binary, ConfigPath, Service string
	Renderer                    Renderer
	Commands                    Commander
	mu                          sync.Mutex
	versionMu                   sync.Mutex
	version                     string
	versionAt                   time.Time
}

func (m *Manager) command(ctx context.Context, name string, args ...string) ([]byte, error) {
	if m.Commands == nil {
		m.Commands = ExecCommander{}
	}
	return m.Commands.Run(ctx, name, args...)
}

func (m *Manager) Apply(ctx context.Context, cfg model.Config, quotaExceeded bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.invalidateVersion()
	data, err := m.Renderer.Render(cfg)
	if err != nil {
		return err
	}
	dir := filepath.Dir(m.ConfigPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".sing-box.*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	_, err = m.command(checkCtx, m.Binary, "check", "-c", tmpPath)
	cancel()
	if err != nil {
		return errors.New("sing-box 配置校验失败")
	}
	backup := m.ConfigPath + ".bak"
	hadOld := false
	if old, readErr := os.ReadFile(m.ConfigPath); readErr == nil {
		hadOld = true
		if err := writePrivate(backup, old); err != nil {
			return fmt.Errorf("备份旧配置: %w", err)
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	if err := os.Rename(tmpPath, m.ConfigPath); err != nil {
		return err
	}
	if quotaExceeded {
		return nil
	}
	if err := m.restartAndCheck(ctx); err == nil {
		return nil
	}
	if hadOld {
		old, readErr := os.ReadFile(backup)
		if readErr == nil {
			_ = writePrivate(m.ConfigPath, old)
			_ = m.Restart(ctx)
		}
	}
	return errors.New("sing-box 启动失败，已恢复上一份配置")
}

func (m *Manager) restartAndCheck(ctx context.Context) error {
	if err := m.Restart(ctx); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		// Only a fully active unit counts as a successful start; a core still
		// activating may yet fail, and this decides whether to roll back.
		if state, err := m.serviceState(ctx); err == nil && state == "active" {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return errors.New("service did not become active")
}

func (m *Manager) Restart(ctx context.Context) error {
	out, err := m.command(ctx, "systemctl", "restart", m.Service)
	if err != nil {
		return fmt.Errorf("重启 sing-box 失败: %s", safeOutput(out))
	}
	return nil
}
func (m *Manager) Start(ctx context.Context) error {
	out, err := m.command(ctx, "systemctl", "start", m.Service)
	if err != nil {
		return fmt.Errorf("启动 sing-box 失败: %s", safeOutput(out))
	}
	return nil
}
func (m *Manager) Stop(ctx context.Context) error {
	out, err := m.command(ctx, "systemctl", "stop", m.Service)
	if err != nil {
		return fmt.Errorf("停止 sing-box 失败: %s", safeOutput(out))
	}
	return nil
}

// serviceState reports the systemd state, or an error when systemd could not
// answer at all. systemctl exits non-zero for every state except "active", so
// the printed state decides and the exit status only matters when there is none.
func (m *Manager) serviceState(ctx context.Context) (string, error) {
	out, err := m.command(ctx, "systemctl", "is-active", m.Service)
	switch state := strings.TrimSpace(string(out)); state {
	case "active", "activating", "reloading", "inactive", "deactivating", "failed":
		return state, nil
	}
	if err != nil {
		return "", fmt.Errorf("查询 sing-box 状态失败: %s", safeOutput(out))
	}
	return "", fmt.Errorf("无法识别的 sing-box 状态: %s", safeOutput(out))
}

// Active reports whether the service is running. A state systemd did not report
// is an error, never "inactive": quota enforcement only stops the core when it
// believes the core is up, so a misread would silently skip the stop and let the
// core keep serving traffic past the limit.
func (m *Manager) Active(ctx context.Context) (bool, error) {
	state, err := m.serviceState(ctx)
	if err != nil {
		return false, err
	}
	return state == "active" || state == "activating" || state == "reloading", nil
}

// Version reports the core version, executing the binary at most once per TTL.
// The dashboard polls every few seconds per open tab, so caching here removes a
// process spawn from the hot path.
func (m *Manager) Version(ctx context.Context) string {
	m.versionMu.Lock()
	defer m.versionMu.Unlock()
	if m.version != "" && time.Since(m.versionAt) < versionTTL {
		return m.version
	}
	out, err := m.command(ctx, m.Binary, "version")
	if err != nil {
		return "unknown"
	}
	line := strings.Split(strings.TrimSpace(string(out)), "\n")[0]
	if len(line) > 80 {
		line = line[:80]
	}
	m.version, m.versionAt = line, time.Now()
	return line
}

// invalidateVersion is called whenever the core binary may have been replaced.
func (m *Manager) invalidateVersion() {
	m.versionMu.Lock()
	m.version, m.versionAt = "", time.Time{}
	m.versionMu.Unlock()
}

func safeOutput(out []byte) string {
	value := strings.TrimSpace(string(out))
	if value == "" {
		return "命令未返回详情"
	}
	if len(value) > 500 {
		value = value[:500]
	}
	return value
}

func writePrivate(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".private.*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
