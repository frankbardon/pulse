package synth

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/processing/regression"
	"github.com/frankbardon/pulse/types"
)

// This file is the model-recovery half of the fidelity report (E5-S1):
// for every linear model generation ACTUALLY APPLIED, it refits that
// same model on the output cohort's `_synthetic` partition and reports
// the captured coefficient beside the recovered one, per predictor.
//
// # Why a refit rather than another marginal delta
//
// Every other section of this report answers "does the generated
// partition look like the source" — a marginal, a correlation, a
// contingency table. None of them answers the question a multi-predictor
// capture actually raises, which is "did generation reproduce the
// STRUCTURE that was captured". Those are different questions and the
// difference is not academic: the effort that introduced `--fit-models`
// shipped two silent structure-loss bugs (v0.32.2's fidelity report
// scoring pairs generation never applied, and E2-S5's top-K catch-all
// killing 85 of 105 captured models outright), and in both cases every
// marginal in the report stayed healthy while the conditioning was
// gone. A cohort with no conditioning in it is still a plausible cohort.
// This section is the instrument that fails loudly in that situation.
//
// It also settles a question the marginal sections cannot even pose.
// Measured on the motivating cohort, a modelled numeric's mean spread
// across a predictor's levels is visibly COMPRESSED against the source
// (`nps` across `educationLevel`: 1.85 in source, 0.73 synthetic). Two
// worlds produce that number and they call for opposite responses:
//
//   - Recovered coefficients MATCH the captured ones. Generation is
//     faithful and the compression is the legitimate gap between a
//     PARTIAL effect (what a coefficient is) and a MARGINAL contrast
//     (what a mean-by-level table shows) — widened further because the
//     predictor fields are drawn from their own marginals, so the
//     source's predictor-predictor confounding is absent by design.
//
//   - Recovered coefficients are themselves ATTENUATED. Generation is
//     losing structure and the compression is a real fault.
//
// Reading the per-predictor deltas below is what tells those apart.
//
// # Why the refit lives HERE, in package synth
//
// synth_fidelity.go (at the module root) carries a doc comment
// explaining that the fidelity bridge cannot live in this package
// because synth cannot import processing — descriptor imports synth, and
// processing's own tests import descriptor. That constraint is real and
// it is why TestRunner is an injected function type. It does NOT apply
// to this file: the refit needs processing/regression, a subpackage
// whose imports are encoding + errors + types only, which this package
// already depends on for capture (see synth/regression_record.go). So
// there is nothing to inject and no reason to route the code through the
// facade; the acceptance criterion that capture and recovery use the
// SAME fitting path is easiest to keep true when the two calls sit in
// one package and share one Record adapter.
//
// # Why the comparison is on the LATENT scale
//
// A modelled numeric is drawn as value = Q(Φ(μ + σz)) where μ carries
// the coefficients (synth/model_draw.go). For a `normal` target the
// round trip collapses to prediction + residual_std·z and a coefficient
// reads as a data-scale effect; for a lognormal, uniform, exponential or
// captured-mixture target the map from latent to value is NON-LINEAR and
// the same coefficient moves the value by an amount that depends where
// in the distribution the row landed. Regressing raw generated values on
// the dummies would therefore compare a value-space effect against a
// latent coefficient, which is the trap this section exists to avoid: it
// would report every `--fit-shape` target as badly recovered while the
// generation path was exactly correct. Each generated value is instead
// inverted back to its latent through latentFor (synth/copula.go, the
// algebraic inverse of the quantileFor the draw used) BEFORE the refit,
// and the captured coefficient is divided by the target's own marginal
// std, which is precisely the division buildModelDrawers performs. Both
// sides then live in units of one target standard deviation, which also
// makes a single unit-free tolerance meaningful across every field.

