package logger

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/fatih/color"
)

type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("webserver doesn't support hijacking")
	}
	return hijacker.Hijack()
}

func Middleware(verbose bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !verbose {
			next.ServeHTTP(w, req)
			return
		}

		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(recorder, req)
		elapsed := time.Since(start).Round(time.Millisecond)

		var statusColor *color.Color
		switch {
		case recorder.statusCode >= 200 && recorder.statusCode <= 299:
			statusColor = color.New(color.FgGreen)
		case recorder.statusCode >= 300 && recorder.statusCode <= 399:
			statusColor = color.New(color.FgCyan)
		case recorder.statusCode >= 400 && recorder.statusCode <= 499:
			statusColor = color.New(color.FgYellow)
		case recorder.statusCode >= 500 && recorder.statusCode <= 599:
			statusColor = color.New(color.FgRed)
		default:
			statusColor = color.New(color.FgWhite)
		}

		statusColor.Printf("%s %s -> %d (%s)\n", req.Method, req.URL.Path, recorder.statusCode, elapsed)

		// Headers only on top of verbose; the same flag now gates the access
		// line itself so a quiet CLI stays quiet (security findings are the
		// CLI's always-on output, printed by the event sink, not here).
		headerList := ""
		first := true
		for name := range req.Header {
			if !first {
				headerList += ", "
			}
			headerList += name
			first = false
		}
		fmt.Printf("  headers: %s | content-length: %d\n", headerList, req.ContentLength)
	})
}
