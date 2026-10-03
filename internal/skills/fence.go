package skills

import (
	"fmt"
	"io/fs"
	"regexp"
	"strings"
)

// Feature fences — the served-body renderer (U10).
//
// A skill body may wrap prose that only makes sense when a feature is
// offered in a fence:
//
//	<!-- feature: NAME[, NAME…] -->…<!-- /feature -->
//
// NAME is the feature-profile spelling (operators bare — TEST_WELCH —
// every other kind `<kind>:<name>` — capability:crosstab). A comma list
// means AND: the fenced text is kept only when EVERY name is visible.
//
// Two forms:
//
//   - BLOCK: the opener and the closer each stand alone on their line
//     (surrounding whitespace allowed). Rendering removes whole lines —
//     the two marker lines always, the lines between them too when the
//     fence is hidden — so a fenced table row, list item or code-fence
//     body never leaves a half-line behind.
//   - INLINE: an opener sharing its line with other text. It must close
//     on the SAME line. A hidden inline span is cut out of the line; a
//     line left empty (or holding only a list / heading marker) by the
//     cut is dropped.
//
// Nesting (an opener inside an open fence) and unbalanced markers (a
// closer with no opener, a block left open at the end of the body, an
// inline opener not closed on its line) are parse errors (*FenceError).
// Markers are recognised everywhere, code fences included.
//
// Markers are ALWAYS stripped from a served body — the full instance
// (keep == nil) included — and the embedded files are never rewritten:
// Raw serves the file verbatim, Get the full-instance render. A blank
// line a dropped run leaves next to another blank line collapses into
// it; blank runs the source already had are kept, so a fence-free body
// renders byte-identically.

// FenceError is a malformed feature fence: Line is 1-based.
type FenceError struct {
	Line int
	Msg  string
}

func (e *FenceError) Error() string {
	return fmt.Sprintf("feature fence: line %d: %s", e.Line, e.Msg)
}

// Fence is one parsed feature fence.
type Fence struct {
	// Line is the 1-based line of the opener.
	Line int
	// Names are the fenced features (AND), in source order.
	Names []string
	// Block is true for a block fence, false for an inline one.
	Block bool
}

// fenceMarker matches one opener (group 2 = the name list) or closer.
var fenceMarker = regexp.MustCompile(`<!--\s*(/feature|feature:(.*?))\s*-->`)

// ParseFences returns every fence in body, in source order, or the first
// malformation as a *FenceError. An opener with an empty name (or an
// empty entry in its list) is malformed; whether a name is a REAL
// feature is the caller's check (the feature table lives in
// internal/descriptor).
func ParseFences(body string) ([]Fence, error) {
	var out []Fence
	_, err := renderFences(body, nil, func(f Fence) { out = append(out, f) })
	return out, err
}

// RenderFences renders body for an instance: a fence is kept iff keep
// reports every one of its names visible (keep == nil keeps every
// fence), and every marker is stripped. A malformed body returns the
// *FenceError and "".
func RenderFences(body string, keep func(name string) bool) (string, error) {
	return renderFences(body, keep, nil)
}

