package config

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"strconv"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// SetRuleEnabled toggles the Enabled field of the rule matching the given name.
// The in-memory config is always updated — via the read-copy-modify-publish
// pattern, which never mutates the shared *Config/Rule in place — if the rule
// exists. Then an attempt is made to persist the change to the source file with
// a surgical, byte-preserving edit.
//
// Returns (persisted, err):
//   - err is non-nil ONLY when the rule was not found (the in-memory change did
//     not happen). err == nil means the in-memory toggle succeeded.
//   - If err is nil and persisted is true, the change was also written to disk.
//   - If err is nil and persisted is false, the in-memory toggle succeeded but
//     the disk write did not happen (reason logged); callers should treat this
//     as a soft warning, since the toggle still affects live traffic but will be
//     lost on hot-reload or restart.
//
// Note: writing to the watched file triggers the existing hot-reload path, which
// reloads and republishes the same change — a harmless, self-consistent no-op.
func (s *Store) SetRuleEnabled(name string, enabled bool) (persisted bool, err error) {
	cfg := s.current.Load()
	if cfg == nil {
		return false, fmt.Errorf("no config loaded")
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
		return false, fmt.Errorf("no rule found with name %q", name)
	}

	newCfg := *cfg
	newCfg.Rules = newRules
	s.current.Store(&newCfg)

	persisted, persistErr := s.persistRuleEnabled(name, enabled)
	if !persisted {
		log.Printf("config: rule %q toggle applied in-memory but not persisted to %q: %v", name, s.path, persistErr)
	}
	return persisted, nil
}

// persistRuleEnabled surgically flips the enabled scalar for the named rule in
// the source file. It uses yaml.v3's Node API ONLY for location: the rule is
// matched by walking the parsed node tree (so a nested `mangle.enabled` can
// never be confused with the rule's direct `enabled` key), then the value token
// found at that exact line/column is replaced in the RAW file bytes.
//
// Nothing else is rewritten — comments, blank lines, indentation, and line
// endings survive byte-for-byte because the document is never re-encoded. (A
// full yaml.Node re-encode was rejected after probing: yaml.v3 normalizes
// nested indentation, drops blank lines, and collapses inline-comment spacing.)
//
// Returns (false, cause) on any failure: unreadable/missing file, parse failure,
// rule not found, rule lacking a direct `enabled` key, or a sanity-check
// mismatch at the located token (in which case it refuses to write rather than
// corrupt the file).
func (s *Store) persistRuleEnabled(name string, enabled bool) (bool, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return false, fmt.Errorf("read %q: %w", s.path, err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return false, fmt.Errorf("parse %q: %w", s.path, err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return false, fmt.Errorf("parse %q: unexpected document shape (not a top-level mapping)", s.path)
	}

	var enabledNode *yaml.Node
search:
	for i := 0; i+1 < len(doc.Content[0].Content); i += 2 {
		if doc.Content[0].Content[i].Value != "rules" {
			continue
		}
		seq := doc.Content[0].Content[i+1]
		if seq.Kind != yaml.SequenceNode {
			continue
		}
		for _, ruleMap := range seq.Content {
			if ruleMap.Kind != yaml.MappingNode {
				continue
			}
			var nameNode, enNode *yaml.Node
			for j := 0; j+1 < len(ruleMap.Content); j += 2 {
				switch ruleMap.Content[j].Value {
				case "name":
					nameNode = ruleMap.Content[j+1]
				case "enabled":
					enNode = ruleMap.Content[j+1]
				}
			}
			if nameNode != nil && nameNode.Value == name {
				if enNode == nil {
					return false, fmt.Errorf("rule %q has no direct enabled key in %q; skipping persistence (add an explicit enabled: line to persist toggles)", name, s.path)
				}
				enabledNode = enNode
				break search
			}
		}
		break
	}
	if enabledNode == nil {
		return false, fmt.Errorf("rule %q not found in %q (or file uses an unsupported structure); skipping persistence", name, s.path)
	}

	lines := bytes.Split(data, []byte{'\n'})
	lineIdx := enabledNode.Line - 1
	if lineIdx < 0 || lineIdx >= len(lines) {
		return false, fmt.Errorf("locate rule %q: line %d out of range", name, enabledNode.Line)
	}
	line := lines[lineIdx]
	token := []byte(enabledNode.Value)
	colBytes, err := byteOffsetAtColumn(line, enabledNode.Column-1)
	if err != nil {
		return false, fmt.Errorf("locate rule %q enabled value: %w", name, err)
	}
	if colBytes+len(token) > len(line) || !bytes.Equal(line[colBytes:colBytes+len(token)], token) {
		return false, fmt.Errorf("locate rule %q enabled value: token mismatch at line %d column %d (refusing to write)", name, enabledNode.Line, enabledNode.Column)
	}

	repl := strconv.FormatBool(enabled)
	newLine := make([]byte, 0, len(line)+len(repl)-len(token))
	newLine = append(newLine, line[:colBytes]...)
	newLine = append(newLine, repl...)
	newLine = append(newLine, line[colBytes+len(token):]...)
	lines[lineIdx] = newLine

	if err := os.WriteFile(s.path, bytes.Join(lines, []byte{'\n'}), 0o644); err != nil {
		return false, fmt.Errorf("write %q: %w", s.path, err)
	}
	return true, nil
}

// byteOffsetAtColumn converts a zero-based character column (as used to land on
// yaml.Node.Column) into a byte offset within line, so tokens after multibyte
// characters are still located precisely.
func byteOffsetAtColumn(line []byte, colRunes int) (int, error) {
	off := 0
	count := 0
	for count < colRunes && off < len(line) {
		_, size := utf8.DecodeRune(line[off:])
		if size == 0 {
			size = 1
		}
		off += size
		count++
	}
	if count != colRunes {
		return 0, fmt.Errorf("column %d beyond line length %d", colRunes, utf8.RuneCount(line))
	}
	return off, nil
}
