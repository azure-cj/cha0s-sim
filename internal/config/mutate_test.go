package config

import (
	"fmt"
	"os"
	"path/filepath"
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

func mustBuildRule(t *testing.T, req CreateRuleRequest) *Rule {
	t.Helper()
	r, err := BuildRuleFromRequest(req)
	if err != nil {
		t.Fatalf("BuildRuleFromRequest() error = %v", err)
	}
	return r
}

func TestAddRuleAppendsInMemory(t *testing.T) {
	s := newStoreFromYAML(t, togglableRulesYAML)
	rule := mustBuildRule(t, CreateRuleRequest{Name: "rule-new", Path: "/api/new", Frequency: "often", Effect: "drop"})

	persisted, err := s.AddRule(rule)
	if err != nil {
		t.Fatalf("AddRule() error = %v, want nil", err)
	}
	if !persisted {
		t.Fatal("AddRule() persisted = false, want true")
	}

	cfg := s.Current()
	if len(cfg.Rules) != 4 {
		t.Fatalf("len(Current().Rules) = %d, want 4", len(cfg.Rules))
	}
	got := cfg.Rules[3]
	if got.Name != "rule-new" || !got.Enabled || !got.DropConnection {
		t.Errorf("new rule = %+v, want name rule-new, enabled, drop_connection", got)
	}
	if got.ErrorRate != 0.5 {
		t.Errorf("new rule ErrorRate = %v, want 0.5 (often)", got.ErrorRate)
	}
}

func TestAddRuleDuplicateNameRejected(t *testing.T) {
	s := newStoreFromYAML(t, togglableRulesYAML)
	before := len(s.Current().Rules)
	dup := mustBuildRule(t, CreateRuleRequest{Name: "rulea", Path: "/api/dup", Frequency: "always", Effect: "drop"})

	persisted, err := s.AddRule(dup)
	if err == nil {
		t.Fatal("AddRule() = nil error, want error for duplicate rule name")
	}
	if !strings.Contains(err.Error(), "rulea") {
		t.Errorf("error = %q, want it to name the duplicate rule", err.Error())
	}
	if persisted {
		t.Error("AddRule() persisted = true for duplicate, want false")
	}
	if len(s.Current().Rules) != before {
		t.Errorf("rule count changed after rejected duplicate: %d -> %d", before, len(s.Current().Rules))
	}
}

// TestAddRulePersistsToFile verifies the append is surgical: everything that was
// in the file before survives byte-for-byte as a prefix, and the new rule is
// appended as a well-indented, loadable block after the last rule.
func TestAddRulePersistsToFile(t *testing.T) {
	s, path := newStoreFromYAMLWithPath(t, commentedRulesYAML)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading fixture before add: %v", err)
	}

	rule := mustBuildRule(t, CreateRuleRequest{Name: "rule-d", Path: "/api/d", Frequency: "rarely", Effect: "corrupt", TargetKeys: []string{"ssn"}})
	persisted, err := s.AddRule(rule)
	if err != nil {
		t.Fatalf("AddRule() error = %v, want nil", err)
	}
	if !persisted {
		t.Fatal("AddRule() persisted = false, want true")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading fixture after add: %v", err)
	}
	afterStr := string(after)
	if !strings.HasPrefix(afterStr, string(before)) {
		t.Errorf("existing file bytes were modified by the append:\n=== before ===\n%s\n=== after ===\n%s", before, after)
	}

	for _, want := range []string{
		"  - name: rule-d\n",
		"    path: /api/d\n",
		"    enabled: true\n",
		"    error_rate: 0.05\n",
		"    mangle:\n",
		"      enabled: true\n",
		"      strategy: delete_keys\n",
		"      target_keys:\n",
		"        - ssn\n",
	} {
		if !strings.Contains(afterStr, want) {
			t.Errorf("after add, file is missing %q", want)
		}
	}

	// The file must re-load cleanly with the original rules intact.
	cfg, err := Load(s.path)
	if err != nil {
		t.Fatalf("Load(%q) error = %v, want nil", s.path, err)
	}
	if got := len(cfg.Rules); got != 4 {
		t.Errorf("reloaded rule count = %d, want 4", got)
	}
}

// TestAddRuleMissingFileCreatesConfig covers the desktop "first run" path: no
// config file exists (not even its directory), the app holds an empty store,
// and the create action materializes the file with the new rule inside.
func TestAddRuleMissingFileCreatesConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no", "such", "dir", "chaos.yaml")

	store, err := NewEmptyStore(path)
	if err != nil {
		t.Fatalf("NewEmptyStore() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if got := len(store.Current().Rules); got != 0 {
		t.Fatalf("empty store has %d rules, want 0", got)
	}

	rule := mustBuildRule(t, CreateRuleRequest{Name: "first-rule", Path: "/api/first", Frequency: "always", Effect: "error"})
	persisted, err := store.AddRule(rule)
	if err != nil {
		t.Fatalf("AddRule() error = %v, want nil", err)
	}
	if !persisted {
		t.Fatal("AddRule() persisted = false for a created file, want true")
	}
	if got := len(store.Current().Rules); got != 1 {
		t.Errorf("in-memory rule count = %d, want 1", got)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%q) error = %v, want nil after creation", path, err)
	}
	if len(cfg.Rules) != 1 || cfg.Rules[0].Name != "first-rule" {
		t.Errorf("created file rules = %+v, want exactly first-rule", cfg.Rules)
	}
}

