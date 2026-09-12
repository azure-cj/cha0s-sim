package stress

import (
	"testing"
)

func TestMetricsPercentilesSaneRange(t *testing.T) {
	m := NewMetrics()

	// 100 samples: 10ms, 20ms, ..., 1000ms.
	for i := 1; i <= 100; i++ {
		m.Record(int64(i*10), "")
	}

	snap := m.Snapshot()

	if snap.TotalRequests != 100 {
		t.Errorf("TotalRequests = %d, want 100", snap.TotalRequests)
	}
	if snap.TotalErrors != 0 {
		t.Errorf("TotalErrors = %d, want 0", snap.TotalErrors)
	}
	if snap.ErrorRate != 0 {
		t.Errorf("ErrorRate = %v, want 0", snap.ErrorRate)
	}
	// HDRHistogram has inherent precision limits: assert rough placement rather
	// than exact quantile values.
	if snap.P50Ms < 400 || snap.P50Ms > 600 {
		t.Errorf("P50Ms = %d, want roughly mid-range (400-600)", snap.P50Ms)
	}
	if snap.P95Ms < 800 || snap.P95Ms > 1000 {
		t.Errorf("P95Ms = %d, want roughly the upper tail (800-1000)", snap.P95Ms)
	}
	if snap.P99Ms < 950 {
		t.Errorf("P99Ms = %d, want near the top of the range (>= 950)", snap.P99Ms)
	}
	if !(snap.P50Ms <= snap.P95Ms && snap.P95Ms <= snap.P99Ms) {
		t.Errorf("percentiles not monotonic: P50=%d P95=%d P99=%d", snap.P50Ms, snap.P95Ms, snap.P99Ms)
	}
	if snap.RPS <= 0 {
		t.Errorf("RPS = %v, want > 0", snap.RPS)
	}
}

func TestMetricsErrorRate(t *testing.T) {
	m := NewMetrics()
	// 20 requests, 5 failures -> 25% error rate. Categorization is orthogonal
	// to the aggregate: 5 timeout errors must still land in totalErrors and
	// errorCategories, leaving the 0.25 rate intact.
	for i := 0; i < 20; i++ {
		if i < 5 {
			m.Record(int64(50), ErrTimeout)
		} else {
			m.Record(int64(50), "")
		}
	}

	snap := m.Snapshot()

	if snap.ErrorRate != 0.25 {
		t.Errorf("ErrorRate = %v, want 0.25", snap.ErrorRate)
	}
	if snap.TotalErrors != 5 {
		t.Errorf("TotalErrors = %d, want 5", snap.TotalErrors)
	}
	if snap.TotalRequests != 20 {
		t.Errorf("TotalRequests = %d, want 20", snap.TotalRequests)
	}
	if snap.ErrorCategories[ErrTimeout] != 5 {
		t.Errorf("ErrorCategories[%q] = %d, want 5", ErrTimeout, snap.ErrorCategories[ErrTimeout])
	}
}

func TestMetricsErrorCategorization(t *testing.T) {
	m := NewMetrics()
	// Mix of categories + successes in one run, matching what the engine emits:
	// connection-refused, a 503 status error, a 500 status error, and successes.
	m.Record(10, "")
	m.Record(20, ErrConnectionRefused)
	m.Record(30, "status_503")
	m.Record(40, "")
	m.Record(50, "status_500")

	snap := m.Snapshot()

	if snap.TotalRequests != 5 {
		t.Errorf("TotalRequests = %d, want 5", snap.TotalRequests)
	}
	if snap.TotalErrors != 3 {
		t.Errorf("TotalErrors = %d, want 3", snap.TotalErrors)
	}
	want := map[string]int64{ErrConnectionRefused: 1, "status_503": 1, "status_500": 1}
	for cat, n := range want {
		if snap.ErrorCategories[cat] != n {
			t.Errorf("ErrorCategories[%q] = %d, want %d", cat, snap.ErrorCategories[cat], n)
		}
	}
	if len(snap.ErrorCategories) != 3 {
		t.Errorf("ErrorCategories has %d entries, want exactly 3: %v", len(snap.ErrorCategories), snap.ErrorCategories)
	}
	if _, ok := snap.ErrorCategories[""]; ok {
		t.Error("empty-string (success) category must never be recorded as a key")
	}
	// Non-empty categories recorded together with a run that also has successes:
	// sums must reconcile against the aggregates.
	sum := int64(0)
	for _, n := range snap.ErrorCategories {
		sum += n
	}
	if sum != snap.TotalErrors {
		t.Errorf("ErrorCategories sum = %d, want %d (must equal TotalErrors)", sum, snap.TotalErrors)
	}
}

