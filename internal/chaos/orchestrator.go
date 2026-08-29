package chaos

import (
	"net/http"

	"cha0s-sim/internal/config"
	"cha0s-sim/internal/matcher"
)

type FireDecider func(rule config.Rule) bool

// FiredRule pairs a rule that actually fired (matched and passed the
// probability gate) with the injectors built from it. It gives callers enough
// context to attribute chaos effects back to the rule that caused them.
type FiredRule struct {
	Rule       config.Rule
	ReqInject  []RequestInjector
	RespInject []ResponseInjector
}

// ResolveFired is the attribution-aware counterpart of Resolve. It returns the
// firing rules plus their injectors instead of flattening just the injectors.
func ResolveFired(req *http.Request, rules []config.Rule, decide FireDecider) []FiredRule {
	matched := matcher.Match(req, rules)

	var fired []FiredRule
	for _, rule := range matched {
		if !decide(rule) {
			continue
		}
		rReqs, rResps := BuildInjectors(rule)
		fired = append(fired, FiredRule{Rule: rule, ReqInject: rReqs, RespInject: rResps})
	}

	return fired
}

func ResolveFiredDefault(req *http.Request, rules []config.Rule) []FiredRule {
	return ResolveFired(req, rules, matcher.ShouldFireDefault)
}

func Resolve(req *http.Request, rules []config.Rule, decide FireDecider) (reqInjectors []RequestInjector, respInjectors []ResponseInjector) {
	for _, fr := range ResolveFired(req, rules, decide) {
		reqInjectors = append(reqInjectors, fr.ReqInject...)
		respInjectors = append(respInjectors, fr.RespInject...)
	}
	return reqInjectors, respInjectors
}

func ResolveDefault(req *http.Request, rules []config.Rule) (reqInjectors []RequestInjector, respInjectors []ResponseInjector) {
	return Resolve(req, rules, matcher.ShouldFireDefault)
}
