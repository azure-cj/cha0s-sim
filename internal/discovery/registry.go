// Package discovery provides a passive, in-memory registry of the unique
// (method, path) pairs observed flowing through each proxy session. Its sole
// purpose is to feed the Rule Wizard's "pick from what you've actually seen"
// list instead of forcing users to hand-type a path they hope the backend has.
//
// It is intentionally tiny and dependency-free (sync + time): a pure data
// structure with no knowledge of the network stack, config, or UI. Observation
// happens live in RAM. The caveat that the registry resets whenever the app
// restarts or a session stops is an honest v1 limitation — persisting observed
// endpoints across restarts is a deliberate future enhancement, not an
// oversight.
package discovery

import (
	"sync"
	"time"
)

// Endpoint is a single observed (method, path) pair attributed to the session
// that produced it. Path is the display form: purely numeric segments have
// already been collapsed to the "{id}" placeholder, so /api/users/42 and
// /api/users/7 both surface as /api/users/{id}.
type Endpoint struct {
	Method      string    `json:"method"`
	Path        string    `json:"path"`
	SeenCount   int       `json:"seenCount"`
	LastSeen    time.Time `json:"lastSeen"`
	SessionName string    `json:"sessionName"`
}

// Registry is a concurrency-safe accumulator of observed endpoints, keyed by
// session + method + normalized path.
type Registry struct {
	mu        sync.Mutex
	endpoints map[string]*Endpoint
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{endpoints: make(map[string]*Endpoint)}
}

// Record observes a (method, path) pair flowing through the named session.
// Thread-safe. An existing entry has its SeenCount incremented and LastSeen
// refreshed; a new entry is created with SeenCount 1. The path is normalized
// before it reaches both the registry key and the stored display value.
func (r *Registry) Record(sessionName, method, path string) {
	norm := normalizePath(path)

	r.mu.Lock()
	defer r.mu.Unlock()

	key := sessionName + "|" + method + "|" + norm
	if e, ok := r.endpoints[key]; ok {
		e.SeenCount++
		e.LastSeen = time.Now()
		return
	}
	r.endpoints[key] = &Endpoint{
		Method:      method,
		Path:        norm,
		SeenCount:   1,
		LastSeen:    time.Now(),
		SessionName: sessionName,
	}
}

// List returns a snapshot of every endpoint recorded for the given session,
// sorted by SeenCount descending — the most-frequently-seen paths first, since
// those are the most likely to be real endpoints worth building a rule against
// rather than one-off requests. Returns an empty non-nil slice when the session
// has observed nothing.
func (r *Registry) List(sessionName string) []Endpoint {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]Endpoint, 0, len(r.endpoints))
	for _, e := range r.endpoints {
		if e.SessionName == sessionName {
			out = append(out, *e)
		}
	}

	// Insertion sort, descending by SeenCount. Hand-rolled rather than
	// importing the sort package to keep the registry dependency-free; the set
	// of distinct observed endpoints is tiny in practice.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].SeenCount > out[j-1].SeenCount; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// Clear removes every endpoint recorded for the given session. Called when a
// session stops so stale endpoints from a previous run don't linger
// indefinitely. Persisting observations across app restarts is a deliberate
// future enhancement, not an oversight — this registry is in-memory only.
func (r *Registry) Clear(sessionName string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for key, e := range r.endpoints {
		if e.SessionName == sessionName {
			delete(r.endpoints, key)
		}
	}
}

// normalizePath collapses purely numeric path segments (e.g. "1", "42", "007")
// to the "{id}" placeholder, so /api/users/1 and /api/users/2 register as the
// same logical endpoint /api/users/{id}. This is a deliberately simple, high
// value heuristic for the most common REST case (numeric IDs); UUIDs, slugs,
// and other parameter forms are NOT normalized in this version.
//
// Implemented inline (no strings/sort imports) to keep the registry exactly
// sync + time: path segmentation is trivial and doesn't justify another import
// for a pure data structure.
func normalizePath(path string) string {
	segs := splitPath(path)
	for i, seg := range segs {
		if isAllDigits(seg) {
			segs[i] = "{id}"
		}
	}
	if len(segs) == 0 {
		return "/"
	}
	out := make([]byte, 0, len(path)+len(segs))
	for _, seg := range segs {
		out = append(out, '/')
		out = append(out, seg...)
	}
	return string(out)
}

// splitPath returns the non-empty "/"-separated segments of path, collapsing
// leading, trailing, and repeated separators.
func splitPath(path string) []string {
	var segs []string
	i := 0
	for i < len(path) {
		for i < len(path) && path[i] == '/' {
			i++
		}
		start := i
		for i < len(path) && path[i] != '/' {
			i++
		}
		if start < i {
			segs = append(segs, path[start:i])
		}
	}
	return segs
}

// isAllDigits reports whether s is non-empty and every byte is 0-9.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
