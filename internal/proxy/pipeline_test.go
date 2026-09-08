package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"cha0s-sim/internal/security"
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

// emittedSnapshot partitions everything that flowed through a sink into
// security findings and traffic events.
type emittedSnapshot struct {
	findings int
	traffic  []TrafficEvent
}

func collectEmitted(sink *collectingSink) emittedSnapshot {
	var snap emittedSnapshot
	for {
		select {
		case evt := <-sink.events:
			switch v := evt.(type) {
			case security.Finding:
				snap.findings++
			case TrafficEvent:
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

// TestPipelineChaosOnlyFiresChaosButNotScanner proves that in chaos-only mode
// a fired rule's effect is applied to the response, but NO security finding is
// emitted even though the backend response would trigger the header scanner
// (it serves no security headers, which the full pipeline reports on).
func TestPipelineChaosOnlyFiresChaosButNotScanner(t *testing.T) {
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
	srv := NewServerInstance(0, target, false, false, false, store, PipelineChaosOnly, "", sink)
	ts := newTestServer(t, srv.Handler)

	resp, err := http.Get(ts.URL + "/api/users")
	if err != nil {
		t.Fatalf("request through chaos-only proxy failed: %v", err)
	}
	defer resp.Body.Close()

	// Chaos rule fires: the status override applies.
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 (chaos override must apply in chaos-only mode)", resp.StatusCode)
	}

	snap := collectEmitted(sink)

	// Traffic event reflects the fired override effect.
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

	// No scanner runs: even though the backend response has no security
	// headers, zero findings should have been emitted.
	if snap.findings != 0 {
		t.Errorf("chaos-only mode emitted %d findings, want 0 (scanners must not run)", snap.findings)
	}
}

// TestPipelineSecurityOnlyScansButIgnoresChaos proves that in security-only
// mode a configured chaos rule that would fire is NOT applied (the response
// passes through unmodified from the backend), yet a scannable issue still
// produces a Finding.
func TestPipelineSecurityOnlyScansButIgnoresChaos(t *testing.T) {
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
	srv := NewServerInstance(0, target, false, false, false, store, PipelineSecurityOnly, "", sink)
	ts := newTestServer(t, srv.Handler)

	resp, err := http.Get(ts.URL + "/api/users")
	if err != nil {
		t.Fatalf("request through security-only proxy failed: %v", err)
	}
	defer resp.Body.Close()

	// Chaos rule must NOT fire: response passes through unmodified (200).
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (chaos override must NOT apply in security-only mode)", resp.StatusCode)
	}

	snap := collectEmitted(sink)

	// Traffic event reflects a clean pass-through with no effects.
	if len(snap.traffic) != 1 {
		t.Fatalf("got %d traffic events, want 1", len(snap.traffic))
	}
	evt := snap.traffic[0]
	if evt.Status != http.StatusOK {
		t.Errorf("traffic status = %d, want 200", evt.Status)
	}
	if len(evt.Effects) != 0 {
		t.Errorf("Effects = %v, want none (chaos skipped)", evt.Effects)
	}

	// Scanner DOES run: the missing security headers produce findings.
	if snap.findings == 0 {
		t.Error("security-only mode emitted no findings, want >= 1 (scanners must run)")
	}
}

// TestPipelineFullFiresBothChaosAndScanner confirms the full pipeline runs
// both phases together (a fired rule mutates the response AND the header
// scanner reports on the backend's missing security headers), i.e. existing
// behavior is preserved unchanged.
func TestPipelineFullFiresBothChaosAndScanner(t *testing.T) {
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
	srv := NewServerInstance(0, target, false, false, false, store, PipelineFull, "", sink)
	ts := newTestServer(t, srv.Handler)

	resp, err := http.Get(ts.URL + "/api/users")
	if err != nil {
		t.Fatalf("request through full-pipeline proxy failed: %v", err)
	}
	defer resp.Body.Close()

	// Chaos fires.
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

	// Scanner also fires on the (mutated) response.
	if snap.findings == 0 {
		t.Error("full pipeline emitted no findings, want >= 1 (scanners must run)")
	}
}
