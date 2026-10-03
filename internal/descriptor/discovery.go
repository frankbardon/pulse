package descriptor

import (
	"sort"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/examples"
	"github.com/frankbardon/pulse/internal/skills"
)

// Discovery is one instance's view of the embedded skill pack and request
// example library, read off the instance's PRUNED ontology graph
// (ontology_instance.go): a skill or example is visible iff its node
// survived the prune. It keeps a feature-profiled instance from
// advertising a skill or example for a surface it does not offer.
//
// Every discovery path reads through it — pulse_skills_list /
// pulse_skills_get, pulse_examples_search / pulse_examples_get (via the
// facade's ExamplesSearch / ExampleGet), the pulse-skill:// enumeration and
// template read, and the manifest's skills list and examples count /
// categories / tags — so they agree. A pruned skill or example is absent
// from every listing and an exact-name read of it takes the same not-found
// path as a name that never existed (parity principle).
//
// Visibility is the graph prune (pruneOntology): an atomic skill follows
// the operator or MCP tool it documents, a topical skill goes when any
// `requires:` feature is hidden, an example goes when an operator it
// exemplifies, a capability it requires or an operator its description
// routes to is hidden. Type skills and the virtual intents / glossary
// skills always stay; the virtual bodies render from the pruned graph
// (an intent no enabled operator serves, a term every user of which is
// hidden, is absent). Rendering on top of visibility:
//
//   - every surviving skill's listed metadata (description, applies_to,
//     covers, examples_tags) is rendered through the instance's
//     ProseScrub, so it names no hidden operator or tool (a frontmatter
//     description cannot hold a feature fence);
//   - every surviving embedded BODY — atomic and topical — is rendered
//     by its feature fences against the pruned graph, and an atomic body's
//     `## See` section by edge (skill_render.go). Fence markers are
//     always stripped. Prose outside a fence is served as written: a body
//     names a hidden feature only where it is not fenced yet.
//
// A Discovery with nothing hidden (no feature profile, or a profile that
// hides nothing) passes every call straight through to the embedded
// packages, so profile-free output is byte-identical.
type Discovery struct {
	hiddenSkills   map[string]struct{}
	hiddenExamples map[string]struct{}
	// topical is every kind: design skill — the bodies whose `## See`
	// section is not rendered by edge (it emits no edges).
	topical map[string]struct{}
	// scrub is the instance's prose scrub (the manifest's token set and
	// sentence drop). It renders the visible skills' listed metadata so no
	// listing names a hidden operator or tool; bodies render by fence.
	scrub ProseScrub
	// graph is the instance's pruned ontology; nil on the pass-through
	// view (the virtual skills then render whole).
	graph *OntologyGraph
	// ext is every embedder skill whose node survived the prune
	// (Extensions.Skills; extension_skills.go), keyed by name; a pruned
	// one sits in hiddenSkills like a pruned built-in.
	ext map[string]ExtensionSkill
	// passBuiltins: the instance hides nothing (a Discovery built only
	// to serve embedder skills), so built-in skills pass through to
	// skills.List / skills.Get byte-identically.
	passBuiltins bool
}

// fullDiscovery is the pass-through view every unscoped instance shares.
var fullDiscovery = &Discovery{}

// Discovery returns the instance's skill / example view, built with the
// snapshot (NewInstanceSnapshot). Nil-safe: a nil, unscoped or
// hide-nothing snapshot returns the shared pass-through view.
func (s *InstanceSnapshot) Discovery() *Discovery {
	if s == nil || s.discovery == nil {
		return fullDiscovery
	}
	return s.discovery
}

// buildDiscovery reads skill and example visibility off the pruned
// graph g: a name the embedded pack or library carries whose node g
// lacks was pruned.
func buildDiscovery(inst *InstanceSnapshot, g *OntologyGraph) *Discovery {
	d := &Discovery{
		hiddenSkills:   map[string]struct{}{},
		hiddenExamples: map[string]struct{}{},
		topical:        map[string]struct{}{},
		scrub:          NewProseScrub(inst),
		graph:          g,
	}
	for _, md := range skills.List() {
		if md.Kind == "design" {
			d.topical[md.Name] = struct{}{}
		}
		if !g.Has(OntologyID(descriptor.OntologyNodeSkill, md.Name)) {
			d.hiddenSkills[md.Name] = struct{}{}
		}
	}
	for _, sum := range examples.Search("", nil, "") {
		if !g.Has(OntologyID(descriptor.OntologyNodeExample, sum.Name)) {
			d.hiddenExamples[sum.Name] = struct{}{}
		}
	}
	d.passBuiltins = !inst.Scoped() || len(inst.hidden) == 0
	if ext := inst.Extensions(); ext != nil {
		for _, s := range ext.Skills {
			name := s.Metadata.Name
			if !g.Has(OntologyID(descriptor.OntologyNodeSkill, name)) {
				d.hiddenSkills[name] = struct{}{}
				continue
			}
			if d.ext == nil {
				d.ext = map[string]ExtensionSkill{}
			}
			d.ext[name] = s
			if s.Metadata.Kind == "design" {
				d.topical[name] = struct{}{}
			}
		}
	}
	return d
}

func notTokenRune(r rune) bool {
	return r != '_' && (r < '0' || r > '9') && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z')
}

// jsonHasKey reports whether any object anywhere in v carries key.
func jsonHasKey(v any, key string) bool {
	switch t := v.(type) {
	case map[string]any:
		if _, ok := t[key]; ok {
			return true
		}
		for _, c := range t {
			if jsonHasKey(c, key) {
				return true
			}
		}
	case []any:
		for _, c := range t {
			if jsonHasKey(c, key) {
				return true
			}
		}
	}
	return false
}

