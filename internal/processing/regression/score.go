package regression

import (
	"fmt"
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
}

// scorerStackTerms is the predictor count Score evaluates without a heap
// allocation.
const scorerStackTerms = 16

// NewScorer builds b's scorer: every term with terms nil, else exactly
// the listed terms (model.InterceptKey for the intercept; a candidate
// predictor the fit's selection dropped scores 0 and reads no column),
// on scale (model.ScaleResponse — the default — or model.ScaleLink;
// REG_GLM only). The caller has run model.CheckScore; a GLM whose
// family / link has no inverse surfaces the engine's own refusal.
func NewScorer(b *model.Bound, terms []string, scale string) (*Scorer, error) {
	if b == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL, "NewScorer called with a nil model")
	}
	if len(b.Coefficients) != len(b.Predictors) {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			fmt.Sprintf("model %q has %d coefficients for %d predictors", b.Name, len(b.Coefficients), len(b.Predictors)),
			map[string]any{"name": b.Name})
	}
	s := &Scorer{}
	keep := func(term string) bool { return terms == nil || slices.Contains(terms, term) }
	if keep(model.InterceptKey) {
		s.intercept = b.Intercept
	}
	for i, p := range b.Predictors {
		if keep(p) {
			s.coefs = append(s.coefs, b.Coefficients[i])
			s.columns = append(s.columns, p)
		}
	}
	if b.Type == types.REG_GLM && scale != model.ScaleLink {
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
