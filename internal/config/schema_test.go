package config

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name     string
		config   Config
		wantErr  bool
		contains []string
	}{
		{
			name: "valid config passes",
			config: Config{
				Rules: []Rule{
					{
						Name:      "exact path rule",
						Path:      "/api/users",
						ErrorRate: 0.1,
						Latency:   &LatencyConfig{FixedMs: 200},
					},
					{
						Name:      "regex rule",
						PathRegex: "^/v1/.*",
						Mangle:    &MangleConfig{Enabled: true, Strategy: "delete_keys"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "path and path_regex mutually exclusive",
			config: Config{
				Rules: []Rule{
					{Path: "/api", PathRegex: "^/api"},
				},
			},
			wantErr:  true,
			contains: []string{"path and path_regex are mutually exclusive"},
		},
		{
			name: "invalid path_regex",
			config: Config{
				Rules: []Rule{
					{PathRegex: "([unclosed"},
				},
			},
			wantErr:  true,
			contains: []string{"invalid path_regex"},
		},
		{
			name: "neither path nor path_regex set",
			config: Config{
				Rules: []Rule{
					{Name: "nomatch"},
				},
			},
			wantErr:  true,
			contains: []string{"either path or path_regex must be set"},
		},
		{
			name: "error_rate out of range",
			config: Config{
				Rules: []Rule{
					{Path: "/api", ErrorRate: 1.5},
				},
			},
			wantErr:  true,
			contains: []string{"error_rate must be between 0.0 and 1.0, got 1.5"},
		},
		{
			name: "invalid mangle strategy",
			config: Config{
				Rules: []Rule{
					{Path: "/api", Mangle: &MangleConfig{Enabled: true, Strategy: "bogus"}},
				},
			},
			wantErr:  true,
			contains: []string{"invalid mangle strategy 'bogus', must be one of: delete_keys, corrupt_types"},
		},
		{
			name: "invalid fuzz strategy",
			config: Config{
				Rules: []Rule{
					{Path: "/api", Fuzz: &FuzzConfig{Strategy: "bogus"}},
				},
			},
			wantErr:  true,
			contains: []string{"invalid fuzz strategy 'bogus', must be one of: sqli, xss, path_traversal"},
		},
		{
			name: "valid fuzz strategy passes",
			config: Config{
				Rules: []Rule{
					{Path: "/api", Fuzz: &FuzzConfig{Strategy: "sqli"}},
					{Path: "/api2", Fuzz: &FuzzConfig{Strategy: "xss"}},
					{Path: "/api3", Fuzz: &FuzzConfig{Strategy: "path_traversal"}},
				},
			},
			wantErr: false,
		},
		{
			name: "latency jitter min greater than max",
			config: Config{
				Rules: []Rule{
					{Path: "/api", Latency: &LatencyConfig{JitterMinMs: 500, JitterMaxMs: 100}},
				},
			},
			wantErr:  true,
			contains: []string{"latency jitter_min_ms (500) must be <= jitter_max_ms (100)"},
		},
		{
			name: "invalid status_override code",
			config: Config{
				Rules: []Rule{
					{Path: "/api", StatusOverride: &StatusConfig{Code: 42}},
				},
			},
			wantErr:  true,
			contains: []string{"status_override code 42 is not a valid HTTP status code (must be 100-599)"},
		},
		{
			name: "rule id uses index when name empty",
			config: Config{
				Rules: []Rule{
					{PathRegex: "([unclosed"},
				},
			},
			wantErr:  true,
			contains: []string{"rule[0]"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.config.Validate()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Validate() = nil, want error")
				}
				for _, c := range tc.contains {
					if !strings.Contains(err.Error(), c) {
						t.Errorf("error %q does not contain %q", err.Error(), c)
					}
				}
			} else {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
			}
		})
	}
}

func TestValidateAggregatesMultipleProblems(t *testing.T) {
	config := Config{
		Rules: []Rule{
			{Name: "ruleA", Path: "/a", PathRegex: "^/a", ErrorRate: 2.0},
			{Name: "ruleB", Path: "/b", StatusOverride: &StatusConfig{Code: 999}, Mangle: &MangleConfig{Enabled: true, Strategy: "nope"}},
		},
	}

	err := config.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want aggregated error")
	}

	msg := err.Error()
	for _, idText := range []string{"ruleA", "ruleB"} {
		if !strings.Contains(msg, idText) {
			t.Errorf("aggregated error %q missing rule id %q", msg, idText)
		}
	}
	if !strings.Contains(msg, "path and path_regex are mutually exclusive") {
		t.Errorf("aggregated error missing first problem: %q", msg)
	}
	if !strings.Contains(msg, "error_rate must be between 0.0 and 1.0") {
		t.Errorf("aggregated error missing ruleA error_rate problem: %q", msg)
	}
	if !strings.Contains(msg, "status_override code 999") {
		t.Errorf("aggregated error missing ruleB status problem: %q", msg)
	}
	if !strings.Contains(msg, "invalid mangle strategy 'nope'") {
		t.Errorf("aggregated error missing ruleB mangle problem: %q", msg)
	}
}

func TestWarnings(t *testing.T) {
	t.Run("single disabled rule produces one warning", func(t *testing.T) {
		cfg := Config{
			Rules: []Rule{
				{Name: "frobnicate", Path: "/api/frob", Enabled: false},
			},
		}
		warnings := cfg.Warnings()
		if len(warnings) != 1 {
			t.Fatalf("Warnings() len = %d, want 1", len(warnings))
		}
		if !strings.Contains(warnings[0], "frobnicate") {
			t.Errorf("warning %q missing rule id", warnings[0])
		}
		if !strings.Contains(warnings[0], "disabled") {
			t.Errorf("warning %q missing 'disabled'", warnings[0])
		}
	})

	t.Run("all rules enabled produces no warnings", func(t *testing.T) {
		cfg := Config{
			Rules: []Rule{
				{Name: "a", Path: "/a", Enabled: true},
				{Name: "b", Path: "/b", Enabled: true},
			},
		}
		if warnings := cfg.Warnings(); len(warnings) != 0 {
			t.Errorf("Warnings() = %v, want empty", warnings)
		}
	})

	t.Run("multiple disabled rules produce one warning each", func(t *testing.T) {
		cfg := Config{
			Rules: []Rule{
				{Name: "first", Path: "/first", Enabled: false},
				{Name: "second", Path: "/second", Enabled: true},
				{Name: "", Path: "/third", Enabled: false},
			},
		}
		warnings := cfg.Warnings()
		if len(warnings) != 2 {
			t.Fatalf("Warnings() len = %d, want 2", len(warnings))
		}
		if !strings.Contains(warnings[0], "first") {
			t.Errorf("warning[0] %q missing rule id 'first'", warnings[0])
		}
		if !strings.Contains(warnings[1], "rule[2]") {
			t.Errorf("warning[1] %q missing index-based rule id 'rule[2]'", warnings[1])
		}
	})
}
