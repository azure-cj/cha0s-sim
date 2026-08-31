package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"cha0s-sim/internal/config"
	"cha0s-sim/internal/platform"
	"cha0s-sim/internal/proxy"
	"cha0s-sim/internal/security"
	"cha0s-sim/internal/stress"
)

const (
	defaultTargetURL  = "http://localhost:3000"
	defaultConfigPath = "chaos.yaml"
)

// ---------------------------------------------------------------------------
// Multi-session infrastructure
// ---------------------------------------------------------------------------

type sessionName string

const (
	sessionChaos    sessionName = "chaos"
	sessionSecurity sessionName = "security"
)

// allSessions is the ordered list of every known session type. It drives
// validation, iteration, and status reporting.
var allSessions = []sessionName{sessionChaos, sessionSecurity}

// sessionPorts maps each session to its fixed, predictable listen port.
var sessionPorts = map[sessionName]int{
	sessionChaos:    8081,
	sessionSecurity: 8082,
}

// proxySession holds the per-session server and its running state.
type proxySession struct {
	srv     *http.Server
	running bool
}

// validateSessionName returns true if name matches a known session.
func validateSessionName(name string) bool {
	sn := sessionName(name)
	for _, s := range allSessions {
		if s == sn {
			return true
		}
	}
	return false
}

// modeForSession returns the pipeline mode each session should run under:
// chaos runs only chaos rule firing, security runs only scanning.
func modeForSession(sn sessionName) proxy.PipelineMode {
	switch sn {
	case sessionChaos:
		return proxy.PipelineChaosOnly
	case sessionSecurity:
		return proxy.PipelineSecurityOnly
	default:
		return proxy.PipelineFull
	}
}

// ---------------------------------------------------------------------------
// App struct
// ---------------------------------------------------------------------------

// App struct
type App struct {
	ctx        context.Context
	store      *config.Store
	sessions   map[sessionName]*proxySession
	targetURL  string
	configPath string

	stressEngine  *stress.Engine
	stressRunning bool
	stressCancel  context.CancelFunc
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// trafficEventSink forwards proxy traffic events and security findings to the
// frontend over Wails' event bus. It lives on an unexported type so its method
// is NOT exposed as a frontend-callable binding — the frontend only subscribes
// via EventsOn.
type trafficEventSink struct {
	ctx context.Context
}

func (s trafficEventSink) Emit(evt platform.Reportable) {
	switch v := evt.(type) {
	case proxy.TrafficEvent:
		runtime.EventsEmit(s.ctx, "traffic", v)
	case security.Finding:
		runtime.EventsEmit(s.ctx, "security_finding", v)
	case StressUpdate:
		runtime.EventsEmit(s.ctx, "stress_update", v)
	default:
		// Fallback for any future Reportable type: emit generically so nothing
		// is silently dropped.
		runtime.EventsEmit(s.ctx, "platform_event", evt)
	}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods. Mutable settings are initialized from
// the defaults before the config store is attempted.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.sessions = make(map[sessionName]*proxySession)

	a.targetURL = defaultTargetURL
	a.configPath = defaultConfigPath

	store, err := config.NewStore(a.configPath)
	if err != nil {
		fmt.Printf("app: failed to load config %q (leaving store nil): %v\n", a.configPath, err)
		return
	}
	a.store = store
}

// shutdown is called when the app is shutting down. It gracefully stops
// all running sessions and closes the config store (if loaded).
func (a *App) shutdown(ctx context.Context) {
	for name, ps := range a.sessions {
		if ps.running && ps.srv != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := ps.srv.Shutdown(shutdownCtx); err != nil {
				fmt.Printf("app: session %q shutdown error: %v\n", name, err)
			}
			ps.running = false
			ps.srv = nil
		}
	}

	if a.store != nil {
		if err := a.store.Close(); err != nil {
			fmt.Printf("app: config store close error: %v\n", err)
		}
		a.store = nil
	}
}

// ---------------------------------------------------------------------------
// Settings
// ---------------------------------------------------------------------------

// SettingsView is the frontend contract for the configurable proxy settings.
type SettingsView struct {
	TargetURL  string `json:"targetURL"`
	ConfigPath string `json:"configPath"`
	FirstRun   bool   `json:"firstRun"` // true if no valid config store is loaded
}

