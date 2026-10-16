package traffic

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/boltguo/sbm/internal/model"
)

func TestClassifyConnection(t *testing.T) {
	tests := []struct {
		name       string
		meta       ClashConnectionMeta
		wantApp    string
		wantDomain string
	}{
		{
			name:       "YouTube CDN subdomain collapsed to eTLD+1",
			meta:       ClashConnectionMeta{Host: "rr3---sn-ab5l6nzy.googlevideo.com:443", Network: "udp"},
			wantApp:    "youtube",
			wantDomain: "googlevideo.com",
		},
		{
			name:       "YouTube API on googleapis subdomain",
			meta:       ClashConnectionMeta{Host: "youtubei.googleapis.com", Network: "tcp"},
			wantApp:    "youtube",
			wantDomain: "youtubei.googleapis.com",
		},
		{
			name:       "Google Gemini subdomain",
			meta:       ClashConnectionMeta{Host: "generativelanguage.googleapis.com", Network: "tcp"},
			wantApp:    "gemini",
			wantDomain: "generativelanguage.googleapis.com",
		},
		{
			name:       "Google Antigravity backend subdomain",
			meta:       ClashConnectionMeta{Host: "antigravity-pa.googleapis.com", Network: "tcp"},
			wantApp:    "gemini",
			wantDomain: "antigravity-pa.googleapis.com",
		},
		{
			name:       "OpenAI API / Codex backend subdomain",
			meta:       ClashConnectionMeta{Host: "api.openai.com", Network: "tcp"},
			wantApp:    "openai",
			wantDomain: "api.openai.com",
		},
		{
			name:       "OpenAI ChatGPT / Codex WebSocket subdomain",
			meta:       ClashConnectionMeta{Host: "ws.chatgpt.com", Network: "tcp"},
			wantApp:    "openai",
			wantDomain: "ws.chatgpt.com",
		},
		{
			name:       "GitHub Copilot proxy on githubusercontent",
			meta:       ClashConnectionMeta{Host: "copilot-proxy.githubusercontent.com", Network: "tcp"},
			wantApp:    "github-copilot",
			wantDomain: "copilot-proxy.githubusercontent.com",
		},
		{
			name:       "Google shared ecosystem",
			meta:       ClashConnectionMeta{Host: " Play-lh.googleusercontent.com. ", Network: "tcp"},
			wantApp:    "ecosystem:google",
			wantDomain: "googleusercontent.com",
		},
		{
			name:       "Telegram direct IPv4 without domain",
			meta:       ClashConnectionMeta{DestinationIP: "149.154.167.50", DestinationPort: "443", Network: "tcp"},
			wantApp:    "telegram",
			wantDomain: "Telegram IP (MTProto)",
		},
		{
			name:       "Telegram direct IPv6 without domain",
			meta:       ClashConnectionMeta{DestinationIP: "2001:b28:f23d:f001::a", DestinationPort: "443", Network: "tcp"},
			wantApp:    "telegram",
			wantDomain: "Telegram IP (MTProto)",
		},
		{
			name:       "Independent site collapses private subdomain to eTLD+1",
			meta:       ClashConnectionMeta{Host: "user-secret-123.sub.example.com", Network: "tcp"},
			wantApp:    "site:example.com",
			wantDomain: "example.com",
		},
		{
			name:       "Multi-label ccTLD co.uk",
			meta:       ClashConnectionMeta{Host: "vod.media.bbc.co.uk", Network: "tcp"},
			wantApp:    "site:bbc.co.uk",
			wantDomain: "bbc.co.uk",
		},
		{
			name:       "Unknown direct IP does not leak raw IP address",
			meta:       ClashConnectionMeta{DestinationIP: "198.51.100.42", DestinationPort: "443", Network: "udp"},
			wantApp:    "ip:udp",
			wantDomain: "UDP/443",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotApp, gotDomain := ClassifyConnection(tc.meta)
			if gotApp != tc.wantApp || gotDomain != tc.wantDomain {
				t.Fatalf("ClassifyConnection(%+v) = (%q, %q), want (%q, %q)", tc.meta, gotApp, gotDomain, tc.wantApp, tc.wantDomain)
			}
			if strings.Contains(gotDomain, "198.51.100.42") || strings.Contains(gotDomain, "user-secret-123") {
				t.Fatalf("sensitive subdomain or raw IP leaked into domain key: %q", gotDomain)
			}
		})
	}
}

