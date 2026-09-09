package config

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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

	if err := writeFileAtomic(s.path, bytes.Join(lines, []byte{'\n'}), 0o644); err != nil {
		return false, fmt.Errorf("write %q: %w", s.path, err)
	}
	return true, nil
}

// renameOver is os.Rename with a short retry loop. Windows' MoveFileEx replace
// can transiently fail with a sharing violation ("Access is denied") when two
// writers replace the same target back-to-back or a concurrent reader has the
// file open; a couple of retries make the read-copy-modify-publish path under
// concurrency reliably durable while still reporting a true error for hard
// failures (e.g. a read-only target).
func renameOver(src, dst string) error {
	const attempts = 5
	var err error
	for i := 0; i < attempts; i++ {
		if err = os.Rename(src, dst); err == nil {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return err
}

// writeFileAtomic writes data to path via a same-directory temp file + rename,
// so the watched config file is never read mid-write: readers (including the
// hot-reload watcher) always observe either the old complete file or the new
// complete file — never a truncated/partial one. This eliminates the reload
// race where a concurrent toggle could be picked up as an empty config.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file in %q: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	// Sync before rename so the new file is durable before it becomes visible.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := renameOver(tmpName, path); err != nil {
		return fmt.Errorf("rename temp over %q: %w", path, err)
	}
	tmpName = "" // renamed into place; nothing to clean up
	return nil
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

// AddRule appends a new rule to the in-memory config and then attempts to
// persist it to the source file. The two-value contract mirrors SetRuleEnabled:
//   - err is non-nil ONLY when the rule was not added in-memory (bad input or a
//     duplicate name), so the rule set is never mutated on failure.
//   - err == nil means the rule is now live in memory; persisted reports whether
//     it was also written to disk (false means it will be lost on hot-reload or
//     restart, with the cause logged).
//
// The new rule is appended as a value copy, so a caller mutating its *Rule
// afterwards cannot corrupt the stored config.
func (s *Store) AddRule(rule *Rule) (persisted bool, err error) {
	cfg := s.current.Load()
	if cfg == nil {
		return false, fmt.Errorf("no config loaded")
	}
	for _, r := range cfg.Rules {
		if r.Name == rule.Name {
			return false, fmt.Errorf("a rule named %q already exists — rule names must be unique", rule.Name)
		}
	}

	newCfg := *cfg
	newCfg.Rules = append(newCfg.Rules, *rule)
	s.current.Store(&newCfg)

	persisted, persistErr := s.persistAddRule(rule)
	if !persisted {
		log.Printf("config: rule %q added in-memory but not persisted to %q: %v", rule.Name, s.path, persistErr)
	}
	return persisted, nil
}

// persistAddRule writes a newly created rule into the source file by appending
// it to the existing `rules` sequence, preserving the rest of the document
// (comments, blank lines, indentation, other rules) byte-for-byte. It uses the
// yaml.Node API ONLY for location: where the sequence ends. The entry itself is
// rendered fresh through a yaml.Encoder (SetIndent(2), matching the repo's 2-space
// convention), then textually wrapped so its dash aligns with the existing
// elements' dash column.
//
// Handled shapes: a populated block sequence (the common case), an empty block
// sequence (`rules:` with nothing under it), a flow value (`rules: []`), a
// missing top-level `rules` key, and a missing file (which is created from
// scratch). The result is re-parsed and validated BEFORE writing; any mismatch
// refuses the write rather than risk corrupting the file.
func (s *Store) persistAddRule(rule *Rule) (bool, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return s.createConfigWithRule(rule)
		}
		return false, fmt.Errorf("read %q: %w", s.path, err)
	}

	entry, err := renderRuleEntryLines(rule)
	if err != nil {
		return false, err
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return false, fmt.Errorf("parse %q: %w", s.path, err)
	}
	// Kind 0 = a document holding nothing but comments (or nothing at all),
	// which has no root mapping yet; treat it like a file with no rules key.
	if doc.Kind != yaml.DocumentNode && doc.Kind != 0 {
		return false, fmt.Errorf("parse %q: unexpected document shape", s.path)
	}

	var root *yaml.Node
	if len(doc.Content) > 0 {
		root = doc.Content[0]
	}
	if root != nil && root.Kind != yaml.MappingNode {
		return false, fmt.Errorf("parse %q: unexpected document shape (not a top-level mapping)", s.path)
	}

	var rulesKey, rulesSeq *yaml.Node
	if root != nil {
		for i := 0; i+1 < len(root.Content); i += 2 {
			if root.Content[i].Value == "rules" {
				rulesKey = root.Content[i]
				rulesSeq = root.Content[i+1]
				break
			}
		}
	}

	// Split on '\n' so trailing '\r' (CRLF files) stays glued to each line:
	// node line/column references still line up, and inserted lines are given
	// a matching '\r' below.
	lines := bytes.Split(data, []byte{'\n'})
	crlf := bytes.Contains(data, []byte("\r\n"))
	eol := ""
	if crlf {
		eol = "\r"
	}

	var insertAt int   // index of the line we insert AFTER
	var dashIndent int // base column of the sequence dashes
	var insert []string

	switch {
	case rulesSeq == nil:
		// No `rules` key anywhere: append a fresh section at the end, using the
		// top-level key indentation of whatever the document already has.
		rootIndent := 0
		if k := rootKeyLine(root, lines); k >= 0 {
			rootIndent = leadingSpaces(lines[k])
		}
		dashIndent = rootIndent + 2
		insertAt = len(lines) - 1
		insert = append([]string{strings.Repeat(" ", rootIndent) + "rules:"}, prefixRuleEntry(entry, dashIndent)...)

	case rulesSeq.Content == nil || len(rulesSeq.Content) == 0:
		// Empty sequence: `rules:` block with no items, or a flow `rules: []`.
		rulesLine := lines[rulesKey.Line-1]
		dashIndent = leadingSpaces(rulesLine) + 2
		insertAt = rulesKey.Line - 1

		if col, err := valueColumnOffset(rulesLine, rulesSeq); err == nil {
			if stripped, ok := stripFlowEmpty(rulesLine, col); ok {
				lines[insertAt] = stripped
			}
		}
		insert = prefixRuleEntry(entry, dashIndent)

	default:
		// Populated block sequence: insert after the last element's final line.
		firstEl := rulesSeq.Content[0]
		if firstEl.Line == rulesKey.Line {
			// Inline/flow form like `rules: [{...}, ...]` cannot be appended to
			// without rewriting the whole sequence; refuse rather than corrupt.
			return false, fmt.Errorf("rule %q not persisted: %q uses an inline flow rules list, which is unsupported for appending — write the rules in block style instead", rule.Name, s.path)
		}
		last := rulesSeq.Content[len(rulesSeq.Content)-1]
		insertAt = nodeMaxLine(last) - 1
		dashIndent = leadingSpaces(lines[firstEl.Line-1])
		insert = append([]string{""}, prefixRuleEntry(entry, dashIndent)...)
	}

	if eol != "" {
		for i := range insert {
			insert[i] += eol
		}
	}

	// Assemble as three blocks joined by single newlines so no part of the
	// document is ever glued onto the previous part.
	parts := make([][]byte, 0, 3)
	parts = append(parts, bytes.Join(lines[:insertAt+1], []byte{'\n'}))
	parts = append(parts, []byte(strings.Join(insert, "\n")))
	if insertAt+1 < len(lines) {
		parts = append(parts, bytes.Join(lines[insertAt+1:], []byte{'\n'}))
	}
	newLines := bytes.Join(parts, []byte{'\n'})

	if err := verifyRuleConfig(newLines); err != nil {
		return false, fmt.Errorf("refusing to persist rule %q: appended YAML failed verification: %w", rule.Name, err)
	}

	if err := writeFileAtomic(s.path, newLines, 0o644); err != nil {
		return false, fmt.Errorf("write %q: %w", s.path, err)
	}
	return true, nil
}

