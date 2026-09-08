package proxy

import (
	"net/http"

	"cha0s-sim/internal/platform"
	"cha0s-sim/internal/security"
)

// RunScanners runs every configured ResponseScanner against a completed
// backend response and forwards each Finding to the sink. Scanners are
// UNCONDITIONAL (they run on all traffic, never gated by chaos
// match/fire logic) and NON-MUTATING (RunScanners never alters resp.Body,
// resp.StatusCode, or headers — it only observes and reports).
//
// It follows the same nil-sink convention as withEventSink: when nothing is
// listening, scanning is skipped entirely so the headless CLI path pays zero
// overhead.
//
// Each emitted Finding carries the originating session ("chaos"/"security"),
// read from the request context that withEventSink stamped (the same
// resp.Request.Context() data flow RunResponseInjectors already relies on).
// When that context has no session (e.g. direct-call tests, CLI-style runs
// where no sink was ever added), SessionName stays empty.
func RunScanners(resp *http.Response, scanners []security.ResponseScanner, sink EventSink) {
	if sink == nil {
		return
	}
	var sessionName string
	if resp != nil && resp.Request != nil {
		if v, ok := resp.Request.Context().Value(sessionNameKey).(string); ok {
			sessionName = v
		}
	}
	for _, scanner := range scanners {
		for _, finding := range scanner.Scan(resp) {
			finding.SessionName = sessionName
			sink.Emit(finding)
		}
	}
}

// compile-time contract check: findings must satisfy platform.Reportable so
// they can flow through the event sink regardless of concrete event type.
var _ platform.Reportable = security.Finding{}
