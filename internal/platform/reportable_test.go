package platform

import "testing"

// fakeReport proves a plain struct with the three methods satisfies
// Reportable — a compile-time contract check plus a callable-through-interface
// sanity check.
type fakeReport struct {
	category string
	summary  string
	severity string
}

func (f fakeReport) Category() string { return f.category }
func (f fakeReport) Summary() string  { return f.summary }
func (f fakeReport) Severity() string { return f.severity }

var _ Reportable = fakeReport{}

func TestReportableInterfaceSatisfied(t *testing.T) {
	r := fakeReport{category: "chaos", summary: "GET /api/users -> 200", severity: "info"}

	if r.Category() != "chaos" {
		t.Errorf("Category() = %q, want %q", r.Category(), "chaos")
	}
	if r.Summary() != "GET /api/users -> 200" {
		t.Errorf("Summary() = %q, want %q", r.Summary(), "GET /api/users -> 200")
	}
	if r.Severity() != "info" {
		t.Errorf("Severity() = %q, want %q", r.Severity(), "info")
	}
}

func TestReportableCallableThroughInterface(t *testing.T) {
	var reportable Reportable = fakeReport{category: "security", summary: "missing header", severity: "warning"}

	if got := reportable.Category(); got != "security" {
		t.Errorf("Category() via interface = %q, want %q", got, "security")
	}
	if got := reportable.Summary(); got != "missing header" {
		t.Errorf("Summary() via interface = %q, want %q", got, "missing header")
	}
	if got := reportable.Severity(); got != "warning" {
		t.Errorf("Severity() via interface = %q, want %q", got, "warning")
	}
}