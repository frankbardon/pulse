package descriptor

import (
	"fmt"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/examples"
	"github.com/frankbardon/pulse/internal/skills"
)

// Served-body rendering (U10, E2-S1).
//
// A visible skill's body is rendered for the instance in two passes,
// replacing the line-wise ProseScrub the U06 prune applied to atomic
// bodies (ProseScrub now renders only listed METADATA — descriptions
// cannot hold fences):
//
//  1. Feature fences (skills.RenderFences): a fence survives iff every
//     name it lists resolves to a node of the instance's PRUNED ontology
//     (FenceFeatureID). Markers are always stripped. Atomic and topical
//     bodies alike.
//  2. `## See` by edge (atomic and topical bodies alike — the section
//     the ontology reads routes_to / exemplified_by edges from): a
//     backticked stem naming a skill the instance pruned is cut from its
//     line, a `pulse_examples_search tags=[…]` ref is cut when no VISIBLE
//     example answers it (seeTags), a line every span of which was cut is
//     dropped, and a section a cut left with no text is dropped with its
//     heading.
//
// Between the two, every generated-section marker
// (`<!-- generated: use-when -->` / `<!-- generated: reading-the-output -->`)
// is replaced with its section rendered from the operator's guidance
// metadata for the instance (skill_sections.go).
//
// A profile-free instance never reaches this file (the pass-through
// Discovery serves skills.Get — the full render, markers stripped).

// FenceFeatureID resolves a fence name (feature-profile spelling) to the
// ontology node that carries it: an operator feature — or an operator
// that is no feature (an extension operator, a synth distribution) — is
// `operator:<NAME>`, every other feature its spelling verbatim.
func FenceFeatureID(name string) string {
	if k, ok := FeatureKindOf(name); ok && k != FeatureKindOperator {
		return OntologyID(descriptor.OntologyNodeCapability, name)
	}
	return OntologyID(descriptor.OntologyNodeOperator, name)
}

// ValidateSkillFences checks body's fences against g (nil = the base
// graph): each must parse, and each name must be a node of g — a
// built-in feature, or an operator g carries. The error is a
// *skills.FenceError (Line = the opener's line).
func ValidateSkillFences(body string, g *OntologyGraph) error {
	if g == nil {
		g = BaseOntology()
	}
	fences, err := skills.ParseFences(body)
	if err != nil {
		return err
	}
	for _, f := range fences {
		for _, n := range f.Names {
			if !g.Has(FenceFeatureID(n)) {
				return &skills.FenceError{Line: f.Line, Msg: fmt.Sprintf("unknown feature %q (feature-profile spelling: operators bare, others <kind>:<name>)", n)}
			}
		}
	}
	return nil
}

// renderBody renders a visible embedded skill's raw body for the
// instance. A body whose fences do not parse is served as the full
// render would serve it (raw) — unreachable for the embedded pack
// (TestSkillFences_EmbeddedPackParses).
func (d *Discovery) renderBody(name, raw string) string {
	out, err := skills.RenderFences(raw, func(n string) bool { return d.graph.Has(FenceFeatureID(n)) })
	if err != nil {
		return raw
	}
	out = d.renderGenerated(out)
	if _, topical := d.topical[name]; topical {
		out = d.renderCovers(out)
	}
	var visible []examples.ExampleSummary
	return renderSeeSection(out, func(span string) bool {
		if searches, ok := seeTags(span); ok {
			if visible == nil {
				visible = d.ExamplesSearch("", nil, "")
			}
			for _, ex := range visible {
				if seeTagsMatch(searches, ex.Tags) {
					return true
				}
			}
			return false
		}
		_, pruned := d.hiddenSkills[span]
		return !pruned
	})
}

// renderSeeSection renders body's `## See` section line by line
// (renderSeeLine); lines outside the section are untouched, a line left
// naming nothing is dropped, and a section a cut left holding only blank
// lines is dropped with its heading (a section that was blank to begin
// with stays). A render that cuts nothing returns body unchanged.
func renderSeeSection(body string, keepSpan func(span string) bool) string {
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	head := -1 // index in out of the open See heading, -1 outside one
	cut, text := false, false
	closeSee := func() {
		if head >= 0 && cut && !text {
			out = out[:head]
		}
		head, cut, text = -1, false, false
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			closeSee()
			if strings.TrimSpace(line) == "## See" {
				head = len(out)
			}
			out = append(out, line)
			continue
		}
		if head < 0 {
			out = append(out, line)
			continue
		}
		r, keep := renderSeeLine(line, keepSpan)
		if !keep {
			cut = true
			continue
		}
		cut = cut || r != line
		text = text || strings.TrimSpace(r) != ""
		out = append(out, r)
	}
	closeSee()
	return strings.Join(out, "\n")
}

