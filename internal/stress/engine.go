package stress

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"syscall"
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
	// OnProgress, when non-nil, receives a live in-progress MetricsSnapshot on
	// a 500ms ticker while Run executes. It is a side-channel only — Run still
	// returns the single final snapshot. nil disables progress reporting.
	OnProgress func(MetricsSnapshot)
}

// Engine owns everything a load run needs: the shared client pool, the metrics
// accumulator, and the shared rate limiter that paces all workers.
type Engine struct {
	cfg     Config
	client  *http.Client
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
// behavior. When cfg.OnProgress is set, another goroutine emits live snapshots
// on a 500ms ticker; Run's own return value is unaffected.
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

	// progressTicker reports live snapshots; only spawned when OnProgress is
	// set (nil keeps the previous behavior byte-for-byte, and Snapshot is safe
	// to call concurrently with Record — it is mutex-guarded in metrics.go).
	if e.cfg.OnProgress != nil {
		progressCtx, stop := context.WithCancel(runCtx)
		done := make(chan struct{})
		go e.trackProgress(progressCtx, done)
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

// trackProgress reports a live MetricsSnapshot to Config.OnProgress on a 500ms
// ticker — frequent enough for a smooth dashboard update, cheap enough to run
// for the lifetime of a Soak. It returns via done once ctx is cancelled. Its
// lifecycle mirrors trackShape exactly so neither can leak.
func (e *Engine) trackProgress(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			e.cfg.OnProgress(e.metrics.Snapshot())
		case <-ctx.Done():
			return
		}
	}
}

// fire issues one timed GET and records its outcome. The success/failure
// boundary is unchanged from the original engine: a request that fails to be
// built, fails at the transport (client.Do error), or returns a 5xx response
// counts as an error; everything else (2xx/3xx/4xx) is a success. What changed
// is that Record now receives a category label instead of a bool — transport
// failures are classified (connection refused / timeout / dns / other), 5xx
// responses use the status code itself, and successes pass "".
func (e *Engine) fire(runCtx context.Context) {
	start := time.Now()
	reqCtx := runCtx
	var cancel context.CancelFunc
	if e.cfg.RequestTimeout > 0 {
		reqCtx, cancel = context.WithTimeout(runCtx, e.cfg.RequestTimeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, e.cfg.TargetURL, nil)
	category := ""
	if err == nil {
		resp, err := e.client.Do(req)
		if err != nil {
			category = classifyHTTPError(err)
		} else {
			_ = resp.Body.Close()
			if resp.StatusCode >= 500 {
				category = "status_" + strconv.Itoa(resp.StatusCode)
			}
			// Any non-5xx status (2xx/3xx/4xx) stays a success, exactly as the
			// original `isError = resp.StatusCode >= 500` boundary defined it.
		}
	} else {
		category = ErrOther
	}

	latencyMs := time.Since(start).Milliseconds()
	if latencyMs < 1 {
		latencyMs = 1 // histogram floor is 1ms; sub-ms responses clamp to 1
	}
	e.metrics.Record(latencyMs, category)
}

// classifyHTTPError maps a client.Do transport error to a category label.
// The mapping is intentionally pragmatic: Go's http client wraps transport
// errors in opaque url.Error chains, so typed net.Error / *net.DNSError /
// syscall.Errno checks run first, with a string-contains fallback for cases
// the net package rephrases (e.g. Windows' "actively refused" wording). This
// is a known simplification over exhaustively unwrapping every transport
// topology. Anything unrecognized becomes ErrOther.
func classifyHTTPError(err error) string {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return ErrTimeout
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return ErrDNS
	}
	var errno syscall.Errno
	if errors.As(err, &errno) && errno == syscall.ECONNREFUSED {
		return ErrConnectionRefused
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "connection refused"), strings.Contains(msg, "actively refused"):
		return ErrConnectionRefused
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "deadline exceeded"):
		return ErrTimeout
	case strings.Contains(msg, "no such host"), strings.Contains(msg, "server misbehaving"):
		return ErrDNS
	default:
		return ErrOther
	}
}
