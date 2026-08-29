package chaos

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDropperDropsConnectionWhenHijackingSucceeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		di := NewDropperInjector()
		if handled := di.InjectRequest(w, r); !handled {
			t.Errorf("InjectRequest returned false, want true")
		}
	}))
	defer srv.Close()

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("net.Dial failed: %v", err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))

	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n")); err != nil {
		t.Fatalf("failed to write request: %v", err)
	}

	buf := make([]byte, 1024)
	n, err := conn.Read(buf)

	if nerr, ok := err.(net.Error); ok && nerr.Timeout() {
		t.Fatal("read timed out; server did not drop the connection")
	}
	if err == nil {
		t.Errorf("expected a connection error from dropped conn, got response %q", string(buf[:n]))
	}
	if err == nil && strings.HasPrefix(string(buf[:n]), "HTTP/1.1 200") {
		t.Errorf("received successful HTTP response, want dropped connection")
	}
}

func TestDropperReturnsFalseWhenHijackingNotSupported(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "http://x/", nil)

	di := NewDropperInjector()
	handled := di.InjectRequest(rec, req)
	if handled {
		t.Errorf("InjectRequest returned true, want false when hijacking unsupported")
	}
}
