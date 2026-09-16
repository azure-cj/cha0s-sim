package stress

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestLoadScenarioExampleFile(t *testing.T) {
	sc, err := LoadScenario(filepath.Join("..", "..", "configs", "scenario.example.yaml"))
	if err != nil {
		t.Fatalf("LoadScenario(example): %v", err)
	}
	if sc.Name != "login and browse" {
		t.Errorf("Name = %q, want %q", sc.Name, "login and browse")
	}
	if len(sc.Steps) != 2 {
		t.Fatalf("len(Steps) = %d, want 2", len(sc.Steps))
	}

	login := sc.Steps[0]
	if login.Name != "login" || login.Method != "POST" || login.Path != "/api/login" {
		t.Errorf("step 0 = %+v, want login POST /api/login", login)
	}
	if login.Body != `{"username":"test","password":"test"}` {
		t.Errorf("step 0 body = %q", login.Body)
	}
	if got := login.ExtractJSON["authToken"]; got != "token" {
		t.Errorf("step 0 ExtractJSON[authToken] = %q, want %q", got, "token")
	}
	if login.ThinkTime != 500*time.Millisecond {
		t.Errorf("step 0 ThinkTime = %v, want 500ms", login.ThinkTime)
	}

	profile := sc.Steps[1]
	if profile.Name != "get profile" || profile.Method != "GET" || profile.Path != "/api/profile" {
		t.Errorf("step 1 = %+v, want get profile GET /api/profile", profile)
	}
	if profile.Headers["Authorization"] != "Bearer {{authToken}}" {
		t.Errorf("step 1 Authorization header = %q, want %q", profile.Headers["Authorization"], "Bearer {{authToken}}")
	}
	if profile.ThinkTime != time.Second {
		t.Errorf("step 1 ThinkTime = %v, want 1s", profile.ThinkTime)
	}
}

func TestLoadScenarioYAMLRoundTrip(t *testing.T) {
	want := Scenario{
		Name: "roundtrip",
		Steps: []Step{
			{Name: "login", Method: "POST", Path: "/login", Body: `{"u":"t"}`,
				ExtractJSON: map[string]string{"authToken": "token"}, ThinkTime: 250 * time.Millisecond},
			{Name: "profile", Method: "GET", Path: "/profile",
				Headers: map[string]string{"Authorization": "Bearer {{authToken}}"}},
		},
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "sc.yaml")
	data, err := yaml.Marshal(want)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := LoadScenario(path)
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}
	// yaml.v3 can materialize a nil Header/Extract map as an empty non-nil map
	// (or vice versa) depending on the file contents; normalize before the
	// structural comparison so nil/empty are treated as equivalent.
	norm := func(s *Scenario) {
		for i := range s.Steps {
			if s.Steps[i].Headers == nil {
				s.Steps[i].Headers = map[string]string{}
			}
			if s.Steps[i].ExtractJSON == nil {
				s.Steps[i].ExtractJSON = map[string]string{}
			}
		}
	}
	norm(&got)
	norm(&want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Loaded scenario differs:\n got %+v\nwant %+v", got, want)
	}
}

func TestLoadScenarioValidation(t *testing.T) {
	cases := []struct {
		name string
		sc   Scenario
		want string
	}{
		{"no steps", Scenario{Name: "x"}, "has no steps"},
		{"missing name", Scenario{Steps: []Step{{Name: " ", Method: "GET", Path: "/x"}}}, "no name"},
		{"missing path", Scenario{Steps: []Step{{Name: "s", Method: "GET"}}}, "no path"},
		{"bad method", Scenario{Steps: []Step{{Name: "s", Method: "get", Path: "/x"}}}, "unsupported method"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.sc.validate()
			if err == nil {
				t.Fatalf("validate() = nil, want an error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("validate() error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestLoadScenarioErrorWrapping(t *testing.T) {
	if _, err := LoadScenario(filepath.Join(t.TempDir(), "missing.yaml")); err == nil || !strings.Contains(err.Error(), "failed to read scenario file") {
		t.Errorf("missing file err = %v, want read-file wrap", err)
	}

	bad := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(bad, []byte("steps: [this is {not yaml"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadScenario(bad); err == nil || !strings.Contains(err.Error(), "failed to parse scenario file") {
		t.Errorf("bad yaml err = %v, want parse wrap", err)
	}

	ext := filepath.Join(t.TempDir(), "sc.txt")
	if err := os.WriteFile(ext, []byte("name: x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadScenario(ext); err == nil || !strings.Contains(err.Error(), "unsupported scenario file extension") {
		t.Errorf("bad extension err = %v, want extension error", err)
	}
}