// ModelRecoveryTolerance is the absolute latent-scale gap at which a
// recovered coefficient is FLAGGED as evidence of a generation fault
// rather than merely reported.
//
// The unit is one standard deviation of the target's own reconstructed
// marginal (see this file's header on the latent scale), so the number
// carries the same meaning for a dollar-denominated spend field and an
// 11-point NPS scale — which is the whole reason the comparison is put
// on that scale before a threshold is applied to it. 0.10 SD is half of
// Cohen's small-effect boundary: a coefficient recovered within it is
// describing the same relationship the capture described, whatever the
// remaining digits do.
//
// It is a floor, not the whole test. A coefficient estimated from a few
// thousand synthetic rows carries real sampling noise — a level at 5%
// prevalence over 2,000 rows has a standard error near 0.10 on its own —
// so a gap is flagged only when it exceeds BOTH this floor and
// modelRecoverySEMultiple times the refit's own standard error for that
// coefficient. A gap smaller than the estimator's noise cannot be
// evidence of anything, and a report that cries wolf on thin levels is
// one nobody reads to the bottom of.
const ModelRecoveryTolerance = 0.10

// modelRecoverySEMultiple scales the refit's own reported standard error
// into the second half of the flagging band above. Two standard errors
// is the ordinary ~95% reading; the band is max(floor, 2·SE) rather than
// a sum, so a well-supported coefficient is judged against the fixed
// floor and a thin one against its own noise.
const modelRecoverySEMultiple = 2.0

// modelRecoveryRowCap bounds how many synthetic rows the refits consume.
//
// It is deliberately the same 10,000 as capture's own row snapshot
// (modelResidualCap): the captured coefficients this section compares
// against were themselves estimated from a 10,000-row reservoir, so
// spending more rows here would not make the comparison sharper, it
// would only make one side of it sharper than the other. The cost side
// matters too — an OLS accumulator is O(p²) per row and the motivating
// cohort carries 55 applied models over designs up to 126 columns wide,
// so an uncapped 50,000-row refit is minutes of work bolted onto a
// report that is supposed to be a footnote to generation. Generation
// draws rows independently, so the leading N are as representative as
// any other N and taking them needs no sampler and no RNG.
const modelRecoveryRowCap = 10000

// ModelFidelity is one row of FidelityReport.Models: one APPLIED linear
// model's captured coefficients beside the coefficients recovered by
// refitting the same model on the output cohort's `_synthetic`
// partition.
//
// Entries exist for exactly the models generation applied — the subset
// resolveConflicts left, further narrowed by buildModelDrawers' own
// warn-and-skip refusals, reproduced by calling both functions again
// against the same *Spec generate() was given. A field carrying no model
// has NO entry here (rather than an entry with empty values), and
// neither does a captured model that lost its target to an earlier claim
// or whose marginal generation refused: reporting a computed-looking
// delta for a relationship that was never applied is the v0.32.2 bug
// class, and it is worse than reporting nothing because the number looks
// like evidence.
type ModelFidelity struct {
	Field string `json:"field"`
	// Marginal marks an INTERCEPT-ONLY model — one whose predictor
	// selection admitted nothing (see profile_models_select.go's variance
	// floor). It is a COMPLETE model, not a failed capture: the linear
	// predictor is the field's own mean and the residual carries all of
	// its spread, so generation reproduces the marginal, which is the
	// correct answer for a field nothing explains. Such an entry carries
	// no Predictors and reports only the intercept comparison — which is
	// still a real check, since a drifted intercept on a marginal model
	// means the marginal itself did not survive.
	Marginal bool `json:"marginal,omitempty"`
	// LatentScale is the target's reconstructed marginal standard
	// deviation — the divisor that puts every captured coefficient below
	// on the latent scale. Multiply a latent figure by it to return to
	// the units the profile document's `models` section reports, which
	// for a `normal` target are the same units either way.
	LatentScale float64 `json:"latent_scale"`
	// CapturedIntercept / RecoveredIntercept are the latent-scale
	// intercepts: (Intercept − mean)/std as generation standardises it,
	// against the refit's own. InterceptDelta is their absolute
	// difference.
	CapturedIntercept  float64 `json:"captured_intercept"`
	RecoveredIntercept float64 `json:"recovered_intercept,omitempty"`
	InterceptDelta     float64 `json:"intercept_delta,omitempty"`
	// Predictors is one entry per compiled model term, in the order
	// generation evaluates them. Absent on a Marginal model.
	Predictors []*ModelPredictorFidelity `json:"predictors,omitempty"`
	// NObs is the number of synthetic rows the refit admitted: rows
	// carrying a non-null, latent-invertible target and a non-null value
	// for every predictor SOURCE field (listwise deletion, the same rule
	// capture's fit applied). Bounded by modelRecoveryRowCap.
	NObs int `json:"n_obs"`
	// R2 is the refit's own coefficient of determination on the latent
	// scale — how much of the generated latent the recovered model
	// explains. It is NOT comparable to the captured FieldModel.R2, which
	// was measured on real source rows against real source noise;
	// synthetic residuals are Gaussian by construction, so this figure
	// reads high and is here to characterise the refit, not to score it.
	R2 float64 `json:"r2,omitempty"`
	// MaxDelta is the largest absolute latent coefficient gap across
	// Predictors (the intercept excluded — a shifted intercept moves the
	// level, not the structure). Flagged is true when any predictor is.
	MaxDelta float64 `json:"max_delta,omitempty"`
	Flagged  bool    `json:"flagged,omitempty"`
	// Error is set instead of the recovered figures when the refit could
	// not run at all for this model — too few admitted rows, a design the
	// solver refused. It never withholds the rest of the report, the same
	// non-fatal per-entry contract every other Fidelity kind follows.
	Error string `json:"error,omitempty"`
}

