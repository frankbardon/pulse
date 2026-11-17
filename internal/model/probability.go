package model

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// probability.go is the categorical-outcome half of the scoring path
// (U68): a fit whose response is a set of levels or categories
// (REG_ORDINAL, REG_MULTINOMIAL) predicts a probability VECTOR per row,
// not one score.
// ProbabilityScorer is that multi-output scorer — the one kernel the
// ATTR_REG_PREDICT column scorers (OutcomeScorer), a holdout's proper
// scores and the per-level marginal effects read — and the outcome rule
// (checkOutcome, inside CheckScoreFor) is the one place that judges
// ATTR_REG_PREDICT's params.level / params.category / params.output,
// for predict and the runtime alike.

// The params.output values of ATTR_REG_PREDICT over a categorical-
// outcome fit. One slot is one column; `for_each` over the levels fans
// a slot out to one column per level.
const (
	// OutputProbability is P(y = outcome | x): needs the outcome
	// (params.level on REG_ORDINAL, params.category on REG_MULTINOMIAL —
	// its default).
	OutputProbability = "probability"
	// OutputCumulative is P(y ≤ level | x) on an ordered outcome: needs
	// params.level.
	OutputCumulative = "cumulative"
	// OutputExpected is the expected level Σ k·P(y = k | x), the levels
	// numbered 1..K in fitted order: REG_ORDINAL's default when no level
	// is named.
	OutputExpected = "expected"
	// OutputClass is the most probable outcome (the first on a tie): its
	// 1-based position on REG_ORDINAL, its 0-based dictionary index among
	// the fit's categories on REG_MULTINOMIAL.
	OutputClass = "class"
)

// The PULSE_MODEL_SCORE_INVALID reasons of the outcome rule.
const (
	// ReasonOutcomeUnsupported refuses params.level, params.category or
	// params.output on a fit whose type does not take it (details.slot
	// names the key, details.requires the types that do).
	ReasonOutcomeUnsupported = "outcome_unsupported"
	// ReasonOutputUnknown refuses a params.output the fit's type does
	// not compute (details.valid lists the ones it does).
	ReasonOutputUnknown = "output_unknown"
	// ReasonOutcomeRequired refuses an output that reads one outcome
	// (probability, cumulative) without naming it.
	ReasonOutcomeRequired = "outcome_required"
	// ReasonOutcomeUnused refuses a named outcome beside an output that
	// reads the whole vector (expected, class).
	ReasonOutcomeUnused = "outcome_unused"
	// ReasonOutcomeUnknown refuses a level / category the fit does not
	// carry (details.available lists the ones it does).
	ReasonOutcomeUnknown = "outcome_unknown"
	// ReasonOutputScale refuses params.output beside params.scale link,
	// whose score is the linear predictor — and a named level on
	// REG_ORDINAL, whose link score reads none (REG_MULTINOMIAL's link
	// score is one category's log-odds against the reference, so it
	// needs params.category: ReasonOutcomeRequired without it).
	ReasonOutputScale = "output_scale"
)

// ProbabilityScorer is a categorical-outcome fit's multi-output scorer:
// the full probability vector of one row over the fit's outcomes. It is
// immutable once built and safe for concurrent use.
type ProbabilityScorer interface {
	// Columns returns the columns Probabilities reads, in the model's
	// order.
	Columns() []string
	// Outcomes are the fit's levels / categories in fitted order: the
	// positions of Probabilities' vector.
	Outcomes() []string
	// Probabilities writes P(y = Outcomes()[k] | row) into out (len
	// Outcomes()); false when a column it reads is null or absent.
	Probabilities(row Row, out []float64) bool
}

// outcomeRule is one categorical-outcome type's ATTR_REG_PREDICT
// surface: the params key naming one outcome, the outputs it computes,
// the ones that read the named outcome, its default output with and
// without a named outcome, whether its params.scale link score reads
// the named outcome (linkNeedsOutcome) and the class output's origin
// (1: a 1-based position; 0: a 0-based index).
type outcomeRule struct {
	selector            string
	outputs             []string
	needsOutcome        []string
	defaultOutput       string
	defaultOutputNamed  string
	selectorDescription string
	linkNeedsOutcome    bool
	classOrigin         int
}

