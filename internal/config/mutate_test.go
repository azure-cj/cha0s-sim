package config

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

const togglableRulesYAML = `
rules:
  - name: "rulea"
    path: "/api/a"
    enabled: true
  - name: "ruleb"
    path: "/api/b"
    enabled: false
  - name: "rulec"
    path: "/api/c"
    enabled: true
`

func newStoreFromYAML(t *testing.T, content string) *Store {
	t.Helper()
	path := writeConfig(t, content)
	s, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func ruleByName(t *testing.T, cfg *Config, name string) *Rule {
	t.Helper()
	for i := range cfg.Rules {
		if cfg.Rules[i].Name == name {
			return &cfg.Rules[i]
		}
	}
	t.Fatalf("rule %q not found in config with %d rules", name, len(cfg.Rules))
	return nil
}

func TestSetRuleEnabledFlipsEnabled(t *testing.T) {
	s := newStoreFromYAML(t, togglableRulesYAML)

	if err := s.SetRuleEnabled("rulea", false); err != nil {
		t.Fatalf("SetRuleEnabled() error = %v, want nil", err)
	}

	got := ruleByName(t, s.Current(), "rulea")
	if got.Enabled {
		t.Errorf("rulea.Enabled = true, want false after toggle")
	}
}

func TestSetRuleEnabledUnknownName(t *testing.T) {
	s := newStoreFromYAML(t, togglableRulesYAML)

	before := s.Current()
	wantEnabled := map[string]bool{}
	for _, r := range before.Rules {
		wantEnabled[r.Name] = r.Enabled
	}

	err := s.SetRuleEnabled("does-not-exist", true)
	if err == nil {
		t.Fatal("SetRuleEnabled() = nil error, want error for unknown rule name")
	}
	if !strings.Contains(err.Error(), `no rule found with name "does-not-exist"`) {
		t.Errorf("error = %q, want it to contain the unknown rule name", err.Error())
	}

	after := s.Current()
	if len(after.Rules) != len(before.Rules) {
		t.Fatalf("rule count changed after failed toggle: %d -> %d", len(before.Rules), len(after.Rules))
	}
	for _, r := range after.Rules {
		if before, ok := wantEnabled[r.Name]; ok && r.Enabled != before {
			t.Errorf("%s.Enabled changed to %v after failed toggle, want %v", r.Name, r.Enabled, before)
		}
	}
}

func TestSetRuleEnabledDoesNotAffectOthers(t *testing.T) {
	s := newStoreFromYAML(t, togglableRulesYAML)

	if err := s.SetRuleEnabled("ruleb", true); err != nil {
		t.Fatalf("SetRuleEnabled() error = %v, want nil", err)
	}

	cfg := s.Current()
	if got := ruleByName(t, cfg, "ruleb"); !got.Enabled {
		t.Errorf("ruleb.Enabled = false, want true after toggle")
	}
	if got := ruleByName(t, cfg, "rulea"); !got.Enabled {
		t.Errorf("rulea.Enabled = false, want true (untouched)")
	}
	if got := ruleByName(t, cfg, "rulec"); !got.Enabled {
		t.Errorf("rulec.Enabled = false, want true (untouched)")
	}
}

// TestSetRuleEnabledConcurrent spins up concurrent toggles against distinct
// rule names to exercise the read-copy-modify-publish path. It asserts no
// errors, no panics, and that the rule set is never corrupted (every rule
// survives, in order). Run with -race to confirm no data races.
func TestSetRuleEnabledConcurrent(t *testing.T) {
	var yamlDoc strings.Builder
	yamlDoc.WriteString("rules:\n")
	const n = 4
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("rule%c", 'a'+i)
		fmt.Fprintf(&yamlDoc, "  - name: %q\n    path: %q\n    enabled: true\n", name, "/api/"+name)
	}
	s := newStoreFromYAML(t, yamlDoc.String())

	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("rule%c", 'a'+i)
			errs[i] = s.SetRuleEnabled(name, i%2 == 0)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("SetRuleEnabled(%q) error = %v", fmt.Sprintf("rule%c", 'a'+i), err)
		}
	}

	cfg := s.Current()
	if len(cfg.Rules) != n {
		t.Fatalf("len(Current().Rules) = %d, want %d", len(cfg.Rules), n)
	}
	for i, r := range cfg.Rules {
		if want := fmt.Sprintf("rule%c", 'a'+i); r.Name != want {
			t.Errorf("Current().Rules[%d].Name = %q, want %q", i, r.Name, want)
		}
	}
}
