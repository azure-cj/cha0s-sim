package stress

import (
	"net/http"
	"time"
)

// NewClientPool returns an http.Client whose transport is tuned for sustained
// load testing, cloned from the default transport (same technique as
// internal/proxy/tls.go).
//
// The single most important knob is MaxIdleConnsPerHost. http.DefaultTransport
// leaves it at 2, which means every worker beyond two concurrent in-flight
// requests silently stalls on connection creation regardless of how many
// workers the engine spawns. Callers MUST set maxIdlePerHost well above that
// default or the pool will throttle concurrency regardless of worker count.
//
// Concurrency is intentionally left to the worker pool, not the transport:
// MaxConnsPerHost stays 0 (unlimited). No client-level Timeout is set —
// per-request timeouts belong in each scenario's request context, since
// different scenarios may want different timeout behavior.
func NewClientPool(maxIdlePerHost int) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 1000
	transport.MaxIdleConnsPerHost = maxIdlePerHost
	transport.MaxConnsPerHost = 0
	transport.IdleConnTimeout = 90 * time.Second
	return &http.Client{Transport: transport}
}