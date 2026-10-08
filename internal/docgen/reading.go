package docgen

import (
	_ "embed"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	descx "github.com/frankbardon/pulse/internal/descriptor"
)

//go:embed components_intro.md
var componentsIntro string

// readingFamily is one "Reading your results" page: the catalog
// categories whose operators' Interpretation entries it renders.
type readingFamily struct {
	key   string
	title string
	intro string
	cats  []string
}

// readingFamilies are the Interpretation pages, in render order. The
// first page citing a shared rule set (the Test page, whenever the
// instance offers a test) renders that rule once at its top; every
// other citation links there.
var readingFamilies = []readingFamily{
	{key: "test", title: "Reading test results", cats: []string{"test"},
		intro: "How to read each output field of the statistical tests this instance offers."},
	{key: "regression", title: "Reading regression results", cats: []string{"regression"},
		intro: "How to read each output field of the regressions this instance offers."},
	{key: "matrix", title: "Reading matrix results", cats: []string{"matrix"},
		intro: "How to read the standardised results of the matrix operators this instance offers."},
	{key: "overlay", title: "Reading overlay results", cats: []string{"overlay"},
		intro: "How to read the figures each overlay kind this instance offers lays over its host."},
	{key: "descriptive", title: "Reading descriptive results",
		cats:  []string{"aggregator", "attribute", "filterer", "grouper", "window", "feature", "synth_distribution"},
		intro: "How to read the descriptive results whose number a name alone does not explain: standardised, scale-free, model-based, transformed or relative figures."},
}

// componentsPage is the Components reading page's path.
const componentsPage = "reading/components.md"

// Preserve markers. A span between a begin and an end marker is
// hand-written prose, not generated text: Render copies it verbatim
// from its embedded source, so regeneration reproduces it byte for
// byte. The only span today is the Components intro, whose source of
// truth is components_intro.md in this package (embedded, so an export
// outside the repository carries it too) — edit that file, never the
// span in a rendered page.
const (
	preserveBeginPrefix = "<!-- docgen:preserve begin "
	preserveEndPrefix   = "<!-- docgen:preserve end "
	preserveSuffix      = " -->"

	componentsIntroID = "components-intro"
)

func readingPath(key string) string { return path.Join("reading", key+".md") }

// readingEntry is one operator on a reading page with its rendered
// field blocks.
type readingEntry struct {
	entry
	category string
	fields   []descriptor.Interpretation
}

// readingSection groups a page's entries by catalog category.
type readingSection struct {
	category string
	entries  []readingEntry
}

// readingPage is one rendered Interpretation page.
type readingPage struct {
	fam      readingFamily
	sections []readingSection
	// shared lists the shared rule sets this page is home to, sorted.
	shared []string
}

// interpretationsOf returns name's Interpretations: the built-in
// registry's, else the extension registration's.
func (g *gen) interpretationsOf(name string) []descriptor.Interpretation {
	if ins, ok := descx.InterpretationsOf(name); ok {
		return ins
	}
	if g.ext != nil {
		return g.ext.Interpretations[name]
	}
	return nil
}

// fieldVisible reports whether an Interpretation's field survives the
// instance: a field path naming a hidden token (p_adjusted without
// capability:multiplicity) is dropped whole.
func (g *gen) fieldVisible(in descriptor.Interpretation) bool {
	return strings.TrimSpace(in.Field) != "" && g.text(in.Field) != ""
}

// readingPages returns the Interpretation pages the instance has
// content for, in readingFamilies order, and the home page of every
// cited shared rule set. A page with no visible operator carrying a
// visible Interpretation is omitted.
func (g *gen) readingPages(cats []category) ([]readingPage, map[string]string) {
	byKey := map[string]category{}
	for _, c := range cats {
		byKey[c.key] = c
	}
	homes := map[string]string{}
	var pages []readingPage
	for _, fam := range readingFamilies {
		p := readingPage{fam: fam}
		for _, ck := range fam.cats {
			c, ok := byKey[ck]
			if !ok {
				continue
			}
			sec := readingSection{category: ck}
			for _, e := range c.ops {
				var fields []descriptor.Interpretation
				for _, in := range g.interpretationsOf(e.name) {
					if g.fieldVisible(in) && g.fieldHasContent(in) {
						fields = append(fields, in)
					}
				}
				if len(fields) > 0 {
					sec.entries = append(sec.entries, readingEntry{entry: e, category: ck, fields: fields})
				}
			}
			if len(sec.entries) > 0 {
				p.sections = append(p.sections, sec)
			}
		}
		if len(p.sections) == 0 {
			continue
		}
		page := readingPath(fam.key)
		for _, s := range p.sections {
			for _, e := range s.entries {
				for _, in := range e.fields {
					if in.Shared == "" {
						continue
					}
					if _, ok := descx.SharedInterpretation(in.Shared); !ok {
						continue
					}
					if _, ok := homes[in.Shared]; !ok {
						homes[in.Shared] = page
						p.shared = append(p.shared, in.Shared)
					}
				}
			}
		}
		sort.Strings(p.shared)
		pages = append(pages, p)
	}
	return pages, homes
}