// createConfigWithRule writes a brand-new config file when none exists yet,
// creating parent directories as needed and including only the new rule.
func (s *Store) createConfigWithRule(rule *Rule) (bool, error) {
	entry, err := renderRuleEntryLines(rule)
	if err != nil {
		return false, err
	}
	var out bytes.Buffer
	out.WriteString("rules:\n")
	for _, ln := range prefixRuleEntry(entry, 2) {
		out.WriteString(ln + "\n")
	}
	if err := verifyRuleConfig(out.Bytes()); err != nil {
		return false, fmt.Errorf("refusing to persist rule %q: generated YAML failed verification: %w", rule.Name, err)
	}
	if dir := filepath.Dir(s.path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return false, fmt.Errorf("create config directory %q: %w", dir, err)
		}
	}
	if err := writeFileAtomic(s.path, out.Bytes(), 0o644); err != nil {
		return false, fmt.Errorf("write %q: %w", s.path, err)
	}
	return true, nil
}

// renderRuleEntryLines marshals a single Rule through a 2-space-indent encoder
// and returns its lines WITHOUT the sequence dash, so callers can prefix them
// with whatever base indentation the target file uses. yaml.v3 emits struct
// fields in declaration order, matching the repo's field layout.
func renderRuleEntryLines(rule *Rule) ([]string, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(rule); err != nil {
		return nil, fmt.Errorf("encode rule %q: %w", rule.Name, err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encode rule %q: %w", rule.Name, err)
	}
	lines := strings.Split(buf.String(), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, nil
}

// prefixRuleEntry turns a bare encoded rule into a sequence element: the first
// line gets the dash at dashIndent, every continuation line gets dashIndent+2
// (the key column), matching the repo's nested 2-space convention on top of the
// encoder's own relative indentation.
func prefixRuleEntry(entry []string, dashIndent int) []string {
	out := make([]string, 0, len(entry))
	dash := strings.Repeat(" ", dashIndent)
	keyPad := strings.Repeat(" ", dashIndent+2)
	for i, ln := range entry {
		if i == 0 {
			out = append(out, dash+"- "+ln)
		} else {
			out = append(out, keyPad+ln)
		}
	}
	return out
}

// nodeMaxLine returns the largest Line over node and all its descendants, i.e.
// the last physical line an element occupies.
func nodeMaxLine(n *yaml.Node) int {
	mx := n.Line
	for _, c := range n.Content {
		if l := nodeMaxLine(c); l > mx {
			mx = l
		}
	}
	return mx
}

// rootKeyLine returns the 0-based index of the raw line holding the root
// mapping's first key, or -1 if there is none.
func rootKeyLine(root *yaml.Node, lines [][]byte) int {
	if root == nil || len(root.Content) == 0 {
		return -1
	}
	idx := root.Content[0].Line - 1
	if idx < 0 || idx >= len(lines) {
		return -1
	}
	return idx
}

// valueColumnOffset converts a sequence node's Column into a byte offset into
// its raw line (the line holding `<key>: <value>`).
func valueColumnOffset(line []byte, n *yaml.Node) (int, error) {
	return byteOffsetAtColumn(line, n.Column-1)
}

// stripFlowEmpty detects a flow empty sequence (`[]`) starting at byte offset
// col and removes exactly those two characters, preserving anything else on the
// line (e.g. a trailing comment). Returns (line, true) when `[]` was found.
func stripFlowEmpty(line []byte, col int) ([]byte, bool) {
	start := -1
	for i := col; i < len(line); i++ {
		if line[i] == '[' {
			start = i
			break
		}
		if line[i] == '#' {
			return line, false
		}
	}
	if start < 0 {
		return line, false
	}
	end := -1
	for i := start; i < len(line); i++ {
		if line[i] == ']' {
			end = i
			break
		}
		if line[i] == '#' {
			return line, false
		}
	}
	if end < 0 {
		return line, false
	}
	rem := make([]byte, 0, len(line)-2)
	rem = append(rem, line[:start]...)
	rem = append(rem, line[end+1:]...)
	return rem, true
}

func leadingSpaces(b []byte) int {
	n := 0
	for n < len(b) && b[n] == ' ' {
		n++
	}
	return n
}

// verifyRuleConfig re-parses candidate file bytes and runs the same validation
// the loader applies, so a broken append can never be written over a good file.
func verifyRuleConfig(data []byte) error {
	var check Config
	if err := yaml.Unmarshal(data, &check); err != nil {
		return fmt.Errorf("verify parse: %w", err)
	}
	if err := check.Validate(); err != nil {
		return fmt.Errorf("verify validation: %w", err)
	}
	return nil
}
