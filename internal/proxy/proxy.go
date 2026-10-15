package proxy

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"

	"cha0s-sim/internal/config"
	"cha0s-sim/internal/discovery"
	"cha0s-sim/internal/logger"
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

// PipelineMode selects how a server instance is invoked. It historically
// distinguished chaos-only, security-only, and full pipelines; with the passive
// security scanners removed, every mode now runs the chaos pipeline only and
// the value no longer changes behavior.
type PipelineMode int

const (
	// PipelineFull is used by the CLI's plain proxy command.
	PipelineFull PipelineMode = iota
	// PipelineChaosOnly is used by the desktop "chaos" session.
	PipelineChaosOnly
)

// NewServerInstance builds an *http.Server serving the chaos proxy. mode selects
// which pipeline stages run; no scanners remain, so every mode runs the chaos
// middleware + response injectors. sessionName tags every emitted traffic event
// with its originating session ("chaos"); pass "" for non-session contexts (the
// CLI's plain proxy passes a nil registry, so it never records). The registry
// parameter is the passive endpoint observer: pass an existing
// *discovery.Registry to have every request recorded against sessionName at the
// same point TrafficEvent is constructed (the desktop app passes its App-level
// registry); pass nil to disable observation entirely. The final variadic sink
// argument is optional: when provided, platform events (traffic) are emitted per
// request (the desktop app uses this to feed the live traffic view). The
// headless CLI omits it entirely and behaves exactly as before.
func NewServerInstance(port int, target *url.URL, preserveHost bool, insecureSkipVerify bool, verbose bool, store *config.Store, mode PipelineMode, sessionName string, registry *discovery.Registry, sinks ...EventSink) *http.Server {
	_ = mode // retained for call-site compatibility; no longer affects behavior

	proxy := New(target, preserveHost, insecureSkipVerify, store)

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
		serverHandler = withEventSink(sessionName, sinks[0], registry, serverHandler)
	}

	return &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: serverHandler,
	}
}

func Serve(port int, target *url.URL, preserveHost bool, insecureSkipVerify bool, verbose bool, store *config.Store) error {
	return NewServerInstance(port, target, preserveHost, insecureSkipVerify, verbose, store, PipelineFull, "", nil).ListenAndServe()
}
