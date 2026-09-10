package synth

import (
	"fmt"
	"math"
	mrand "math/rand/v2"
	"sort"

	"github.com/frankbardon/pulse/errors"
)

// This file is the generation-time half of the multi-predictor work:
// it turns a Spec.Models entry (FieldModelSpec, written by
// SpecFromProfile from a `profile create --fit-models` document) into a
// per-row draw for the numeric field that model targets.
//
// # The construction
//
// A modelled numeric is drawn as
//
//	value = Q( Phi( mu(row) + sigma*z ) )
//
// where mu(row) is the model's linear prediction for this row expressed
// on the STANDARD NORMAL scale, sigma is the residual scale on that
// same scale, z ~ N(0,1), Phi is the standard normal CDF and Q is the
// field's OWN quantile function. Phi and Q are the exact functions the
// Gaussian-copula stage already uses (phi / quantileFor in
// synth/copula.go) — deliberately reused rather than re-derived, so a
// modelled field and a correlated field agree on what "this field's
// marginal shape" means down to the last digit.
//
// # Why the prediction is standardised first
//
// The fit behind FieldModelSpec is an ordinary least-squares fit on RAW
// values (see profile_models.go), so Intercept + sum(coefficients) is a
// prediction in the field's own data units — spend in dollars, not in
// standard deviations. Phi only accepts a standard-normal argument, so
// the prediction is divided through by the field's own reconstructed
// marginal moments before it enters the copula:
//
//	u = (prediction - mean)/std + (residual_std/std)*z
//
// and the residual is scaled the same way so it keeps meaning "one
// residual standard deviation" after the division.
//
// The payoff is the identity the whole construction is anchored on. For
// a plain `normal` target — which is the ONLY shape SpecFromProfile
// ever emits a model for, see modelSpecFromProfile — quantileFor
// returns Q(u,p) = mean + std*u, so the round trip collapses to
//
//	value = mean + std*((prediction-mean)/std + (residual_std/std)*z)
//	      = prediction + residual_std*z
//
// i.e. exactly the textbook OLS draw, with no copula machinery visible
// in the answer. Routing it through Phi/Q anyway is what lets a target
// whose marginal is NOT Gaussian (a hand-authored uniform, lognormal or
// exponential field) keep its own marginal shape while still being
// driven by the linear predictor — the same reason correlator maps
// through Q instead of overriding with mean + std*u, see copula.go's
// v1-removed note.
//
// # Where this sits in the row
//
// The model stage runs LAST in drawRow, after every conditional
// resample stage has settled, because its predictors are categorical
// and set_* fields those stages can still be rewriting: a
// categorical-categorical pair can replace a predictor's drawn level,
// and a set-set pair can flip a predictor option's bit. Reading them
// before they are final would evaluate the model against a value the
// generated row does not actually carry.
//
// Nothing overwrites a modelled field afterwards, and nothing may:
// resolveConflicts claims the target for the model before any of the
// six pair/correlation stages get to bid (synth/conflict.go), so a
// categorical-numeric or set-numeric resample naming it is dropped with
// a warning rather than layered on top. That claim is what retires the
// parallel numeric resample stages for a modelled field.
//
// # z is INDEPENDENT here
//
// Each drawer takes its own fresh rng.NormFloat64(). Correlating the
// residuals ACROSS modelled fields is a separate, later change; until
// it lands, a Spec.Correlations entry naming a modelled field is
// reported as not-yet-honoured by resolveConflicts rather than being
// silently applied or silently dropped.

// modelDrawer is one FieldModelSpec compiled for the row loop: the
// linear predictor's terms, the target's own marginal moments and
// quantile function, and the clamp to apply to the result.
type modelDrawer struct {
	field string
	// order is the target's index in the schema's field order. Drawers
	// are sorted by it so the per-row sequence of rng.NormFloat64()
	// calls is fixed by the SCHEMA, never by the order the `models`
	// array happened to be written in and never by a map range — the
	// same determinism rule buildSchema's dictionary pre-registration
	// comment and weightedSamplerFromCounts's key sort already follow
	// (same spec + same seed must produce a byte-identical .pulse file;
	// see skills/synthetic-data.md, Determinism).
	order int

	intercept  float64
	predictors []modelPredictor

	// mean and invStd standardise the raw-scale prediction; residualZ is
	// the residual standard deviation already divided by std, so the
	// per-row work is two multiplies and no division.
	mean      float64
	invStd    float64
	residualZ float64

	quantile quantileFunc

	hasClamp float64Clamp
}

