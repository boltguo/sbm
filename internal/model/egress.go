package model

import (
	"fmt"
	"sort"
	"time"
)

// TunnelSlot is persisted, so removing or reordering another gateway never
// changes the address the operator has configured on this gateway's B server.
type EgressGateway struct {
	ID               string             `json:"id"`
	Enabled          bool               `json:"enabled"`
	Marker           string             `json:"marker"`
	Position         int                `json:"position"`
	Server           string             `json:"server"`
	ServerPort       int                `json:"serverPort"`
	PrivateKey       string             `json:"privateKey"`
	PeerPublicKey    string             `json:"peerPublicKey"`
	TunnelSlot       int                `json:"tunnelSlot"`
	Geo              GatewayGeo         `json:"geo"`
	LocationOverride string             `json:"locationOverride"`
	TrafficQuota     TrafficQuotaConfig `json:"trafficQuota"`
	Reset            ResetConfig        `json:"reset"`
	// StopOnQuota cuts off this gateway alone once its estimated provider
	// usage reaches the stop point; other exits and sing-box keep running.
	StopOnQuota bool `json:"stopOnQuota,omitempty"`
}

type GatewayGeo struct {
	IP          string    `json:"ip"`
	CountryCode string    `json:"countryCode"`
	Country     string    `json:"country"`
	Region      string    `json:"region"`
	City        string    `json:"city"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type EgressCredential struct {
	GatewayID string `json:"gatewayId"`
	UUID      string `json:"uuid,omitempty"`
	Password  string `json:"password,omitempty"`
}

func (g EgressGateway) EndpointTag() string   { return "egress-wg-" + g.ID }
func (g EgressGateway) DNSTag() string        { return "dns-" + g.EndpointTag() }
func (g EgressGateway) TunnelAddress() string { return fmt.Sprintf("10.66.%d.2/32", g.TunnelSlot) }
func (g EgressGateway) PeerAddress() string   { return fmt.Sprintf("10.66.%d.1/24", g.TunnelSlot) }

func OrderedGateways(gateways []EgressGateway) []EgressGateway {
	result := append([]EgressGateway(nil), gateways...)
	sort.SliceStable(result, func(i, j int) bool { return result[i].Position < result[j].Position })
	return result
}

// Counters describe encrypted UDP packets observed by the entry host, not a
// cloud provider invoice. They include tunnel overhead and keepalives.
type GatewayTrafficState struct {
	TX              int64       `json:"tx"`
	RX              int64       `json:"rx"`
	LastTX          int64       `json:"lastTX"`
	LastRX          int64       `json:"lastRX"`
	TXGeneration    string      `json:"txGeneration,omitempty"`
	RXGeneration    string      `json:"rxGeneration,omitempty"`
	Peer            string      `json:"peer,omitempty"`
	Initialized     bool        `json:"initialized"`
	PeriodStartedAt time.Time   `json:"periodStartedAt"`
	NextResetAt     time.Time   `json:"nextResetAt,omitempty"`
	Reset           ResetConfig `json:"reset"`
	Status          string      `json:"status"`
	LastSuccessAt   time.Time   `json:"lastSuccessAt,omitempty"`
	FailureSince    time.Time   `json:"failureSince,omitempty"`
	Partial         bool        `json:"partial,omitempty"`
	// Adjustment calibrates the estimate to the provider dashboard, which
	// also counts the gateway host's own traffic.
	Adjustment UsageAdjustment `json:"adjustment,omitzero"`
}

// ProviderUsage estimates the gateway provider's billed bytes for the period.
func (s GatewayTrafficState) ProviderUsage(q TrafficQuotaConfig) int64 {
	tunnel := s.TX
	if s.RX > (1<<63-1)-tunnel {
		tunnel = 1<<63 - 1
	} else {
		tunnel += s.RX
	}
	factor := q.ProviderUsageFactor()
	used := tunnel
	if tunnel > (1<<63-1)/factor {
		used = 1<<63 - 1
	} else {
		used = tunnel * factor
	}
	return s.Adjustment.Apply(used, q.BillingMode)
}
