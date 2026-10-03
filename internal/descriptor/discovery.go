package descriptor

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"

	"github.com/frankbardon/pulse/internal/examples"
	"github.com/frankbardon/pulse/internal/skills"
)

// Discovery is one instance's view of the embedded skill pack and request
// example library: the prune that keeps a feature-profiled instance from
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
// The prune is MINIMAL by design:
//
//   - an ATOMIC skill is dropped when its surface is hidden — an operator
//     skill (`operator:` frontmatter; a SYNTH distribution follows
//     capability:synth; a REG spec modifier that is not itself a feature
//     goes when every regression is hidden) or a tool skill whose MCP
//     tool's feature is hidden;
//   - a type skill stays (field types are not features);
//   - a TOPICAL (kind: design) skill stays and its body is served
//     unrendered, even where it names a hidden operator — the exemption is
//     deliberate until the skill ontology can fence or render topical
//     bodies per instance;
//   - every surviving skill's listed metadata (description, applies_to,
//     covers, examples_tags) and every surviving ATOMIC body is rendered
//     through the instance's ProseScrub, so they name no hidden operator
//     or tool;
//   - an example is dropped when any `_meta.operators` entry is hidden,
//     when its request body or its description names a hidden feature as
//     a whole token (an overlay kind is not always tagged in
//     `_meta.operators`), or when it
//     needs a hidden capability (a facet request; a `crosstab` / `joins`
//     slot anywhere in the body; a compose root).
//
// Extending the prune (topical fences, rendering, capability-keyed
// examples) means adding a rule to skillPruneRules / examplePruneRules or
// rendering inside Skill — the single body path — not replacing the type.
//
// A Discovery with nothing hidden (no feature profile, or a profile that
// hides nothing) passes every call straight through to the embedded
// packages, so profile-free output is byte-identical.
type Discovery struct {
	hiddenSkills   map[string]struct{}
	hiddenExamples map[string]struct{}
	// topical is every kind: design skill — the bodies served unrendered.
	topical map[string]struct{}
	// scrub is the instance's prose scrub (the manifest's token set and
	// sentence drop). It renders the visible skills' metadata and atomic
	// bodies so no listing or read names a hidden operator or tool.
	scrub ProseScrub
}

// fullDiscovery is the pass-through view every unscoped instance shares.
var fullDiscovery = &Discovery{}

// skillPruneRules decide whether one skill is hidden on inst. A skill is
// pruned when any rule says so.
var skillPruneRules = []func(inst *InstanceSnapshot, md skills.Metadata) bool{
	operatorSkillHidden,
	toolSkillHidden,
}

// examplePruneRules decide whether one example is hidden on inst.
var examplePruneRules = []func(inst *InstanceSnapshot, ex *examples.Example) bool{
	exampleOperatorHidden,
	exampleBodyNamesHidden,
	exampleDescriptionNamesHidden,
	exampleCapabilityHidden,
}

// Discovery returns the instance's skill / example prune, computed once
// per instance on first use. Nil-safe: a nil, unscoped or hide-nothing
// snapshot returns the shared pass-through view.
func (s *InstanceSnapshot) Discovery() *Discovery {
	if !s.Scoped() || len(s.hidden) == 0 {
		return fullDiscovery
	}
	s.discoveryOnce.Do(func() { s.discovery = buildDiscovery(s) })
	return s.discovery
}

func buildDiscovery(inst *InstanceSnapshot) *Discovery {
	d := &Discovery{
		hiddenSkills:   map[string]struct{}{},
		hiddenExamples: map[string]struct{}{},
		topical:        map[string]struct{}{},
		scrub:          NewProseScrub(inst),
	}
	for _, md := range skills.List() {
		if md.Kind == "design" {
			d.topical[md.Name] = struct{}{}
		}
		for _, rule := range skillPruneRules {
			if rule(inst, md) {
				d.hiddenSkills[md.Name] = struct{}{}
				break
			}
		}
	}
	for _, sum := range examples.Search("", nil, "") {
		ex, ok := examples.Get(sum.Name)
		if !ok {
			continue
		}
		for _, rule := range examplePruneRules {
			if rule(inst, ex) {
				d.hiddenExamples[ex.Name] = struct{}{}
				break
			}
		}
	}
	return d
}

