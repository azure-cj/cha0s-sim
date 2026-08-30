package config

import (
	"fmt"
	"net/url"
)

// ValidateTargetURL parses raw as a proxy target URL and checks it is an
// http:// or https:// absolute URI. It returns the parsed URL on success.
// Shared by the CLI (--target flag) and the desktop Settings flow so both
// produce identical validation behavior.
func ValidateTargetURL(raw string) (*url.URL, error) {
	u, err := url.ParseRequestURI(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("scheme must be http:// or https://, got: %s", u.Scheme)
	}
	return u, nil
}
