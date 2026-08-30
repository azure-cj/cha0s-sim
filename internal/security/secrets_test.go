package security

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Test fixture secret values. These are fake/well-known sample credentials —
// the exact strings the safety test below must prove can never appear inside a
// Finding's Detail or Location.
const (
	fixtureAWSAccessKeyID = "AKIAIOSFODNN7EXAMPLE"
	fixtureJWT            = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	fixtureBearerToken    = "AbCdEfGhIjKlMnOpQrStUvWxYz1234567890abc"
	fixtureAWSSecret      = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	fixturePrivateKey     = "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA7dQmGXdKpX9OXhDdU8mYpP1QnRZq3JvNFWnT9zBL5z4Qdb7VtPAbEZqLk2gZhCWRjXQ6fWn4TvZICbBFsdMGDgpIh5EUDwAjcVkTEmGnaNpJvP3zsKJ9yWXYA8bKzJ6NxJhO0v5CqLbM3i8Q7hR1gTSaVdUjqBcL2eFPsM0KAXAQAB\n-----END RSA PRIVATE KEY-----"
)

// makeBodyResponse builds a response with the given headers and a readable
// body, reusing the existing makeResponse helper for header construction.
func makeBodyResponse(headers map[string]string, body string) *http.Response {
	resp := makeResponse(headers)
	resp.Body = io.NopCloser(bytes.NewReader([]byte(body)))
	return resp
}

func hasLeakFinding(findings []Finding, severity, location string) bool {
	for _, f := range findings {
		if f.FindingCategory == findingLeakedSecret && f.FindingSeverity == severity && f.Location == location {
			return true
		}
	}
	return false
}

func countForPattern(findings []Finding, category, location string) int {
	n := 0
	for _, f := range findings {
		if f.FindingCategory == category && f.Location == location {
			n++
		}
	}
	return n
}

func TestSecretScannerAWSAccessKeyIDInBody(t *testing.T) {
	resp := makeBodyResponse(nil, `{"credentials":{"awsKey":"`+fixtureAWSAccessKeyID+`"}}`)

	findings := NewSecretScanner().Scan(resp)

	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.FindingCategory != findingLeakedSecret {
		t.Errorf("category = %q, want %q", f.FindingCategory, findingLeakedSecret)
	}
	if f.FindingSeverity != severityCritical {
		t.Errorf("severity = %q, want %q", f.FindingSeverity, severityCritical)
	}
	if f.Location != "body" {
		t.Errorf("location = %q, want %q", f.Location, "body")
	}
}

func TestSecretScannerAWSAccessKeyIDInHeader(t *testing.T) {
	const headerName = "X-Amz-Credential"
	resp := makeBodyResponse(map[string]string{
		headerName: fixtureAWSAccessKeyID + "/20260101/us-east-1/execute-api/aws4_request",
	}, "ok")

	findings := NewSecretScanner().Scan(resp)

	if !hasLeakFinding(findings, severityCritical, "header:"+headerName) {
		t.Errorf("missing header finding; got %+v", findings)
	}
}

func TestSecretScannerJWTInBody(t *testing.T) {
	resp := makeBodyResponse(nil, `{"token":"`+fixtureJWT+`"}`)

	findings := NewSecretScanner().Scan(resp)

	if !hasLeakFinding(findings, severityWarning, "body") {
		t.Errorf("missing JWT finding; got %+v", findings)
	}
}

func TestSecretScannerPrivateKeyInBody(t *testing.T) {
	resp := makeBodyResponse(nil, fixturePrivateKey)

	findings := NewSecretScanner().Scan(resp)

	if !hasLeakFinding(findings, severityCritical, "body") {
		t.Errorf("missing private key finding; got %+v", findings)
	}
}

func TestSecretScannerGenericBearerToken(t *testing.T) {
	resp := makeBodyResponse(map[string]string{
		"Authorization": "Bearer " + fixtureBearerToken,
	}, "ok")

	findings := NewSecretScanner().Scan(resp)

	if !hasLeakFinding(findings, severityWarning, "header:Authorization") {
		t.Errorf("missing bearer token finding; got %+v", findings)
	}
}

func TestSecretScannerCleanResponseNoFindings(t *testing.T) {
	resp := makeBodyResponse(map[string]string{
		"Content-Type":  "text/plain",
		"X-Request-ID":  "req-123",
		"Cache-Control": "no-store",
	}, "hello, world")

	findings := NewSecretScanner().Scan(resp)

	if len(findings) != 0 {
		t.Fatalf("clean response produced %d findings: %+v", len(findings), findings)
	}
}

func TestSecretScannerDedupSamePatternInBody(t *testing.T) {
	// Three JWT-shaped tokens in one body must yield exactly ONE finding, not
	// one per match.
	body := `token1=` + fixtureJWT + ",token2=" + fixtureJWT + ",token3=" + fixtureJWT
	resp := makeBodyResponse(nil, body)

	findings := NewSecretScanner().Scan(resp)

	if len(findings) != 1 {
		t.Fatalf("got %d findings for repeated JWT pattern, want 1: %+v", len(findings), findings)
	}
	if findings[0].Location != "body" {
		t.Errorf("location = %q, want %q", findings[0].Location, "body")
	}
}