// GetSettings returns the currently configured proxy settings.
func (a *App) GetSettings() SettingsView {
	return SettingsView{
		TargetURL:  a.targetURL,
		ConfigPath: a.configPath,
		FirstRun:   a.store == nil,
	}
}

// SaveSettings validates and applies new proxy settings. Returns a non-empty
// error message on failure, empty string on success. All validation happens
// before any state changes so a bad input never partially applies.
func (a *App) SaveSettings(targetURL string, configPath string) string {
	if _, err := config.ValidateTargetURL(targetURL); err != nil {
		return fmt.Sprintf("invalid target URL: %v", err)
	}

	// A non-existent configPath is acceptable: the store stays nil (FirstRun
	// remains true), mirroring how startup handles a missing chaos.yaml.
	if a.store != nil {
		if err := a.store.Close(); err != nil {
			fmt.Printf("app: config store close error during SaveSettings: %v\n", err)
		}
		a.store = nil
	}

	a.targetURL = targetURL
	a.configPath = configPath

	if store, err := config.NewStore(a.configPath); err == nil {
		a.store = store
	}

	return ""
}

// ---------------------------------------------------------------------------
// Session management
// ---------------------------------------------------------------------------

// StartSession starts a named proxy session (e.g. "chaos", "security").
// Each session listens on its own fixed port and proxies to the shared target
// URL. Returns an error message string (empty string means success).
func (a *App) StartSession(name string) string {
	if !validateSessionName(name) {
		return fmt.Sprintf("unknown session %q — valid names: chaos, security", name)
	}

	sn := sessionName(name)
	if ps, ok := a.sessions[sn]; ok && ps.running {
		return fmt.Sprintf("session %q is already running", name)
	}

	target, err := url.Parse(a.targetURL)
	if err != nil {
		return fmt.Sprintf("failed to parse target URL %q: %v", a.targetURL, err)
	}

	port := sessionPorts[sn]
	mode := modeForSession(sn)
	srv := proxy.NewServerInstance(port, target, false, false, false, a.store, mode, trafficEventSink{ctx: a.ctx})

	ps := &proxySession{srv: srv, running: true}
	a.sessions[sn] = ps

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("app: session %q ListenAndServe error: %v\n", name, err)
		}
	}()

	return ""
}

// StopSession stops a running named session. Returns an error message string
// (empty string means success).
func (a *App) StopSession(name string) string {
	if !validateSessionName(name) {
		return fmt.Sprintf("unknown session %q — valid names: chaos, security", name)
	}

	sn := sessionName(name)
	ps, ok := a.sessions[sn]
	if !ok || !ps.running || ps.srv == nil {
		return fmt.Sprintf("session %q is not running", name)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ps.srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Sprintf("failed to stop session %q: %v", name, err)
	}

	ps.running = false
	ps.srv = nil
	return ""
}

// GetSessionStatus reports whether a named session is currently running.
// Returns false if the session name is unknown or the session is not running.
func (a *App) GetSessionStatus(name string) bool {
	if !validateSessionName(name) {
		return false
	}
	ps, ok := a.sessions[sessionName(name)]
	if !ok {
		return false
	}
	return ps.running
}

// GetAllSessionStatuses returns the running state of every known session in a
// single call. Keys are the session name strings ("chaos", "security").
func (a *App) GetAllSessionStatuses() map[string]bool {
	statuses := make(map[string]bool, len(allSessions))
	for _, name := range allSessions {
		ps, ok := a.sessions[name]
		statuses[string(name)] = ok && ps.running
	}
	return statuses
}

// ---------------------------------------------------------------------------
// Rules
// ---------------------------------------------------------------------------

// GetRuleCount returns how many chaos rules are currently loaded (0 if no config).
func (a *App) GetRuleCount() int {
	if a.store == nil {
		return 0
	}
	return len(a.store.Current().Rules)
}

// RuleView is the flat frontend contract for a chaos rule. Has* flags signal
// which chaos types a rule applies without exposing the nested config structs.
type RuleView struct {
	Name              string   `json:"name"`
	Path              string   `json:"path,omitempty"`
	PathRegex         string   `json:"pathRegex,omitempty"`
	Methods           []string `json:"methods,omitempty"`
	ErrorRate         float64  `json:"errorRate"`
	Enabled           bool     `json:"enabled"`
	HasLatency        bool     `json:"hasLatency"`
	HasStatusOverride bool     `json:"hasStatusOverride"`
	HasDropConnection bool     `json:"hasDropConnection"`
	HasMangle         bool     `json:"hasMangle"`
	HasFuzz           bool     `json:"hasFuzz"`
}

