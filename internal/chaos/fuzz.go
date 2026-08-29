package chaos

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"cha0s-sim/internal/config"
)

// Fuzz strategies. Each maps to an active payload injected into string-valued
// JSON request body fields to test the backend's input validation.
const (
	FuzzSQLi          = "sqli"
	FuzzXSS           = "xss"
	FuzzPathTraversal = "path_traversal"
)

// fuzzPayloads holds the active injection payload per strategy.
var fuzzPayloads = map[string]string{
	FuzzSQLi:          `' OR '1'='1`,
	FuzzXSS:           `<script>alert(1)</script>`,
	FuzzPathTraversal: `../../../etc/passwd`,
}

// FuzzInjector mutates the REQUEST body before it is forwarded to the backend,
// substituting active payloads into string-valued JSON fields. It implements
// chaos.RequestInjector so it runs in the request phase, before the proxy
// forwards to the target.
type FuzzInjector struct {
	Strategy   string
	TargetKeys []string
}

func NewFuzzInjector(cfg *config.FuzzConfig) *FuzzInjector {
	return &FuzzInjector{
		Strategy:   cfg.Strategy,
		TargetKeys: cfg.TargetKeys,
	}
}

// InjectRequest fuzzes string-valued top-level JSON keys in the request body.
// It never "handles" the request — the mutated body is restored into
// r.Body and normal forwarding proceeds (same contract as LatencyInjector).
//
// Content-Length handling: r.ContentLength is the authoritative value — Go's
// http.Transport derives the wire Content-Length header from
// Request.ContentLength (via TransferWriter), overwriting the header, so
// setting the field is what actually matters. r.Header.Set("Content-Length",
// ...) is also set to mirror the response-side mangler technique and keep any
// header-reading code paths consistent.
func (f *FuzzInjector) InjectRequest(w http.ResponseWriter, r *http.Request) bool {
	if !strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		return false
	}

	original, err := io.ReadAll(r.Body)
	if err != nil {
		r.Body = io.NopCloser(bytes.NewReader(original))
		return false
	}
	r.Body.Close()

	var obj map[string]interface{}
	if err := json.Unmarshal(original, &obj); err != nil {
		r.Body = io.NopCloser(bytes.NewReader(original))
		return false
	}

	eligible := make([]string, 0)
	if len(f.TargetKeys) > 0 {
		for _, key := range f.TargetKeys {
			if _, ok := obj[key]; ok {
				eligible = append(eligible, key)
			}
		}
	} else {
		for key := range obj {
			eligible = append(eligible, key)
		}
	}

	payload := fuzzPayloads[f.Strategy]
	changed := false
	for _, key := range eligible {
		// Fuzzing payloads are string-injection attacks: only substitute into
		// string fields. Numbers/bools/nulls/nested objects are left alone —
		// type corruption is the mangler's job, a separate feature.
		if _, isString := obj[key].(string); isString {
			obj[key] = payload
			changed = true
		}
	}

	if !changed {
		r.Body = io.NopCloser(bytes.NewReader(original))
		return false
	}

	newBytes, err := json.Marshal(obj)
	if err != nil {
		r.Body = io.NopCloser(bytes.NewReader(original))
		return false
	}

	r.Body = io.NopCloser(bytes.NewReader(newBytes))
	r.ContentLength = int64(len(newBytes))
	r.Header.Set("Content-Length", strconv.Itoa(len(newBytes)))

	return false
}
