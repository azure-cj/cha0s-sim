package proxy

import (
	"context"
	"net/http"

	"cha0s-sim/internal/chaos"
	"cha0s-sim/internal/config"
)

type ctxKey string

const respInjectorsKey ctxKey = "chaos_resp_injectors"

func ChaosMiddleware(store *config.Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if store == nil {
			next.ServeHTTP(w, req)
			return
		}

		rules := store.Current().Rules
		reqInjectors, respInjectors := chaos.ResolveDefault(req, rules)

		for _, injector := range reqInjectors {
			if injector.InjectRequest(w, req) {
				return
			}
		}

		ctx := context.WithValue(req.Context(), respInjectorsKey, respInjectors)
		next.ServeHTTP(w, req.WithContext(ctx))
	})
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
