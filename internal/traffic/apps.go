package traffic

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const maxDailyAppDomains = 200

//go:embed app_rules.json
var defaultAppRulesJSON []byte

type ClashConnection struct {
	ID       string              `json:"id"`
	Upload   int64               `json:"upload"`
	Download int64               `json:"download"`
	Metadata ClashConnectionMeta `json:"metadata"`
	Chains   []string            `json:"chains"`
}

type ClashConnectionMeta struct {
	Network         string `json:"network"`
	Type            string `json:"type"`
	Host            string `json:"host"`
	DestinationIP   string `json:"destinationIP"`
	DestinationPort string `json:"destinationPort"`
}

type ClashSnapshot struct {
	Upload      int64
	Download    int64
	Connections []ClashConnection
}

type connCounter struct {
	upload   int64
	download int64
}

type appHistoryKey struct {
	Day    string
	Hour   int64
	AppID  string
	Domain string
}

type appHistoryUsage struct {
	Upload   int64
	Download int64
}

type AppDomainUsage struct {
	Domain   string `json:"domain"`
	Upload   int64  `json:"upload"`
	Download int64  `json:"download"`
	Total    int64  `json:"total"`
}

type AppUsage struct {
	ID       string           `json:"id"`
	Name     string           `json:"name"`
	Category string           `json:"category"`
	Upload   int64            `json:"upload"`
	Download int64            `json:"download"`
	Total    int64            `json:"total"`
	Domains  []AppDomainUsage `json:"domains"`
}