// operatorSkillHidden: an operator skill names its surface in the
// `operator:` frontmatter; a synth distribution is gated by
// capability:synth rather than by its own name.
func operatorSkillHidden(inst *InstanceSnapshot, md skills.Metadata) bool {
	if md.Kind != "operator" || md.Operator == "" {
		return false
	}
	switch {
	case md.Category == "SYNTH":
		return inst.Hidden(featSynth)
	case md.Category == "REG" && !inst.Hidden(md.Operator) && !inst.Enabled(md.Operator):
		// A spec modifier (REG_RESAMPLE, REG_SELECTION) composes with a
		// regression; it is not a feature of its own.
		for _, r := range regressionCapabilities() {
			if !inst.Hidden(r.Name) {
				return false
			}
		}
		return true
	}
	return inst.Hidden(md.Operator)
}

// toolSkillHidden: tool-<kebab> documents pulse_<snake>; it goes with the
// tool's owning feature. Core-bound tools are always present.
func toolSkillHidden(inst *InstanceSnapshot, md skills.Metadata) bool {
	if md.Kind != "tool" || !strings.HasPrefix(md.Name, "tool-") {
		return false
	}
	tool := "pulse_" + strings.ReplaceAll(strings.TrimPrefix(md.Name, "tool-"), "-", "_")
	b, ok := MCPToolBindingOf(tool)
	if !ok || b.Core != "" {
		return false
	}
	return inst.Hidden(b.Feature)
}

// exampleOperatorHidden: an example using any hidden operator would teach
// a request the instance refuses.
func exampleOperatorHidden(inst *InstanceSnapshot, ex *examples.Example) bool {
	for _, op := range ex.Operators {
		if inst.Hidden(op) {
			return true
		}
	}
	return false
}

// exampleBodyNamesHidden: any whole [A-Za-z0-9_] run of the runnable body
// that is a hidden feature name (operator or overlay kind).
func exampleBodyNamesHidden(inst *InstanceSnapshot, ex *examples.Example) bool {
	for _, tok := range strings.FieldsFunc(string(ex.Body), notTokenRune) {
		if inst.Hidden(tok) {
			return true
		}
	}
	return false
}

// exampleDescriptionNamesHidden: the description an example is listed
// and served with names a hidden feature as a whole token — the example
// teaches (or contrasts with) a surface the instance does not offer.
func exampleDescriptionNamesHidden(inst *InstanceSnapshot, ex *examples.Example) bool {
	for _, tok := range strings.FieldsFunc(ex.Description, notTokenRune) {
		if inst.Hidden(tok) {
			return true
		}
	}
	return false
}

func notTokenRune(r rune) bool {
	return r != '_' && (r < '0' || r > '9') && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z')
}

// exampleCapabilityHidden: the request root or a slot the example uses
// needs a capability the instance hides.
func exampleCapabilityHidden(inst *InstanceSnapshot, ex *examples.Example) bool {
	if ex.Category == "facet" || slices.Contains(ex.Tags, "facet") {
		if inst.Hidden(featFacet) {
			return true
		}
	}
	var body any
	if err := json.Unmarshal(ex.Body, &body); err != nil {
		return false
	}
	if root, ok := body.(map[string]any); ok {
		if _, compose := root["requests"]; compose && inst.Hidden(featCompose) {
			return true
		}
	}
	return jsonHasKey(body, "crosstab") && inst.Hidden(featCrosstab) ||
		jsonHasKey(body, "joins") && inst.Hidden(featJoins)
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
// naming a hidden token dropped (topical metadata included — only a
// topical BODY is exempt).
func (d *Discovery) Skills() []skills.Metadata {
	all := skills.List()
	if len(d.hiddenSkills) == 0 && !d.scrub.Active() {
		return all
	}
	out := make([]skills.Metadata, 0, len(all))
	for _, md := range all {
		if d.SkillVisible(md.Name) {
			out = append(out, d.renderMetadata(md))
		}
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

// Skill returns the markdown body of a visible skill; a pruned name
// answers exactly as a nonexistent one ("", false). An atomic (operator,
// tool, type) body is prose-scrubbed — a sentence naming a hidden
// operator or tool, a cross-reference to a sibling the instance hides,
// is dropped line by line (ProseScrub.Text). Topical bodies are served
// unrendered (see the Discovery doc).
func (d *Discovery) Skill(name string) (string, bool) {
	if !d.SkillVisible(name) {
		return "", false
	}
	body, ok := skills.Get(name)
	if !ok {
		return "", false
	}
	if _, topical := d.topical[name]; topical {
		return body, true
	}
	return d.scrub.Text(body), true
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
