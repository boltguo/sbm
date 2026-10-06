package geo

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"time"
	"unicode"

	"github.com/boltguo/sbm/internal/model"
)

// LocationName follows install.sh's location_node_name: uppercase country
// code, city retained with a code (SG-Singapore), and whitespace removed.
func LocationName(country, code, city string) string {
	location := country
	if code != "" {
		location = strings.ToUpper(code)
	}
	if city != "" && city != location && (code != "" || city != country) {
		location += "-" + city
	}
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, location)
}

type Lookup interface {
	Lookup(context.Context, string) (model.GatewayGeo, error)
}
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func (c Client) Lookup(ctx context.Context, ip string) (model.GatewayGeo, error) {
	addr, err := netip.ParseAddr(ip)
	if err != nil || !addr.Is4() {
		return model.GatewayGeo{}, errors.New("invalid geolocation address")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	base := c.BaseURL
	if base == "" {
		base = "https://ipwho.is/"
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/"+addr.String(), nil)
	if err != nil {
		return model.GatewayGeo{}, errors.New("geolocation unavailable")
	}
	resp, err := client.Do(req)
	if err != nil {
		return model.GatewayGeo{}, errors.New("geolocation unavailable")
	}
	defer resp.Body.Close()
	var data struct {
		Success     bool   `json:"success"`
		IP          string `json:"ip"`
		CountryCode string `json:"country_code"`
		Country     string `json:"country"`
		Region      string `json:"region"`
		City        string `json:"city"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 32<<10)).Decode(&data) != nil || !data.Success || data.IP != addr.String() || len(data.CountryCode) != 2 {
		return model.GatewayGeo{}, errors.New("geolocation unavailable")
	}
	return model.GatewayGeo{IP: addr.String(), CountryCode: strings.ToUpper(data.CountryCode), Country: data.Country, Region: data.Region, City: data.City, UpdatedAt: time.Now().UTC()}, nil
}
