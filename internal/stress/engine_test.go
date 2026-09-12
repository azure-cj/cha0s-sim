package stress

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// newFakeTarget serves 200s normally and a 500 on every nth request (to
// exercise the error-rate path).
func newFakeTarget(t *testing.T, every int) *httptest.Server {
	t.Helper()
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&hits, 1)
		if every > 0 && n%int64(every) == 0 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestEngineRunFiresApproxRequestRate(t *testing.T) {
	srv := newFakeTarget(t, 0)

	e := NewEngine(Config{
		TargetURL:      srv.URL,
		TargetRPS:      20,
		Duration:       500 * time.Millisecond,
		Concurrency:    5,
		RequestTimeout: 2 * time.Second,
	}, NewClientPool(64))

	start := time.Now()
	snap := e.Run(context.Background())
	elapsed := time.Since(start)

	// 20 RPS for 0.5s ~= 10 requests, plus burst allowance. Rate limiting isn't
	// metronomically exact, so allow a generous window.
	if snap.TotalRequests < 4 || snap.TotalRequests > 30 {
		t.Errorf("TotalRequests = %d, want roughly 10 (20 RPS x 0.5s)", snap.TotalRequests)
	}
	// No 500s are simulated, so the only error source is a request clipped by
	// the run deadline: runCtx's timeout cancels any in-flight request that
	// straddles Duration's end, which can legitimately account for at most one
	// in-flight request per worker. Assert a bounded (not zero) error count.
	if snap.TotalErrors > 5 {
		t.Errorf("TotalErrors = %d, want <= 5 (only run-boundary cancellations, no simulated 500s)", snap.TotalErrors)
	}
	if snap.RPS <= 0 {
		t.Errorf("RPS = %v, want > 0", snap.RPS)
	}
	// The run must return promptly once Duration elapses — a hang here would
	// be a real engine bug, not flakiness.
	if elapsed > 5*time.Second {
		t.Errorf("Run returned after %v, want < 5s (prompt return after Duration)", elapsed)
	}
	t.Logf("engine run: %d requests in %v (%.1f RPS), percentiles p50=%d p95=%d p99=%d",
		snap.TotalRequests, elapsed, snap.RPS, snap.P50Ms, snap.P95Ms, snap.P99Ms)
}

func TestEngineRunTracksSimulatedErrors(t *testing.T) {
	srv := newFakeTarget(t, 3) // every 3rd request -> 500

	e := NewEngine(Config{
		TargetURL:      srv.URL,
		TargetRPS:      30,
		Duration:       600 * time.Millisecond,
		Concurrency:    5,
		RequestTimeout: 2 * time.Second,
	}, NewClientPool(64))

	snap := e.Run(context.Background())

	if snap.TotalRequests == 0 {
		t.Fatal("no requests fired; cannot assess error rate")
	}
	if snap.TotalErrors == 0 {
		t.Errorf("TotalErrors = 0, want some (every 3rd request is a 500)")
	}
	if snap.ErrorRate <= 0 || snap.ErrorRate > 1 {
		t.Errorf("ErrorRate = %v, want within (0,1]", snap.ErrorRate)
	}
	// Every 500 response is a 5xx status, so every error must carry the
	// status_500 category (no transport errors occur against the fake server).
	if snap.ErrorCategories["status_500"] == 0 {
		t.Errorf("ErrorCategories[status_500] = 0, want > 0: %v", snap.ErrorCategories)
	}
	sum := int64(0)
	for _, n := range snap.ErrorCategories {
		sum += n
	}
	if sum != snap.TotalErrors {
		t.Errorf("ErrorCategories sum = %d, want %d (every error must be categorized)", sum, snap.TotalErrors)
	}
}

// timeoutErr implements net.Error so the classifier's typed Timeout() check
// picks it up, mimicking a client.Do deadline expiry.
type timeoutErr struct{}