// outcomeRules are the categorical-outcome types' rules, keyed by type.
var outcomeRules = map[types.RegressionType]outcomeRule{
	types.REG_ORDINAL: {
		selector:            "level",
		outputs:             []string{OutputProbability, OutputCumulative, OutputExpected, OutputClass},
		needsOutcome:        []string{OutputProbability, OutputCumulative},
		defaultOutput:       OutputExpected,
		defaultOutputNamed:  OutputProbability,
		selectorDescription: "a level of the fit (outputs.levels)",
		classOrigin:         1,
	},
	// REG_MULTINOMIAL: P(y = category) by default (so a slot names its
	// category), the class as the category's dictionary index among the
	// fit's categories (Result.Categories, dictionary order), and on the
	// link scale the category's log-odds against the reference.
	types.REG_MULTINOMIAL: {
		selector:            "category",
		outputs:             []string{OutputProbability, OutputClass},
		needsOutcome:        []string{OutputProbability},
		defaultOutput:       OutputProbability,
		defaultOutputNamed:  OutputProbability,
		selectorDescription: "a category of the fit (categories)",
		linkNeedsOutcome:    true,
		classOrigin:         0,
	},
}

// ScoresWholeVectorByDefault reports whether a categorical-outcome
// type's default score (no params.output, no outcome named) reads the
// whole probability vector — REG_ORDINAL's expected level — rather than
// one outcome a slot must name (REG_MULTINOMIAL's probability). A
// params.fit fitted / residual / design slot scores the fit's default,
// so it can bind only a type whose default needs no outcome. True on a
// type with no outcome rule.
func ScoresWholeVectorByDefault(t types.RegressionType) bool {
	r, ok := outcomeRules[t]
	return !ok || !slices.Contains(r.needsOutcome, r.defaultOutput)
}

// OutcomeType reports whether t is a categorical-outcome regression,
// whose ATTR_REG_PREDICT takes params.output and an outcome selector.
func OutcomeType(t types.RegressionType) bool {
	_, ok := outcomeRules[t]
	return ok
}

// outcomeTypesFor are the types (sorted) whose rule names selector
// ("" any categorical-outcome type) — details.requires of a refusal.
func outcomeTypesFor(selector string) []string {
	var out []string
	for t, r := range outcomeRules {
		if selector == "" || r.selector == selector {
			out = append(out, string(t))
		}
	}
	slices.Sort(out)
	return out
}

// requiresTypes is details.requires naming types: "type A" or "type A
// or B"; "" when no scorable type takes the key yet.
func requiresTypes(ts []string) string {
	if len(ts) == 0 {
		return ""
	}
	return "type " + strings.Join(ts, " or ")
}

// outcome is p's named outcome: params.level or params.category,
// whichever the rule's selector is ("" none).
func (p ScoreParams) outcome(selector string) string {
	if selector == "category" {
		return p.Category
	}
	return p.Level
}

// EffectiveOutput is p's output over a fit of type t: params.output, or
// the type's default (with or without a named outcome); "" on a type
// with no outcome rule.
func (p ScoreParams) EffectiveOutput(t types.RegressionType) string {
	r, ok := outcomeRules[t]
	if !ok {
		return ""
	}
	if p.Output != "" {
		return p.Output
	}
	if p.outcome(r.selector) != "" {
		return r.defaultOutputNamed
	}
	return r.defaultOutput
}

