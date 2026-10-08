package descriptor

import (
	"sort"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/skills"
)

// Generated skill sections (U21, E1-S1).
//
// An atomic skill may carry the marker lines
// `<!-- generated: use-when -->` and `<!-- generated: reading-the-output -->`
// (internal/skills/generated.go). Every rendered read replaces them with
// a heading plus a body built HERE from the documented operator's
// guidance metadata — never stored in the file:
//
//   - `## Use when` (RenderUseWhenSection): the Purpose's plain line, up
//     to two questions and the not-for list, each alternative naming the
//     operator or feature to use instead;
//   - `## Reading the output` (RenderReadingSection): per Interpretation
//     field its meaning, then its bands (the convention named), sign and
//     caveats; a field citing the shared p-value rule set gets a one-line
//     summary of it.
//
// Sources: the built-in Purpose / Interpretation registries, then the
// instance's extension registrations (ExtensionsSnapshot.PurposeOf /
// .Interpretations). On a feature-profiled instance a not-for
// alternative whose target the pruned ontology lacks is dropped (the
// whole not-for list when every alternative is hidden) and every
// remaining prose string goes through the instance ProseScrub — so the
// rendered text is built from pruned data and names nothing the
// instance hides. The profile-free render (skills.Get) uses the full
// registries through the renderer registered at init.
//
// Each section is capped (heading included, measured on the full-set
// render): UseWhenSectionCap and ReadingSectionCap. Overflow never
// fails: the plain line always stays, and the other items (questions,
// alternatives; reading fields, then their bands, signs and caveats)
// are added in order while they fit — an item that does not fit is
// skipped and a later, shorter one may still be added.

// Hard caps on a generated section, in bytes (heading included).
const (
	UseWhenSectionCap = 450
	ReadingSectionCap = 600
)

// useWhenQuestions is how many Purpose.Questions `## Use when` carries.
const useWhenQuestions = 2

// pValueBrief is the one-line summary of the shared p-value rule set a
// reading field citing it renders (the full rule set lives in the
// Interpretation registry and the glossary).
const pValueBrief = "a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; " +
	"that is not the same as important, so read the effect size for how big it is."

func init() {
	skills.RegisterSectionRenderer(func(operator, section string) string {
		return RenderGuidanceSection(operator, section, nil, nil, ProseScrub{})
	})
}

// RenderGuidanceSection renders one generated section for operator:
// its Purpose (built-in, else ext's) for skills.SectionUseWhen, its
// Interpretation entries for skills.SectionReadingTheOutput. keepUse
// reports whether a not-for target (bare operator name or
// `<kind>:<name>` feature spelling) is visible; nil keeps every target.
// "" when the operator declares no metadata for the section or the
// section is unknown.
func RenderGuidanceSection(operator, section string, ext *ExtensionsSnapshot, keepUse func(use string) bool, scrub ProseScrub) string {
	switch section {
	case skills.SectionUseWhen:
		p, ok := operatorPurpose(operator, ext)
		if !ok {
			return ""
		}
		return RenderUseWhenSection(p, keepUse, scrub)
	case skills.SectionReadingTheOutput:
		ins, ok := operatorInterpretations(operator, ext)
		if !ok {
			return ""
		}
		return RenderReadingSection(ins, scrub)
	}
	return ""
}

// operatorPurpose resolves an operator's Purpose: the built-in registry
// first, then the instance's extension registrations.
func operatorPurpose(op string, ext *ExtensionsSnapshot) (descriptor.Purpose, bool) {
	if p, ok := PurposeOf(op); ok {
		return p, true
	}
	return ext.PurposeOf(op)
}

// operatorInterpretations resolves an operator's Interpretation entries
// the same way; an empty declaration counts as none.
func operatorInterpretations(op string, ext *ExtensionsSnapshot) ([]descriptor.Interpretation, bool) {
	if ins, ok := InterpretationsOf(op); ok && len(ins) > 0 {
		return ins, true
	}
	if ext != nil {
		if ins := ext.Interpretations[op]; len(ins) > 0 {
			return ins, true
		}
	}
	return nil, false
}

// useWhen is a `## Use when` section under assembly.
type useWhen struct {
	plain string
	qs    []string
	alts  []string
}

