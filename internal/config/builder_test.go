package config

import (
	"strings"
	"testing"
)

func TestBuildRuleFromRequestEffectMapping(t *testing.T) {
	tests := []struct {
		name  string
		req   CreateRuleRequest
		check func(t *testing.T, r *Rule)
	}{
		{
			name: "slow defaults to 1000ms with no jitter",
			req:  CreateRuleRequest{Name: "slow", Path: "/api/slow", Frequency: "always", Effect: "slow"},
			check: func(t *testing.T, r *Rule) {
				if r.Latency == nil {
					t.Fatal("Latency = nil, want non-nil for slow effect")
				}
				if r.Latency.FixedMs != 1000 {
					t.Errorf("FixedMs = %d, want default 1000", r.Latency.FixedMs)
				}
				if r.Latency.JitterMinMs != 0 || r.Latency.JitterMaxMs != 0 {
					t.Errorf("jitter = [%d,%d], want no jitter for a created rule", r.Latency.JitterMinMs, r.Latency.JitterMaxMs)
				}
			},
		},
		{
			name: "slow honors explicit duration",
			req:  CreateRuleRequest{Name: "slow", Path: "/api/slow", Frequency: "always", Effect: "slow", SlowMs: 250},
			check: func(t *testing.T, r *Rule) {
				if r.Latency.FixedMs != 250 {
					t.Errorf("FixedMs = %d, want 250", r.Latency.FixedMs)
				}
			},
		},
		{
			name: "error defaults to 500",
			req:  CreateRuleRequest{Name: "err", Path: "/api/err", Frequency: "always", Effect: "error"},
			check: func(t *testing.T, r *Rule) {
				if r.StatusOverride == nil {
					t.Fatal("StatusOverride = nil, want non-nil for error effect")
				}
				if r.StatusOverride.Code != 500 {
					t.Errorf("code = %d, want default 500", r.StatusOverride.Code)
				}
			},
		},
		{
			name: "error honors explicit code and stays in valid range",
			req:  CreateRuleRequest{Name: "err", Path: "/api/err", Frequency: "always", Effect: "error", ErrorCode: 503},
			check: func(t *testing.T, r *Rule) {
				if r.StatusOverride.Code != 503 {
					t.Errorf("code = %d, want 503", r.StatusOverride.Code)
				}
			},
		},
		{
			name: "drop sets drop_connection",
			req:  CreateRuleRequest{Name: "drop", Path: "/api/drop", Frequency: "always", Effect: "drop"},
			check: func(t *testing.T, r *Rule) {
				if !r.DropConnection {
					t.Error("DropConnection = false, want true for drop effect")
				}
				if r.Latency != nil || r.StatusOverride != nil || r.Mangle != nil || r.Fuzz != nil {
					t.Error("drop should not set any other chaos config")
				}
			},
		},
		{
			name: "corrupt builds enabled delete_keys mangle with target keys",
			req:  CreateRuleRequest{Name: "corrupt", Path: "/api/corrupt", Frequency: "always", Effect: "corrupt", TargetKeys: []string{"ssn", "address"}},
			check: func(t *testing.T, r *Rule) {
				if r.Mangle == nil {
					t.Fatal("Mangle = nil, want non-nil for corrupt effect")
				}
				if !r.Mangle.Enabled {
					t.Error("Mangle.Enabled = false, want true")
				}
				if r.Mangle.Strategy != "delete_keys" {
					t.Errorf("Mangle.Strategy = %q, want delete_keys", r.Mangle.Strategy)
				}
				if len(r.Mangle.TargetKeys) != 2 || r.Mangle.TargetKeys[0] != "ssn" {
					t.Errorf("Mangle.TargetKeys = %v, want [ssn address]", r.Mangle.TargetKeys)
				}
			},
		},
		{
			name: "attack maps to fuzz strategy",
			req:  CreateRuleRequest{Name: "attack", Path: "/api/attack", Frequency: "always", Effect: "attack", Attack: "sqli"},
			check: func(t *testing.T, r *Rule) {
				if r.Fuzz == nil {
					t.Fatal("Fuzz = nil, want non-nil for attack effect")
				}
				if r.Fuzz.Strategy != "sqli" {
					t.Errorf("Fuzz.Strategy = %q, want sqli", r.Fuzz.Strategy)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := BuildRuleFromRequest(tt.req)
			if err != nil {
				t.Fatalf("BuildRuleFromRequest() error = %v, want nil", err)
			}
			if r.Name != tt.req.Name || r.Path != tt.req.Path {
				t.Errorf("name/path = %q/%q, want %q/%q", r.Name, r.Path, tt.req.Name, tt.req.Path)
			}
			if r.PathRegex != "" {
				t.Error("PathRegex set on a created rule; create-rule form only ever uses exact paths")
			}
			if !r.Enabled {
				t.Error("Enabled = false, want true — a created rule must act immediately")
			}
			tt.check(t, r)
		})
	}
}

func TestBuildRuleFromRequestFrequencyMapping(t *testing.T) {
	want := map[string]float64{"always": 1.0, "often": 0.5, "sometimes": 0.2, "rarely": 0.05}
	for frequency, rate := range want {
		t.Run(frequency, func(t *testing.T) {
			r, err := BuildRuleFromRequest(CreateRuleRequest{Name: "r", Path: "/x", Frequency: frequency, Effect: "drop"})
			if err != nil {
				t.Fatalf("BuildRuleFromRequest() error = %v, want nil", err)
			}
			if r.ErrorRate != rate {
				t.Errorf("ErrorRate = %v, want %v for frequency %q", r.ErrorRate, rate, frequency)
			}
			if r.ErrorRate < 0 || r.ErrorRate > 1 {
				t.Errorf("ErrorRate = %v out of valid [0,1] range", r.ErrorRate)
			}
		})
	}
}

func TestBuildRuleFromRequestNormalizesMethods(t *testing.T) {
	r, err := BuildRuleFromRequest(CreateRuleRequest{
		Name: "r", Path: "/x", Frequency: "sometimes", Effect: "drop",
		Methods: []string{" get ", "POST", "DELETE"},
	})
	if err != nil {
		t.Fatalf("BuildRuleFromRequest() error = %v, want nil", err)
	}
	if len(r.Methods) != 3 || r.Methods[0] != "GET" || r.Methods[1] != "POST" || r.Methods[2] != "DELETE" {
		t.Errorf("Methods = %v, want [GET POST DELETE] (trimmed and uppercased)", r.Methods)
	}
}

func TestBuildRuleFromRequestNoMethodsMeansAll(t *testing.T) {
	r, err := BuildRuleFromRequest(CreateRuleRequest{Name: "r", Path: "/x", Frequency: "always", Effect: "drop"})
	if err != nil {
		t.Fatalf("BuildRuleFromRequest() error = %v, want nil", err)
	}
	if len(r.Methods) != 0 {
		t.Errorf("Methods = %v, want empty (all methods) when none specified", r.Methods)
	}
}

func TestBuildRuleFromRequestRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		req  CreateRuleRequest
	}{
		{"empty name", CreateRuleRequest{Path: "/x", Frequency: "always", Effect: "drop"}},
		{"blank name", CreateRuleRequest{Name: "  ", Path: "/x", Frequency: "always", Effect: "drop"}},
		{"empty path", CreateRuleRequest{Name: "r", Frequency: "always", Effect: "drop"}},
		{"unknown frequency", CreateRuleRequest{Name: "r", Path: "/x", Frequency: "daily", Effect: "drop"}},
		{"unknown effect", CreateRuleRequest{Name: "r", Path: "/x", Frequency: "always", Effect: "explode"}},
		{"unsupported method", CreateRuleRequest{Name: "r", Path: "/x", Frequency: "always", Effect: "drop", Methods: []string{"OPTIONS"}}},
		{"corrupt without target keys", CreateRuleRequest{Name: "r", Path: "/x", Frequency: "always", Effect: "corrupt"}},
		{"attack without strategy", CreateRuleRequest{Name: "r", Path: "/x", Frequency: "always", Effect: "attack"}},
		{"attack with unknown strategy", CreateRuleRequest{Name: "r", Path: "/x", Frequency: "always", Effect: "attack", Attack: "buffer_overflow"}},
		{"error code out of range", CreateRuleRequest{Name: "r", Path: "/x", Frequency: "always", Effect: "error", ErrorCode: 999}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := BuildRuleFromRequest(tt.req)
			if err == nil {
				t.Fatalf("BuildRuleFromRequest() = %+v, want error for %s", r, tt.name)
			}
			if strings.Contains(err.Error(), "\n") {
				t.Errorf("error message should be single-line and human readable, got %q", err.Error())
			}
		})
	}
}

