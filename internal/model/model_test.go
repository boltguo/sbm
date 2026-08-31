package model

import (
	"math"
	"testing"
)

func TestTrafficQuotaConversions(t *testing.T) {
	tests := []struct {
		name      string
		quota     TrafficQuotaConfig
		allowance int64
		effective int64
	}{
		{
			name: "one thousand gigabytes bidirectional with headroom",
			quota: TrafficQuotaConfig{
				AmountGB: 1000, BillingMode: TrafficBillingBidirectional, HeadroomPercent: 10,
			},
			allowance: 1_000_000_000_000,
			effective: 450_000_000_000,
		},
		{
			name: "two hundred gigabytes single direction",
			quota: TrafficQuotaConfig{
				AmountGB: 200, BillingMode: TrafficBillingSingle, HeadroomPercent: 10,
			},
			allowance: 200_000_000_000,
			effective: 180_000_000_000,
		},
		{
			name: "fractional decimal gigabytes",
			quota: TrafficQuotaConfig{
				AmountGB: 1.5, BillingMode: TrafficBillingSingle,
			},
			allowance: 1_500_000_000,
			effective: 1_500_000_000,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.quota.Validate(); err != nil {
				t.Fatal(err)
			}
			allowance, err := test.quota.AllowanceBytes()
			if err != nil || allowance != test.allowance {
				t.Fatalf("allowance=%d err=%v, want %d", allowance, err, test.allowance)
			}
			effective, err := test.quota.EffectiveBytes()
			if err != nil || effective != test.effective {
				t.Fatalf("effective=%d err=%v, want %d", effective, err, test.effective)
			}
		})
	}
}

func TestTrafficQuotaValidation(t *testing.T) {
	valid := TrafficQuotaConfig{AmountGB: 1000, BillingMode: TrafficBillingSingle, HeadroomPercent: 10}
	tests := []TrafficQuotaConfig{
		{AmountGB: -1, BillingMode: TrafficBillingSingle},
		{AmountGB: math.NaN(), BillingMode: TrafficBillingSingle},
		{AmountGB: 1, BillingMode: "both"},
		{AmountGB: 1, BillingMode: TrafficBillingSingle, HeadroomPercent: 51},
		{AmountGB: math.MaxFloat64, BillingMode: TrafficBillingSingle},
	}
	for _, quota := range tests {
		if err := quota.Validate(); err == nil {
			t.Fatalf("expected validation error for %#v", quota)
		}
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid quota rejected: %v", err)
	}
}
