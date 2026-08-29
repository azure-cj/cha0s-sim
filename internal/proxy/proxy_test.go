package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestCORSHeaderPassthrough(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Origin"); got != "https://frontend.example.com" {
			t.Errorf("backend received Origin = %q, want %q", got, "https://frontend.example.com")
		}
		if got := r.Header.Get("Access-Control-Request-Method"); got != "POST" {
			t.Errorf("backend received Access-Control-Request-Method = %q, want %q", got, "POST")
		}
		w.Header().Set("Access-Control-Allow-Origin", "https://frontend.example.com")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	targetURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatalf("failed to parse backend URL: %v", err)
	}

	proxyServer := httptest.NewServer(New(targetURL, false, false, nil))
	defer proxyServer.Close()

	req, err := http.NewRequest(http.MethodOptions, proxyServer.URL, nil)
	if err != nil {
		t.Fatalf("failed to build request: %v", err)
	}
	req.Header.Set("Origin", "https://frontend.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request through proxy failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status code = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}

	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://frontend.example.com" {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, "https://frontend.example.com")
	}
	if got := resp.Header.Get("Access-Control-Allow-Methods"); got != "POST, GET, OPTIONS" {
		t.Errorf("Access-Control-Allow-Methods = %q, want %q", got, "POST, GET, OPTIONS")
	}
}

func TestIsWebSocketUpgrade(t *testing.T) {
	cases := []struct {
		name       string
		connection string
		upgrade    string
		want       bool
	}{
		{name: "proper upgrade headers", connection: "Upgrade", upgrade: "websocket", want: true},
		{name: "missing Upgrade header", connection: "Upgrade", upgrade: "", want: false},
		{name: "connection keep-alive only", connection: "keep-alive", upgrade: "websocket", want: false},
		{name: "case-insensitive headers", connection: "Upgrade", upgrade: "WebSocket", want: true},
		{name: "comma-separated connection", connection: "keep-alive, Upgrade", upgrade: "websocket", want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
			req.Header.Set("Connection", tc.connection)
			req.Header.Set("Upgrade", tc.upgrade)

			if got := IsWebSocketUpgrade(req); got != tc.want {
				t.Errorf("IsWebSocketUpgrade(connection=%q, upgrade=%q) = %v, want %v", tc.connection, tc.upgrade, got, tc.want)
			}
		})
	}
}