// ModelPredictorFidelity is one compiled model term's recovery: the
// captured coefficient generation used, on the latent scale, beside the
// coefficient a refit of the same term on the generated rows recovers.
type ModelPredictorFidelity struct {
	// Kind / Field / Level address the term exactly as
	// ModelPredictorSpec does, so an entry here zips against the profile
	// document's `models[].predictors[]` without a join key.
	Kind  string `json:"kind"`
	Field string `json:"field"`
	Level string `json:"level"`
	// CapturedCoefficient is the spec's coefficient divided by
	// LatentScale — the quantity buildModelDrawers actually adds into the
	// latent, not the raw number the profile document carries.
	CapturedCoefficient float64 `json:"captured_coefficient"`
	// RecoveredCoefficient is the refit's estimate for the same term.
	// StdError is the refit's own standard error for it, which is the
	// second half of the flagging band — see ModelRecoveryTolerance.
	RecoveredCoefficient float64 `json:"recovered_coefficient,omitempty"`
	StdError             float64 `json:"std_error,omitempty"`
	Delta                float64 `json:"delta,omitempty"`
	// NFired is the number of admitted synthetic rows on which this
	// term's indicator was 1. It is the term's own support and it is what
	// distinguishes the two ways a recovered coefficient reaches zero: a
	// term that fired thousands of times and recovered nothing is a
	// generation fault, while a term that never fired had nothing to
	// recover — the level was captured but the field's reconstructed
	// marginal does not generate it.
	NFired int `json:"n_fired"`
	// Flagged is true when Delta exceeds both ModelRecoveryTolerance and
	// modelRecoverySEMultiple standard errors. False whenever Error is
	// set: a term that was not estimated has no gap to judge.
	Flagged bool `json:"flagged,omitempty"`
	// Error is set instead of the recovered figures when this term could
	// not be carried into the refit at all — its level is absent from the
	// output cohort's dictionary, or its column is constant over the
	// admitted rows and so carries no information to estimate from. Both
	// are informative outcomes rather than failures, and both leave every
	// OTHER term of the same model estimated.
	Error string `json:"error,omitempty"`
}

