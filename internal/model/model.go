package model

import (
	"errors"
	"math"
	"math/big"
	"strconv"
	"time"
)

const (
	ConfigVersion = 3
	StateVersion  = 1

	OutboundStrategyAuto       = "auto"
	OutboundStrategyPreferIPv4 = "prefer_ipv4"
	OutboundStrategyPreferIPv6 = "prefer_ipv6"
	OutboundStrategyIPv4Only   = "ipv4_only"
	OutboundStrategyIPv6Only   = "ipv6_only"

	TrafficBillingBidirectional = "bidirectional"
	TrafficBillingSingle        = "single"
)

type Config struct {
	Version              int                `json:"version"`
	Domain               string             `json:"domain"`
	PanelPort            int                `json:"panelPort"`
	WebManagementEnabled bool               `json:"webManagementEnabled"`
	AdminUsername        string             `json:"adminUsername"`
	AdminPasswordHash    string             `json:"adminPasswordHash"`
	SessionSecret        string             `json:"sessionSecret"`
	ClashAPISecret       string             `json:"clashAPISecret"`
	SubscriptionToken    string             `json:"subscriptionToken"`
	TrafficQuota         TrafficQuotaConfig `json:"trafficQuota"`
	Reset                ResetConfig        `json:"reset"`
	OutboundStrategy     string             `json:"outboundStrategy,omitempty"`
	Inbounds             []Inbound          `json:"inbounds"`
}

// TrafficQuotaConfig stores provider plans in decimal GB. The internal
// proxy-traffic stop threshold is derived from it whenever it is needed.
type TrafficQuotaConfig struct {
	AmountGB        float64 `json:"amountGB"`
	BillingMode     string  `json:"billingMode"`
	HeadroomPercent int     `json:"headroomPercent"`
}

func (q TrafficQuotaConfig) Validate() error {
	if math.IsNaN(q.AmountGB) || math.IsInf(q.AmountGB, 0) || q.AmountGB < 0 {
		return errors.New("套餐流量不能为负数或非有限值")
	}
	if q.BillingMode != TrafficBillingBidirectional && q.BillingMode != TrafficBillingSingle {
		return errors.New("流量计费方式无效")
	}
	if q.HeadroomPercent < 0 || q.HeadroomPercent > 50 {
		return errors.New("安全预留比例必须在 0 到 50 之间")
	}
	_, err := q.AllowanceBytes()
	return err
}

// AllowanceBytes converts the provider's advertised allowance to bytes.
func (q TrafficQuotaConfig) AllowanceBytes() (int64, error) {
	return trafficBytes(q.AmountGB, 1_000_000_000, 1, 1)
}

// EffectiveBytes returns the proxy traffic at which SBM should stop sing-box.
// Bidirectional providers count the same proxied byte once on ingress and once
// on egress, so their effective proxy allowance is half the advertised value.
func (q TrafficQuotaConfig) EffectiveBytes() (int64, error) {
	factor := int64(1)
	if q.BillingMode == TrafficBillingBidirectional {
		factor = 2
	} else if q.BillingMode != TrafficBillingSingle {
		return 0, errors.New("流量计费方式无效")
	}
	if q.HeadroomPercent < 0 || q.HeadroomPercent > 50 {
		return 0, errors.New("安全预留比例必须在 0 到 50 之间")
	}
	return trafficBytes(q.AmountGB, 1_000_000_000, int64(100-q.HeadroomPercent), 100*factor)
}

func (q TrafficQuotaConfig) ProviderUsageFactor() int64 {
	if q.BillingMode == TrafficBillingBidirectional {
		return 2
	}
	return 1
}

func (c Config) EffectiveTrafficLimitBytes() (int64, error) {
	return c.TrafficQuota.EffectiveBytes()
}

func trafficBytes(amount float64, unitBytes, numerator, denominator int64) (int64, error) {
	if math.IsNaN(amount) || math.IsInf(amount, 0) || amount < 0 {
		return 0, errors.New("套餐流量不能为负数或非有限值")
	}
	amountText := strconv.FormatFloat(amount, 'f', -1, 64)
	amountRat, ok := new(big.Rat).SetString(amountText)
	if !ok {
		return 0, errors.New("套餐流量格式无效")
	}
	value := new(big.Rat).Mul(amountRat, new(big.Rat).SetInt64(unitBytes))
	value.Mul(value, new(big.Rat).SetInt64(numerator))
	value.Quo(value, new(big.Rat).SetInt64(denominator))
	bytes := new(big.Int).Quo(value.Num(), value.Denom())
	if !bytes.IsInt64() {
		return 0, errors.New("套餐流量超出支持范围")
	}
	return bytes.Int64(), nil
}

type ResetConfig struct {
	Mode     string `json:"mode"`
	Day      int    `json:"day"`
	Timezone string `json:"timezone"`
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
	UUID       string `json:"uuid"`
	SNI        string `json:"sni"`
	PrivateKey string `json:"privateKey"`
	PublicKey  string `json:"publicKey"`
	ShortID    string `json:"shortId"`
}

type Hysteria2Options struct {
	Password     string `json:"password"`
	Obfs         string `json:"obfs,omitempty"`
	ObfsPassword string `json:"obfsPassword,omitempty"`
}

type State struct {
	Version          int       `json:"version"`
	Upload           int64     `json:"upload"`
	Download         int64     `json:"download"`
	LastCoreUpload   int64     `json:"lastCoreUpload"`
	LastCoreDownload int64     `json:"lastCoreDownload"`
	CoreGeneration   string    `json:"coreGeneration,omitempty"`
	PeriodStartedAt  time.Time `json:"periodStartedAt"`
	NextResetAt      time.Time `json:"nextResetAt,omitempty"`
	QuotaExceeded    bool      `json:"quotaExceeded"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

func (s State) Total() int64 { return s.Upload + s.Download }

func DefaultConfig() Config {
	return Config{
		Version: ConfigVersion, PanelPort: 2096, AdminUsername: "admin",
		WebManagementEnabled: true,
		TrafficQuota:         TrafficQuotaConfig{BillingMode: TrafficBillingSingle, HeadroomPercent: 10},
		Reset:                ResetConfig{Mode: "none", Day: 1, Timezone: "Local"},
		OutboundStrategy:     OutboundStrategyAuto,
	}
}

func DefaultState(now time.Time) State {
	return State{Version: StateVersion, PeriodStartedAt: now, UpdatedAt: now}
}