func (timeoutErr) Error() string   { return "request timed out" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

// stressMixTransport deterministically varies transport outcomes across one
// run: connection refused, timeout, 503, 404, then 200, cycling. Dialing never
// actually happens — the RoundTripper fabricates responses/errors.
type stressMixTransport struct {
	n atomic.Int64
}

func (tr *stressMixTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	switch tr.n.Add(1) % 5 {
	case 0:
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	case 1:
		return nil, timeoutErr{}
	case 2:
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: http.NoBody, Header: make(http.Header)}, nil
	case 3:
		return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody, Header: make(http.Header)}, nil
	default:
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: make(http.Header)}, nil
	}
}

func TestEngineRunCategorizesMixedErrors(t *testing.T) {
	transport := &stressMixTransport{}
	e := NewEngine(Config{
		TargetURL:      "http://target.invalid", // RoundTripper never dials it
		TargetRPS:      30,
		Duration:       600 * time.Millisecond,
		Concurrency:    5,
		RequestTimeout: 2 * time.Second,
	}, &http.Client{Transport: transport})

	snap := e.Run(context.Background())

	if snap.TotalRequests == 0 {
		t.Fatal("no requests fired; cannot assess categorization")
	}
	if snap.ErrorCategories[ErrConnectionRefused] == 0 {
		t.Errorf("ErrorCategories[%q] = 0, want > 0 (conn-refused requests)", ErrConnectionRefused)
	}
	if snap.ErrorCategories[ErrTimeout] == 0 {
		t.Errorf("ErrorCategories[%q] = 0, want > 0 (timeout requests)", ErrTimeout)
	}
	if snap.ErrorCategories["status_503"] == 0 {
		t.Errorf("ErrorCategories[status_503] = 0, want > 0 (5xx requests)")
	}
	if _, ok := snap.ErrorCategories["status_404"]; ok {
		t.Errorf("ErrorCategories[status_404] present: 4xx must NOT count as errors (original boundary was >= 500 only)")
	}
	sum := int64(0)
	for _, n := range snap.ErrorCategories {
		sum += n
	}
	if sum != snap.TotalErrors {
		t.Errorf("ErrorCategories sum = %d, want %d (every error categorized, 4xx excluded)", sum, snap.TotalErrors)
	}
}

func TestEngineRunCategorizesConnectionRefused(t *testing.T) {
	// A listener that exists, then vanishes: dialing its address yields a real
	// ECONNREFUSED wrapped by the real http.Client transport (not a fake).
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := "http://" + srv.Listener.Addr().String()
	srv.Close()

	e := NewEngine(Config{
		TargetURL:      deadURL,
		TargetRPS:      40,
		Duration:       300 * time.Millisecond,
		Concurrency:    4,
		RequestTimeout: 2 * time.Second,
	}, NewClientPool(64))

	snap := e.Run(context.Background())

	if snap.TotalRequests == 0 {
		t.Fatal("no requests fired against the dead endpoint")
	}
	if snap.TotalErrors == 0 {
		t.Fatal("expected errors dialing a closed endpoint")
	}
	if snap.ErrorCategories[ErrConnectionRefused] == 0 {
		t.Errorf("ErrorCategories[%q] = 0, want > 0 (real dial errors): %v", ErrConnectionRefused, snap.ErrorCategories)
	}
	sum := int64(0)
	for _, n := range snap.ErrorCategories {
		sum += n
	}
	if sum != snap.TotalErrors {
		t.Errorf("ErrorCategories sum = %d, want %d (every dial error must be categorized)", sum, snap.TotalErrors)
	}
}

func TestEngineRunRespectsContextCancellation(t *testing.T) {
	srv := newFakeTarget(t, 0)

	e := NewEngine(Config{
		TargetURL:      srv.URL,
		TargetRPS:      50,
		Duration:       5 * time.Second, // longer than the cancel: cancel must win
		Concurrency:    5,
		RequestTimeout: 2 * time.Second,
	}, NewClientPool(64))

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(150*time.Millisecond, cancel)

	start := time.Now()
	snap := e.Run(ctx)
	elapsed := time.Since(start)

	if elapsed > 3*time.Second {
		t.Errorf("Run ignored context cancellation; returned after %v", elapsed)
	}
	if snap.TotalRequests == 0 {
		t.Log("context cancelled before any request fired")
	}
}

