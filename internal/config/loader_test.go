package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validYAML = `
rules:
  - name: "users latency"
    path: "/api/users"
    error_rate: 0.2
    latency:
      fixed_ms: 100
`

const validJSON = `{
  "rules": [
    {
      "name": "users latency",
      "path": "/api/users",
      "error_rate": 0.2,
      "latency": { "fixed_ms": 100 }
    }
  ]
}`

func writeFile(t *testing.T, content, ext string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "chaos"+ext)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	return path
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name        string
		path        func() string
		wantErr     bool
		wantRules   int
		errContains string
	}{
		{
			name:      "loads valid yaml",
			path:      func() string { return writeFile(t, validYAML, ".yaml") },
			wantErr:   false,
			wantRules: 1,
		},
		{
			name:      "loads valid json",
			path:      func() string { return writeFile(t, validJSON, ".json") },
			wantErr:   false,
			wantRules: 1,
		},
		{
			name:        "file not found",
			path:        func() string { return filepath.Join(t.TempDir(), "missing.yaml") },
			wantErr:     true,
			errContains: "failed to read config file",
		},
		{
			name:        "unsupported extension",
			path:        func() string { return writeFile(t, validYAML, ".txt") },
			wantErr:     true,
			errContains: "unsupported config file extension",
		},
		{
			name: "malformed yaml",
			path: func() string {
				return writeFile(t, "rules:\n  - name: \"oops\"\n    path: \"/x\"\n   error_rate: 0.1\n", ".yaml")
			},
			wantErr:     true,
			errContains: "failed to parse config file",
		},
		{
			name: "fails validation",
			path: func() string {
				return writeFile(t, "rules:\n  - name: \"bad\"\n    path: \"/x\"\n    error_rate: 5.0\n", ".yaml")
			},
			wantErr:     true,
			errContains: "config validation failed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(tc.path())
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Load() = nil, want error")
				}
				if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v, want nil", err)
			}
			if cfg == nil {
				t.Fatalf("Load() = nil Config, want non-nil")
			}
			if len(cfg.Rules) != tc.wantRules {
				t.Errorf("len(cfg.Rules) = %d, want %d", len(cfg.Rules), tc.wantRules)
			}
		})
	}
}
