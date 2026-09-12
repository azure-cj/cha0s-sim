package stress

import (
	"sync"
	"time"

	"github.com/HdrHistogram/hdrhistogram-go"
)

// Metric error categories. An empty-string category means "success". Any
// non-empty category is a label counted alongside the aggregate totalErrors;
// HTTP-level failures (HTTP 5xx responses) use the status code itself as the
// category (e.g. "status_503") instead of one of these transport-level labels.
const (
	ErrConnectionRefused = "connection_refused"
	ErrTimeout           = "timeout"
	ErrDNS               = "dns_error"
	ErrOther             = "other_error"
)

// Metrics tracks latency and success/failure counts for a load run. Latency is
// stored in a HDR histogram (memory-bounded, fixed precision) rather than a raw
// sample slice, so percentile computation never degrades into unbounded memory
// growth under sustained load.
type Metrics struct {
	mu              sync.Mutex
	hist            *hdrhistogram.Histogram
	totalRequests   int64
	totalErrors     int64
	errorCategories map[string]int64 // category -> count, only non-empty categories
	startTime       time.Time
}

// MetricsSnapshot is a point-in-time read of a Metrics; safe to hand to a
// dashboard renderer without holding the underlying lock. The json tags keep
// the wire/TypeScript shape camelCase for the Wails frontend.
type MetricsSnapshot struct {
	TotalRequests   int64            `json:"totalRequests"`
	TotalErrors     int64            `json:"totalErrors"`
	ErrorRate       float64          `json:"errorRate"` // totalErrors/totalRequests, 0 if totalRequests is 0
	RPS             float64          `json:"rps"`       // totalRequests / elapsed seconds since startTime
	P50Ms           int64            `json:"p50Ms"`
	P95Ms           int64            `json:"p95Ms"`
	P99Ms           int64            `json:"p99Ms"`
	ErrorCategories map[string]int64 `json:"errorCategories,omitempty"` // nil when no errors were recorded
}

// NewMetrics returns a Metrics with a histogram spanning 1ms to 60s at 3
// significant digits and its start time pinned to now.
func NewMetrics() *Metrics {
	return &Metrics{
		hist:            hdrhistogram.New(1, 60_000, 3),
		errorCategories: make(map[string]int64),
		startTime:       time.Now(),
	}
}

// Record is the thread-safe single latest sample: one request's latency in
// milliseconds and its outcome. An empty errorCategory means success; any
// non-empty value is an error category label that is counted both into the
// aggregate totalErrors and into the per-category breakdown.
func (m *Metrics) Record(latencyMs int64, errorCategory string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hist.RecordValue(latencyMs)
	m.totalRequests++
	if errorCategory != "" {
		m.totalErrors++
		m.errorCategories[errorCategory]++
	}
}

// Snapshot returns a point-in-time view. A Metrics with zero recorded requests
// yields an all-zero snapshot with no division-by-zero surprises.
func (m *Metrics) Snapshot() MetricsSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := MetricsSnapshot{
		TotalRequests: m.totalRequests,
		TotalErrors:   m.totalErrors,
	}
	if len(m.errorCategories) > 0 {
		// Copy, never hand out the live map: a snapshot must stay stable even
		// if the caller later records more requests into the live Metrics.
		out.ErrorCategories = make(map[string]int64, len(m.errorCategories))
		for k, v := range m.errorCategories {
			out.ErrorCategories[k] = v
		}
	}
	if m.totalRequests == 0 {
		return out
	}

	out.ErrorRate = float64(m.totalErrors) / float64(m.totalRequests)
	out.RPS = float64(m.totalRequests) / time.Since(m.startTime).Seconds()
	out.P50Ms = m.hist.ValueAtQuantile(50)
	out.P95Ms = m.hist.ValueAtQuantile(95)
	out.P99Ms = m.hist.ValueAtQuantile(99)
	return out
}

// Reset clears all recorded samples and repins the start time to now. It is the
// primitive behind a "sliding window" live view: the caller runs it on a
// ticker, swapping snapshots each window rather than building fresh Metrics.
func (m *Metrics) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hist.Reset()
	m.totalRequests = 0
	m.totalErrors = 0
	m.errorCategories = make(map[string]int64)
	m.startTime = time.Now()
}