func renderFences(body string, keep func(name string) bool, seen func(Fence)) (string, error) {
	if !strings.Contains(body, "<!--") {
		return body, nil
	}
	visible := func(names []string) bool {
		if keep == nil {
			return true
		}
		for _, n := range names {
			if !keep(n) {
				return false
			}
		}
		return true
	}
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	var (
		inBlock   bool
		blockKeep bool
		blockLine int
		dropped   bool // a line was dropped since the last emitted line
	)
	emit := func(line string) {
		if line == "" && dropped && len(out) > 0 && out[len(out)-1] == "" {
			return
		}
		out = append(out, line)
		dropped = false
	}
	for i, line := range lines {
		n := i + 1
		ms := fenceMarker.FindAllStringSubmatchIndex(line, -1)
		if len(ms) == 0 {
			if inBlock && !blockKeep {
				dropped = true
				continue
			}
			emit(line)
			continue
		}
		// A marker alone on its line is a block opener / closer.
		if len(ms) == 1 && strings.TrimSpace(line) == line[ms[0][0]:ms[0][1]] {
			if line[ms[0][2]:ms[0][3]] == "/feature" {
				if !inBlock {
					return "", &FenceError{Line: n, Msg: "closing marker with no open fence"}
				}
				inBlock = false
				dropped = true
				continue
			}
			if inBlock {
				return "", &FenceError{Line: n, Msg: fmt.Sprintf("nested fence: block fence opened at line %d is still open", blockLine)}
			}
			names, err := fenceNames(line[ms[0][4]:ms[0][5]], n)
			if err != nil {
				return "", err
			}
			if seen != nil {
				seen(Fence{Line: n, Names: names, Block: true})
			}
			inBlock, blockLine = true, n
			blockKeep = visible(names)
			dropped = true
			continue
		}
		if inBlock {
			return "", &FenceError{Line: n, Msg: fmt.Sprintf("nested fence: inline marker inside the block fence opened at line %d", blockLine)}
		}
		// Inline: opener / closer pairs on this line.
		var (
			b       strings.Builder
			last    int
			open    = -1 // index into ms of the open inline opener
			keepCur bool
			cut     bool
		)
		for j, m := range ms {
			isClose := line[m[2]:m[3]] == "/feature"
			if !isClose {
				if open >= 0 {
					return "", &FenceError{Line: n, Msg: "nested fence: inline opener inside an open inline fence"}
				}
				names, err := fenceNames(line[m[4]:m[5]], n)
				if err != nil {
					return "", err
				}
				if seen != nil {
					seen(Fence{Line: n, Names: names})
				}
				b.WriteString(line[last:m[0]])
				open, keepCur, last = j, visible(names), m[1]
				continue
			}
			if open < 0 {
				return "", &FenceError{Line: n, Msg: "closing marker with no open fence"}
			}
			if keepCur {
				b.WriteString(line[last:m[0]])
			} else {
				cut = true
			}
			open, last = -1, m[1]
			// A cut between two spaces leaves one.
			if cut && !keepCur && strings.HasSuffix(b.String(), " ") && strings.HasPrefix(line[last:], " ") {
				last++
			}
		}
		if open >= 0 {
			return "", &FenceError{Line: n, Msg: "inline fence not closed on its line (a block fence's markers stand alone on their lines)"}
		}
		b.WriteString(line[last:])
		r := b.String()
		if cut {
			r = strings.TrimRight(r, " \t")
			if isBareLine(r) {
				dropped = true
				continue
			}
		}
		emit(r)
	}
	if inBlock {
		return "", &FenceError{Line: blockLine, Msg: "block fence never closed"}
	}
	return strings.Join(out, "\n"), nil
}

// fenceNames splits an opener's name list; an empty list or entry is
// malformed.
func fenceNames(raw string, line int) ([]string, error) {
	var names []string
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil, &FenceError{Line: line, Msg: fmt.Sprintf("empty feature name in %q", strings.TrimSpace(raw))}
		}
		names = append(names, p)
	}
	return names, nil
}

// isBareLine reports whether a cut line kept nothing but whitespace or a
// list / heading / table marker.
func isBareLine(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" || s == "*" || s == "+" || strings.Trim(s, "#") == "" {
		return true
	}
	if digits := strings.TrimSuffix(s, "."); digits != s && digits != "" && strings.Trim(digits, "0123456789") == "" {
		return true
	}
	return false
}

// Raw returns the named skill exactly as embedded — fence markers and
// all — or a virtual skill's rendered body. Renderers (the instance
// discovery view, the ontology builder) start here; every SERVED body
// goes through RenderFences.
func Raw(name string) (string, bool) {
	data, err := fs.ReadFile(content, name+".md")
	if err != nil {
		return virtualBody(name)
	}
	return string(data), true
}
