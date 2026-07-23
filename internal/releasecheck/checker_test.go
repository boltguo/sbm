package releasecheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGitHubLatest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("User-Agent") != "sbm-panel" || r.Header.Get("X-GitHub-Api-Version") == "" {
			t.Fatalf("missing GitHub headers: %#v", r.Header)
		}
		_, _ = w.Write([]byte(`{"tag_name":"v0.2.0","html_url":"https://github.com/boltguo/sbm/releases/tag/v0.2.0"}`))
	}))
	defer server.Close()

	checker := &GitHub{Client: server.Client(), Endpoint: server.URL}
	info, err := checker.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.TagName != "v0.2.0" || info.URL != "https://github.com/boltguo/sbm/releases/tag/v0.2.0" {
		t.Fatalf("unexpected release info %#v", info)
	}
}

func TestGitHubLatestRejectsBadResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer server.Close()

	if _, err := (&GitHub{Client: server.Client(), Endpoint: server.URL}).Latest(context.Background()); err == nil {
		t.Fatal("expected GitHub error")
	}
}

func TestIsNewer(t *testing.T) {
	tests := []struct {
		latest, current string
		want            bool
	}{
		{"v0.2.0", "0.1.0", true},
		{"v0.10.0", "v0.9.9", true},
		{"v1.0.0", "v1.0.0-beta.1", true},
		{"v1.0.0", "v1.0.0", false},
		{"v0.9.0", "v1.0.0", false},
		{"v1.0.0", "dev", false},
	}
	for _, test := range tests {
		if got := IsNewer(test.latest, test.current); got != test.want {
			t.Errorf("IsNewer(%q, %q)=%v, want %v", test.latest, test.current, got, test.want)
		}
	}
}
