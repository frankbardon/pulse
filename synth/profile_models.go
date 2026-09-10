package synth

import (
	"fmt"
	"math"
	"sort"
	"strings"

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
// # Why it rides the existing scan, and why the fit runs at the END of it
//
// The capture reads the cohort EXACTLY ONCE — the rows profileRecords is
// already decoding, no second pass, which
// TestProfileModels_RidesTheExistingScan measures directly by counting
// bytes pulled. What the per-row hook does with them is retain a
// bounded, reservoir-sampled row snapshot; the fits themselves run over
// that snapshot after the scan, immediately before the residual replay
// that already walked the same rows.
//
// That split is forced by predictor selection and is not an
// optimisation. The top-K collapse ranks a categorical's levels by
// FREQUENCY and the variance-explained floor scores each candidate
// against each target — neither quantity exists until rows have been
// seen, so a design frozen before the first read can only be the FULL
// expansion of every dictionary. That is what E1 did, and on the
// motivating cohort it produced a 2,420-column design for all 105
// targets and skipped every one of them.
//
// It also removes a cost that would have made the feature unusable even
// with the cap lifted. The engine's accumulator is O(p²) PER ROW: at 194
// columns and 105 targets, streaming 381k rows is ~7.5e11 flops of
// centered cross-products. Over the bounded snapshot it is three orders
// of magnitude less, and the coefficients are estimated from a uniform
// sample of the cohort rather than from whichever rows a prefix happened
// to contain. NObs reports the contributing SNAPSHOT rows, so it is
// bounded by modelResidualCap on a cohort larger than the reservoir —
// read it as the fit's own support, not as the cohort's row count
// (Profile.RowCount is that).
//
// The adapter underneath — dummyRecord, which synthesises a 0/1 design
// column per categorical level and per set option out of the same three
// decoded maps profileRecords fills — lives in regression_record.go;
// read its header for why processing/regression needs no change to
// accept columns that exist in no cohort schema.
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
//   - Predictor SELECTION itself. Which categorical LEVELS get a column
//     (the top-K collapse) and which candidate FIELDS enter a given
//     target's model (variance explained against an absolute floor) are
//     both decided in profile_models_select.go; this file consumes the
//     answer. Read that file's header before changing anything about
//     which columns exist.
//   - Regularisation. Ridge is already implemented and tested inside the
//     engine (spec.Penalty = "l2" + Alpha); a degenerate design is
//     SKIPPED with a warning here rather than quietly bent into shape by
//     a hand-rolled penalty. Shrinking thin levels is a modelling
//     decision that deserves its own change and its own threshold.

const (
	// modelResidualCap bounds the row-aligned snapshot this capture
	// retains from the scan.
	//
	// The snapshot serves three consumers and the cap is sized for all
	// of them at once: predictor selection reads it to rank levels and
	// score candidates, the fits are driven over it, and the residuals
	// are replayed from it. Residuals are the reason it has to be row
	// ALIGNED — they are consumed pairwise across fields, and a
	// correlation between two fields' residuals is only meaningful on
	// rows where both exist. The cap deliberately matches
	// conditionalJointCap so the two row-aligned reservoirs in this
	// package cost the same order of memory.
	//
	// Ten thousand rows is ample for the coefficients: a top-K-collapsed
	// design is at most a couple of hundred columns and the thinnest
	// retained level still lands hundreds of rows. It is NOT ample for
	// arbitrarily thin levels, which is exactly why level thinness is
	// its own follow-on concern rather than something to solve by
	// raising this number — the accumulator is O(p²) per row, so the
	// number is not free to raise.
	modelResidualCap = conditionalJointCap

	// maxModelColumns caps the design width of a single model.
	//
	// It is deliberately RETAINED and deliberately NOT raised, but its
	// job changed. E1 set it as a working limit against unbounded dummy
	// expansion, and on the motivating cohort it was the whole story:
	// sixteen categoricals expanded to 2,420 columns and every one of
	// 105 numeric targets was skipped, leaving `--fit-models` inert on
	// real data. The top-K collapse removed that cause — the same cohort
	// expands to under 200 columns before any candidate is scored — so
	// this no longer refuses anything a plausible schema produces.
	//
	// What keeps it here is that the accumulator is O(p²) per row in
	// BOTH memory and arithmetic. 256 columns is a 512 KB Gram matrix and
	// ~33k flops per retained row per fitted field, which is the ceiling
	// this capture is willing to pay when a pathological schema (hundreds
	// of candidate categoricals, none of them collapsible below the
	// per-field top-K) slips past selection. Raising it would license a
	// profile run that takes minutes without ever saying why; the honest
	// answer at that width is to refuse the field by name.
	maxModelColumns = 256

	// maxModelRefitRounds bounds how many times a rank-deficient design
	// is narrowed and retried before the field is given up.
	//
	// The retry exists because selection scores candidates marginally
	// and therefore admits collinear ones on purpose (see
	// profile_models_select.go). One round is enough for the ordinary
	// case — a single nested pair, `age` inside `ageExact` — and each
	// further round gives up the next weakest predictor, so a design
	// still refused after four is not carrying one redundancy, it is
	// carrying a candidate set whose structure this rule cannot untangle
	// and the honest outcome is a warning naming the field. The bound is
	// also the cost bound: a round is one more walk of the snapshot for
	// the fits still in flight.
	maxModelRefitRounds = 4
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

// modelFitter retains a bounded, row-aligned snapshot of the profiler's
// single scan, then — after the scan — selects each numeric target's
// predictors, fits one OLS model per target over that snapshot, and
// replays it once more for the residuals.
//
// The ordering is the whole design: see this file's header for why the
// fits cannot be frozen before the first row is read, and
// profile_models_select.go for what the selection actually decides.
type modelFitter struct {
	schema *encoding.Schema
	// topK is ProfileOptions.TopK, already defaulted by the caller. It is
	// the SAME per-field cap FieldProfile.Categorical.Top and the
	// conditional contingency tables use — reused rather than given its
	// own knob, so a document's collapsed buckets mean one thing
	// everywhere in it.
	topK int

	// targets and candidates are the model targets and the candidate
	// predictor SOURCE fields, both in schema order. candidates is the
	// widest possible set; narrowing is selection's job.
	targets    []string
	candidates []string

	fits []*fieldFit

	// snapFields is the union of every target and every candidate
	// predictor source field, in schema order — the columns the
	// selection, the fit and the residual replay all have to be able to
	// reconstitute. Held as parallel slices rather than maps so a
	// retained row is two small allocations, matching
	// jointRows/jointNulls.
	//
	// It covers every CANDIDATE rather than every admitted predictor
	// because admission is not known until the scan is over, which is
	// the one price the deferred design pays in memory.
	snapFields []string
	snapIsSet  []bool
	// candSnap and targetSnap index snapFields for the candidates and
	// targets respectively, so the selection walk addresses a retained
	// row by position instead of by name.
	candSnap   []int
	targetSnap []int

	rows []modelRow
	rng  *mrand.Rand
	seen int
}

// modelRow is one retained row of the snapshot: the decoded value, null
// flag and (for set fields) exact mask of every snapshot field. The mask
// is kept separately from the float64 echo because a set_u64's bits
// exceed float64's 2^53 exact-integer range — the same reason
// profileRecords reads set fields out of `wide` rather than `values`.
type modelRow struct {
	values []float64
	nulls  []bool
	masks  []uint64
}

// fieldFit is one target's fit: its dummy expansion, the engine
// consuming it, and the reusable Record adapter bound to the replay's
// decode maps.
type fieldFit struct {
	target  string
	plan    *dummyPlan
	columns []dummyColumn
	// references are the categorical levels modelColumns dropped to keep
	// the design full-rank; carried through to FieldModel.References so
	// the emitted document names its own baseline.
	references []ModelReference
	// engine is nil for an INTERCEPT-ONLY fit — a target for which no
	// candidate cleared the variance floor. That is a complete model
	// (the field's own mean plus its own spread) and not a failure, but
	// the OLS engine refuses a zero-predictor spec outright, so its
	// sufficient statistics are accumulated in `only` instead. See
	// newFieldFit.
	engine regression.StreamingEngine
	only   interceptAcc
	rec    *dummyRecord

	// choices are the predictors selection admitted, retained past
	// construction so a refit can give up the weakest of them. failure
	// is the reason the previous attempt was refused, kept so a fit that
	// exhausts its refits reports the ORIGINAL diagnosis rather than
	// whatever the last narrowed attempt happened to say.
	choices []predictorChoice
	failure string

	// model is populated at finish() when the fit succeeds; a nil model
	// means the field was skipped and has already been warned about.
	model *FieldModel
}

// interceptAcc is the sufficient statistic of an intercept-only fit:
// Welford count/mean/M2 over the target's own values.
//
// It exists because "no predictor cleared the floor" has to produce a
// MODEL rather than a skip. A skip is indistinguishable on the wire from
// a failure to fit, and the two mean opposite things — one says the
// field is unrelated to everything in the cohort, which is a finding,
// and the other says the capture could not answer, which is a defect.
type interceptAcc struct {
	n    int
	mean float64
	m2   float64
}

func (a *interceptAcc) add(y float64) {
	a.n++
	d := y - a.mean
	a.mean += d / float64(a.n)
	a.m2 += d * (y - a.mean)
}

// std returns the sample standard deviation, or 0 below two
// observations.
func (a *interceptAcc) std() float64 {
	if a.n < 2 {
		return 0
	}
	v := a.m2 / float64(a.n-1)
	if v < 0 {
		return 0
	}
	return math.Sqrt(v)
}

// newModelFitter prepares the capture for schema. It builds no fit and
// constructs no engine: every design decision below depends on
// statistics that do not exist yet, so all this establishes is WHICH
// columns the snapshot has to carry.
//
// Returns nil when nothing is fittable — no numeric target at all, or no
// candidate predictor in the whole cohort — which lets the caller skip
// the per-row work entirely. The second case is warned per target,
// because "this cohort has nothing to regress on" is a fact about the
// cohort a caller should see once per field it affects.
func newModelFitter(schema *encoding.Schema, topK int, seed int64, warnings *[]string) *modelFitter {
	targets := modelTargetFields(schema)
	if len(targets) == 0 {
		return nil
	}
	candidates := modelCandidateFields(schema)
	if len(candidates) == 0 {
		for _, target := range targets {
			*warnings = append(*warnings, modelSkipWarning(target,
				"no categorical or set_* candidate predictors in the cohort"))
		}
		return nil
	}

	f := &modelFitter{
		schema:     schema,
		topK:       topK,
		targets:    targets,
		candidates: candidates,
		rng:        newRng(seed),
	}

	// Snapshot fields are taken in SCHEMA order, not in the order the
	// target and candidate scans happened to name them, so the
	// snapshot's column layout is a property of the cohort.
	want := make(map[string]bool, len(candidates)+len(targets))
	for _, n := range targets {
		want[n] = true
	}
	for _, n := range candidates {
		want[n] = true
	}
	at := make(map[string]int, len(want))
	for i := range schema.Fields {
		fld := &schema.Fields[i]
		if !want[fld.Name] {
			continue
		}
		at[fld.Name] = len(f.snapFields)
		f.snapFields = append(f.snapFields, fld.Name)
		f.snapIsSet = append(f.snapIsSet, fld.Type.IsSet())
	}
	f.candSnap = make([]int, len(candidates))
	for i, n := range candidates {
		f.candSnap[i] = at[n]
	}
	f.targetSnap = make([]int, len(targets))
	for i, n := range targets {
		f.targetSnap[i] = at[n]
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
// on merit is a selection rule, and it happens after the scan, in
// profile_models_select.go, because both halves of the narrowing (which
// levels, then which fields) are measured rather than declared.
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

// modelColumns chooses which of a plan's expanded columns enter the
// design matrix. It does exactly one thing, and that one thing is
// mandatory rather than a policy choice — WHICH candidates and which of
// their levels reached the plan at all was already decided by
// profile_models_select.go.
//
// Every categorical drops one REFERENCE level. The plan expands the
// retained level set plus, when the top-K collapse cut anything, a
// catch-all standing for the rest — and that set sums to 1 on every row,
// which is the intercept column, so the design is rank-deficient and the
// solver refuses it outright (the dummy trap). The dropped level's
// effect is absorbed into the intercept, and every surviving
// coefficient is read relative to it. The reference is the FIRST
// retained level in plan order (dictionary order), chosen because it
// needs no second pass and is stable across runs; WHICH level is dropped
// changes no fitted value and no residual, only the reading of the
// coefficients. The catch-all is never the reference, because it is
// appended after the retained levels — deliberately, since a baseline
// meaning "one of these 1,874 brands" is not a baseline a reader can
// interpret.
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

// modelNoPredictorWarning is deliberately NOT a skip.
//
// A target no candidate explains still gets a model — its own mean plus
// its own spread — so the wording has to say "carries no predictors",
// not "skipped": the two outcomes have opposite meanings, one a finding
// about the cohort and the other a defect in the capture, and a reader
// scanning Warnings for failures must be able to tell them apart. The
// floor is named in the text because it is the only number that
// explains the outcome, and it is not a knob a caller can reach.
func modelNoPredictorWarning(field string) string {
	return fmt.Sprintf(
		"model for numeric field %q carries no predictors: no candidate explained at least %.1f%% of its variance",
		field, minVarianceExplained*100)
}

// observe offers the current row to the snapshot. It is the whole of
// the capture's per-row cost — no engine exists yet, because no design
// does (see the file header) — which is why enabling --fit-models moves
// profile runtime by single-digit percent.
//
// values/nulls/wide are profileRecords' own decode maps, reused in place
// by each ReadRecordWithWide call, so nothing here may retain them.
func (f *modelFitter) observe(values map[string]float64, nulls map[string]bool, wide map[string]any) error {
	f.retain(values, nulls, wide)
	return nil
}

// retain offers the current row to the snapshot reservoir using
// Algorithm R — the same discipline the categorical joint capture uses,
// and for the same reason: first-N truncation silently profiles
// whichever block of a block-ordered cohort (one sorted by region, say)
// happens to be read first, and a measurement computed off that block is
// a measurement of the block rather than of the cohort. Under the
// deferred design that reason now covers the COEFFICIENTS and the
// predictor SELECTION too, not just the residuals — a prefix-biased
// snapshot would rank a categorical's levels by whatever the first block
// happened to contain.
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

// buildSurvey measures the retained snapshot into the per-candidate,
// per-target sufficient statistics predictor selection is computed from.
//
// One walk, candidates outer per row, targets inner — the same shape the
// residual replay uses, so a retained row is reconstituted from its
// parallel slices exactly once here.
func (f *modelFitter) buildSurvey() *candidateSurvey {
	survey := newCandidateSurvey(f.targets, buildSurveyFields(f.schema, f.candidates))
	targetVals := make([]float64, len(f.targets))
	targetOK := make([]bool, len(f.targets))

	for ri := range f.rows {
		row := &f.rows[ri]
		for ti, si := range f.targetSnap {
			targetOK[ti] = !row.nulls[si]
			if targetOK[ti] {
				targetVals[ti] = row.values[si]
			}
		}
		for ci, si := range f.candSnap {
			if row.nulls[si] {
				continue
			}
			if f.snapIsSet[si] {
				if row.masks == nil {
					continue
				}
				survey.observeSet(ci, row.masks[si], targetVals, targetOK)
				continue
			}
			survey.observeCategorical(ci, uint32(row.values[si]), targetVals, targetOK)
		}
	}
	return survey
}

// newFieldFit builds one target's fit from its admitted predictors, or
// returns the reason the target has no fit at all.
//
// The zero-predictor case is a MODEL, not a reason: a target no
// candidate explains is a finding about the cohort, and a skip there
// would be indistinguishable on the wire from a capture that failed.
func (f *modelFitter) newFieldFit(target string, choices []predictorChoice) (*fieldFit, string) {
	fields := make([]string, 0, len(choices))
	filters := make(map[string]dummyLevelFilter, len(choices))
	for _, c := range choices {
		if c.field == target {
			// A field cannot regress on itself. Unreachable while
			// candidates are categorical/set and targets are scalar, but
			// the plan would refuse it and take the whole model down.
			continue
		}
		fields = append(fields, c.field)
		if !c.isSet {
			filters[c.field] = dummyLevelFilter{keep: c.levels, other: c.other}
		}
	}
	plan, err := newDummyPlanFiltered(f.schema, target, fields, filters)
	if err != nil {
		return nil, err.Error()
	}
	columns, references := modelColumns(plan)
	if len(columns) > maxModelColumns {
		return nil, fmt.Sprintf("design matrix would be %d columns wide (cap %d)",
			len(columns), maxModelColumns)
	}
	return &fieldFit{
		target:     target,
		plan:       plan,
		columns:    columns,
		references: references,
		rec:        plan.newRecord(),
		choices:    choices,
	}, ""
}

// dropWeakestChoice returns this fit's admitted predictors with the
// lowest-scoring one removed, or false when there is nothing left to
// give up.
//
// It is the narrowing step behind maxModelRefitRounds. Ties break on the
// LATER field in schema order, purely so the choice is deterministic;
// which of two equally-explanatory candidates survives changes the
// reading of the coefficients and no fitted value.
func dropWeakestChoice(choices []predictorChoice) ([]predictorChoice, bool) {
	if len(choices) < 2 {
		return nil, false
	}
	worst := 0
	for i := 1; i < len(choices); i++ {
		if choices[i].score <= choices[worst].score {
			worst = i
		}
	}
	out := make([]predictorChoice, 0, len(choices)-1)
	out = append(out, choices[:worst]...)
	out = append(out, choices[worst+1:]...)
	return out, true
}

// buildEngine constructs the fit's OLS engine over its (already pruned)
// column set, or leaves the fit intercept-only when nothing survived.
//
// Engine construction is deliberately the LAST step: the columns it is
// handed have to be the ones the design actually carries, and
// pruneDegenerateColumns cannot run until the snapshot has been walked.
// Building first and rebuilding after would leave a window in which the
// engine and fit.columns disagree, which is precisely the kind of drift
// that surfaces as a coefficient attached to the wrong column name.
func (f *fieldFit) buildEngine(plan *dummyPlan) string {
	if len(f.columns) == 0 {
		// Intercept-only. References is cleared alongside: a baseline
		// naming a level of a field that contributes no coefficient
		// would describe a contrast the model does not carry.
		f.references = nil
		return ""
	}
	names := make([]string, len(f.columns))
	for i := range f.columns {
		names[i] = f.columns[i].Name
	}
	spec := &types.RegressionSpec{
		Type:       types.REG_OLS,
		Name:       f.target,
		Target:     plan.Target(),
		Predictors: names,
	}
	engines, err := regression.BuildStreaming([]*types.RegressionSpec{spec}, plan.viewSchema())
	if err != nil {
		return err.Error()
	}
	if len(engines) != 1 {
		return "engine construction returned no engine"
	}
	f.engine = engines[0]
	return ""
}

// pruneDegenerateColumns removes design columns that carry no
// information ON THE ROWS THIS FIT WILL ACTUALLY SEE, then builds each
// surviving fit's engine.
//
// It exists because selection and fitting apply DIFFERENT deletion
// rules, and the gap between them is the dominant cause of a
// rank-deficient design. Selection scores a candidate PAIRWISE — rows
// where that candidate and the target are both present — while the fit
// deletes a row LISTWISE, on any null among the target and every
// admitted predictor source. A level with a healthy pairwise count can
// therefore have no listwise rows at all, and its column is then
// identically zero: not wrong, just empty, and the solver refuses the
// whole design over it. On the motivating cohort that alone cost 23 of
// 105 targets their model, on fields the capture otherwise fitted
// cleanly.
//
// Two degeneracies are removed, both decided over contributing rows
// only:
//
//   - A CONSTANT column — always 0 (the level never occurs) or always 1
//     (it is the only level that occurs). The second is collinear with
//     the intercept, which is the dummy trap again in a different dress.
//
//   - A categorical whose REFERENCE level never occurs. Its surviving
//     columns then sum to 1 on every row, so one of them has to become
//     the new reference; the emitted References entry is rewritten to
//     name it, because a document that named an unobserved baseline
//     would describe a contrast no row demonstrates.
//
// What it deliberately does NOT do is detect collinearity BETWEEN
// fields (two exactly nested categoricals). That is a genuine modelling
// conflict rather than an artefact of deletion, the selection rule
// admits both by design, and the solver refusing it with a warning
// naming the field is the honest outcome.
func (f *modelFitter) pruneDegenerateColumns(fits []*fieldFit, values map[string]float64, nulls map[string]bool, wide map[string]any) []*fieldFit {
	// refOf maps each column onto the index of its field's reference
	// entry (−1 for a set option, which has none), so the per-row
	// "did this field land on its reference" test is a slice write
	// rather than a map probe in a loop that runs once per snapshot row
	// per fitted field.
	type colState struct {
		hits    []int
		refOf   []int
		refHits []int
		contrib int
	}
	states := make([]colState, len(fits))
	maxRefs := 0
	for i, fit := range fits {
		refOf := make([]int, len(fit.columns))
		for ci := range fit.columns {
			refOf[ci] = -1
			for ri := range fit.references {
				if fit.references[ri].Field == fit.columns[ci].Field {
					refOf[ci] = ri
					break
				}
			}
		}
		states[i] = colState{
			hits:    make([]int, len(fit.columns)),
			refOf:   refOf,
			refHits: make([]int, len(fit.references)),
		}
		if len(fit.references) > maxRefs {
			maxRefs = len(fit.references)
		}
	}
	refSeen := make([]bool, maxRefs)

	// Scratch is sized for the widest fit and reused: the walk is
	// (snapshot rows x fits) and a per-row allocation here would be the
	// stage's whole cost.
	widest := 0
	for _, fit := range fits {
		if len(fit.columns) > widest {
			widest = len(fit.columns)
		}
	}
	scratch := make([]float64, widest)

	for ri := range f.rows {
		f.reconstitute(ri, values, nulls, wide)
		for i, fit := range fits {
			if len(fit.columns) == 0 {
				continue
			}
			if _, ok := fit.rec.NumericValue(fit.target); !ok {
				continue
			}
			// Read the whole row's design BEFORE counting anything:
			// admission is listwise, so a column read late must be able
			// to disqualify a column read early.
			admitted := true
			for ci := range fit.columns {
				v, ok := fit.rec.NumericValue(fit.columns[ci].Name)
				if !ok {
					admitted = false
					break
				}
				scratch[ci] = v
			}
			if !admitted {
				continue
			}
			st := &states[i]
			st.contrib++
			clear(refSeen[:len(fit.references)])
			for ci := range fit.columns {
				if scratch[ci] == 0 {
					continue
				}
				st.hits[ci]++
				if r := st.refOf[ci]; r >= 0 {
					refSeen[r] = true
				}
			}
			for ri := range fit.references {
				if !refSeen[ri] {
					st.refHits[ri]++
				}
			}
		}
	}

	kept := fits[:0]
	for i, fit := range fits {
		fit.prune(states[i].hits, states[i].refOf, states[i].refHits, states[i].contrib)
		if why := fit.buildEngine(fit.plan); why != "" {
			fit.failure = why
			continue
		}
		kept = append(kept, fit)
	}
	return kept
}

// prune rewrites this fit's column and reference lists to the columns
// that carry information over the `contrib` rows the fit will admit.
//
// hits[ci] is the number of admitted rows on which column ci was
// non-zero; refOf[ci] is that column's field's reference index (−1 for a
// set option); refHits[ri] is the number of admitted rows that landed on
// reference ri, i.e. carried none of that field's columns.
//
// Three drops, in one pass over the columns:
//
//   - hits == 0: the level never occurs among admitted rows, so the
//     column is identically zero.
//   - hits == contrib: the level occurs on EVERY admitted row, so the
//     column is identically one and collinear with the intercept.
//   - the field's reference level never occurs (refHits == 0) and the
//     field still contributes columns: those columns sum to 1 on every
//     admitted row, so the first surviving one becomes the new
//     reference and its ModelReference entry is rewritten to name it.
//
// A field left with no columns loses its reference entry outright: a
// baseline is only meaningful as the arm some other coefficient is read
// against.
func (f *fieldFit) prune(hits []int, refOf []int, refHits []int, contrib int) {
	if len(f.columns) == 0 {
		return
	}
	// Pass one: mark the columns that survive on their own merit.
	keep := make([]bool, len(f.columns))
	surviving := make([]int, len(f.references))
	for ci := range f.columns {
		if hits[ci] == 0 || hits[ci] == contrib {
			continue
		}
		keep[ci] = true
		if r := refOf[ci]; r >= 0 {
			surviving[r]++
		}
	}
	// Pass two: a field whose reference is unobserved needs one of its
	// survivors promoted into that role, or the design is rank-deficient
	// against the intercept.
	for ci := range f.columns {
		if !keep[ci] {
			continue
		}
		r := refOf[ci]
		if r < 0 || refHits[r] > 0 || surviving[r] < 1 {
			continue
		}
		keep[ci] = false
		surviving[r]--
		f.references[r].Level = f.columns[ci].Level
		refHits[r] = -1 // promoted; the field is settled
	}

	cols := f.columns[:0]
	for ci := range f.columns {
		if keep[ci] {
			cols = append(cols, f.columns[ci])
		}
	}
	f.columns = cols

	refs := f.references[:0]
	for r := range f.references {
		if surviving[r] > 0 {
			refs = append(refs, f.references[r])
		}
	}
	f.references = refs
}

// finish selects each target's predictors, fits every model over the
// retained snapshot, and returns the surviving models in target schema
// order.
//
// A fit that fails at finalize is not immediately given up. Selection
// scores candidates MARGINALLY, so two collinear ones (`age` is a
// banding of `ageExact`; `region` is nested inside `dma`) both clear the
// floor and the design comes out rank-deficient — on the motivating
// cohort that was 23 of 105 targets, every one of them the same
// `age`/`ageExact` pair. Rather than encode a redundancy theory in the
// selection rule, the solver's refusal IS the redundancy signal: the fit
// is retried with its weakest-scoring admitted predictor dropped, up to
// maxModelRefitRounds times. The candidate that survives a nesting is
// therefore the one explaining more of the target, which is the only
// non-arbitrary answer available.
//
// A fit that exhausts its refits — or has nothing left to give up — is
// dropped with a warning naming the field, its ORIGINAL diagnosis and
// the predictors it was carrying at the time. It must never fail the
// profile run: the models are an ADDITION to a document that is complete
// without them, and SpecFromProfile falls back to that field's captured
// conditional pair, so a skip costs the field its model and not its
// structure.
func (f *modelFitter) finish(warnings *[]string) []FieldModel {
	if len(f.rows) == 0 {
		for _, target := range f.targets {
			*warnings = append(*warnings, modelSkipWarning(target,
				"the cohort retained no rows to fit against"))
		}
		return nil
	}

	survey := f.buildSurvey()
	values := make(map[string]float64, len(f.snapFields))
	nulls := make(map[string]bool, len(f.snapFields))
	wide := make(map[string]any, len(f.snapFields))

	var pending []*fieldFit
	for ti, target := range f.targets {
		choices := survey.selectionFor(ti, f.topK)
		fit, why := f.newFieldFit(target, choices)
		if why != "" {
			*warnings = append(*warnings, modelSkipWarning(target, why))
			continue
		}
		pending = append(pending, fit)
	}

	var kept []*fieldFit
	for round := 0; len(pending) > 0; round++ {
		for _, fit := range pending {
			fit.rec.bind(values, nulls, wide)
		}
		survivors := f.pruneDegenerateColumns(pending, values, nulls, wide)
		f.drive(survivors, values, nulls, wide, warnings)

		var retry []*fieldFit
		for _, fit := range pending {
			if fit.engine == nil && len(fit.columns) > 0 {
				// Dropped by pruneDegenerateColumns or by drive; its
				// failure is already recorded.
			} else {
				model, why := fit.finalize()
				if why == "" {
					if what, ok := finiteModel(model); !ok {
						why = "fit produced a non-finite " + what
					}
				}
				if why == "" {
					fit.model = model
					if len(fit.columns) == 0 {
						*warnings = append(*warnings, modelNoPredictorWarning(fit.target))
					}
					kept = append(kept, fit)
					continue
				}
				fit.failure = why
			}
			narrowed, ok := dropWeakestChoice(fit.choices)
			if !ok || round >= maxModelRefitRounds {
				*warnings = append(*warnings, modelSkipWarning(fit.target,
					fit.failure+" [predictors: "+fit.predictorFieldList()+"]"))
				continue
			}
			next, why := f.newFieldFit(fit.target, narrowed)
			if why != "" {
				*warnings = append(*warnings, modelSkipWarning(fit.target, why))
				continue
			}
			next.failure = fit.failure
			retry = append(retry, next)
		}
		pending = retry
	}
	if len(kept) == 0 {
		return nil
	}

	// Residuals are computed against the FINAL set of models, so the
	// refits above must all have settled first.
	f.fits = kept
	for _, fit := range kept {
		fit.rec.bind(values, nulls, wide)
	}
	f.computeResiduals(kept)

	out := make([]FieldModel, 0, len(kept))
	for _, fit := range f.targetOrder(kept) {
		out = append(out, *fit.model)
	}
	return out
}

// drive folds every retained snapshot row into each fit. The row loop is
// OUTER and the fit loop INNER so a retained row is reconstituted into
// the decode maps exactly once regardless of how many targets are in
// flight.
func (f *modelFitter) drive(fits []*fieldFit, values map[string]float64, nulls map[string]bool, wide map[string]any, warnings *[]string) {
	for ri := range f.rows {
		f.reconstitute(ri, values, nulls, wide)
		for _, fit := range fits {
			if fit.engine == nil {
				if y, ok := fit.rec.NumericValue(fit.target); ok {
					fit.only.add(y)
				}
				continue
			}
			if err := fit.engine.UpdateRow(fit.rec); err != nil {
				// UpdateRow's only error is a post-Finalize call, which
				// cannot happen here; treat it as this field's failure
				// rather than the run's, exactly as a finalize error is
				// treated.
				*warnings = append(*warnings, modelSkipWarning(fit.target, err.Error()))
				fit.engine = nil
				fit.columns = nil
				fit.choices = nil
			}
		}
	}
}

// targetOrder returns the fits sorted by their target's position in the
// schema, restoring the emission order the refit loop's round-by-round
// completion breaks. The document's `models` array is target schema
// order, which is what lets a reader zip it against `fields`.
func (f *modelFitter) targetOrder(fits []*fieldFit) []*fieldFit {
	at := make(map[string]int, len(f.targets))
	for i, name := range f.targets {
		at[name] = i
	}
	out := append([]*fieldFit(nil), fits...)
	sort.SliceStable(out, func(i, j int) bool { return at[out[i].target] < at[out[j].target] })
	return out
}

// finalize closes one fit and renders it as a FieldModel, or returns the
// reason it cannot be rendered.
//
// The intercept-only arm reports R2 = 0 and ResidualStd = the target's
// own sample standard deviation, which is exactly what "no predictor
// explains this field" means: the linear predictor is the field's mean
// and the residual carries all of its spread. Generation reading such a
// model back therefore reproduces the marginal, which is the correct
// answer rather than a degenerate one.
func (f *fieldFit) finalize() (*FieldModel, string) {
	if f.engine == nil {
		if f.only.n == 0 {
			return nil, "no retained row carries a value for this field"
		}
		return &FieldModel{
			Field:       f.target,
			Intercept:   f.only.mean,
			Predictors:  []ModelPredictor{},
			NObs:        f.only.n,
			R2:          0,
			ResidualStd: f.only.std(),
		}, ""
	}
	res, err := f.engine.Finalize()
	if err != nil {
		return nil, err.Error()
	}
	model := &FieldModel{
		Field:       f.target,
		Intercept:   res.Coefficients[regression.InterceptKey],
		Predictors:  make([]ModelPredictor, 0, len(f.columns)),
		References:  f.references,
		NObs:        res.NObs,
		R2:          res.R2,
		ResidualStd: res.ResidualStdErr,
	}
	for _, c := range f.columns {
		model.Predictors = append(model.Predictors, ModelPredictor{
			Column:      c.Name,
			Kind:        modelPredictorKind(c.Kind),
			Field:       c.Field,
			Level:       c.Level,
			Coefficient: res.Coefficients[c.Name],
		})
	}
	return model, ""
}

// predictorFieldList names the fit's admitted predictor SOURCE fields,
// in design order, for diagnostics.
func (f *fieldFit) predictorFieldList() string {
	seen := make(map[string]bool, len(f.columns))
	var out []string
	for i := range f.columns {
		if seen[f.columns[i].Field] {
			continue
		}
		seen[f.columns[i].Field] = true
		out = append(out, f.columns[i].Field)
	}
	return strings.Join(out, ", ")
}

// reconstitute expands retained row ri back into the three decode maps
// every Record adapter is bound to.
func (f *modelFitter) reconstitute(ri int, values map[string]float64, nulls map[string]bool, wide map[string]any) {
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
}

// computeResiduals replays the retained snapshot rows through each
// surviving model.
//
// A residual needs the coefficients, which do not exist until Finalize,
// so it is necessarily a second walk of the snapshot — never of the
// cohort, which is read once and only once. The row loop is OUTER and
// the model loop INNER so each retained row is reconstituted into the
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
		f.reconstitute(ri, values, nulls, wide)
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
