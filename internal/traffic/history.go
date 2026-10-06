package traffic

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/boltguo/sbm/internal/model"
	"github.com/boltguo/sbm/internal/store"
	_ "modernc.org/sqlite"
)

var ErrHistoryDisabled = errors.New("流量历史记录未启用")

type Usage struct {
	Date                       string `json:"date"`
	Upload                     int64  `json:"upload"`
	Download                   int64  `json:"download"`
	ProxyUsedBytes             int64  `json:"proxyUsedBytes"`
	EstimatedProviderUsedBytes int64  `json:"estimatedProviderUsedBytes"`
	Partial                    bool   `json:"partial"`
	Imported                   bool   `json:"imported"`
}

// ImportedUsage preserves an existing total without inventing daily usage.
type ImportedUsage struct {
	StartedAt                  time.Time `json:"startedAt"`
	EndedAt                    time.Time `json:"endedAt"`
	Upload                     int64     `json:"upload"`
	Download                   int64     `json:"download"`
	EstimatedProviderUsedBytes int64     `json:"estimatedProviderUsedBytes"`
}

type HistoryResponse struct {
	Granularity string          `json:"granularity"`
	Timezone    string          `json:"timezone"`
	StartedAt   time.Time       `json:"startedAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
	Rows        []Usage         `json:"rows"`
	Imports     []ImportedUsage `json:"imports"`
}

type historyStore struct {
	db        *sql.DB
	location  *time.Location
	startedAt time.Time
}

// OpenWithHistory makes SQLite the authoritative checkpoint. JSON remains a
// compatibility mirror for the installer and old administrative commands.
func OpenWithHistory(statePath, databasePath string, config ConfigSource, core CoreControl, now func() time.Time) (*Tracker, error) {
	if now == nil {
		now = time.Now
	}
	zone := config.Get().Reset.Timezone
	if zone == "" || zone == "Local" {
		zone = "UTC"
	}
	h, err := openHistory(databasePath, zone, now())
	if err != nil {
		return nil, err
	}
	state, found, err := h.loadState()
	if err != nil {
		h.db.Close()
		return nil, err
	}
	var t *Tracker
	if found {
		if state.Version != model.StateVersion {
			h.db.Close()
			return nil, fmt.Errorf("unsupported state version %d", state.Version)
		}
		t = &Tracker{state: state, file: store.NewJSONFile[model.State](statePath), config: config, core: core, now: now, health: SampleHealth{Status: SampleStatusWaiting}}
	} else {
		t, err = Open(statePath, config, core, now)
		if err == nil {
			err = h.initialize(t.State(), config.Get().TrafficQuota.ProviderUsageFactor())
		}
		if err != nil {
			h.db.Close()
			return nil, err
		}
	}
	t.history = h
	t.pendingHistory = make(map[string]Usage)
	t.pendingGatewayHistory = make(map[gatewayHistoryKey]gatewayHistoryUsage)
	if err := h.initializeGatewayHistory(t.state, now()); err != nil {
		h.db.Close()
		return nil, err
	}
	t.historyLastSample, err = h.loadLastSample(t.state.UpdatedAt)
	if err != nil {
		h.db.Close()
		return nil, err
	}
	t.persistenceHealth = SampleHealth{Status: SampleStatusHealthy, LastSuccessAt: now()}
	return t, nil
}

func openHistory(path, zone string, now time.Time) (*historyStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	// Create privately before SQLite opens the file; its default mode is 0644.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	fail := func(err error) (*historyStore, error) { db.Close(); return nil, err }
	if _, err := db.Exec(`PRAGMA busy_timeout=5000; PRAGMA synchronous=FULL; PRAGMA journal_mode=DELETE;`); err != nil {
		return fail(err)
	}
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fail(err)
	}
	if version > 2 {
		return fail(fmt.Errorf("unsupported traffic database version %d", version))
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS checkpoint (id INTEGER PRIMARY KEY CHECK(id=1), state TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS daily (
			date TEXT PRIMARY KEY, upload INTEGER NOT NULL, download INTEGER NOT NULL,
			provider INTEGER NOT NULL, partial INTEGER NOT NULL DEFAULT 0);
		CREATE TABLE IF NOT EXISTS imports (
			id INTEGER PRIMARY KEY, started_at TEXT NOT NULL, ended_at TEXT NOT NULL,
			upload INTEGER NOT NULL, download INTEGER NOT NULL, provider INTEGER NOT NULL);
		CREATE TABLE IF NOT EXISTS gateway_daily (
			gateway_id TEXT NOT NULL, date TEXT NOT NULL, tx INTEGER NOT NULL,
			rx INTEGER NOT NULL, partial INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY(gateway_id, date));
		CREATE TABLE IF NOT EXISTS gateway_imports (
			gateway_id TEXT PRIMARY KEY, started_at TEXT NOT NULL,
			ended_at TEXT NOT NULL, tx INTEGER NOT NULL, rx INTEGER NOT NULL);
		PRAGMA user_version=2;`); err != nil {
		return fail(err)
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO metadata VALUES ('timezone', ?), ('started_at', ?)`, zone, now.UTC().Format(time.RFC3339Nano)); err != nil {
		return fail(err)
	}
	var started string
	if err := db.QueryRow(`SELECT value FROM metadata WHERE key='timezone'`).Scan(&zone); err != nil {
		return fail(err)
	}
	if err := db.QueryRow(`SELECT value FROM metadata WHERE key='started_at'`).Scan(&started); err != nil {
		return fail(err)
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return fail(err)
	}
	startedAt, err := time.Parse(time.RFC3339Nano, started)
	if err != nil {
		return fail(err)
	}
	return &historyStore{db: db, location: location, startedAt: startedAt}, nil
}

func (h *historyStore) loadState() (model.State, bool, error) {
	var raw string
	err := h.db.QueryRow(`SELECT state FROM checkpoint WHERE id=1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return model.State{}, false, nil
	}
	if err != nil {
		return model.State{}, false, err
	}
	var state model.State
	err = json.Unmarshal([]byte(raw), &state)
	return state, true, err
}

