package doccheck

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/frankbardon/pulse/internal/strictjson"
	"github.com/frankbardon/pulse/types"
)

// Fence is one fenced code block in a markdown document.
type Fence struct {
	// Info is the info string after the opening fence, trimmed.
	Info string
	// Body is the block's content, the fence's indentation removed from
	// every line, ending in a newline when non-empty.
	Body string
	// Line is the 1-based line of the opening fence.
	Line int
}

// ScanFences returns every fenced code block in markdown source, in order.
// A fence may be indented (inside a list item); its indentation is removed
// from the body lines. An unclosed fence runs to the end of the document,
// as CommonMark renders it.
func ScanFences(src []byte) []Fence {
	lines := strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
	var out []Fence
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		indent := len(line) - len(strings.TrimLeft(line, " "))
		m := fenceOpenRe.FindStringSubmatch(strings.TrimLeft(line, " "))
		if m == nil || indent > 3 && !insideList(lines, i) {
			continue
		}
		marker := m[1]
		info := strings.TrimSpace(strings.TrimLeft(line, " ")[len(marker):])
		if marker[0] == '`' && strings.Contains(info, "`") {
			continue // not a fence: a backtick fence's info string has no backtick
		}
		f := Fence{Info: info, Line: i + 1}
		var body strings.Builder
		for i++; i < len(lines); i++ {
			t := strings.TrimLeft(lines[i], " ")
			if strings.HasPrefix(t, marker) && strings.Trim(t, string(marker[0])+" \t") == "" {
				break
			}
			body.WriteString(dedent(lines[i], indent))
			body.WriteByte('\n')
		}
		f.Body = body.String()
		out = append(out, f)
	}
	return out
}

// insideList is a loose test that an over-indented fence belongs to a list
// item (a nested list indents past four columns) rather than being an
// indented code block: some earlier non-blank line is a list marker.
func insideList(lines []string, i int) bool {
	for j := i - 1; j >= 0; j-- {
		t := strings.TrimSpace(lines[j])
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") || strings.HasPrefix(t, "+ ") {
			return true
		}
		if k := strings.IndexAny(t, ".)"); k > 0 && k < 10 && strings.Trim(t[:k], "0123456789") == "" {
			return true
		}
	}
	return false
}

// dedent removes up to n leading spaces from line.
func dedent(line string, n int) string {
	for k := 0; k < n && strings.HasPrefix(line, " "); k++ {
		line = line[1:]
	}
	return line
}

// SnippetInfo is a fence's info string read under the snippet tags
// TestDocsSnippetsRun honours.
type SnippetInfo struct {
	// Lang is the first word: json, go, yaml, …
	Lang string
	// NoRun is the `norun` word: a json block the snippet runner skips.
	NoRun bool
	// Compile is the `compile` word: a go block the runner compiles.
	Compile bool
	// Cohorts are the fixture cohort names of a `cohort=<name>[,<name>…]`
	// word, in order; the first is the default.
	Cohorts []string
}

// ParseSnippetInfo splits an info string into its snippet tags. Words are
// separated by spaces or commas (mdBook's own attribute separator); an
// unrecognised word is ignored.
func ParseSnippetInfo(info string) SnippetInfo {
	words := strings.FieldsFunc(info, func(r rune) bool { return r == ' ' || r == '\t' || r == ',' })
	var si SnippetInfo
	if len(words) == 0 {
		return si
	}
	si.Lang = words[0]
	// cohort= takes a comma list, which the split above broke into
	// words: re-read the raw info for it.
	for _, w := range strings.Fields(info) {
		if v, ok := strings.CutPrefix(w, "cohort="); ok {
			for _, c := range strings.Split(v, ",") {
				if c = strings.TrimSpace(c); c != "" {
					si.Cohorts = append(si.Cohorts, c)
				}
			}
		}
	}
	for _, w := range words[1:] {
		switch w {
		case "norun":
			si.NoRun = true
		case "compile":
			si.Compile = true
		}
	}
	return si
}

// RootKind names the request root a json snippet decodes as.
type RootKind string

// The request roots a json snippet can be.
const (
	RootRequest  RootKind = "request"
	RootComposed RootKind = "compose"
	RootChain    RootKind = "chain"
	RootFacet    RootKind = "facet"
)

// RequestRoot reports which request root a json snippet strictly decodes
// as, and false when it is not a request root at all (a response, a
// fragment, a schema, a non-object, malformed or commented JSON) — the
// runner skips those silently. The root is chosen by its root keys, as
// the example library chooses it, then strict-decoded; an empty object is
// not a request.
func RequestRoot(body []byte) (RootKind, bool) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil || len(top) == 0 {
		return "", false
	}
	if len(top) == 1 {
		if _, onlyMeta := top["_meta"]; onlyMeta {
			return "", false
		}
	}
	has := func(k string) bool { _, ok := top[k]; return ok }
	var kind RootKind
	var out any
	switch {
	case has("requests") || has("sweep"):
		kind, out = RootComposed, new(types.ComposedRequest)
	case has("stages"):
		kind, out = RootChain, new(types.ChainRequest)
	case has("fields"):
		kind, out = RootFacet, new(types.FacetRequest)
	default:
		kind, out = RootRequest, new(types.Request)
	}
	if err := strictjson.DecodeInto(body, out, strictjson.Options{AllowMeta: true}); err != nil {
		return "", false
	}
	return kind, true
}

// SnippetKey is the allowlist entry naming a snippet: its language and a
// hash of its body, so the entry survives edits elsewhere on the page and
// goes stale when the block itself changes. Whitespace at the ends of the
// body does not count.
func SnippetKey(lang string, body []byte) string {
	sum := sha256.Sum256(bytes.TrimSpace(body))
	return lang + ":" + hex.EncodeToString(sum[:6])
}

// RetargetCohorts points every cohort reference in a request body at the
// fixture cohorts: each object under a "cohort" key gets data_dir set to
// dataDir, and its filename is kept when its stem (the part before ".pulse"
// or a "#" shard anchor) is one of cohorts, else replaced by the first
// cohort's file. It returns the rewritten body.
func RetargetCohorts(body []byte, cohorts []string, dataDir string) ([]byte, error) {
	var root any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, c := range cohorts {
		known[c] = true
	}
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			for k, child := range t {
				if ref, ok := child.(map[string]any); ok && k == "cohort" {
					retarget(ref, cohorts, known, dataDir)
				}
				walk(child)
			}
		case []any:
			for _, child := range t {
				walk(child)
			}
		}
	}
	walk(root)
	return json.Marshal(root)
}

func retarget(ref map[string]any, cohorts []string, known map[string]bool, dataDir string) {
	ref["data_dir"] = dataDir
	name, _ := ref["filename"].(string)
	stem := name
	if i := strings.Index(stem, "#"); i >= 0 {
		stem = stem[:i]
	}
	stem = strings.TrimSuffix(stem, ".pulse")
	if stem != "" && known[stem] {
		return
	}
	ref["filename"] = cohorts[0] + ".pulse"
}
