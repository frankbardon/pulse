package synth

import (
	"fmt"
	"math"

	mrand "math/rand/v2"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/processing/regression"
	"github.com/frankbardon/pulse/types"
)

// This file is the profile-time model CAPTURE stage: under
// ProfileOptions.FitModels (`profile create --fit-models`) it fits one
// linear model per eligible numeric field — the field regressed on the
// cohort's categorical LEVELS and set OPTIONS — and retains the fitted
// coefficients, the residual scale and a bounded row-aligned residual
// vector in memory.
//
// It exists because the profile's numeric marginals today describe each
// field in isolation, and generation reconstructs a numeric by drawing
// its marginal and then OVERWRITING it once per claimed conditional
// pair. A single additive linear predictor replaces that overwrite
// chain, and this is where its coefficients come from.
//
// # Why it rides the existing scan
//
// The whole fit is driven off the rows profileRecords is ALREADY
// decoding. regression's OLS engine is a streaming Welford accumulator:
// per-field state is the O(p²) centered Gram matrix and is independent
// of row count, so folding a fit into the per-row loop costs one
// UpdateRow per model per row and no second read of the cohort. The
// adapter that makes that possible — dummyRecord, which synthesises a
// 0/1 design column per categorical level and per set option out of the
// same three decoded maps profileRecords already fills — lives in
// regression_record.go; read its header for why processing/regression
// needs no change to accept columns that exist in no cohort schema.
//
// # What is deliberately NOT here
//
//   - Persistence POLICY. The fits reach the document through
//     Profile.Models (the additive `models` section, serialised by the
//     json tags on the shapes below); which of their parts are durable
//     and which stay in-process is decided ON those shapes — see
//     ModelPredictor.Column and FieldModel.Residuals for the two
//     deliberate `json:"-"` slots. Nothing here runs when the flag is
//     off, which is the property the byte-identity test asserts.
//   - Predictor SELECTION by variance explained. Every categorical and
//     every set option in the cohort is a candidate here. modelColumns
//     below is the single seam that narrows them, so a selection rule
//     lands by replacing one function rather than by restructuring this
//     file.
//   - Regularisation. Ridge is already implemented and tested inside the
//     engine (spec.Penalty = "l2" + Alpha); a degenerate design is
//     SKIPPED with a warning here rather than quietly bent into shape by
//     a hand-rolled penalty. Shrinking thin levels is a modelling
//     decision that deserves its own change and its own threshold.

const (
	// modelResidualCap bounds the row-aligned residual reservoir below.
	//
	// The FIT's state is O(p²) and independent of row count — that is
	// the engine's own guarantee and the reason capture is free. The
	// RESIDUALS are a different quantity: they are per-row by
	// construction, cannot be computed until the coefficients exist
	// (i.e. not before Finalize), and are consumed pairwise across
	// fields, so they need a row-ALIGNED sample retained from the scan.
	// This cap is what keeps that sample bounded, and it deliberately
	// matches conditionalJointCap so the two row-aligned reservoirs in
	// this package cost the same order of memory.
	modelResidualCap = conditionalJointCap

	// maxModelColumns caps the design width of a single model.
	//
	// A cohort with a 210-level `dma` beside twenty other categoricals
	// expands to thousands of columns, and the engine's O(p²) Gram
	// matrix is quadratic in exactly that number — 2,000 columns is a
	// 32 MB matrix PER FITTED FIELD. Until predictor selection narrows
	// the candidate set on merit, a model wider than this is skipped
	// with a warning naming the field: refusing loudly beats either
	// allocating gigabytes or silently truncating the candidate list in
	// schema order, which would make the surviving coefficients depend
	// on column ordering rather than on the data.
	maxModelColumns = 256
)

// Model predictor kinds, as they appear on ModelPredictor.Kind. These
// are the string spellings of the internal dummyColumnKind — spelled
// out rather than exposing the internal enum because this shape is what
// the profile document's `models` section serialises.
const (
	ModelPredictorCategoricalLevel = "categorical_level"
	ModelPredictorSetOption        = "set_option"
	ModelPredictorNumeric          = "numeric"
)

