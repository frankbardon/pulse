package doccheck

import (
	"fmt"
	"sort"
	"strings"
)

// DeniedName is one entry of a removed-name denylist: a spelling served
// text must not use (an identifier, a qualified selector or a phrase) and
// why it is gone.
type DeniedName struct {
	Name   string
	Reason string
}

// ImportAliases maps a public package's qualifier to the import alias the
// docs and examples spell it with: the public io package collides with the
// standard library's, so it is imported as pio.
var ImportAliases = map[string]string{"io": "pio"}

// Spellings returns every spelling the scan matches for d: AliasSpellings
// of its Name.
func (d DeniedName) Spellings() []string { return AliasSpellings(d.Name) }

// AliasSpellings returns name and, when name is a selector `<pkg>.<Name>`
// whose package has an import alias in ImportAliases, the aliased selector
// (`io.InferSchema` also yields `pio.InferSchema`). TestNoRemovedNamesInDocs
// and the doc-coverage matcher (internal/doccoverage) both match through it.
func AliasSpellings(name string) []string {
	out := []string{name}
	pkg, rest, ok := strings.Cut(name, ".")
	if !ok || pkg == "" || rest == "" || strings.ContainsFunc(pkg, func(r rune) bool { return r > 0x7f || !isIdentByte(byte(r)) }) {
		return out
	}
	if alias, ok := ImportAliases[pkg]; ok {
		out = append(out, alias+"."+rest)
	}
	return out
}

// WordCount counts the occurrences of needle in text that are whole words:
// the byte before and the byte after are not identifier characters (ASCII
// letters, digits, '_', or any non-ASCII byte). needle may itself hold
// punctuation and spaces (`io.InferSchema`, `MCP version 1.0.0`); only its
// ends are bounded.
func WordCount(text, needle string) int {
	if needle == "" {
		return 0
	}
	n := 0
	for i := 0; ; {
		j := strings.Index(text[i:], needle)
		if j < 0 {
			return n
		}
		start, end := i+j, i+j+len(needle)
		if (start == 0 || !isIdentByte(text[start-1])) && (end == len(text) || !isIdentByte(text[end])) {
			n++
		}
		i = start + 1
	}
}

func isIdentByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= 0x80
}

// CheckRemovedNames returns one violation per (source, denied name) pair
// where the source's text names one of the denied spellings (Spellings) as
// a whole word; the entry is the denied Name whichever spelling hit. The
// sources map a stable label (a repo path, or "<surface> <name>" for text
// an instance serves) to its text. The result is sorted by path, then
// entry.
func CheckRemovedNames(sources map[string]string, deny []DeniedName) []Violation {
	var out []Violation
	for path, text := range sources {
		for _, d := range deny {
			hit := ""
			for _, sp := range d.Spellings() {
				if WordCount(text, sp) > 0 {
					hit = sp
					break
				}
			}
			if hit == "" {
				continue
			}
			name := fmt.Sprintf("%q", d.Name)
			if hit != d.Name {
				name = fmt.Sprintf("%q (as %q)", d.Name, hit)
			}
			out = append(out, Violation{
				Path:  path,
				Entry: d.Name,
				Message: fmt.Sprintf("served text names the removed %s (%s): rewrite it to today's spelling, "+
					"or drop the mention", name, d.Reason),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Entry < out[j].Entry
	})
	return out
}
