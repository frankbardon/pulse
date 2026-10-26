package regression

import (
	"fmt"
	"math"
	"slices"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// LinearPredictor is the ONE linear-predictor kernel:
//
//	intercept + Σ coefs[i]·x[i]
//
// summed left to right in predictor order. Every per-row scorer —
// ATTR_REG_FITTED / RESIDUAL / LEVERAGE, ATTR_REG_PREDICT — and the GLM
// engine's own η evaluate it, so a scored row equals the fit's own
// fitted value bit for bit on every platform: one expression, one
// summation order, one place the compiler may fuse a multiply-add.
// len(x) must be at least len(coefs).
func LinearPredictor(intercept float64, coefs, x []float64) float64 {
	s := intercept
	for i, c := range coefs {
		s += c * x[i]
	}
	return s
}

// InverseLink returns g⁻¹ for a REG_GLM family / link pair as the fit
// reported them (RegressionResult.Family / .Link) — the very function
// the engine computes its fitted means with.
func InverseLink(family, link string) (func(float64) float64, error) {
	fam, err := selectFamily(family, link)
	if err != nil {
		return nil, err
	}
	return fam.InverseLink, nil
}

// BuiltinScorer is the built-in model.ScorerSource: the RowScorer
// factory of each scorable built-in regression — REG_OLS (penalties and
// selection included) and REG_BAYES_LINEAR (its posterior mean) score
// the linear predictor, REG_GLM its inverse link on the response scale
// (the linear predictor on the link scale). model.ScorableType is this
// table's static face (predict cannot import the engine);
// TestBuiltinScorer_MatchesScorableType keeps them one set.
func BuiltinScorer(t types.RegressionType) (model.ScorerFactory, bool) {
	switch t {
	case types.REG_OLS, types.REG_BAYES_LINEAR:
		return newLinearScorerFactory, true
	case types.REG_GLM:
		return newGLMScorer, true
	}
	// A model regression scores through its registration's scorer
	// (RegisterModel); model.ScorableType, the static face of the
	// coefficient-shaped built-ins, does not list it.
	if f, ok := modelScorers[t]; ok {
		return f, true
	}
	return nil, false
}

// NewScorer builds b's RowScorer for p through BuiltinScorer
// (b.Scorer): see newLinearScorer for the terms / bound rule and
// newGLMScorer for the scale.
func NewScorer(b *model.Bound, p model.ScoreParams) (model.RowScorer, error) {
	return b.Scorer(BuiltinScorer, p)
}

// linearScorer is the linear-predictor RowScorer (REG_OLS,
// REG_BAYES_LINEAR, and a REG_GLM on the link scale). It is immutable
// once built and safe for concurrent use.
type linearScorer struct {
	// intercept is β₀, or 0 when `terms` leaves the intercept out.
	intercept float64
	// coefs / columns are the scored terms' coefficients and predictor
	// columns, in the FIT's order whatever order `terms` lists them in,
	// so terms = every term reproduces the whole model bit for bit.
	coefs   []float64
	columns []string
	// null marks a bound score whose term has no bound in the fit:
	// every row scores null.
	null bool
}

// glmScorer is REG_GLM's response-scale RowScorer: the inverse link of
// its linear predictor, the very function the engine computes its
// fitted means with.
type glmScorer struct {
	linear  *linearScorer
	inverse func(float64) float64
}

var (
	_ model.RowScorer = (*linearScorer)(nil)
	_ model.RowScorer = (*glmScorer)(nil)
)

// scorerStackTerms is the predictor count Score evaluates without a heap
// allocation.
const scorerStackTerms = 16

func newLinearScorerFactory(b *model.Bound, p model.ScoreParams) (model.RowScorer, error) {
	return newLinearScorer(b, p)
}

// newLinearScorer builds b's linear-predictor scorer for p: every term
// with p.Terms nil, else exactly the listed terms (model.InterceptKey
// for the intercept; a candidate predictor the fit's selection dropped
// scores 0 and reads no column). A p.Bound of model.BoundLower /
// BoundUpper (one predictor term, model.CheckScore's rule) multiplies
// that term's column by its confidence bound instead of its
// coefficient; a bound the fit lacks for the term (a lasso-dropped
// predictor, a selection-dropped candidate, df ≤ 0) scores null on
// every row, and a fit with no intervals at all is
// PULSE_MODEL_INTERVALS_UNAVAILABLE.
func newLinearScorer(b *model.Bound, p model.ScoreParams) (*linearScorer, error) {
	if len(b.Coefficients) != len(b.Predictors) {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			fmt.Sprintf("model %q has %d coefficients for %d predictors", b.Name, len(b.Coefficients), len(b.Predictors)),
			map[string]any{"name": b.Name})
	}
	coefs := b.Coefficients
	bounded := p.Bound == model.BoundLower || p.Bound == model.BoundUpper
	if bounded {
		if !b.ReportsIntervals() {
			return nil, model.IntervalsUnavailable(b.Name, "bound", p.Bound)
		}
		coefs = b.CILower
		if p.Bound == model.BoundUpper {
			coefs = b.CIUpper
		}
	}
	terms := p.Terms
	s := &linearScorer{}
	keep := func(term string) bool { return terms == nil || slices.Contains(terms, term) }
	if keep(model.InterceptKey) {
		s.intercept = b.Intercept
	}
	for i, pred := range b.Predictors {
		if keep(pred) {
			s.coefs = append(s.coefs, coefs[i])
			s.columns = append(s.columns, pred)
		}
	}
	if bounded && (len(s.coefs) == 0 || math.IsNaN(s.coefs[0])) {
		s.null = true
	}
	return s, nil
}

// newGLMScorer builds a REG_GLM fit's scorer for p: the linear scorer
// (newLinearScorer's terms / bound rule) on p.Scale model.ScaleLink,
// else (model.ScaleResponse, the default) its inverse link. A family /
// link with no inverse surfaces the engine's own refusal.
func newGLMScorer(b *model.Bound, p model.ScoreParams) (model.RowScorer, error) {
	lin, err := newLinearScorer(b, p)
	if err != nil {
		return nil, err
	}
	if p.Scale == model.ScaleLink {
		return lin, nil
	}
	inv, err := InverseLink(b.Family, b.Link)
	if err != nil {
		return nil, err
	}
	return &glmScorer{linear: lin, inverse: inv}, nil
}

// Columns returns the predictor columns Score reads, in fit order.
func (s *linearScorer) Columns() []string { return append([]string(nil), s.columns...) }

// Score is row's linear predictor; ok=false (a null score) when any
// column it reads is null or absent on row.
func (s *linearScorer) Score(row model.Row) (float64, bool) {
	if s.null {
		return 0, false
	}
	var buf [scorerStackTerms]float64
	x := buf[:0]
	if len(s.columns) > scorerStackTerms {
		x = make([]float64, 0, len(s.columns))
	}
	for _, c := range s.columns {
		v, ok := InputValue(row, c)
		if !ok {
			return 0, false
		}
		x = append(x, v)
	}
	return LinearPredictor(s.intercept, s.coefs, x), true
}

// Columns returns the predictor columns Score reads, in fit order.
func (s *glmScorer) Columns() []string { return s.linear.Columns() }

// Score is g⁻¹ of row's linear predictor; null where the linear
// predictor is.
func (s *glmScorer) Score(row model.Row) (float64, bool) {
	eta, ok := s.linear.Score(row)
	if !ok {
		return 0, false
	}
	return s.inverse(eta), true
}
