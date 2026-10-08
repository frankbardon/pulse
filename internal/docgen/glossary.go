package docgen

import (
	"sort"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// termAnchor is the anchor of a glossary term on glossary.md.
func termAnchor(id string) string { return "term-" + id }

// termVisible reports whether the instance keeps glossary term id: its
// node survived the prune — the keep-function the virtual glossary
// skill renders with. A term every operator user of which is hidden is
// dropped; an orphan term (no operator cites it) stays.
func (g *gen) termVisible(id string) bool {
	return g.graph.Has(descx.OntologyID(descriptor.OntologyNodeGlossaryTerm, id))
}

// renderGlossary renders glossary.md: every kept term, alphabetical by
// ID, each under its anchor with its definition, why it matters, its
// spellings and its kept related terms. Forms are spellings of a kept
// term and render verbatim (never token-scrubbed); the definition and
// why-care prose pass through the instance scrub.
func (g *gen) renderGlossary() string {
	var terms []descriptor.Term
	for _, t := range descx.Glossary() {
		if g.termVisible(t.ID) {
			terms = append(terms, t)
		}
	}
	sort.Slice(terms, func(i, j int) bool { return terms[i].ID < terms[j].ID })

	var b strings.Builder
	b.WriteString("# Glossary\n\n")
	b.WriteString("Plain-language definitions of the terms the guidance relies on, sorted by ID. ")
	b.WriteString("Catalog entries link here for every term they cite.\n")
	for _, t := range terms {
		b.WriteString("\n<a id=\"")
		b.WriteString(termAnchor(t.ID))
		b.WriteString("\"></a>\n\n## ")
		b.WriteString(t.ID)
		b.WriteString("\n")
		if s := g.text(t.Short); s != "" {
			b.WriteString("\n")
			b.WriteString(s)
			b.WriteString("\n")
		}
		if w := g.text(t.WhyCare); w != "" {
			b.WriteString("\n**Why care:** ")
			b.WriteString(w)
			b.WriteString("\n")
		}
		if len(t.Forms) > 0 {
			b.WriteString("\n**Forms:** ")
			b.WriteString(strings.Join(t.Forms, ", "))
			b.WriteString("\n")
		}
		var see []string
		for _, id := range t.SeeAlso {
			if g.termVisible(id) {
				see = append(see, "[`"+id+"`](#"+termAnchor(id)+")")
			}
		}
		if len(see) > 0 {
			b.WriteString("\n**See also:** ")
			b.WriteString(strings.Join(see, ", "))
			b.WriteString("\n")
		}
	}
	return b.String()
}
