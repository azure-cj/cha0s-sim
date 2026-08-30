package config

import (
	"strings"
	"testing"
)

func TestValidateTargetURL(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantErr  bool
		contains string
	}{
		{name: "valid http", raw: "http://localhost:3000", wantErr: false},
		{name: "valid https", raw: "https://api.example.com", wantErr: false},
		{name: "valid http with path", raw: "http://localhost:8080/api", wantErr: false},
		{name: "valid https with port", raw: "https://example.com:8443", wantErr: false},
		{name: "invalid scheme ftp", raw: "ftp://example.com", wantErr: true, contains: "scheme must be http:// or https://, got: ftp"},
		{name: "scheme missing", raw: "localhost:3000", wantErr: true, contains: "scheme must be http:// or https://"},
		{name: "malformed url", raw: "http://exa mple.com", wantErr: true},
		{name: "empty url", raw: "", wantErr: true},
		{name: "non-url garbage", raw: "not a url at all", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, err := ValidateTargetURL(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ValidateTargetURL(%q) = %v, want error", tc.raw, u)
				}
				if tc.contains != "" && !strings.Contains(err.Error(), tc.contains) {
					t.Errorf("error = %q, want it to contain %q", err.Error(), tc.contains)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateTargetURL(%q) unexpected error: %v", tc.raw, err)
			}
			if u == nil || u.String() == "" {
				t.Errorf("ValidateTargetURL(%q) returned nil/empty URL", tc.raw)
			}
		})
	}
}
