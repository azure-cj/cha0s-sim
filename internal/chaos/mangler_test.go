package chaos

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"testing"

	"cha0s-sim/internal/config"
)

func jsonResp(t *testing.T, body string) *http.Response {
	t.Helper()
	return &http.Response{
		StatusCode:    200,
		Status:        "200 OK",
		Body:          io.NopCloser(bytes.NewReader([]byte(body))),
		ContentLength: int64(len(body)),
		Header:        http.Header{"Content-Type": []string{"application/json"}},
	}
}

func readBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	return data
}

func unmarshalMap(t *testing.T, data []byte) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal failed: %v (body: %s)", err, string(data))
	}
	return m
}

func TestDeleteKeysRemovesSpecifiedTargetKeys(t *testing.T) {
	resp := jsonResp(t, `{"userId": 1, "email": "a@b.com", "role": "admin"}`)
	m := NewManglerInjector(&config.MangleConfig{Strategy: "delete_keys", TargetKeys: []string{"email"}})

	if err := m.InjectResponse(resp); err != nil {
		t.Fatalf("InjectResponse error = %v, want nil", err)
	}

	result := unmarshalMap(t, readBody(t, resp))
	if _, ok := result["email"]; ok {
		t.Error("email key still present after delete_keys")
	}
	if _, ok := result["userId"]; !ok {
		t.Error("userId key missing")
	}
	if _, ok := result["role"]; !ok {
		t.Error("role key missing")
	}
}

func TestDeleteKeysEmptyTargetsRemovesAllKeys(t *testing.T) {
	resp := jsonResp(t, `{"userId": 1, "email": "a@b.com", "role": "admin"}`)
	m := NewManglerInjector(&config.MangleConfig{Strategy: "delete_keys"})

	if err := m.InjectResponse(resp); err != nil {
		t.Fatalf("InjectResponse error = %v, want nil", err)
	}

	result := unmarshalMap(t, readBody(t, resp))
	if len(result) != 0 {
		t.Errorf("expected empty object, got %d keys", len(result))
	}
}

func TestCorruptTypesFlipsStringAndNumber(t *testing.T) {
	resp := jsonResp(t, `{"name": "Alice", "age": 30}`)
	m := NewManglerInjector(&config.MangleConfig{Strategy: "corrupt_types", TargetKeys: []string{"name", "age"}})

	if err := m.InjectResponse(resp); err != nil {
		t.Fatalf("InjectResponse error = %v, want nil", err)
	}

	result := unmarshalMap(t, readBody(t, resp))
	if result["name"] != float64(12345) {
		t.Errorf("name = %v (%T), want float64(12345)", result["name"], result["name"])
	}
	if result["age"] != "corrupted" {
		t.Errorf("age = %v, want %q", result["age"], "corrupted")
	}
}

func TestCorruptTypesHandlesBoolAndNil(t *testing.T) {
	resp := jsonResp(t, `{"active": true, "deleted": null}`)
	m := NewManglerInjector(&config.MangleConfig{Strategy: "corrupt_types"})

	if err := m.InjectResponse(resp); err != nil {
		t.Fatalf("InjectResponse error = %v, want nil", err)
	}

	result := unmarshalMap(t, readBody(t, resp))
	if result["active"] != "corrupted" {
		t.Errorf("active = %v, want %q", result["active"], "corrupted")
	}
	if result["deleted"] != "corrupted" {
		t.Errorf("deleted = %v, want %q", result["deleted"], "corrupted")
	}
}

func TestNonJSONContentTypeLeftUntouched(t *testing.T) {
	body := "<html>hi</html>"
	resp := &http.Response{
		StatusCode:    200,
		Status:        "200 OK",
		Body:          io.NopCloser(bytes.NewReader([]byte(body))),
		ContentLength: int64(len(body)),
		Header:        http.Header{"Content-Type": []string{"text/html"}},
	}
	m := NewManglerInjector(&config.MangleConfig{Strategy: "delete_keys", TargetKeys: []string{"anything"}})

	if err := m.InjectResponse(resp); err != nil {
		t.Fatalf("InjectResponse error = %v, want nil", err)
	}
	if got := string(readBody(t, resp)); got != body {
		t.Errorf("body = %q, want unchanged %q", got, body)
	}
}

func TestMalformedJSONLeftUntouched(t *testing.T) {
	body := "not valid json {{{"
	resp := jsonResp(t, body)
	m := NewManglerInjector(&config.MangleConfig{Strategy: "delete_keys", TargetKeys: []string{"x"}})

	if err := m.InjectResponse(resp); err != nil {
		t.Fatalf("InjectResponse error = %v, want nil", err)
	}
	if got := string(readBody(t, resp)); got != body {
		t.Errorf("body = %q, want original malformed %q", got, body)
	}
}

func TestTargetKeysReferencingNonexistentKeysSkipped(t *testing.T) {
	body := `{"userId": 1}`
	resp := jsonResp(t, body)
	m := NewManglerInjector(&config.MangleConfig{Strategy: "delete_keys", TargetKeys: []string{"doesNotExist"}})

	if err := m.InjectResponse(resp); err != nil {
		t.Fatalf("InjectResponse error = %v, want nil", err)
	}

	result := unmarshalMap(t, readBody(t, resp))
	if _, ok := result["userId"]; !ok {
		t.Error("userId missing; body should be unchanged when no eligible keys")
	}
}

func TestContentLengthHeaderUpdatedAfterMangling(t *testing.T) {
	resp := jsonResp(t, `{"userId": 1, "email": "a@b.com"}`)
	m := NewManglerInjector(&config.MangleConfig{Strategy: "delete_keys", TargetKeys: []string{"email"}})

	if err := m.InjectResponse(resp); err != nil {
		t.Fatalf("InjectResponse error = %v, want nil", err)
	}

	newBody := readBody(t, resp)
	wantLen := strconv.Itoa(len(newBody))
	if got := resp.Header.Get("Content-Length"); got != wantLen {
		t.Errorf("Content-Length header = %q, want %q", got, wantLen)
	}
	if resp.ContentLength != int64(len(newBody)) {
		t.Errorf("ContentLength = %d, want %d", resp.ContentLength, len(newBody))
	}
}
