package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func newTestURL(raw string) (*url.URL, error) {
	return url.Parse(raw)
}

func newTestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

// emittedSnapshot partitions everything that flowed through a sink into its
// traffic events.
type emittedSnapshot struct {
	traffic []TrafficEvent
}

func collectEmitted(sink *collectingSink) emittedSnapshot {
	var snap emittedSnapshot
	for {
		select {
		case evt := <-sink.events:
			if v, ok := evt.(TrafficEvent); ok {
				snap.traffic = append(snap.traffic, v)
			}
		case <-time.After(5 * time.Second):
			return snap
		}
	}
}

func containsEffectsKind(effects []ChaosEffect, want string) bool {
	for _, e := range effects {
		if e.Kind == want {
			return true
		}
	}
	return false
}

// TestPipelineChaosOnlyFiresChaos confirms a fired rule's effect is applied to
// the response in chaos-only mode.
func TestPipelineChaosOnlyFiresChaos(t *testing.T) {
	_, backendURL := newBackend(t)
	store := writeStore(t, `
rules:
  - name: "override route"
    enabled: true
    path: "/api/users"
    error_rate: 1.0
    status_override:
      code: 503
`)
	sink := newCollectingSink()

	target, _ := newTestURL(backendURL)
	srv := NewServerInstance(0, target, false, false, false, store, PipelineChaosOnly, "", nil, sink)
	ts := newTestServer(t, srv.Handler)

	resp, err := http.Get(ts.URL + "/api/users")
	if err != nil {
		t.Fatalf("request through chaos-only proxy failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 (chaos override must apply in chaos-only mode)", resp.StatusCode)
	}

	snap := collectEmitted(sink)
	if len(snap.traffic) != 1 {
		t.Fatalf("got %d traffic events, want 1", len(snap.traffic))
	}
	evt := snap.traffic[0]
	if evt.Status != http.StatusServiceUnavailable {
		t.Errorf("traffic status = %d, want 503", evt.Status)
	}
	if !containsEffectsKind(evt.Effects, "override") {
		t.Errorf("Effects = %v, want an override effect", evt.Effects)
	}
}

// TestPipelineFullFiresChaos confirms the full pipeline applies a fired rule's
// effect to the response.
func TestPipelineFullFiresChaos(t *testing.T) {
	_, backendURL := newBackend(t)
	store := writeStore(t, `
rules:
  - name: "override route"
    enabled: true
    path: "/api/users"
    error_rate: 1.0
    status_override:
      code: 503
`)
	sink := newCollectingSink()

	target, _ := newTestURL(backendURL)
	srv := NewServerInstance(0, target, false, false, false, store, PipelineFull, "", nil, sink)
	ts := newTestServer(t, srv.Handler)

	resp, err := http.Get(ts.URL + "/api/users")
	if err != nil {
		t.Fatalf("request through full-pipeline proxy failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 (chaos override fires in full pipeline)", resp.StatusCode)
	}

	snap := collectEmitted(sink)
	if len(snap.traffic) != 1 {
		t.Fatalf("got %d traffic events, want 1", len(snap.traffic))
	}
	if !containsEffectsKind(snap.traffic[0].Effects, "override") {
		t.Errorf("Effects = %v, want an override effect", snap.traffic[0].Effects)
	}
}
