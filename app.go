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