func TestMetricsSnapshotCopiesErrorCategories(t *testing.T) {
	m := NewMetrics()
	m.Record(10, ErrTimeout)
	m.Record(20, "status_503")

	snap := m.Snapshot()
	// Mutating the caller's snapshot must never leak back into the Metrics.
	snap.ErrorCategories[ErrTimeout] = 9999
	snap.ErrorCategories["injected"] = 1

	again := m.Snapshot()
	if again.ErrorCategories[ErrTimeout] != 1 {
		t.Errorf("snapshot mutation leaked into live Metrics: ErrorCategories[%q] = %d, want 1",
			ErrTimeout, again.ErrorCategories[ErrTimeout])
	}
	if _, ok := again.ErrorCategories["injected"]; ok {
		t.Error("snapshot mutation leaked into live Metrics: injected key present")
	}
}

func TestMetricsAllZeroAfterOneRecordIsError(t *testing.T) {
	m := NewMetrics()
	m.Record(100, ErrOther)
	snap := m.Snapshot()

	if snap.ErrorRate != 1 {
		t.Errorf("ErrorRate = %v, want 1 (single request was an error)", snap.ErrorRate)
	}
	if snap.P50Ms == 0 || snap.P95Ms == 0 || snap.P99Ms == 0 {
		t.Errorf("percentiles should be populated after one record: %+v", snap)
	}
	if snap.ErrorCategories[ErrOther] != 1 {
		t.Errorf("ErrorCategories[%q] = %d, want 1", ErrOther, snap.ErrorCategories[ErrOther])
	}
}

func TestMetricsZeroRequestsSnapshot(t *testing.T) {
	m := NewMetrics()

	snap := m.Snapshot()

	if snap.TotalRequests != 0 || snap.TotalErrors != 0 || snap.ErrorRate != 0 || snap.RPS != 0 {
		t.Errorf("zero-request snapshot not all-zero: %+v", snap)
	}
	if snap.P50Ms != 0 || snap.P95Ms != 0 || snap.P99Ms != 0 {
		t.Errorf("zero-request percentiles not zero: %+v", snap)
	}
}

func TestMetricsResetClearsState(t *testing.T) {
	m := NewMetrics()
	for i := 1; i <= 100; i++ {
		if i%3 == 0 {
			m.Record(int64(i*10), ErrConnectionRefused)
		} else {
			m.Record(int64(i*10), "")
		}
	}

	before := m.Snapshot()
	if before.TotalRequests != 100 {
		t.Fatalf("setup: TotalRequests = %d, want 100", before.TotalRequests)
	}

	m.Reset()
	snap := m.Snapshot()

	if snap.TotalRequests != 0 || snap.TotalErrors != 0 || snap.ErrorRate != 0 || snap.RPS != 0 {
		t.Errorf("post-reset snapshot not all-zero: %+v", snap)
	}
	if snap.P50Ms != 0 || snap.P95Ms != 0 || snap.P99Ms != 0 {
		t.Errorf("post-reset percentiles not zero: %+v", snap)
	}
	if len(snap.ErrorCategories) != 0 {
		t.Errorf("post-reset ErrorCategories not cleared: %v", snap.ErrorCategories)
	}

	// Post-reset, a fresh record still lands (startTime was repinned).
	m.Record(25, "")
	after := m.Snapshot()
	if after.TotalRequests != 1 {
		t.Errorf("post-reset record got TotalRequests = %d, want 1", after.TotalRequests)
	}
	if after.RPS <= 0 {
		t.Errorf("post-reset RPS = %v, want > 0", after.RPS)
	}
}

func TestMetricsConcurrentRecording(t *testing.T) {
	m := NewMetrics()

	const workers = 8
	const perWorker = 250
	done := make(chan struct{})
	for w := 0; w < workers; w++ {
		go func() {
			for i := 0; i < perWorker; i++ {
				m.Record(int64(10+(i%100)), "")
			}
			done <- struct{}{}
		}()
	}
	for w := 0; w < workers; w++ {
		<-done
	}

	snap := m.Snapshot()
	if snap.TotalRequests != workers*perWorker {
		t.Errorf("TotalRequests = %d, want %d (no lost updates under contention)", snap.TotalRequests, workers*perWorker)
	}
}
