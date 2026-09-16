package stress

import (
	"testing"
)

func TestSubstituteSimple(t *testing.T) {
	got := substituteVars("Bearer {{token}}", map[string]string{"token": "abc123"})
	if got != "Bearer abc123" {
		t.Errorf("substituteVars = %q, want %q", got, "Bearer abc123")
	}
}

func TestSubstituteMissingVariableLeavesPlaceholder(t *testing.T) {
	in := "/api/{{missing}}/users"
	got := substituteVars(in, map[string]string{})
	if got != in {
		t.Errorf("substituteVars = %q, want the original %q untouched", got, in)
	}
}

func TestSubstituteMissingAmongPresent(t *testing.T) {
	got := substituteVars("x={{a}}&y={{b}}", map[string]string{"a": "1", "b": "2"})
	if got != "x=1&y=2" {
		t.Errorf("substituteVars = %q, want %q", got, "x=1&y=2")
	}
}

func TestSubstituteMultipleAndRepeat(t *testing.T) {
	got := substituteVars("{{a}}-{{b}}-{{a}}", map[string]string{"a": "1", "b": "2"})
	if got != "1-2-1" {
		t.Errorf("substituteVars = %q, want %q", got, "1-2-1")
	}
}

func TestSubstituteWithSurroundingWhitespace(t *testing.T) {
	got := substituteVars("{{ token }}", map[string]string{"token": "abc"})
	if got != "abc" {
		t.Errorf("substituteVars = %q, want %q", got, "abc")
	}
}

func TestSubstituteDottedReferenceStaysLiteral(t *testing.T) {
	// Nested paths like {{user.id}} are out of scope for v1 and must pass
	// through untouched rather than being half-substituted.
	got := substituteVars("/protected/{{user.id}}", map[string]string{"user": "42"})
	if got != "/protected/{{user.id}}" {
		t.Errorf("substituteVars = %q, want the dotted reference untouched", got)
	}
}

func TestSubstituteValueContainingBracesNotReScanned(t *testing.T) {
	// A single-pass replace must not re-scan substituted text, otherwise a
	// value that itself contains {{...}} could recursively substitute.
	got := substituteVars("{{a}}", map[string]string{"a": "{{b}}"})
	if got != "{{b}}" {
		t.Errorf("substituteVars = %q, want %q (substitution must be single-pass)", got, "{{b}}")
	}
}

func TestSubstituteEmptyBodyAndHeaders(t *testing.T) {
	if got := substituteVars("", map[string]string{"a": "1"}); got != "" {
		t.Errorf("substituteVars of empty string = %q, want empty", got)
	}
}

func TestExtractTopLevelString(t *testing.T) {
	v, ok := extractTopLevel(`{"token":"abc123","id":"42"}`, "token")
	if !ok || v != "abc123" {
		t.Errorf("extractTopLevel = (%q, %v), want (\"abc123\", true)", v, ok)
	}
}

func TestExtractTopLevelNumericScalar(t *testing.T) {
	v, ok := extractTopLevel(`{"count":7}`, "count")
	if !ok || v != "7" {
		t.Errorf("extractTopLevel = (%q, %v), want (\"7\", true)", v, ok)
	}
}

func TestExtractTopLevelMissingKey(t *testing.T) {
	if _, ok := extractTopLevel(`{"token":"abc"}`, "nope"); ok {
		t.Error("extractTopLevel found a missing key; want ok == false")
	}
}

func TestExtractTopLevelNonJSONBody(t *testing.T) {
	if _, ok := extractTopLevel("this is not json", "token"); ok {
		t.Error("extractTopLevel succeeded on a non-JSON body; want ok == false")
	}
	if _, ok := extractTopLevel("", "token"); ok {
		t.Error("extractTopLevel succeeded on an empty body; want ok == false")
	}
}

func TestExtractTopLevelNestedPathUnsupported(t *testing.T) {
	// v1 only supports top-level keys; a dotted "path" must never resolve.
	if _, ok := extractTopLevel(`{"user":{"id":1}}`, "user.id"); ok {
		t.Error("extractTopLevel resolved a dotted path; want ok == false")
	}
	// And an object-valued top-level key is not substitutable either.
	if _, ok := extractTopLevel(`{"user":{"id":1}}`, "user"); ok {
		t.Error("extractTopLevel returned an object value; want ok == false")
	}
}

func TestExtractTopLevelNullValue(t *testing.T) {
	if _, ok := extractTopLevel(`{"token":null}`, "token"); ok {
		t.Error("extractTopLevel returned a null value; want ok == false")
	}
}