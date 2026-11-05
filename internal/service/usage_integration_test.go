package service

import (
	"context"
	"testing"
)

func TestUsage_ReflectsUsageAndCost(t *testing.T) {
	pool := testPool(t)
	meter := NewMeterService(pool, quietLogger())
	usage := NewUsageService(pool)
	tenant := newTenant(t, pool, "free")

	if _, err := meter.Generate(context.Background(), tenant, "u1", GenerateInput{InputTokens: 1000, OutputTokens: 2500}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	v, err := usage.Usage(context.Background(), tenant)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if v.Plan != "free" {
		t.Fatalf("plan = %s, want free", v.Plan)
	}
	if v.APICalls.Used != 1 || v.APICalls.Limit != 1000 || v.APICalls.Remaining != 999 {
		t.Fatalf("api_calls = %+v", v.APICalls)
	}
	if v.AITokens.Used != 3500 || v.AITokens.Limit != 100000 {
		t.Fatalf("ai_tokens = %+v", v.AITokens)
	}
	// 1000*300000/1e6 + 2500*2500000/1e6 = 300 + 6250 = 6550
	if v.CostMicroUSD != 6550 {
		t.Fatalf("cost = %d, want 6550", v.CostMicroUSD)
	}
}

func TestUsage_TenantIsolation(t *testing.T) {
	pool := testPool(t)
	meter := NewMeterService(pool, quietLogger())
	usage := NewUsageService(pool)
	a := newTenant(t, pool, "free")
	b := newTenant(t, pool, "free")

	if _, err := meter.Generate(context.Background(), a, "ak", GenerateInput{OutputTokens: 100}); err != nil {
		t.Fatalf("generate A: %v", err)
	}
	vb, err := usage.Usage(context.Background(), b)
	if err != nil {
		t.Fatalf("usage B: %v", err)
	}
	if vb.APICalls.Used != 0 || vb.AITokens.Used != 0 {
		t.Fatalf("tenant B sees tenant A's usage: %+v", vb)
	}
	va, _ := usage.Usage(context.Background(), a)
	if va.APICalls.Used != 1 {
		t.Fatalf("tenant A usage = %+v, want 1 api call", va.APICalls)
	}
}

func TestUsage_ProLimits(t *testing.T) {
	pool := testPool(t)
	usage := NewUsageService(pool)
	tenant := newTenant(t, pool, "pro")

	v, err := usage.Usage(context.Background(), tenant)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if v.Plan != "pro" || v.APICalls.Limit != 10000 || v.AITokens.Limit != 1000000 {
		t.Fatalf("pro limits wrong: %+v", v)
	}
}
