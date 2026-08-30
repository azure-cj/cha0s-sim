package stress

import (
	"context"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Config describes a single sustained-RPS load run. Scenario shaping
// (ramp/step/spike) is deliberately absent here — it is the next task and must
// not be built prematurely.
type Config struct {
	TargetURL      string
	TargetRPS      float64       // requests per second, sustained for Duration
	Duration       time.Duration // total wall-clock run length
	Concurrency    int           // bounded worker pool size
	RequestTimeout time.Duration // per-request context timeout
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

// Run fires requests at the configured sustained RPS until EITHER the caller's
// context is cancelled OR cfg.Duration elapses (whichever comes first), then
// returns a final metrics snapshot.
//
// Each of the Concurrency worker goroutines loops on limiter.Wait(ctx): the
// limiter paces aggregate firing rate across the whole pool, so the request
// cadence stays at TargetRPS regardless of worker count (workers exist to bound
// in-flight parallelism, not to multiply the rate). A request is a GET against
// TargetURL with a per-request context bounded by cfg.RequestTimeout.
func (e *Engine) Run(ctx context.Context) MetricsSnapshot {
	runCtx, cancel := context.WithTimeout(ctx, e.cfg.Duration)
	defer cancel()

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