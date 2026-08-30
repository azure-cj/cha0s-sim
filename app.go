package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
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
	defaultProxyPort  = 8080
	defaultConfigPath = "chaos.yaml"
)

// App struct
type App struct {
	ctx        context.Context
	proxySrv   *http.Server
	store      *config.Store
	running    bool
	targetURL  string
	proxyPort  int
	configPath string

	stressEngine   *stress.Engine
	stressRunning  bool
	stressCancel   context.CancelFunc // non-nil only while a stress test is running
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

	a.targetURL = defaultTargetURL
	a.proxyPort = defaultProxyPort
	a.configPath = defaultConfigPath

	store, err := config.NewStore(a.configPath)
	if err != nil {
		fmt.Printf("app: failed to load config %q (leaving store nil): %v\n", a.configPath, err)
		return
	}
	a.store = store
}

// shutdown is called when the app is shutting down. It gracefully stops
// the proxy (if running) and closes the config store (if loaded).
func (a *App) shutdown(ctx context.Context) {
	if a.proxySrv != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.proxySrv.Shutdown(shutdownCtx); err != nil {
			fmt.Printf("app: proxy shutdown error: %v\n", err)
		}
		a.proxySrv = nil
	}

	if a.store != nil {
		if err := a.store.Close(); err != nil {
			fmt.Printf("app: config store close error: %v\n", err)
		}
		a.store = nil
	}
}

// SettingsView is the frontend contract for the configurable proxy settings.
type SettingsView struct {
	TargetURL  string `json:"targetURL"`
	ProxyPort  int    `json:"proxyPort"`
	ConfigPath string `json:"configPath"`
	FirstRun   bool   `json:"firstRun"` // true if no valid config store is loaded
}

// GetSettings returns the currently configured proxy settings.
func (a *App) GetSettings() SettingsView {
	return SettingsView{
		TargetURL:  a.targetURL,
		ProxyPort:  a.proxyPort,
		ConfigPath: a.configPath,
		FirstRun:   a.store == nil,
	}
}

// SaveSettings validates and applies new proxy settings. Returns a non-empty
// error message on failure, empty string on success (same contract as
// StartProxy/StopProxy). All validation happens before any state changes so a
// bad input never partially applies.
func (a *App) SaveSettings(targetURL string, proxyPort int, configPath string) string {
	if a.running {
		return "cannot change settings while proxy is running — stop it first"
	}

	if _, err := config.ValidateTargetURL(targetURL); err != nil {
		return fmt.Sprintf("invalid target URL: %v", err)
	}

	if proxyPort < 1 || proxyPort > 65535 {
		return "invalid port: must be between 1 and 65535"
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
	a.proxyPort = proxyPort
	a.configPath = configPath

	if store, err := config.NewStore(a.configPath); err == nil {
		a.store = store
	}

	return ""
}

// StartProxy starts the chaos proxy using the configured target/port/config.
// Returns an error message string (empty string means success) rather than
// a Go error type, since Wails' JS bindings handle string returns more
// predictably across all frontend frameworks for this kind of status reporting.
func (a *App) StartProxy() string {
	if a.running {
		return "proxy is already running"
	}

	target, err := url.Parse(a.targetURL)
	if err != nil {
		return fmt.Sprintf("failed to parse target URL %q: %v", a.targetURL, err)
	}

	srv := proxy.NewServerInstance(a.proxyPort, target, false, false, false, a.store, trafficEventSink{ctx: a.ctx})
	a.proxySrv = srv

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("app: proxy ListenAndServe error: %v\n", err)
		}
	}()

	a.running = true
	return ""
}

// StopProxy stops the running chaos proxy, if any.
func (a *App) StopProxy() string {
	if !a.running || a.proxySrv == nil {
		return "proxy is not running"
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.proxySrv.Shutdown(shutdownCtx); err != nil {
		return fmt.Sprintf("failed to stop proxy: %v", err)
	}

	a.running = false
	a.proxySrv = nil
	return ""
}

// GetStatus returns a simple status string for the frontend to display.
func (a *App) GetStatus() string {
	if a.running {
		return fmt.Sprintf("Running on :%d -> %s", a.proxyPort, a.targetURL)
	}
	return "Stopped"
}

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

	// Run under a cancellable context: StopStressTest fires stressCancel to
	// end the run early. engine.Run derives runCtx from this ctx via
	// WithTimeout(ctx, Duration), so canceling here short-circuits Duration.
	ctx, cancel := context.WithCancel(context.Background())
	a.stressCancel = cancel
	a.stressRunning = true

	go func() {
		final := engine.Run(ctx)
		a.stressRunning = false
		if a.stressCancel != nil {
			a.stressCancel = nil // guard: StopStressTest may have already raced nil
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