type AppTrafficResponse struct {
	Period        string     `json:"period"`
	Date          string     `json:"date"`
	Timezone      string     `json:"timezone"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	TotalUpload   int64      `json:"totalUpload"`
	TotalDownload int64      `json:"totalDownload"`
	TotalBytes    int64      `json:"totalBytes"`
	Apps          []AppUsage `json:"apps"`
}

type appRulesFile struct {
	Rules              []appRuleConfig `json:"rules"`
	MultiLabelSuffixes []string        `json:"multiLabelSuffixes"`
}

type appRuleConfig struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Category   string   `json:"category"`
	Domains    []string `json:"domains,omitempty"`
	Subdomains []string `json:"subdomains,omitempty"`
	CIDRs      []string `json:"cidrs,omitempty"`
	IPLabel    string   `json:"ipLabel,omitempty"`
}

type appRule struct {
	id         string
	name       string
	category   string
	domains    []string
	subdomains []string
	cidrs      []netip.Prefix
	ipLabel    string
}

var (
	rulesMu          sync.RWMutex
	builtInAppRules  []appRule
	domainToApp      map[string]int
	subdomainToApp   map[string]int
	appMetaByID      map[string]appRule
	multiLabelSuffix map[string]struct{}
)

func init() {
	if err := LoadAppRules(defaultAppRulesJSON); err != nil {
		panic(fmt.Sprintf("invalid embedded app_rules.json: %v", err))
	}
}

// LoadAppRulesFile loads custom application classification rules from a JSON
// file on disk (for example /etc/sbm/app-rules.json).
func LoadAppRulesFile(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return LoadAppRules(raw)
}

// LoadAppRules parses and activates an application classification JSON document.
func LoadAppRules(raw []byte) error {
	var parsed appRulesFile
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return fmt.Errorf("parse app rules JSON: %w", err)
	}
	rules := make([]appRule, 0, len(parsed.Rules))
	domMap := make(map[string]int, len(parsed.Rules)*4)
	subMap := make(map[string]int, 16)
	metaMap := make(map[string]appRule, len(parsed.Rules)+4)

	for _, cfg := range parsed.Rules {
		id := strings.TrimSpace(cfg.ID)
		name := strings.TrimSpace(cfg.Name)
		category := strings.TrimSpace(cfg.Category)
		if id == "" || name == "" {
			return errors.New("app rule must specify non-empty id and name")
		}
		if category == "" {
			category = "other"
		}
		prefixes := make([]netip.Prefix, 0, len(cfg.CIDRs))
		for _, cidr := range cfg.CIDRs {
			p, err := netip.ParsePrefix(strings.TrimSpace(cidr))
			if err != nil {
				return fmt.Errorf("app rule %q invalid CIDR %q: %w", id, cidr, err)
			}
			prefixes = append(prefixes, p)
		}
		rule := appRule{
			id:         id,
			name:       name,
			category:   category,
			domains:    make([]string, 0, len(cfg.Domains)),
			subdomains: make([]string, 0, len(cfg.Subdomains)),
			cidrs:      prefixes,
			ipLabel:    strings.TrimSpace(cfg.IPLabel),
		}
		idx := len(rules)
		for _, d := range cfg.Domains {
			norm := strings.ToLower(strings.TrimSpace(d))
			if norm != "" {
				rule.domains = append(rule.domains, norm)
				domMap[norm] = idx
			}
		}
		for _, sub := range cfg.Subdomains {
			norm := strings.ToLower(strings.TrimSpace(sub))
			if norm != "" {
				rule.subdomains = append(rule.subdomains, norm)
				subMap[norm] = idx
			}
		}
		rules = append(rules, rule)
		metaMap[id] = rule
	}

	suffixes := make(map[string]struct{}, len(parsed.MultiLabelSuffixes))
	for _, s := range parsed.MultiLabelSuffixes {
		norm := strings.ToLower(strings.Trim(strings.TrimSpace(s), "."))
		if norm != "" {
			suffixes[norm] = struct{}{}
		}
	}

	rulesMu.Lock()
	builtInAppRules = rules
	domainToApp = domMap
	subdomainToApp = subMap
	appMetaByID = metaMap
	if len(suffixes) > 0 {
		multiLabelSuffix = suffixes
	}
	rulesMu.Unlock()
	return nil
}

// ClassifyConnection maps a Clash API connection metadata record to a
// privacy-preserving (appID, domainKey) pair. Subdomains are always collapsed
// to registrable root domains (eTLD+1) or known service endpoints, and raw
// destination IPs are never persisted.
func ClassifyConnection(meta ClashConnectionMeta) (appID, domainKey string) {
	rulesMu.RLock()
	defer rulesMu.RUnlock()

	host := normalizeHost(meta.Host)
	if host != "" {
		// Check specific multi-service subdomains first (e.g. api.openai.com, antigravity-pa.googleapis.com).
		if idx, ok := subdomainToApp[host]; ok {
			return builtInAppRules[idx].id, host
		}
		bestSub := ""
		bestIdx := -1
		for sub, idx := range subdomainToApp {
			if len(sub) > len(bestSub) && strings.HasSuffix(host, "."+sub) {
				bestSub = sub
				bestIdx = idx
			}
		}
		if bestIdx >= 0 {
			return builtInAppRules[bestIdx].id, bestSub
		}
		root := extractRootDomainLocked(host)
		if root != "" {
			if idx, ok := domainToApp[root]; ok {
				return builtInAppRules[idx].id, ecosystemDomainKey(host, root)
			}
			// Check if host matches a multi-label rule domain (such as google.com.hk).
			for d, idx := range domainToApp {
				if host == d || strings.HasSuffix(host, "."+d) {
					return builtInAppRules[idx].id, d
				}
			}
			return "site:" + root, root
		}
	}

	// IP-only connection (or Host was an IP literal): check known IP CIDR ranges (e.g. Telegram MTProto).
	ipStr := strings.TrimSpace(meta.DestinationIP)
	if ipStr == "" {
		ipStr = strings.TrimSpace(meta.Host)
	}
	if h, _, err := net.SplitHostPort(ipStr); err == nil {
		ipStr = h
	}
	ipStr = strings.Trim(ipStr, "[]")
	if addr, err := netip.ParseAddr(ipStr); err == nil {
		addr = addr.Unmap()
		for _, rule := range builtInAppRules {
			for _, prefix := range rule.cidrs {
				if prefix.Contains(addr) {
					label := rule.ipLabel
					if label == "" {
						label = rule.name + " IP"
					}
					return rule.id, label
				}
			}
		}
	}

	netProto := strings.ToUpper(strings.TrimSpace(meta.Network))
	if netProto != "UDP" {
		netProto = "TCP"
	}
	port := strings.TrimSpace(meta.DestinationPort)
	portBucket := classifyPortBucket(port)
	if netProto == "UDP" {
		return "ip:udp", "UDP/" + portBucket
	}
	return "ip:tcp", "TCP/" + portBucket
}

func ecosystemDomainKey(host, root string) string {
	if root == "googleapis.com" && host != root {
		parts := strings.Split(host, ".")
		if len(parts) >= 3 {
			return strings.Join(parts[len(parts)-3:], ".")
		}
	}
	return root
}

// reclassifyStoredDomain re-evaluates a stored (appID, domain) row against the
// currently active app_rules.json so that adding a domain to the rules file
// retroactively groups existing SQLite records into the new application bucket.
func reclassifyStoredDomain(appID, domain string) (string, string) {
	if appID == "other" || strings.HasPrefix(appID, "ip:") || strings.Contains(domain, " ") {
		return appID, domain
	}
	host := normalizeHost(domain)
	if host == "" {
		return appID, domain
	}
	rulesMu.RLock()
	defer rulesMu.RUnlock()

	if idx, ok := subdomainToApp[host]; ok {
		return builtInAppRules[idx].id, host
	}
	bestSub := ""
	bestIdx := -1
	for sub, idx := range subdomainToApp {
		if len(sub) > len(bestSub) && strings.HasSuffix(host, "."+sub) {
			bestSub = sub
			bestIdx = idx
		}
	}
	if bestIdx >= 0 {
		return builtInAppRules[bestIdx].id, bestSub
	}
	root := extractRootDomainLocked(host)
	if root != "" {
		if idx, ok := domainToApp[root]; ok {
			return builtInAppRules[idx].id, ecosystemDomainKey(host, root)
		}
		for d, idx := range domainToApp {
			if host == d || strings.HasSuffix(host, "."+d) {
				return builtInAppRules[idx].id, d
			}
		}
	}
	return appID, domain
}

func classifyPortBucket(port string) string {
	switch port {
	case "443", "80", "53", "853", "22", "8080", "8443", "5222", "5228":
		return port
	case "":
		return "unknown"
	default:
		return "other"
	}
}

func normalizeHost(raw string) string {
	host := strings.ToLower(strings.TrimSpace(raw))
	if host == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return ""
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return ""
	}
	for i := 0; i < len(host); i++ {
		c := host[i]
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '.' || c == '_') {
			return ""
		}
	}
	if !strings.Contains(host, ".") {
		return ""
	}
	return host
}

func extractRootDomainLocked(host string) string {
	parts := strings.Split(host, ".")
	n := len(parts)
	if n < 2 {
		return ""
	}
	for _, p := range parts {
		if p == "" {
			return ""
		}
	}
	if n == 2 {
		return host
	}
	lastTwo := parts[n-2] + "." + parts[n-1]
	if _, ok := multiLabelSuffix[lastTwo]; ok && n >= 3 {
		return parts[n-3] + "." + lastTwo
	}
	return lastTwo
}

func resolveAppMeta(appID string) (name, category string) {
	rulesMu.RLock()
	rule, ok := appMetaByID[appID]
	rulesMu.RUnlock()
	if ok {
		return rule.name, rule.category
	}
	if strings.HasPrefix(appID, "site:") {
		domain := strings.TrimPrefix(appID, "site:")
		return domain, "site"
	}
	switch appID {
	case "ip:tcp":
		return "Direct IP (TCP)", "direct_ip"
	case "ip:udp":
		return "Direct IP (UDP)", "direct_ip"
	case "other":
		return "Other", "other"
	default:
		return appID, "other"
	}
}

// Called with t.mu held.
func (t *Tracker) recordConnections(now time.Time, conns []ClashConnection, restarted, hadBaseline bool) {
	if t.pendingAppHistory == nil {
		t.pendingAppHistory = make(map[appHistoryKey]appHistoryUsage)
	}
	if restarted {
		t.activeConns = nil
	}
	// When the panel restarts while sing-box is already running (hadBaseline && !restarted),
	// seed activeConns on the first sample so pre-existing connection totals are not counted twice.
	seedOnly := !t.connsSeeded && hadBaseline && !restarted
	t.connsSeeded = true

	if len(conns) == 0 {
		t.activeConns = nil
		return
	}

	loc := time.UTC
	if t.history != nil && t.history.location != nil {
		loc = t.history.location
	}
	day := now.In(loc).Format(time.DateOnly)
	hour := now.UTC().Truncate(time.Hour).Unix()

	nextConns := make(map[string]connCounter, len(conns))
	for _, c := range conns {
		if c.ID == "" || (c.Upload < 0 || c.Download < 0) {
			continue
		}
		nextConns[c.ID] = connCounter{upload: c.Upload, download: c.Download}
		if seedOnly {
			continue
		}
		prev, exists := t.activeConns[c.ID]
		upDelta := c.Upload
		downDelta := c.Download
		if exists {
			if c.Upload >= prev.upload && c.Download >= prev.download {
				upDelta = c.Upload - prev.upload
				downDelta = c.Download - prev.download
			}
		}
		if upDelta == 0 && downDelta == 0 {
			continue
		}
		appID, domainKey := ClassifyConnection(c.Metadata)
		key := appHistoryKey{Day: day, Hour: hour, AppID: appID, Domain: domainKey}
		usage := t.pendingAppHistory[key]
		usage.Upload += upDelta
		usage.Download += downDelta
		t.pendingAppHistory[key] = usage
	}
	t.activeConns = nextConns
}

func saveAppUsage(tx *sql.Tx, pending map[appHistoryKey]appHistoryUsage) error {
	if len(pending) == 0 {
		return nil
	}
	days := make(map[string]struct{}, 2)
	hours := make(map[int64]struct{}, 2)
	for key, usage := range pending {
		if usage.Upload <= 0 && usage.Download <= 0 {
			continue
		}
		if key.Day != "" {
			days[key.Day] = struct{}{}
			if _, err := tx.Exec(`INSERT INTO app_daily (date, app_id, domain, upload, download) VALUES (?, ?, ?, ?, ?)
				ON CONFLICT(date, app_id, domain) DO UPDATE SET upload=upload+excluded.upload, download=download+excluded.download`,
				key.Day, key.AppID, key.Domain, usage.Upload, usage.Download); err != nil {
				return err
			}
		}
		if key.Hour > 0 {
			hours[key.Hour] = struct{}{}
			if _, err := tx.Exec(`INSERT INTO app_hourly (hour, app_id, domain, upload, download) VALUES (?, ?, ?, ?, ?)
				ON CONFLICT(hour, app_id, domain) DO UPDATE SET upload=upload+excluded.upload, download=download+excluded.download`,
				key.Hour, key.AppID, key.Domain, usage.Upload, usage.Download); err != nil {
				return err
			}
		}
	}
	for day := range days {
		if err := pruneDailyAppDomains(tx, day); err != nil {
			return err
		}
	}
	for hour := range hours {
		if err := pruneHourlyAppDomains(tx, hour); err != nil {
			return err
		}
	}
	return nil
}

func pruneDailyAppDomains(tx *sql.Tx, day string) error {
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM app_daily WHERE date=? AND NOT (app_id='other' AND domain='other')`, day).Scan(&count); err != nil {
		return err
	}
	if count <= maxDailyAppDomains {
		return nil
	}
	var tailUpload, tailDownload int64
	if err := tx.QueryRow(`
		SELECT COALESCE(SUM(upload), 0), COALESCE(SUM(download), 0)
		FROM app_daily
		WHERE date=? AND NOT (app_id='other' AND domain='other')
		AND rowid NOT IN (
			SELECT rowid FROM app_daily
			WHERE date=? AND NOT (app_id='other' AND domain='other')
			ORDER BY (upload + download) DESC, app_id ASC, domain ASC
			LIMIT ?
		)`, day, day, maxDailyAppDomains).Scan(&tailUpload, &tailDownload); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		DELETE FROM app_daily
		WHERE date=? AND NOT (app_id='other' AND domain='other')
		AND rowid NOT IN (
			SELECT rowid FROM app_daily
			WHERE date=? AND NOT (app_id='other' AND domain='other')
			ORDER BY (upload + download) DESC, app_id ASC, domain ASC
			LIMIT ?
		)`, day, day, maxDailyAppDomains); err != nil {
		return err
	}
	if tailUpload > 0 || tailDownload > 0 {
		if _, err := tx.Exec(`INSERT INTO app_daily (date, app_id, domain, upload, download) VALUES (?, 'other', 'other', ?, ?)
			ON CONFLICT(date, app_id, domain) DO UPDATE SET upload=upload+excluded.upload, download=download+excluded.download`,
			day, tailUpload, tailDownload); err != nil {
			return err
		}
	}
	return nil
}

func pruneHourlyAppDomains(tx *sql.Tx, hour int64) error {
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM app_hourly WHERE hour=? AND NOT (app_id='other' AND domain='other')`, hour).Scan(&count); err != nil {
		return err
	}
	if count <= maxDailyAppDomains {
		return nil
	}
	var tailUpload, tailDownload int64
	if err := tx.QueryRow(`
		SELECT COALESCE(SUM(upload), 0), COALESCE(SUM(download), 0)
		FROM app_hourly
		WHERE hour=? AND NOT (app_id='other' AND domain='other')
		AND rowid NOT IN (
			SELECT rowid FROM app_hourly
			WHERE hour=? AND NOT (app_id='other' AND domain='other')
			ORDER BY (upload + download) DESC, app_id ASC, domain ASC
			LIMIT ?
		)`, hour, hour, maxDailyAppDomains).Scan(&tailUpload, &tailDownload); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		DELETE FROM app_hourly
		WHERE hour=? AND NOT (app_id='other' AND domain='other')
		AND rowid NOT IN (
			SELECT rowid FROM app_hourly
			WHERE hour=? AND NOT (app_id='other' AND domain='other')
			ORDER BY (upload + download) DESC, app_id ASC, domain ASC
			LIMIT ?
		)`, hour, hour, maxDailyAppDomains); err != nil {
		return err
	}
	if tailUpload > 0 || tailDownload > 0 {
		if _, err := tx.Exec(`INSERT INTO app_hourly (hour, app_id, domain, upload, download) VALUES (?, 'other', 'other', ?, ?)
			ON CONFLICT(hour, app_id, domain) DO UPDATE SET upload=upload+excluded.upload, download=download+excluded.download`,
			hour, tailUpload, tailDownload); err != nil {
			return err
		}
	}
	return nil
}

var ErrAppTrafficPeriod = errors.New("应用流量查询周期无效")

func (t *Tracker) AppTraffic(ctx context.Context, period, date string, tz ...string) (AppTrafficResponse, error) {
	if t.history == nil {
		return AppTrafficResponse{}, ErrHistoryDisabled
	}
	if period == "" {
		period = "day"
	}
	loc := t.history.resolveLocation(firstTZ(tz))
	now := t.now().In(loc)
	var startTime, endTime time.Time
	switch period {
	case "day":
		if date == "" {
			date = now.Format(time.DateOnly)
		}
		parsed, err := time.ParseInLocation(time.DateOnly, date, loc)
		if err != nil || parsed.Format(time.DateOnly) != date {
			return AppTrafficResponse{}, ErrAppTrafficPeriod
		}
		startTime = parsed
		endTime = parsed.AddDate(0, 0, 1)
	case "month":
		if date == "" {
			date = now.Format("2006-01")
		}
		parsed, err := time.ParseInLocation("2006-01", date, loc)
		if err != nil || parsed.Format("2006-01") != date {
			return AppTrafficResponse{}, ErrAppTrafficPeriod
		}
		startTime = parsed
		endTime = parsed.AddDate(0, 1, 0)
	default:
		return AppTrafficResponse{}, ErrAppTrafficPeriod
	}
	startUnix, endUnix := startTime.Unix(), endTime.Unix()

	// Hold persistMu briefly so committed SQLite + pending in-memory deltas
	// never overlap during a save commit.
	t.persistMu.Lock()
	defer t.persistMu.Unlock()

	t.mu.RLock()
	updatedAt := t.state.UpdatedAt
	pendingCopy := make(map[appHistoryKey]appHistoryUsage, len(t.pendingAppHistory))
	for k, v := range t.pendingAppHistory {
		if k.Hour >= startUnix && k.Hour < endUnix {
			pendingCopy[k] = v
		}
	}
	t.mu.RUnlock()

	type domainKey struct {
		appID  string
		domain string
	}
	merged := make(map[domainKey]appHistoryUsage)

	rows, err := t.history.db.QueryContext(ctx, `
		SELECT app_id, domain, SUM(upload), SUM(download)
		FROM app_hourly
		WHERE hour >= ? AND hour < ?
		GROUP BY app_id, domain`, startUnix, endUnix)
	if err != nil {
		return AppTrafficResponse{}, err
	}
	for rows.Next() {
		var appID, domain string
		var up, down int64
		if err := rows.Scan(&appID, &domain, &up, &down); err != nil {
			rows.Close()
			return AppTrafficResponse{}, err
		}
		appID, domain = reclassifyStoredDomain(appID, domain)
		dk := domainKey{appID: appID, domain: domain}
		cur := merged[dk]
		cur.Upload += up
		cur.Download += down
		merged[dk] = cur
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return AppTrafficResponse{}, err
	}
	rows.Close()

	for k, v := range pendingCopy {
		appID, domain := reclassifyStoredDomain(k.AppID, k.Domain)
		dk := domainKey{appID: appID, domain: domain}
		cur := merged[dk]
		cur.Upload += v.Upload
		cur.Download += v.Download
		merged[dk] = cur
	}

	appMap := make(map[string]*AppUsage)
	var totalUp, totalDown int64
	for dk, usage := range merged {
		total := usage.Upload + usage.Download
		if total <= 0 {
			continue
		}
		totalUp += usage.Upload
		totalDown += usage.Download
		app, ok := appMap[dk.appID]
		if !ok {
			name, category := resolveAppMeta(dk.appID)
			app = &AppUsage{
				ID:       dk.appID,
				Name:     name,
				Category: category,
				Domains:  []AppDomainUsage{},
			}
			appMap[dk.appID] = app
		}
		app.Upload += usage.Upload
		app.Download += usage.Download
		app.Total += total
		app.Domains = append(app.Domains, AppDomainUsage{
			Domain:   dk.domain,
			Upload:   usage.Upload,
			Download: usage.Download,
			Total:    total,
		})
	}

	apps := make([]AppUsage, 0, len(appMap))
	for _, app := range appMap {
		sort.Slice(app.Domains, func(i, j int) bool {
			if app.Domains[i].Total != app.Domains[j].Total {
				return app.Domains[i].Total > app.Domains[j].Total
			}
			return app.Domains[i].Domain < app.Domains[j].Domain
		})
		if len(app.Domains) > 100 {
			app.Domains = app.Domains[:100]
		}
		apps = append(apps, *app)
	}
	sort.Slice(apps, func(i, j int) bool {
		if apps[i].Total != apps[j].Total {
			return apps[i].Total > apps[j].Total
		}
		return apps[i].Name < apps[j].Name
	})
	if len(apps) > 200 {
		apps = apps[:200]
	}

	return AppTrafficResponse{
		Period:        period,
		Date:          date,
		Timezone:      loc.String(),
		UpdatedAt:     updatedAt,
		TotalUpload:   totalUp,
		TotalDownload: totalDown,
		TotalBytes:    totalUp + totalDown,
		Apps:          apps,
	}, nil
}

func (t *Tracker) ClearAppTraffic(ctx context.Context) error {
	if t.history == nil {
		return ErrHistoryDisabled
	}
	t.persistMu.Lock()
	defer t.persistMu.Unlock()

	if _, err := t.history.db.ExecContext(ctx, `DELETE FROM app_daily; DELETE FROM app_hourly;`); err != nil {
		return err
	}
	t.mu.Lock()
	clear(t.pendingAppHistory)
	t.mu.Unlock()
	return nil
}
