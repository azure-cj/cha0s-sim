package admin

import (
	"fmt"
	"log"
	"net/http"

	"cha0s-sim/internal/config"
)

type Server struct {
	Port  int
	Store *config.Store

	httpServer *http.Server
}

const dashboardHTML = `<!DOCTYPE html>
<html>
<head><title>cha0s;sim Admin Dashboard</title></head>
<body>
<h1>cha0s;sim Admin Dashboard</h1>
<p>Status: Running</p>
</body>
</html>
`

func NewServer(port int, store *config.Store) *Server {
	return &Server{Port: port, Store: store}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, dashboardHTML)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "OK")
	})

	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.Port),
		Handler: mux,
	}

	log.Printf("admin server listening on %s", s.httpServer.Addr)
	return s.httpServer.ListenAndServe()
}

// Shutdown closes the admin server. It uses Close() rather than the
// context-based graceful Shutdown for simplicity in this phase; a graceful
// drain-based shutdown is a possible future improvement.
func (s *Server) Shutdown() error {
	if s.httpServer == nil {
		return nil
	}
	return s.httpServer.Close()
}
