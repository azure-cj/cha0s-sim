package stress

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ScenarioConfig configures one scenario load run.
type ScenarioConfig struct {
	BaseURL        string
	Scenario       Scenario
	VirtualUsers   int           // concurrent virtual users, each looping the whole scenario
	Duration       time.Duration // how long to keep looping virtual users through the scenario
	RequestTimeout time.Duration // per-request context timeout
	// OnProgress, when non-nil, receives a live in-progress ScenarioSnapshot on
	// a 500ms ticker while Run executes. nil disables progress reporting.
	OnProgress func(ScenarioSnapshot)
}

// ScenarioSnapshot is a point-in-time read of a scenario run: per-step metrics
// (each reusing MetricsSnapshot), plus the run-wide counters that don't belong
// to any single step.
type ScenarioSnapshot struct {
	StepSnapshots      map[string]MetricsSnapshot // step name -> that step's own latency/error breakdown
	TotalIterations    int64                      // full scenario loops completed across all virtual users
	ExtractionFailures int64                      // ExtractJSON attempts that could not produce a value
}

// ScenarioEngine runs a Scenario across a fixed pool of concurrent virtual
// users. It deliberately does NOT use a rate limiter, unlike the flat/shaped
// Engine: virtual users pace themselves via ThinkTime plus real request
// latency, which is how k6/Locust-style virtual-user models generate load
// (load is a function of concurrent users and their natural pacing, not an
// explicit target RPS). That is an intentional, different load-generation
// philosophy from the flat-rate engine, not an omission.
type ScenarioEngine struct {
	cfg          ScenarioConfig
	client       *http.Client
	stepMets     map[string]*Metrics // step name -> per-step Metrics; duplicate step names share a Metrics
	iterations   atomic.Int64
	extractFails atomic.Int64
}

// NewScenarioEngine builds a ScenarioEngine, pre-creating one Metrics per
// distinct step name so a snapshot always contains every step even when a step
// recorded zero requests.
func NewScenarioEngine(cfg ScenarioConfig, client *http.Client) *ScenarioEngine {
	stepMets := make(map[string]*Metrics, len(cfg.Scenario.Steps))
	for _, s := range cfg.Scenario.Steps {
		if _, ok := stepMets[s.Name]; !ok {
			stepMets[s.Name] = NewMetrics()
		}
	}
	return &ScenarioEngine{cfg: cfg, client: client, stepMets: stepMets}
}

// Run loops every virtual user through the full scenario (all steps in order)
// until EITHER the caller's context is cancelled OR cfg.Duration elapses
// (whichever comes first), then returns a final snapshot built from each
// step's Metrics.
func (e *ScenarioEngine) Run(ctx context.Context) ScenarioSnapshot {
	runCtx, cancel := context.WithTimeout(ctx, e.cfg.Duration)
	defer cancel()

	if e.cfg.OnProgress != nil {
		progressCtx, stop := context.WithCancel(runCtx)
		done := make(chan struct{})
		go e.trackProgress(progressCtx, done)
		defer func() { stop(); <-done }()
	}

	var wg sync.WaitGroup
	for i := 0; i < e.cfg.VirtualUsers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.runUser(runCtx)
		}()
	}
	wg.Wait()
	return e.snapshot()
}

// runUser keeps driving one virtual user through whole scenario iterations
// until the run context ends. A scenario with no steps has nothing to run and
// returns immediately — otherwise the empty iteration would spin the loop hot
// until the duration expired.
func (e *ScenarioEngine) runUser(ctx context.Context) {
	if len(e.cfg.Scenario.Steps) == 0 {
		return
	}
	for {
		if !e.runIteration(ctx) {
			return
		}
	}
}