// checkOutcome is the outcome rule over a scorable fit's reference: a
// level / category / output on a type that does not take it, an output
// the type does not compute, an outcome-reading output with no outcome
// named, an outcome beside a whole-vector output, either beside scale
// link, and an outcome the fit does not carry (judged when sig knows
// the fit's outcomes) are refused. The first fault wins.
func checkOutcome(p ScoreParams, sig Signature) *errors.CodedError {
	r, ok := outcomeRules[sig.Type]
	for _, k := range []struct{ slot, value string }{{"level", p.Level}, {"category", p.Category}, {"output", p.Output}} {
		if k.value == "" {
			continue
		}
		selector := k.slot
		if selector == "output" {
			selector = ""
		}
		if ok && (k.slot == "output" || k.slot == r.selector) {
			continue
		}
		details := map[string]any{"type": string(sig.Type), "slot": "params." + k.slot}
		what := fmt.Sprintf("params.%s does not apply to %q, a %s fit: no scorable regression type takes it", k.slot, p.Fit, sig.Type)
		if requires := requiresTypes(outcomeTypesFor(selector)); requires != "" {
			details["requires"] = requires
			what = fmt.Sprintf("params.%s does not apply to %q, a %s fit: it takes %s", k.slot, p.Fit, sig.Type, requires)
		}
		return scoreInvalid(p.Fit, ReasonOutcomeUnsupported, what, details)
	}
	if !ok {
		return nil
	}
	named := p.outcome(r.selector)
	if p.Scale == ScaleLink {
		switch {
		case r.linkNeedsOutcome && p.Output != "":
			return scoreInvalid(p.Fit, ReasonOutputScale,
				"params.scale link scores params."+r.selector+"'s log-odds against the reference; it takes no params.output — drop it, or drop scale",
				map[string]any{"type": string(sig.Type), "scale": p.Scale})
		case !r.linkNeedsOutcome && (p.Output != "" || named != ""):
			return scoreInvalid(p.Fit, ReasonOutputScale,
				"params.scale link scores the linear predictor xᵀβ; it takes no params.output or params."+r.selector+" — drop them, or drop scale",
				map[string]any{"type": string(sig.Type), "scale": p.Scale})
		case r.linkNeedsOutcome && named == "":
			return scoreInvalid(p.Fit, ReasonOutcomeRequired,
				fmt.Sprintf("params.scale link scores one outcome's log-odds against the reference: name it under params.%s (%s); for_each over the outcomes makes one column each", r.selector, r.selectorDescription),
				map[string]any{"type": string(sig.Type), "scale": p.Scale, "requires": "params." + r.selector})
		}
		return outcomeKnown(p, sig, r, named)
	}
	out := p.EffectiveOutput(sig.Type)
	if !slices.Contains(r.outputs, out) {
		return scoreInvalid(p.Fit, ReasonOutputUnknown,
			fmt.Sprintf("params.output %q is not one of %s for a %s fit", p.Output, strings.Join(r.outputs, ", "), sig.Type),
			map[string]any{"type": string(sig.Type), "output": p.Output, "valid": slices.Clone(r.outputs)})
	}
	needs := slices.Contains(r.needsOutcome, out)
	if needs && named == "" {
		return scoreInvalid(p.Fit, ReasonOutcomeRequired,
			fmt.Sprintf("params.output %q reads one outcome: name it under params.%s (%s); for_each over the outcomes makes one column each", out, r.selector, r.selectorDescription),
			map[string]any{"type": string(sig.Type), "output": out, "requires": "params." + r.selector})
	}
	if !needs && named != "" {
		return scoreInvalid(p.Fit, ReasonOutcomeUnused,
			fmt.Sprintf("params.output %q reads every outcome; params.%s does not apply — drop it", out, r.selector),
			map[string]any{"type": string(sig.Type), "output": out, "slot": "params." + r.selector})
	}
	return outcomeKnown(p, sig, r, named)
}

// outcomeKnown refuses a named outcome the fit does not carry, judged
// when sig knows the fit's outcomes.
func outcomeKnown(p ScoreParams, sig Signature, r outcomeRule, named string) *errors.CodedError {
	if named != "" && sig.OutcomesKnown && !slices.Contains(sig.Outcomes, named) {
		return scoreInvalid(p.Fit, ReasonOutcomeUnknown,
			fmt.Sprintf("params.%s %q is not one of the fit's outcomes %v", r.selector, named, sig.Outcomes),
			map[string]any{"type": string(sig.Type), "slot": "params." + r.selector, r.selector: named, "available": slices.Clone(sig.Outcomes)})
	}
	return nil
}

// OutcomeIndex is outcome's position in ps's outcomes (-1 when absent).
func OutcomeIndex(ps ProbabilityScorer, outcome string) int {
	return slices.Index(ps.Outcomes(), outcome)
}

// OutcomeScorer is the one-column RowScorer of output over ps:
// P(y = outcome) (probability), Σ_{k ≤ outcome} P(y = k) (cumulative),
// Σ (k+1)·P(y = k) (expected) or the 1-based position of the most
// probable outcome (class). outcome is the named outcome's position
// (ignored by expected and class). The caller has run CheckScoreFor.
func OutcomeScorer(ps ProbabilityScorer, output string, outcome int) (RowScorer, error) {
	return outcomeScorerAt(ps, output, outcome, 1)
}