// GetRules returns the currently loaded chaos rules as flat RuleViews.
// Returns an empty (non-nil) slice when no config is loaded.
func (a *App) GetRules() []RuleView {
	if a.store == nil {
		return []RuleView{}
	}
	cfg := a.store.Current()
	if cfg == nil {
		return []RuleView{}
	}
	views := make([]RuleView, 0, len(cfg.Rules))
	for _, r := range cfg.Rules {
		views = append(views, RuleView{
			Name:              r.Name,
			Path:              r.Path,
			PathRegex:         r.PathRegex,
			Methods:           r.Methods,
			ErrorRate:         r.ErrorRate,
			Enabled:           r.Enabled,
			HasLatency:        r.Latency != nil,
			HasStatusOverride: r.StatusOverride != nil,
			HasDropConnection: r.DropConnection,
			HasMangle:         r.Mangle != nil,
			HasFuzz:           r.Fuzz != nil,
		})
	}
	return views
}

// ToggleRule flips a rule's Enabled state in the in-memory config and attempts
// to persist it to the config file.
//
// Return contract (single string, keeping the Wails binding shape simple):
//   - "" (empty) — full success: toggled AND persisted.
//   - A message prefixed with "warning:" — the toggle applied in-memory but could
//     not be persisted to disk (e.g. file deleted/locked). The frontend should
//     treat this as a soft warning, not a hard failure: the toggle affects live
//     traffic now but will be lost on hot-reload or restart.
//   - A plain error message (no prefix) — the toggle failed entirely (e.g. no
//     config loaded, or no rule with that name).
func (a *App) ToggleRule(name string, enabled bool) string {
	if a.store == nil {
		return "no config loaded — cannot toggle rules"
	}
	persisted, err := a.store.SetRuleEnabled(name, enabled)
	if err != nil {
		return err.Error()
	}
	if !persisted {
		return "warning: toggled in-memory but not persisted to disk — the change is live now but will be lost on hot-reload or restart"
	}
	return ""
}

// CreateRule builds a new chaos rule from the create-rule form and adds it to
// the live config, persisting it to the config file.
//
// Return contract (single string, same shape as ToggleRule):
//   - "" (empty) — full success: the rule is live AND persisted.
//   - A message prefixed with "warning:" — the rule is live in-memory but could
//     not be persisted to disk. It affects live traffic now but will be lost on
//     hot-reload or restart.
//   - A plain error message (no prefix) — the rule was NOT created (bad input,
//     invalid config file, duplicate name, etc.); nothing changed.
//
// First runs are handled here: if no config store is loaded yet, the app
// creates one from the configured path on demand — re-opening the existing file
// when present, or an empty store (no watcher) when the file does not exist yet.
func (a *App) CreateRule(req config.CreateRuleRequest) string {
	if a.store == nil {
		if _, err := os.Stat(a.configPath); err == nil {
			store, err := config.NewStore(a.configPath)
			if err != nil {
				return fmt.Sprintf("failed to load config file %q — fix or remove it before creating a rule: %v", a.configPath, err)
			}
			a.store = store
		} else {
			store, err := config.NewEmptyStore(a.configPath)
			if err != nil {
				return fmt.Sprintf("failed to prepare config file %q: %v", a.configPath, err)
			}
			a.store = store
		}
	}

	rule, err := config.BuildRuleFromRequest(req)
	if err != nil {
		return err.Error()
	}
	persisted, err := a.store.AddRule(rule)
	if err != nil {
		return err.Error()
	}
	if !persisted {
		return "warning: rule created in-memory but not persisted to disk — it is live now but will be lost on hot-reload or restart"
	}
	return ""
}

// ---------------------------------------------------------------------------
// Stress testing (unchanged)
// ---------------------------------------------------------------------------

// StressConfig is the frontend contract for configuring a load test. Kept
// deliberately smaller than the CLI's full flag set: step/spike timing details
// and the per-request timeout are not exposed in the UI yet and use hardcoded
// defaults in StartStressTest.
type StressConfig struct {
	TargetURL   string  `json:"targetURL"`
	TargetRPS   float64 `json:"targetRPS"`
	DurationSec int     `json:"durationSec"`
	Concurrency int     `json:"concurrency"`
	Shape       string  `json:"shape"` // "", "continuous", "stepped", "spike"
	EndRPS      float64 `json:"endRPS,omitempty"`
	SpikeRPS    float64 `json:"spikeRPS,omitempty"`
}