// BuildModelFidelity appends one ModelFidelity entry per APPLIED model
// onto report.Models. mergedSchema/records must be the same physical
// schema and record buffer BuildFidelityReport was given.
//
// spec is the Spec generate() was given, unpruned. Which models were
// applied is not guessed from it: resolveConflicts and buildModelDrawers
// are both pure functions of the Spec (conflicts are static, and drawer
// compilation consumes no RNG), so calling them again here reproduces
// the exact surviving set generation compiled — including the two
// warn-and-skip refusals buildModelDrawers makes on its own, a target
// absent from the schema and a target whose marginal has no scale. That
// is the whole of how this section keeps the v0.32.2 promise: it cannot
// report a model generation did not run, because it asks generation's
// own compiler which ones those were.
//
// The synthetic partition is decoded ONCE (decodeSyntheticRows, the
// cache v0.32.1 introduced to kill an O(pairs × records) blowup in this
// same report) and every model refits over that one slice. A refit per
// field with its own full decode would be exactly that bug in a new
// place, at 105 fields.
func BuildModelFidelity(report *FidelityReport, mergedSchema *encoding.Schema, records []byte, spec *Spec) {
	if report == nil || spec == nil || len(spec.Models) == 0 {
		return
	}
	// buildSchema is how generation turns FieldSpecs into the writerFields
	// buildModelDrawers reads its marginals from. An error here means this
	// spec could not have generated the cohort in hand at all, so there is
	// no applied model to report and nothing honest to say about one.
	_, wfs, err := buildSchema(spec)
	if err != nil {
		return
	}
	drawers, _, err := buildModelDrawers(resolveConflicts(spec).models, wfs)
	if err != nil || len(drawers) == 0 {
		return
	}

	rows := decodeSyntheticRows(mergedSchema, records)
	if len(rows) > modelRecoveryRowCap {
		rows = rows[:modelRecoveryRowCap]
	}

	specOf := make(map[string]FieldSpec, len(spec.Fields))
	for _, fs := range spec.Fields {
		specOf[fs.Name] = fs
	}
	// residualsOf carries each refit's own per-row latent residuals out
	// of the loop so the residual-correlation half (E5-S2) rides the
	// SAME decode and the SAME refits rather than repeating either. The
	// map is only ever read by key; nothing folds over it, so its
	// iteration order never reaches a number (E2-S4).
	residualsOf := make(map[string]*recoveredResiduals, len(drawers))
	for _, d := range drawers {
		entry, resid := computeModelFidelity(mergedSchema, rows, d, specOf[d.field])
		report.Models = append(report.Models, entry)
		if resid != nil {
			residualsOf[d.field] = resid
		}
	}
	report.ModelResidualCorrelations = buildModelResidualFidelity(spec, drawers, residualsOf)
}

// recoveryTerm pairs one compiled model term with the design column that
// will estimate it, so the emitted entry and the fitted column can never
// drift apart by index.
type recoveryTerm struct {
	entry *ModelPredictorFidelity
	col   dummyColumn
	// live is false for a term that could not be carried into the design
	// (unresolvable level, or a constant column). Its entry still ships,
	// carrying Error and its captured coefficient — the reader needs to
	// see that the term exists and was not estimated, which is a
	// different statement from "it recovered zero".
	live bool
}

