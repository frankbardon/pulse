package descriptor

import (
	"sort"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/skills"
)

// Skill tiers of SkillsForIntent, in serving order.
const (
	intentSkillTierReference = iota // the intent's documented_by skill (the virtual `intents` skill)
	intentSkillTierDesign           // a design skill whose covers names a serving operator
	intentSkillTierAtomic           // a serving operator's atomic skill
)

// levelRank orders Purpose.Level basic → intermediate → advanced; an
// operator with no Purpose (an extension registered without one) or no
// Level ranks after every graded one.
func levelRank(l descriptor.Level) int {
	switch l {
	case descriptor.LevelBasic:
		return 0
	case descriptor.LevelIntermediate:
		return 1
	case descriptor.LevelAdvanced:
		return 2
	}
	return 3
}

// SkillsForIntent returns the instance's skills relevant to one intent,
// ranked — the list pulse_skills_list {intent} serves. The intent is
// resolved by checkIntent (an unknown or hidden one is
// PULSE_RECOMMEND_INTENT_UNKNOWN, details.valid = the instance's
// intents), and every edge is read off the instance's PRUNED ontology,
// so a hidden operator contributes nothing:
//
//  1. the skills the intent node is documented_by (the virtual
//     `intents` skill);
//  2. design skills whose (rendered) covers names an operator serving
//     the intent, or that operator's category prefix (`TEST` covers
//     TEST_T), by name;
//  3. the atomic skills those serving operators are documented_by,
//     ranked by the operator's Purpose.Level (basic → intermediate →
//     advanced; no Level last) — the Level is joined through the
//     skill's operator, never stored on it — then by name.
//
// Each entry is the instance's rendered metadata, exactly as Skills
// lists it. An intent no operator serves answers its reference skill
// only. Never nil on success.
func (s *InstanceSnapshot) SkillsForIntent(intent string) ([]skills.Metadata, error) {
	if err := s.checkIntent("skills list", intent); err != nil {
		return nil, err
	}
	g := s.Ontology()
	intentID := OntologyID(descriptor.OntologyNodeIntent, intent)

	reference := map[string]bool{}
	for _, e := range g.Out(intentID, descriptor.OntologyEdgeDocumentedBy) {
		if n, ok := g.Node(e.To); ok && n.Kind == descriptor.OntologyNodeSkill {
			reference[n.Name] = true
		}
	}
	serving := map[string]bool{}    // serving operators and their category prefixes
	atomicOp := map[string]string{} // skill name → the serving operator it documents
	for _, op := range g.OperatorsServing(intent) {
		serving[op] = true
		if i := strings.IndexByte(op, '_'); i > 0 {
			serving[op[:i]] = true
		}
		for _, e := range g.Out(OntologyID(descriptor.OntologyNodeOperator, op), descriptor.OntologyEdgeDocumentedBy) {
			if n, ok := g.Node(e.To); ok && n.Kind == descriptor.OntologyNodeSkill {
				atomicOp[n.Name] = op
			}
		}
	}

	type ranked struct {
		md    skills.Metadata
		tier  int
		level int
	}
	var hits []ranked
	for _, md := range s.Discovery().Skills() {
		switch {
		case reference[md.Name]:
			hits = append(hits, ranked{md: md, tier: intentSkillTierReference})
		case atomicOp[md.Name] != "":
			hits = append(hits, ranked{md: md, tier: intentSkillTierAtomic, level: levelRank(s.purposeLevel(atomicOp[md.Name]))})
		case md.Kind == "design" && coversAny(md.Covers, serving):
			hits = append(hits, ranked{md: md, tier: intentSkillTierDesign})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.tier != b.tier {
			return a.tier < b.tier
		}
		if a.level != b.level {
			return a.level < b.level
		}
		return a.md.Name < b.md.Name
	})
	out := make([]skills.Metadata, len(hits))
	for i, h := range hits {
		out[i] = h.md
	}
	return out, nil
}

// purposeLevel is the operator's Purpose.Level: the built-in registry,
// else the instance's extension registration; "" when it declares none.
func (s *InstanceSnapshot) purposeLevel(op string) descriptor.Level {
	if p, ok := builtinPurposes[op]; ok {
		return p.Level
	}
	if p, ok := s.Extensions().PurposeOf(op); ok {
		return p.Level
	}
	return ""
}

func coversAny(covers []string, ops map[string]bool) bool {
	for _, c := range covers {
		if ops[c] {
			return true
		}
	}
	return false
}
