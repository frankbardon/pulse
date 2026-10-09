package docgen

import (
	"sort"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/skills"
)

// noGuidance is the plain-words text of an operator that declares no
// Purpose (an extension registered without one).
const noGuidance = "No guidance provided."

// emptyCell fills a summary-table cell with nothing to say.
const emptyCell = "—"

// gen is one render's view of the instance.
type gen struct {
	inst  *descx.InstanceSnapshot
	graph *descx.OntologyGraph
	disc  *descx.Discovery
	ext   *descx.ExtensionsSnapshot
	scrub descx.ProseScrub
	opts  Options

	// opCategory maps every catalogued operator to its category key —
	// the page a link to it targets.
	opCategory map[string]string
	// skillSet is every skill stem the export writes a page for (empty
	// with OmitSkills).
	skillSet map[string]bool
}

func newGen(inst *descx.InstanceSnapshot, opts Options) *gen {
	g := &gen{
		inst:       inst,
		graph:      inst.Ontology(),
		disc:       inst.Discovery(),
		ext:        inst.Extensions(),
		scrub:      descx.NewProseScrub(inst),
		opts:       opts,
		opCategory: map[string]string{},
		skillSet:   map[string]bool{},
	}
	if !opts.OmitSkills {
		for _, md := range g.visibleSkills() {
			g.skillSet[md.Name] = true
		}
	}
	return g
}

// category is one catalog page: a PurposeSurfaces() category and the
// operators of it the instance offers, sorted by name.
type category struct {
	key   string
	title string
	ops   []entry
}

// entry is one catalogued operator.
type entry struct {
	name string
	// purpose is the operator's Purpose; nil when it declares none.
	purpose *descriptor.Purpose
	// description is an extension operator's manifest description.
	description string
}

// categoryTitles names each PurposeSurfaces() category's page.
var categoryTitles = map[string]string{
	"aggregator":         "Aggregators",
	"attribute":          "Attributes",
	"filterer":           "Filterers",
	"grouper":            "Groupers",
	"window":             "Window operators",
	"feature":            "Feature operators",
	"test":               "Statistical tests",
	"regression":         "Regressions",
	"matrix":             "Matrix operators",
	"overlay":            "Overlays",
	"synth_distribution": "Synth distributions",
}

func categoryTitle(key string) string {
	if t, ok := categoryTitles[key]; ok {
		return t
	}
	return key
}

// extensionLists maps each extension operator list to its catalog
// category.
func extensionLists(ext *descx.ExtensionsSnapshot) map[string][]descriptor.OperatorMeta {
	if ext == nil {
		return nil
	}
	return map[string][]descriptor.OperatorMeta{
		"aggregator":         ext.Aggregators,
		"attribute":          ext.Attributes,
		"filterer":           ext.Filterers,
		"grouper":            ext.Groupers,
		"window":             ext.Windows,
		"feature":            ext.Features,
		"test":               ext.Tests,
		"synth_distribution": ext.SynthDistributions,
		"overlay":            ext.OverlayKinds,
	}
}

func (g *gen) operatorVisible(name string) bool {
	return g.graph.Has(descx.OntologyID(descriptor.OntologyNodeOperator, name))
}

// categories returns the catalog pages, in PurposeSurfaces() order,
// each holding the built-in and extension operators of the category
// whose node survived the instance prune. A category with no visible
// operator has no page.
func (g *gen) categories() []category {
	extLists := extensionLists(g.ext)
	var out []category
	for _, s := range descx.PurposeSurfaces() {
		c := category{key: s.Category, title: categoryTitle(s.Category)}
		seen := map[string]bool{}
		for _, name := range s.Names {
			if seen[name] || !g.operatorVisible(name) {
				continue
			}
			seen[name] = true
			e := entry{name: name}
			if p, ok := descx.PurposeOf(name); ok {
				e.purpose = &p
			}
			c.ops = append(c.ops, e)
		}
		for _, m := range extLists[s.Category] {
			if seen[m.Name] || !g.operatorVisible(m.Name) {
				continue
			}
			seen[m.Name] = true
			e := entry{name: m.Name, description: m.Description}
			if p, ok := g.ext.PurposeOf(m.Name); ok {
				e.purpose = &p
			}
			c.ops = append(c.ops, e)
		}
		if len(c.ops) == 0 {
			continue
		}
		sort.Slice(c.ops, func(i, j int) bool { return c.ops[i].name < c.ops[j].name })
		for _, e := range c.ops {
			g.opCategory[e.name] = c.key
		}
		out = append(out, c)
	}
	return out
}

// operatorAnchor is the anchor of an operator's detail block on its
// catalog page.
func operatorAnchor(name string) string { return "op-" + strings.ToLower(name) }

