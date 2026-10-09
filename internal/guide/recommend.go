package guide

import (
	"sort"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// DefaultLimit caps the recommendations when the request sets no Limit.
const DefaultLimit = 10

// unboundWhy is the sentence an unbound draft's why ends with.
const unboundWhy = "Fill in each placeholder in the draft before you run it."

// routes names the tooling that answers an intent no operator draft
// can: the non-analytic intents, and the analytic ones no operator
// serves yet. Use is a feature spelling; a route whose feature the
// instance hides is dropped.
var routes = map[string][]descriptor.Alternative{
	descx.IntentPrepare: {
		{When: "the source data still has to be brought in as a cohort", Use: "capability:import"},
		{When: "rows repeat and duplicates should be removed", Use: "capability:dedup"},
		{When: "a set field needs room for more options", Use: "capability:widen"},
	},
	descx.IntentSimulate: {
		{When: "you want synthetic rows that follow a schema or a captured data profile", Use: "capability:synth"},
	},
	descx.IntentLookup: {
		{When: "you want the records stored under a key value", Use: "capability:lookup"},
	},
	descx.IntentMeasureConstruct: {
		{When: "you want to see how the items move together before scoring them", Use: "capability:matrices"},
	},
	descx.IntentFlows: {
		{When: "you want a table of the from state against the to state", Use: "capability:crosstab"},
	},
}

// Recommend answers an UNBOUND recommend request (no cohort) for the
// instance inst: the operators the pruned ontology says serve the
// intent, each as a placeholder skeleton, ranked and capped.
func Recommend(inst *descx.InstanceSnapshot, req descriptor.RecommendRequest) (*descriptor.RecommendResult, error) {
	intent, ok := findIntent(req.Intent)
	if !ok {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_RECOMMEND_INTENT_UNKNOWN,
			"recommend: unknown intent "+quote(req.Intent),
			map[string]any{"intent": req.Intent, "valid": descx.IntentIDs()})
	}
	if err := validateLevel(req.Level); err != nil {
		return nil, err
	}
	if req.Limit < 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			"recommend: limit must not be negative", map[string]any{"field": "limit", "limit": req.Limit})
	}
	limit := req.Limit
	if limit == 0 {
		limit = DefaultLimit
	}

	g := inst.Ontology()
	scrub := descx.NewProseScrub(inst)
	out := &descriptor.RecommendResult{
		Intent:          intent.ID,
		Shapes:          intent.Shapes,
		Recommendations: []descriptor.Recommendation{},
	}
	if intent.Analytic {
		cat := catalog(descx.BuildManifestForInstance(inst))
		for _, name := range g.OperatorsServing(intent.ID) {
			info, ok := cat[name]
			if !ok {
				continue // overlay or distribution: no single-slot draft
			}
			p, ok := purposeOf(inst, name)
			if !ok {
				continue
			}
			d, ok := skeleton(info, intent.ID)
			if !ok {
				continue
			}
			opID := descx.OntologyID(descriptor.OntologyNodeOperator, name)
			out.Recommendations = append(out.Recommendations, descriptor.Recommendation{
				Operator:     name,
				Category:     info.category,
				Level:        p.Level,
				Why:          why(scrub.Text(p.Plain)),
				Request:      d.request,
				Placeholders: d.placeholders,
				Alternatives: keepAlternatives(g, opID, descriptor.OntologyEdgeNotFor, p.NotFor, scrub),
				FollowUps:    keepAlternatives(g, opID, descriptor.OntologyEdgeFollowUp, p.FollowUps, scrub),
			})
		}
	}
	rank(out.Recommendations, req.Level)
	out.CandidatesConsidered = len(out.Recommendations)
	if len(out.Recommendations) > limit {
		out.Recommendations = out.Recommendations[:limit]
		out.Truncated = true
	}
	if len(out.Recommendations) == 0 {
		for _, r := range routes[intent.ID] {
			if _, visible := g.Node(r.Use); visible {
				out.RoutesTo = append(out.RoutesTo, r)
			}
		}
	}
	return out, nil
}

