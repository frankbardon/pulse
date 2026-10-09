package pulse

import (
	"github.com/frankbardon/pulse/descriptor"
)

// Ontology returns the instance's skill ontology: the typed node / edge
// graph over the intent taxonomy, the registered operators (extensions
// included), the skill pack, the runnable examples, the glossary, the
// capabilities, the MCP tools and the named tables. It carries no prose —
// read bodies through Skill, ExampleGet and Glossary.
//
// It is always the instance's PRUNED view: under a feature profile every
// node for a hidden surface — and every skill, example or intent left
// with nothing to describe — is absent, together with its edges, exactly
// as if it had never been registered. There is no full-graph variant.
// Each call returns a fresh deep copy the caller may mutate. Nodes are
// sorted by ID; edges by From, then Kind, then To.
func (p *Pulse) Ontology() descriptor.Ontology {
	return p.svc.InstanceSnapshot().Ontology().Ontology()
}

// Skills returns the metadata of every skill the instance exposes, sorted
// by name: the embedded skill pack plus the virtual "glossary" and
// "intents" skills. Under a feature profile a skill for a hidden surface
// is absent, and each listed skill's description and applies_to /
// covers / examples_tags entries are rendered so they name no hidden
// operator or MCP tool. It is the list pulse_skills_list and the
// pulse-skill:// resource enumeration serve. Each call returns a fresh
// slice the caller may mutate.
func (p *Pulse) Skills() []SkillMetadata {
	return p.svc.InstanceSnapshot().Discovery().Skills()
}

// SkillsForIntent returns the skills relevant to one intent-taxonomy ID
// (see Intents), ranked — the list pulse_skills_list {intent} serves.
// Order: the skill documenting the intent itself (the virtual "intents"
// skill), then the design skills whose covers names an operator serving
// the intent or its category (TEST, AGG, ...), by name, then those operators' atomic skills, simplest
// first by the operator's Purpose.Level (basic, intermediate, advanced,
// then operators with none), then by name. Every edge is read off the
// instance's pruned ontology, so under a feature profile a hidden
// operator — and its skill — takes no part, and each entry is the
// rendered metadata Skills lists. An intent no operator serves answers
// its reference skill only.
//
// An unknown intent, or one the instance's feature profile hides, is
// PULSE_RECOMMEND_INTENT_UNKNOWN (details.valid lists the instance's
// intents) — the same refusal ExamplesSearchWith and Recommend give.
// Skills itself is unchanged. Never nil on success; each call returns a
// fresh slice the caller may mutate.
func (p *Pulse) SkillsForIntent(intent string) ([]SkillMetadata, error) {
	return p.svc.InstanceSnapshot().SkillsForIntent(intent)
}

// Skill returns the markdown body of the named skill (no ".md" suffix) as
// the instance serves it — the body pulse_skills_get and a
// pulse-skill://<name> read return. A skill the instance hides answers
// ("", false), exactly as a name that does not exist. Under a feature
// profile an atomic (operator, tool, type) body drops the lines naming a
// hidden operator or tool, and the "intents" / "glossary" bodies render
// only what the instance keeps.
func (p *Pulse) Skill(name string) (string, bool) {
	return p.svc.InstanceSnapshot().Discovery().Skill(name)
}