// StressUpdate is the live progress payload streamed to the frontend while a
// stress test runs. It implements platform.Reportable so it flows through the
// same EventSink as traffic events and security findings. Final distinguishes
// the last emission (the completed test's final snapshot) from the
// intermediate 500ms progress ticks.
type StressUpdate struct {
	stress.MetricsSnapshot
	Final bool `json:"final"`
}

func (s StressUpdate) Category() string { return "stress" }

func (s StressUpdate) Summary() string {
	return fmt.Sprintf("stress rps=%.1f errors=%d/%d p50=%dms final=%v", s.RPS, s.TotalErrors, s.TotalRequests, s.P50Ms, s.Final)
}

func (s StressUpdate) Severity() string {
	switch {
	case s.ErrorRate > 0.5:
		return "critical"
	case s.ErrorRate > 0.1:
		return "warning"
	default:
		return "info"
	}
}

var _ platform.Reportable = StressUpdate{}

// emitStressUpdate forwards a snapshot to the frontend via the shared
// trafficEventSink (the same single event-emission path used by proxy traffic
// and security findings).
func (a *App) emitStressUpdate(snap stress.MetricsSnapshot, final bool) {
	trafficEventSink{ctx: a.ctx}.Emit(StressUpdate{MetricsSnapshot: snap, Final: final})
}

// StartStressTest launches a load test asynchronously and returns immediately.
// Progress snapshots are streamed to the frontend every 500ms via the
// "stress_update" event; the run ends by emitting one final StressUpdate with
// Final=true. Returns a non-empty error message on failure, "" on success.
func (a *App) StartStressTest(cfg StressConfig) string {
	if a.stressRunning {
		return "a stress test is already running"
	}

	target, err := config.ValidateTargetURL(cfg.TargetURL)
	if err != nil {
		return fmt.Sprintf("invalid target URL: %v", err)
	}
	if cfg.DurationSec <= 0 {
		return "invalid duration: must be a positive number of seconds"
	}
	if cfg.Concurrency <= 0 {
		return "invalid concurrency: must be a positive number of workers"
	}
	if cfg.TargetRPS <= 0 {
		return "invalid target RPS: must be positive"
	}

	shapeFn, err := stress.BuildShape(cfg.Shape, cfg.TargetRPS, cfg.EndRPS, 5, 10*time.Second, cfg.SpikeRPS, 0, 5*time.Second)
	if err != nil {
		return err.Error()
	}

	engineCfg := stress.Config{
		TargetURL:      target.String(),
		TargetRPS:      cfg.TargetRPS,
		Duration:       time.Duration(cfg.DurationSec) * time.Second,
		Concurrency:    cfg.Concurrency,
		RequestTimeout: 5 * time.Second,
		Shape:          shapeFn,
		OnProgress: func(snap stress.MetricsSnapshot) {
			a.emitStressUpdate(snap, false)
		},
	}

	engine := stress.NewEngine(engineCfg, stress.NewClientPool(1000))
	a.stressEngine = engine

	ctx, cancel := context.WithCancel(context.Background())
	a.stressCancel = cancel
	a.stressRunning = true

	go func() {
		final := engine.Run(ctx)
		a.stressRunning = false
		if a.stressCancel != nil {
			a.stressCancel = nil
		}
		a.emitStressUpdate(final, true)
	}()

	return ""
}

// StopStressTest requests early termination of the running stress test. It is
// asynchronous: the stop request is issued here (immediate "" return), and the
// actual "stopped" state arrives via the next stress_update event with
// Final=true, exactly like a natural completion — engine.Run returns its final
// snapshot regardless of WHY the run stopped, so the frontend needs no special
// case for aborted runs. Returns a non-empty message if nothing is running.
func (a *App) StopStressTest() string {
	if !a.stressRunning || a.stressCancel == nil {
		return "no stress test is running"
	}
	a.stressCancel() // safe: invoking a CancelFunc twice is a no-op
	return ""
}

// GetStressStatus reports whether a stress test is currently running.
func (a *App) GetStressStatus() bool {
	return a.stressRunning
}
