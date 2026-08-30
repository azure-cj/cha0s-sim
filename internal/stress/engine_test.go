package stress

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
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