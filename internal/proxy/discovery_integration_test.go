package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"cha0s-sim/internal/discovery"
)

// discoveryProxy starts a NewServerInstance wired to a real discovery.Registry
// (exactly like the desktop app's StartSession does — registry passed alongside
// the sink) and returns the httptest server, the sink it feeds (for draining so
// events never stall), and the registry it observes into.
func discoveryProxy(t *testing.T, backendURL string, mode PipelineMode, sessionName string, reg *discovery.Registry) (*httptest.Server, *collectingSink) {
	t.Helper()
	target, err := url.Parse(backendURL)
	if err != nil {
		t.Fatalf("failed to parse backend URL %q: %v", backendURL, err)
	}
	sink := newCollectingSink()
	srv := NewServerInstance(0, target, false, false, false, nil, mode, sessionName, reg, sink)
	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)
	return ts, sink
}

// TestRegistryAccumulatesObservedEndpointsThroughServer verifies the full
// wiring: requests sent through a session-tagged server instance are recorded
// into the registry as (method, path) pairs, numeric ID segments collapse to
// the "{id}" placeholder, and repeated hits accumulate SeenCount instead of
// duplicating entries. Uses the real chaos session's pipeline mode
// (PipelineChaosOnly) so the backend's missing security headers produce no
// findings, matching production.
func TestRegistryAccumulatesObservedEndpointsThroughServer(t *testing.T) {
	_, backendURL := newBackend(t)
	reg := discovery.NewRegistry()
	ts, sink := discoveryProxy(t, backendURL, PipelineChaosOnly, "chaos", reg)

	for _, path := range []string{"/api/users", "/api/users", "/api/users/42", "/api/users/7", "/api/orders"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("request %s through proxy failed: %v", path, err)
		}
		resp.Body.Close()
	}
	// health check: requests actually flowed through the event layer
	if got := drainSink(t, sink); len(got.traffic) != 5 {
		t.Fatalf("got %d traffic events, want 5", len(got.traffic))
	}

	eps := reg.List("chaos")
	if len(eps) != 3 {
		t.Fatalf("got %d endpoints, want 3 (/api/users, /api/users/{id}, /api/orders)", len(eps))
	}
	got := map[string]int{}
	for _, e := range eps {
		got[e.Path] = e.SeenCount
		if e.SessionName != "chaos" {
			t.Errorf("endpoint %s SessionName = %q, want %q", e.Path, e.SessionName, "chaos")
		}
	}
	if got["/api/users"] != 2 {
		t.Errorf("/api/users SeenCount = %d, want 2", got["/api/users"])
	}
	// /api/users/42 and /api/users/7 both collapse to the same logical endpoint.
	if got["/api/users/{id}"] != 2 {
		t.Errorf("/api/users/{id} SeenCount = %d, want 2", got["/api/users/{id}"])
	}
	if got["/api/orders"] != 1 {
		t.Errorf("/api/orders SeenCount = %d, want 1", got["/api/orders"])
	}
	// Sorted by SeenCount descending.
	for i := 1; i < len(eps); i++ {
		if eps[i].SeenCount > eps[i-1].SeenCount {
			t.Errorf("endpoints not sorted by SeenCount descending: %+v", eps)
			break
		}
	}
}

// TestRegistrySeparatesSessionsThroughServer proves session isolation holds at
// the pipeline level: traffic through the "security" session is only visible in
// that session's list, never bleeding into "chaos", even when both share the
// same registry instance. Using each session's real pipeline mode also proves
// recording happens outside the chaos middleware — PipelineSecurityOnly skips
// the chaos layer entirely, yet still records (the record lives in the
// event-sink layer, which every mode passes through).
func TestRegistrySeparatesSessionsThroughServer(t *testing.T) {
	_, backendURL := newBackend(t)
	reg := discovery.NewRegistry()

	chaosTS, chaosSink := discoveryProxy(t, backendURL, PipelineChaosOnly, "chaos", reg)
	resp, err := http.Get(chaosTS.URL + "/api/users")
	if err != nil {
		t.Fatalf("request through chaos session failed: %v", err)
	}
	resp.Body.Close()

	secTS, secSink := discoveryProxy(t, backendURL, PipelineSecurityOnly, "security", reg)
	for _, path := range []string{"/health", "/health", "/login"} {
		resp, err := http.Get(secTS.URL + path)
		if err != nil {
			t.Fatalf("request %s through security session failed: %v", path, err)
		}
		resp.Body.Close()
	}

	// Drain both sinks: the security session's scanner emits findings plus a
	// traffic event per request, and a stalled sink would deadlock the test.
	_ = drainSink(t, chaosSink)
	if got := drainSink(t, secSink); len(got.traffic) != 3 {
		t.Fatalf("got %d security traffic events, want 3", len(got.traffic))
	}

	chaos := reg.List("chaos")
	if len(chaos) != 1 || chaos[0].Path != "/api/users" || chaos[0].SeenCount != 1 {
		t.Errorf("chaos list = %+v, want exactly [/api/users x1]", chaos)
	}

	sec := reg.List("security")
	if len(sec) != 2 {
		t.Fatalf("security list = %d endpoints, want 2", len(sec))
	}
	got := map[string]int{}
	for _, e := range sec {
		got[e.Path] = e.SeenCount
	}
	if got["/health"] != 2 {
		t.Errorf("/health SeenCount = %d, want 2", got["/health"])
	}
	if got["/login"] != 1 {
		t.Errorf("/login SeenCount = %d, want 1", got["/login"])
	}
}
