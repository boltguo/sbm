package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/boltguo/sbm/internal/model"
	"github.com/boltguo/sbm/internal/nettraffic"
	"github.com/boltguo/sbm/internal/traffic"
)

type serverNetworkReader struct{ snapshot nettraffic.Snapshot }

func (r serverNetworkReader) Read(context.Context, nettraffic.Request) (nettraffic.Snapshot, error) {
	return r.snapshot, nil
}
func TestVnStatDashboardHistoryGatewayAndSubscription(t *testing.T) {
	s, _ := testServer(t)
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	cfg := s.Config.Get()
	cfg.VnStatInterface = "ens5"
	cfg.Reset = model.ResetConfig{Mode: "monthly", Day: 1, Timezone: "UTC"}
	cfg.TrafficQuota = model.TrafficQuotaConfig{Amount: 1, Unit: "GB", BillingMode: "single", HeadroomPercent: 10}
	cfg.EgressGateways = []model.EgressGateway{{ID: "exit-1", Enabled: true, Server: "example.com", TrafficQuota: cfg.TrafficQuota, Reset: cfg.Reset}}
	if err := s.Config.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	tracker, err := traffic.OpenWithHistory(filepath.Join(dir, "state.json"), filepath.Join(dir, "traffic.db"), s.Config, nil, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer tracker.Close()
	s.Traffic = tracker
	tracker.NetworkReader = serverNetworkReader{nettraffic.Snapshot{Interface: "ens5", CreatedAt: now.Add(-time.Hour), UpdatedAt: now, RX: 100, TX: 200, Buckets: []nettraffic.Bucket{{Start: now.Add(-time.Hour).Unix(), Seconds: 3600, RX: 100, TX: 200}}}}
	for _, id := range []string{"entry"} {
		if err := tracker.SampleNetwork(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tracker.ApplySample(context.Background(), 10, 20); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Persist(); err != nil {
		t.Fatal(err)
	}
	get := func(url string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		s.Handler().ServeHTTP(r, authenticatedRequest(t, s, http.MethodGet, url, nil))
		if r.Code != 200 {
			t.Fatalf("%s: %d %s", url, r.Code, r.Body.String())
		}
		return r
	}
	dashboard := get("/api/dashboard")
	var view struct {
		Upload, Download, ProxyUsedBytes, EstimatedProviderUsedBytes, ProviderStopBytes int64
		TrafficSource                                                                   string
		EgressGateways                                                                  []gatewayUsageView
	}
	if err := json.Unmarshal(dashboard.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.TrafficSource != "vnstat" || view.Upload != 200 || view.Download != 100 || view.ProxyUsedBytes != 30 || view.EstimatedProviderUsedBytes != 200 || view.ProviderStopBytes != 900_000_000 {
		t.Fatalf("dashboard mixed counters: %+v", view)
	}
	if len(view.EgressGateways) != 1 || view.EgressGateways[0].EstimatedProviderUsedBytes != 0 {
		t.Fatalf("gateway estimate was changed by entry vnStat: %+v", view)
	}
	for _, scope := range []string{"entry"} {
		response := get("/api/traffic/history?scope=" + scope + "&from=2026-10-06&to=2026-10-06")
		var h traffic.HistoryResponse
		if err := json.Unmarshal(response.Body.Bytes(), &h); err != nil {
			t.Fatal(err)
		}
		if h.Source != "vnstat" || len(h.Rows) != 1 || h.Rows[0].NetworkTotal != 300 {
			t.Fatalf("%s history=%+v", scope, h)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/sub/"+cfg.SubscriptionToken, nil)
	result := httptest.NewRecorder()
	s.Handler().ServeHTTP(result, request)
	if result.Code != 200 || !strings.Contains(result.Header().Get("Subscription-Userinfo"), "upload=200; download=0; total=900000000;") {
		t.Fatalf("subscription used old proxy estimate: %d %s", result.Code, result.Header().Get("Subscription-Userinfo"))
	}
}