// ModelPredictor is one fitted coefficient: which real cohort field and
// level the design column was expanded from, and the coefficient itself.
//
// # Why (Field, Level) is the serialised identity and Column is not
//
// Column is the design-matrix name the solver keyed the coefficient by,
// and it is an internal, length-prefixed encoding (see
// dummyCategoricalName) invented purely so two different fields cannot
// collide on a level spelling. Writing it into the document would
// freeze that encoding into the profile file format: every future
// reader would have to parse it, and changing it — to widen the field
// namespace, say — would silently invalidate every document already on
// disk. (Field, Level) is the durable identity a consumer actually
// needs ("the coefficient for dma=602"), so Column is `json:"-"` and
// survives only in-process, where the residual replay uses it.
type ModelPredictor struct {
	// Column is the design-matrix column name the engine keyed the
	// coefficient by. Internal encoding; diagnostics and the in-process
	// residual replay only, never serialised — see the type comment.
	Column string `json:"-"`
	// Kind is one of the ModelPredictor* constants above. It is carried
	// on the wire because it is not derivable from (Field, Level): a
	// categorical level is one arm of a partition read against a dropped
	// reference (see FieldModel.References), while a set option is an
	// independent indicator with no reference at all, and a consumer
	// reconstructing effects has to know which it is holding.
	Kind string `json:"kind"`
	// Field is the real cohort field this column was expanded from.
	Field string `json:"field"`
	// Level is the categorical level text or set option text this column
	// indicates. Empty when Kind is ModelPredictorNumeric. Deliberately
	// NOT omitempty: it is half of the addressing key, and a categorical
	// dictionary may legitimately carry the empty-string level, so the
	// key is always written rather than left to be inferred from its
	// absence.
	Level string `json:"level"`
	// Coefficient is the fitted slope for this column.
	Coefficient float64 `json:"coefficient"`
}

// ModelReference names one categorical predictor field's dropped
// REFERENCE level — the level whose column modelColumns removed to keep
// the design full-rank alongside an intercept, and whose effect is
// therefore folded into FieldModel.Intercept.
//
// It exists so the document is self-describing about the baseline. A
// consumer reading `predictors` alone sees k−1 levels of a k-level
// field and has to GUESS that the missing one is the zero baseline —
// and, worse, guess WHICH one it was, since the drop rule (dictionary
// ID 0) is an implementation choice this section must not require a
// reader to know. Writing the dropped level down turns both guesses
// into a lookup, and leaves the drop rule free to change later without
// invalidating a single document already written.
type ModelReference struct {
	// Field is the categorical predictor source field.
	Field string `json:"field"`
	// Level is the level whose effect is folded into the intercept. A
	// row carrying it contributes nothing beyond the intercept for this
	// field; every other level's coefficient is read relative to it.
	Level string `json:"level"`
}

