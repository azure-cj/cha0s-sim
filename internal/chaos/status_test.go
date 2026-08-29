package chaos

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"cha0s-sim/internal/config"
)

func newResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode:    status,
		Status:        "200 OK",
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Header:        make(http.Header),
	}
}

func TestOverridesStatusCode(t *testing.T) {
	resp := newResp(200, "original body")
	si := NewStatusInjector(&config.StatusConfig{Code: 504, StripBody: false})

	if err := si.InjectResponse(resp); err != nil {
		t.Fatalf("InjectResponse error = %v, want nil", err)
	}
	if resp.StatusCode != 504 {
		t.Errorf("StatusCode = %d, want 504", resp.StatusCode)
	}
	if resp.Status != "504 Gateway Timeout" {
		t.Errorf("Status = %q, want %q", resp.Status, "504 Gateway Timeout")
	}
}

func TestStripsBodyWhenConfigured(t *testing.T) {
	resp := newResp(200, "original body")
	si := NewStatusInjector(&config.StatusConfig{Code: 503, StripBody: true})

	if err := si.InjectResponse(resp); err != nil {
		t.Fatalf("InjectResponse error = %v, want nil", err)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll error = %v", err)
	}
	if len(data) != 0 {
		t.Errorf("body after strip = %q, want empty", string(data))
	}
	if got := resp.Header.Get("Content-Length"); got != "0" {
		t.Errorf("Content-Length header = %q, want %q", got, "0")
	}
	if resp.ContentLength != 0 {
		t.Errorf("ContentLength = %d, want 0", resp.ContentLength)
	}
}

func TestPreservesBodyWhenStripBodyFalse(t *testing.T) {
	resp := newResp(200, "original body")
	si := NewStatusInjector(&config.StatusConfig{Code: 504, StripBody: false})

	if err := si.InjectResponse(resp); err != nil {
		t.Fatalf("InjectResponse error = %v, want nil", err)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll error = %v", err)
	}
	if string(data) != "original body" {
		t.Errorf("body after inject = %q, want %q", string(data), "original body")
	}
}

func TestUnrecognizedCodeFallsBackToNumericStatus(t *testing.T) {
	resp := newResp(200, "original body")
	si := NewStatusInjector(&config.StatusConfig{Code: 999, StripBody: false})

	if err := si.InjectResponse(resp); err != nil {
		t.Fatalf("InjectResponse error = %v, want nil", err)
	}
	if resp.StatusCode != 999 {
		t.Errorf("StatusCode = %d, want 999", resp.StatusCode)
	}
	if resp.Status != "999" {
		t.Errorf("Status = %q, want %q", resp.Status, "999")
	}
}

func TestReturnsNilError(t *testing.T) {
	resp := newResp(200, "original body")
	si := NewStatusInjector(&config.StatusConfig{Code: 503, StripBody: true})

	if err := si.InjectResponse(resp); err != nil {
		t.Errorf("InjectResponse error = %v, want nil", err)
	}
}
