package config

import (
	"fmt"
	"regexp"
)

var validMangleStrategies = map[string]bool{"delete_keys": true, "corrupt_types": true}

type Config struct {
	Rules []Rule `yaml:"rules"`
}

type Rule struct {
	Name           string            `yaml:"name"`
	Path           string            `yaml:"path,omitempty"`
	PathRegex      string            `yaml:"path_regex,omitempty"`
	Methods        []string          `yaml:"methods,omitempty"`
	Headers        map[string]string `yaml:"headers,omitempty"`
	ErrorRate      float64           `yaml:"error_rate"`
	Enabled        bool              `yaml:"enabled"`
	Latency        *LatencyConfig    `yaml:"latency,omitempty"`
	StatusOverride *StatusConfig     `yaml:"status_override,omitempty"`
	DropConnection bool              `yaml:"drop_connection,omitempty"`
	Mangle         *MangleConfig     `yaml:"mangle,omitempty"`
}

type LatencyConfig struct {
	FixedMs     int `yaml:"fixed_ms,omitempty"`
	JitterMinMs int `yaml:"jitter_min_ms,omitempty"`
	JitterMaxMs int `yaml:"jitter_max_ms,omitempty"`
}

type StatusConfig struct {
	Code      int  `yaml:"code"`
	StripBody bool `yaml:"strip_body,omitempty"`
}

type MangleConfig struct {
	Enabled    bool     `yaml:"enabled"`
	Strategy   string   `yaml:"strategy"`
	TargetKeys []string `yaml:"target_keys,omitempty"`
}

func (c *Config) Validate() error {
	var problems []string
	for i := range c.Rules {
		rule := &c.Rules[i]
		id := rule.Name
		if id == "" {
			id = fmt.Sprintf("rule[%d]", i)
		}
		problems = append(problems, validateRule(rule, id)...)
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%s", joinProblems(problems))
}

func (c *Config) Warnings() []string {
	var warnings []string
	for i := range c.Rules {
		rule := &c.Rules[i]
		id := rule.Name
		if id == "" {
			id = fmt.Sprintf("rule[%d]", i)
		}
		if !rule.Enabled {
			warnings = append(warnings, fmt.Sprintf("%s: rule is disabled (enabled: false or omitted) and will never match — add 'enabled: true' if this rule should be active", id))
		}
	}
	return warnings
}

func joinProblems(problems []string) string {
	result := ""
	for i, p := range problems {
		if i > 0 {
			result += "; "
		}
		result += p
	}
	return result
}

func validateRule(rule *Rule, id string) []string {
	var problems []string
	appendProblem := func(format string, args ...interface{}) {
		problems = append(problems, fmt.Sprintf(id+": "+format, args...))
	}

	if rule.Path != "" && rule.PathRegex != "" {
		appendProblem("path and path_regex are mutually exclusive, only one may be set")
	}

	if rule.PathRegex != "" {
		if _, err := regexp.Compile(rule.PathRegex); err != nil {
			appendProblem("invalid path_regex: %v", err)
		}
	}

	if rule.Path == "" && rule.PathRegex == "" {
		appendProblem("either path or path_regex must be set")
	}

	if rule.ErrorRate < 0.0 || rule.ErrorRate > 1.0 {
		appendProblem("error_rate must be between 0.0 and 1.0, got %v", rule.ErrorRate)
	}

	if rule.Mangle != nil && rule.Mangle.Enabled {
		if !validMangleStrategies[rule.Mangle.Strategy] {
			appendProblem("invalid mangle strategy '%s', must be one of: delete_keys, corrupt_types", rule.Mangle.Strategy)
		}
	}

	if rule.Latency != nil {
		if rule.Latency.JitterMinMs != 0 && rule.Latency.JitterMaxMs != 0 && rule.Latency.JitterMinMs > rule.Latency.JitterMaxMs {
			appendProblem("latency jitter_min_ms (%d) must be <= jitter_max_ms (%d)", rule.Latency.JitterMinMs, rule.Latency.JitterMaxMs)
		}
	}

	if rule.StatusOverride != nil {
		if rule.StatusOverride.Code < 100 || rule.StatusOverride.Code > 599 {
			appendProblem("status_override code %d is not a valid HTTP status code (must be 100-599)", rule.StatusOverride.Code)
		}
	}

	return problems
}
