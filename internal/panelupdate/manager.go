// Package panelupdate starts the fixed SBM updater outside the panel's service
// so restarting the panel cannot kill its own update or rollback.
package panelupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/boltguo/sbm/internal/store"
	"golang.org/x/sys/unix"
)

const Unit = "sbm-panel-update.service"

var ErrBusy = errors.New("panel update is already running")
var stableTag = regexp.MustCompile(`^v2\.[1-9][0-9]*\.[0-9]+$`)

func Supports(target string) bool { return stableTag.MatchString(target) }

type Status struct {
	State         string    `json:"state"`
	Phase         string    `json:"phase"`
	TargetVersion string    `json:"targetVersion"`
	RolledBack    bool      `json:"rolledBack"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type Updater interface {
	Start(context.Context, string) error
	Status(context.Context) (Status, error)
}

type Commander interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type Manager struct {
	Commands                     Commander
	StatusPath, LockPath, Script string
}

func New(commands Commander) *Manager {
	return &Manager{Commands: commands, StatusPath: "/var/lib/sbm/panel-update.json", LockPath: "/var/lib/sbm/panel-update.lock", Script: "/usr/local/bin/sbm"}
}

func (m *Manager) lock() (*os.File, error) {
	f, err := os.OpenFile(m.LockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, err
	}
	return f, nil
}

func unlock(f *os.File) {
	_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
	_ = f.Close()
}

func (m *Manager) Start(ctx context.Context, target string) error {
	// No request text becomes a shell command. Only stable supported release
	// tags fetched by the server can be passed to this fixed manager entry point.
	if !Supports(target) {
		return errors.New("unsupported release tag")
	}
	lock, err := m.lock()
	if err != nil {
		return err
	}
	defer unlock(lock)
	if _, err := m.Commands.Run(ctx, "systemctl", "is-active", "--quiet", Unit); err == nil {
		return ErrBusy
	}
	status := Status{State: "running", Phase: "queued", TargetVersion: target, UpdatedAt: time.Now().UTC()}
	file := store.NewJSONFile[Status](m.StatusPath)
	if err := file.SaveWithoutBackup(status); err != nil {
		return err
	}
	// Type=exec verifies that Bash actually starts. The service manager owns
	// the detached job, not this HTTP process. The worker waits for our lock.
	_, err = m.Commands.Run(ctx, "systemd-run", "--quiet", "--collect", "--no-ask-password",
		"--unit="+Unit, "--property=Type=exec", "--property=UMask=0077",
		"/bin/bash", m.Script, "--update-panel", target)
	if err != nil {
		// A timed-out launcher may already have started the unit. Preserve its
		// running status instead of claiming failure while it installs files.
		probe, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, activeErr := m.Commands.Run(probe, "systemctl", "is-active", "--quiet", Unit); activeErr == nil {
			return nil
		}
		status.State, status.Phase, status.UpdatedAt = "failed", "launch", time.Now().UTC()
		_ = file.SaveWithoutBackup(status)
		return fmt.Errorf("start update service: %w", err)
	}
	return nil
}

func (m *Manager) Status(ctx context.Context) (Status, error) {
	data, err := os.ReadFile(m.StatusPath)
	if errors.Is(err, os.ErrNotExist) {
		return Status{State: "idle"}, nil
	}
	if err != nil {
		return Status{}, err
	}
	var status Status
	if err := json.Unmarshal(data, &status); err != nil {
		return Status{}, err
	}
	if status.State == "running" {
		lock, err := m.lock()
		if errors.Is(err, ErrBusy) {
			return status, nil
		}
		if err != nil {
			return Status{}, err
		}
		defer unlock(lock)
		// The worker can commit its final result between our first read and
		// acquiring the lock. Re-read under the lock before declaring it gone.
		data, err = os.ReadFile(m.StatusPath)
		if err != nil {
			return Status{}, err
		}
		if err := json.Unmarshal(data, &status); err != nil {
			return Status{}, err
		}
		if status.State != "running" {
			return status, nil
		}
		if _, err := m.Commands.Run(ctx, "systemctl", "is-active", "--quiet", Unit); err != nil {
			// Reboot, SIGKILL or a failed service must not leave a permanent
			// spinner. Keep the record intact for diagnosis and allow retry.
			status.State, status.Phase = "failed", "interrupted"
		}
	}
	return status, nil
}