// operatorLink links name from the catalog page of category from: its
// detail block (on this page or a sibling page) when it is catalogued,
// else plain code.
func (g *gen) operatorLink(name, from string) string {
	code := "`" + name + "`"
	cat, ok := g.opCategory[name]
	if !ok {
		return code
	}
	target := "#" + operatorAnchor(name)
	if cat != from {
		target = cat + ".md" + target
	}
	return "[" + code + "](" + target + ")"
}

// operatorSkill returns the stem of the visible skill documenting
// operator name; "" when none is exported.
func (g *gen) operatorSkill(name string) string {
	var stems []string
	for _, e := range g.graph.Out(descx.OntologyID(descriptor.OntologyNodeOperator, name), descriptor.OntologyEdgeDocumentedBy) {
		n, ok := g.graph.Node(e.To)
		if ok && n.Kind == descriptor.OntologyNodeSkill && g.skillSet[n.Name] {
			stems = append(stems, n.Name)
		}
	}
	if len(stems) == 0 {
		return ""
	}
	sort.Strings(stems)
	return stems[0]
}

// text scrubs one prose string for the instance and trims it.
func (g *gen) text(s string) string { return strings.TrimSpace(g.scrub.Text(s)) }

// alternative is one rendered not-for item.
type alternative struct {
	use  string
	when string
}

// alternatives returns p's not-for list for the instance: an
// alternative whose target the prune removed, or whose situation the
// scrub empties, is dropped.
func (g *gen) alternatives(p *descriptor.Purpose) []alternative {
	return g.visibleAlternatives(p.NotFor)
}

// followUps returns p's follow-up list for the instance, filtered like
// alternatives.
func (g *gen) followUps(p *descriptor.Purpose) []alternative {
	return g.visibleAlternatives(p.FollowUps)
}

func (g *gen) visibleAlternatives(in []descriptor.Alternative) []alternative {
	var out []alternative
	for _, a := range in {
		use := strings.TrimSpace(a.Use)
		if use == "" || g.text(use) == "" {
			continue
		}
		use = strings.TrimPrefix(use, string(descriptor.OntologyNodeOperator)+":")
		if !g.graph.Has(descx.FenceFeatureID(use)) {
			continue
		}
		when := g.text(a.When)
		if when == "" {
			continue
		}
		out = append(out, alternative{use: use, when: when})
	}
	return out
}

// questions returns p's questions the scrub leaves.
func (g *gen) questions(p *descriptor.Purpose) []string {
	var out []string
	for _, q := range p.Questions {
		if q = g.text(q); q != "" {
			out = append(out, q)
		}
	}
	return out
}

// glossaryTerms returns p's glossary term IDs the instance keeps.
func (g *gen) glossaryTerms(p *descriptor.Purpose) []string {
	var out []string
	for _, id := range p.Glossary {
		if g.termVisible(id) {
			out = append(out, id)
		}
	}
	return out
}

// plain returns e's plain-words line: the scrubbed Purpose.Plain, or
// "No guidance provided." plus the description when there is none.
func (g *gen) plain(e entry) string {
	if e.purpose != nil {
		if p := g.text(e.purpose.Plain); p != "" {
			return p
		}
	}
	if d := g.text(e.description); d != "" {
		return noGuidance + " " + d
	}
	return noGuidance
}

// cell makes s safe inside a Markdown table cell.
func cell(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	s = strings.ReplaceAll(s, "|", `\|`)
	if s == "" {
		return emptyCell
	}
	return s
}

// renderCategory renders one catalog page: the summary table, then a
// detail block per operator under its anchor.
func (g *gen) renderCategory(c category) string {
	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(c.title)
	b.WriteString("\n\n")
	b.WriteString("The ")
	b.WriteString(strings.ToLower(c.title))
	b.WriteString(" this instance offers: what each is for, the questions it answers and when to reach for something else. ")
	b.WriteString("Operators are sorted by name; each name links to its detail block below.\n\n")
	b.WriteString("| Operator | In plain words | Answers questions like | Level | Instead, when… |\n")
	b.WriteString("|---|---|---|---|---|\n")
	for _, e := range c.ops {
		var q, level, instead string
		if e.purpose != nil {
			if qs := g.questions(e.purpose); len(qs) > 0 {
				q = qs[0]
			}
			level = string(e.purpose.Level)
			var alts []string
			for _, a := range g.alternatives(e.purpose) {
				alts = append(alts, g.operatorLink(a.use, c.key)+" when "+sentenceEnd(a.when))
			}
			instead = strings.Join(alts, "<br>")
		}
		b.WriteString("| [`")
		b.WriteString(e.name)
		b.WriteString("`](#")
		b.WriteString(operatorAnchor(e.name))
		b.WriteString(") | ")
		b.WriteString(cell(g.plain(e)))
		b.WriteString(" | ")
		b.WriteString(cell(q))
		b.WriteString(" | ")
		b.WriteString(cell(level))
		b.WriteString(" | ")
		b.WriteString(cell(instead))
		b.WriteString(" |\n")
	}
	b.WriteString("\n## Operators\n")
	for _, e := range c.ops {
		b.WriteString("\n")
		g.renderDetail(&b, e, c.key)
	}
	return b.String()
}