// See-line separators between list items (outside backticks and
// parentheses); " · " is the topical skills' inline-list separator.
var seeSeparators = []string{", ", "; ", " / ", " · "}

// renderSeeLine cuts the ITEMS of one See line whose every backticked
// span keepSpan rejects. A line is `<prefix><item><sep><item>…<tail>`:
// the prefix runs to the first backtick ("- Skills: "), items split at
// ", " / "; " / " / " / " · " outside backticks and parentheses (so an
// item keeps its annotation — "`request-envelope` (Time zones)"), and
// the tail is a
// " — description" after the last span, shared by every item. A cut item
// takes its separator with it (a trailing "." survives on the new last
// item); a line whose every span-bearing item is cut reports keep=false.
// A line with nothing to cut is returned unchanged.
func renderSeeLine(line string, keepSpan func(string) bool) (string, bool) {
	first := strings.IndexByte(line, '`')
	last := strings.LastIndexByte(line, '`')
	if first < 0 || last == first {
		return line, true
	}
	prefix, body, tail := line[:first], line[first:], ""
	if k := strings.Index(line[last:], " — "); k >= 0 {
		body, tail = line[first:last+k], line[last+k:]
	}
	items, seps := splitSeeItems(body)
	cut := make([]bool, len(items))
	anyCut, anyKept := false, false
	for i, it := range items {
		spans := backtickSpans(it)
		if len(spans) == 0 {
			continue
		}
		all := true
		for _, sp := range spans {
			if keepSpan(sp) {
				all = false
				break
			}
		}
		cut[i] = all
		anyCut = anyCut || all
		anyKept = anyKept || !all
	}
	if !anyCut {
		return line, true
	}
	if !anyKept {
		return "", false
	}
	var b strings.Builder
	b.WriteString(prefix)
	lastKept := -1
	for i, it := range items {
		if cut[i] {
			continue
		}
		if lastKept >= 0 {
			b.WriteString(seps[i-1])
		}
		b.WriteString(it)
		lastKept = i
	}
	r := b.String()
	if lastKept < len(items)-1 && strings.HasSuffix(items[len(items)-1], ".") && !strings.HasSuffix(r, ".") {
		r += "."
	}
	return r + tail, true
}

// splitSeeItems splits s at the seeSeparators that sit outside
// backticks and parentheses; seps[i] joins items[i] and items[i+1].
func splitSeeItems(s string) (items, seps []string) {
	start, depth, inTick := 0, 0, false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '`':
			inTick = !inTick
			continue
		case inTick:
			continue
		case c == '(':
			depth++
			continue
		case c == ')':
			depth--
			continue
		case depth > 0:
			continue
		}
		for _, sep := range seeSeparators {
			if strings.HasPrefix(s[i:], sep) {
				items = append(items, s[start:i])
				seps = append(seps, sep)
				i += len(sep) - 1
				start = i + 1
				break
			}
		}
	}
	return append(items, s[start:]), seps
}

// renderCovers re-renders the frontmatter `covers:` list of a served
// body through keepVisibleTokens, so the frontmatter a body is served
// with names exactly what the listed metadata does — a list can hold no
// fence, and only topical skills carry one. A list naming nothing hidden
// leaves the body byte-identical.
func (d *Discovery) renderCovers(body string) string {
	if !strings.HasPrefix(body, "---\n") {
		return body
	}
	end := strings.Index(body[4:], "\n---")
	if end < 0 {
		return body
	}
	fm := body[4 : 4+end]
	lines := strings.Split(fm, "\n")
	changed := false
	for i, line := range lines {
		rest, ok := strings.CutPrefix(line, "covers:")
		if !ok {
			continue
		}
		rest = strings.TrimSpace(rest)
		if !strings.HasPrefix(rest, "[") || !strings.HasSuffix(rest, "]") {
			continue
		}
		var items []string
		for _, it := range strings.Split(rest[1:len(rest)-1], ",") {
			if it = strings.TrimSpace(it); it != "" {
				items = append(items, it)
			}
		}
		kept := d.keepVisibleTokens(items)
		if len(kept) == len(items) {
			continue
		}
		lines[i] = "covers: [" + strings.Join(kept, ", ") + "]"
		changed = true
	}
	if !changed {
		return body
	}
	return "---\n" + strings.Join(lines, "\n") + body[4+end:]
}
