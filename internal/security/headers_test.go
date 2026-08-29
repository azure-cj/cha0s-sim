package security

import (
	"net/http"
	"testing"
)

func makeResponse(headers map[string]string) *http.Response {
	resp := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
	}
	for k, v := range headers {
		resp.Header.Set(k, v)
	}
	return resp
}

func hasFinding(findings []Finding, category, name string) bool {
	for _, f := range findings {
		if f.FindingCategory == category && f.Location == "header:"+name {
			return true
		}
	}
	return false
}

func TestHeaderValidatorMissingHeadersDetected(t *testing.T) {
	v := NewDefaultHeaderValidator()
	resp := makeResponse(nil)

	findings := v.Scan(resp)
	if len(findings) != len(defaultSecurityHeaders) {
		t.Fatalf("got %d findings, want %d", len(findings), len(defaultSecurityHeaders))
	}
	for _, f := range findings {
		if f.FindingCategory != findingMissingHeader {
			t.Errorf("finding for %s has category %q, want %q", f.Location, f.FindingCategory, findingMissingHeader)
		}
		if f.FindingSeverity != severityWarning {
			t.Errorf("finding for %s has severity %q, want %q", f.Location, f.FindingSeverity, severityWarning)
		}
	}
}

func TestHeaderValidatorValidHeadersNoFindings(t *testing.T) {
	v := NewDefaultHeaderValidator()
	resp := makeResponse(map[string]string{
		"Content-Security-Policy":   "default-src 'self'; script-src 'self'",
		"Strict-Transport-Security": "max-age=63072000",
		"X-Frame-Options":           "DENY",
		"X-Content-Type-Options":    "nosniff",
	})

	if findings := v.Scan(resp); len(findings) != 0 {
		t.Fatalf("got %d findings for fully-secure response, want 0: %+v", len(findings), findings)
	}
}

func TestHeaderValidatorWeakHeaderDetected(t *testing.T) {
	v := NewDefaultHeaderValidator()
	resp := makeResponse(map[string]string{
		"Content-Security-Policy":   "upgrade-insecure-requests",
		"Strict-Transport-Security": "preload",
	})

	findings := v.Scan(resp)
	if !hasFinding(findings, findingWeakHeader, "Content-Security-Policy") {
		t.Errorf("missing weak_header finding for Content-Security-Policy; got %+v", findings)
	}
	// X-Frame-Options and X-Content-Type-Options absent -> missing_header.
	if !hasFinding(findings, findingMissingHeader, "X-Frame-Options") {
		t.Errorf("missing missing_header finding for X-Frame-Options; got %+v", findings)
	}
}

func TestHeaderValidatorMatchingIsCaseInsensitive(t *testing.T) {
	v := NewDefaultHeaderValidator()
	resp := makeResponse(map[string]string{
		"X-Frame-Options": "SAMEORIGIN",
	})
	findings := v.Scan(resp)
	if hasFinding(findings, findingMissingHeader, "X-Frame-Options") || hasFinding(findings, findingWeakHeader, "X-Frame-Options") {
		t.Errorf("X-Frame-Options SAMEORIGIN should pass case-insensitive check; got %+v", findings)
	}
}

func TestHeaderValidatorCustomPolicy(t *testing.T) {
	v := NewHeaderValidator(map[string][]string{
		"X-Custom-Broken": {"must-be-here"},
	})
	resp := makeResponse(map[string]string{
		"X-Custom-Broken": "not-the-right-value",
	})

	findings := v.Scan(resp)
	if !hasFinding(findings, findingWeakHeader, "X-Custom-Broken") {
		t.Errorf("custom policy weakness not detected; got %+v", findings)
	}

	okResp := makeResponse(map[string]string{"X-Custom-Broken": "must-be-here"})
	if findings := v.Scan(okResp); len(findings) != 0 {
		t.Errorf("custom policy satisfied but %d findings returned: %+v", len(findings), findings)
	}
}

func TestHeaderValidatorFindingOrderIndependent(t *testing.T) {
	v := NewDefaultHeaderValidator()

	total := 0
	for i := 0; i < 50; i++ {
		// One missing, one weak: order of findings across runs must not matter.
		resp := makeResponse(map[string]string{
			"Content-Security-Policy":   "upgrade-insecure-requests",
			"Strict-Transport-Security": "preload",
			"X-Frame-Options":           "DENY",
			"X-Content-Type-Options":    "nosniff",
		})
		findings := v.Scan(resp)
		if len(findings) != 2 {
			t.Fatalf("run %d: got %d findings, want 2", i, len(findings))
		}
		if !hasFinding(findings, findingWeakHeader, "Content-Security-Policy") {
			t.Fatalf("run %d: missing weak CSP finding", i)
		}
		if !hasFinding(findings, findingWeakHeader, "Strict-Transport-Security") {
			t.Fatalf("run %d: missing weak HSTS finding", i)
		}
		total += len(findings)
	}
	if total == 0 {
		t.Fatal("no findings accumulated")
	}
}

func TestHeaderValidatorImplementsResponseScanner(t *testing.T) {
	var _ ResponseScanner = (*HeaderValidator)(nil)
}
