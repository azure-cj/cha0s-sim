package chaos

import (
	"net/http"

	"cha0s-sim/internal/config"
)

type RequestInjector interface {
	// InjectRequest runs before the request is forwarded to the backend.
	// It may block (e.g. latency) or terminate the connection itself (e.g. dropper).
	// Returns true if it fully handled the response itself (e.g. dropped the connection)
	// and the caller must NOT continue forwarding to the backend.
	InjectRequest(w http.ResponseWriter, r *http.Request) (handled bool)
}

type ResponseInjector interface {
	// InjectResponse runs after the backend responds, before the response reaches the client.
	// Matches the httputil.ReverseProxy.ModifyResponse signature so it can be wired in directly.
	InjectResponse(resp *http.Response) error
}

func BuildInjectors(rule config.Rule) (reqInjectors []RequestInjector, respInjectors []ResponseInjector) {
	if rule.DropConnection {
		reqInjectors = append(reqInjectors, NewDropperInjector())
	}

	if rule.Mangle != nil && rule.Mangle.Enabled {
		respInjectors = append(respInjectors, NewManglerInjector(rule.Mangle))
	}

	if rule.StatusOverride != nil {
		respInjectors = append(respInjectors, NewStatusInjector(rule.StatusOverride))
	}

	if rule.Latency != nil {
		reqInjectors = append(reqInjectors, NewLatencyInjector(rule.Latency))
	}

	if rule.Fuzz != nil {
		reqInjectors = append(reqInjectors, NewFuzzInjector(rule.Fuzz))
	}

	return reqInjectors, respInjectors
}
