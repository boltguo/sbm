package traffic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/boltguo/sing-box/internal/model"
	"github.com/boltguo/sing-box/internal/store"
)

type CoreControl interface {
	Stop(context.Context) error
	Start(context.Context) error
}
type ConfigSource interface{ Get() model.Config }

type Tracker struct {
	mu     sync.RWMutex
	state  model.State
	file   *store.JSONFile[model.State]
	config ConfigSource
	core   CoreControl
	now    func() time.Time
}

func Open(path string, config ConfigSource, core CoreControl, now func() time.Time) (*Tracker, error) {
	if now == nil {
		now = time.Now
	}
	file := store.NewJSONFile[model.State](path)
	state, err := file.Load()
	if err != nil {
		_, primaryErr := os.Stat(path)
		_, backupErr := os.Stat(path + ".bak")
		if primaryErr == nil || backupErr == nil {
			return nil, err
		}
		if !errors.Is(primaryErr, os.ErrNotExist) || !errors.Is(backupErr, os.ErrNotExist) {
			return nil, err
		}
		state = model.DefaultState(now())
		if cfg := config.Get(); cfg.Reset.Mode == "monthly" {
			state.NextResetAt, _ = NextMonthlyReset(now(), cfg.Reset)
		}
		if saveErr := file.SaveWithoutBackup(state); saveErr != nil {
			return nil, fmt.Errorf("初始化流量状态: %w", saveErr)
		}
	}
	if state.Version != model.StateVersion {
		return nil, fmt.Errorf("unsupported state version %d", state.Version)
	}
	return &Tracker{state: state, file: file, config: config, core: core, now: now}, nil
}

func (t *Tracker) UpdateSchedule() error {
	t.mu.Lock()
	now := t.now()
	t.state.NextResetAt = time.Time{}
	if cfg := t.config.Get(); cfg.Reset.Mode == "monthly" {
		var err error
		t.state.NextResetAt, err = NextMonthlyReset(now, cfg.Reset)
		if err != nil {
			t.mu.Unlock()
			return err
		}
	}
	t.state.UpdatedAt = now
	snapshot := t.state
	t.mu.Unlock()
	return t.persist(snapshot)
}

func NewForTest(state model.State, config ConfigSource, core CoreControl, now func() time.Time) *Tracker {
	if now == nil {
		now = time.Now
	}
	return &Tracker{state: state, config: config, core: core, now: now}
}

func (t *Tracker) State() model.State { t.mu.RLock(); defer t.mu.RUnlock(); return t.state }

func (t *Tracker) ApplySample(ctx context.Context, currentUpload, currentDownload int64) (bool, error) {
	if currentUpload < 0 || currentDownload < 0 {
		return false, errors.New("核心流量计数不能为负数")
	}
	t.mu.Lock()
	if currentUpload < t.state.LastCoreUpload {
		t.state.Upload += currentUpload
	} else {
		t.state.Upload += currentUpload - t.state.LastCoreUpload
	}
	if currentDownload < t.state.LastCoreDownload {
		t.state.Download += currentDownload
	} else {
		t.state.Download += currentDownload - t.state.LastCoreDownload
	}
	t.state.LastCoreUpload = currentUpload
	t.state.LastCoreDownload = currentDownload
	t.state.UpdatedAt = t.now()
	cfg := t.config.Get()
	newlyExceeded := !t.state.QuotaExceeded && cfg.TotalBytes > 0 && t.state.Total() >= cfg.TotalBytes
	if newlyExceeded {
		t.state.QuotaExceeded = true
	}
	snapshot := t.state
	t.mu.Unlock()
	if newlyExceeded {
		if err := t.persist(snapshot); err != nil {
			return true, err
		}
		if t.core != nil {
			return true, t.core.Stop(ctx)
		}
	}
	return false, nil
}

