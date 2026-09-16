package stress

import (
	"encoding/json"
	"regexp"
	"time"
)

// Scenario is a named, ordered sequence of steps a virtual user runs through.
// It deliberately has no branching, loops, or scripting: it is a flat
// step-sequence in which each step can extract a value from its own response
// into a variable that later steps consume. That single mechanism covers the
// realistic "login then use the token" class of multi-step flows without
// growing into a full load-scripting language.
type Scenario struct {
	Name  string
	Steps []Step
}

// Step is one request within a Scenario, plus the variable-extraction that
// happens against its own response after it completes.
type Step struct {
	Name        string            // diagnostic label surfaced in per-step metrics, e.g. "login"
	Method      string            // GET/POST/PUT/DELETE/PATCH
	Path        string            // relative path joined with the scenario base URL; supports {{var}} substitution
	Body        string            // optional raw request body (JSON typically); supports {{var}} substitution
	Headers     map[string]string // optional headers; values support {{var}} substitution
	ExtractJSON map[string]string // variableName -> top-level JSON key on THIS step's response body
	ThinkTime   time.Duration     // delay after this step completes, before the next step resumes
}

// placeholderRe matches a {{variableName}} token with optional surrounding
// whitespace inside the braces. Variable names are restricted to letters,
// digits and underscores, so a dotted or nested reference like {{user.id}}
// simply does not match and stays literal.
var placeholderRe = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_]+)\s*\}\}`)

// substituteVars replaces every {{name}} token with the value held in vars. A
// token whose name is missing from vars is left UNSUBSTITUTED, byte-for-byte —
// the literal {{name}} stays in the output. An unsubstituted placeholder in a
// request is a visible signal that an upstream extraction failed, which is more
// useful than silently blanking the field. Because replacement happens in a
// single pass and the regex engine does not re-scan replacement text, an
// extracted value that itself contains {{...}} cannot re-trigger substitution.
func substituteVars(s string, vars map[string]string) string {
	return placeholderRe.ReplaceAllStringFunc(s, func(tok string) string {
		name := placeholderRe.FindStringSubmatch(tok)[1]
		if v, ok := vars[name]; ok {
			return v
		}
		return tok
	})
}

// extractTopLevel pulls the value of a single TOP-LEVEL JSON object key from a
// response body. v1 intentionally supports only top-level keys: nested paths
// ("user.profile.id") and array indexing are out of scope and simply never
// match. A missing key, a non-JSON body, a null value, or a value that is
// itself an object/array all yield ok == false; the caller treats that as "the
// variable is not available", not as a fatal error. String values extract as
// the string; other scalars (numbers/booleans) extract as their literal JSON
// text.
func extractTopLevel(body, key string) (value string, ok bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &obj); err != nil {
		return "", false
	}
	raw, has := obj[key]
	if !has || string(raw) == "null" {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, true
	}
	switch raw[0] {
	case '"', '{', '[':
		return "", false // string parse failed, and object/array values aren't substitutable
	}
	return string(raw), true
}