package stress

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// validScenarioMethods is the method set a scenario step may fire. The engine
// does not itself enforce this (a step's method is whatever the caller set),
// but a scenario FILE with a typo'd method would otherwise fail confusingly,
// one request at a time, deep inside the run.
var validScenarioMethods = map[string]bool{
	http.MethodGet:    true,
	http.MethodPost:   true,
	http.MethodPut:    true,
	http.MethodDelete: true,
	http.MethodPatch:  true,
}

// LoadScenario reads a scenario definition from a .yaml/.yml/.json file into
// the plain Scenario value the engine runs directly, mirroring config.Load's
// error-wrapping style: every failure is wrapped with the offending path so a
// bad run flag points at the file, not at a subtle zero-value later.
func LoadScenario(path string) (Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Scenario{}, fmt.Errorf("failed to read scenario file %q: %w", path, err)
	}

	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".yaml", ".yml", ".json":
	default:
		return Scenario{}, fmt.Errorf("unsupported scenario file extension %q (expected .yaml, .yml, or .json)", ext)
	}

	var sc Scenario
	if err := yaml.Unmarshal(data, &sc); err != nil {
		return Scenario{}, fmt.Errorf("failed to parse scenario file %q: %w", path, err)
	}

	if err := sc.validate(); err != nil {
		return Scenario{}, fmt.Errorf("scenario validation failed for %q: %w", path, err)
	}
	return sc, nil
}

// validate holds the checks a scenario file must pass to be runnable. The
// engine keys per-step metrics by step name, so a nameless step would silently
// be unreportable; an empty path would fire the base URL itself; a method
// outside the documented set would fail confusingly at runtime.
func (s Scenario) validate() error {
	if len(s.Steps) == 0 {
		return fmt.Errorf("scenario has no steps")
	}
	for i, step := range s.Steps {
		if strings.TrimSpace(step.Name) == "" {
			return fmt.Errorf("step %d has no name", i)
		}
		if strings.TrimSpace(step.Path) == "" {
			return fmt.Errorf("step %d (%q) has no path", i, step.Name)
		}
		if !validScenarioMethods[step.Method] {
			return fmt.Errorf("step %d (%q) has unsupported method %q (expected one of GET/POST/PUT/DELETE/PATCH)", i, step.Name, step.Method)
		}
	}
	return nil
}
