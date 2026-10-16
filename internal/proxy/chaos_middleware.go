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

		effects, tracked := req.Context().Value(eventEffectsKey).(*eventEffects)

		for _, injector := range reqInjectors {
			if injector.InjectRequest(w, req) {
				// The injector fully handled the response (e.g. dropped the
				// connection). Record the fired effects for the event, then
				// stop — the backend must not be reached.
				if tracked {
					recordEffects(effects, fired)
				}
				return
			}
		}

		if tracked {
			recordEffects(effects, fired)
		}

		ctx := context.WithValue(req.Context(), respInjectorsKey, respInjectors)
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}

// recordEffects appends every fired rule's effects to the shared holder. It runs
// after the request injectors so a latency effect can report the delay actually
// applied and a fuzz effect only appears when the body was really mutated (both
// are known only once the injectors have run).
func recordEffects(effects *eventEffects, fired []chaos.FiredRule) {
	for _, fr := range fired {
		effects.effects = append(effects.effects, effectsForRule(fr.Rule, appliedStateFor(fr))...)
	}
}

// appliedState is the per-request outcome of a rule's request-side injectors,
// used to report what actually happened rather than what was merely configured.
type appliedState struct {
	latencyMs   int64
	fuzzApplied bool
	fuzzFields  int
}

// appliedStateFor reads the state the fired rule's request injectors recorded
// while running. An injector that never ran (e.g. one that followed a drop)
// contributes its zero value.
func appliedStateFor(fr chaos.FiredRule) appliedState {
	var st appliedState
	for _, injector := range fr.ReqInject {
		switch inj := injector.(type) {
		case *chaos.LatencyInjector:
			st.latencyMs = inj.AppliedMs
		case *chaos.FuzzInjector:
			st.fuzzApplied = inj.Applied
			st.fuzzFields = inj.FieldsMutated
		}
	}
	return st
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
