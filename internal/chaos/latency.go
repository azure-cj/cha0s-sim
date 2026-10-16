package chaos

import (
	"math/rand"
	"net/http"
	"time"

	"cha0s-sim/internal/config"
)

type LatencyInjector struct {
	FixedMs     int
	JitterMinMs int
	JitterMaxMs int
	// AppliedMs records the delay applied by the most recent InjectRequest,
	// in milliseconds. Callers read it after InjectRequest to report the real
	// (jittered) latency on the traffic event.
	AppliedMs int64
	sleepFunc func(time.Duration)
	randIntn  func(n int) int
}

func NewLatencyInjector(cfg *config.LatencyConfig) *LatencyInjector {
	return &LatencyInjector{
		FixedMs:     cfg.FixedMs,
		JitterMinMs: cfg.JitterMinMs,
		JitterMaxMs: cfg.JitterMaxMs,
		sleepFunc:   time.Sleep,
		randIntn:    rand.Intn,
	}
}

func (li *LatencyInjector) InjectRequest(w http.ResponseWriter, r *http.Request) bool {
	var delay time.Duration

	if li.JitterMaxMs > 0 {
		if li.JitterMaxMs < li.JitterMinMs {
			delay = time.Duration(li.JitterMinMs) * time.Millisecond
		} else {
			delay = time.Duration(li.JitterMinMs+li.randIntn(li.JitterMaxMs-li.JitterMinMs+1)) * time.Millisecond
		}
	} else if li.FixedMs > 0 {
		delay = time.Duration(li.FixedMs) * time.Millisecond
	}

	li.AppliedMs = delay.Milliseconds()

	if delay > 0 {
		li.sleepFunc(delay)
	}

	return false
}
