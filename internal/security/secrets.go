package security

import (
	"bytes"
	"io"
	"net/http"
	"regexp"
)

// secretPattern describes one high-confidence secret format: a recognizable
// structural signature (not broad heuristic guessing) that, when matched,
// almost certainly indicates a leaked credential.
type secretPattern struct {
	name     string
	pattern  *regexp.Regexp
	severity string
}

// secretPatterns is the v1 detection set. Deliberately small: only well-known,
// structured secret formats with near-zero ambiguity. Expanded later only after
// these prove not to produce excessive false-positive noise. Generic PII
// (names, addresses, arbitrary emails) is explicitly NOT a target.
var secretPatterns = []secretPattern{
	{"aws_access_key_id", regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), "critical"},
	{"aws_secret_access_key", regexp.MustCompile(`(?i)aws_secret_access_key["']?\s*[:=]\s*["']?[A-Za-z0-9/+=]{40}\b`), "critical"},
	{"generic_bearer_token", regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9\-._~+/]{20,}`), "warning"},
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\b`), "warning"},
	{"private_key_header", regexp.MustCompile(`-----BEGIN (RSA |EC |DSA |OPENSSH |)PRIVATE KEY-----`), "critical"},
	{"generic_api_key_assignment", regexp.MustCompile(`(?i)(api[_-]?key|secret[_-]?key)["']?\s*[:=]\s*["']?[A-Za-z0-9\-_]{16,}["']?`), "warning"},
}

// secretPatternDescriptions maps a pattern name to the human-readable WHAT text
// used in finding Details. Details NEVER include any portion of the matched
// secret text — only the pattern name and where it appeared. A security scanner
// whose own findings leak the secrets it detects would itself become a leak
// vector, so the safest default is to include nothing recognizable.
var secretPatternDescriptions = map[string]string{
	"aws_access_key_id":          "Detected AWS Access Key ID pattern",
	"aws_secret_access_key":      "Detected AWS Secret Access Key assignment",
	"generic_bearer_token":       "Detected generic bearer token",
	"jwt":                        "Detected JWT-shaped token",
	"private_key_header":         "Detected private key block",
	"generic_api_key_assignment": "Detected API key / secret assignment",
}

const (
	// findingLeakedSecret is the FindingCategory used for every SecretScanner
	// finding.
	findingLeakedSecret = "leaked_secret"
	severityCritical    = "critical"
)

// SecretScanner detects well-known, structurally recognizable secret formats in
// response headers and bodies. It implements ResponseScanner: unconditional and
// read-only (Scan restores resp.Body so later consumers see the payload
// untouched).
type SecretScanner struct{}

// NewSecretScanner returns a SecretScanner with the default detection set.
func NewSecretScanner() *SecretScanner {
	return &SecretScanner{}
}

// Scan inspects response headers first, then the response body, returning one
// Finding per distinct pattern per location (headers, or the body). Findings
// describe WHAT and WHERE in the Detail/Location fields but never embed the
// actual discovered secret value.
func (s *SecretScanner) Scan(resp *http.Response) []Finding {
	findings := make([]Finding, 0)
	// seen dedups per (location, pattern): multiple JWTs in one body report once.
	seen := make(map[string]bool)

	scanText := func(text, location string) {
		for i := range secretPatterns {
			p := &secretPatterns[i]
			if !p.pattern.MatchString(text) {
				continue
			}
			key := location + "\x00" + p.name
			if seen[key] {
				continue
			}
			seen[key] = true
			detail := secretPatternDescriptions[p.name]
			if detail == "" {
				detail = "Detected leaked secret pattern " + p.name
			}
			findings = append(findings, Finding{
				FindingCategory: findingLeakedSecret,
				Detail:          detail + " in " + location,
				FindingSeverity: p.severity,
				Location:        location,
			})
		}
	}

	// 1. Headers first.
	for name, values := range resp.Header {
		for _, v := range values {
			scanText(v, "header:"+name)
		}
	}

	// 2. Body: read once, restore afterward (read-only observation — mirror the
	//    read+restore technique from mangler.go). Though this scanner currently
	//    runs last, other scanners/consumers may need the body, so it is always
	//    restored regardless of execution order.
	original, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(original))
	if err != nil {
		return findings
	}
	scanText(string(original), "body")

	return findings
}

// compile-time check that SecretScanner satisfies ResponseScanner.
var _ ResponseScanner = (*SecretScanner)(nil)