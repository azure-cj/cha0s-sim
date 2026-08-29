package proxy

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"cha0s-sim/internal/config"
	"cha0s-sim/internal/platform"
)

// EventSink consumes platform.Reportable items (traffic events, security
// findings, and any future reportable). The headless CLI passes no sink (nil —
// nothing is emitted and behavior is unchanged); the desktop app provides a
// sink that forwards events to the frontend.
type EventSink interface {
	Emit(evt platform.Reportable)
}

// TrafficEvent is a single normalized, post-hoc record of a request that went
// through the proxy. It combines request identity, measured latency/status,
// and the chaos effects that actually fired for that request.
type TrafficEvent struct {
	Ts         time.Time     `json:"ts"`          // when the request started
	Method     string        `json:"method"`      // e.g. GET
	Path       string        `json:"path"`        // request path (no query string)
	Host       string        `json:"host"`        // Host header as received
	Status     int           `json:"status"`      // 0 when the connection was dropped before any response
	DurationMs int64         `json:"duration_ms"` // wall-clock time from start to response/drop
	Effects    []ChaosEffect `json:"effects"`     // empty when the request passed through cleanly
}

// TrafficEvent implements platform.Reportable so live traffic flows through
// the same aggregated platform event log as security findings.
func (e TrafficEvent) Category() string { return "traffic" }

func (e TrafficEvent) Summary() string {
	return fmt.Sprintf("%s %s -> %d (%dms)", e.Method, e.Path, e.Status, e.DurationMs)
}

func (e TrafficEvent) Severity() string {
	switch {
	case e.Status == 0 || e.Status >= 500:
		return "critical"
	case e.Status >= 400:
		return "warning"
	default:
		return "info"
	}
}

// compile-time check that TrafficEvent satisfies platform.Reportable.
var _ platform.Reportable = TrafficEvent{}

// ChaosEffect attributes a single chaos action to the rule that caused it.
type ChaosEffect struct {
	Kind   string `json:"kind"`   // latency | override | drop | mangle
	Detail string `json:"detail"` // human-readable specifics, e.g. "+250ms", "=> 503"
	Rule   string `json:"rule"`   // name of the rule that fired
}

// withEventSink wraps the full server handler so that a live trace of every
// request can be emitted to the frontend. It is a no-op (returns next
// unchanged) when sink is nil, which keeps the headless CLI path identical.
func withEventSink(sink EventSink, next http.Handler) http.Handler {
	if sink == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Transparent WebSocket upgrades bypass the proxy chaos layer and
		// never produce a normal response; skip them rather than emit a
		// misleading event.
		if IsWebSocketUpgrade(req) {
			next.ServeHTTP(w, req)
			return
		}

		started := time.Now()
		rec := &eventStatusRecorder{ResponseWriter: w}
		effects := &eventEffects{}
		ctx := context.WithValue(req.Context(), eventEffectsKey, effects)
		next.ServeHTTP(rec, req.WithContext(ctx))

		sink.Emit(TrafficEvent{
			Ts:         started,
			Method:     req.Method,
			Path:       req.URL.Path,
			Host:       req.Host,
			Status:     rec.status,
			DurationMs: time.Since(started).Milliseconds(),
			Effects:    effects.effects,
		})
	})
}

// eventStatusRecorder captures the final status code written downstream while
// still passing through Hijack so that connection-drop injectors keep working.
// status starts at 0, which is also the sentinel for "no response was ever
// written" (i.e. the connection was hijacked and dropped).
type eventStatusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *eventStatusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *eventStatusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

func (r *eventStatusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("underlying response writer does not support hijacking")
	}
	return h.Hijack()
}

// effectsForRule maps a fired rule to its chaos effects. The gating conditions
// mirror chaos.BuildInjectors so the reported effects match what was actually
// built and run.
func effectsForRule(rule config.Rule) []ChaosEffect {
	var out []ChaosEffect

	if rule.DropConnection {
		out = append(out, ChaosEffect{Kind: "drop", Detail: "connection dropped", Rule: rule.Name})
	}

	if rule.Mangle != nil && rule.Mangle.Enabled {
		detail := rule.Mangle.Strategy
		if len(rule.Mangle.TargetKeys) > 0 {
			detail += " (keys: " + strings.Join(rule.Mangle.TargetKeys, ", ") + ")"
		}
		out = append(out, ChaosEffect{Kind: "mangle", Detail: detail, Rule: rule.Name})
	}

	if rule.StatusOverride != nil {
		detail := fmt.Sprintf("=> %d", rule.StatusOverride.Code)
		if rule.StatusOverride.StripBody {
			detail += " (body stripped)"
		}
		out = append(out, ChaosEffect{Kind: "override", Detail: detail, Rule: rule.Name})
	}

	if rule.Latency != nil {
		l := rule.Latency
		detail := ""
		if l.FixedMs > 0 {
			detail = fmt.Sprintf("+%dms", l.FixedMs)
		}
		if l.JitterMinMs > 0 || l.JitterMaxMs > 0 {
			if detail != "" {
				detail += " "
			}
			detail += fmt.Sprintf("+jitter %d..%dms", l.JitterMinMs, l.JitterMaxMs)
		}
		if detail == "" {
			detail = "latency"
		}
		out = append(out, ChaosEffect{Kind: "latency", Detail: detail, Rule: rule.Name})
	}

	return out
}
