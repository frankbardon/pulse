package pulse

import (
	"strings"
)

// exportSlotExempt reports whether line of the exported file path is
// exempt from the slot-token check: a glossary term's forms line — the
// glossary page's `**Forms:**`, the glossary skill's `Written as:` —
// lists the term's own spellings verbatim ("transition matrices" for
// stochastic-matrix). A term is kept or dropped by its owning
// operators, never token-scrubbed; hidden operator and tool names are
// still checked on these lines.
func exportSlotExempt(path, line string) bool {
	l := strings.TrimSpace(line)
	switch path {
	case "glossary.md":
		return strings.HasPrefix(l, "**Forms:**")
	case "skills/glossary.md":
		return strings.HasPrefix(l, "Written as:")
	}
	return false
}
