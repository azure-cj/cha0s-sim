package chaos

import (
	"net/http"

	"cha0s-sim/internal/config"
	"cha0s-sim/internal/matcher"
)

type FireDecider func(rule config.Rule) bool

func Resolve(req *http.Request, rules []config.Rule, decide FireDecider) (reqInjectors []RequestInjector, respInjectors []ResponseInjector) {
	matched := matcher.Match(req, rules)

	for _, rule := range matched {
		if !decide(rule) {
			continue
		}
		rReqs, rResps := BuildInjectors(rule)
		reqInjectors = append(reqInjectors, rReqs...)
		respInjectors = append(respInjectors, rResps...)
	}

	return reqInjectors, respInjectors
}

func ResolveDefault(req *http.Request, rules []config.Rule) (reqInjectors []RequestInjector, respInjectors []ResponseInjector) {
	return Resolve(req, rules, matcher.ShouldFireDefault)
}
