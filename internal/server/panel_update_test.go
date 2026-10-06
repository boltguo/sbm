package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/boltguo/sbm/internal/panelupdate"
	"github.com/boltguo/sbm/internal/releasecheck"
)

type fakeUpdater struct {
	target string
	calls  int
	err    error
	status panelupdate.Status
}

func (u *fakeUpdater) Start(_ context.Context, target string) error {
	u.target = target
	u.calls++
	return u.err
}
func (u *fakeUpdater) Status(context.Context) (panelupdate.Status, error) { return u.status, nil }
func TestPanelUpdateRequiresAuthenticationAndCSRF(t *testing.T) {
	s, _ := testServer(t)
	u := &fakeUpdater{}
	s.Updater = u
	s.Releases = &fakeReleases{info: releasecheck.Info{TagName: "v2.1.1"}}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		route := "/api/update/status"
		if method == http.MethodPost {
			route = "/api/update"
		}
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, httptest.NewRequest(method, route, strings.NewReader("{}")))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s: %d", method, response.Code)
		}
	}
	request := authenticatedRequest(t, s, http.MethodPost, "/api/update", map[string]any{})
	request.Header.Del("X-CSRF-Token")
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || u.calls != 0 {
		t.Fatalf("CSRF bypass: %d", response.Code)
	}
}
func TestPanelUpdatePinsServerReleaseAndReportsPersistedProgress(t *testing.T) {
	s, _ := testServer(t)
	u := &fakeUpdater{status: panelupdate.Status{State: "running", Phase: "restarting", TargetVersion: "v2.1.1"}}
	s.Updater = u
	releases := &fakeReleases{info: releasecheck.Info{TagName: "v2.1.1"}}
	s.Releases = releases
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, authenticatedRequest(t, s, http.MethodPost, "/api/update", map[string]any{}))
	if response.Code != http.StatusAccepted || u.target != "v2.1.1" || releases.calls != 1 {
		t.Fatalf("launch: %d %s %s", response.Code, u.target, response.Body.String())
	}
	response = httptest.NewRecorder()
	s.Handler().ServeHTTP(response, authenticatedRequest(t, s, http.MethodGet, "/api/update/status", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"phase":"restarting"`) {
		t.Fatalf("progress: %s", response.Body.String())
	}
	response = httptest.NewRecorder()
	s.Handler().ServeHTTP(response, authenticatedRequest(t, s, http.MethodPost, "/api/update", map[string]any{"command": "reboot", "version": "v9.9.9"}))
	if response.Code != http.StatusBadRequest || u.calls != 1 {
		t.Fatalf("arbitrary command accepted: %d", response.Code)
	}
}
func TestPanelUpdateRejectsNoUpdateBusyAndFailedLaunch(t *testing.T) {
	for _, tc := range []struct {
		name, tag string
		err       error
		code      int
		calls     int
	}{
		{"latest", "v0.1.0", nil, http.StatusConflict, 0},
		{"busy", "v2.1.1", panelupdate.ErrBusy, http.StatusConflict, 1},
		{"failed", "v2.1.1", errors.New("secret command output"), http.StatusServiceUnavailable, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := testServer(t)
			u := &fakeUpdater{err: tc.err}
			s.Updater = u
			s.Releases = &fakeReleases{info: releasecheck.Info{TagName: tc.tag}}
			response := httptest.NewRecorder()
			s.Handler().ServeHTTP(response, authenticatedRequest(t, s, http.MethodPost, "/api/update", map[string]any{}))
			if response.Code != tc.code || u.calls != tc.calls || strings.Contains(response.Body.String(), "secret command output") {
				t.Fatalf("%d %s", response.Code, response.Body.String())
			}
		})
	}
}
