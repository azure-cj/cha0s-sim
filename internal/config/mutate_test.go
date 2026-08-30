package config

import (
	"fmt"
	"os"
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

// commentedRulesYAML exercises the surgical-disk-edit path: header comments,
// an inline comment on the toggled enabled line, and a blank line between rules
// must all survive byte-for-byte.
const commentedRulesYAML = `# header comment line one
# header comment line two
rules:
  # latency rule for users
  - name: "rulea"
    path: "/api/a"
    enabled: true  # control traffic at 20%

  - name: "ruleb"
    path: "/api/b"
    enabled: false  # disabled until release
  - name: "rulec"
    path: "/api/c"
    enabled: true
`

func newStoreFromYAML(t *testing.T, content string) *Store {
	t.Helper()
	s, _ := newStoreFromYAMLWithPath(t, content)
	return s
}

func newStoreFromYAMLWithPath(t *testing.T, content string) (*Store, string) {
	t.Helper()
	path := writeConfig(t, content)
	s, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
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

	persisted, err := s.SetRuleEnabled("rulea", false)
	if err != nil {
		t.Fatalf("SetRuleEnabled() error = %v, want nil", err)
	}
	if !persisted {
		t.Fatal("SetRuleEnabled() persisted = false, want true")
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

	persisted, err := s.SetRuleEnabled("does-not-exist", true)
	if err == nil {
		t.Fatal("SetRuleEnabled() = nil error, want error for unknown rule name")
	}
	if !strings.Contains(err.Error(), `no rule found with name "does-not-exist"`) {
		t.Errorf("error = %q, want it to contain the unknown rule name", err.Error())
	}
	if persisted {
		t.Error("SetRuleEnabled() persisted = true for unknown rule, want false")
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

	persisted, err := s.SetRuleEnabled("ruleb", true)
	if err != nil {
		t.Fatalf("SetRuleEnabled() error = %v, want nil", err)
	}
	if !persisted {
		t.Fatal("SetRuleEnabled() persisted = false, want true")
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

// TestSetRuleEnabledPersistsToFile verifies the SURGICAL disk edit: the file is
// re-read as raw bytes after the toggle and reconstructed back to the original
// by flipping only the one token back — proving comments, blank lines, and all
// other rules are byte-for-byte untouched.
func TestSetRuleEnabledPersistsToFile(t *testing.T) {
	s, path := newStoreFromYAMLWithPath(t, commentedRulesYAML)

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading fixture before toggle: %v", err)
	}

	persisted, err := s.SetRuleEnabled("rulea", false)
	if err != nil {
		t.Fatalf("SetRuleEnabled() error = %v, want nil", err)
	}
	if !persisted {
		t.Fatal("SetRuleEnabled() persisted = false, want true")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading fixture after toggle: %v", err)
	}

	afterStr := string(after)
	for _, want := range []string{
		"# header comment line one",
		"# header comment line two",
		"  # latency rule for users",
		"enabled: false  # control traffic at 20%",
		"enabled: false  # disabled until release",
		"\n  - name: \"rulec\"\n    path: \"/api/c\"\n    enabled: true\n",
	} {
		if !strings.Contains(afterStr, want) {
			t.Errorf("after toggle, file is missing %q", want)
		}
	}

	restored := strings.Replace(afterStr, "enabled: false  # control traffic at 20%", "enabled: true  # control traffic at 20%", 1)
	if restored != string(before) {
		t.Errorf("edit was not surgical (more than the one token changed):\n=== restored (should equal before) ===\n%s\n=== before ===\n%s", restored, string(before))
	}
}

// TestSetRuleEnabledPersistenceFailureKeepsInMemory makes the file read-only and
// confirms the hard-error contract of the two-value return: the in-memory toggle
// still applies (err == nil) even though the disk write fails (persisted false).
// Windows honors the read-only attribute (writes fail with access denied); some
// Unix setups running as root may still write, in which case the test skips.
func TestSetRuleEnabledPersistenceFailureKeepsInMemory(t *testing.T) {
	s, path := newStoreFromYAMLWithPath(t, togglableRulesYAML)

	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatalf("os.Chmod(%q, 0o444) error = %v", path, err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })

	persisted, err := s.SetRuleEnabled("ruleb", true)
	if err != nil {
		t.Fatalf("SetRuleEnabled() error = %v, want nil even when persistence is blocked", err)
	}
	if persisted {
		t.Skip("read-only file was still writable on this platform; persistence failure not simulable here")
	}

	if got := ruleByName(t, s.Current(), "ruleb"); !got.Enabled {
		t.Errorf("ruleb.Enabled = false after failed persistence, want true (in-memory toggle must survive)")
	}
}

// TestSetRuleEnabledConcurrent spins up concurrent toggles against distinct
// rule names to exercise the read-copy-modify-publish path plus concurrent disk
// writes. It asserts no hard errors and that the rule set is never corrupted
// (every rule survives, in order). Run with -race to confirm no data races where
// the detector is available.
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
	persisted := make([]bool, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("rule%c", 'a'+i)
			var err error
			persisted[i], err = s.SetRuleEnabled(name, i%2 == 0)
			errs[i] = err
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