func (h *historyStore) initialize(state model.State, factor int64) error {
	tx, err := h.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if state.Total() > 0 {
		if _, err = tx.Exec(`INSERT INTO imports (started_at, ended_at, upload, download, provider) VALUES (?, ?, ?, ?, ?)`,
			state.PeriodStartedAt.UTC().Format(time.RFC3339Nano), state.UpdatedAt.UTC().Format(time.RFC3339Nano), state.Upload, state.Download, state.Total()*factor); err != nil {
			return err
		}
	}
	if err := saveCheckpoint(tx, state); err != nil {
		return err
	}
	if err := saveLastSample(tx, state.UpdatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (h *historyStore) loadLastSample(fallback time.Time) (time.Time, error) {
	var raw string
	err := h.db.QueryRow(`SELECT value FROM metadata WHERE key='last_sample_at'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		// Databases created before this metadata used UpdatedAt as the baseline.
		return fallback, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339Nano, raw)
}

func saveLastSample(tx *sql.Tx, sampledAt time.Time) error {
	_, err := tx.Exec(`INSERT INTO metadata VALUES ('last_sample_at', ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, sampledAt.UTC().Format(time.RFC3339Nano))
	return err
}

func saveCheckpoint(tx *sql.Tx, state model.State) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO checkpoint VALUES (1, ?) ON CONFLICT(id) DO UPDATE SET state=excluded.state`, string(raw))
	return err
}

func (h *historyStore) save(state model.State, pending map[string]Usage, gateways map[gatewayHistoryKey]gatewayHistoryUsage, sampledAt time.Time) error {
	tx, err := h.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for day, usage := range pending {
		_, err := tx.Exec(`INSERT INTO daily VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(date) DO UPDATE SET upload=upload+excluded.upload,
			download=download+excluded.download, provider=provider+excluded.provider,
			partial=MAX(partial, excluded.partial)`, day, usage.Upload, usage.Download, usage.EstimatedProviderUsedBytes, usage.Partial)
		if err != nil {
			return err
		}
	}
	for key, usage := range gateways {
		_, err := tx.Exec(`INSERT INTO gateway_daily VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(gateway_id, date) DO UPDATE SET tx=tx+excluded.tx,
			rx=rx+excluded.rx, partial=MAX(partial, excluded.partial)`, key.ID, key.Day, usage.TX, usage.RX, usage.Partial)
		if err != nil {
			return err
		}
	}
	if err := saveCheckpoint(tx, state); err != nil {
		return err
	}
	if err := saveLastSample(tx, sampledAt); err != nil {
		return err
	}
	return tx.Commit()
}

// Called with t.mu held. Bytes are assigned when sampled, never fabricated for
// earlier days after an outage. Cross-day gaps are explicitly marked partial.
func (t *Tracker) recordHistory(now time.Time, upload, download, factor int64, restarted bool) {
	if t.history == nil {
		return
	}
	day := now.In(t.history.location).Format(time.DateOnly)
	usage := t.pendingHistory[day]
	usage.Upload += upload
	usage.Download += download
	usage.EstimatedProviderUsedBytes += (upload + download) * factor
	gap := !t.historyLastSample.IsZero() && now.Sub(t.historyLastSample) > 5*time.Second
	usage.Partial = usage.Partial || gap || restarted || day == t.history.startedAt.In(t.history.location).Format(time.DateOnly)
	t.pendingHistory[day] = usage
	if gap {
		previousDay := t.historyLastSample.In(t.history.location).Format(time.DateOnly)
		if previousDay >= t.history.startedAt.In(t.history.location).Format(time.DateOnly) {
			previous := t.pendingHistory[previousDay]
			previous.Partial = true
			t.pendingHistory[previousDay] = previous
		}
	}
	t.historyLastSample = now
}

func (t *Tracker) Close() error {
	if t.history == nil {
		return nil
	}
	return t.history.db.Close()
}

func (t *Tracker) History(ctx context.Context, granularity, from, to string) (HistoryResponse, error) {
	if t.history == nil {
		return HistoryResponse{}, ErrHistoryDisabled
	}
	return t.history.query(ctx, granularity, from, to, t.now())
}

func (h *historyStore) query(ctx context.Context, granularity, from, to string, now time.Time) (HistoryResponse, error) {
	result := HistoryResponse{Granularity: granularity, Timezone: h.location.String(), StartedAt: h.startedAt, Rows: []Usage{}, Imports: []ImportedUsage{}}
	start, end, err := historyRange(granularity, from, to, h.location, now)
	if err != nil {
		return result, err
	}
	// One read transaction keeps the checkpoint, daily sums and import rows at
	// the same committed sample, even while the background sampler persists.
	tx, err := h.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT state FROM checkpoint WHERE id=1`).Scan(&raw); err != nil {
		return result, err
	}
	var state model.State
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return result, err
	}
	result.UpdatedAt = state.UpdatedAt
	rows, err := tx.QueryContext(ctx, `SELECT date, upload, download, provider, partial FROM daily WHERE date>=? AND date<? ORDER BY date`, start.Format(time.DateOnly), end.Format(time.DateOnly))
	if err != nil {
		return result, err
	}
	buckets := make(map[string]Usage)
	for rows.Next() {
		var usage Usage
		if err := rows.Scan(&usage.Date, &usage.Upload, &usage.Download, &usage.EstimatedProviderUsedBytes, &usage.Partial); err != nil {
			rows.Close()
			return result, err
		}
		key := usage.Date
		if granularity == "month" {
			key = key[:7]
		}
		bucket := buckets[key]
		bucket.Date = key
		bucket.Upload += usage.Upload
		bucket.Download += usage.Download
		bucket.EstimatedProviderUsedBytes += usage.EstimatedProviderUsedBytes
		bucket.Partial = bucket.Partial || usage.Partial
		buckets[key] = bucket
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	imports, err := tx.QueryContext(ctx, `SELECT started_at, ended_at, upload, download, provider FROM imports`)
	if err != nil {
		return result, err
	}
	for imports.Next() {
		var item ImportedUsage
		var started, ended string
		if err := imports.Scan(&started, &ended, &item.Upload, &item.Download, &item.EstimatedProviderUsedBytes); err != nil {
			imports.Close()
			return result, err
		}
		item.StartedAt, err = time.Parse(time.RFC3339Nano, started)
		if err == nil {
			item.EndedAt, err = time.Parse(time.RFC3339Nano, ended)
		}
		if err != nil {
			imports.Close()
			return result, err
		}
		if item.EndedAt.Before(start) || !item.StartedAt.Before(end) {
			continue
		}
		result.Imports = append(result.Imports, item)
		month := item.StartedAt.In(h.location).Format("2006-01")
		// Only a total wholly within one calendar month can join that month.
		if granularity == "month" && month == item.EndedAt.In(h.location).Format("2006-01") {
			bucket := buckets[month]
			bucket.Date = month
			bucket.Upload += item.Upload
			bucket.Download += item.Download
			bucket.EstimatedProviderUsedBytes += item.EstimatedProviderUsedBytes
			bucket.Imported, bucket.Partial = true, true
			buckets[month] = bucket
		}
	}
	err = imports.Err()
	imports.Close()
	if err != nil {
		return result, err
	}
	layout := time.DateOnly
	if granularity == "month" {
		layout = "2006-01"
	}
	current := now.In(h.location).Format(layout)
	for _, bucket := range buckets {
		bucket.ProxyUsedBytes = bucket.Upload + bucket.Download
		bucket.Partial = bucket.Partial || bucket.Date == current
		result.Rows = append(result.Rows, bucket)
	}
	sort.Slice(result.Rows, func(i, j int) bool { return result.Rows[i].Date < result.Rows[j].Date })
	return result, tx.Commit()
}

var ErrHistoryRange = errors.New("流量历史查询范围无效")

func historyRange(granularity, from, to string, location *time.Location, now time.Time) (time.Time, time.Time, error) {
	now = now.In(location)
	layout := time.DateOnly
	if granularity == "month" {
		layout = "2006-01"
	} else if granularity != "day" {
		return time.Time{}, time.Time{}, ErrHistoryRange
	}
	if from == "" {
		if granularity == "month" {
			from = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, location).AddDate(0, -11, 0).Format(layout)
		} else {
			from = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, location).Format(layout)
		}
	}
	if to == "" {
		to = now.Format(layout)
	}
	start, err := time.ParseInLocation(layout, from, location)
	if err != nil || start.Format(layout) != from {
		return time.Time{}, time.Time{}, ErrHistoryRange
	}
	end, err := time.ParseInLocation(layout, to, location)
	if err != nil || end.Format(layout) != to || end.Before(start) {
		return time.Time{}, time.Time{}, ErrHistoryRange
	}
	if granularity == "month" {
		if (end.Year()-start.Year())*12+int(end.Month()-start.Month()) > 119 {
			return time.Time{}, time.Time{}, ErrHistoryRange
		}
		end = end.AddDate(0, 1, 0)
	} else {
		if end.Sub(start) > 366*24*time.Hour {
			return time.Time{}, time.Time{}, ErrHistoryRange
		}
		end = end.AddDate(0, 0, 1)
	}
	return start, end, nil
}
