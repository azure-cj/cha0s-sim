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
	sleepFunc   func(time.Duration)
}

func NewLatencyInjector(cfg *config.LatencyConfig) *LatencyInjector {
	return &LatencyInjector{
		FixedMs:     cfg.FixedMs,
		JitterMinMs: cfg.JitterMinMs,
		JitterMaxMs: cfg.JitterMaxMs,
		sleepFunc:   time.Sleep,
	}
}

func (li *LatencyInjector) InjectRequest(w http.ResponseWriter, r *http.Request) bool {
	var delay time.Duration

	if li.JitterMaxMs > 0 {
		if li.JitterMaxMs < li.JitterMinMs {
			delay = time.Duration(li.JitterMinMs) * time.Millisecond
		} else {
			delay = time.Duration(li.JitterMinMs+rand.Intn(li.JitterMaxMs-li.JitterMinMs+1)) * time.Millisecond
		}
	} else if li.FixedMs > 0 {
		delay = time.Duration(li.FixedMs) * time.Millisecond
	}

	if delay > 0 {
		li.sleepFunc(delay)
	}

	return false
}