// FieldModel is one numeric field's fitted linear predictor.
//
// The additive form is Intercept + Σ Coefficient·column, where each
// column is 1 when the row carries that categorical level or has that
// set option selected and 0 otherwise. A categorical contributes one
// column per level EXCEPT its reference level (see modelColumns), whose
// effect is folded into Intercept — that is what keeps the design
// full-rank alongside an intercept.
type FieldModel struct {
	// Field is the numeric target this model predicts.
	Field string `json:"field"`
	// Intercept is the fitted constant term: the predicted value for a
	// row sitting at every categorical's reference level (see
	// References) with no set option selected.
	Intercept float64 `json:"intercept"`
	// Predictors lists the fitted coefficients in design-column order.
	// Empty is legal and meaningful only in the sense that such a model
	// is never retained — a target with no usable candidate columns is
	// skipped, not stored with an empty predictor list.
	Predictors []ModelPredictor `json:"predictors"`
	// References names, per categorical predictor field, the level
	// dropped from Predictors and folded into Intercept. Absent
	// (omitempty) exactly when the model carries no categorical
	// predictor at all — a set-only model has nothing to drop, since a
	// multi-select is not a partition — which a reader can confirm from
	// predictors[].kind rather than having to assume. See ModelReference
	// for why the baseline is written down instead of inferred.
	References []ModelReference `json:"references,omitempty"`
	// NObs is the number of rows that actually contributed to the fit.
	// The engine applies listwise deletion, so a row with a null target
	// or a null predictor SOURCE field contributes nothing and this is
	// below the profile's RowCount whenever the cohort has nulls.
	NObs int `json:"n_obs"`
	// R2 and ResidualStd are the fit's explanatory power and residual
	// scale. ResidualStd is the standard deviation of the residual that
	// generation adds back on top of the linear predictor.
	R2          float64 `json:"r2"`
	ResidualStd float64 `json:"residual_std"`
	// Residuals is the row-aligned fitted residual (observed − predicted)
	// for each row retained in the capture's residual reservoir, and
	// ResidualPresent[i] reports whether row i contributed one at all —
	// a row listwise-deleted for THIS field has no residual even though
	// another field's model may have one for the same row.
	//
	// Every FieldModel from one capture indexes the SAME retained rows,
	// which is the whole point: residual correlation between two fields
	// is only meaningful on rows where both residuals exist.
	//
	// Both are `json:"-"` — DELIBERATELY not part of the document. They
	// are a bounded row-aligned SAMPLE (modelResidualCap entries per
	// model, per field), so serialising them would add tens of thousands
	// of numbers per numeric column to a document whose every other
	// section is a handful of summary statistics, and would do it to
	// carry rows a profile exists precisely to avoid retaining. Their
	// consumer is the residual-correlation measurement, which runs in
	// the SAME process as the capture and reads them straight off
	// FittedModels(); what belongs in the document is the small derived
	// quantity that measurement produces, not its input. A Profile read
	// back from JSON therefore has coefficients and ResidualStd but nil
	// Residuals, and any consumer that needs per-row residuals must
	// capture rather than re-read.
	Residuals       []float64 `json:"-"`
	ResidualPresent []bool    `json:"-"`
}

// FittedModels returns the per-numeric linear models captured under
// ProfileOptions.FitModels, or nil when the flag was off.
//
// It is a thin accessor over Profile.Models, retained because it is the
// name the capture stage published and because it reads correctly at
// both ends of a round trip: on a freshly captured Profile it hands
// back models complete with their residual reservoir, and on one
// decoded from a document it hands back the same models minus the
// `json:"-"` parts. Callers that need per-row residuals must therefore
// hold the capture, not re-read the file — see FieldModel.Residuals.
func (p *Profile) FittedModels() []FieldModel { return p.Models }

// modelFitter drives one streaming OLS engine per eligible numeric
// field off the profiler's single scan, and afterwards computes each
// field's residuals over a bounded row-aligned sample of that scan.
type modelFitter struct {
	fits []*fieldFit

	// snapFields is the union of every fitted target and every
	// predictor SOURCE field, in schema order — the columns the
	// residual pass has to be able to reconstitute. Held as parallel
	// slices rather than maps so a retained row is two small
	// allocations, matching jointRows/jointNulls.
	snapFields []string
	snapIsSet  []bool

	rows  []modelRow
	rng   *mrand.Rand
	seen  int
	bound bool
}

// modelRow is one retained row of the residual reservoir: the decoded
// value, null flag and (for set fields) exact mask of every snapshot
// field. The mask is kept separately from the float64 echo because a
// set_u64's bits exceed float64's 2^53 exact-integer range — the same
// reason profileRecords reads set fields out of `wide` rather than
// `values`.
type modelRow struct {
	values []float64
	nulls  []bool
	masks  []uint64
}

