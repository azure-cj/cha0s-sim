package stress

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newScenarioServer serves a fake login endpoint and a protected endpoint that
// records the Authorization header and path of every request it receives, so
// tests can assert the token extracted at login was genuinely carried into the
// later step.
func newScenarioServer(t *testing.T) (*httptest.Server, *atomic.Int64, *[]string, *[]string, *sync.Mutex) {
	t.Helper()
	var logins atomic.Int64
	var mu sync.Mutex
	var auths []string
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			logins.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"token":"abc123","id":"42"}`)
		default:
			mu.Lock()
			auths = append(auths, r.Header.Get("Authorization"))
			paths = append(paths, r.URL.Path)
			mu.Unlock()
			_, _ = io.WriteString(w, "authed")
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &logins, &auths, &paths, &mu
}

func TestScenarioEngineExtractsAndCarriesVariablesEndToEnd(t *testing.T) {
	srv, logins, auths, paths, mu := newScenarioServer(t)

	e := NewScenarioEngine(ScenarioConfig{
		BaseURL: srv.URL,
		Scenario: Scenario{
			Name: "login-flow",
			Steps: []Step{
				{
					Name:        "login",
					Method:      http.MethodPost,
					Path:        "/login",
					ExtractJSON: map[string]string{"authToken": "token", "id": "id"},
					ThinkTime:   5 * time.Millisecond,
				},
				{
					Name:      "protected",
					Method:    http.MethodGet,
					Path:      "/protected/{{id}}",
					Headers:   map[string]string{"Authorization": "Bearer {{authToken}}"},
					ThinkTime: 5 * time.Millisecond,
				},
			},
		},
		VirtualUsers:   3,
		Duration:       800 * time.Millisecond,
		RequestTimeout: 2 * time.Second,
	}, NewClientPool(64))

	start := time.Now()
	snap := e.Run(context.Background())
	elapsed := time.Since(start)

	login, hasLogin := snap.StepSnapshots["login"]
	protected, hasProtected := snap.StepSnapshots["protected"]
	if !hasLogin {
		t.Fatal("StepSnapshots missing the 'login' step")
	}
	if !hasProtected {
		t.Fatal("StepSnapshots missing the 'protected' step")
	}

	if login.TotalRequests == 0 || protected.TotalRequests == 0 {
		t.Fatalf("steps recorded no requests: login=%d protected=%d", login.TotalRequests, protected.TotalRequests)
	}
	// Every full iteration performs exactly one login request; partial
	// iterations clipped at the run boundary can add logins but never remove
	// them, and can never out-produce the completed count.
	if login.TotalRequests < snap.TotalIterations {
		t.Errorf("login requests (%d) < TotalIterations (%d); every completed iteration must contain a login",
			login.TotalRequests, snap.TotalIterations)
	}
	if protected.TotalRequests > login.TotalRequests {
		t.Errorf("protected requests (%d) > login requests (%d); each protected request depends on a prior login",
			protected.TotalRequests, login.TotalRequests)
	}
	if snap.TotalIterations < 1 || snap.TotalIterations > 2000 {
		t.Errorf("TotalIterations = %d, want within [1, 2000] for a 800ms 3-VU run", snap.TotalIterations)
	}
	if snap.ExtractionFailures != 0 {
		t.Errorf("ExtractionFailures = %d, want 0 (every login returned a valid token/id)", snap.ExtractionFailures)
	}

	// The proof of extraction+substitution working end to end: every request
	// that hit the protected endpoint must have carried the token extracted
	// from the login response, and the {{id}} substitution must have landed in
	// the path. The server may legitimately see a request more than once (an
	// http.Client retry after a failed connection reuse), so compare against
	// >= the recorded count rather than ==.
	mu.Lock()
	defer mu.Unlock()
	if len(*auths) < int(protected.TotalRequests) {
		t.Errorf("server saw %d protected requests, but %d were recorded: retry/bookkeeping mismatch",
			len(*auths), protected.TotalRequests)
	}
	for i, a := range *auths {
		if a != "Bearer abc123" {
			t.Errorf("protected request %d carried Authorization %q, want %q (extracted token not substituted)",
				i, a, "Bearer abc123")
		}
	}
	for i, p := range *paths {
		if p != "/protected/42" {
			t.Errorf("protected request %d hit %q, want %q ({{id}} not substituted or not extracted)",
				i, p, "/protected/42")
		}
	}
	if got := logins.Load(); got < snap.TotalIterations {
		t.Errorf("server saw %d logins, want >= TotalIterations (%d)", got, snap.TotalIterations)
	}
	if elapsed > 5*time.Second {
		t.Errorf("Run returned after %v, want prompt return after Duration", elapsed)
	}
	t.Logf("scenario run: iterations=%d login=%d protected=%d extractionFails=%d protectedSeen=%d in %v",
		snap.TotalIterations, login.TotalRequests, protected.TotalRequests, snap.ExtractionFailures, len(*auths), elapsed)
}

func TestScenarioEngineUnsubstitutedPlaceholderReachesWire(t *testing.T) {
	// A step that references a variable that was never set must send the
	// literal {{name}} placeholder in the request — a visible failure signal,
	// not silent data corruption.
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"token":"abc123"}`)
			return
		}
		mu.Lock()
		seen = append(seen, fmt.Sprintf("%s %s", r.URL.Path, r.Header.Get("X-Token")))
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)

	e := NewScenarioEngine(ScenarioConfig{
		BaseURL: srv.URL,
		Scenario: Scenario{
			Name: "unset-var",
			Steps: []Step{
				{
					Name:        "login",
					Method:      http.MethodGet,
					Path:        "/login",
					ExtractJSON: map[string]string{"token": "token"},
					ThinkTime:   2 * time.Millisecond,
				},
				{
					Name:    "protected",
					Method:  http.MethodGet,
					Path:    "/protected/{{unset}}",
					Headers: map[string]string{"X-Token": "{{token}}"},
				},
			},
		},
		VirtualUsers:   1,
		Duration:       300 * time.Millisecond,
		RequestTimeout: 2 * time.Second,
	}, NewClientPool(16))

	snap := e.Run(context.Background())

	if snap.TotalIterations < 1 {
		t.Fatalf("TotalIterations = %d, want >= 1", snap.TotalIterations)
	}
	if snap.ExtractionFailures != 0 {
		t.Errorf("ExtractionFailures = %d, want 0 (token itself was extractable)", snap.ExtractionFailures)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) == 0 {
		t.Fatal("server saw no protected requests; substitution behavior unverified")
	}
	for i, got := range seen {
		if got != "/protected/{{unset}} abc123" {
			t.Errorf("request %d = %q, want %q (unset var must stay literal while set var substitutes)",
				i, got, "/protected/{{unset}} abc123")
		}
	}
}

