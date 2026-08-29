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
func RunScanners(resp *http.Response, scanners []security.ResponseScanner, sink EventSink) {
	if sink == nil {
		return
	}
	for _, scanner := range scanners {
		for _, finding := range scanner.Scan(resp) {
			sink.Emit(finding)
		}
	}
}

// compile-time contract check: findings must satisfy platform.Reportable so
// they can flow through the event sink regardless of concrete event type.
var _ platform.Reportable = security.Finding{}