// float64Clamp is an optional inclusive [min, max] bound.
type float64Clamp struct {
	active   bool
	min, max float64
}

func (c float64Clamp) apply(v float64) float64 {
	if !c.active {
		return v
	}
	if v < c.min {
		return c.min
	}
	if v > c.max {
		return c.max
	}
	return v
}

// modelPredictor is one compiled (field, level) indicator term.
type modelPredictor struct {
	// isSet distinguishes the two indicator semantics carried on
	// ModelPredictorSpec.Kind. A categorical_level term reads the row's
	// drawn level for field and fires on an exact string match; a
	// set_option term reads the row's selection map for a set_* field
	// and fires when that option's bit is on.
	isSet bool
	field string
	level string
	coef  float64
}

// buildModelDrawers compiles the SURVIVING models — the subset
// resolveConflicts left after arbitration, not Spec.Models verbatim —
// into row-loop drawers, returning any warning the compilation itself
// produced.
//
// Two failures are warned-and-skipped rather than fatal, matching how
// every other conditional stage's builder treats a shape it cannot use
// (SpecFromProfile bypasses validateSpec, so these are reachable from a
// profile-derived spec and must not take the whole generation down):
// a target that is not in the schema at all, and a target whose
// reconstructed marginal is degenerate. A predictor KIND generation
// cannot evaluate is fatal instead — silently contributing zero for it
// would publish a cohort carrying a relationship the spec asked for and
// generation never applied, which is the fabricated-structure failure
// mode this package refuses on principle.
func buildModelDrawers(models []FieldModelSpec, wfs []*writerField) ([]*modelDrawer, []string, error) {
	if len(models) == 0 {
		return nil, nil, nil
	}
	idx := make(map[string]int, len(wfs))
	for i, wf := range wfs {
		idx[wf.spec.Name] = i
	}

	out := make([]*modelDrawer, 0, len(models))
	var warnings []string
	for _, m := range models {
		pos, ok := idx[m.Field]
		if !ok {
			warnings = append(warnings, fmt.Sprintf(
				"model not applied: target field %q is not declared in the schema", m.Field))
			continue
		}
		fs := wfs[pos].spec

		// fieldMoments doubles as the eligibility check: it accepts
		// exactly the four distributions that carry a closed-form
		// (mean, std) AND a closed-form quantile, and refuses anything
		// else by name rather than approximating it. A DistMixture
		// target (`--fit-shape`) lands in that refusal, which is
		// correct for now — a captured mixture has no quantile the
		// linear predictor could ride, and modelSpecFromProfile never
		// emits a model for one, so only a hand-authored spec can reach
		// it.
		mean, std, marginMin, marginMax, marginHasClamp, err := fieldMoments(fs)
		if err != nil {
			return nil, nil, err
		}
		if std <= 0 || math.IsNaN(std) || math.IsInf(std, 0) {
			// A degenerate or non-finite marginal has no scale to read
			// the prediction against: (prediction-mean)/std is
			// undefined, and there is no honest value to substitute — a
			// field whose marginal says "always exactly mean" cannot
			// also carry the variation the model describes. Every
			// sampler for the four eligible distributions already
			// refuses a zero scale at buildSchema time, so the
			// reachable case here is the OVERFLOW one (a lognormal with
			// a large sigma whose analytic std evaluates to +Inf); the
			// zero and NaN arms are the same refusal held defensively.
			// Fall back to the field's own draw and say so.
			warnings = append(warnings, fmt.Sprintf(
				"model not applied: target field %q has a degenerate marginal (std %g), which carries no scale to standardise the prediction against",
				m.Field, std))
			continue
		}
		q, qerr := quantileFor(fs, mean, std)
		if qerr != nil {
			return nil, nil, qerr
		}

		d := &modelDrawer{
			field:     m.Field,
			order:     pos,
			intercept: m.Intercept,
			mean:      mean,
			invStd:    1 / std,
			residualZ: m.ResidualStd / std,
			quantile:  q,
		}

		// CLAMPING. Exactly one clamp is applied, once, to the final
		// data-scale value — never to the standardised latent, where a
		// bound in data units means nothing.
		//
		// The model's own Min/Max win when it declares them. They are
		// the target's OBSERVED bounds, carried on FieldModelSpec for
		// precisely this purpose, and preferring them mirrors
		// categoricalNumericPairSampler.transform, which is this
		// package's existing precedent for a conditionally-drawn
		// numeric: a conditional draw is bounded by what the source
		// data actually contained, not by whatever the independent
		// marginal happened to declare. For a profile-derived spec the
		// two are the same numbers (SpecFromProfile writes the same
		// NumericProfile Min/Max into both), so this preference changes
		// nothing there; it matters only for a hand-authored spec,
		// where an explicit model bound should not be silently
		// overridden by the marginal's.
		//
		// Falling back to the marginal's own clamp when the model
		// declares none keeps a modelled `normal` field bounded exactly
		// as the copula stage bounds the same field (correlator
		// .transform applies fieldMoments' clamp), so the two stages
		// cannot disagree about the field's admissible range.
		switch {
		case m.HasClamp:
			d.hasClamp = float64Clamp{active: true, min: m.Min, max: m.Max}
		case marginHasClamp:
			d.hasClamp = float64Clamp{active: true, min: marginMin, max: marginMax}
		}

		d.predictors = make([]modelPredictor, 0, len(m.Predictors))
		for _, pr := range m.Predictors {
			switch pr.Kind {
			case ModelPredictorCategoricalLevel:
				d.predictors = append(d.predictors, modelPredictor{
					field: pr.Field, level: pr.Level, coef: pr.Coefficient,
				})
			case ModelPredictorSetOption:
				d.predictors = append(d.predictors, modelPredictor{
					isSet: true, field: pr.Field, level: pr.Level, coef: pr.Coefficient,
				})
			default:
				return nil, nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
					fmt.Sprintf("field %q: model predictor kind %q cannot be evaluated at generation time", m.Field, pr.Kind),
					map[string]any{"field": m.Field, "predictor": pr.Field, "kind": pr.Kind})
			}
		}
		out = append(out, d)
	}

	// Schema order, decided once here rather than per row. Two models
	// can never share a target (validateSpec refuses a duplicate and
	// SpecFromProfile emits one per field), so the keys are distinct;
	// the stable sort is belt-and-braces for a spec that reached this
	// point unvalidated.
	sort.SliceStable(out, func(i, j int) bool { return out[i].order < out[j].order })
	return out, warnings, nil
}

