package docgen

import (
	"strings"

	"github.com/frankbardon/pulse/internal/skills"
)

// renderIndex renders index.md, the landing page.
func (g *gen) renderIndex() string {
	var b strings.Builder
	b.WriteString("# Analysis Guide\n\n")
	b.WriteString("A reference to the analysis this instance offers, rendered from the guidance metadata Pulse ships with ")
	b.WriteString("every operator: what each one is for, the questions it answers, when to use something else, ")
	b.WriteString("and the terms it relies on.\n")
	if g.inst.Scoped() && len(g.inst.HiddenNames()) > 0 {
		b.WriteString("\nThis instance offers a subset of Pulse's features; the reference covers only what it offers.\n")
	}
	b.WriteString("\n- [Operator catalog](catalog.md): ")
	b.WriteString("one page per operator category.\n")
	b.WriteString("- [Glossary](glossary.md): plain-language definitions of the terms the guidance uses.\n")
	if !g.opts.OmitSkills {
		b.WriteString("- [Skills](skills.md): every skill as an agent reads it.\n")
	}
	return b.String()
}

// renderSummary renders SUMMARY.md: an mdBook summary fragment listing
// every page of the tree, paths relative to the export root.
func renderSummary(cats []category, list []skills.Metadata, omitSkills bool) string {
	var b strings.Builder
	b.WriteString("# Summary\n\n")
	b.WriteString("- [Analysis Guide](index.md)\n")
	b.WriteString("  - [Operator catalog](catalog.md)\n")
	for _, c := range cats {
		b.WriteString("    - [")
		b.WriteString(c.title)
		b.WriteString("](")
		b.WriteString(catalogPath(c.key))
		b.WriteString(")\n")
	}
	b.WriteString("  - [Glossary](glossary.md)\n")
	if !omitSkills {
		b.WriteString("  - [Skills](skills.md)\n")
		for _, md := range list {
			b.WriteString("    - [")
			b.WriteString(md.Name)
			b.WriteString("](")
			b.WriteString(skillPath(md.Name))
			b.WriteString(")\n")
		}
	}
	return b.String()
}
