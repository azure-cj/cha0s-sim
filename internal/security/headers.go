package security

import (
	"net/http"
	"strings"
)

// headerPolicy describes one security header check: the header name, the
// expected value substrings that make it acceptable, and concrete remediation
// text for the "missing" and "weak" failure modes.
type headerPolicy struct {
	name           string
	want           []string
	fixMissing     string // remediation shown when the header is absent
	fixWeak        string // remediation shown when the header is present but ineffective
}

// defaultSecurityHeaders are checked on every proxied response. Values are
// matched case-insensitively as substrings so we accept both the exact
// recommended directive sets and reasonable variations. Each entry carries a
// concrete, copy-paste-able remediation so a finding tells the user exactly
// what header+value to add.
var defaultSecurityHeaders = []headerPolicy{
	{
		name: "Content-Security-Policy",
		want: []string{"default-src"},
		fixMissing: "Add a Content-Security-Policy header, e.g.: Content-Security-Policy: default-src 'self'",
		fixWeak:    "Your Content-Security-Policy is missing a default-src directive — add one, e.g.: default-src 'self'",
	},
	{
		name: "Strict-Transport-Security",
		want: []string{"max-age"},
		fixMissing: "Add: Strict-Transport-Security: max-age=31536000; includeSubDomains",
		fixWeak:    "Your Strict-Transport-Security header is missing the max-age directive — add: Strict-Transport-Security: max-age=31536000; includeSubDomains",
	},
	{
		name: "X-Frame-Options",
		want: []string{"DENY", "SAMEORIGIN"},
		fixMissing: "Add: X-Frame-Options: DENY (or use SAMEORIGIN if your own site must frame this page)",
		fixWeak:    "Your X-Frame-Options value must be DENY or SAMEORIGIN — set: X-Frame-Options: DENY (or use SAMEORIGIN if your own site must frame this page)",
	},
	{
		name: "X-Content-Type-Options",
		want: []string{"nosniff"},
		fixMissing: "Add: X-Content-Type-Options: nosniff",
		fixWeak:    "Your X-Content-Type-Options header must be nosniff — set: X-Content-Type-Options: nosniff",
	},
}

const (
	findingMissingHeader = "missing_header"
	findingWeakHeader    = "weak_header"
	severityWarning      = "warning"
)

// HeaderValidator checks responses for missing or ineffective security
// headers. It implements ResponseScanner: it runs on every response and never
// mutates the conversation.
type HeaderValidator struct {
	// headers is the set of checks to perform, keyed by header name with the
	// acceptable value substrings. When empty, the default security header set
	// is used.
	headers map[string][]string
}

// NewHeaderValidator returns a validator that checks exactly the given
// header->value-substring pairs.
func NewHeaderValidator(headers map[string][]string) *HeaderValidator {
	return &HeaderValidator{headers: headers}
}

// NewDefaultHeaderValidator returns a validator that checks the standard
// security header set (CSP, HSTS, X-Frame-Options, X-Content-Type-Options).
func NewDefaultHeaderValidator() *HeaderValidator {
	return &HeaderValidator{headers: nil}
}

func (v *HeaderValidator) policies() []headerPolicy {
	if v.headers != nil {
		out := make([]headerPolicy, 0, len(v.headers))
		for name, want := range v.headers {
			out = append(out, headerPolicy{
				name:       name,
				want:       want,
				fixMissing: "Add the " + name + " response header.",
				fixWeak:    "Set the " + name + " header to one of: " + strings.Join(want, ", "),
			})
		}
		return out
	}
	return defaultSecurityHeaders
}

// Scan evaluates the response's headers and returns a Finding for every
// missing or weak security header, each with a concrete, specific Remediation.
func (v *HeaderValidator) Scan(resp *http.Response) []Finding {
	findings := make([]Finding, 0)
	for _, p := range v.policies() {
		val := resp.Header.Get(p.name)
		if val == "" {
			findings = append(findings, Finding{
				FindingCategory: findingMissingHeader,
				Detail:          "missing security header " + p.name,
				FindingSeverity: severityWarning,
				Location:        "header:" + p.name,
				Remediation:     p.fixMissing,
			})
			continue
		}
		if !satisfies(val, p.want) {
			findings = append(findings, Finding{
				FindingCategory: findingWeakHeader,
				Detail:          "security header " + p.name + " present but expected value(s) missing: " + strings.Join(p.want, ", ") + "; got '" + val + "'",
				FindingSeverity: severityWarning,
				Location:        "header:" + p.name,
				Remediation:     p.fixWeak,
			})
		}
	}
	return findings
}

// satisfies reports whether header value val contains any of the acceptable
// substrings, compared case-insensitively.
func satisfies(val string, want []string) bool {
	lower := strings.ToLower(val)
	for _, w := range want {
		if strings.Contains(lower, strings.ToLower(w)) {
			return true
		}
	}
	return false
}
