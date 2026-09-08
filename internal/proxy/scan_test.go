package proxy

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"cha0s-sim/internal/platform"
	"cha0s-sim/internal/security"
)

// fakeScanner returns a canned set of findings and records whether it was
// actually invoked, so the nil-sink path can assert zero scan work happens.
type fakeScanner struct {
	findings []security.Finding
	called   bool
}

func (s *fakeScanner) Scan(resp *http.Response) []security.Finding {
	s.called = true
	return s.findings
}

// recordSink captures everything Emit()'d so tests can assert what flowed
// through the EventSink (now Reportable-widened) untouched.
type recordSink struct {
	emitted []platform.Reportable
}

func (r *recordSink) Emit(evt platform.Reportable) {
	r.emitted = append(r.emitted, evt)
}

func scanResp(body []byte) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Status:     "200 OK",
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: io.NopCloser(bytes.NewReader(body)),
	}
}

func snapshotResp(resp *http.Response) (statusCode int, status string, headers map[string][]string, body []byte) {
	headers = make(map[string][]string, len(resp.Header))
	for k, vs := range resp.Header {
		headers[k] = append([]string(nil), vs...)
	}
	body, _ = io.ReadAll(resp.Body)
	// Restore the body so the response remains usable in both the "before"
	// snapshot and the actual RunScanners call that follows.
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp.StatusCode, resp.Status, headers, body
}

func TestRunScannersEmitsFindings(t *testing.T) {
	findings := []security.Finding{
		{FindingCategory: "missing_header", Detail: "missing security header Content-Security-Policy", FindingSeverity: "warning", Location: "header:Content-Security-Policy"},
		{FindingCategory: "missing_header", Detail: "missing security header Strict-Transport-Security", FindingSeverity: "warning", Location: "header:Strict-Transport-Security"},
	}
	scanner := &fakeScanner{findings: findings}
	sink := &recordSink{}

	RunScanners(scanResp([]byte("hello")), []security.ResponseScanner{scanner}, sink)

	if !scanner.called {
		t.Fatal("scanner was never invoked")
	}
	if len(sink.emitted) != 2 {
		t.Fatalf("emitted %d findings, want 2", len(sink.emitted))
	}
	for i, evt := range sink.emitted {
		f, ok := evt.(security.Finding)
		if !ok {
			t.Fatalf("emitted item %d is %T, want security.Finding", i, evt)
		}
		if f != findings[i] {
			t.Errorf("emitted finding %d = %+v, want %+v", i, f, findings[i])
		}
	}
}

func TestRunScannersDoesNotMutateResponse(t *testing.T) {
	body := []byte(`{"message":"original payload"}`)
	resp := scanResp(body)
	scanner := &fakeScanner{findings: []security.Finding{{FindingCategory: "missing_header", Detail: "x", FindingSeverity: "warning", Location: "y"}}}
	sink := &recordSink{}

	wantCode, wantStatus, wantHeaders, wantBody := snapshotResp(resp)
	// Sanity: the snapshot itself proves the body is the original.
	if !bytes.Equal(wantBody, body) {
		t.Fatalf("snapshot body = %q, want %q", wantBody, body)
	}

	RunScanners(resp, []security.ResponseScanner{scanner}, sink)

	if len(sink.emitted) != 1 {
		t.Fatalf("emitted %d findings, want 1", len(sink.emitted))
	}
	if resp.StatusCode != wantCode {
		t.Errorf("StatusCode = %d, want unchanged %d", resp.StatusCode, wantCode)
	}
	if resp.Status != wantStatus {
		t.Errorf("Status = %q, want unchanged %q", resp.Status, wantStatus)
	}
	if len(resp.Header) != len(wantHeaders) {
		t.Fatalf("header count = %d, want unchanged %d", len(resp.Header), len(wantHeaders))
	}
	for k, want := range wantHeaders {
		got := resp.Header.Values(k)
		if len(got) != len(want) {
			t.Errorf("header %q values = %v, want %v (unchanged)", k, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("header %q values = %v, want %v (unchanged)", k, got, want)
			}
		}
	}
	gotBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("body unreadable after RunScanners: %v", err)
	}
	if !bytes.Equal(gotBody, wantBody) {
		t.Errorf("body = %q, want unchanged %q", gotBody, wantBody)
	}
}

func TestRunScannersNilSinkIsSafeNoOp(t *testing.T) {
	scanner := &fakeScanner{findings: []security.Finding{{FindingCategory: "missing_header", Detail: "x", FindingSeverity: "warning", Location: "y"}}}

	RunScanners(scanResp([]byte("hello")), []security.ResponseScanner{scanner}, nil)

	if scanner.called {
		t.Error("scanner was invoked despite a nil sink; want zero scan work")
	}
}

func TestRunScannersEmptyScannerListIsSafe(t *testing.T) {
	sink := &recordSink{}
	RunScanners(scanResp([]byte("hello")), nil, sink)
	if len(sink.emitted) != 0 {
		t.Errorf("emitted %d findings with no scanners, want 0", len(sink.emitted))
	}
}

// TestNewServerInstanceEmitsFindingsOnRealTraffic exercises the FULL pipeline:
// a live request through NewServerInstance must produce the HeaderValidator's
// findings (the default test backend serves no security headers) AND the
// usual TrafficEvent, both via the same sink. Findings are emitted first
// (during ModifyResponse, inside ServeHTTP); the traffic event is emitted
// after ServeHTTP returns.
func TestNewServerInstanceEmitsFindingsOnRealTraffic(t *testing.T) {
	_, backendURL := newBackend(t)
	sink := newCollectingSink()
	tsURL := eventProxy(t, backendURL, nil, sink, "")

	resp, err := http.Get(tsURL + "/api/users")
	if err != nil {
		t.Fatalf("request through proxy failed: %v", err)
	}
	resp.Body.Close()

	// All emits are synchronous within the handler chain, so by the time the
	// client Get returned, the (buffered) sink holds everything: 4
	// missing_header findings + 1 traffic event.
	if len(sink.events) != 5 {
		t.Fatalf("sink holds %d events, want 5 (4 missing-header findings + 1 traffic event)", len(sink.events))
	}

	for i := 0; i < 4; i++ {
		f, ok := (<-sink.events).(security.Finding)
		if !ok {
			t.Fatalf("event %d is not a security.Finding", i)
		}
		if f.FindingCategory != "missing_header" || f.Severity() != "warning" {
			t.Errorf("finding %d = %+v, want a warning missing_header finding", i, f)
		}
	}

	te, ok := (<-sink.events).(TrafficEvent)
	if !ok {
		t.Fatalf("last event is %T, want TrafficEvent", <-sink.events)
	}
	if te.Method != http.MethodGet || te.Path != "/api/users" {
		t.Errorf("traffic event = %s %s, want GET /api/users", te.Method, te.Path)
	}
}
