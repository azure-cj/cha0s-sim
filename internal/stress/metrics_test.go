package stress

import (
	"testing"
)

func TestMetricsPercentilesSaneRange(t *testing.T) {
	m := NewMetrics()

	// 100 samples: 10ms, 20ms, ..., 1000ms.
	for i := 1; i <= 100; i++ {
		m.Record(int64(i*10), false)
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
	// 20 requests, 5 failures -> 25% error rate.
	for i := 0; i < 20; i++ {
		m.Record(int64(50), i < 5)
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
}

func TestMetricsAllZeroAfterOneRecordIsError(t *testing.T) {
	m := NewMetrics()
	m.Record(100, true)
	snap := m.Snapshot()

	if snap.ErrorRate != 1 {
		t.Errorf("ErrorRate = %v, want 1 (single request was an error)", snap.ErrorRate)
	}
	if snap.P50Ms == 0 || snap.P95Ms == 0 || snap.P99Ms == 0 {
		t.Errorf("percentiles should be populated after one record: %+v", snap)
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
		m.Record(int64(i*10), i%3 == 0)
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

	// Post-reset, a fresh record still lands (startTime was repinned).
	m.Record(25, false)
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
				m.Record(int64(10+(i%100)), false)
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