package proxy

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"

	"cha0s-sim/internal/config"
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

func NewServerInstance(port int, target *url.URL, preserveHost bool, insecureSkipVerify bool, verbose bool, store *config.Store) *http.Server {
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
	return &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: logger.Middleware(verbose, handler),
	}
}

func Serve(port int, target *url.URL, preserveHost bool, insecureSkipVerify bool, verbose bool, store *config.Store) error {
	return NewServerInstance(port, target, preserveHost, insecureSkipVerify, verbose, store).ListenAndServe()
}