// SkillVisible reports whether the instance exposes the named skill.
func (d *Discovery) SkillVisible(name string) bool {
	_, hidden := d.hiddenSkills[name]
	return !hidden
}

// ExampleVisible reports whether the instance exposes the named example.
func (d *Discovery) ExampleVisible(name string) bool {
	_, hidden := d.hiddenExamples[name]
	return !hidden
}

// Skills is skills.List() minus the pruned skills, in the same order,
// each survivor's metadata rendered for the instance: the description
// prose-scrubbed and every applies_to / covers / examples_tags entry
// naming a hidden token dropped (topical metadata included; bodies
// render by feature fence in Skill).
//
// Visible embedder skills (Extensions.Skills) are merged in by name.
func (d *Discovery) Skills() []skills.Metadata {
	all := skills.List()
	if len(d.hiddenSkills) == 0 && !d.scrub.Active() && len(d.ext) == 0 {
		return all
	}
	out := make([]skills.Metadata, 0, len(all)+len(d.ext))
	for _, md := range all {
		if d.SkillVisible(md.Name) {
			out = append(out, d.renderMetadata(md))
		}
	}
	if len(d.ext) > 0 {
		for _, s := range d.ext {
			out = append(out, d.renderMetadata(s.Metadata))
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	}
	return out
}

// renderMetadata scrubs one skill's listed metadata (a copy).
func (d *Discovery) renderMetadata(md skills.Metadata) skills.Metadata {
	if !d.scrub.Active() {
		return md
	}
	md.Description = d.scrub.Text(md.Description)
	md.AppliesTo = d.keepVisibleTokens(md.AppliesTo)
	md.Covers = d.keepVisibleTokens(md.Covers)
	md.ExamplesTags = d.keepVisibleTokens(md.ExamplesTags)
	return md
}

// keepVisibleTokens returns in without the entries that name a hidden
// token; in itself when none does (nil stays nil).
func (d *Discovery) keepVisibleTokens(in []string) []string {
	drop := false
	for _, s := range in {
		if mentionsHidden(s, d.scrub.hidden) {
			drop = true
			break
		}
	}
	if !drop {
		return in
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !mentionsHidden(s, d.scrub.hidden) {
			out = append(out, s)
		}
	}
	return out
}

// keepNode reports, for a registry ID of kind, whether its node
// survived the instance prune.
func (d *Discovery) keepNode(kind descriptor.OntologyNodeKind) func(id string) bool {
	return func(id string) bool { return d.graph.Has(OntologyID(kind, id)) }
}

// Skill returns the markdown body of a visible skill; a pruned name
// answers exactly as a nonexistent one ("", false). An embedded body is
// rendered for the instance (renderBody, skill_render.go): its feature
// fences against the pruned graph and, atomic only, its `## See` section
// by edge — so a table row, list item or code block survives whole or
// goes whole. The virtual intents / glossary bodies render from the
// pruned graph. The pass-through view serves skills.Get (the full
// render: every fence kept, markers stripped).
func (d *Discovery) Skill(name string) (string, bool) {
	if !d.SkillVisible(name) {
		return "", false
	}
	if s, ok := d.ext[name]; ok {
		return d.renderBody(name, s.Raw), true
	}
	if d.graph == nil || d.passBuiltins {
		return skills.Get(name)
	}
	switch name {
	case skills.VirtualIntents:
		return renderIntentsSkill(d.keepNode(descriptor.OntologyNodeIntent)), true
	case skills.VirtualGlossary:
		return renderGlossarySkill(d.keepNode(descriptor.OntologyNodeGlossaryTerm)), true
	}
	raw, ok := skills.Raw(name)
	if !ok {
		return "", false
	}
	if skills.IsVirtual(name) {
		return raw, true
	}
	return d.renderBody(name, raw), true
}

// ExamplesSearch is examples.Search minus the pruned examples; ranking and
// order of the survivors are unchanged. Never nil.
func (d *Discovery) ExamplesSearch(query string, tags []string, category string) []examples.ExampleSummary {
	hits := examples.Search(query, tags, category)
	if len(d.hiddenExamples) == 0 {
		return hits
	}
	out := make([]examples.ExampleSummary, 0, len(hits))
	for _, h := range hits {
		if d.ExampleVisible(h.Name) {
			out = append(out, h)
		}
	}
	return out
}

// Example returns a visible example; a pruned name answers exactly as a
// nonexistent one (nil, false).
func (d *Discovery) Example(name string) (*examples.Example, bool) {
	if !d.ExampleVisible(name) {
		return nil, false
	}
	return examples.Get(name)
}

// ExampleStats returns the manifest's examples count, categories and tags
// over the visible examples. A category or tag carried only by pruned
// examples is absent. Slices are sorted and never nil.
func (d *Discovery) ExampleStats() (count int, categories, tags []string) {
	if len(d.hiddenExamples) == 0 {
		return examples.Count(), examples.AllCategories(), examples.AllTags()
	}
	cats := map[string]struct{}{}
	tagSet := map[string]struct{}{}
	for _, ex := range d.ExamplesSearch("", nil, "") {
		count++
		cats[ex.Category] = struct{}{}
		for _, t := range ex.Tags {
			tagSet[t] = struct{}{}
		}
	}
	return count, sortedKeys(cats), sortedKeys(tagSet)
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
