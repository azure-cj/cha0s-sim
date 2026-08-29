package security

import (
	"net/http"
	"strings"
)

// headerPolicy describes one security header check: the header name and the
// expected value substrings that make it acceptable.
type headerPolicy struct {
	name string
	want []string
}

// defaultSecurityHeaders are checked on every proxied response. Values are
// matched case-insensitively as substrings so we accept both the exact
// recommended directive sets and reasonable variations.
var defaultSecurityHeaders = []headerPolicy{
	{name: "Content-Security-Policy", want: []string{"default-src"}},
	{name: "Strict-Transport-Security", want: []string{"max-age"}},
	{name: "X-Frame-Options", want: []string{"DENY", "SAMEORIGIN"}},
	{name: "X-Content-Type-Options", want: []string{"nosniff"}},
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
			out = append(out, headerPolicy{name: name, want: want})
		}
		return out
	}
	return defaultSecurityHeaders
}

// Scan evaluates the response's headers and returns a Finding for every
// missing or weak security header.
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
			})
			continue
		}
		if !satisfies(val, p.want) {
			findings = append(findings, Finding{
				FindingCategory: findingWeakHeader,
				Detail:          "security header " + p.name + " present but expected value(s) missing: " + strings.Join(p.want, ", ") + "; got '" + val + "'",
				FindingSeverity: severityWarning,
				Location:        "header:" + p.name,
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
