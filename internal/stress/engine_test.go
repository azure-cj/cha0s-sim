package stress

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	if snap.ErrorRate != 0 {
		t.Errorf("ErrorRate = %v, want 0 (no 500s simulated)", snap.ErrorRate)
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