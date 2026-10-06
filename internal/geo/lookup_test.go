package geo

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInstallerLocationNaming(t *testing.T) {
	for _, c := range []struct{ country, code, city, want string }{
		{"United States", "US", "Boardman", "US-Boardman"}, {"Japan", "jp", "Tokyo", "JP-Tokyo"}, {"Singapore", "SG", "Singapore", "SG-Singapore"}, {"United States", "US", "Los Angeles", "US-LosAngeles"}, {"Singapore", "", "Singapore", "Singapore"}, {"Japan", "", "Tokyo", "Japan-Tokyo"}, {"", "JP", "", "JP"},
	} {
		if got := LocationName(c.country, c.code, c.city); got != c.want {
			t.Errorf("name=%q want=%q", got, c.want)
		}
	}
}
func TestLookupSpecifiedIP(t *testing.T) {
	for _, c := range []struct {
		body   string
		status int
		ok     bool
	}{
		{`{"success":true,"ip":"203.0.113.1","country_code":"us","country":"United States","region":"Oregon","city":"Boardman"}`, 200, true},
		{`{"success":false}`, 200, false}, {`{"success":true,"ip":"203.0.113.2","country_code":"US"}`, 200, false}, {`bad json`, 200, false}, {`{}`, 429, false},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/203.0.113.1" {
				t.Errorf("path=%s", r.URL.Path)
			}
			w.WriteHeader(c.status)
			fmt.Fprint(w, c.body)
		}))
		g, err := (Client{BaseURL: server.URL}).Lookup(context.Background(), "203.0.113.1")
		server.Close()
		if (err == nil) != c.ok {
			t.Fatalf("lookup success=%v want=%v", err == nil, c.ok)
		}
		if c.ok && (g.CountryCode != "US" || g.City != "Boardman" || g.UpdatedAt.IsZero()) {
			t.Fatalf("invalid detected geo: %+v", g)
		}
	}
}
