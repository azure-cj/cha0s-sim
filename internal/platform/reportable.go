package platform

// Reportable is the shared contract every event/finding/verdict in the
// platform must implement, so the Wails event layer can emit one unified
// filterable event log instead of N bespoke event types and UI patterns.
type Reportable interface {
	// Category groups the item, e.g. "chaos", "security", "stress",
	// "authtest".
	Category() string
	// Summary is a one-line human-readable description for a unified event
	// log.
	Summary() string
	// Severity is one of "info", "warning", "critical".
	Severity() string
}