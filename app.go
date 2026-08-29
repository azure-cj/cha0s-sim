package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"cha0s-sim/internal/config"
	"cha0s-sim/internal/proxy"
)

const (
	defaultTargetURL  = "http://localhost:3000"
	defaultProxyPort  = 8080
	defaultConfigPath = "chaos.yaml"
)

// App struct
type App struct {
	ctx      context.Context
	proxySrv *http.Server
	store    *config.Store
	running  bool
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// trafficEventSink forwards proxy traffic events to the frontend over Wails'
// event bus. It lives on an unexported type so its method is NOT exposed as a
// frontend-callable binding — the frontend only subscribes via EventsOn.
type trafficEventSink struct {
	ctx context.Context
}

func (s trafficEventSink) Emit(evt proxy.TrafficEvent) {
	runtime.EventsEmit(s.ctx, "traffic", evt)
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	store, err := config.NewStore(defaultConfigPath)
	if err != nil {
		fmt.Printf("app: failed to load config %q (leaving store nil): %v\n", defaultConfigPath, err)
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

// StartProxy starts the chaos proxy using the default target/port/config.
// Returns an error message string (empty string means success) rather than
// a Go error type, since Wails' JS bindings handle string returns more
// predictably across all frontend frameworks for this kind of status reporting.
func (a *App) StartProxy() string {
	if a.running {
		return "proxy is already running"
	}

	target, err := url.Parse(defaultTargetURL)
	if err != nil {
		return fmt.Sprintf("failed to parse target URL %q: %v", defaultTargetURL, err)
	}

	srv := proxy.NewServerInstance(defaultProxyPort, target, false, false, false, a.store, trafficEventSink{ctx: a.ctx})
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
		return fmt.Sprintf("Running on :%d -> %s", defaultProxyPort, defaultTargetURL)
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
