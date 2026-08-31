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
			name: "one thousand decimal gigabytes bidirectional with headroom",
			quota: TrafficQuotaConfig{
				Amount: 1000, Unit: TrafficUnitGB, BillingMode: TrafficBillingBidirectional, HeadroomPercent: 10,
			},
			allowance: 1_000_000_000_000,
			effective: 450_000_000_000,
		},
		{
			name: "two hundred gibibytes single direction",
			quota: TrafficQuotaConfig{
				Amount: 200, Unit: TrafficUnitGiB, BillingMode: TrafficBillingSingle, HeadroomPercent: 10,
			},
			allowance: 214_748_364_800,
			effective: 193_273_528_320,
		},
		{
			name: "fractional decimal gigabytes",
			quota: TrafficQuotaConfig{
				Amount: 1.5, Unit: TrafficUnitGB, BillingMode: TrafficBillingSingle,
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
	valid := TrafficQuotaConfig{Amount: 1000, Unit: TrafficUnitGB, BillingMode: TrafficBillingSingle, HeadroomPercent: 10}
	tests := []TrafficQuotaConfig{
		{Amount: -1, Unit: TrafficUnitGB, BillingMode: TrafficBillingSingle},
		{Amount: math.NaN(), Unit: TrafficUnitGB, BillingMode: TrafficBillingSingle},
		{Amount: 1, BillingMode: TrafficBillingSingle},
		{Amount: 1, Unit: "TB", BillingMode: TrafficBillingSingle},
		{Amount: 1, Unit: TrafficUnitGB, BillingMode: "both"},
		{Amount: 1, Unit: TrafficUnitGB, BillingMode: TrafficBillingSingle, HeadroomPercent: 51},
		{Amount: math.MaxFloat64, Unit: TrafficUnitGB, BillingMode: TrafficBillingSingle},
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
