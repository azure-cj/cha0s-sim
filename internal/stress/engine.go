package stress

import (
	"context"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RPSShape computes the target request rate at a given point in a run. elapsed
// is how far into the run we are; total is the run's full Duration. Concrete
// shapes live in shapes.go (ContinuousRamp, SteppedRamp, Spike).
type RPSShape func(elapsed time.Duration, total time.Duration) float64

// Config describes a single load run.
type Config struct {
	TargetURL      string
	TargetRPS      float64       // requests per second (flat baseline / when Shape is nil)
	Duration       time.Duration // total wall-clock run length
	Concurrency    int           // bounded worker pool size
	RequestTimeout time.Duration // per-request context timeout
	// Shape makes the target RPS vary over time. nil means "flat rate at
	// TargetRPS for the whole Duration" — the original sustained-load behavior,
	// and also exactly what a Soak test is: target.Shape == nil with a long
	// Duration. No separate Soak constructor exists; that would be redundant.
	Shape RPSShape
}

// Engine owns everything a load run needs: the shared client pool, the metrics
// accumulator, and the shared rate limiter that paces all workers.
type Engine struct {
	cfg    Config
	client *http.Client
	metrics *Metrics
	limiter *rate.Limiter
}

// NewEngine builds an Engine from a config and a shared pool client. Burst is
// sized as roughly a tenth-of-a-second's worth of the target rate (never below
// 1), a small allowance that lets the pool reach steady state without making
// the ramp-up meaningless.
func NewEngine(cfg Config, client *http.Client) *Engine {
	burst := int(cfg.TargetRPS / 10)
	if burst < 1 {
		burst = 1
	}
	return &Engine{
		cfg:     cfg,
		client:  client,
		metrics: NewMetrics(),
		limiter: rate.NewLimiter(rate.Limit(cfg.TargetRPS), burst),
	}
}

// Run fires requests at the shaped RPS until EITHER the caller's context is
// cancelled OR cfg.Duration elapses (whichever comes first), then returns a
// final metrics snapshot.
//
// Each of the Concurrency worker goroutines loops on limiter.Wait(ctx): the
// limiter paces aggregate firing rate across the whole pool, so the request
// cadence follows the limiter's current limit regardless of worker count
// (workers exist to bound in-flight parallelism, not to multiply the rate). A
// request is a GET against TargetURL with a per-request context bounded by
// cfg.RequestTimeout.
//
// When cfg.Shape is set, an additional goroutine re-computes the limiter's
// limit on a 100ms ticker, so the target RPS follows the shape over time. When
// cfg.Shape is nil the ticker is skipped entirely and the limiter keeps its
// construction-time rate: the flat-rate path is byte-for-byte the previous
// behavior.
func (e *Engine) Run(ctx context.Context) MetricsSnapshot {
	runCtx, cancel := context.WithTimeout(ctx, e.cfg.Duration)
	defer cancel()

	// shapeTicker re-evaluates the target rate; only spawned when a shape is
	// present (nil Shape keeps the fixed-rate path unchanged).
	if e.cfg.Shape != nil {
		start := time.Now()
		shapeCtx, stop := context.WithCancel(runCtx)
		done := make(chan struct{})
		go e.trackShape(shapeCtx, done, start)
		defer func() { stop(); <-done }()
	}

	var wg sync.WaitGroup
	for i := 0; i < e.cfg.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if err := e.limiter.Wait(runCtx); err != nil {
					return // runCtx done or cancelled: stop firing
				}
				e.fire(runCtx)
			}
		}()
	}
	wg.Wait()
	return e.metrics.Snapshot()
}

// trackShape updates the limiter's limit to shape(elapsed, total) on a 100ms
// ticker — frequent enough for smooth-feeling ramps, cheap enough to run for
// the lifetime of a Soak. It returns via done once ctx is cancelled.
func (e *Engine) trackShape(ctx context.Context, done chan<- struct{}, start time.Time) {
	defer close(done)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case now := <-ticker.C:
			elapsed := now.Sub(start)
			limit := e.cfg.Shape(elapsed, e.cfg.Duration)
			if limit <= 0 {
				limit = 0 // SetLimit panics on negative; clamp to a hard stop
			}
			e.limiter.SetLimit(rate.Limit(limit))
		case <-ctx.Done():
			return
		}
	}
}

// fire issues one timed GET and records its outcome. Errors and 5xx responses
// both count as errors.
func (e *Engine) fire(runCtx context.Context) {
	start := time.Now()
	reqCtx := runCtx
	var cancel context.CancelFunc
	if e.cfg.RequestTimeout > 0 {
		reqCtx, cancel = context.WithTimeout(runCtx, e.cfg.RequestTimeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, e.cfg.TargetURL, nil)
	isError := false
	if err == nil {
		resp, err := e.client.Do(req)
		if err != nil {
			isError = true
		} else {
			_ = resp.Body.Close()
			isError = resp.StatusCode >= 500
		}
	} else {
		isError = true
	}

	latencyMs := time.Since(start).Milliseconds()
	if latencyMs < 1 {
		latencyMs = 1 // histogram floor is 1ms; sub-ms responses clamp to 1
	}
	e.metrics.Record(latencyMs, isError)
}