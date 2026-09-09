package cli

import (
	"fmt"
	"io"
	"sync"

	"cha0s-sim/internal/platform"
	"cha0s-sim/internal/proxy"
	"cha0s-sim/internal/security"
)

// stdoutSink is the headless CLI's event surface. Unlike the desktop app (which
// forwards events to the frontend over the Wails event bus), the plain CLI
// proxy has no GUI, so this sink prints to stdout:
//
//   - security findings are ALWAYS printed (they are the important output and
//     must never be silently dropped), with their remediation.
//   - traffic events are printed only when the proxy runs with --verbose, so
//     the default output stays quiet unless something needs attention.
//
// Emits can arrive from concurrent requests, so printing is mutex-guarded to
// keep a finding block atomic.
type stdoutSink struct {
	out     io.Writer
	verbose bool
	mu      sync.Mutex
}

func (s *stdoutSink) Emit(evt platform.Reportable) {
	switch v := evt.(type) {
	case security.Finding:
		s.mu.Lock()
		defer s.mu.Unlock()
		fmt.Fprintf(s.out, "\nSECURITY FINDING [%s]\n", v.FindingSeverity)
		fmt.Fprintf(s.out, "  %s: %s\n", v.FindingCategory, v.Detail)
		if v.Location != "" {
			fmt.Fprintf(s.out, "  location: %s\n", v.Location)
		}
		if v.Remediation != "" {
			fmt.Fprintf(s.out, "  remediation: %s\n", v.Remediation)
		}
		fmt.Fprintln(s.out)
	case proxy.TrafficEvent:
		if !s.verbose {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		fmt.Fprintln(s.out, v.Summary())
	default:
		// Future Reportable types are surfaced rather than dropped.
		s.mu.Lock()
		defer s.mu.Unlock()
		fmt.Fprintf(s.out, "\n[%s] %s\n", v.Severity(), v.Summary())
	}
}