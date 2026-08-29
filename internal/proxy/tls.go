package proxy

import (
	"crypto/tls"
	"net/http"
)

func NewTransport(insecureSkipVerify bool) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: insecureSkipVerify}
	return transport
}
