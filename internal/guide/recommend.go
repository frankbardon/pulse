package guide

import (
	"slices"
	"sort"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// DefaultLimit caps the recommendations when the request sets no Limit.
const DefaultLimit = 10

// unboundWhy is the sentence an unbound draft's why ends with.
const unboundWhy = "Fill in each placeholder in the draft before you run it."

// routes names the tooling that answers an intent no operator draft
// can: the non-analytic intents, the analytic ones no operator serves
// yet, and the fallback when an intent's operators leave no draft. Use is a feature spelling; a route whose feature the
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
	// measure_construct drafts MAT_RELIABILITY; the route answers only
	// when no draft survives (a bound cohort with fewer than two
	// numeric items to bind).
	descx.IntentMeasureConstruct: {
		{When: "you want to check that a battery of items is reliable (Cronbach's alpha, McDonald's omega) before scoring it", Use: "MAT_RELIABILITY"},
	},
	// flows drafts ATTR_PREVIOUS_STATE (a long panel's previous state);
	// the route answers when no draft survives — a bound cohort with no
	// date field to order by, such as a wide from / to table.
	descx.IntentFlows: {
		{When: "you want a table of the from state against the to state", Use: "capability:crosstab"},
	},
}

// materializeSteps names the derived-column producers whose column a
// model reads in a LATER request: a request cannot hand a column it
// derives to its own regression, so a draft of one of these carries
// Steps — a pulse_materialize draft writing the column (labelled
// column) to a new cohort, then a model request reading it there in the
// role key ("predictors" or "target"; the other role is a placeholder).
// A written table, like routes: no registry declares that an output
// feeds a model. The feature rows are every drafted FEAT_* operator
// whose capability declaration emits ONE float column, read as a
// predictor (TestMaterializeSteps_FeatureTriggersDecided); a bin
// number, calendar parts, a split code or a column set sized by a
// param the caller still picks is not a trigger.
var materializeSteps = map[string]materializeTrigger{
	"ATTR_PC_SCORE":         {column: "pc_score", model: "REG_OLS", role: "predictors"},
	"ATTR_FACTOR_SCORE":     {column: "factor_score", model: "REG_OLS", role: "predictors"},
	"ATTR_SCALE_SCORE":      {column: "scale_score", model: "REG_OLS", role: "target"},
	"ATTR_REG_PREDICT":      {column: "predicted", model: "REG_OLS", role: "predictors"},
	"FEAT_LOG":              {column: "log_value", model: "REG_OLS", role: "predictors"},
	"FEAT_SQRT":             {column: "sqrt_value", model: "REG_OLS", role: "predictors"},
	"FEAT_FREQUENCY_ENCODE": {column: "frequency", model: "REG_OLS", role: "predictors"},
}

// Routes returns a copy of the tooling routes declared for an intent
// (feature spellings, unfiltered by any instance), or nil when the
// intent declares none. The MCP author-request prompt reads it to
// point the tooling intents straight at their tools.
func Routes(intentID string) []descriptor.Alternative {
	rs := routes[intentID]
	if len(rs) == 0 {
		return nil
	}
	return append([]descriptor.Alternative(nil), rs...)
}

// Recommend answers an UNBOUND recommend request (no cohort) for the
// instance inst: the operators the pruned ontology says serve the
// intent, each as a placeholder skeleton, ranked and capped.
func Recommend(inst *descx.InstanceSnapshot, req descriptor.RecommendRequest) (*descriptor.RecommendResult, error) {
	return recommend(inst, req, nil)
}

// RecommendBound answers a COHORT-BOUND recommend request: each serving
// operator's slots are bound to the cohort's fields (hints first, then
// up to BindingsPerOperator bindings in schema order), every draft is
// predict-validated through b.Predict, an invalid one is dropped, and
// each survivor carries predict's advisories. A draft that still needs
// a value only the caller can choose is kept with Bound false and
// Needs naming it.
func RecommendBound(inst *descx.InstanceSnapshot, req descriptor.RecommendRequest, b Bound) (*descriptor.RecommendResult, error) {
	return recommend(inst, req, &b)
}