// computeModelFidelity refits one applied model on the cached synthetic
// rows and renders the comparison.
//
// The target is inverted to its latent through latentFor before the
// refit — see this file's header for why comparing raw values would be
// comparing a value-space effect against a latent coefficient. The
// design columns are built from the drawer's OWN compiled terms rather
// than re-expanded from the schema, which is what keeps "same fitting
// path as capture" honest in the one place the two could legitimately
// diverge: capture's top-K catch-all column is a membership test against
// a retained level set, but generation evaluates the surviving spec term
// as an exact match on the literal level text `otherCategoryLabel`
// (modelDrawer.transform), and the recovery must reproduce what
// GENERATION did, not what the fitter did.
//
// The second return is the refit's own per-row latent residuals over
// the admitted rows, row-aligned with rows, or nil when no refit ran.
// They are produced HERE rather than by a second pass because they are
// a by-product of a fit that has already happened: the residual
// correlation section (E5-S2) needs the residuals of a model fitted on
// the synthetic partition, which is precisely what this function just
// fitted, and re-deriving them would mean either a second refit or —
// worse — residuals taken against the CAPTURED coefficients, which
// would fold this section's own coefficient gaps into a correlation and
// report one finding twice.
func computeModelFidelity(mergedSchema *encoding.Schema, rows []syntheticRow, d *modelDrawer, fs FieldSpec) (*ModelFidelity, *recoveredResiduals) {
	std := 1 / d.invStd
	entry := &ModelFidelity{
		Field:             d.field,
		LatentScale:       std,
		CapturedIntercept: (d.intercept - d.mean) * d.invStd,
		Marginal:          len(d.predictors) == 0,
	}

	latent, err := latentFor(fs, d.mean, std)
	if err != nil {
		// Unreachable for a drawer that compiled: buildModelDrawers
		// already ran fieldMoments and quantileFor over the same
		// FieldSpec, and latentFor accepts exactly that set. Held rather
		// than assumed, because the two switches drifting apart is the
		// one way this section could silently stop covering a
		// distribution.
		entry.Error = err.Error()
		return entry, nil
	}

	terms := resolveRecoveryTerms(mergedSchema, d, std)
	for _, t := range terms {
		entry.Predictors = append(entry.Predictors, t.entry)
	}

	// Pass one: fix the admitted row set and count each term's support.
	// Admission is decided against the FULL candidate design — every
	// predictor source field non-null — and then held fixed for the refit
	// even after a degenerate column is dropped below, so NFired and NObs
	// describe one admission rule rather than two.
	admitted, ok := admitRecoveryRows(rows, d, terms, latent)
	entry.NObs = len(admitted)
	if !ok {
		entry.Error = fmt.Sprintf(
			"synthetic partition admits fewer than two rows carrying %s and every predictor of its model", d.field)
		return entry, nil
	}

	if entry.Marginal {
		// An intercept-only model has no design to solve: the recovered
		// intercept IS the mean latent over the admitted rows, which is
		// what an OLS fit with no regressors returns. Computing it
		// directly rather than driving a zero-column engine mirrors
		// fieldFit.finalize's own intercept-only arm at capture.
		var sum float64
		for _, ri := range admitted {
			u, _ := latent(rows[ri].values[d.field])
			sum += u
		}
		entry.RecoveredIntercept = sum / float64(len(admitted))
		entry.InterceptDelta = math.Abs(entry.RecoveredIntercept - entry.CapturedIntercept)
		// A marginal model's residual is the row's whole deviation from
		// its own recovered mean, which is what an intercept-only fit
		// leaves over. It participates in the residual correlation
		// exactly as a predictor-carrying model does — the same
		// symmetry computeResidualCorrelations applies at capture, and
		// for the same reason: a field nothing explains still has a
		// residual, and it is the field's own centred latent.
		resid := newRecoveredResiduals(len(rows))
		for _, ri := range admitted {
			u, _ := latent(rows[ri].values[d.field])
			resid.set(ri, u-entry.RecoveredIntercept)
		}
		return entry, resid
	}

	live := liveRecoveryColumns(terms)
	if len(live) == 0 {
		entry.Error = "no model term carries information on the synthetic partition: every design column is absent or constant"
		return entry, nil
	}

	res, ferr := fitRecovery(mergedSchema, rows, admitted, d, live, latent)
	if ferr != "" {
		entry.Error = ferr
		return entry, nil
	}

	entry.RecoveredIntercept = res.Coefficients[regression.InterceptKey]
	entry.InterceptDelta = math.Abs(entry.RecoveredIntercept - entry.CapturedIntercept)
	entry.R2 = res.R2
	for _, t := range terms {
		if !t.live {
			continue
		}
		t.entry.RecoveredCoefficient = res.Coefficients[t.col.Name]
		t.entry.StdError = res.StdErrors[t.col.Name]
		t.entry.Delta = math.Abs(t.entry.RecoveredCoefficient - t.entry.CapturedCoefficient)
		band := ModelRecoveryTolerance
		if se := modelRecoverySEMultiple * t.entry.StdError; se > band {
			band = se
		}
		t.entry.Flagged = t.entry.Delta > band
		if t.entry.Delta > entry.MaxDelta {
			entry.MaxDelta = t.entry.Delta
		}
		if t.entry.Flagged {
			entry.Flagged = true
		}
	}
	return entry, recoveryResidualsFor(rows, admitted, d, terms, latent, res)
}

