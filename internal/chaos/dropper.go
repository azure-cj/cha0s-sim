package chaos

import (
	"log"
	"net/http"
)

type DropperInjector struct{}

func NewDropperInjector() *DropperInjector {
	return &DropperInjector{}
}

func (di *DropperInjector) InjectRequest(w http.ResponseWriter, r *http.Request) bool {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		log.Printf("dropper: response writer does not support hijacking, cannot drop connection")
		return false
	}

	conn, _, err := hijacker.Hijack()
	if err != nil {
		log.Printf("dropper: failed to hijack connection: %v", err)
		return false
	}

	conn.Close()
	log.Printf("dropper: connection dropped for %s %s", r.Method, r.URL.Path)
	return true
}