func (u useWhen) render() string {
	var b strings.Builder
	b.WriteString("## Use when\n")
	if u.plain != "" {
		b.WriteString("\n")
		b.WriteString(u.plain)
		b.WriteString("\n")
	}
	if len(u.qs) > 0 {
		b.WriteString("\nQuestions it answers:\n\n")
		for _, q := range u.qs {
			b.WriteString("- ")
			b.WriteString(q)
			b.WriteString("\n")
		}
	}
	if len(u.alts) > 0 {
		b.WriteString("\nUse something else:\n\n")
		for _, a := range u.alts {
			b.WriteString("- ")
			b.WriteString(a)
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// RenderUseWhenSection renders `## Use when` from p: the plain line, up
// to two questions, then each not-for alternative keepUse admits (nil
// admits all), every string through scrub. Capped at UseWhenSectionCap;
// "" when nothing survives.
func RenderUseWhenSection(p descriptor.Purpose, keepUse func(use string) bool, scrub ProseScrub) string {
	u := useWhen{plain: strings.TrimSpace(scrub.Text(p.Plain))}
	var cands []func(*useWhen) func()
	for i, q := range p.Questions {
		if i == useWhenQuestions {
			break
		}
		if q = strings.TrimSpace(scrub.Text(q)); q != "" {
			cands = append(cands, func(u *useWhen) func() {
				u.qs = append(u.qs, q)
				return func() { u.qs = u.qs[:len(u.qs)-1] }
			})
		}
	}
	for _, a := range p.NotFor {
		use := strings.TrimSpace(a.Use)
		if use == "" || (keepUse != nil && !keepUse(use)) || mentionsHidden(use, scrub.hidden) {
			continue
		}
		when := strings.TrimSpace(scrub.Text(a.When))
		if when == "" {
			continue
		}
		item := "`" + use + "` when " + sentenceEnd(when)
		cands = append(cands, func(u *useWhen) func() {
			u.alts = append(u.alts, item)
			return func() { u.alts = u.alts[:len(u.alts)-1] }
		})
	}
	if u.plain == "" && len(cands) == 0 {
		return ""
	}
	for _, add := range cands {
		undo := add(&u)
		if len(u.render()) > UseWhenSectionCap {
			undo()
		}
	}
	if u.plain == "" && len(u.qs) == 0 && len(u.alts) == 0 {
		return ""
	}
	return u.render()
}

// readingField is one Interpretation field under assembly: its base
// line and the extras (bands, sign, caveats) admitted so far.
type readingField struct {
	base    string
	bands   string
	sign    string
	caveats []string

	kept     bool
	keepBand bool
	keepSign bool
	keepCav  []bool
}

func renderReading(fields []*readingField) string {
	var b strings.Builder
	b.WriteString("## Reading the output\n\n")
	for _, f := range fields {
		if !f.kept {
			continue
		}
		b.WriteString("- ")
		b.WriteString(f.base)
		b.WriteString("\n")
		if f.keepBand {
			b.WriteString("  - ")
			b.WriteString(f.bands)
			b.WriteString("\n")
		}
		if f.keepSign {
			b.WriteString("  - ")
			b.WriteString(f.sign)
			b.WriteString("\n")
		}
		for i, c := range f.caveats {
			if f.keepCav[i] {
				b.WriteString("  - Caveat: ")
				b.WriteString(c)
				b.WriteString("\n")
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// RenderReadingSection renders `## Reading the output` from ins: one
// item per field (its meaning, or the shared rule set's summary), with
// the field's bands (convention named), sign and caveats as sub-items,
// every string through scrub. Capped at ReadingSectionCap — fields are
// admitted first, in declaration order, then bands, signs and caveats;
// "" when nothing survives.
func RenderReadingSection(ins []descriptor.Interpretation, scrub ProseScrub) string {
	var fields []*readingField
	for _, in := range ins {
		means := strings.TrimSpace(scrub.Text(in.Means))
		if means == "" && in.Shared != "" {
			if in.Shared == SharedPValue {
				means = pValueBrief
			} else if sh, ok := SharedInterpretation(in.Shared); ok {
				means = strings.TrimSpace(scrub.Text(sh.Means))
			}
		}
		if in.Field == "" || means == "" {
			continue
		}
		f := &readingField{base: "`" + in.Field + "`: " + sentenceEnd(means)}
		if len(in.Bands) > 0 && in.Convention != "" {
			f.bands = renderBands(in, scrub)
		}
		f.sign = renderSign(in.Sign, scrub)
		for _, c := range in.Caveats {
			if c = strings.TrimSpace(scrub.Text(c)); c != "" {
				f.caveats = append(f.caveats, sentenceEnd(c))
			}
		}
		f.keepCav = make([]bool, len(f.caveats))
		fields = append(fields, f)
	}
	if len(fields) == 0 {
		return ""
	}
	try := func(set, undo func()) {
		set()
		if len(renderReading(fields)) > ReadingSectionCap {
			undo()
		}
	}
	for _, f := range fields {
		try(func() { f.kept = true }, func() { f.kept = false })
	}
	for _, f := range fields {
		if f.kept && f.bands != "" {
			try(func() { f.keepBand = true }, func() { f.keepBand = false })
		}
	}
	for _, f := range fields {
		if f.kept && f.sign != "" {
			try(func() { f.keepSign = true }, func() { f.keepSign = false })
		}
	}
	for _, f := range fields {
		if !f.kept {
			continue
		}
		for i := range f.caveats {
			try(func() { f.keepCav[i] = true }, func() { f.keepCav[i] = false })
		}
	}
	kept := false
	for _, f := range fields {
		kept = kept || f.kept
	}
	if !kept {
		return ""
	}
	return renderReading(fields)
}

// renderBands renders in's bands with the convention named: "Bands
// (Cohen (1988), absolute value): small below 0.2; …".
func renderBands(in descriptor.Interpretation, scrub ProseScrub) string {
	var b strings.Builder
	b.WriteString("Bands (")
	b.WriteString(scrub.Text(in.Convention))
	if in.Abs {
		b.WriteString(", absolute value")
	}
	b.WriteString("): ")
	for i, band := range in.Bands {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(band.Label)
		b.WriteString(" ")
		switch {
		case band.Min == nil && band.Max != nil:
			b.WriteString("below ")
			b.WriteString(formatBound(*band.Max))
		case band.Min != nil && band.Max == nil:
			b.WriteString(formatBound(*band.Min))
			b.WriteString(" and above")
		case band.Min != nil && band.Max != nil:
			b.WriteString(formatBound(*band.Min))
			b.WriteString(" to ")
			b.WriteString(formatBound(*band.Max))
		default:
			b.WriteString("any value")
		}
	}
	b.WriteString(".")
	return b.String()
}

func formatBound(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

// renderSign renders a Sign map: "Sign: positive means …; negative means
// …." — "" when empty or scrubbed away.
func renderSign(sign map[string]string, scrub ProseScrub) string {
	keys := make([]string, 0, len(sign))
	for k := range sign {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		v := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(scrub.Text(sign[k])), "."))
		if v == "" {
			continue
		}
		label := k
		switch k {
		case "+":
			label = "positive"
		case "-":
			label = "negative"
		}
		parts = append(parts, label+" means "+v)
	}
	if len(parts) == 0 {
		return ""
	}
	return "Sign: " + strings.Join(parts, "; ") + "."
}

// sentenceEnd terminates s with a period unless it already ends a
// sentence.
func sentenceEnd(s string) string {
	if strings.HasSuffix(s, ".") || strings.HasSuffix(s, "?") || strings.HasSuffix(s, "!") {
		return s
	}
	return s + "."
}

// renderGenerated substitutes body's generated-section markers for the
// instance: the operator is the body's frontmatter `operator:`; a
// not-for target survives iff its node survived the prune, and every
// string goes through the instance scrub.
func (d *Discovery) renderGenerated(body string) string {
	op := ""
	if md, ok := skills.ParseMetadata(body); ok {
		op = md.Operator
	}
	keep := func(use string) bool {
		if d.graph == nil {
			return true
		}
		return d.graph.Has(FenceFeatureID(strings.TrimPrefix(use, string(descriptor.OntologyNodeOperator)+":")))
	}
	return skills.RenderGenerated(body, func(section string) string {
		if op == "" {
			return ""
		}
		return RenderGuidanceSection(op, section, d.extSnap, keep, d.scrub)
	})
}
