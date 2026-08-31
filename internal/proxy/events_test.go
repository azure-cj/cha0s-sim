package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cha0s-sim/internal/config"
	"cha0s-sim/internal/platform"
)

type collectingSink struct {
	events chan platform.Reportable
}

func newCollectingSink() *collectingSink {
	return &collectingSink{events: make(chan platform.Reportable, 16)}
}

func (s *collectingSink) Emit(evt platform.Reportable) {
	s.events <- evt
}

func (s *collectingSink) next(t *testing.T) TrafficEvent {
	t.Helper()
	for {
		select {
		case evt := <-s.events:
			if te, ok := evt.(TrafficEvent); ok {
				return te
			}
			// Security findings flow through the same (Reportable-widened)
			// sink; traffic-focused tests skip past them.
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for traffic event")
			return TrafficEvent{}
		}
	}
}

func writeStore(t *testing.T, body string) *config.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "chaos.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
	store, err := config.NewStore(path)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func newBackend(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("hello"))
	}))
	t.Cleanup(backend.Close)
	return backend, backend.URL
}

func eventProxy(t *testing.T, backendURL string, store *config.Store, sink EventSink) string {
	t.Helper()
	target, err := url.Parse(backendURL)
	if err != nil {
		t.Fatalf("failed to parse backend URL %q: %v", backendURL, err)
	}
	srv := NewServerInstance(0, target, false, false, false, store, PipelineFull, sink)
	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestEventPassthroughWithoutStoreEmitsCleanEvent(t *testing.T) {
	_, backendURL := newBackend(t)
	sink := newCollectingSink()
	tsURL := eventProxy(t, backendURL, nil, sink)

	resp, err := http.Get(tsURL + "/api/users")
	if err != nil {
		t.Fatalf("request through proxy failed: %v", err)
	}
	defer resp.Body.Close()

	evt := sink.next(t)
	if evt.Status != http.StatusOK {
		t.Errorf("Status = %d, want %d", evt.Status, http.StatusOK)
	}
	if evt.Method != http.MethodGet || evt.Path != "/api/users" {
		t.Errorf("got %s %s, want GET /api/users", evt.Method, evt.Path)
	}
	if len(evt.Effects) != 0 {
		t.Errorf("Effects = %v, want none (no rules loaded)", evt.Effects)
	}
	if evt.Ts.IsZero() {
		t.Error("Ts is zero, want the request start time")
	}
}

func TestEventReportsFiredRuleEffectsAndMeasuredLatency(t *testing.T) {
	backend, backendURL := newBackend(t)
	store := writeStore(t, `
rules:
  - name: "user chaos"
    enabled: true
    path: "/api/users"
    error_rate: 1.0
    latency:
      fixed_ms: 10
    status_override:
      code: 503
      strip_body: true
`)
	sink := newCollectingSink()
	tsURL := eventProxy(t, backendURL, store, sink)
	_ = backend

	resp, err := http.Get(tsURL + "/api/users")
	if err != nil {
		t.Fatalf("request through proxy failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 from override", resp.StatusCode)
	}

	evt := sink.next(t)
	if evt.Status != http.StatusServiceUnavailable {
		t.Errorf("Status = %d, want 503", evt.Status)
	}
	if evt.DurationMs < 10 {
		t.Errorf("DurationMs = %d, want >= 10 (fixed latency sleep)", evt.DurationMs)
	}

	var kinds []string
	for _, e := range evt.Effects {
		kinds = append(kinds, e.Kind)
		if e.Rule != "user chaos" {
			t.Errorf("effect Rule = %q, want %q", e.Rule, "user chaos")
		}
	}
	if !contains(kinds, "latency") || !contains(kinds, "override") {
		t.Errorf("Effects kinds = %v, want both latency and override", kinds)
	}
	for _, e := range evt.Effects {
		if e.Kind == "override" && e.Detail != "=> 503 (body stripped)" {
			t.Errorf("override detail = %q, want %q", e.Detail, "=> 503 (body stripped)")
		}
		if e.Kind == "latency" && e.Detail != "+10ms" {
			t.Errorf("latency detail = %q, want %q", e.Detail, "+10ms")
		}
	}
}

func TestEventReportsDroppedConnectionWithZeroStatus(t *testing.T) {
	_, backendURL := newBackend(t)
	store := writeStore(t, `
rules:
  - name: "drop route"
    enabled: true
    path: "/api/drop"
    error_rate: 1.0
    drop_connection: true
`)
	sink := newCollectingSink()
	tsURL := eventProxy(t, backendURL, store, sink)

	// The backend is never reached: the proxy hijacks and closes the TCP
	// connection, so the client request is expected to fail.
	_, _ = http.Get(tsURL + "/api/drop")

	evt := sink.next(t)
	if evt.Status != 0 {
		t.Errorf("Status = %d, want 0 (connection dropped before any response)", evt.Status)
	}
	var kinds []string
	for _, e := range evt.Effects {
		kinds = append(kinds, e.Kind)
	}
	if !contains(kinds, "drop") {
		t.Errorf("Effects kinds = %v, want a drop effect", kinds)
	}
}

func TestNonFiringRuleProducesCleanEvent(t *testing.T) {
	_, backendURL := newBackend(t)
	store := writeStore(t, `
rules:
  - name: "never fires"
    enabled: true
    path: "/api/users"
    error_rate: 0.0
    status_override:
      code: 500
`)
	sink := newCollectingSink()
	tsURL := eventProxy(t, backendURL, store, sink)

	resp, err := http.Get(tsURL + "/api/users")
	if err != nil {
		t.Fatalf("request through proxy failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (rule must not fire at error_rate 0)", resp.StatusCode)
	}

	evt := sink.next(t)
	if evt.Status != http.StatusOK {
		t.Errorf("Status = %d, want 200", evt.Status)
	}
	if len(evt.Effects) != 0 {
		t.Errorf("Effects = %v, want none (rule did not fire)", evt.Effects)
	}
}

func TestHeadlessServerWithoutSinkStillProxies(t *testing.T) {
	_, backendURL := newBackend(t)
	target, err := url.Parse(backendURL)
	if err != nil {
		t.Fatalf("failed to parse backend URL: %v", err)
	}

	// No sink supplied — exactly what the headless CLI does. The chain must
	// still serve requests without emitting or crashing.
	srv := NewServerInstance(0, target, false, false, false, nil, PipelineFull)
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("request through headless proxy failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func contains(items []string, want string) bool {
	for _, it := range items {
		if it == want {
			return true
		}
	}
	return false
}

func TestTrafficEventImplementsReportable(t *testing.T) {
	var reportable platform.Reportable = TrafficEvent{
		Method:     http.MethodGet,
		Path:       "/api/users",
		Status:     http.StatusOK,
		DurationMs: 7,
	}
	if reportable.Category() != "traffic" {
		t.Errorf("Category() = %q, want %q", reportable.Category(), "traffic")
	}
	if reportable.Summary() != "GET /api/users -> 200 (7ms)" {
		t.Errorf("Summary() = %q, want %q", reportable.Summary(), "GET /api/users -> 200 (7ms)")
	}
	if reportable.Severity() != "info" {
		t.Errorf("Severity() = %q, want %q", reportable.Severity(), "info")
	}
}

func TestTrafficEventSeverityMapping(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{status: 0, want: "critical"},   // dropped connection, no response
		{status: 500, want: "critical"}, // server error
		{status: 404, want: "warning"},
		{status: 200, want: "info"},
	}
	for _, tc := range cases {
		evt := TrafficEvent{Status: tc.status}
		if got := evt.Severity(); got != tc.want {
			t.Errorf("Status %d Severity() = %q, want %q", tc.status, got, tc.want)
		}
	}
}