// scored is one recommendation with the rank keys the wire omits.
type scored struct {
	rec   descriptor.Recommendation
	hints int
}

// ValidateRequest checks the parts of req that need no cohort — the
// intent, level and limit — so a caller can refuse a bad request
// before it opens the cohort.
func ValidateRequest(req descriptor.RecommendRequest) error {
	_, err := checkRequest(req)
	return err
}

func checkRequest(req descriptor.RecommendRequest) (descriptor.Intent, error) {
	intent, ok := findIntent(req.Intent)
	if !ok {
		return intent, errors.NewCodedErrorWithDetails(errors.PULSE_RECOMMEND_INTENT_UNKNOWN,
			"recommend: unknown intent "+quote(req.Intent),
			map[string]any{"intent": req.Intent, "valid": descx.IntentIDs()})
	}
	if err := validateLevel(req.Level); err != nil {
		return intent, err
	}
	if req.Limit < 0 {
		return intent, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			"recommend: limit must not be negative", map[string]any{"field": "limit", "limit": req.Limit})
	}
	return intent, nil
}

func recommend(inst *descx.InstanceSnapshot, req descriptor.RecommendRequest, b *Bound) (*descriptor.RecommendResult, error) {
	intent, err := checkRequest(req)
	if err != nil {
		return nil, err
	}
	limit := req.Limit
	if limit == 0 {
		limit = DefaultLimit
	}
	g := inst.Ontology()
	var cat map[string]opInfo
	if intent.Analytic {
		cat = catalog(descx.BuildManifestForInstance(inst))
	}
	var hints []string
	if b != nil {
		if hints, err = validateHints(req.Fields, b.Schema, intent, takesVector(inst, g, cat, intent)); err != nil {
			return nil, err
		}
	}

	scrub := descx.NewProseScrub(inst)
	out := &descriptor.RecommendResult{
		Intent:          intent.ID,
		Bound:           b != nil,
		Shapes:          intent.Shapes,
		Recommendations: []descriptor.Recommendation{},
	}
	var recs []scored
	if intent.Analytic {
		for _, name := range g.OperatorsServing(intent.ID) {
			info, ok := cat[name]
			if !ok {
				continue // overlay or distribution: no single-slot draft
			}
			p, ok := purposeOf(inst, name)
			if !ok {
				continue
			}
			opID := descx.OntologyID(descriptor.OntologyNodeOperator, name)
			base := descriptor.Recommendation{
				Operator:     name,
				Category:     info.category,
				Level:        p.Level,
				Alternatives: keepAlternatives(g, opID, descriptor.OntologyEdgeNotFor, p.NotFor, scrub),
				FollowUps:    keepAlternatives(g, opID, descriptor.OntologyEdgeFollowUp, p.FollowUps, scrub),
			}
			plain := scrub.Text(p.Plain)
			if b == nil {
				d, ok := skeleton(info, intent.ID)
				if !ok {
					continue
				}
				base.Why = why(plain)
				base.Request = d.request
				base.Placeholders = d.placeholders
				recs = append(recs, scored{rec: base})
				continue
			}
			recs = append(recs, boundRecommendations(info, intent, base, plain, hints, b)...)
		}
	}
	for i := range recs {
		recs[i].rec.Steps = steps(inst, recs[i].rec, b)
	}
	rank(recs, req.Level)
	out.CandidatesConsidered = len(recs)
	if len(recs) > limit {
		recs = recs[:limit]
		out.Truncated = true
	}
	for _, r := range recs {
		out.Recommendations = append(out.Recommendations, r.rec)
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

// boundRecommendations drafts op over the cohort: up to
// BindingsPerOperator bindings, each predict-validated; the survivors
// carry predict's advisories and, when a value only the caller can
// choose remains, Bound false plus Needs.
func boundRecommendations(op opInfo, in descriptor.Intent, base descriptor.Recommendation, plain string, hints []string, b *Bound) []scored {
	slots, needs, ok := plan(op, in)
	if !ok {
		return nil
	}
	if key, ok := weightParams[op.name]; ok {
		if b.Weight != "" {
			w := b.Weight
			slots = append(slots, bslot{key: key, loc: locParams, accept: func(f *encoding.Field) bool { return f.Name == w }})
		} else {
			needs = append(needs, bneed{key: key, loc: locParams, optional: true})
		}
	}
	var out []scored
	for _, bnd := range bind(slots, b.Schema, hints, BindingsPerOperator) {
		needs := nativeNeeds(op, needs, bnd, b.Schema)
		request, probe, ok := boundDraft(op, b.Cohort, slots, bnd, needs)
		if !ok {
			continue
		}
		env := b.Predict(probe)
		if env == nil || !passes(env, needs) {
			continue
		}
		if res, ok := env.Data.(*descriptor.PredictResult); ok && res.Valid && len(env.Errors) == 0 &&
			slices.ContainsFunc(needs, func(n bneed) bool { return n.optional }) {
			// The probe runs without the optional needs: an instance
			// default supplied them, so the draft does not ask.
			needs = slices.DeleteFunc(slices.Clone(needs), func(n bneed) bool { return n.optional })
			if request, _, ok = boundDraft(op, b.Cohort, slots, bnd, needs); !ok {
				continue
			}
		}
		rec := base
		rec.Request = request
		rec.Bound = len(needs) == 0
		rec.Why = boundWhy(plain, b.Schema, bnd, needs)
		if res, ok := env.Data.(*descriptor.PredictResult); ok && len(res.Advisories) > 0 {
			rec.Advisories = append([]descriptor.Advisory(nil), res.Advisories...)
		}
		for _, n := range needs {
			rec.Placeholders = append(rec.Placeholders, n.key)
			rec.Needs = append(rec.Needs, descriptor.RecommendNeed{Param: n.key, Why: whyNeed(n)})
		}
		if len(rec.Placeholders) > 0 {
			rec.Placeholders = sortedUnique(rec.Placeholders)
		}
		out = append(out, scored{rec: rec, hints: bnd.hints})
	}
	return out
}

// takesVector reports whether some operator serving intent in on the
// instance graph drafts a vector slot (bslot.vector), so a native
// vector field may be hinted for it.
func takesVector(inst *descx.InstanceSnapshot, g *descx.OntologyGraph, cat map[string]opInfo, in descriptor.Intent) bool {
	for _, name := range g.OperatorsServing(in.ID) {
		info, ok := cat[name]
		if !ok {
			continue
		}
		if _, ok := purposeOf(inst, name); !ok {
			continue
		}
		slots, _, ok := plan(info, in)
		if ok && slices.ContainsFunc(slots, func(s bslot) bool { return s.vector }) {
			return true
		}
	}
	return false
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

// rank orders recommendations: the drafts that use more hinted fields
// first, then a fully bound draft before one with needs (a draft the
// caller can run as written beats one they must finish), then the
// requested level (else the simplest), then fewest advisories, then by
// category (categoryOrder) and operator name. The sort is stable, so
// one operator's bindings keep schema order and the order is
// deterministic.
func rank(recs []scored, want descriptor.Level) {
	sort.SliceStable(recs, func(i, j int) bool { return before(recs[i], recs[j], want) })
}

// before is rank's strict order: whether a ranks ahead of b.
func before(a, b scored, want descriptor.Level) bool {
	if a.hints != b.hints {
		return a.hints > b.hints
	}
	if na, nb := len(a.rec.Needs) > 0, len(b.rec.Needs) > 0; na != nb {
		return !na
	}
	if ra, rb := levelRank(a.rec.Level, want), levelRank(b.rec.Level, want); ra != rb {
		return ra < rb
	}
	if aa, ab := len(a.rec.Advisories), len(b.rec.Advisories); aa != ab {
		return aa < ab
	}
	if ca, cb := categoryOrder[a.rec.Category], categoryOrder[b.rec.Category]; ca != cb {
		return ca < cb
	}
	return a.rec.Operator < b.rec.Operator
}

func quote(s string) string { return "\"" + s + "\"" }
