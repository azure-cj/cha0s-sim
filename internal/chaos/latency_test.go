package chaos

import (
	"net/http/httptest"
	"testing"
	"time"

	"cha0s-sim/internal/config"
)

func TestFixedDelayCallsSleepWithCorrectDuration(t *testing.T) {
	li := NewLatencyInjector(&config.LatencyConfig{FixedMs: 500})
	var got time.Duration
	li.sleepFunc = func(d time.Duration) { got = d }

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "http://x/", nil)

	handled := li.InjectRequest(w, r)
	if got != 500*time.Millisecond {
		t.Errorf("sleepFunc received %v, want %v", got, 500*time.Millisecond)
	}
	if handled {
		t.Error("InjectRequest returned true, want false")
	}
}

func TestJitterDelayWithinBounds(t *testing.T) {
	li := NewLatencyInjector(&config.LatencyConfig{JitterMinMs: 500, JitterMaxMs: 2500})
	li.sleepFunc = func(d time.Duration) {}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "http://x/", nil)

	for i := 0; i < 20; i++ {
		var got time.Duration
		li.sleepFunc = func(d time.Duration) { got = d }
		li.InjectRequest(w, r)
		if got < 500*time.Millisecond || got > 2500*time.Millisecond {
			t.Errorf("iteration %d: delay %v out of [500ms, 2500ms]", i, got)
		}
	}
}

func TestJitterTakesPriorityOverFixed(t *testing.T) {
	li := NewLatencyInjector(&config.LatencyConfig{FixedMs: 100, JitterMinMs: 500, JitterMaxMs: 600})
	var got time.Duration
	li.sleepFunc = func(d time.Duration) { got = d }

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "http://x/", nil)

	li.InjectRequest(w, r)
	if got < 500*time.Millisecond || got > 600*time.Millisecond {
		t.Errorf("delay %v not within jitter range [500ms, 600ms]", got)
	}
	if got == 100*time.Millisecond {
		t.Errorf("delay = %v, fixed delay used despite jitter configured", got)
	}
}

func TestZeroConfigNoDelayNoSleepCall(t *testing.T) {
	li := NewLatencyInjector(&config.LatencyConfig{})
	called := false
	li.sleepFunc = func(d time.Duration) { called = true }

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "http://x/", nil)

	li.InjectRequest(w, r)
	if called {
		t.Error("sleepFunc was called for zero config, want no call")
	}
}

func TestMalformedJitterDefensivelyUsesMin(t *testing.T) {
	li := NewLatencyInjector(&config.LatencyConfig{JitterMinMs: 1000, JitterMaxMs: 500})
	var got time.Duration
	li.sleepFunc = func(d time.Duration) { got = d }

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "http://x/", nil)

	li.InjectRequest(w, r)
	if got != 1000*time.Millisecond {
		t.Errorf("delay = %v, want 1000ms (defensive min fallback)", got)
	}
}
