package config

import (
	"fmt"
	"strings"
)

// CreateRuleRequest is the frontend contract for building a new chaos rule.
// Every field comes from the desktop "create rule" form and is validated by
// BuildRuleFromRequest before a Rule is ever constructed, so the store can
// never receive a malformed rule.
type CreateRuleRequest struct {
	Name      string   `json:"name"`
	Path      string   `json:"path"`
	Methods   []string `json:"methods"`
	Frequency string   `json:"frequency"` // one of: always, often, sometimes, rarely
	Effect    string   `json:"effect"`    // one of: slow, error, drop, corrupt, attack

	// Effect-specific inputs; only the one matching Effect is consulted.
	SlowMs     int      `json:"slowMs,omitempty"` // slow: fixed latency in ms (default 1000)
	ErrorCode  int      `json:"errorCode,omitempty"` // error: HTTP status to force (default 500)
	TargetKeys []string `json:"targetKeys,omitempty"` // corrupt: key(s) to delete (at least one)
	Attack     string   `json:"attack,omitempty"` // attack: fuzz strategy (sqli, xss, path_traversal)
}

// frequencyRates maps the UI's plain-language frequency choices onto the
// rule's error_rate. A chaos effect with error_rate 0 never fires, so the
// AT LEAST 1-in-20 downscales mirror the CLI's documented suggestions.
var frequencyRates = map[string]float64{
	"always":    1.0,
	"often":     0.5,
	"sometimes": 0.2,
	"rarely":    0.05,
}

// validMethods are the only HTTP methods the create-rule form accepts; an
// empty Methods list means the rule matches every method.
var validMethods = map[string]bool{
	"GET":    true,
	"POST":   true,
	"PUT":    true,
	"DELETE": true,
	"PATCH":  true,
}

const (
	defaultSlowMs    = 1000
	defaultErrorCode = 500
)

// BuildRuleFromRequest turns a validated CreateRuleRequest into a config.Rule.
// Notes on the mapping choices:
//   - frequency -> error_rate (see frequencyRates). "always" produces 1.0 so the
//     effect triggers on every matching request, the visual default.
//   - slow does not add jitter: the UI only exposes a fixed delay.
//   - error defaults to 500 when no code is given, and validateRule enforces the
//     HTTP range (100-599) after the default is applied.
//   - corrupt always uses the delete_keys strategy with an explicit enabled: true
//     and requires at least one target key.
//   - attack maps to an active fuzz strategy; empty TargetKeys means all string
//     request fields are injected, matching CLI semantics.
//   - enabled is always true and the path is always an exact match — never a
//     regex — so a freshly created rule acts immediately and predictably.
//
// Errors are written for humans reading them in the UI, not for logs: they name
// the offending field and what is expected.
func BuildRuleFromRequest(req CreateRuleRequest) (*Rule, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, fmt.Errorf("rule name is required")
	}
	path := strings.TrimSpace(req.Path)
	if path == "" {
		return nil, fmt.Errorf("rule path is required — enter the exact path to target (e.g. /api/users)")
	}

	rate, ok := frequencyRates[strings.ToLower(strings.TrimSpace(req.Frequency))]
	if !ok {
		return nil, fmt.Errorf("invalid frequency %q — must be one of: always, often, sometimes, rarely", req.Frequency)
	}

	methods := make([]string, 0, len(req.Methods))
	for _, m := range req.Methods {
		m = strings.ToUpper(strings.TrimSpace(m))
		if m == "" {
			continue
		}
		if !validMethods[m] {
			return nil, fmt.Errorf("unsupported method %q — must be one of: GET, POST, PUT, DELETE, PATCH", m)
		}
		methods = append(methods, m)
	}

	rule := &Rule{
		Name:      name,
		Path:      path,
		Methods:   methods,
		ErrorRate: rate,
		Enabled:   true,
	}

	switch strings.ToLower(strings.TrimSpace(req.Effect)) {
	case "slow":
		ms := req.SlowMs
		if ms <= 0 {
			ms = defaultSlowMs
		}
		rule.Latency = &LatencyConfig{FixedMs: ms}
	case "error":
		code := req.ErrorCode
		if code == 0 {
			code = defaultErrorCode
		}
		rule.StatusOverride = &StatusConfig{Code: code}
	case "drop":
		rule.DropConnection = true
	case "corrupt":
		if len(req.TargetKeys) == 0 {
			return nil, fmt.Errorf("corrupt effect requires at least one target key to delete from the response")
		}
		rule.Mangle = &MangleConfig{
			Enabled:    true,
			Strategy:   "delete_keys",
			TargetKeys: req.TargetKeys,
		}
	case "attack":
		strategy := strings.ToLower(strings.TrimSpace(req.Attack))
		if !validFuzzStrategies[strategy] {
			return nil, fmt.Errorf("unsupported attack %q — must be one of: sqli, xss, path_traversal", req.Attack)
		}
		rule.Fuzz = &FuzzConfig{Strategy: strategy}
	default:
		return nil, fmt.Errorf("unsupported effect %q — must be one of: slow, error, drop, corrupt, attack", req.Effect)
	}

	// Reuse the same per-rule validation the config loader applies on startup
	// and hot-reload, so a request-built rule can never end up in a file the
	// reactor (or the store watcher) would reject.
	if problems := validateRule(rule, rule.Name); len(problems) > 0 {
		return nil, fmt.Errorf("%s", joinProblems(problems))
	}

	return rule, nil
}