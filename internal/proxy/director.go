package proxy

import (
	"net/http"
	"net/url"
)

func NewDirector(target *url.URL, preserveHost bool) func(*http.Request) {
	return func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.URL.Path = joinPaths(target.Path, req.URL.Path)
		if !preserveHost {
			req.Host = target.Host
		}
	}
}

func joinPaths(base, path string) string {
	aslash := len(base) > 0 && base[len(base)-1] == '/'
	bslash := len(path) > 0 && path[0] == '/'
	switch {
	case aslash && bslash:
		return base + path[1:]
	case !aslash && !bslash:
		if base == "" {
			return "/" + path
		}
		return base + "/" + path
	}
	return base + path
}