// TestBuildRuleFromRequestAlwaysPassesValidate ensures every effect produces a
// rule that the config loader's own validation accepts, so a created rule can
// never be written to disk and then rejected on reload.
func TestBuildRuleFromRequestAlwaysPassesValidate(t *testing.T) {
	canonical := map[string]CreateRuleRequest{
		"slow":    {Name: "slow rule", Path: "/api/slow", Frequency: "rarely", Effect: "slow", SlowMs: 300},
		"error":   {Name: "error rule", Path: "/api/err", Frequency: "sometimes", Effect: "error", ErrorCode: 418},
		"drop":    {Name: "drop rule", Path: "/api/drop", Frequency: "often", Effect: "drop"},
		"corrupt": {Name: "corrupt rule", Path: "/api/corrupt", Frequency: "always", Effect: "corrupt", TargetKeys: []string{"ssn"}},
		"attack":  {Name: "attack rule", Path: "/api/attack", Frequency: "always", Effect: "attack", Attack: "xss", Methods: []string{"post"}},
	}
	for name, req := range canonical {
		t.Run(name, func(t *testing.T) {
			r, err := BuildRuleFromRequest(req)
			if err != nil {
				t.Fatalf("BuildRuleFromRequest() error = %v, want nil", err)
			}
			cfg := Config{Rules: []Rule{*r}}
			if err := cfg.Validate(); err != nil {
				t.Errorf("Config.Validate() error = %v, want nil for a request-built rule", err)
			}
		})
	}
}