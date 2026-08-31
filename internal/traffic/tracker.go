package traffic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
	_ "time/tzdata"

	"github.com/boltguo/sbm/internal/model"
	"github.com/boltguo/sbm/internal/store"
)

type CoreControl interface {
	Stop(context.Context) error
	Start(context.Context) error
	Active(context.Context) (bool, error)
}
type ConfigSource interface{ Get() model.Config }

type coreGeneration interface {
	Generation() (string, error)
}

type Tracker struct {
	mu        sync.RWMutex
	controlMu sync.Mutex
	sampleMu  sync.Mutex
	state     model.State
	file      *store.JSONFile[model.State]
	config    ConfigSource
	core      CoreControl
	now       func() time.Time
	health    SampleHealth
}

type SampleHealth struct {
	Status        string    `json:"status"`
	LastSuccessAt time.Time `json:"lastSuccessAt,omitempty"`
	FailureSince  time.Time `json:"failureSince,omitempty"`
}

const (
	SampleStatusWaiting     = "waiting"
	SampleStatusHealthy     = "healthy"
	SampleStatusInterrupted = "interrupted"
)

// ErrSampleUnavailable reports that the core counters could not be read. It is
// distinct from an apply failure so callers can tell "retry later" apart from
// "the state could not be saved".
var ErrSampleUnavailable = errors.New("读取核心流量失败")

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
			state.NextResetAt, err = NextMonthlyReset(now(), cfg.Reset)
			if err != nil {
				return nil, err
			}
		}
		if saveErr := file.SaveWithoutBackup(state); saveErr != nil {
			return nil, fmt.Errorf("初始化流量状态: %w", saveErr)
		}
	}
	if state.Version != model.StateVersion {
		return nil, fmt.Errorf("unsupported state version %d", state.Version)
	}
	return &Tracker{state: state, file: file, config: config, core: core, now: now, health: SampleHealth{Status: SampleStatusWaiting}}, nil
}

func (t *Tracker) UpdateSchedule() error {
	now := t.now()
	next := time.Time{}
	if cfg := t.config.Get(); cfg.Reset.Mode == "monthly" {
		var err error
		next, err = NextMonthlyReset(now, cfg.Reset)
		if err != nil {
			return err
		}
	}
	t.mu.Lock()
	t.state.NextResetAt = next
	t.state.UpdatedAt = now
	snapshot := t.state
	t.mu.Unlock()
	return t.persist(snapshot)
}

func NewForTest(state model.State, config ConfigSource, core CoreControl, now func() time.Time) *Tracker {
	if now == nil {
		now = time.Now
	}
	return &Tracker{state: state, config: config, core: core, now: now, health: SampleHealth{Status: SampleStatusWaiting}}
}

func (t *Tracker) State() model.State { t.mu.RLock(); defer t.mu.RUnlock(); return t.state }
func (t *Tracker) SampleHealth() SampleHealth {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.health
}

// Sample reads the core counters and applies them as one atomic step.
//
// Every caller must go through here. ApplySample infers a core restart from a
// counter that moved backwards, so two concurrent readings applied out of order
// would look like a restart and add the whole counter a second time. Serialising
// read and apply keeps at most one reading in flight, which makes that
// reordering impossible.
func (t *Tracker) Sample(ctx context.Context, client ClashClient) (bool, error) {
	t.sampleMu.Lock()
	defer t.sampleMu.Unlock()
	if err := t.CheckScheduledReset(ctx); err != nil {
		t.recordSampleFailure()
		return false, fmt.Errorf("检查流量重置周期: %w", err)
	}
	generationBefore := t.readCoreGeneration()
	upload, download, err := client.Sample(ctx)
	if err != nil {
		t.recordSampleFailure()
		return false, fmt.Errorf("%w: %v", ErrSampleUnavailable, err)
	}
	generationAfter := t.readCoreGeneration()
	if generationBefore != "" && generationAfter != "" && generationBefore != generationAfter {
		t.recordSampleFailure()
		return false, fmt.Errorf("%w: sing-box restarted while counters were being read", ErrSampleUnavailable)
	}
	generation := ""
	if generationBefore != "" && generationBefore == generationAfter {
		generation = generationBefore
	}
	t.recordSampleSuccess()
	return t.applySample(ctx, upload, download, generation)
}

func (t *Tracker) readCoreGeneration() string {
	source, ok := t.core.(coreGeneration)
	if !ok {
		return ""
	}
	generation, err := source.Generation()
	if err != nil {
		return ""
	}
	return generation
}

func (t *Tracker) recordSampleFailure() {
	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.health.FailureSince.IsZero() {
		t.health.FailureSince = now
	}
	t.health.Status = SampleStatusInterrupted
}

func (t *Tracker) recordSampleSuccess() {
	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.health = SampleHealth{Status: SampleStatusHealthy, LastSuccessAt: now}
}

// ApplySample folds one core counter reading into the period totals. Callers
// outside tests should use Sample so readings stay serialised.
func (t *Tracker) ApplySample(ctx context.Context, currentUpload, currentDownload int64) (bool, error) {
	return t.applySample(ctx, currentUpload, currentDownload, "")
}

