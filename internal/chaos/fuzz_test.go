package chaos

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"testing"

	"cha0s-sim/internal/config"
)

func jsonReq(t *testing.T, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://localhost:8080/api/echo", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("failed to build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = int64(len(body))
	return req
}

func readReqBody(t *testing.T, req *http.Request) []byte {
	t.Helper()
	data, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	return data
}

func assertAlwaysReturnsFalse(t *testing.T, f *FuzzInjector, req *http.Request) {
	t.Helper()
	if handled := f.InjectRequest(nil, req); handled {
		t.Fatal("InjectRequest returned true, want false (fuzzing never handles the request)")
	}
}

func TestFuzzSQLiReplacesTargetStringKeys(t *testing.T) {
	req := jsonReq(t, `{"email": "a@b.com", "role": "admin", "userId": 1}`)
	f := NewFuzzInjector(&config.FuzzConfig{Strategy: FuzzSQLi, TargetKeys: []string{"email"}})
	assertAlwaysReturnsFalse(t, f, req)

	result := unmarshalMap(t, readReqBody(t, req))
	if result["email"] != `' OR '1'='1` {
		t.Errorf("email = %q, want SQLi payload", result["email"])
	}
	if result["role"] != "admin" {
		t.Errorf("role = %q, want unchanged %q", result["role"], "admin")
	}
	if result["userId"] != float64(1) {
		t.Errorf("userId = %v, want unchanged 1", result["userId"])
	}
	if req.ContentLength != int64(len(`{"email":"' OR '1'='1","role":"admin","userId":1}`)) {
		t.Errorf("ContentLength = %d, want re-marshaled length", req.ContentLength)
	}
}

func TestFuzzXSSReplacesTargetStringKeys(t *testing.T) {
	req := jsonReq(t, `{"query": "hello", "name": "world"}`)
	f := NewFuzzInjector(&config.FuzzConfig{Strategy: FuzzXSS, TargetKeys: []string{"query"}})
	assertAlwaysReturnsFalse(t, f, req)

	result := unmarshalMap(t, readReqBody(t, req))
	if result["query"] != `<script>alert(1)</script>` {
		t.Errorf("query = %q, want XSS payload", result["query"])
	}
	if result["name"] != "world" {
		t.Errorf("name = %q, want unchanged %q", result["name"], "world")
	}
}

func TestFuzzPathTraversalReplacesTargetStringKeys(t *testing.T) {
	req := jsonReq(t, `{"path": "/safe", "mode": "read"}`)
	f := NewFuzzInjector(&config.FuzzConfig{Strategy: FuzzPathTraversal, TargetKeys: []string{"path"}})
	assertAlwaysReturnsFalse(t, f, req)

	result := unmarshalMap(t, readReqBody(t, req))
	if result["path"] != `../../../etc/passwd` {
		t.Errorf("path = %q, want path traversal payload", result["path"])
	}
	if result["mode"] != "read" {
		t.Errorf("mode = %q, want unchanged %q", result["mode"], "read")
	}
}

func TestFuzzLeavesNonStringValuesUntouched(t *testing.T) {
	body := `{"count": 5, "active": true, "note": null, "nested": {"email": "a@b.com"}, "email": "a@b.com"}`
	req := jsonReq(t, body)
	f := NewFuzzInjector(&config.FuzzConfig{Strategy: FuzzSQLi, TargetKeys: []string{"count", "active", "note", "nested", "email"}})
	assertAlwaysReturnsFalse(t, f, req)

	result := unmarshalMap(t, readReqBody(t, req))
	if result["count"] != float64(5) {
		t.Errorf("count = %v, want unchanged 5", result["count"])
	}
	if result["active"] != true {
		t.Errorf("active = %v, want unchanged true", result["active"])
	}
	if result["note"] != nil {
		t.Errorf("note = %v, want unchanged null", result["note"])
	}
	if result["email"] != `' OR '1'='1` {
		t.Errorf("email = %q, want SQLi payload (string field)", result["email"])
	}
}

func TestFuzzNonJSONContentTypeLeftUntouched(t *testing.T) {
	body := `name=alice`
	req, err := http.NewRequest(http.MethodPost, "http://localhost:8080/api/echo", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("failed to build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.ContentLength = int64(len(body))
	f := NewFuzzInjector(&config.FuzzConfig{Strategy: FuzzSQLi, TargetKeys: []string{"name"}})
	assertAlwaysReturnsFalse(t, f, req)

	if got := string(readReqBody(t, req)); got != body {
		t.Errorf("body = %q, want unchanged %q", got, body)
	}
}

func TestFuzzMalformedJSONLeftUntouchedAndReadable(t *testing.T) {
	body := "not valid json {{{"
	req := jsonReq(t, body)
	f := NewFuzzInjector(&config.FuzzConfig{Strategy: FuzzSQLi, TargetKeys: []string{"x"}})
	assertAlwaysReturnsFalse(t, f, req)

	if got := string(readReqBody(t, req)); got != body {
		t.Errorf("body = %q, want original malformed %q", got, body)
	}
}

func TestFuzzEmptyTargetKeysFuzzesAllStringKeys(t *testing.T) {
	req := jsonReq(t, `{"email": "a@b.com", "role": "admin", "userId": 1}`)
	f := NewFuzzInjector(&config.FuzzConfig{Strategy: FuzzXSS})
	assertAlwaysReturnsFalse(t, f, req)

	result := unmarshalMap(t, readReqBody(t, req))
	if result["email"] != `<script>alert(1)</script>` {
		t.Errorf("email = %q, want XSS payload", result["email"])
	}
	if result["role"] != `<script>alert(1)</script>` {
		t.Errorf("role = %q, want XSS payload", result["role"])
	}
	if result["userId"] != float64(1) {
		t.Errorf("userId = %v, want untouched 1 (non-string)", result["userId"])
	}
}

func TestFuzzAlwaysReturnsFalse(t *testing.T) {
	cases := []struct {
		name string
		body string
		fuzz *config.FuzzConfig
	}{
		{name: "json body", body: `{"a": "b"}`, fuzz: &config.FuzzConfig{Strategy: FuzzSQLi}},
		{name: "no eligible keys", body: `{"a": 1}`, fuzz: &config.FuzzConfig{Strategy: FuzzSQLi}},
		{name: "malformed json", body: "{{{{", fuzz: &config.FuzzConfig{Strategy: FuzzSQLi}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := jsonReq(t, tc.body)
			assertAlwaysReturnsFalse(t, NewFuzzInjector(tc.fuzz), req)
		})
	}
}

func TestFuzzContentLengthHeaderAndFieldUpdated(t *testing.T) {
	req := jsonReq(t, `{"email": "a@b.com"}`)
	f := NewFuzzInjector(&config.FuzzConfig{Strategy: FuzzSQLi, TargetKeys: []string{"email"}})
	assertAlwaysReturnsFalse(t, f, req)

	newBody := readReqBody(t, req)
	wantLen := strconv.Itoa(len(newBody))
	if got := req.Header.Get("Content-Length"); got != wantLen {
		t.Errorf("Content-Length header = %q, want %q", got, wantLen)
	}
	if req.ContentLength != int64(len(newBody)) {
		t.Errorf("ContentLength = %d, want %d", req.ContentLength, len(newBody))
	}
	// Round-trip: the body must still unmarshal with the fuzzed value intact.
	result := unmarshalMap(t, newBody)
	if result["email"] != `' OR '1'='1` {
		t.Errorf("email = %q, want SQLi payload", result["email"])
	}
}

// compile-time check that FuzzInjector satisfies RequestInjector
var _ RequestInjector = (*FuzzInjector)(nil)
