package proxy

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"

	"cha0s-sim/internal/config"
	"cha0s-sim/internal/logger"
	"cha0s-sim/internal/security"
)

func New(target *url.URL, preserveHost bool, insecureSkipVerify bool, store *config.Store) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Director = NewDirector(target, preserveHost)
	proxy.Transport = NewTransport(insecureSkipVerify)
	proxy.ModifyResponse = RunResponseInjectors
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("proxy error: %v", err)
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, "Bad Gateway: upstream target unreachable")
	}
	return proxy
}

// NewServerInstance builds an *http.Server serving the chaos proxy. The final
// variadic sink argument is optional: when provided, platform events (traffic
// + security findings) are emitted per request (the desktop app uses this to
// feed the live traffic view). The headless CLI omits it entirely and behaves
// exactly as before.
//
// Scanners: the default security scanner set is built internally (option a) —
// a hardcoded NewDefaultHeaderValidator() — rather than threading a new
// parameter through every caller (root.go, app.go.StartProxy), since there is
// no config/UI toggle for scanners yet. Injectors mutate; scanners observe. A
// composite ModifyResponse runs response-side chaos injectors FIRST so
// scanners see the already-mutated response (e.g. a body stripped by an
// override injector is scanned as empty), matching what the frontend truly
// receives.
func NewServerInstance(port int, target *url.URL, preserveHost bool, insecureSkipVerify bool, verbose bool, store *config.Store, sinks ...EventSink) *http.Server {
	proxy := New(target, preserveHost, insecureSkipVerify, store)

	var sink EventSink
	if len(sinks) > 0 {
		sink = sinks[0]
	}

	scanners := []security.ResponseScanner{security.NewDefaultHeaderValidator()}
	proxy.ModifyResponse = func(resp *http.Response) error {
		if err := RunResponseInjectors(resp); err != nil {
			return err
		}
		RunScanners(resp, scanners, sink)
		return nil
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if IsWebSocketUpgrade(req) {
			if err := HandleUpgrade(w, req, target.Host); err != nil {
				log.Printf("websocket upgrade error: %v", err)
			}
			return
		}
		ChaosMiddleware(store, http.HandlerFunc(proxy.ServeHTTP)).ServeHTTP(w, req)
	})

	serverHandler := http.Handler(logger.Middleware(verbose, handler))
	if len(sinks) > 0 {
		serverHandler = withEventSink(sinks[0], serverHandler)
	}

	return &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: serverHandler,
	}
}

func Serve(port int, target *url.URL, preserveHost bool, insecureSkipVerify bool, verbose bool, store *config.Store) error {
	return NewServerInstance(port, target, preserveHost, insecureSkipVerify, verbose, store).ListenAndServe()
}
