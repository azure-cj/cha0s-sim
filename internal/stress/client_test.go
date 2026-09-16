package stress

import (
	"net/http"
	"testing"
	"time"
)

func TestNewClientPoolSetsIdleConnsPerHost(t *testing.T) {
	client := NewClientPool(100)

	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("client.Transport is %T, want *http.Transport", client.Transport)
	}
	if transport.MaxIdleConnsPerHost != 100 {
		t.Errorf("MaxIdleConnsPerHost = %d, want 100 (the default of 2 silently throttles load-test concurrency)", transport.MaxIdleConnsPerHost)
	}
}

func TestNewClientPoolTuningSanity(t *testing.T) {
	client := NewClientPool(64)

	transport := client.Transport.(*http.Transport)

	if transport.MaxIdleConns != 1000 {
		t.Errorf("MaxIdleConns = %d, want 1000", transport.MaxIdleConns)
	}
	if transport.MaxConnsPerHost != 0 {
		t.Errorf("MaxConnsPerHost = %d, want 0 (unlimited; the worker pool owns concurrency)", transport.MaxConnsPerHost)
	}
	if transport.IdleConnTimeout != 90*time.Second {
		t.Errorf("IdleConnTimeout = %v, want 90s", transport.IdleConnTimeout)
	}
	if client.Timeout != 0 {
		t.Errorf("client.Timeout = %v, want 0 (per-request context timeouts, not a blanket client timeout)", client.Timeout)
	}
	if _, ok := client.Transport.(*http.Transport); !ok {
		t.Fatalf("transport is %T, want *http.Transport", client.Transport)
	}
}

func TestNewClientPoolPerHostLowerThanDefault(t *testing.T) {
	// The caller controls the value; a value below the transport default is the
	// caller's choice, but the plumbing (MaxIdleConnsPerHost) must not be left
	// at the library default of 2.
	client := NewClientPool(3)
	transport := client.Transport.(*http.Transport)
	if transport.MaxIdleConnsPerHost != 3 {
		t.Errorf("MaxIdleConnsPerHost = %d, want 3", transport.MaxIdleConnsPerHost)
	}
}
