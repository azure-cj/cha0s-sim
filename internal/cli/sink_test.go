package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"cha0s-sim/internal/proxy"
	"cha0s-sim/internal/security"
)

const awsKey = "AKIA1234567890ABCDEF"

func TestStdoutSinkPrintsFindingRegardlessOfVerbose(t *testing.T) {
	for _, verbose := range []bool{false, true} {
		var buf bytes.Buffer
		sink := &stdoutSink{out: &buf, verbose: verbose}
		sink.Emit(security.Finding{
			FindingCategory: "missing_header",
			Detail:          "missing security header Content-Security-Policy",
			FindingSeverity: "warning",
			Location:        "header:Content-Security-Policy",
			Remediation:     "add a Content-Security-Policy header",
		})

		got := buf.String()
		for _, want := range []string{
			"SECURITY FINDING [warning]",
			"missing_header: missing security header Content-Security-Policy",
			"location: header:Content-Security-Policy",
			"remediation: add a Content-Security-Policy header",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("verbose=%v: output missing %q:\n%s", verbose, want, got)
			}
		}
	}
}

func TestStdoutSinkGatesTrafficBehindVerbose(t *testing.T) {
	evt := proxy.TrafficEvent{Method: "GET", Path: "/api/users", Status: 200, DurationMs: 7}

	var quiet bytes.Buffer
	(&stdoutSink{out: &quiet, verbose: false}).Emit(evt)
	if quiet.Len() != 0 {
		t.Errorf("non-verbose sink printed traffic: %q, want silence", quiet.String())
	}

	var loud bytes.Buffer
	(&stdoutSink{out: &loud, verbose: true}).Emit(evt)
	if !strings.Contains(loud.String(), "GET /api/users -> 200 (7ms)") {
		t.Errorf("verbose sink output = %q, want traffic summary line", loud.String())
	}
}

// TestCLIProxyPrintsFindingsForLeakyBackend exercises the real wiring: a live
// request through proxy.NewServerInstance with a stdoutSink must print findings
// (header-validator warnings AND the leaked-secret critical) to the sink, and
// must never echo the secret value itself into the output.
func TestCLIProxyPrintsFindingsForLeakyBackend(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"api_key":"` + awsKey + `"}`))
	}))
	t.Cleanup(backend.Close)

	var buf bytes.Buffer
	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatalf("parse backend URL: %v", err)
	}
	srv := proxy.NewServerInstance(0, target, false, false, false, nil, proxy.PipelineFull, "", nil, &stdoutSink{out: &buf, verbose: false})
	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/api/users")
	if err != nil {
		t.Fatalf("request through proxy failed: %v", err)
	}
	resp.Body.Close()

	got := buf.String()
	if !strings.Contains(got, "SECURITY FINDING") {
		t.Fatalf("no finding printed for leaky backend:\n%s", got)
	}
	for _, want := range []string{
		"missing_header: missing security header Content-Security-Policy",
		"leaked_secret: Detected AWS Access Key ID pattern in body",
		"remediation:",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	// Findings must never leak the discovered secret back into the output.
	if strings.Contains(got, awsKey) {
		t.Errorf("output leaks the secret value %q:\n%s", awsKey, got)
	}
	// The sink must not emit traffic lines without verbose (the proxy's own
	// access-log middleware is orthogonal and writes to os.Stdout, not here).
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "->") {
			t.Errorf("non-verbose sink output contains a traffic line %q", line)
		}
	}
}