package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"cha0s-sim/internal/config"
	"cha0s-sim/internal/security"
)

// collectedEvents partitions everything that flowed through a sink into its
// traffic events and findings so a test can assert session attribution on both.
type collectedEvents struct {
	traffic  []TrafficEvent
	findings []security.Finding
}

func drainSink(t *testing.T, sink *collectingSink) collectedEvents {
	t.Helper()
	var out collectedEvents
	for {
		select {
		case evt := <-sink.events:
			switch v := evt.(type) {
			case security.Finding:
				out.findings = append(out.findings, v)
			case TrafficEvent:
				out.traffic = append(out.traffic, v)
			}
		case <-time.After(5 * time.Second):
			return out
		}
	}
}

// sessionProxy starts a NewServerInstance for the given session name and mode,
// pointing at a backend that serves a plain "hello" body with no security
// headers (so the HeaderValidator flags 4 missing_header findings whenever the
// scanner set runs). It returns the proxy's httptest server and the sink it
// feeds, so tests can drain and assert on the emitted events.
func sessionProxy(t *testing.T, backendURL string, store *config.Store, mode PipelineMode, sessionName string) (*httptest.Server, *collectingSink) {
	t.Helper()
	target, err := url.Parse(backendURL)
	if err != nil {
		t.Fatalf("failed to parse backend URL %q: %v", backendURL, err)
	}
	sink := newCollectingSink()
	srv := NewServerInstance(0, target, false, false, false, store, mode, sessionName, sink)
	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)
	return ts, sink
}

// TestSessionAttribution carries the session name onto emitted traffic events
// AND security findings, for both named sessions plus the CLI/no-session path
// (which must produce empty SessionName values).
func TestSessionAttribution(t *testing.T) {
	// A firing rule makes the chaos session emit an effected traffic event;
	// the security session ignores chaos rules regardless.
	store := writeStore(t, `
rules:
  - name: "override route"
    enabled: true
    path: "/api/users"
    error_rate: 1.0
    status_override:
      code: 503
`)

	cases := []struct {
		name         string
		mode         PipelineMode
		wantFindings int // default backend yields 4 missing_header findings wherever scanners run
	}{
		{name: "chaos", mode: PipelineChaosOnly, wantFindings: 0},
		{name: "security", mode: PipelineSecurityOnly, wantFindings: 4},
		{name: "cli", mode: PipelineFull, wantFindings: 4},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, backendURL := newBackend(t)
			ts, sink := sessionProxy(t, backendURL, store, tc.mode, tc.name)

			resp, err := http.Get(ts.URL + "/api/users")
			if err != nil {
				t.Fatalf("request through session proxy failed: %v", err)
			}
			resp.Body.Close()

			got := drainSink(t, sink)

			if len(got.traffic) != 1 {
				t.Fatalf("got %d traffic events, want 1", len(got.traffic))
			}
			if got.traffic[0].SessionName != tc.name {
				t.Errorf("traffic SessionName = %q, want %q", got.traffic[0].SessionName, tc.name)
			}

			if len(got.findings) != tc.wantFindings {
				t.Fatalf("got %d findings, want %d", len(got.findings), tc.wantFindings)
			}
			for i, f := range got.findings {
				if f.SessionName != tc.name {
					t.Errorf("finding %d SessionName = %q, want %q", i, f.SessionName, tc.name)
				}
			}
		})
	}
}

// TestFindingSessionNameStampedInFullPipeline verifies findings are attributed
// even through a server that runs the full pipeline with a named session (the
// chaos-only mode never emits findings, so the "chaos" attribution for
// findings would otherwise be untested).
func TestFindingSessionNameStampedInFullPipeline(t *testing.T) {
	_, backendURL := newBackend(t)
	ts, sink := sessionProxy(t, backendURL, nil, PipelineFull, "chaos")

	resp, err := http.Get(ts.URL + "/api/users")
	if err != nil {
		t.Fatalf("request through session proxy failed: %v", err)
	}
	resp.Body.Close()

	got := drainSink(t, sink)
	if len(got.findings) != 4 {
		t.Fatalf("got %d findings, want 4", len(got.findings))
	}
	for i, f := range got.findings {
		if f.SessionName != "chaos" {
			t.Errorf("finding %d SessionName = %q, want %q", i, f.SessionName, "chaos")
		}
	}
}