// runIteration drives one pass through every step in order, maintaining the
// virtual user's OWN variable map (extracted values are per-user; two users
// never see each other's tokens). It returns false if the context was
// cancelled mid-iteration — a partial iteration is not counted as completed.
func (e *ScenarioEngine) runIteration(ctx context.Context) bool {
	if ctx.Err() != nil {
		return false
	}
	vars := make(map[string]string)
	for i := range e.cfg.Scenario.Steps {
		step := &e.cfg.Scenario.Steps[i]
		e.runStep(ctx, step, vars)
		if step.ThinkTime > 0 {
			select {
			case <-time.After(step.ThinkTime):
			case <-ctx.Done():
				return false
			}
		}
		if ctx.Err() != nil {
			return false
		}
	}
	e.iterations.Add(1)
	return true
}

// runStep fires a single step's request after substituting variables into its
// Path/Body/Headers, records latency + outcome into the step's own Metrics,
// then attempts ExtractJSON against the response body. A failed or errored
// request is recorded as an error but does NOT abort the virtual user's
// iteration — the next step still runs, mirroring the principle that one bad
// step shouldn't necessarily kill a whole session.
func (e *ScenarioEngine) runStep(ctx context.Context, step *Step, vars map[string]string) {
	path := substituteVars(step.Path, vars)
	url := strings.TrimSuffix(e.cfg.BaseURL, "/") + "/" + strings.TrimPrefix(path, "/")
	body := substituteVars(step.Body, vars)

	start := time.Now()
	reqCtx := ctx
	var cancel context.CancelFunc
	if e.cfg.RequestTimeout > 0 {
		reqCtx, cancel = context.WithTimeout(ctx, e.cfg.RequestTimeout)
		defer cancel()
	}

	var category string
	var respBody []byte
	gotResponse := false

	req, err := http.NewRequestWithContext(reqCtx, step.Method, url, strings.NewReader(body))
	if err == nil {
		for k, v := range step.Headers {
			req.Header.Set(k, substituteVars(v, vars))
		}
		resp, err := e.client.Do(req)
		if err != nil {
			category = classifyHTTPError(err)
		} else {
			gotResponse = true
			if len(step.ExtractJSON) > 0 {
				respBody, _ = io.ReadAll(resp.Body)
			}
			_ = resp.Body.Close()
			if resp.StatusCode >= 500 {
				category = "status_" + strconv.Itoa(resp.StatusCode)
			}
			// Non-5xx statuses (2xx/3xx/4xx) stay success, matching the flat
			// engine's error boundary exactly.
		}
	} else {
		category = ErrOther
	}

	latencyMs := time.Since(start).Milliseconds()
	if latencyMs < 1 {
		latencyMs = 1 // histogram floor is 1ms; sub-ms responses clamp to 1
	}
	e.stepMets[step.Name].Record(latencyMs, category)

	if gotResponse {
		for name, key := range step.ExtractJSON {
			if v, ok := extractTopLevel(string(respBody), key); ok {
				vars[name] = v
			} else {
				e.extractFails.Add(1)
			}
		}
	}
}

// snapshot assembles the final read of every step's Metrics plus the run-wide
// counters. Safe to call from the progress ticker goroutine while workers are
// still recording — Metrics.Snapshot is mutex-guarded.
func (e *ScenarioEngine) snapshot() ScenarioSnapshot {
	out := ScenarioSnapshot{
		StepSnapshots:      make(map[string]MetricsSnapshot, len(e.stepMets)),
		TotalIterations:    e.iterations.Load(),
		ExtractionFailures: e.extractFails.Load(),
	}
	for name, m := range e.stepMets {
		out.StepSnapshots[name] = m.Snapshot()
	}
	return out
}

// trackProgress reports a live ScenarioSnapshot to ScenarioConfig.OnProgress
// on a 500ms ticker, mirroring Engine.trackProgress. It returns via done once
// ctx is cancelled so it can never leak.
func (e *ScenarioEngine) trackProgress(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			e.cfg.OnProgress(e.snapshot())
		case <-ctx.Done():
			return
		}
	}
}