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

// Scorer applies one bound model to rows (ATTR_REG_PREDICT). It is
// immutable once built and safe for concurrent use.
type Scorer struct {
	// intercept is β₀, or 0 when `terms` leaves the intercept out.
	intercept float64
	// coefs / columns are the scored terms' coefficients and predictor
	// columns, in the FIT's order whatever order `terms` lists them in,
	// so terms = every term reproduces the whole model bit for bit.
	coefs   []float64
	columns []string
	// inverse is the GLM inverse link on the response scale; nil is the
	// identity (OLS, Bayes, a GLM on the link scale).
	inverse func(float64) float64
	// null marks a bound score whose term has no bound in the fit:
	// every row scores null.
	null bool
}

// scorerStackTerms is the predictor count Score evaluates without a heap
// allocation.
const scorerStackTerms = 16

// NewScorer builds b's scorer for p: every term with p.Terms nil, else
// exactly the listed terms (model.InterceptKey for the intercept; a
// candidate predictor the fit's selection dropped scores 0 and reads no
// column), on p.Scale (model.ScaleResponse — the default — or
// model.ScaleLink; REG_GLM only). A p.Bound of model.BoundLower /
// BoundUpper (one predictor term, model.CheckScore's rule) multiplies
// that term's column by its confidence bound instead of its
// coefficient; a bound the fit lacks for the term (a lasso-dropped
// predictor, a selection-dropped candidate, df ≤ 0) scores null on
// every row, and a fit with no intervals at all is
// PULSE_MODEL_INTERVALS_UNAVAILABLE. The caller has run
// model.CheckScore; a GLM whose family / link has no inverse surfaces
// the engine's own refusal.
func NewScorer(b *model.Bound, p model.ScoreParams) (*Scorer, error) {
	if b == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL, "NewScorer called with a nil model")
	}
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
	s := &Scorer{}
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
	if b.Type == types.REG_GLM && p.Scale != model.ScaleLink {
		inv, err := InverseLink(b.Family, b.Link)
		if err != nil {
			return nil, err
		}
		s.inverse = inv
	}
	return s, nil
}

// Columns returns the predictor columns Score reads, in fit order.
func (s *Scorer) Columns() []string { return append([]string(nil), s.columns...) }

// Score is rec's score; ok=false (a null score) when any column it
// reads is null or absent on rec.
func (s *Scorer) Score(rec Record) (float64, bool) {
	if s.null {
		return 0, false
	}
	var buf [scorerStackTerms]float64
	x := buf[:0]
	if len(s.columns) > scorerStackTerms {
		x = make([]float64, 0, len(s.columns))
	}
	for _, c := range s.columns {
		v, ok := rec.NumericValue(c)
		if !ok {
			return 0, false
		}
		x = append(x, v)
	}
	eta := LinearPredictor(s.intercept, s.coefs, x)
	if s.inverse != nil {
		return s.inverse(eta), true
	}
	return eta, true
}