// fieldFit is one target's in-flight fit: its dummy expansion, the
// engine consuming it, and the reusable Record adapter bound to the
// scan's decode maps.
type fieldFit struct {
	target  string
	plan    *dummyPlan
	columns []dummyColumn
	// references are the categorical levels modelColumns dropped to keep
	// the design full-rank; carried through to FieldModel.References so
	// the emitted document names its own baseline.
	references []ModelReference
	engine     regression.StreamingEngine
	rec        *dummyRecord

	// model is populated at finish() when the fit succeeds; a nil model
	// means the field was skipped and has already been warned about.
	model *FieldModel
}

// newModelFitter builds one fit per eligible numeric target against
// schema. A target whose model cannot even be CONSTRUCTED (no usable
// candidate columns, a design wider than maxModelColumns, a spec the
// engine rejects) is dropped here with a warning rather than at
// finalize, so the scan never carries an engine that cannot produce a
// result. Returns nil when nothing is fittable, which lets the caller
// skip the per-row work entirely.
func newModelFitter(schema *encoding.Schema, seed int64, warnings *[]string) *modelFitter {
	candidates := modelCandidateFields(schema)
	targets := modelTargetFields(schema)
	if len(targets) == 0 {
		return nil
	}

	f := &modelFitter{rng: newRng(seed)}
	used := make(map[string]bool, len(candidates)+len(targets))

	for _, target := range targets {
		if len(candidates) == 0 {
			*warnings = append(*warnings, modelSkipWarning(target,
				"no categorical or set_* candidate predictors in the cohort"))
			continue
		}
		plan, err := newDummyPlan(schema, target, candidates)
		if err != nil {
			*warnings = append(*warnings, modelSkipWarning(target, err.Error()))
			continue
		}
		columns, references := modelColumns(plan)
		if len(columns) == 0 {
			*warnings = append(*warnings, modelSkipWarning(target,
				"no usable predictor columns survived reference-level dropping"))
			continue
		}
		if len(columns) > maxModelColumns {
			*warnings = append(*warnings, modelSkipWarning(target, fmt.Sprintf(
				"design matrix would be %d columns wide (cap %d)", len(columns), maxModelColumns)))
			continue
		}
		names := make([]string, len(columns))
		for i := range columns {
			names[i] = columns[i].Name
		}
		spec := &types.RegressionSpec{
			Type:       types.REG_OLS,
			Name:       target,
			Target:     plan.Target(),
			Predictors: names,
		}
		engines, err := regression.BuildStreaming([]*types.RegressionSpec{spec}, plan.viewSchema())
		if err != nil || len(engines) != 1 {
			reason := "engine construction returned no engine"
			if err != nil {
				reason = err.Error()
			}
			*warnings = append(*warnings, modelSkipWarning(target, reason))
			continue
		}
		f.fits = append(f.fits, &fieldFit{
			target:     target,
			plan:       plan,
			columns:    columns,
			references: references,
			engine:     engines[0],
			rec:        plan.newRecord(),
		})
		used[target] = true
		for _, c := range columns {
			used[c.Field] = true
		}
	}
	if len(f.fits) == 0 {
		return nil
	}

	// Snapshot fields are taken in SCHEMA order, not in the order the
	// fits happened to name them, so the reservoir's column layout is a
	// property of the cohort rather than of the loop above.
	for i := range schema.Fields {
		fld := &schema.Fields[i]
		if !used[fld.Name] {
			continue
		}
		f.snapFields = append(f.snapFields, fld.Name)
		f.snapIsSet = append(f.snapIsSet, fld.Type.IsSet())
	}
	return f
}