// OutcomeScorerFor is OutcomeScorer over a fit of type t: its class
// output counts from t's origin (REG_MULTINOMIAL's 0-based dictionary
// index, REG_ORDINAL's 1-based position).
func OutcomeScorerFor(t types.RegressionType, ps ProbabilityScorer, output string, outcome int) (RowScorer, error) {
	origin := 1
	if r, ok := outcomeRules[t]; ok {
		origin = r.classOrigin
	}
	return outcomeScorerAt(ps, output, outcome, origin)
}

// outcomeScorerAt builds the column, its class counted from origin.
func outcomeScorerAt(ps ProbabilityScorer, output string, outcome, origin int) (RowScorer, error) {
	k := len(ps.Outcomes())
	switch output {
	case OutputProbability, OutputCumulative:
		if outcome < 0 || outcome >= k {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
				fmt.Sprintf("outcome position %d is outside the fit's %d outcomes", outcome, k), nil)
		}
	case OutputExpected, OutputClass:
	default:
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			fmt.Sprintf("no outcome scorer for output %q", output), nil)
	}
	return outcomeScorer{ps: ps, output: output, outcome: outcome, k: k, origin: origin}, nil
}

// outcomeScorer is OutcomeScorer's column.
type outcomeScorer struct {
	ps      ProbabilityScorer
	output  string
	outcome int
	k       int
	origin  int
}

func (s outcomeScorer) Columns() []string { return s.ps.Columns() }

// outcomeStack is the stack buffer's size: a vector this short needs no
// allocation per row.
const outcomeStack = 32

func (s outcomeScorer) Score(row Row) (float64, bool) {
	var stack [outcomeStack]float64
	var pr []float64
	if s.k <= outcomeStack {
		pr = stack[:s.k]
	} else {
		pr = make([]float64, s.k)
	}
	if !s.ps.Probabilities(row, pr) {
		return 0, false
	}
	switch s.output {
	case OutputProbability:
		return pr[s.outcome], true
	case OutputCumulative:
		c := 0.0
		for _, v := range pr[:s.outcome+1] {
			c += v
		}
		return c, true
	case OutputExpected:
		e := 0.0
		for k, v := range pr {
			e += float64(float64(k+1) * v)
		}
		return e, true
	default: // OutputClass
		best := 0
		for k, v := range pr {
			if v > pr[best] {
				best = k
			}
		}
		return float64(best + s.origin), true
	}
}

// ResultOutcomes are a categorical-outcome fit's outcomes off its result:
// REG_MULTINOMIAL's Categories, else outputs.levels (OutcomesOf); false
// when absent or malformed.
func ResultOutcomes(t types.RegressionType, res *types.RegressionResult) ([]string, bool) {
	if res == nil {
		return nil, false
	}
	if t == types.REG_MULTINOMIAL {
		if len(res.Categories) == 0 {
			return nil, false
		}
		return slices.Clone(res.Categories), true
	}
	return OutcomesOf(res.Outputs)
}

// OutcomesOf reads a categorical-outcome fit's outcomes off its Outputs
// (outputs.levels: a []string in the run, a []any once read back from a
// model file); false when absent or malformed.
func OutcomesOf(outs map[string]any) ([]string, bool) {
	switch v := outs["levels"].(type) {
	case []string:
		return slices.Clone(v), true
	case []any:
		out := make([]string, len(v))
		for i, x := range v {
			s, ok := x.(string)
			if !ok {
				return nil, false
			}
			out[i] = s
		}
		return out, true
	}
	return nil, false
}

// staticOutcomes is the outcome set a categorical-outcome fit request
// pins before it runs — REG_ORDINAL's params.levels — and whether it
// pins one (otherwise the data decide them, which only the run knows).
func staticOutcomes(reg *types.RegressionSpec) ([]string, bool) {
	if reg == nil || reg.Type != types.REG_ORDINAL || len(reg.Params) == 0 {
		return nil, false
	}
	var p struct {
		Levels []string `json:"levels"`
	}
	if err := json.Unmarshal(reg.Params, &p); err != nil || p.Levels == nil {
		return nil, false
	}
	return p.Levels, true
}
