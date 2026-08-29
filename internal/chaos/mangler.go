package chaos

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"strings"

	"cha0s-sim/internal/config"
)

type ManglerInjector struct {
	Strategy   string
	TargetKeys []string
	randSource func() float64 // randSource currently unused: mangling applies to all eligible keys deterministically once triggered; reserved for future partial-mangle strategies
}

func NewManglerInjector(cfg *config.MangleConfig) *ManglerInjector {
	return &ManglerInjector{
		Strategy:   cfg.Strategy,
		TargetKeys: cfg.TargetKeys,
		randSource: rand.Float64,
	}
}

func (m *ManglerInjector) InjectResponse(resp *http.Response) error {
	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "application/json") {
		return nil
	}

	original, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	var obj map[string]interface{}
	if err := json.Unmarshal(original, &obj); err != nil {
		resp.Body = io.NopCloser(bytes.NewReader(original))
		return nil
	}

	eligible := make([]string, 0)
	if len(m.TargetKeys) > 0 {
		for _, key := range m.TargetKeys {
			if _, ok := obj[key]; ok {
				eligible = append(eligible, key)
			}
		}
	} else {
		for key := range obj {
			eligible = append(eligible, key)
		}
	}

	if len(eligible) == 0 {
		resp.Body = io.NopCloser(bytes.NewReader(original))
		return nil
	}

	for _, key := range eligible {
		switch m.Strategy {
		case "delete_keys":
			delete(obj, key)
		case "corrupt_types":
			obj[key] = corruptValue(obj[key])
		}
	}

	newBytes, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("failed to marshal mangled body: %w", err)
	}

	resp.Body = io.NopCloser(bytes.NewReader(newBytes))
	resp.ContentLength = int64(len(newBytes))
	resp.Header.Set("Content-Length", strconv.Itoa(len(newBytes)))

	return nil
}

func corruptValue(v interface{}) interface{} {
	switch v.(type) {
	case string:
		return float64(12345)
	default:
		return "corrupted"
	}
}
