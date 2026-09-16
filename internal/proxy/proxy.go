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

// PipelineMode selects which phase(s) of the chaos+security pipeline a server
// instance runs for a request.
type PipelineMode int

const (
	// PipelineFull runs chaos rule matching/firing AND security scanning,
	// exactly as the original proxy did. Used by the CLI's plain proxy command
	// and any caller that wants the entire pipeline.
	PipelineFull PipelineMode = iota
	// PipelineChaosOnly runs chaos rule matching/firing (chaos middleware +
	// response injectors) but performs NO security scanning.
	PipelineChaosOnly
	// PipelineSecurityOnly performs security scanning only. ChaosMiddleware is
	// skipped entirely, so traffic passes through to the backend unmodified
	// (chaos rules never fire); scanners observe the untouched response.
	PipelineSecurityOnly
)

// pipelineScanners returns the scanner set for the given mode. ChaosOnly and
// SecurityOnly are intentionally built identically (full scanner set for
// SecurityOnly, empty for ChaosOnly) so the response pipeline below can branch
// on a single selection.
func pipelineScanners(mode PipelineMode) []security.ResponseScanner {
	switch mode {
	case PipelineSecurityOnly:
		return []security.ResponseScanner{security.NewDefaultHeaderValidator(), security.NewSecretScanner()}
	case PipelineChaosOnly:
		return []security.ResponseScanner{}
	default: // PipelineFull
		return []security.ResponseScanner{security.NewDefaultHeaderValidator(), security.NewSecretScanner()}
	}
}

// NewServerInstance builds an *http.Server serving the chaos proxy. mode
// selects which pipeline stages run (see PipelineMode). sessionName tags every
// emitted traffic event and security finding with its originating session
// ("chaos"/"security"); pass "" for non-session contexts (the CLI's plain
// PipelineFull proxy has no session concept — and passes a nil registry, so it
// never records). The registry parameter is the passive endpoint observer:
// pass an existing *discovery.Registry to have every request recorded against
// sessionName at the same point TrafficEvent is constructed (the desktop app
// passes its App-level registry); pass nil to disable observation entirely.
// The final variadic sink argument is optional: when provided, platform events
// (traffic + security findings) are emitted per request (the desktop app uses
// this to feed the live traffic view). The headless CLI omits it entirely and
// behaves exactly as before.
//
// Scanners: the default security scanner set is built internally (option a) —
// a hardcoded NewDefaultHeaderValidator() — rather than threading a new
// parameter through every caller (root.go, app.go.StartProxy), since there is
// no config/UI toggle for scanners yet. Injectors mutate; scanners observe. A
// composite ModifyResponse runs response-side chaos injectors FIRST so
// scanners see the already-mutated response (e.g. a body stripped by an
// override injector is scanned as empty), matching what the frontend truly
// receives.
func NewServerInstance(port int, target *url.URL, preserveHost bool, insecureSkipVerify bool, verbose bool, store *config.Store, mode PipelineMode, sessionName string, registry *discovery.Registry, sinks ...EventSink) *http.Server {
	proxy := New(target, preserveHost, insecureSkipVerify, store)

	var sink EventSink
	if len(sinks) > 0 {
		sink = sinks[0]
	}

	scanners := pipelineScanners(mode)
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
		// PipelineSecurityOnly skips ChaosMiddleware entirely: traffic goes
		// straight through with no rule matching/firing. RunResponseInjectors
		// then finds no stashed injectors in context and safely no-ops (its
		// nil-slice range is a no-op), so the response reaches scanners
		// unmodified.
		if mode == PipelineSecurityOnly {
			proxy.ServeHTTP(w, req)
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