func (t *Tracker) applySample(ctx context.Context, currentUpload, currentDownload int64, generation string) (bool, error) {
	if currentUpload < 0 || currentDownload < 0 {
		return false, errors.New("核心流量计数不能为负数")
	}
	limit, err := t.config.Get().EffectiveTrafficLimitBytes()
	if err != nil {
		return false, fmt.Errorf("计算流量限额: %w", err)
	}
	t.mu.Lock()
	restarted := currentUpload < t.state.LastCoreUpload || currentDownload < t.state.LastCoreDownload
	if generation != "" && t.state.CoreGeneration != "" && generation != t.state.CoreGeneration {
		restarted = true
	}
	if restarted {
		t.state.Upload += currentUpload
		t.state.Download += currentDownload
	} else {
		t.state.Upload += currentUpload - t.state.LastCoreUpload
		t.state.Download += currentDownload - t.state.LastCoreDownload
	}
	t.state.LastCoreUpload = currentUpload
	t.state.LastCoreDownload = currentDownload
	if generation != "" {
		t.state.CoreGeneration = generation
	}
	t.state.UpdatedAt = t.now()
	shouldExceed := limit > 0 && t.state.Total() >= limit
	newlyExceeded := !t.state.QuotaExceeded && shouldExceed
	if newlyExceeded {
		t.state.QuotaExceeded = true
	}
	snapshot := t.state
	t.mu.Unlock()
	var persistErr error
	var coreErr error
	if shouldExceed {
		persistErr = t.persist(snapshot)
		coreErr = t.reconcileCore(ctx)
	}
	return newlyExceeded, errors.Join(persistErr, coreErr)
}

func (t *Tracker) Reset(ctx context.Context) error {
	now := t.now()
	next := time.Time{}
	if cfg := t.config.Get(); cfg.Reset.Mode == "monthly" {
		var err error
		next, err = NextMonthlyReset(now, cfg.Reset)
		if err != nil {
			return err
		}
	}
	t.mu.Lock()
	wasExceeded := t.state.QuotaExceeded
	t.state.Upload = 0
	t.state.Download = 0
	// Keep the latest core baselines so pre-reset bytes are never counted twice.
	t.state.QuotaExceeded = false
	t.state.PeriodStartedAt = now
	t.state.UpdatedAt = now
	t.state.NextResetAt = next
	snapshot := t.state
	t.mu.Unlock()
	if err := t.persist(snapshot); err != nil {
		return err
	}
	if wasExceeded {
		return t.reconcileCore(ctx)
	}
	return nil
}

// ReconcileQuota updates the desired quota state and makes the core converge on it.
func (t *Tracker) ReconcileQuota(ctx context.Context) error {
	limit, err := t.config.Get().EffectiveTrafficLimitBytes()
	if err != nil {
		return fmt.Errorf("计算流量限额: %w", err)
	}
	t.mu.Lock()
	shouldExceed := limit > 0 && t.state.Total() >= limit
	wasExceeded := t.state.QuotaExceeded
	changed := shouldExceed != wasExceeded
	if changed {
		t.state.QuotaExceeded = shouldExceed
		t.state.UpdatedAt = t.now()
	}
	snapshot := t.state
	t.mu.Unlock()
	var persistErr error
	if changed {
		persistErr = t.persist(snapshot)
	}
	return errors.Join(persistErr, t.reconcileCore(ctx))
}

func (t *Tracker) reconcileCore(ctx context.Context) error {
	if t.core == nil {
		return nil
	}
	t.controlMu.Lock()
	defer t.controlMu.Unlock()
	shouldExceed := t.State().QuotaExceeded
	active, err := t.core.Active(ctx)
	if err != nil && !shouldExceed {
		// Nothing to enforce, so leaving the core untouched until the state can
		// be read again is safe.
		return fmt.Errorf("检查 sing-box 运行状态: %w", err)
	}
	if err == nil && active == !shouldExceed {
		return nil
	}
	if shouldExceed {
		// An unreadable state must never be taken as "already stopped": that
		// would skip enforcement while the core keeps serving traffic. Stopping
		// is idempotent, so it is issued again and the error surfaces if it fails.
		if err := t.core.Stop(ctx); err != nil {
			return fmt.Errorf("应用流量限额: %w", err)
		}
		return nil
	}
	if err := t.core.Start(ctx); err != nil {
		return fmt.Errorf("恢复 sing-box: %w", err)
	}
	return nil
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
	poll := time.NewTicker(time.Second)
	schedule := time.NewTicker(15 * time.Second)
	persist := time.NewTicker(30 * time.Second)
	defer poll.Stop()
	defer schedule.Stop()
	defer persist.Stop()
	sampleFailed := false
	for {
		select {
		case <-ctx.Done():
			if err := t.Persist(); err != nil {
				log.Printf("traffic: final state save failed: %v", err)
			}
			return
		case <-poll.C:
			_, err := t.Sample(ctx, client)
			if err == nil {
				sampleFailed = false
				continue
			}
			if !errors.Is(err, ErrSampleUnavailable) {
				log.Printf("traffic: apply sample failed: %v", err)
				continue
			}
			if t.State().QuotaExceeded {
				sampleFailed = false
				continue
			}
			if !sampleFailed {
				log.Printf("traffic: read core counters failed: %v", err)
				sampleFailed = true
			}
			if err := t.ReconcileQuota(ctx); err != nil {
				log.Printf("traffic: reconcile core state failed: %v", err)
			}
		case <-schedule.C:
			if err := t.ReconcileQuota(ctx); err != nil {
				log.Printf("traffic: periodic quota reconciliation failed: %v", err)
			}
		case <-persist.C:
			if err := t.Persist(); err != nil {
				log.Printf("traffic: periodic state save failed: %v", err)
			}
		}
	}
}
