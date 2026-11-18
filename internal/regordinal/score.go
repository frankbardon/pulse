package regordinal

import (
	"fmt"
	"math"
	"slices"

	"github.com/frankbardon/pulse/extend"
	pmodel "github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// score.go scores a bound REG_ORDINAL fit (ATTR_REG_PREDICT, U68 #650).
// Fitted is the fit rebuilt from what a saved model file keeps — the
// coefficients (predictors and "<lo>|<hi>" thresholds) and
// outputs.link / outputs.levels — so a fit and its reloaded file score
// identically; it is the categorical-outcome ProbabilityScorer
// (internal/model) every ordinal column, holdout score and per-level
// effect reads. ParamScorer turns one ATTR_REG_PREDICT reference into
// its column: P(y = level), P(y ≤ level), the expected level, the class,
// or the linear predictor (params.scale link).

// Fitted is a fitted proportional-odds model: P(Y ≤ k | x) =
// F(ζ_k − xᵀβ). Immutable; safe for concurrent use.
type Fitted struct {
	predictors []string
	beta       []float64
	zeta       []float64
	link       link
	levels     []string
}

var _ pmodel.ProbabilityScorer = (*Fitted)(nil)

// NewFitted rebuilds the model from fit: outputs.link, outputs.levels
// (3..MaxLevels) and the coefficients of every predictor and threshold.
func NewFitted(fit extend.FittedRegression) (*Fitted, error) {
	name, _ := fit.Outputs["link"].(string)
	l, ok := links[name]
	if !ok {
		return nil, fmt.Errorf("%s scorer: outputs.link %v is not one of %v", Name, fit.Outputs["link"], Links())
	}
	levels, ok := pmodel.OutcomesOf(fit.Outputs)
	if !ok || len(levels) < 2 {
		return nil, fmt.Errorf("%s scorer: outputs.levels %v is not a list of at least two levels", Name, fit.Outputs["levels"])
	}
	f := &Fitted{predictors: slices.Clone(fit.Predictors), link: l, levels: levels,
		beta: make([]float64, len(fit.Predictors)), zeta: make([]float64, len(levels)-1)}
	for j, x := range fit.Predictors {
		v, ok := fit.Coefficients[x]
		if !ok {
			return nil, fmt.Errorf("%s scorer: no coefficient for predictor %q", Name, x)
		}
		f.beta[j] = v
	}
	for k := range f.zeta {
		key := levels[k] + ThresholdSeparator + levels[k+1]
		v, ok := fit.Coefficients[key]
		if !ok {
			return nil, fmt.Errorf("%s scorer: no threshold coefficient %q", Name, key)
		}
		f.zeta[k] = v
	}
	return f, nil
}

// Columns are the predictors, in fit order.
func (f *Fitted) Columns() []string { return slices.Clone(f.predictors) }

// Outcomes are the levels, lowest first.
func (f *Fitted) Outcomes() []string { return slices.Clone(f.levels) }

// Linear is xᵀβ for row; false when a predictor is null, absent or
// non-finite.
func (f *Fitted) Linear(row pmodel.Row) (float64, bool) {
	eta := 0.0
	for j, x := range f.predictors {
		v, ok := row.NumericValue(x)
		if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, false
		}
		eta += float64(v * f.beta[j])
	}
	return eta, true
}

// Probabilities writes P(Y = k | x) = F(ζ_k − η) − F(ζ_{k−1} − η) (ζ_0 =
// −∞, ζ_K = +∞) into out, the fit's own per-row kernel
// (cumulativeProbabilities).
func (f *Fitted) Probabilities(row pmodel.Row, out []float64) bool {
	eta, ok := f.Linear(row)
	if !ok {
		return false
	}
	cumulativeProbabilities(f.link, f.zeta, eta, out)
	return true
}

// cumulativeProbabilities is the level probabilities at linear
// predictor eta: the differences of F(ζ_k − η), written into out (len
// len(zeta) + 1). The fit's mean fitted probabilities and every row
// score read it, so they agree bit for bit.
func cumulativeProbabilities(l link, zeta []float64, eta float64, out []float64) {
	prev := 0.0
	for k := range out {
		cur := 1.0
		if k < len(zeta) {
			cur, _, _ = l.eval(zeta[k] - eta)
		}
		out[k] = cur - prev
		prev = cur
	}
}

// Scorer is REG_ORDINAL's whole-model scorer (RegressionRegistration
// shape): the expected level, ATTR_REG_PREDICT's default.
func Scorer(fit extend.FittedRegression) (extend.RowScorer, error) {
	return ParamScorer(fit, pmodel.ScoreParams{})
}

// ParamScorer is the column one ATTR_REG_PREDICT reference p scores over
// fit (model.CheckScoreFor has judged p): the linear predictor under
// params.scale link, else model.OutcomeScorer's output over the fit's
// probability vector.
func ParamScorer(fit extend.FittedRegression, p pmodel.ScoreParams) (extend.RowScorer, error) {
	f, err := NewFitted(fit)
	if err != nil {
		return nil, err
	}
	if p.Scale == pmodel.ScaleLink {
		return linearScorer{f}, nil
	}
	out := p.EffectiveOutput(Name)
	k := -1
	if p.Level != "" {
		if k = slices.Index(f.levels, p.Level); k < 0 {
			return nil, fmt.Errorf("%s scorer: params.level %q is not one of the fit's levels %v", Name, p.Level, f.levels)
		}
	}
	rs, err := pmodel.OutcomeScorer(f, out, k)
	if err != nil {
		return nil, err
	}
	return rowScorer{rs}, nil
}

// rowScorer is a model.RowScorer in the extend contract's shape.
type rowScorer struct{ rs pmodel.RowScorer }

func (s rowScorer) Columns() []string                         { return s.rs.Columns() }
func (s rowScorer) Score(row extend.ScoreRow) (float64, bool) { return s.rs.Score(row) }

// linearScorer scores η = xᵀβ (params.scale link).
type linearScorer struct{ f *Fitted }

func (s linearScorer) Columns() []string                         { return s.f.Columns() }
func (s linearScorer) Score(row extend.ScoreRow) (float64, bool) { return s.f.Linear(row) }

// Scorers are the package's row-scorer factories keyed by type (the
// model-regression wiring, internal/regadapt, reads them beside
// Factories).
var Scorers = map[types.RegressionType]extend.RegressionScorerFactory{
	types.REG_ORDINAL: Scorer,
}

// ParamScorers are the scorers that read the ATTR_REG_PREDICT reference
// (params.level / output / scale), keyed by type: the registration
// adapter prefers one over the type's Scorers entry.
var ParamScorers = map[types.RegressionType]func(extend.FittedRegression, pmodel.ScoreParams) (extend.RowScorer, error){
	types.REG_ORDINAL: ParamScorer,
}

// Probabilities are the package's probability-vector factories keyed by
// type: the whole per-row vector a holdout's proper scores read (the
// model-regression wiring, internal/regadapt, registers them on the
// engine beside Scorers).
var Probabilities = map[types.RegressionType]func(extend.FittedRegression) (pmodel.ProbabilityScorer, error){
	types.REG_ORDINAL: func(fit extend.FittedRegression) (pmodel.ProbabilityScorer, error) {
		f, err := NewFitted(fit)
		if err != nil {
			return nil, err
		}
		return f, nil
	},
}