func TestEngineRunWithShapeFiresRequests(t *testing.T) {
	// Integration test for the shaped path: the ticker-based rate updates must
	// not break engine execution. Precise per-shape values are covered in
	// shapes_test.go; here we just prove a shaped run completes promptly with
	// traffic flowing on a nominally-10x trajectory (5 -> 50 RPS over 1s).
	srv := newFakeTarget(t, 0)

	e := NewEngine(Config{
		TargetURL:      srv.URL,
		TargetRPS:      5,
		Duration:       1 * time.Second,
		Concurrency:    5,
		RequestTimeout: 2 * time.Second,
		Shape:          ContinuousRamp(5, 50),
	}, NewClientPool(64))

	start := time.Now()
	snap := e.Run(context.Background())
	elapsed := time.Since(start)

	if snap.TotalRequests == 0 {
		t.Error("shaped run fired no requests; want TotalRequests > 0")
	}
	if elapsed > 5*time.Second {
		t.Errorf("shaped Run returned after %v, want prompt return after Duration", elapsed)
	}
	t.Logf("shaped engine run: %d requests in %v (avg %.1f RPS)", snap.TotalRequests, elapsed, snap.RPS)
	if snap.RPS <= 0 {
		t.Errorf("RPS = %v, want > 0", snap.RPS)
	}
}

func TestEngineRunReportsProgress(t *testing.T) {
	srv := newFakeTarget(t, 0)

	var mu sync.Mutex
	var progress []MetricsSnapshot

	e := NewEngine(Config{
		TargetURL:      srv.URL,
		TargetRPS:      30,
		Duration:       1200 * time.Millisecond, // 500ms ticker -> ~2 ticks
		Concurrency:    5,
		RequestTimeout: 2 * time.Second,
		OnProgress: func(s MetricsSnapshot) {
			// Called from the engine's own goroutine; the test must not touch
			// the slice unsynchronized.
			mu.Lock()
			progress = append(progress, s)
			mu.Unlock()
		},
	}, NewClientPool(64))

	final := e.Run(context.Background())

	mu.Lock()
	n := len(progress)
	last := MetricsSnapshot{}
	if n > 0 {
		last = progress[n-1]
	}
	mu.Unlock()

	if n < 2 {
		t.Errorf("OnProgress fired %d times over a 1.2s run (500ms ticker), want >= 2", n)
	}
	if final.TotalRequests == 0 {
		t.Error("run fired no requests; progress/result meaningless")
	}
	for i, s := range progress {
		if s.TotalRequests < 0 || s.RPS < 0 {
			t.Errorf("progress[%d] has negative values: %+v", i, s)
		}
	}
	if n > 0 && last.TotalRequests > final.TotalRequests {
		t.Errorf("last progress snapshot shows %d requests but final shows %d: snapshots must reflect cumulative state", last.TotalRequests, final.TotalRequests)
	}
}

func TestEngineRunCancelsPromptly(t *testing.T) {
	srv := newFakeTarget(t, 0)

	e := NewEngine(Config{
		TargetURL:      srv.URL,
		TargetRPS:      20,
		Duration:       10 * time.Second,
		Concurrency:    5,
		RequestTimeout: 2 * time.Second,
	}, NewClientPool(64))

	ctx, cancel := context.WithCancel(context.Background())

	// final is written by the goroutine BEFORE done is closed (defer runs after
	// the assignment), so reading it after <-done has a happens-before edge.
	done := make(chan struct{})
	var final MetricsSnapshot
	go func() {
		defer close(done)
		final = e.Run(ctx)
	}()

	time.Sleep(300 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// returned promptly — good
	case <-time.After(1 * time.Second):
		t.Fatal("Run did not return within 1s of context cancellation (10s duration should be short-circuited)")
	}

	if final.TotalRequests == 0 {
		t.Errorf("Run returned %d requests after cancellation; want > 0 (requests must have fired before the cancel)", final.TotalRequests)
	}
}