func TestAppTrafficDeltaPersistencePanelRestartAndClear(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	cfg := model.DefaultConfig()
	cfg.Reset.Timezone = "UTC"
	tracker, statePath, dbPath, source := historyTracker(t, model.DefaultState(now), cfg, &now)

	payloads := []string{
		`{
			"uploadTotal": 150,
			"downloadTotal": 1200,
			"connections": [
				{"id": "c1", "upload": 100, "download": 1000, "metadata": {"network": "udp", "host": "rr1---sn-xxx.googlevideo.com", "destinationPort": "443"}},
				{"id": "c2", "upload": 50, "download": 200, "metadata": {"network": "tcp", "destinationIP": "149.154.167.99", "destinationPort": "443"}}
			]
		}`,
		`{
			"uploadTotal": 320,
			"downloadTotal": 3700,
			"connections": [
				{"id": "c1", "upload": 250, "download": 3000, "metadata": {"network": "udp", "host": "rr1---sn-xxx.googlevideo.com", "destinationPort": "443"}},
				{"id": "c3", "upload": 20, "download": 500, "metadata": {"network": "tcp", "host": "i.ytimg.com", "destinationPort": "443"}}
			]
		}`,
		`{
			"uploadTotal": 320,
			"downloadTotal": 3700,
			"connections": [
				{"id": "c1", "upload": 250, "download": 3000, "metadata": {"network": "udp", "host": "rr1---sn-xxx.googlevideo.com", "destinationPort": "443"}}
			]
		}`,
		`{
			"uploadTotal": 370,
			"downloadTotal": 4700,
			"connections": [
				{"id": "c1", "upload": 300, "download": 4000, "metadata": {"network": "udp", "host": "rr1---sn-xxx.googlevideo.com", "destinationPort": "443"}}
			]
		}`,
	}
	step := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(payloads[step]))
		step++
	}))
	defer srv.Close()
	client := ClashClient{URL: srv.URL}

	// Sample 1 & 2 before persist: verify real-time pending query works immediately.
	if _, err := tracker.Sample(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if _, err := tracker.Sample(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	res, err := tracker.AppTraffic(context.Background(), "day", "2026-10-16")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Apps) != 2 || res.TotalUpload != 320 || res.TotalDownload != 3700 {
		t.Fatalf("unexpected app traffic before persist: %+v", res)
	}
	if res.Apps[0].ID != "youtube" || res.Apps[0].Upload != 270 || res.Apps[0].Download != 3500 || len(res.Apps[0].Domains) != 2 {
		t.Fatalf("unexpected YouTube aggregation: %+v", res.Apps[0])
	}
	if res.Apps[1].ID != "telegram" || res.Apps[1].Upload != 50 || res.Apps[1].Download != 200 {
		t.Fatalf("unexpected Telegram aggregation: %+v", res.Apps[1])
	}

	// Persist and simulate panel restart while sing-box keeps c1 open.
	if err := tracker.Persist(); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenWithHistory(statePath, dbPath, source, &fakeCore{running: true}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()

	// Sample 3: c1 is still at 250/3000; panel restart must seed c1 and not double-count.
	now = now.Add(time.Second)
	if _, err := reopened.Sample(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	// Sample 4: c1 grows to 300/4000 (+50/+1000).
	now = now.Add(time.Second)
	if _, err := reopened.Sample(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Persist(); err != nil {
		t.Fatal(err)
	}

	monthly, err := reopened.AppTraffic(context.Background(), "month", "2026-10")
	if err != nil {
		t.Fatal(err)
	}
	if len(monthly.Apps) != 2 || monthly.Apps[0].ID != "youtube" || monthly.Apps[0].Upload != 320 || monthly.Apps[0].Download != 4500 {
		t.Fatalf("panel restart double-counted or lost c1 delta: %+v", monthly)
	}

	// Clear app traffic and verify daily/plan traffic remains intact.
	if err := reopened.ClearAppTraffic(context.Background()); err != nil {
		t.Fatal(err)
	}
	cleared, err := reopened.AppTraffic(context.Background(), "day", "2026-10-16")
	if err != nil {
		t.Fatal(err)
	}
	if len(cleared.Apps) != 0 || cleared.TotalBytes != 0 {
		t.Fatalf("expected cleared app traffic, got %+v", cleared)
	}
	if reopened.State().Total() != 5070 {
		t.Fatalf("clearing app stats altered plan usage: %d", reopened.State().Total())
	}
}

func TestAppTrafficDailyTailPruning(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	tracker, _, _, _ := historyTracker(t, model.DefaultState(now), model.DefaultConfig(), &now)

	conns := make([]ClashConnection, 0, 210)
	var wantTotal int64
	for i := 1; i <= 210; i++ {
		down := int64(i * 100)
		wantTotal += down
		conns = append(conns, ClashConnection{
			ID:       fmt.Sprintf("conn-%d", i),
			Download: down,
			Metadata: ClashConnectionMeta{Host: fmt.Sprintf("sub.site-%03d.example", i), Network: "tcp"},
		})
	}
	if _, err := tracker.applySampleWithConns(context.Background(), 0, wantTotal, "", conns); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Persist(); err != nil {
		t.Fatal(err)
	}

	var rowCount int
	if err := tracker.history.db.QueryRow(`SELECT COUNT(*) FROM app_daily WHERE date='2026-10-16'`).Scan(&rowCount); err != nil {
		t.Fatal(err)
	}
	// 200 top rows + 1 "other" row = 201 rows max.
	if rowCount != maxDailyAppDomains+1 {
		t.Fatalf("rowCount = %d, want %d", rowCount, maxDailyAppDomains+1)
	}
	var sumDown int64
	if err := tracker.history.db.QueryRow(`SELECT SUM(download) FROM app_daily WHERE date='2026-10-16'`).Scan(&sumDown); err != nil {
		t.Fatal(err)
	}
	if sumDown != wantTotal {
		t.Fatalf("sumDown = %d, want %d", sumDown, wantTotal)
	}
}

func TestCustomAppRulesJSON(t *testing.T) {
	t.Cleanup(func() {
		if err := LoadAppRules(defaultAppRulesJSON); err != nil {
			t.Fatal(err)
		}
	})
	custom := []byte(`{
		"rules": [
			{
				"id": "emby",
				"name": "Emby Media",
				"category": "streaming",
				"domains": ["emby.media", "my-emby.example.com"],
				"cidrs": ["203.0.113.0/24"],
				"ipLabel": "Emby Server IP"
			}
		],
		"multiLabelSuffixes": ["co.uk"]
	}`)
	if err := LoadAppRules(custom); err != nil {
		t.Fatal(err)
	}
	appID, dom := ClassifyConnection(ClashConnectionMeta{Host: "stream1.emby.media", Network: "tcp"})
	if appID != "emby" || dom != "emby.media" {
		t.Fatalf("got (%q, %q), want (emby, emby.media)", appID, dom)
	}
	appID, dom = ClassifyConnection(ClashConnectionMeta{DestinationIP: "203.0.113.88", DestinationPort: "8096", Network: "tcp"})
	if appID != "emby" || dom != "Emby Server IP" {
		t.Fatalf("got (%q, %q), want (emby, Emby Server IP)", appID, dom)
	}
	name, cat := resolveAppMeta("emby")
	if name != "Emby Media" || cat != "streaming" {
		t.Fatalf("resolveAppMeta(emby) = (%q, %q)", name, cat)
	}
}

func TestBrowserTimezoneViewingWithUTCBillingReset(t *testing.T) {
	// 2026-10-15 17:00 UTC is 2026-10-16 01:00 Asia/Shanghai (1 AM Beijing time on Oct 16).
	now := time.Date(2026, 10, 15, 17, 0, 0, 0, time.UTC)
	cfg := model.DefaultConfig()
	cfg.Reset = model.ResetConfig{Mode: "monthly", Day: 1, Timezone: "UTC"}
	tracker, _, _, _ := historyTracker(t, model.DefaultState(now), cfg, &now)

	// Sample 1 at 2026-10-15 17:00 UTC (2026-10-16 01:00 CST): 100 upload / 400 download (500 total).
	conns1 := []ClashConnection{
		{ID: "c1", Upload: 100, Download: 400, Metadata: ClashConnectionMeta{Host: "api.openai.com", Network: "tcp"}},
	}
	if _, err := tracker.applySampleWithConns(context.Background(), 100, 400, "", conns1); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Persist(); err != nil {
		t.Fatal(err)
	}

	// Sample 2 at 2026-10-16 10:00 UTC (2026-10-16 18:00 CST): +200 upload / +600 download (+800 total).
	now = time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	conns2 := []ClashConnection{
		{ID: "c1", Upload: 300, Download: 1000, Metadata: ClashConnectionMeta{Host: "api.openai.com", Network: "tcp"}},
	}
	if _, err := tracker.applySampleWithConns(context.Background(), 300, 1000, "", conns2); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Persist(); err != nil {
		t.Fatal(err)
	}

	// 1. In default UTC billing timezone, 2026-10-16 only has the 10:00 UTC sample (800 bytes).
	utcHist, err := tracker.History(context.Background(), "day", "2026-10-15", "2026-10-16")
	if err != nil {
		t.Fatal(err)
	}
	if utcHist.Timezone != "UTC" || len(utcHist.Rows) != 2 || utcHist.Rows[0].ProxyUsedBytes != 500 || utcHist.Rows[1].ProxyUsedBytes != 800 {
		t.Fatalf("unexpected UTC history: %+v", utcHist)
	}

	// 2. When viewed in browser timezone Asia/Shanghai (CST), both samples belong to 2026-10-16 (1300 bytes).
	cstHist, err := tracker.History(context.Background(), "day", "2026-10-15", "2026-10-16", "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	if cstHist.Timezone != "Asia/Shanghai" || len(cstHist.Rows) != 1 || cstHist.Rows[0].Date != "2026-10-16" || cstHist.Rows[0].ProxyUsedBytes != 1300 {
		t.Fatalf("unexpected Asia/Shanghai history: %+v", cstHist)
	}

	// 3. AppTraffic in Asia/Shanghai for 2026-10-16 also includes both samples (300 upload + 1000 download = 1300 bytes).
	cstApps, err := tracker.AppTraffic(context.Background(), "day", "2026-10-16", "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	if cstApps.Timezone != "Asia/Shanghai" || cstApps.TotalBytes != 1300 || len(cstApps.Apps) != 1 || cstApps.Apps[0].ID != "openai" {
		t.Fatalf("unexpected Asia/Shanghai app traffic: %+v", cstApps)
	}
}
