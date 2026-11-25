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
// where the source's text names the denied spelling as a whole word. The
// sources map a stable label (a repo path, or "<surface> <name>" for text
// an instance serves) to its text. The result is sorted by path, then
// entry.
func CheckRemovedNames(sources map[string]string, deny []DeniedName) []Violation {
	var out []Violation
	for path, text := range sources {
		for _, d := range deny {
			if WordCount(text, d.Name) == 0 {
				continue
			}
			out = append(out, Violation{
				Path:  path,
				Entry: d.Name,
				Message: fmt.Sprintf("served text names the removed %q (%s): rewrite it to today's spelling, "+
					"or drop the mention", d.Name, d.Reason),
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
