package matcher

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"cha0s-sim/internal/config"
)

func TestMatch(t *testing.T) {
	tests := []struct {
		name      string
		req       func() *http.Request
		rules     []config.Rule
		want      int
		wantOrder []string
	}{
		{
			name: "exact path, no method (matches all)",
			req:  func() *http.Request { return httptest.NewRequest("POST", "http://x/api/users", nil) },
			rules: []config.Rule{
				{Enabled: true, Path: "/api/users"},
			},
			want: 1,
		},
		{
			name: "exact path, method matches",
			req:  func() *http.Request { return httptest.NewRequest("GET", "http://x/api/users", nil) },
			rules: []config.Rule{
				{Enabled: true, Path: "/api/users", Methods: []string{"GET"}},
			},
			want: 1,
		},
		{
			name: "exact path, method does not match",
			req:  func() *http.Request { return httptest.NewRequest("POST", "http://x/api/users", nil) },
			rules: []config.Rule{
				{Enabled: true, Path: "/api/users", Methods: []string{"GET"}},
			},
			want: 0,
		},
		{
			name: "path_regex dynamic path matches",
			req:  func() *http.Request { return httptest.NewRequest("GET", "http://x/api/orders/42", nil) },
			rules: []config.Rule{
				{Enabled: true, PathRegex: "^/api/orders/[0-9]+$"},
			},
			want: 1,
		},
		{
			name: "path_regex does not match",
			req:  func() *http.Request { return httptest.NewRequest("GET", "http://x/api/orders/abc", nil) },
			rules: []config.Rule{
				{Enabled: true, PathRegex: "^/api/orders/[0-9]+$"},
			},
			want: 0,
		},
		{
			name: "header requirement satisfied",
			req: func() *http.Request {
				r := httptest.NewRequest("GET", "http://x/api/users", nil)
				r.Header.Set("X-Tenant", "acme")
				return r
			},
			rules: []config.Rule{
				{Enabled: true, Path: "/api/users", Headers: map[string]string{"X-Tenant": "acme"}},
			},
			want: 1,
		},
		{
			name: "header requirement missing",
			req:  func() *http.Request { return httptest.NewRequest("GET", "http://x/api/users", nil) },
			rules: []config.Rule{
				{Enabled: true, Path: "/api/users", Headers: map[string]string{"X-Tenant": "acme"}},
			},
			want: 0,
		},
		{
			name: "header present but wrong value",
			req: func() *http.Request {
				r := httptest.NewRequest("GET", "http://x/api/users", nil)
				r.Header.Set("X-Tenant", "globex")
				return r
			},
			rules: []config.Rule{
				{Enabled: true, Path: "/api/users", Headers: map[string]string{"X-Tenant": "acme"}},
			},
			want: 0,
		},
		{
			name: "disabled rule does not match",
			req:  func() *http.Request { return httptest.NewRequest("GET", "http://x/api/users", nil) },
			rules: []config.Rule{
				{Enabled: false, Path: "/api/users"},
			},
			want: 0,
		},
		{
			name: "multiple rules match, order preserved",
			req:  func() *http.Request { return httptest.NewRequest("GET", "http://x/api/users", nil) },
			rules: []config.Rule{
				{Enabled: true, Name: "first", Path: "/api/users"},
				{Enabled: true, Name: "second", Path: "/api/users"},
				{Enabled: true, Name: "third", Path: "/api/orders"},
			},
			want:      2,
			wantOrder: []string{"first", "second"},
		},
		{
			name: "no rules match",
			req:  func() *http.Request { return httptest.NewRequest("GET", "http://x/api/nope", nil) },
			rules: []config.Rule{
				{Enabled: true, Path: "/api/users"},
			},
			want: 0,
		},
		{
			name: "malformed rule path and path_regex both empty",
			req:  func() *http.Request { return httptest.NewRequest("GET", "http://x/api", nil) },
			rules: []config.Rule{
				{Enabled: true, Name: "malformed"},
			},
			want: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Match(tc.req(), tc.rules)
			if len(got) != tc.want {
				t.Fatalf("Match() returned %d rules, want %d", len(got), tc.want)
			}
			if tc.wantOrder != nil {
				for i, wantName := range tc.wantOrder {
					if got[i].Name != wantName {
						t.Errorf("matched rule[%d].Name = %q, want %q", i, got[i].Name, wantName)
					}
				}
			}
		})
	}
}
