package security

import (
	"net/http"

	"cha0s-sim/internal/platform"
)

// Finding is a security scan result. It implements platform.Reportable so it
// flows through the unified platform event log.
//
// IAST BOUNDARY: Finding MUST only ever contain HTTP-conversation-scoped
// fields (header name, JSON path in a body, request ID, timestamp). NEVER add
// fields like SourceFile, LineNumber, or StackTrace — those would require a
// language-specific runtime agent (IAST), which is explicitly out of scope.
type Finding struct {
	FindingCategory string // "missing_header" / "weak_header" (HeaderValidator), "leaked_secret" (SecretScanner)
	Detail          string
	FindingSeverity string // "info", "warning", or "critical"
	Location        string // e.g. "header:Content-Security-Policy"
	// Remediation is a short, concrete, actionable instruction for fixing the
	// finding — what a senior engineer would tell a junior exactly what to
	// type. It is pattern/header-level guidance and NEVER includes any value
	// discovered in the traffic (same no-leak rule as Detail/Location).
	Remediation string `json:"remediation"`
	// SessionName identifies which named session ("chaos"/"security") produced
	// this finding. It is stamped by the proxy's scanner runner (RunScanners)
	// from the request context; it is empty when the finding did not originate
	// from a named session (e.g. the CLI proxy, direct-call tests).
	SessionName string `json:"sessionName"`
}

// Category/Summary/Severity satisfy platform.Reportable. Summary deliberately
// stays a compact WHAT log line (the Detail); Remediation is a separate field
// surfaced by the UI alongside the finding, not folded into the summary.
func (f Finding) Category() string { return f.FindingCategory }
func (f Finding) Summary() string  { return f.Detail }
func (f Finding) Severity() string { return f.FindingSeverity }

// compile-time check that Finding satisfies platform.Reportable.
var _ platform.Reportable = Finding{}

// ResponseScanner inspects a backend response and returns any findings it
// detects. Scanners are UNCONDITIONAL (run on all traffic, not gated by chaos
// match/fire logic) and NON-MUTATING (they only observe, never alter the
// request or response).
type ResponseScanner interface {
	Scan(resp *http.Response) []Finding
}