// recoveryResidualsFor evaluates the refit's own residual for every
// admitted row: the row's latent target minus the refit's prediction
// for it, using the RECOVERED coefficients.
//
// Only live terms contribute, and that is exact rather than an
// approximation. A term dropped by admitRecoveryRows is constant over
// the admitted rows, so its contribution is either identically zero (it
// never fires) or a constant the recovered intercept has already
// absorbed (it always fires) — and a correlation is invariant to a
// constant shift either way.
func recoveryResidualsFor(rows []syntheticRow, admitted []int, d *modelDrawer, terms []*recoveryTerm, latent latentFunc, res *types.RegressionResult) *recoveredResiduals {
	out := newRecoveredResiduals(len(rows))
	intercept := res.Coefficients[regression.InterceptKey]
	for _, ri := range admitted {
		row := &rows[ri]
		u, ok := latent(row.values[d.field])
		if !ok {
			// Unreachable: admission already required invertibility.
			continue
		}
		pred := intercept
		for _, t := range terms {
			if !t.live {
				continue
			}
			if recoveryIndicator(row, t.col) == 1 {
				pred += res.Coefficients[t.col.Name]
			}
		}
		out.set(ri, u-pred)
	}
	return out
}

// resolveRecoveryTerms turns each compiled model term into the design
// column that estimates it, against the OUTPUT cohort's dictionaries.
//
// A term whose level the output cohort never carries gets no column and
// an Error saying so. That is a real and expected outcome rather than a
// defect, and the honest report of it is "this term was never estimable
// here", not a recovered zero sitting beside a captured 0.4.
//
// It is NOT the otherCategoryLabel catch-all, which is reached through
// the categorical-pair stage and fires freely — on the motivating cohort
// all 54 catch-all terms fire, 3,992 to 9,046 rows each. The cause is
// that a model's retained level set and Categorical.Top's are ranked on
// DIFFERENT bases: the design ranks by frequency within the rows the fit
// listwise-admitted for that target, the marginal ranks over the whole
// cohort. Both keep --top-k of them and they disagree, so a level the
// design retained can be absent from the marginal that generates it (9
// of brand's 32, 4 of ageExact's, 2 of category's) leaving a column that
// is constant over the synthetic partition.
func resolveRecoveryTerms(mergedSchema *encoding.Schema, d *modelDrawer, std float64) []*recoveryTerm {
	out := make([]*recoveryTerm, 0, len(d.predictors))
	for i := range d.predictors {
		p := &d.predictors[i]
		kind := ModelPredictorCategoricalLevel
		if p.isSet {
			kind = ModelPredictorSetOption
		}
		t := &recoveryTerm{entry: &ModelPredictorFidelity{
			Kind:  kind,
			Field: p.field,
			Level: p.level,
			// The captured coefficient is divided by the target's own
			// marginal std here and nowhere else, because that is exactly
			// what buildModelDrawers does to it on the way into the
			// latent (u = (prediction-mean)/std). Reporting the raw number
			// beside a latent-scale recovery would be the like-for-like
			// failure this section is built to avoid.
			CapturedCoefficient: p.coef / std,
		}}
		out = append(out, t)

		f := mergedSchema.Field(p.field)
		if f == nil {
			t.entry.Error = "predictor field is not present in the output cohort"
			continue
		}
		if f.Dictionary == nil {
			t.entry.Error = "predictor field carries no dictionary in the output cohort"
			continue
		}
		id, found := f.Dictionary.IDFor(p.level)
		if !found {
			t.entry.Error = "predictor level is absent from the output cohort's dictionary, so no row can carry it"
			continue
		}
		switch {
		case p.isSet && f.Type.IsSet():
			if int(id) >= int(f.Type.MaxSetEntries()) {
				t.entry.Error = "predictor option lies beyond the set field's on-wire mask width"
				continue
			}
			t.col = dummyColumn{
				Name: dummySetName(p.field, p.level), Kind: dummySetOption,
				Field: p.field, Level: p.level, Bit: uint(id), dict: f.Dictionary,
			}
		case !p.isSet && f.Type.IsCategorical():
			t.col = dummyColumn{
				Name: dummyCategoricalName(p.field, p.level), Kind: dummyCategoricalLevel,
				Field: p.field, Level: p.level, LevelID: id, dict: f.Dictionary,
			}
		default:
			t.entry.Error = "predictor field's output-cohort type does not match the term's indicator kind"
			continue
		}
		t.live = true
	}
	return out
}

