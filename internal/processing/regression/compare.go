package regression

import (
	"math"
	"slices"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/regmultinom"
	"github.com/frankbardon/pulse/types"
)

// compare.go is the engine side of the Compose nested-model comparison
// (types.ComposedRequest.Compare, internal/regcompare): the whole-fit
// figures a comparison reads beyond the wire result
// (model.RegressionFigures), offered only to a run that asked for them.
// The Compose runtime runs every fit a comparison names with a
// model.VectorCapture on its bound set; the processor then resolves its
// regression factories through OfferingFigures, which hands each
// REG_OLS / REG_GLM / REG_MULTINOMIAL engine — and every
// extension-shaped engine whose model declares Compare
// (internal/regadapt) — the capture's offer. Nothing here reaches a
// result or the public API, and a run without a capture computes none.

// FiguresOfferer is an engine that can offer its comparison figures at
// finalize: the OLS, GLM, multinomial and modifier engines, and an extension-shaped
// model's engine (internal/regadapt) whose declaration carries Compare.
type FiguresOfferer interface {
	SetFiguresOffer(offer func(*model.RegressionFigures))
}

// OfferingFigures wraps resolve so every engine its factories build that
// can offer comparison figures offers them to offer at finalize (nil
// offer: resolve itself). The processor wraps its resolver this way when
// its bound set carries a capture (a Compose fit request).
func OfferingFigures(resolve Resolver, offer func(*model.RegressionFigures)) Resolver {
	if offer == nil {
		return resolve
	}
	return func(t types.RegressionType) (Factory, bool) {
		f, ok := resolve(t)
		if !ok || f == nil {
			return f, ok
		}
		return func(spec *types.RegressionSpec, schema *encoding.Schema) (Engine, error) {
			eng, err := f(spec, schema)
			if err == nil {
				if o, ok := eng.(FiguresOfferer); ok {
					o.SetFiguresOffer(offer)
				}
			}
			return eng, err
		}, true
	}
}

// olsFitFigures are an unpenalized, unbounded REG_OLS fit's comparison
// figures: the weighted RSS (R's deviance(lm)), the residual df N* −
// rank, σ̂² = RSS/df (NaN when df ≤ 0), and cov — the covariance the
// standard errors read, (p+1)-square over [intercept, predictors] — with
// the coefficients, on waldDF (the table's t df). A fit through the
// origin drops the intercept's row and column (not estimated).
func olsFitFigures(solve *olsSolveResult, predictors []string, cov [][]float64, waldDF float64) *model.RegressionFigures {
	terms := coefficientKeys(predictors)
	coefs := append([]float64{solve.Intercept}, solve.Coefficients...)
	if solve.throughOrigin {
		terms, coefs = terms[1:], coefs[1:]
		if cov != nil {
			trimmed := make([][]float64, len(cov)-1)
			for i := range trimmed {
				trimmed[i] = cov[i+1][1:]
			}
			cov = trimmed
		}
	}
	disp := math.NaN()
	if solve.DF > 0 {
		disp = solve.RSS / solve.DF
	}
	return &model.RegressionFigures{
		Terms:        terms,
		Coefficients: coefs,
		Covariance:   cov,
		WaldDF:       waldDF,
		Deviance:     solve.RSS,
		DFResidual:   solve.DF,
		Dispersion:   disp,
	}
}

// multinomialFigures are an unpenalized REG_MULTINOMIAL fit's
// comparison figures: Terms its "<category>:<term>" keys (category-major,
// the covariance's order), TermKeys each term's K − 1 keys — so a
// comparison tests (K − 1)·q coefficients for q added predictors — the
// covariance the standard errors read (the inverse information or the
// robust sandwich), the deviance −2ℓ, dispersion 1 (a likelihood ratio
// divides by none), a Wald z table (WaldDF 0), and the residual df
// (K − 1)·N* − d over the d coefficients: anova.multinom's Resid. df
// (nrow · (K − 1) − edf; N* the rep() expansion's under a frequency
// weight).
func multinomialFigures(pr *multinomialProblem, predictors []string, keys []string, beta []float64, cov [][]float64, deviance, nStar float64) *model.RegressionFigures {
	terms := append([]string{InterceptKey}, predictors...)
	termKeys := make(map[string][]string, len(terms))
	for _, c := range pr.nonRef {
		for _, t := range terms {
			termKeys[t] = append(termKeys[t], regmultinom.Key(pr.cats[c], t))
		}
	}
	k := float64(len(pr.nonRef))
	return &model.RegressionFigures{
		Terms:        slices.Clone(keys),
		TermKeys:     termKeys,
		Coefficients: slices.Clone(beta),
		Covariance:   cov,
		Deviance:     deviance,
		DFResidual:   float64(k*nStar) - float64(len(keys)),
		Dispersion:   1,
	}
}

func (e *olsEngine) SetFiguresOffer(offer func(*model.RegressionFigures))      { e.figures = offer }
func (e *glmEngine) SetFiguresOffer(offer func(*model.RegressionFigures))      { e.figures = offer }
func (e *modifierEngine) SetFiguresOffer(offer func(*model.RegressionFigures)) { e.figures = offer }
func (e *multinomialEngine) SetFiguresOffer(offer func(*model.RegressionFigures)) {
	e.figures = offer
}
