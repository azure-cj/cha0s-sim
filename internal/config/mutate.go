package config

import "fmt"

// SetRuleEnabled toggles the Enabled field of the rule matching the given name
// in the CURRENTLY loaded in-memory config. This does NOT persist to disk and
// WILL BE OVERWRITTEN if the underlying config file changes and hot-reload fires.
// Returns an error if no rule with the given name exists.
//
// The read-copy-modify-publish pattern never mutates the current *Config (or any
// Rule inside it) in place: a fresh Rules slice is built with a copy of the target
// rule carrying the new Enabled value, and the whole new Config is atomically
// swapped in. This keeps it race-free against concurrent SetRuleEnabled calls,
// hot-reload's s.current.Store, and request-handling goroutines that iterate
// Rules from store.Current().
func (s *Store) SetRuleEnabled(name string, enabled bool) error {
	cfg := s.current.Load()
	if cfg == nil {
		return fmt.Errorf("no config loaded")
	}

	newRules := make([]Rule, 0, len(cfg.Rules))
	found := false
	for _, r := range cfg.Rules {
		if r.Name == name {
			r.Enabled = enabled
			found = true
		}
		newRules = append(newRules, r)
	}
	if !found {
		return fmt.Errorf("no rule found with name %q", name)
	}

	newCfg := *cfg
	newCfg.Rules = newRules
	s.current.Store(&newCfg)
	return nil
}
