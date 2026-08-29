package proxy

import (
	"context"
	"net/http"

	"cha0s-sim/internal/chaos"
	"cha0s-sim/internal/config"
)

type ctxKey string

// respInjectorsKey carries the response injectors from the request phase to
// the proxy's ModifyResponse hook so response-side chaos (status override,
// mangling) can be applied after the backend responds.
const respInjectorsKey ctxKey = "chaos_resp_injectors"

// eventEffectsKey carries a shared holder into which the fired chaos effects
// for a request are recorded. withEventSink seeds the holder; this middleware
// fills it, so the event includes the effects even though it is emitted by an
// outer wrapper.
const eventEffectsKey ctxKey = "chaos_event_effects"

// eventEffects is a mutable holder shared between withEventSink (who allocates
// it via the request context) and this middleware (who populates it).
type eventEffects struct {
	effects []ChaosEffect
}

func ChaosMiddleware(store *config.Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if store == nil {
			next.ServeHTTP(w, req)
			return
		}

		rules := store.Current().Rules
		fired := chaos.ResolveFiredDefault(req, rules)

		reqInjectors, respInjectors := injectorsFromFired(fired)

		if effects, ok := req.Context().Value(eventEffectsKey).(*eventEffects); ok {
			for _, fr := range fired {
				effects.effects = append(effects.effects, effectsForRule(fr.Rule)...)
			}
		}

		for _, injector := range reqInjectors {
			if injector.InjectRequest(w, req) {
				return
			}
		}

		ctx := context.WithValue(req.Context(), respInjectorsKey, respInjectors)
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}

func injectorsFromFired(fired []chaos.FiredRule) (reqInjectors []chaos.RequestInjector, respInjectors []chaos.ResponseInjector) {
	for _, fr := range fired {
		reqInjectors = append(reqInjectors, fr.ReqInject...)
		respInjectors = append(respInjectors, fr.RespInject...)
	}
	return reqInjectors, respInjectors
}

func RunResponseInjectors(resp *http.Response) error {
	injectors, _ := resp.Request.Context().Value(respInjectorsKey).([]chaos.ResponseInjector)

	for _, injector := range injectors {
		if err := injector.InjectResponse(resp); err != nil {
			return err
		}
	}

	return nil
}