func TestAddRuleEmptyFlowRulesAppends(t *testing.T) {
	const src = "target: http://localhost:3000\nrules: []\n"
	s := newStoreFromYAML(t, src)
	rule := mustBuildRule(t, CreateRuleRequest{Name: "flow-rule", Path: "/api/flow", Frequency: "always", Effect: "drop"})

	persisted, err := s.AddRule(rule)
	if err != nil {
		t.Fatalf("AddRule() error = %v, want nil", err)
	}
	if !persisted {
		t.Fatal("AddRule() persisted = false, want true")
	}

	cfg, err := Load(s.path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if len(cfg.Rules) != 1 || cfg.Rules[0].Name != "flow-rule" {
		t.Errorf("loaded rules = %+v, want exactly flow-rule", cfg.Rules)
	}
}

func TestAddRuleEmptyBlockRulesAppends(t *testing.T) {
	const src = "rules:\nother: 1\n"
	s := newStoreFromYAML(t, src)
	rule := mustBuildRule(t, CreateRuleRequest{Name: "block-rule", Path: "/api/block", Frequency: "sometimes", Effect: "slow"})

	persisted, err := s.AddRule(rule)
	if err != nil {
		t.Fatalf("AddRule() error = %v, want nil", err)
	}
	if !persisted {
		t.Fatal("AddRule() persisted = false, want true")
	}

	cfg, err := Load(s.path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if len(cfg.Rules) != 1 || cfg.Rules[0].Name != "block-rule" {
		t.Errorf("loaded rules = %+v, want exactly block-rule", cfg.Rules)
	}
	data, _ := os.ReadFile(s.path)
	if !strings.Contains(string(data), "other: 1") {
		t.Errorf("unrelated top-level key was lost: %q", string(data))
	}
}

func TestAddRuleMissingRulesKeyAppends(t *testing.T) {
	const src = "# hand-written file with no rules yet\n"
	s := newStoreFromYAML(t, src)
	rule := mustBuildRule(t, CreateRuleRequest{Name: "late-rule", Path: "/api/late", Frequency: "often", Effect: "attack", Attack: "path_traversal"})

	persisted, err := s.AddRule(rule)
	if err != nil {
		t.Fatalf("AddRule() error = %v, want nil", err)
	}
	if !persisted {
		t.Fatal("AddRule() persisted = false, want true")
	}

	cfg, err := Load(s.path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if len(cfg.Rules) != 1 || cfg.Rules[0].Name != "late-rule" {
		t.Errorf("loaded rules = %+v, want exactly late-rule", cfg.Rules)
	}
}

// TestAddRulePreservesCRLF ensures a CRLF config file keeps its line endings in
// the untouched region AND the appended block.
func TestAddRulePreservesCRLF(t *testing.T) {
	content := strings.ReplaceAll(togglableRulesYAML, "\n", "\r\n")
	s := newStoreFromYAML(t, content)
	rule := mustBuildRule(t, CreateRuleRequest{Name: "crlf-rule", Path: "/api/crlf", Frequency: "always", Effect: "drop"})

	persisted, err := s.AddRule(rule)
	if err != nil {
		t.Fatalf("AddRule() error = %v, want nil", err)
	}
	if !persisted {
		t.Fatal("AddRule() persisted = false, want true")
	}

	data, _ := os.ReadFile(s.path)
	if !strings.Contains(string(data), "\r\n") {
		t.Errorf("CRLF endings were lost:\n%q", string(data))
	}
	if !strings.Contains(string(data), "  - name: crlf-rule\r\n") {
		t.Errorf("appended rule does not use CRLF endings:\n%q", string(data))
	}
	if _, err := Load(s.path); err != nil {
		t.Errorf("Load() error = %v, want nil for CRLF output", err)
	}
}

// TestNewEmptyStoreCloseIsSafe verifies an empty store without a watcher still
// closes cleanly and reports an empty rule set.
func TestNewEmptyStoreCloseIsSafe(t *testing.T) {
	s, err := NewEmptyStore(filepath.Join(t.TempDir(), "chaos.yaml"))
	if err != nil {
		t.Fatalf("NewEmptyStore() error = %v, want nil", err)
	}
	if got := len(s.Current().Rules); got != 0 {
		t.Errorf("empty store rule count = %d, want 0", got)
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close() error = %v, want nil (no watcher)", err)
	}
}