// fieldHasContent reports whether an Interpretation renders anything
// on the instance: prose the scrub leaves, or a known shared rule set.
func (g *gen) fieldHasContent(in descriptor.Interpretation) bool {
	if _, ok := descx.SharedInterpretation(in.Shared); ok && in.Shared != "" {
		return true
	}
	return g.renderField(in, "", nil) != ""
}

// sharedAnchor is the anchor of a shared rule set on its home page.
func sharedAnchor(key string) string { return "shared-" + key }

func sharedTitle(key string) string {
	if key == descx.SharedPValue {
		return "Reading a p-value"
	}
	return "The shared `" + key + "` reading"
}

// renderReadingPage renders one Interpretation page.
func (g *gen) renderReadingPage(p readingPage, homes map[string]string) string {
	page := readingPath(p.fam.key)
	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(p.fam.title)
	b.WriteString("\n\n")
	b.WriteString(p.fam.intro)
	b.WriteString(" Each operator lists its output fields: what the value means, the labelled bands of a published convention where one applies, what its sign says, and the caveats to keep in mind.\n")
	for _, key := range p.shared {
		in, _ := descx.SharedInterpretation(key)
		b.WriteString("\n<a id=\"")
		b.WriteString(sharedAnchor(key))
		b.WriteString("\"></a>\n\n## ")
		b.WriteString(sharedTitle(key))
		b.WriteString("\n\nEvery field below marked as following this reading is read the same way.\n")
		b.WriteString(g.renderField(in, page, homes))
	}
	for _, s := range p.sections {
		b.WriteString("\n## ")
		b.WriteString(categoryTitle(s.category))
		b.WriteString("\n")
		for _, e := range s.entries {
			b.WriteString("\n<a id=\"")
			b.WriteString(operatorAnchor(e.name))
			b.WriteString("\"></a>\n\n### `")
			b.WriteString(e.name)
			b.WriteString("`\n\n")
			b.WriteString(g.plain(e.entry))
			if ptr := g.catalogPointer(e.name, "../"); ptr != "" {
				b.WriteString(" ")
				b.WriteString(ptr)
			}
			b.WriteString("\n")
			for _, in := range e.fields {
				b.WriteString("\n#### `")
				b.WriteString(in.Field)
				b.WriteString("`\n")
				b.WriteString(g.renderField(in, page, homes))
			}
		}
	}
	return b.String()
}

// renderField renders the body of one Interpretation (no heading): its
// meaning, bands, sign, caveats and shared-rule pointer, every prose
// string scrubbed for the instance. "" when nothing survives. A nil
// homes renders no shared pointer (used to probe for content).
func (g *gen) renderField(in descriptor.Interpretation, from string, homes map[string]string) string {
	var b strings.Builder
	if m := g.text(in.Means); m != "" {
		b.WriteString("\n")
		b.WriteString(strings.ReplaceAll(m, "\n", " "))
		b.WriteString("\n")
	}
	if len(in.Bands) > 0 {
		b.WriteString("\n**Bands** (")
		if c := g.text(in.Convention); c != "" {
			b.WriteString("convention: ")
			b.WriteString(c)
			b.WriteString("; ")
		}
		b.WriteString("a labelled convention, not a rule):\n\n")
		if in.Abs {
			b.WriteString("| Absolute value | Label |\n")
		} else {
			b.WriteString("| Value | Label |\n")
		}
		b.WriteString("|---|---|\n")
		for _, band := range in.Bands {
			b.WriteString("| ")
			b.WriteString(bandRange(band))
			b.WriteString(" | ")
			b.WriteString(cell(band.Label))
			b.WriteString(" |\n")
		}
	}
	var signs []string
	for _, k := range signKeys(in.Sign) {
		if t := g.text(in.Sign[k]); t != "" {
			signs = append(signs, "`"+k+"`: "+t)
		}
	}
	writeList(&b, "Sign", signs)
	var caveats []string
	for _, c := range in.Caveats {
		if c = g.text(c); c != "" {
			caveats = append(caveats, c)
		}
	}
	writeList(&b, "Caveats", caveats)
	if home, ok := homes[in.Shared]; ok && in.Shared != "" {
		target := "#" + sharedAnchor(in.Shared)
		if home != from {
			// Reading pages are siblings under reading/.
			target = path.Base(home) + target
		}
		b.WriteString("\nFollows the shared reading: see [")
		b.WriteString(sharedTitle(in.Shared))
		b.WriteString("](")
		b.WriteString(target)
		b.WriteString(").\n")
	}
	return b.String()
}

