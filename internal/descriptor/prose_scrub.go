package descriptor

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// ProseScrub is one instance's prose scrub, exported for the surfaces
// outside the manifest that carry hand-written prose naming features —
// the MCP tool descriptions, the reflected and bound tool input schemas,
// and the prompt bodies. It is the manifest scrub (hiddenProseNames +
// redactProse), not a second implementation: the same hidden-token set,
// the same whole-token match and the same sentence drop. Build it once
// per registration with NewProseScrub; the zero value (and a scrub over
// a nil / unscoped instance) returns every input unchanged.
type ProseScrub struct {
	hidden map[string]struct{}
}

// NewProseScrub captures inst's hidden-token set: every hidden operator
// name plus every MCP tool whose owning feature the instance does not
// offer.
func NewProseScrub(inst *InstanceSnapshot) ProseScrub {
	return ProseScrub{hidden: hiddenProseNames(inst)}
}

// Active reports whether the scrub can change anything (the instance
// hides at least one prose token).
func (p ProseScrub) Active() bool { return len(p.hidden) > 0 }

// Text returns s without the sentences that mention a hidden token. It
// is line-aware so multi-paragraph descriptions and markdown prompt
// bodies keep their layout: each line is redacted on its own, a line
// left empty (or holding only a list / heading marker) is dropped, the
// rest of an ordered list a dropped item belonged to is renumbered, and
// the blank-line runs a drop leaves behind collapse to one. A string
// that mentions no hidden token is returned unchanged.
func (p ProseScrub) Text(s string) string {
	if !p.Active() || !mentionsHidden(s, p.hidden) {
		return s
	}
	var kept []string
	shift := 0 // numbered items dropped so far in this blank-line block
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) == "" {
			kept = append(kept, "")
			shift = 0
			continue
		}
		r := line
		if mentionsHidden(line, p.hidden) {
			if r = redactProse(line, p.hidden); isBareMarker(r) {
				if _, ok := listNumber(line); ok {
					shift++
				}
				continue
			}
		}
		if n, ok := listNumber(r); ok && shift > 0 {
			r = strconv.Itoa(n-shift) + strings.TrimPrefix(r, strconv.Itoa(n))
		}
		kept = append(kept, r)
	}
	var out []string
	for _, line := range kept {
		if line == "" && (len(out) == 0 || out[len(out)-1] == "") {
			continue
		}
		out = append(out, line)
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	joined := strings.Join(out, "\n")
	if joined != "" && strings.HasSuffix(s, "\n") {
		joined += "\n"
	}
	return joined
}

// listNumber returns N when line opens an ordered-list item ("N. ").
// Dropping an item renumbers the rest of its list so it stays gapless.
func listNumber(line string) (int, bool) {
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i == 0 || i+1 >= len(line) || line[i] != '.' || line[i+1] != ' ' {
		return 0, false
	}
	n, err := strconv.Atoi(line[:i])
	return n, err == nil
}

// isBareMarker reports whether a redacted line kept nothing but a list
// or heading marker ("4.", "-", "*", "##").
func isBareMarker(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" || s == "*" || strings.Trim(s, "#") == "" {
		return true
	}
	digits := strings.TrimSuffix(s, ".")
	if digits == s || digits == "" {
		return false
	}
	for i := range len(digits) {
		if digits[i] < '0' || digits[i] > '9' {
			return false
		}
	}
	return true
}

// SchemaDescriptions returns raw (a JSON Schema document) with Text
// applied to every string-valued "description" keyword at any depth. A
// schema none of whose descriptions changes is returned as is (same
// bytes); otherwise the document is re-encoded with encoding/json.
func (p ProseScrub) SchemaDescriptions(raw json.RawMessage) (json.RawMessage, error) {
	if !p.Active() || len(raw) == 0 || !mentionsHidden(string(raw), p.hidden) {
		return raw, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	if !p.scrubDescriptions(doc) {
		return raw, nil
	}
	return json.Marshal(doc)
}

// scrubDescriptions rewrites v's description keywords in place and
// reports whether any changed.
func (p ProseScrub) scrubDescriptions(v any) bool {
	changed := false
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			if s, ok := e.(string); ok && k == "description" {
				if r := p.Text(s); r != s {
					t[k] = r
					changed = true
				}
				continue
			}
			if p.scrubDescriptions(e) {
				changed = true
			}
		}
	case []any:
		for _, e := range t {
			if p.scrubDescriptions(e) {
				changed = true
			}
		}
	}
	return changed
}