// modelTargetFields lists the fields eligible to be a model TARGET, in
// schema order: exactly the fields profileRecords summarises as
// NumericProfile (its `default` accumulator branch), narrowed to those
// carrying an analytics-numeric value. Keeping the two sets aligned is
// what lets a later stage map a FieldModel back onto the numeric field
// profile it belongs to by name alone.
func modelTargetFields(schema *encoding.Schema) []string {
	var out []string
	for i := range schema.Fields {
		f := &schema.Fields[i]
		switch {
		case f.Type == encoding.FieldTypeDate, f.Type.IsCategorical(), f.Type.IsSet():
			continue
		case !f.Type.IsNumericForAnalytics():
			continue
		}
		out = append(out, f.Name)
	}
	return out
}

// modelCandidateFields lists the fields eligible to CONTRIBUTE design
// columns, in schema order: every categorical and every set_* field
// carrying a dictionary. A dictionary-less categorical is skipped here
// rather than allowed to fail newDummyPlan, because one such field
// would otherwise take down every target's model.
//
// This is the widest possible candidate set on purpose — narrowing it
// on merit is a selection rule, and modelColumns below is where it
// belongs.
func modelCandidateFields(schema *encoding.Schema) []string {
	var out []string
	for i := range schema.Fields {
		f := &schema.Fields[i]
		if !f.Type.IsCategorical() && !f.Type.IsSet() {
			continue
		}
		if f.Dictionary == nil || f.Dictionary.Count() == 0 {
			continue
		}
		out = append(out, f.Name)
	}
	return out
}

// modelColumns chooses which of a plan's fully expanded columns enter
// the design matrix. This is the seam a predictor-selection rule
// replaces; today it does exactly one thing, and that one thing is
// mandatory rather than a policy choice.
//
// Every categorical drops one REFERENCE level. newDummyPlan expands the
// full level set, and a full level set sums to 1 on every row — which
// is the intercept column — so the design is rank-deficient and the
// solver refuses it outright (the dummy trap). The dropped level's
// effect is absorbed into the intercept, and every surviving
// coefficient is read relative to it. The reference is the level at
// dictionary ID 0, chosen because plan.columns is already in dictionary
// order so "first seen for this field" is deterministic and needs no
// second pass; WHICH level is dropped changes no fitted value and no
// residual, only the reading of the coefficients.
//
// Set options are deliberately NOT subject to this. A set field is not
// a partition — a row may select all of its options, or none — so its
// option columns do not sum to 1 and dropping one would discard a real
// effect rather than remove a redundancy.
//
// The dropped levels come back as the second return value rather than
// being discarded, because the profile document has to be able to NAME
// its own baseline: a reader handed k−1 of k levels would otherwise
// have to infer both that a level is missing and which one it was, and
// the second inference would hard-code this function's drop rule into
// every consumer. Returning them from the one function that makes the
// choice is what keeps the rule replaceable.
func modelColumns(plan *dummyPlan) ([]dummyColumn, []ModelReference) {
	out := make([]dummyColumn, 0, len(plan.columns))
	var refs []ModelReference
	reference := make(map[string]bool, len(plan.columns))
	for _, c := range plan.columns {
		if c.Kind == dummyCategoricalLevel && !reference[c.Field] {
			reference[c.Field] = true
			refs = append(refs, ModelReference{Field: c.Field, Level: c.Level})
			continue
		}
		out = append(out, c)
	}
	return out, refs
}

// finiteModel reports whether every float a FieldModel would serialise
// is finite, naming the first offender.
//
// This is a document-integrity guard, not a statistical one. A
// degenerate design can leave the solver returning a NaN or ±Inf
// coefficient, and encoding/json refuses those outright — so a single
// such model would fail the marshal of the ENTIRE profile document,
// turning one unfittable field into a failed `profile create`. The
// models are an addition to a document that is complete without them,
// so an unrepresentable model is dropped with a warning on exactly the
// same footing as a model that failed to fit at all.
func finiteModel(m *FieldModel) (string, bool) {
	if !isFinite(m.Intercept) {
		return "intercept", false
	}
	if !isFinite(m.R2) {
		return "r2", false
	}
	if !isFinite(m.ResidualStd) {
		return "residual_std", false
	}
	for i := range m.Predictors {
		if !isFinite(m.Predictors[i].Coefficient) {
			return "coefficient for " + m.Predictors[i].Field + "=" + m.Predictors[i].Level, false
		}
	}
	return "", true
}