func TestSecretScannerDedupSamePatternAcrossHeaderValues(t *testing.T) {
	// Multiple values of the same header containing the same pattern class must
	// yield one finding per location (dedup key is location+pattern). The second
	// value is a deliberately-40-char token so it demonstrably matches too —
	// without dedup there would be two findings.
	resp := makeBodyResponse(map[string]string{
		"Proxy-Authorization": "Bearer " + fixtureBearerToken,
	}, "ok")
	resp.Header.Add("Proxy-Authorization", "Bearer "+strings.Repeat("x", 40))

	findings := NewSecretScanner().Scan(resp)

	if n := countForPattern(findings, findingLeakedSecret, "header:Proxy-Authorization"); n != 1 {
		t.Fatalf("got %d findings for header:Proxy-Authorization, want 1 (deduped): %+v", n, findings)
	}
}

func TestSecretScannerMultipleDistinctPatterns(t *testing.T) {
	// Four different pattern classes in one synthetic JSON body: one finding
	// per distinct pattern.
	resp := makeBodyResponse(map[string]string{
		"Authorization": "Bearer " + fixtureBearerToken,
	}, `{"awsKey":"`+fixtureAWSAccessKeyID+`","token":"`+fixtureJWT+`","secret":"aws_secret_access_key = "`+fixtureAWSSecret+`""}`)

	findings := NewSecretScanner().Scan(resp)

	expected := map[string]int{
		"header:Authorization": 1, // generic_bearer_token
		"body":                 3, // aws_access_key_id, jwt, aws_secret_access_key
	}
	for loc, want := range expected {
		if got := countForPattern(findings, findingLeakedSecret, loc); got != want {
			t.Errorf("location %q produced %d findings, want %d: %+v", loc, got, want, findings)
		}
	}
	if len(findings) != 4 {
		t.Errorf("total %d findings, want 4 (one per distinct pattern): %+v", len(findings), findings)
	}
}

func TestSecretScannerFindingsNeverLeakSecrets(t *testing.T) {
	// SAFETY REQUIREMENT: most important test in this package. For every
	// finding produced by every realistic input, the actual secret substrings
	// from the input must not appear anywhere in the finding's Detail or
	// Location. Findings must describe WHAT and WHERE, never the secret itself.
	cases := []struct {
		name     string
		headers  map[string]string
		body     string
		secrets  []string
	}{
		{"aws-access-key-id-in-body", nil, `{"awsKey":"` + fixtureAWSAccessKeyID + `"}`, []string{fixtureAWSAccessKeyID}},
		{"aws-access-key-id-in-header", map[string]string{"X-Amz-Credential": fixtureAWSAccessKeyID + "/scope"}, "", []string{fixtureAWSAccessKeyID}},
		{"aws-secret-assignment", nil, `{"x":"aws_secret_access_key = "` + fixtureAWSSecret + `""}`, []string{fixtureAWSSecret}},
		{"jwt-in-body", nil, `{"token":"` + fixtureJWT + `"}`, []string{fixtureJWT}},
		{"private-key-block", nil, fixturePrivateKey, []string{fixturePrivateKey, "-----BEGIN RSA PRIVATE KEY-----"}},
		{"bearer-in-header", map[string]string{"Authorization": "Bearer " + fixtureBearerToken}, "", []string{fixtureBearerToken, "Bearer " + fixtureBearerToken}},
		{"repeated-jwts-deduped", nil, `a=` + fixtureJWT + `,b=` + fixtureJWT, []string{fixtureJWT}},
		{"api-key-assignment", nil, `"api_key":"hunter2hunter2hunter2hunter2"`, []string{"hunter2hunter2hunter2hunter2"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := makeBodyResponse(tc.headers, tc.body)
			findings := NewSecretScanner().Scan(resp)

			if len(findings) == 0 {
				t.Fatalf("expected findings to verify non-leakage, got none")
			}

			for _, f := range findings {
				for _, secret := range tc.secrets {
					if strings.Contains(f.Detail, secret) {
						t.Errorf("Finding.Detail %q leaks secret %q", f.Detail, secret)
					}
					if strings.Contains(f.Location, secret) {
						t.Errorf("Finding.Location %q leaks secret %q", f.Location, secret)
					}
				}
			}
		})
	}
}

func TestSecretScannerRestoresResponseBody(t *testing.T) {
	body := []byte(`{"token":"` + fixtureJWT + `"}`)
	resp := makeBodyResponse(nil, string(body))

	NewSecretScanner().Scan(resp)

	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("body unreadable after Scan: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("body after Scan = %q, want unchanged %q", got, body)
	}
	// Restore after the manual read (mirroring scan_test.go's snapshotResp),
	// so the response is fully usable for the re-scan below.
	resp.Body = io.NopCloser(bytes.NewReader(got))

	// The restored body must be re-scanable (second read yields the same
	// finding), proving the scanner is a read-only observer.
	findings := NewSecretScanner().Scan(resp)
	if !hasLeakFinding(findings, severityWarning, "body") {
		t.Errorf("second Scan lost the JWT finding: %+v", findings)
	}
}

func TestSecretScannerImplementsResponseScanner(t *testing.T) {
	var _ ResponseScanner = (*SecretScanner)(nil)
}