// admitRecoveryRows fixes the refit's row set and tallies each live
// term's support over it, returning ok=false when fewer than two rows
// survive (an OLS fit over one row is not a fit).
//
// Admission is listwise on the target plus every predictor SOURCE field,
// which is the identical rule regression's own UpdateRow applies and the
// identical rule capture's fit applied — so a row this function admits is
// exactly a row the engine will use, and NFired below is measured on the
// same base as the coefficients it explains.
func admitRecoveryRows(rows []syntheticRow, d *modelDrawer, terms []*recoveryTerm, latent latentFunc) (admitted []int, ok bool) {
	for ri := range rows {
		row := &rows[ri]
		if row.nulls[d.field] {
			continue
		}
		if _, invertible := latent(row.values[d.field]); !invertible {
			continue
		}
		usable := true
		for _, t := range terms {
			if !t.live {
				continue
			}
			if row.nulls[t.col.Field] {
				usable = false
				break
			}
		}
		if !usable {
			continue
		}
		admitted = append(admitted, ri)
		for _, t := range terms {
			if !t.live {
				continue
			}
			if recoveryIndicator(row, t.col) == 1 {
				t.entry.NFired++
			}
		}
	}
	if len(admitted) < 2 {
		return admitted, false
	}
	// A column that is constant over the admitted rows carries no
	// information and makes the design rank-deficient — always 0 (the
	// level is never generated) or always 1 (it is the only level
	// generated, which is collinear with the intercept). Capture solves
	// the same problem the same way, in pruneDegenerateColumns, and for
	// the same reason: the solver's refusal is whole-model, so one empty
	// column would cost every OTHER term of that model its estimate.
	for _, t := range terms {
		if !t.live {
			continue
		}
		if t.entry.NFired == 0 || t.entry.NFired == len(admitted) {
			t.live = false
			t.entry.Error = "design column is constant over the admitted synthetic rows, so its coefficient is not identified"
		}
	}
	return admitted, true
}

// recoveryIndicator evaluates one design column against one cached row,
// returning its 0/1 value. Nullity is decided by the caller (admission),
// so this is only ever reached for a row whose source field is present.
func recoveryIndicator(row *syntheticRow, col dummyColumn) float64 {
	switch col.Kind {
	case dummySetOption:
		mask, present := row.wide[col.Field].(uint64)
		if !present {
			return 0
		}
		if mask&(uint64(1)<<col.Bit) != 0 {
			return 1
		}
		return 0
	case dummyCategoricalLevel:
		if uint32(row.values[col.Field]) == col.LevelID {
			return 1
		}
		return 0
	}
	// Every kind resolveRecoveryTerms can construct is named above, and
	// no `default:` arm is offered on purpose — the same rule
	// modelPredictorKind and dummyRecord.NumericValue now follow, for the
	// reason E2-S5 recorded: a default arm over an enum answers
	// confidently for members it was never taught, and here it would
	// silently enter a wrong indicator into the recovery design while the
	// refit succeeded. A new dummyColumnKind reaching this function
	// contributes nothing until it is taught.
	return 0
}