func TestScenarioEngineBadLoginsDoNotKillIterations(t *testing.T) {
	// A login that returns non-JSON makes extraction fail, but the virtual
	// user must keep going: the failure is counted, not fatal.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "definitely not json")
	}))
	t.Cleanup(srv.Close)

	e := NewScenarioEngine(ScenarioConfig{
		BaseURL: srv.URL,
		Scenario: Scenario{
			Name: "bad-extract",
			Steps: []Step{
				{
					Name:        "login",
					Method:      http.MethodPost,
					Path:        "/login",
					ExtractJSON: map[string]string{"authToken": "token"},
				},
				{
					Name:   "protected",
					Method: http.MethodGet,
					Path:   "/protected/{{authToken}}",
					Headers: map[string]string{"Authorization": "Bearer {{authToken}}"},
				},
			},
		},
		VirtualUsers:   2,
		Duration:       400 * time.Millisecond,
		RequestTimeout: 2 * time.Second,
	}, NewClientPool(16))

	snap := e.Run(context.Background())

	if snap.TotalIterations < 1 {
		t.Errorf("TotalIterations = %d, want >= 1 (a failed extraction must not halt the run)", snap.TotalIterations)
	}
	if snap.ExtractionFailures == 0 {
		t.Errorf("ExtractionFailures = 0, want > 0 (non-JSON login bodies cannot yield a token)")
	}
	p, ok := snap.StepSnapshots["protected"]
	if !ok || p.TotalRequests == 0 {
		t.Errorf("protected step ran no requests; want the step to keep firing after failed extractions (got ok=%v, reqs=%d)",
			ok, p.TotalRequests)
	}
}

func TestScenarioEngineCancellationPrompt(t *testing.T) {
	srv, _, _, _, _ := newScenarioServer(t)

	e := NewScenarioEngine(ScenarioConfig{
		BaseURL: srv.URL,
		Scenario: Scenario{
			Name: "cancel",
			Steps: []Step{
				{Name: "login", Method: http.MethodGet, Path: "/login",
					ExtractJSON: map[string]string{"token": "token"}},
				{Name: "protected", Method: http.MethodGet, Path: "/protected/{{token}}"},
			},
		},
		VirtualUsers:   4,
		Duration:       10 * time.Second, // longer than the cancel: cancel must win
		RequestTimeout: 2 * time.Second,
	}, NewClientPool(64))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var snap ScenarioSnapshot
	go func() {
		defer close(done)
		snap = e.Run(ctx)
	}()

	time.Sleep(300 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("Run did not return within 1s of context cancellation (10s duration should be short-circuited)")
	}
	if snap.TotalIterations == 0 {
		t.Errorf("TotalIterations = 0, want > 0 (iterations must have completed before the cancel)")
	}
}

func TestScenarioEngineReportsProgress(t *testing.T) {
	srv, _, _, _, _ := newScenarioServer(t)

	var mu sync.Mutex
	var progress []ScenarioSnapshot

	e := NewScenarioEngine(ScenarioConfig{
		BaseURL: srv.URL,
		Scenario: Scenario{
			Name: "progress",
			Steps: []Step{
				{Name: "login", Method: http.MethodGet, Path: "/login",
					ExtractJSON: map[string]string{"token": "token"}},
				{Name: "protected", Method: http.MethodGet, Path: "/protected/{{token}}"},
			},
		},
		VirtualUsers:   2,
		Duration:       1200 * time.Millisecond, // 500ms ticker -> ~2 ticks
		RequestTimeout: 2 * time.Second,
		OnProgress: func(s ScenarioSnapshot) {
			mu.Lock()
			progress = append(progress, s)
			mu.Unlock()
		},
	}, NewClientPool(32))

	final := e.Run(context.Background())

	mu.Lock()
	n := len(progress)
	last := ScenarioSnapshot{}
	if n > 0 {
		last = progress[n-1]
	}
	mu.Unlock()

	if n < 2 {
		t.Errorf("OnProgress fired %d times over a 1.2s run (500ms ticker), want >= 2", n)
	}
	if _, ok := final.StepSnapshots["login"]; !ok {
		t.Error("final snapshot missing the login step")
	}
	if n > 0 && last.TotalIterations > final.TotalIterations {
		t.Errorf("last progress shows %d iterations but final shows %d: snapshots must be cumulative",
			last.TotalIterations, final.TotalIterations)
	}
}