func findIntent(id string) (descriptor.Intent, bool) {
	for _, in := range descx.Intents() {
		if in.ID == id {
			return in, true
		}
	}
	return descriptor.Intent{}, false
}

func validateLevel(l descriptor.Level) error {
	switch l {
	case "", descriptor.LevelBasic, descriptor.LevelIntermediate, descriptor.LevelAdvanced:
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
		"recommend: level must be basic, intermediate or advanced",
		map[string]any{"field": "level", "level": string(l),
			"valid": []string{string(descriptor.LevelBasic), string(descriptor.LevelIntermediate), string(descriptor.LevelAdvanced)}})
}

// purposeOf finds name's Purpose: built-in first, then the instance's
// extension registrations.
func purposeOf(inst *descx.InstanceSnapshot, name string) (descriptor.Purpose, bool) {
	if p, ok := descx.PurposeOf(name); ok {
		return p, true
	}
	return inst.Extensions().PurposeOf(name)
}

// why is the deterministic unbound template: the operator's plain
// purpose, then the placeholder instruction.
func why(plain string) string {
	plain = strings.TrimSpace(plain)
	if plain == "" {
		return unboundWhy
	}
	return plain + " " + unboundWhy
}

// keepAlternatives returns the alternatives whose target survives on
// the instance graph as an edge of kind from opID — a hidden target's
// edge is pruned with it — with each When scrubbed of hidden tokens.
// Alternatives are never invented: each is one of alts, in order.
func keepAlternatives(g *descx.OntologyGraph, opID string, kind descriptor.OntologyEdgeKind, alts []descriptor.Alternative, scrub descx.ProseScrub) []descriptor.Alternative {
	targets := map[string]bool{}
	for _, e := range g.Out(opID, kind) {
		targets[e.To] = true
	}
	var out []descriptor.Alternative
	for _, a := range alts {
		if !targets[descx.OntologyID(descriptor.OntologyNodeOperator, a.Use)] && !targets[a.Use] {
			continue
		}
		when := strings.TrimSpace(scrub.Text(a.When))
		if when == "" {
			continue
		}
		out = append(out, descriptor.Alternative{When: when, Use: a.Use})
	}
	return out
}

// levelOrder ranks the three levels simplest first; an operator with no
// declared level ranks after all three.
var levelOrder = map[descriptor.Level]int{
	descriptor.LevelBasic: 0, descriptor.LevelIntermediate: 1, descriptor.LevelAdvanced: 2,
}

func levelRank(l, want descriptor.Level) int {
	r, ok := levelOrder[l]
	if !ok {
		r = len(levelOrder)
	}
	if want == "" {
		return r
	}
	if l == want {
		return 0
	}
	return 1 + r
}

// categoryOrder breaks a level tie: an operator that answers the
// question on its own (a test, a model, a matrix) before a summary,
// then derived columns, then the slots that only shape the rows
// (groupers, filterers), and last the post-tests, which need a prior
// result's figures.
var categoryOrder = map[string]int{
	catTest: 0, catRegression: 1, catMatrix: 2, catAggregator: 3, catWindow: 4,
	catAttribute: 5, catFeature: 6, catGrouper: 7, catFilterer: 8, catPostTest: 9,
}

// rank orders recommendations: the requested level first (else the
// simplest), then by category (categoryOrder), then by operator name.
// Total, so the order is deterministic.
func rank(recs []descriptor.Recommendation, want descriptor.Level) {
	sort.SliceStable(recs, func(i, j int) bool {
		ri, rj := levelRank(recs[i].Level, want), levelRank(recs[j].Level, want)
		if ri != rj {
			return ri < rj
		}
		if ci, cj := categoryOrder[recs[i].Category], categoryOrder[recs[j].Category]; ci != cj {
			return ci < cj
		}
		return recs[i].Operator < recs[j].Operator
	})
}

func quote(s string) string { return "\"" + s + "\"" }