// liveRecoveryColumns collects the surviving design columns in term
// order.
func liveRecoveryColumns(terms []*recoveryTerm) []dummyColumn {
	out := make([]dummyColumn, 0, len(terms))
	for _, t := range terms {
		if t.live {
			out = append(out, t.col)
		}
	}
	return out
}

// fitRecovery drives the surviving design through processing/regression's
// REG_OLS engine — the same engine, through the same dummyRecord adapter,
// that capture used (synth/regression_record.go, synth/profile_models.go's
// buildEngine). Using a second estimator here would make every reported
// gap ambiguous between "generation lost structure" and "two fitters
// disagree", which is the one thing this section must never be.
//
// The fit is deliberately UNPENALIZED even when capture shrank its own
// (FieldModel.ShrinkageAlpha): the captured coefficient being compared
// against is the shrunken one, it is what generation actually applied,
// and re-applying a ridge here would shrink the recovery a second time
// and report a gap that neither side committed.
func fitRecovery(mergedSchema *encoding.Schema, rows []syntheticRow, admitted []int, d *modelDrawer, cols []dummyColumn, latent latentFunc) (*types.RegressionResult, string) {
	plan := &dummyPlan{
		target:  d.field,
		columns: cols,
		byName:  make(map[string]dummyColumn, len(cols)+1),
	}
	plan.byName[d.field] = dummyColumn{Name: d.field, Kind: dummyNumeric, Field: d.field}
	for _, c := range cols {
		plan.byName[c.Name] = c
	}

	names := make([]string, len(cols))
	for i := range cols {
		names[i] = cols[i].Name
	}
	engines, err := regression.BuildStreaming([]*types.RegressionSpec{{
		Type:       types.REG_OLS,
		Name:       d.field,
		Target:     d.field,
		Predictors: names,
	}}, plan.viewSchema())
	if err != nil {
		return nil, err.Error()
	}
	if len(engines) != 1 {
		return nil, "engine construction returned no engine"
	}

	rec := &latentRecord{dummyRecord: plan.newRecord(), target: d.field}
	for _, ri := range admitted {
		row := &rows[ri]
		rec.bind(row.values, row.nulls, row.wide)
		rec.latent, rec.ok = latent(row.values[d.field])
		if err := engines[0].UpdateRow(rec); err != nil {
			return nil, err.Error()
		}
	}
	res, err := engines[0].Finalize()
	if err != nil {
		return nil, err.Error()
	}
	return res, ""
}

// latentRecord is the dummyRecord adapter with its TARGET column
// answered on the latent scale instead of the raw one.
//
// It wraps rather than replaces because every PREDICTOR must keep
// resolving through the identical dummy-coding path capture used —
// including its null-propagation contract, where a null source field
// answers (0, false) so the engine deletes the row rather than reading a
// fabricated zero indicator. Only the one target name is intercepted,
// and it is intercepted at the adapter rather than by pre-transforming
// the cached row values because those maps are shared across every model
// in the report; rewriting one field in place for one model would corrupt
// the next.
type latentRecord struct {
	*dummyRecord
	target string
	latent float64
	ok     bool
}

var _ regression.Record = (*latentRecord)(nil)

// NumericValue answers the target on the latent scale and delegates
// everything else, unchanged, to the capture-side adapter.
func (r *latentRecord) NumericValue(name string) (float64, bool) {
	if name == r.target {
		return r.latent, r.ok
	}
	return r.dummyRecord.NumericValue(name)
}