func isFinite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// modelSkipWarning is the single spelling of "this field has no model".
// Skipping is never a refusal — a cohort where every numeric field is
// unfittable still produces a complete profile — so the warning has to
// carry enough to act on: which field, and why.
func modelSkipWarning(field, reason string) string {
	return fmt.Sprintf("model for numeric field %q skipped: %s", field, reason)
}

// observe folds the current row into every fit, and offers it to the
// residual reservoir.
//
// values/nulls/wide are profileRecords' own decode maps, reused in
// place by each ReadRecordWithWide call; the Record adapters bind to
// them ONCE (they hold the map headers, not copies) so the per-row cost
// here is the engine's own UpdateRow and nothing else.
func (f *modelFitter) observe(values map[string]float64, nulls map[string]bool, wide map[string]any) error {
	if !f.bound {
		for _, fit := range f.fits {
			fit.rec.bind(values, nulls, wide)
		}
		f.bound = true
	}
	for _, fit := range f.fits {
		if err := fit.engine.UpdateRow(fit.rec); err != nil {
			return err
		}
	}
	f.retain(values, nulls, wide)
	return nil
}

// retain offers the current row to the residual reservoir using
// Algorithm R — the same discipline the categorical joint capture uses,
// and for the same reason: first-N truncation silently profiles
// whichever block of a block-ordered cohort (one sorted by region, say)
// happens to be read first, and residual correlation computed off that
// block is a measurement of the block rather than of the cohort.
//
// The RNG is this fitter's OWN, seeded from ProfileOptions.Seed. It is
// deliberately not the reservoir RNG the categorical capture draws
// from: sharing one stream would make --conditional's captured output
// depend on whether --fit-models was also passed, and each flag has to
// keep its exact behaviour independent of the other.
func (f *modelFitter) retain(values map[string]float64, nulls map[string]bool, wide map[string]any) {
	slot := len(f.rows)
	if slot >= modelResidualCap {
		j := f.rng.IntN(f.seen + 1)
		if j >= modelResidualCap {
			f.seen++
			return
		}
		slot = j
	}

	row := modelRow{
		values: make([]float64, len(f.snapFields)),
		nulls:  make([]bool, len(f.snapFields)),
	}
	for i, name := range f.snapFields {
		row.nulls[i] = nulls[name]
		if row.nulls[i] {
			continue
		}
		row.values[i] = values[name]
		if f.snapIsSet[i] {
			// A set column's design bits come from the exact mask, so a
			// row whose mask did not decode is retained as MISSING
			// rather than as an empty selection. Recording it as empty
			// would make the replayed residual disagree with the fit,
			// which read the same absent mask as missing and dropped
			// the row.
			mask, present := wide[name].(uint64)
			if !present {
				row.nulls[i] = true
				continue
			}
			if row.masks == nil {
				row.masks = make([]uint64, len(f.snapFields))
			}
			row.masks[i] = mask
		}
	}
	if slot == len(f.rows) {
		f.rows = append(f.rows, row)
	} else {
		f.rows[slot] = row
	}
	f.seen++
}