func (t *Tracker) Reset(ctx context.Context) error {
	t.mu.Lock()
	wasExceeded := t.state.QuotaExceeded
	now := t.now()
	t.state.Upload = 0
	t.state.Download = 0
	// Keep the latest core baselines so pre-reset bytes are never counted twice.
	t.state.QuotaExceeded = false
	t.state.PeriodStartedAt = now
	t.state.UpdatedAt = now
	t.state.NextResetAt = time.Time{}
	if cfg := t.config.Get(); cfg.Reset.Mode == "monthly" {
		t.state.NextResetAt, _ = NextMonthlyReset(now, cfg.Reset)
	}
	snapshot := t.state
	t.mu.Unlock()
	if err := t.persist(snapshot); err != nil {
		return err
	}
	if wasExceeded && t.core != nil {
		return t.core.Start(ctx)
	}
	return nil
}

// ReconcileQuota applies a changed quota without clearing already-accounted traffic.
func (t *Tracker) ReconcileQuota(ctx context.Context) error {
	t.mu.Lock()
	cfg := t.config.Get()
	shouldExceed := cfg.TotalBytes > 0 && t.state.Total() >= cfg.TotalBytes
	wasExceeded := t.state.QuotaExceeded
	if shouldExceed == wasExceeded {
		t.mu.Unlock()
		return nil
	}
	t.state.QuotaExceeded = shouldExceed
	t.state.UpdatedAt = t.now()
	snapshot := t.state
	t.mu.Unlock()
	if err := t.persist(snapshot); err != nil {
		return err
	}
	if t.core == nil {
		return nil
	}
	if shouldExceed {
		return t.core.Stop(ctx)
	}
	return t.core.Start(ctx)
}

func (t *Tracker) CheckScheduledReset(ctx context.Context) error {
	cfg := t.config.Get()
	if cfg.Reset.Mode != "monthly" {
		return nil
	}
	t.mu.RLock()
	next := t.state.NextResetAt
	t.mu.RUnlock()
	if next.IsZero() {
		computed, err := NextMonthlyReset(t.now(), cfg.Reset)
		if err != nil {
			return err
		}
		t.mu.Lock()
		t.state.NextResetAt = computed
		snapshot := t.state
		t.mu.Unlock()
		return t.persist(snapshot)
	}
	if !t.now().Before(next) {
		return t.Reset(ctx)
	}
	return nil
}

func (t *Tracker) Persist() error { return t.persist(t.State()) }
func (t *Tracker) persist(_ model.State) error {
	if t.file == nil {
		return nil
	}
	// Always take a fresh snapshot so a slow periodic save cannot overwrite a
	// newer reset or quota transition that completed while it was waiting.
	return t.file.Save(t.State())
}

func NextMonthlyReset(after time.Time, reset model.ResetConfig) (time.Time, error) {
	if reset.Day < 1 || reset.Day > 28 {
		return time.Time{}, errors.New("reset day must be between 1 and 28")
	}
	location, err := time.LoadLocation(reset.Timezone)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid timezone: %w", err)
	}
	local := after.In(location)
	candidate := time.Date(local.Year(), local.Month(), reset.Day, 0, 0, 0, 0, location)
	if !candidate.After(local) {
		candidate = candidate.AddDate(0, 1, 0)
	}
	return candidate, nil
}

type ClashClient struct {
	URL, Secret string
	Client      *http.Client
}
type clashResponse struct {
	Upload   int64 `json:"uploadTotal"`
	Download int64 `json:"downloadTotal"`
}

func (c ClashClient) Sample(ctx context.Context) (int64, int64, error) {
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Secret)
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("clash API status %d", resp.StatusCode)
	}
	var data clashResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return 0, 0, err
	}
	return data.Upload, data.Download, nil
}

func (t *Tracker) Run(ctx context.Context, client ClashClient) {
	poll := time.NewTicker(4 * time.Second)
	persist := time.NewTicker(30 * time.Second)
	defer poll.Stop()
	defer persist.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = t.Persist()
			return
		case <-poll.C:
			upload, download, err := client.Sample(ctx)
			if err == nil {
				_, _ = t.ApplySample(ctx, upload, download)
				_ = t.CheckScheduledReset(ctx)
			} else if t.State().QuotaExceeded {
				_ = t.CheckScheduledReset(ctx)
			}
		case <-persist.C:
			_ = t.Persist()
		}
	}
}
