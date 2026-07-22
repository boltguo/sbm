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

type Manager struct {
	Binary, ConfigPath, Service string
	Renderer                    Renderer
	Commands                    Commander
	mu                          sync.Mutex
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
		if active, _ := m.Active(ctx); active {
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
func (m *Manager) Active(ctx context.Context) (bool, error) {
	_, err := m.command(ctx, "systemctl", "is-active", "--quiet", m.Service)
	return err == nil, nil
}
func (m *Manager) Version(ctx context.Context) string {
	out, err := m.command(ctx, m.Binary, "version")
	if err != nil {
		return "unknown"
	}
	line := strings.Split(strings.TrimSpace(string(out)), "\n")[0]
	if len(line) > 80 {
		line = line[:80]
	}
	return line
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