// signKeys returns the sign map's keys: "+" then "-", then any other
// key sorted.
func signKeys(m map[string]string) []string {
	var out, rest []string
	for _, k := range []string{"+", "-"} {
		if _, ok := m[k]; ok {
			out = append(out, k)
		}
	}
	for k := range m {
		if k != "+" && k != "-" {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

func formatBound(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

// bandRange renders a band's range: Min inclusive, Max exclusive.
func bandRange(b descriptor.Band) string {
	switch {
	case b.Min == nil && b.Max == nil:
		return "any value"
	case b.Min == nil:
		return "below " + formatBound(*b.Max)
	case b.Max == nil:
		return formatBound(*b.Min) + " and above"
	default:
		return "from " + formatBound(*b.Min) + " to below " + formatBound(*b.Max)
	}
}

// renderReadingIndex renders reading.md: one line per reading page.
func renderReadingIndex(pages []readingPage) string {
	var b strings.Builder
	b.WriteString("# Reading your results\n\n")
	b.WriteString("How to read what the operators this instance offers return, one page per result family, ")
	b.WriteString("plus the counts that ride beside every result.\n\n")
	for _, p := range pages {
		b.WriteString("- [")
		b.WriteString(p.fam.title)
		b.WriteString("](")
		b.WriteString(readingPath(p.fam.key))
		b.WriteString(")\n")
	}
	b.WriteString("- [Reading the components](")
	b.WriteString(componentsPage)
	b.WriteString(")\n")
	return b.String()
}

// componentSlot is one Response.Components slot on the Components page.
type componentSlot struct {
	title   string
	floor   []descriptor.ComponentKey
	schemas map[string]descriptor.ComponentSchema
}

// componentSlots pairs each ComponentFloors() slot with the instance's
// schemas for it (the instance manifest's ComponentsSchemas, already
// filtered and scrubbed for the instance).
func (g *gen) componentSlots() []componentSlot {
	block := descx.BuildManifestForInstance(g.inst).ComponentsSchemas
	bySlot := map[string]struct {
		title   string
		schemas map[string]descriptor.ComponentSchema
	}{
		"aggregations": {"Aggregators", block.Aggregators},
		"groupers":     {"Groupers", block.Groupers},
		"filterers":    {"Filterers", block.Filterers},
		"matrices":     {"Matrix operators", block.Matrices},
	}
	var out []componentSlot
	for _, f := range descx.ComponentFloors() {
		s, ok := bySlot[f.Slot]
		if !ok || len(s.schemas) == 0 {
			continue
		}
		out = append(out, componentSlot{title: s.title, floor: f.Keys, schemas: s.schemas})
	}
	return out
}

// visibleKeys returns keys the instance keeps (a key whose name or
// whole description the scrub empties is dropped), with scrubbed
// descriptions, minus the names in skip.
func (g *gen) visibleKeys(keys []descriptor.ComponentKey, skip map[string]bool) []descriptor.ComponentKey {
	var out []descriptor.ComponentKey
	for _, k := range keys {
		if skip[k.Name] || g.text(k.Name) == "" {
			continue
		}
		k.Description = g.text(k.Description)
		out = append(out, k)
	}
	return out
}

func writeKeyTable(b *strings.Builder, keys []descriptor.ComponentKey) {
	b.WriteString("\n| Key | Type | Emitted | Meaning |\n|---|---|---|---|\n")
	for _, k := range keys {
		b.WriteString("| `")
		b.WriteString(k.Name)
		b.WriteString("` | ")
		b.WriteString(cell(k.Type))
		b.WriteString(" | ")
		if k.Optional {
			b.WriteString("when its condition holds")
		} else {
			b.WriteString("always")
		}
		b.WriteString(" | ")
		b.WriteString(cell(k.Description))
		b.WriteString(" |\n")
	}
}

// mergeabilityText explains each mergeability class.
var mergeabilityText = []struct {
	class descriptor.ComponentsMergeability
	text  string
}{
	{descriptor.Mergeable, "the components fold chunk by chunk, so a streamed run reports running values as it goes."},
	{descriptor.Partial, "the components fold across chunks, but the fold grows with the data (a set or map union), so Pulse may stage it until the end of the run."},
	{descriptor.None, "the components need the whole input at once (a sorted view), so a streamed run reports them only at the end."},
}

// renderComponents renders reading/components.md: the preserved intro,
// the floor of every slot, the weighted floor keys, the mergeability
// classes and each visible operator's own keys.
func (g *gen) renderComponents() string {
	var b strings.Builder
	b.WriteString("# Reading the components\n\n")
	b.WriteString(preserveBeginPrefix + componentsIntroID + preserveSuffix + "\n")
	b.WriteString(componentsIntro)
	if !strings.HasSuffix(componentsIntro, "\n") {
		b.WriteString("\n")
	}
	b.WriteString(preserveEndPrefix + componentsIntroID + preserveSuffix + "\n")

	slots := g.componentSlots()
	b.WriteString("\n<a id=\"floor\"></a>\n\n## The floor every entry carries\n\n")
	b.WriteString("Pulse fills these keys on every entry of a slot, ahead of the operator's own keys.\n")
	for _, s := range slots {
		keys := g.visibleKeys(s.floor, nil)
		if len(keys) == 0 {
			continue
		}
		b.WriteString("\n### ")
		b.WriteString(s.title)
		b.WriteString("\n")
		writeKeyTable(&b, keys)
	}

	weighted := g.inst.Enabled(descx.FeatureWeighting)
	if weighted {
		b.WriteString("\n<a id=\"weighted-floor\"></a>\n\n## Weighted floor keys\n\n")
		b.WriteString("A slot run under a row weight adds these keys beside its floor; their absence means the slot was unweighted. ")
		b.WriteString("`n` and `n_null` stay raw row counts under a weight.\n")
		writeKeyTable(&b, g.visibleKeys(descx.WeightFloorKeys(), nil))
	}

	b.WriteString("\n<a id=\"mergeability\"></a>\n\n## Mergeability\n\n")
	b.WriteString("Each operator's components carry a mergeability class, which says when a streamed run can report them:\n\n")
	for _, m := range mergeabilityText {
		b.WriteString("- **")
		b.WriteString(string(m.class))
		b.WriteString("**: ")
		b.WriteString(m.text)
		b.WriteString("\n")
	}

	b.WriteString("\n## Operator components\n\n")
	b.WriteString("Each operator's own keys, beyond the floor")
	if weighted {
		b.WriteString(" and the weighted floor keys")
	}
	b.WriteString(".\n")
	weightSkip := map[string]bool{}
	if weighted {
		for _, k := range descx.WeightFloorKeys() {
			weightSkip[k.Name] = true
		}
	}
	for _, s := range slots {
		skip := map[string]bool{}
		for _, k := range s.floor {
			skip[k.Name] = true
		}
		for n := range weightSkip {
			skip[n] = true
		}
		names := make([]string, 0, len(s.schemas))
		for n := range s.schemas {
			names = append(names, n)
		}
		sort.Strings(names)
		b.WriteString("\n### ")
		b.WriteString(s.title)
		b.WriteString("\n")
		for _, name := range names {
			schema := s.schemas[name]
			b.WriteString("\n<a id=\"")
			b.WriteString(operatorAnchor(name))
			b.WriteString("\"></a>\n\n#### `")
			b.WriteString(name)
			b.WriteString("`\n\n**Mergeability:** ")
			b.WriteString(string(schema.Mergeability))
			b.WriteString(". ")
			b.WriteString(g.catalogPointer(name, "../"))
			b.WriteString("\n")
			keys := g.visibleKeys(schema.Keys, skip)
			if len(keys) == 0 {
				b.WriteString("\nOnly the floor keys.\n")
				continue
			}
			writeKeyTable(&b, keys)
		}
	}
	return b.String()
}

// catalogPointer links name's catalog entry from a page prefix levels
// below the export root; "" when name is not catalogued.
func (g *gen) catalogPointer(name, prefix string) string {
	cat, ok := g.opCategory[name]
	if !ok {
		return ""
	}
	return "See its [catalog entry](" + prefix + catalogPath(cat) + "#" + operatorAnchor(name) + ")."
}
