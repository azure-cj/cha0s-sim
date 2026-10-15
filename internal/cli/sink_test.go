package cli

import (
	"bytes"
	"strings"
	"testing"

	"cha0s-sim/internal/proxy"
)

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
