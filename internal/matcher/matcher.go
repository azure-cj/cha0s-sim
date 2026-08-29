package matcher

import (
	"net/http"
	"regexp"
	"strings"

	"cha0s-sim/internal/config"
)

func Match(req *http.Request, rules []config.Rule) []config.Rule {
	var matched []config.Rule

	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}

		if !pathMatches(req, rule) {
			continue
		}
		if !methodMatches(req, rule) {
			continue
		}
		if !headersMatch(req, rule) {
			continue
		}

		matched = append(matched, rule)
	}

	return matched
}

func pathMatches(req *http.Request, rule config.Rule) bool {
	switch {
	case rule.Path != "":
		return req.URL.Path == rule.Path
	case rule.PathRegex != "":
		// PERF: consider caching compiled regexes if this becomes a hot path
		re, err := regexp.Compile(rule.PathRegex)
		if err != nil {
			return false
		}
		return re.MatchString(req.URL.Path)
	default:
		return false
	}
}

func methodMatches(req *http.Request, rule config.Rule) bool {
	if len(rule.Methods) == 0 {
		return true
	}
	for _, m := range rule.Methods {
		if strings.EqualFold(req.Method, m) {
			return true
		}
	}
	return false
}

func headersMatch(req *http.Request, rule config.Rule) bool {
	if len(rule.Headers) == 0 {
		return true
	}
	for name, want := range rule.Headers {
		if req.Header.Get(name) != want {
			return false
		}
	}
	return true
}
