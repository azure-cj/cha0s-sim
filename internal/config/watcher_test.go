package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "chaos.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
	return path
}

func rulesYAML(n int) string {
	s := "rules:\n"
	for i := 0; i < n; i++ {
		s += "  - name: \"rule" + string(rune('a'+i)) + "\"\n"
		s += "    path: \"/api/rule" + string(rune('a'+i)) + "\"\n"
	}
	return s
}

func TestNewStoreFailsOnInvalidInitialConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	_, err := NewStore(path)
	if err == nil {
		t.Fatal("NewStore() = nil error, want error for nonexistent file")
	}
}

func TestNewStoreLoadsInitialConfig(t *testing.T) {
	path := writeConfig(t, rulesYAML(2))
	s, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore() error = %v, want nil", err)
	}
	defer s.Close()

	cfg := s.Current()
	if cfg == nil {
		t.Fatal("Current() = nil, want non-nil")
	}
	if len(cfg.Rules) != 2 {
		t.Errorf("len(Current().Rules) = %d, want 2", len(cfg.Rules))
	}
}

func TestHotReloadPicksUpValidChanges(t *testing.T) {
	path := writeConfig(t, rulesYAML(2))
	s, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore() error = %v, want nil", err)
	}
	defer s.Close()

	if got := len(s.Current().Rules); got != 2 {
		t.Fatalf("initial rule count = %d, want 2", got)
	}

	if err := os.WriteFile(path, []byte(rulesYAML(3)), 0o644); err != nil {
		t.Fatalf("failed to overwrite config: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	if got := len(s.Current().Rules); got != 3 {
		t.Errorf("rule count after reload = %d, want 3", got)
	}
}

func TestHotReloadPicksUpAtomicRename(t *testing.T) {
	path := writeConfig(t, rulesYAML(2))
	s, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore() error = %v, want nil", err)
	}
	defer s.Close()

	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		t.Fatalf("os.CreateTemp() error = %v", err)
	}
	if _, err := tmp.Write([]byte(rulesYAML(4))); err != nil {
		t.Fatalf("tmp.Write() error = %v", err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatalf("tmp.Close() error = %v", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		t.Fatalf("os.Rename() error = %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	if got := len(s.Current().Rules); got != 4 {
		t.Errorf("rule count after atomic rename = %d, want 4", got)
	}
}

func TestHotReloadIgnoresInvalidChanges(t *testing.T) {
	path := writeConfig(t, rulesYAML(2))
	s, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore() error = %v, want nil", err)
	}
	defer s.Close()

	if got := len(s.Current().Rules); got != 2 {
		t.Fatalf("initial rule count = %d, want 2", got)
	}

	invalid := "rules:\n  - name: \"bad\"\n    path: \"/x\"\n    error_rate: 5.0\n"
	if err := os.WriteFile(path, []byte(invalid), 0o644); err != nil {
		t.Fatalf("failed to overwrite config: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	if got := len(s.Current().Rules); got != 2 {
		t.Errorf("rule count after invalid reload = %d, want 2 (last-known-good kept)", got)
	}
}
