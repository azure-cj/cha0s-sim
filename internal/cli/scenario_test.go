package cli

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// captureStdout runs fn with os.Stdout redirected to a pipe and returns
// everything fn printed, restoring os.Stdout afterwards.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	defer func() { os.Stdout = old }()

	fn()
	_ = w.Close()
	return <-done
}

// runScenarioCLI drives the real CLI entry point (rootCmd.SetArgs +
// rootCmd.Execute) for the given args and returns everything the command
// printed to stdout together with the cobra error. Routing through the root is
// the only correct way to exercise a leaf command in-process: cobra's bare
// Execute() derives args from os.Args[1:], which inside a test binary is
// cobra test noise and never reaches the real command wiring.
func runScenarioCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var err error
	out := captureStdout(t, func() {
		rootCmd.SetArgs(args)
		err = rootCmd.Execute()
	})
	return out, err
}

func TestScenarioCommandRegisteredUnderStress(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"stress", "scenario"})
	if err != nil {
		t.Fatalf("find stress scenario: %v", err)
	}
	for _, flag := range []string{"file", "target", "vus", "duration", "timeout"} {
		if cmd.Flags().Lookup(flag) == nil {
			t.Errorf("scenario command missing %q flag", flag)
		}
	}
	if vus, _ := cmd.Flags().GetInt("vus"); vus != 5 {
		t.Errorf("default --vus = %d, want 5", vus)
	}
	if d, _ := cmd.Flags().GetDuration("duration"); d != 30*time.Second {
		t.Errorf("default --duration = %v, want 30s", d)
	}
	if d, _ := cmd.Flags().GetDuration("timeout"); d != 5*time.Second {
		t.Errorf("default --timeout = %v, want 5s", d)
	}
}

// TestScenarioCommandRunsEndToEnd starts a believable two-step backend (login
// that returns a token, plus a protected profile that echoes back the
// Authorization header we sent it) and drives the real scenario CLI against
// it. The handler records exactly what token reached it, which proves variable
// extraction -> substitution -> wire end to end through the CLI, not just
// inside the engine package.
func TestScenarioCommandRunsEndToEnd(t *testing.T) {
	var (
		mu     sync.Mutex
		tokens []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/login":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"token":"abc123"}`)
		case "/api/profile":
			mu.Lock()
			tokens = append(tokens, r.Header.Get("Authorization"))
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"email":"t@chaos.dev"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	scenarioYAML := `name: "login flow"
steps:
  - name: "login"
    method: POST
    path: "/api/login"
    body: '{"username":"t","password":"t"}'
    extract:
      authToken: "token"
    think_time: 10ms
  - name: "get profile"
    method: GET
    path: "/api/profile"
    headers:
      Authorization: "Bearer {{authToken}}"
    think_time: 10ms
`
	file := filepath.Join(t.TempDir(), "scenario.yaml")
	if err := os.WriteFile(file, []byte(scenarioYAML), 0o644); err != nil {
		t.Fatalf("write scenario: %v", err)
	}

	out, err := runScenarioCLI(t,
		"stress", "scenario",
		"--file", file,
		"--target", srv.URL,
		"--vus", "2",
		"--duration", "400ms",
		"--timeout", "2s",
	)
	if err != nil {
		t.Fatalf("scenario command: %v", err)
	}

	for _, want := range []string{
		`Starting scenario: name="login flow"`,
		"Total iterations:",
		"Extraction failures:",
		"Per-step results:",
		"Step: login",
		"Step: get profile",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	// Every protected call must have carried the token we extracted at login —
	// the whole point of the feature, proven from the CLI down.
	mu.Lock()
	defer mu.Unlock()
	if len(tokens) == 0 {
		t.Fatal("server saw no /api/profile requests; the scenario never reached step 2")
	}
	for i, tok := range tokens {
		if tok != "Bearer abc123" {
			t.Errorf("profile request %d Authorization = %q, want %q", i, tok, "Bearer abc123")
		}
	}
}

func TestScenarioCommandInvalidTargetAndLoadErrors(t *testing.T) {
	// An invalid --target must fail before the scenario file is ever read.
	_, err := runScenarioCLI(t,
		"stress", "scenario",
		"--file", filepath.Join(t.TempDir(), "x.yaml"),
		"--target", "ftp://localhost:3000",
		"--vus", "1", "--duration", "10ms",
	)
	if err == nil || !strings.Contains(err.Error(), "invalid --target") {
		t.Errorf("invalid target err = %v, want invalid --target wrap", err)
	}

	// A valid target with a missing file surfaces the wrapped load error.
	_, err = runScenarioCLI(t,
		"stress", "scenario",
		"--file", filepath.Join(t.TempDir(), "nope.yaml"),
		"--target", "http://localhost:3000",
		"--vus", "1", "--duration", "10ms",
	)
	if err == nil || !strings.Contains(err.Error(), "failed to read scenario file") {
		t.Errorf("missing file err = %v, want failed-to-read wrap", err)
	}
}