// transform replaces row[field] with the composed model draw described
// on this file's header comment.
//
// # Predictor evaluation
//
// A term fires only on an exact match, and a term that does not fire
// contributes NOTHING — which is the correct reading of the design the
// coefficients came from, not a convenience default. Categorical
// predictors are dummy-coded against a DROPPED reference level whose
// effect is folded into the intercept (see FieldModel.References), so
// "no term fired for this field" is precisely the statement "this row
// sits at that field's baseline". A level the fit NEVER SAW — a
// generation-time value that was absent from, or collapsed out of, the
// captured cohort — therefore contributes zero and is treated as the
// reference level. That is the only answer the model supports: it holds
// no coefficient for that level, and inventing one (or reweighting the
// others) would fabricate structure the data never showed.
//
// A predictor whose row value is NULL likewise contributes zero. The
// fit applied listwise deletion (regression.FitForAttribute), so no row
// with a null in that field ever reached the solver and its
// coefficients say nothing about such rows; falling back to the
// baseline is the one reading consistent with how they were estimated.
//
// # The residual draw is unconditional
//
// rng.NormFloat64() is called exactly once per drawer per row, before
// any branch on the prediction, so the seeded stream depends only on
// the NUMBER of surviving drawers — never on which predictors happened
// to fire, nor on whether ResidualStd is zero. A deterministic model
// (ResidualStd == 0) still consumes its draw and multiplies it by zero.
func (d *modelDrawer) transform(rng *mrand.Rand, row map[string]any, nullMask map[string]bool) {
	prediction := d.intercept
	for i := range d.predictors {
		p := &d.predictors[i]
		if nullMask[p.field] {
			continue
		}
		if p.isSet {
			if setOptionMap(row, p.field)[p.level] {
				prediction += p.coef
			}
			continue
		}
		if level, ok := row[p.field].(string); ok && level == p.level {
			prediction += p.coef
		}
	}

	z := rng.NormFloat64()
	u := (prediction-d.mean)*d.invStd + d.residualZ*z
	row[d.field] = d.hasClamp.apply(d.quantile(u, phi(u)))
}
