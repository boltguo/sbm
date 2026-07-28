package model

import "time"

const (
	ConfigVersion = 1
	StateVersion  = 1

	WireGuardExitLocalAddress        = "10.66.0.2/32"
	WireGuardExitMTU                 = 1408
	WireGuardExitPersistentKeepalive = 25

	OutboundStrategyAuto       = "auto"
	OutboundStrategyPreferIPv4 = "prefer_ipv4"
	OutboundStrategyPreferIPv6 = "prefer_ipv6"
	OutboundStrategyIPv4Only   = "ipv4_only"
	OutboundStrategyIPv6Only   = "ipv6_only"
)

type Config struct {
	Version           int                  `json:"version"`
	Domain            string               `json:"domain"`
	PanelPort         int                  `json:"panelPort"`
	AdminUsername     string               `json:"adminUsername"`
	AdminPasswordHash string               `json:"adminPasswordHash"`
	SessionSecret     string               `json:"sessionSecret"`
	ClashAPISecret    string               `json:"clashAPISecret"`
	SubscriptionToken string               `json:"subscriptionToken"`
	TotalBytes        int64                `json:"totalBytes"`
	Reset             ResetConfig          `json:"reset"`
	OutboundStrategy  string               `json:"outboundStrategy,omitempty"`
	WireGuardExit     *WireGuardExitConfig `json:"wireGuardExit,omitempty"`
	Inbounds          []Inbound            `json:"inbounds"`
}

type ResetConfig struct {
	Mode     string `json:"mode"`
	Day      int    `json:"day"`
	Timezone string `json:"timezone"`
}

type WireGuardExitConfig struct {
	Enabled       bool   `json:"enabled"`
	Label         string `json:"label"`
	Server        string `json:"server"`
	ServerPort    int    `json:"serverPort"`
	PrivateKey    string `json:"privateKey"`
	PeerPublicKey string `json:"peerPublicKey"`
}

type Inbound struct {
	ID        string            `json:"id"`
	Type      string            `json:"type"`
	Name      string            `json:"name"`
	Enabled   bool              `json:"enabled"`
	Port      int               `json:"port"`
	VLESS     *VLESSOptions     `json:"vless,omitempty"`
	Hysteria2 *Hysteria2Options `json:"hysteria2,omitempty"`
}

type VLESSOptions struct {
	UUID              string `json:"uuid"`
	WireGuardExitUUID string `json:"wireGuardExitUuid,omitempty"`
	SNI               string `json:"sni"`
	PrivateKey        string `json:"privateKey"`
	PublicKey         string `json:"publicKey"`
	ShortID           string `json:"shortId"`
}

type Hysteria2Options struct {
	Password              string `json:"password"`
	WireGuardExitPassword string `json:"wireGuardExitPassword,omitempty"`
	Obfs                  string `json:"obfs,omitempty"`
	ObfsPassword          string `json:"obfsPassword,omitempty"`
}

type State struct {
	Version          int       `json:"version"`
	Upload           int64     `json:"upload"`
	Download         int64     `json:"download"`
	LastCoreUpload   int64     `json:"lastCoreUpload"`
	LastCoreDownload int64     `json:"lastCoreDownload"`
	PeriodStartedAt  time.Time `json:"periodStartedAt"`
	NextResetAt      time.Time `json:"nextResetAt,omitempty"`
	QuotaExceeded    bool      `json:"quotaExceeded"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

func (s State) Total() int64 { return s.Upload + s.Download }

func DefaultConfig() Config {
	return Config{
		Version: ConfigVersion, PanelPort: 2096, AdminUsername: "admin",
		Reset:            ResetConfig{Mode: "none", Day: 1, Timezone: "Local"},
		OutboundStrategy: OutboundStrategyAuto,
	}
}

func DefaultWireGuardExitConfig() WireGuardExitConfig {
	return WireGuardExitConfig{
		Label:      "GCP",
		ServerPort: 51820,
	}
}

func DefaultState(now time.Time) State {
	return State{Version: StateVersion, PeriodStartedAt: now, UpdatedAt: now}
}
