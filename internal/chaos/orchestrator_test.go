package chaos

import (
	"net/http/httptest"
	"testing"

	"cha0s-sim/internal/config"
)

func alwaysTrue(config.Rule) bool  { return true }
func alwaysFalse(config.Rule) bool { return false }

func TestNoRulesMatchEmptyInjectors(t *testing.T) {
	req := httptest.NewRequest("GET", "http://x/api/nowhere", nil)
	rules := []config.Rule{
		{Enabled: true, Path: "/api/users"},
	}

	reqs, resps := Resolve(req, rules, alwaysTrue)
	if len(reqs) != 0 || len(resps) != 0 {
		t.Errorf("got reqs=%d resps=%d, want 0/0", len(reqs), len(resps))
	}
}

func TestMatchedRuleDoesNotFireEmptyInjectors(t *testing.T) {
	req := httptest.NewRequest("GET", "http://x/api/users", nil)
	rules := []config.Rule{
		{Enabled: true, Path: "/api/users"},
	}

	reqs, resps := Resolve(req, rules, alwaysFalse)
	if len(reqs) != 0 || len(resps) != 0 {
		t.Errorf("got reqs=%d resps=%d, want 0/0", len(reqs), len(resps))
	}
}

func TestSingleFiredLatencyRuleProducesRequestInjector(t *testing.T) {
	req := httptest.NewRequest("GET", "http://x/api/users", nil)
	rules := []config.Rule{
		{Enabled: true, Path: "/api/users", Latency: &config.LatencyConfig{FixedMs: 100}},
	}

	reqs, resps := Resolve(req, rules, alwaysTrue)
	if len(reqs) != 1 {
		t.Fatalf("len(reqs) = %d, want 1", len(reqs))
	}
	if len(resps) != 0 {
		t.Fatalf("len(resps) = %d, want 0", len(resps))
	}
	if _, ok := reqs[0].(*LatencyInjector); !ok {
		t.Errorf("reqs[0] = %T, want *LatencyInjector", reqs[0])
	}
}

func TestSingleFiredStatusRuleProducesResponseInjector(t *testing.T) {
	req := httptest.NewRequest("GET", "http://x/api/users", nil)
	rules := []config.Rule{
		{Enabled: true, Path: "/api/users", StatusOverride: &config.StatusConfig{Code: 503}},
	}

	reqs, resps := Resolve(req, rules, alwaysTrue)
	if len(reqs) != 0 {
		t.Fatalf("len(reqs) = %d, want 0", len(reqs))
	}
	if len(resps) != 1 {
		t.Fatalf("len(resps) = %d, want 1", len(resps))
	}
	if _, ok := resps[0].(*StatusInjector); !ok {
		t.Errorf("resps[0] = %T, want *StatusInjector", resps[0])
	}
}

func TestMultipleFiredRulesAccumulateInjectors(t *testing.T) {
	req := httptest.NewRequest("GET", "http://x/api/users", nil)
	rules := []config.Rule{
		{Enabled: true, Name: "latency", Path: "/api/users", Latency: &config.LatencyConfig{FixedMs: 100}},
		{Enabled: true, Name: "drop", PathRegex: "^/api/.*", DropConnection: true},
	}

	reqs, resps := Resolve(req, rules, alwaysTrue)
	if len(reqs) != 2 {
		t.Fatalf("len(reqs) = %d, want 2", len(reqs))
	}
	if len(resps) != 0 {
		t.Fatalf("len(resps) = %d, want 0", len(resps))
	}
	if _, ok := reqs[0].(*LatencyInjector); !ok {
		t.Errorf("reqs[0] = %T, want *LatencyInjector (order preserved)", reqs[0])
	}
	if _, ok := reqs[1].(*DropperInjector); !ok {
		t.Errorf("reqs[1] = %T, want *DropperInjector (order preserved)", reqs[1])
	}
}

func TestOnlyFiringRuleInjectorsAppear(t *testing.T) {
	req := httptest.NewRequest("GET", "http://x/api/users", nil)
	rules := []config.Rule{
		{Enabled: true, Name: "fires", Path: "/api/users", Latency: &config.LatencyConfig{FixedMs: 100}},
		{Enabled: true, Name: "noFires", Path: "/api/users", DropConnection: true},
	}

	decide := func(rule config.Rule) bool { return rule.Name == "fires" }

	reqs, resps := Resolve(req, rules, decide)
	if len(reqs) != 1 {
		t.Fatalf("len(reqs) = %d, want 1", len(reqs))
	}
	if len(resps) != 0 {
		t.Fatalf("len(resps) = %d, want 0", len(resps))
	}
	if _, ok := reqs[0].(*LatencyInjector); !ok {
		t.Errorf("reqs[0] = %T, want *LatencyInjector (only the firing rule)", reqs[0])
	}
}