// finish closes every engine and returns the surviving models, in
// target schema order.
//
// A fit that fails at finalize — rank-deficient because two candidate
// categoricals are nested (region inside dma), or short of the n ≥ p+1
// the closed form needs — is dropped with a warning naming the field.
// It must never fail the profile run: the models are an ADDITION to a
// document that is complete without them, and a cohort where one field
// happens to be unfittable is entirely ordinary.
func (f *modelFitter) finish(warnings *[]string) []FieldModel {
	kept := make([]*fieldFit, 0, len(f.fits))
	for _, fit := range f.fits {
		res, err := fit.engine.Finalize()
		if err != nil {
			*warnings = append(*warnings, modelSkipWarning(fit.target, err.Error()))
			continue
		}
		model := &FieldModel{
			Field:       fit.target,
			Intercept:   res.Coefficients[regression.InterceptKey],
			Predictors:  make([]ModelPredictor, 0, len(fit.columns)),
			References:  fit.references,
			NObs:        res.NObs,
			R2:          res.R2,
			ResidualStd: res.ResidualStdErr,
		}
		for _, c := range fit.columns {
			model.Predictors = append(model.Predictors, ModelPredictor{
				Column:      c.Name,
				Kind:        modelPredictorKind(c.Kind),
				Field:       c.Field,
				Level:       c.Level,
				Coefficient: res.Coefficients[c.Name],
			})
		}
		if what, ok := finiteModel(model); !ok {
			*warnings = append(*warnings, modelSkipWarning(fit.target,
				"fit produced a non-finite "+what))
			continue
		}
		fit.model = model
		kept = append(kept, fit)
	}
	if len(kept) == 0 {
		return nil
	}
	f.computeResiduals(kept)

	out := make([]FieldModel, 0, len(kept))
	for _, fit := range kept {
		out = append(out, *fit.model)
	}
	return out
}

// computeResiduals replays the retained reservoir rows through each
// surviving model.
//
// A residual cannot be produced during the scan — it needs the
// coefficients, which do not exist until Finalize — so this is the one
// place the capture touches a row twice, and it touches only the
// bounded reservoir, never the cohort. The row loop is OUTER and the
// model loop INNER so each retained row is reconstituted into the
// decode maps exactly once regardless of how many fields were fitted.
func (f *modelFitter) computeResiduals(fits []*fieldFit) {
	n := len(f.rows)
	for _, fit := range fits {
		fit.model.Residuals = make([]float64, n)
		fit.model.ResidualPresent = make([]bool, n)
	}

	values := make(map[string]float64, len(f.snapFields))
	nulls := make(map[string]bool, len(f.snapFields))
	wide := make(map[string]any, len(f.snapFields))
	for _, fit := range fits {
		fit.rec.bind(values, nulls, wide)
	}

	for ri := range f.rows {
		row := &f.rows[ri]
		clear(values)
		clear(nulls)
		clear(wide)
		for i, name := range f.snapFields {
			if row.nulls[i] {
				nulls[name] = true
				continue
			}
			values[name] = row.values[i]
			if f.snapIsSet[i] && row.masks != nil {
				wide[name] = row.masks[i]
			}
		}
		for _, fit := range fits {
			if r, ok := fit.residual(); ok {
				fit.model.Residuals[ri] = r
				fit.model.ResidualPresent[ri] = true
			}
		}
	}
}

// residual returns observed − predicted for the row currently bound to
// this fit's Record adapter.
//
// It reads every input through the SAME dummyRecord the engine was
// driven with, so a row the fit listwise-deleted is exactly a row that
// answers not-ok here — the residual vector's presence flags and the
// fit's NObs are two views of one admission rule rather than two
// independently derived ones.
func (f *fieldFit) residual() (float64, bool) {
	y, ok := f.rec.NumericValue(f.target)
	if !ok {
		return 0, false
	}
	pred := f.model.Intercept
	for i := range f.columns {
		x, ok := f.rec.NumericValue(f.columns[i].Name)
		if !ok {
			return 0, false
		}
		pred += f.model.Predictors[i].Coefficient * x
	}
	if math.IsNaN(pred) || math.IsInf(pred, 0) {
		return 0, false
	}
	return y - pred, true
}

// modelPredictorKind maps the internal dummy column kind onto the
// stable string spelling carried on ModelPredictor.
func modelPredictorKind(k dummyColumnKind) string {
	switch k {
	case dummyCategoricalLevel:
		return ModelPredictorCategoricalLevel
	case dummySetOption:
		return ModelPredictorSetOption
	default:
		return ModelPredictorNumeric
	}
}
