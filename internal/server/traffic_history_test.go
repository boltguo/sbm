package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/boltguo/sbm/internal/traffic"
)

func TestTrafficHistoryAPIAuthenticationRangeAndTotals(t *testing.T) {
	s, _ := testServer(t)
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	tracker, err := traffic.OpenWithHistory(filepath.Join(dir, "state.json"), filepath.Join(dir, "traffic.db"), s.Config, s.Core, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer tracker.Close()
	s.Traffic = tracker
	if _, err := tracker.ApplySample(context.Background(), 100, 200); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Persist(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		url    string
		auth   bool
		status int
	}{
		{"/api/traffic/history", false, http.StatusUnauthorized},
		{"/api/traffic/history?granularity=month&from=2026-10&to=2026-10", true, http.StatusOK},
		{"/api/traffic/history?granularity=hour", true, http.StatusBadRequest},
		{"/api/traffic/history?from=2026-02-30", true, http.StatusBadRequest},
	} {
		req := httptest.NewRequest(http.MethodGet, test.url, nil)
		if test.auth {
			req = authenticatedRequest(t, s, http.MethodGet, test.url, nil)
		}
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, req)
		if response.Code != test.status {
			t.Fatalf("%s: status %d, body %s", test.url, response.Code, response.Body.String())
		}
		if test.status == http.StatusOK {
			var result traffic.HistoryResponse
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Rows) != 1 || result.Rows[0].ProxyUsedBytes != 300 {
				t.Fatalf("wrong monthly API total: %+v", result)
			}
		}
	}
}