// domainOrder is the order use cases render in; a domain outside it
// follows, sorted.
var domainOrder = []descriptor.Domain{
	descriptor.DomainSurvey, descriptor.DomainOps, descriptor.DomainScience, descriptor.DomainHarness,
}

func useCaseDomains(m map[descriptor.Domain]string) []descriptor.Domain {
	var out []descriptor.Domain
	known := map[descriptor.Domain]bool{}
	for _, d := range domainOrder {
		known[d] = true
		if _, ok := m[d]; ok {
			out = append(out, d)
		}
	}
	var rest []descriptor.Domain
	for d := range m {
		if !known[d] {
			rest = append(rest, d)
		}
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i] < rest[j] })
	return append(out, rest...)
}

// renderDetail renders e's detail block on the page of category from.
func (g *gen) renderDetail(b *strings.Builder, e entry, from string) {
	b.WriteString(`<a id="`)
	b.WriteString(operatorAnchor(e.name))
	b.WriteString(`"></a>`)
	b.WriteString("\n\n### `")
	b.WriteString(e.name)
	b.WriteString("`\n\n")
	b.WriteString(g.plain(e))
	b.WriteString("\n")
	if p := e.purpose; p != nil {
		if p.Level != "" {
			b.WriteString("\n**Level:** ")
			b.WriteString(string(p.Level))
			b.WriteString("\n")
		}
		writeList(b, "Questions it answers", g.questions(p))
		var cases []string
		for _, d := range useCaseDomains(p.UseCases) {
			if t := g.text(p.UseCases[d]); t != "" {
				cases = append(cases, "*"+string(d)+":* "+t)
			}
		}
		writeList(b, "Use cases by domain", cases)
		var assumptions []string
		for _, a := range p.Assumptions {
			if a = g.text(a); a != "" {
				assumptions = append(assumptions, a)
			}
		}
		writeList(b, "Assumptions", assumptions)
		var alts []string
		for _, a := range g.alternatives(p) {
			alts = append(alts, g.operatorLink(a.use, from)+" when "+sentenceEnd(a.when))
		}
		writeList(b, "Use something else", alts)
		var next []string
		for _, a := range g.followUps(p) {
			next = append(next, g.operatorLink(a.use, from)+" when "+sentenceEnd(a.when))
		}
		writeList(b, "Follow up with", next)
		var terms []string
		for _, id := range g.glossaryTerms(p) {
			terms = append(terms, "[`"+id+"`](../glossary.md#"+termAnchor(id)+")")
		}
		if len(terms) > 0 {
			b.WriteString("\n**Glossary:** ")
			b.WriteString(strings.Join(terms, ", "))
			b.WriteString("\n")
		}
	}
	if stem := g.operatorSkill(e.name); stem != "" {
		b.WriteString("\n**Skill:** [`")
		b.WriteString(stem)
		b.WriteString("`](../")
		b.WriteString(skillPath(stem))
		b.WriteString(")\n")
	}
}

// writeList renders a bold-labelled bullet list; nothing when items is
// empty.
func writeList(b *strings.Builder, label string, items []string) {
	if len(items) == 0 {
		return
	}
	b.WriteString("\n**")
	b.WriteString(label)
	b.WriteString(":**\n\n")
	for _, it := range items {
		b.WriteString("- ")
		b.WriteString(strings.ReplaceAll(it, "\n", " "))
		b.WriteString("\n")
	}
}

// sentenceEnd terminates s with a period unless it already ends a
// sentence.
func sentenceEnd(s string) string {
	if strings.HasSuffix(s, ".") || strings.HasSuffix(s, "?") || strings.HasSuffix(s, "!") {
		return s
	}
	return s + "."
}

// renderCatalogIndex renders catalog.md: one line per category page.
func renderCatalogIndex(cats []category) string {
	var b strings.Builder
	b.WriteString("# Operator catalog\n\n")
	b.WriteString("The operators this instance offers, one page per category. ")
	b.WriteString("Each page opens with a summary table and then a detail block per operator.\n\n")
	for _, c := range cats {
		b.WriteString("- [")
		b.WriteString(c.title)
		b.WriteString("](")
		b.WriteString(catalogPath(c.key))
		b.WriteString(") (")
		b.WriteString(strconv.Itoa(len(c.ops)))
		b.WriteString(")\n")
	}
	return b.String()
}

// visibleSkills returns the metadata of every skill the instance
// serves, sorted by name.
func (g *gen) visibleSkills() []skills.Metadata {
	return g.disc.Skills()
}
