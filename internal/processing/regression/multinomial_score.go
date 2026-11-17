package regression

import (
	"fmt"
	"slices"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/regmultinom"
	"github.com/frankbardon/pulse/types"
)

// multinomial_score.go scores a bound REG_MULTINOMIAL fit
// (ATTR_REG_PREDICT, U68 E1-S2 #655). MultinomialFitted is the fit
// rebuilt from what its result — and a saved model file — keeps: the
// "<category>:<term>" coefficients, the categories in dictionary order
// and the reference. It is the categorical-outcome
// model.ProbabilityScorer: every row's category probabilities through
// the fit's own kernel (multinomialSoftmax over LinearPredictor), so a
// scored training row reproduces the fit's fitted probabilities bit for
// bit, and a fit and its reloaded file score identically.
// newMultinomialScorer turns one reference into its column: P(y =
// category) (the default), the class (the most probable category's
// 0-based index among the fit's categories) or, on the link scale, the
// category's log-odds against the reference.

// MultinomialFitted is a fitted baseline-category logit. Immutable;
// safe for concurrent use.
type MultinomialFitted struct {
	predictors []string
	categories []string
	// nonRef are the non-reference categories' positions in categories;
	// intercepts / slopes their coefficients (slopes in predictor
	// order), block for block.
	nonRef     []int
	intercepts []float64
	slopes     [][]float64
}

var _ model.ProbabilityScorer = (*MultinomialFitted)(nil)

// NewMultinomialFitted rebuilds b's fit from its result: the categories
// (at least two), the reference among them, and every non-reference
// category's intercept and predictor coefficients. A result lacking one
// is an invariant violation (PROCESSING_INTERNAL): the engine and the
// model file's rule both guarantee them.
func NewMultinomialFitted(b *model.Bound) (*MultinomialFitted, error) {
	res := b.Result
	if res == nil || len(res.Categories) < 2 {
		return nil, multinomialScoreInternal(b.Name, "carries no categories")
	}
	ref := slices.Index(res.Categories, res.Reference)
	if ref < 0 {
		return nil, multinomialScoreInternal(b.Name, fmt.Sprintf("reference %q is not one of its categories", res.Reference))
	}
	f := &MultinomialFitted{predictors: slices.Clone(b.Predictors), categories: slices.Clone(res.Categories)}
	for c, cat := range res.Categories {
		if c == ref {
			continue
		}
		icpt, ok := res.Coefficients[regmultinom.Key(cat, InterceptKey)]
		if !ok {
			return nil, multinomialScoreInternal(b.Name, "has no coefficient "+regmultinom.Key(cat, InterceptKey))
		}
		slopes := make([]float64, len(b.Predictors))
		for j, x := range b.Predictors {
			v, ok := res.Coefficients[regmultinom.Key(cat, x)]
			if !ok {
				return nil, multinomialScoreInternal(b.Name, "has no coefficient "+regmultinom.Key(cat, x))
			}
			slopes[j] = v
		}
		f.nonRef = append(f.nonRef, c)
		f.intercepts = append(f.intercepts, icpt)
		f.slopes = append(f.slopes, slopes)
	}
	return f, nil
}

func multinomialScoreInternal(name, what string) error {
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
		fmt.Sprintf("REG_MULTINOMIAL model %q %s", name, what), map[string]any{"name": name})
}

// Columns are the predictors, in fit order.
func (f *MultinomialFitted) Columns() []string { return slices.Clone(f.predictors) }

// Outcomes are the categories, in the target's dictionary order.
func (f *MultinomialFitted) Outcomes() []string { return slices.Clone(f.categories) }

// multinomialStack is the category count (and predictor count) scored
// without a heap allocation.
const multinomialStack = 16

// linear writes every category's η (the reference's 0) into eta; false
// when a predictor is null, absent or non-finite (InputValue).
func (f *MultinomialFitted) linear(row model.Row, eta []float64) bool {
	var buf [multinomialStack]float64
	x := buf[:0]
	if len(f.predictors) > multinomialStack {
		x = make([]float64, 0, len(f.predictors))
	}
	for _, name := range f.predictors {
		v, ok := InputValue(row, name)
		if !ok {
			return false
		}
		x = append(x, v)
	}
	for c := range eta {
		eta[c] = 0
	}
	for k, c := range f.nonRef {
		eta[c] = LinearPredictor(f.intercepts[k], f.slopes[k], x)
	}
	return true
}

// etaBuffer is a len(categories) scratch, on the stack when it fits.
func (f *MultinomialFitted) etaBuffer(stack *[multinomialStack]float64) []float64 {
	if len(f.categories) <= multinomialStack {
		return stack[:len(f.categories)]
	}
	return make([]float64, len(f.categories))
}

// Probabilities writes P(y = category | row) into out (len Outcomes()),
// the fit's own kernel (multinomialSoftmax); false when a predictor is
// null or absent.
func (f *MultinomialFitted) Probabilities(row model.Row, out []float64) bool {
	var stack [multinomialStack]float64
	eta := f.etaBuffer(&stack)
	if !f.linear(row, eta) {
		return false
	}
	multinomialSoftmax(eta, out)
	return true
}

// LogOdds is log(P(y = categories[c]) / P(y = reference)) = η_c for row
// (0 for the reference itself); false when a predictor is null or
// absent.
func (f *MultinomialFitted) LogOdds(row model.Row, c int) (float64, bool) {
	var stack [multinomialStack]float64
	eta := f.etaBuffer(&stack)
	if !f.linear(row, eta) {
		return 0, false
	}
	return eta[c], true
}

// newMultinomialScorer is REG_MULTINOMIAL's ScorerFactory: the column p
// names (model.CheckScoreFor has judged it) — the category's log-odds
// against the reference under params.scale link, else
// model.OutcomeScorerFor's probability or class over the fit's vector.
func newMultinomialScorer(b *model.Bound, p model.ScoreParams) (model.RowScorer, error) {
	f, err := NewMultinomialFitted(b)
	if err != nil {
		return nil, err
	}
	k := -1
	if p.Category != "" {
		if k = slices.Index(f.categories, p.Category); k < 0 {
			return nil, multinomialScoreInternal(b.Name, fmt.Sprintf("has no category %q (the outcome rule admits only the fit's)", p.Category))
		}
	}
	if p.Scale == model.ScaleLink {
		if k < 0 {
			return nil, multinomialScoreInternal(b.Name, "is scored on the link scale without params.category (the outcome rule requires it)")
		}
		return multinomialLogOdds{f: f, c: k}, nil
	}
	return model.OutcomeScorerFor(types.REG_MULTINOMIAL, f, p.EffectiveOutput(types.REG_MULTINOMIAL), k)
}

// multinomialLogOdds scores one category's log-odds against the
// reference (params.scale link).
type multinomialLogOdds struct {
	f *MultinomialFitted
	c int
}

func (s multinomialLogOdds) Columns() []string { return s.f.Columns() }

func (s multinomialLogOdds) Score(row model.Row) (float64, bool) { return s.f.LogOdds(row, s.c) }
