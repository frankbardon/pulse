package synth

import (
	"encoding/json"
	"fmt"

	"github.com/frankbardon/pulse/errors"
)

// Spec is the parsed top-level synthesis request. It is the in-memory
// shape of from-schema JSON. A from-profile call builds a Spec internally
// from the Profile and shares the rest of the writer pipeline.
type Spec struct {
	// RowCount is the number of rows to generate. Required (>0).
	RowCount int `json:"row_count"`

	// Fields declares each column with its type and distribution.
	Fields []FieldSpec `json:"fields"`

	// Constraints, if non-empty, are evaluated per row by the expression
	// evaluator. Rows that fail any constraint are rejected and re-drawn.
	Constraints []ConstraintSpec `json:"constraints,omitempty"`

	// MaxRejectionRate caps the fraction of rejected rows during
	// constraint-driven rejection sampling. Defaults to 0.5 if zero.
	MaxRejectionRate float64 `json:"max_rejection_rate,omitempty"`

	// Correlations lists optional pairwise correlations to induce via
	// Gaussian copula post-processing. Each entry references two numeric
	// fields by name with a target Pearson correlation in [-1, 1]. Only
	// numeric fields can participate.
	Correlations []CorrelationSpec `json:"correlations,omitempty"`

	// CategoricalPairs lists optional categorical-categorical joint
	// structure to reproduce during generation: for each pair, field B
	// is resampled conditioned on field A's independently-drawn value
	// using the captured contingency cells, instead of being drawn from
	// its own unconditional marginal. Populated by SpecFromProfile from
	// ConditionalProfile.CategoricalPairs (`profile create --conditional`)
	// when present; absent (nil, the zero value) reproduces today's
	// independent-marginal behavior exactly — every pre-existing spec,
	// hand-authored or profile-derived, has no such key.
	CategoricalPairs []CategoricalPairSpec `json:"categorical_pairs,omitempty"`

	// CategoricalNumericPairs lists optional categorical-numeric
	// conditional structure to reproduce during generation: for each
	// pair, numeric field B is resampled from Normal(mean, std)
	// parameterized by categorical field A's drawn value, using the
	// captured per-category conditional mean/std, instead of being drawn
	// from its own unconditional normal. Populated by SpecFromProfile
	// from ConditionalProfile.CategoricalNumericPairs when present;
	// absent (nil) reproduces today's independent-marginal behavior
	// exactly.
	CategoricalNumericPairs []CategoricalNumericPairSpec `json:"categorical_numeric_pairs,omitempty"`

	// SetCategoricalPairs lists optional set-option x categorical joint
	// structure to reproduce during generation (E5-S3): for each pair,
	// the set field's declared Option (bit) is resampled from
	// Bernoulli(P(selected | categorical field's drawn value)) using the
	// captured contingency cells, instead of the option's own
	// independent marginal frequency. Populated by SpecFromProfile from
	// ConditionalProfile.SetCategoricalPairs when present; absent (nil)
	// reproduces the independent per-option marginal behavior exactly.
	SetCategoricalPairs []SetCategoricalPairSpec `json:"set_categorical_pairs,omitempty"`

	// SetNumericPairs lists optional set-option x numeric joint
	// structure to reproduce during generation (E5-S3): for each pair,
	// numeric field B is resampled from Normal(mean, std) conditioned on
	// whether the set field's declared Option (bit) was independently
	// drawn selected, using the captured per-bucket conditional
	// mean/std. Populated by SpecFromProfile from
	// ConditionalProfile.SetNumericPairs when present; absent (nil)
	// reproduces today's independent-marginal behavior exactly.
	SetNumericPairs []SetNumericPairSpec `json:"set_numeric_pairs,omitempty"`

	// SetSetPairs lists optional option x option joint structure between
	// two DIFFERENT set_* fields to reproduce during generation (E5-S3):
	// for each pair, set B's declared OptionB (bit) is resampled from
	// Bernoulli(P(selected | set A's OptionA drawn state)) using the
	// captured 2x2 contingency cells. Populated by SpecFromProfile from
	// ConditionalProfile.SetSetPairs when present; absent (nil)
	// reproduces the independent per-option marginal behavior exactly.
	SetSetPairs []SetSetPairSpec `json:"set_set_pairs,omitempty"`

	// Models lists optional per-numeric ADDITIVE linear predictors to
	// drive generation with, the Spec-facing counterpart of the profile
	// document's `models` section (`profile create --fit-models`).
	// Populated by SpecFromProfile from Profile.Models when present;
	// absent (nil, the zero value) reproduces today's behaviour exactly
	// — every pre-existing spec, hand-authored or profile-derived, has
	// no such key.
	//
	// Where CategoricalNumericPairs and SetNumericPairs each RESAMPLE a
	// numeric field from one paired field's conditional moments — so two
	// pairs naming the same numeric compete to be the last writer, and
	// resolveConflicts has to drop one — a model accounts for every
	// predictor in a single expression. SpecFromProfile therefore treats
	// the two as alternatives rather than layers: a profile carrying
	// `models` populates this slot and leaves both numeric-target pair
	// slots empty.
	Models []FieldModelSpec `json:"models,omitempty"`

	// ResidualCorrelations lists optional target correlations between
	// the RESIDUALS of two modelled numeric fields — the Spec-facing
	// counterpart of the profile document's `residual_correlations`
	// section (`profile create --residual-correlations`), populated by
	// SpecFromProfile from Profile.ResidualCorrelations when present.
	// Absent (nil, the zero value) reproduces the independent-residual
	// draw exactly: every modelled field takes its own fresh z, which
	// is what every pre-existing spec gets.
	//
	// This is a DIFFERENT quantity from Correlations above and the two
	// are not interchangeable. Correlations names a correlation between
	// two fields' VALUES and is realized by drawing both values from a
	// shared copula, which requires owning the value outright. Once a
	// field carries a model, its value is the model's to produce and
	// the only part still free to move is the residual — so this slot
	// correlates THAT, supplying each modelled field its component of
	// one shared correlated normal vector instead of overwriting what
	// the model drew. Imposing a value-scale figure on the residual
	// draw would apply every predictor the two targets share a second
	// time (see synth/residual_corr.go), which is why a Correlations
	// entry naming a modelled field is excluded rather than rerouted
	// here.
	//
	// Entries reuse CorrelationSpec verbatim: a pair of field names and
	// a rho, read on the residual scale. Both endpoints must carry a
	// model; an entry naming a field whose model did not survive is
	// dropped with a warning at generate() time, and validateSpec
	// refuses one outright for a hand-authored spec.
	ResidualCorrelations []CorrelationSpec `json:"residual_correlations,omitempty"`

	// Rules lists optional STRUCTURAL rules — statements about which
	// fields a row may carry a value for and what that value is, imposed
	// on top of whatever the distributions, conditional pairs,
	// correlations and models produced. Additive and omitempty: absent
	// (nil, the zero value) reproduces today's behaviour exactly, and
	// because the rule pass consumes no RNG, a spec declaring no rules
	// is byte-identical to one written before the slot existed.
	//
	// This is the slot for the class of fact no statistical summary can
	// express, because it is not variation at all: a question block that
	// is not ASKED of a respondent who failed a screener, a flag that is
	// a band of another column. Reconstructed from marginals those come
	// back as soft noise with a plausible rate and no gate.
	//
	// Rules apply in DECLARATION ORDER, sequentially, LAST WRITE WINS,
	// in one pass at the very end of the row — see RuleSpec for the full
	// semantics and for why they are deliberately not topologically
	// sorted. The standalone rules-file format is this array itself, so
	// an inline declaration and a `--rules` file are the same JSON.
	//
	// Validation is EAGER: validateRules refuses every malformed rule at
	// spec parse with a PULSE_SYNTH_RULE_* code naming the rule index.
	Rules []RuleSpec `json:"rules,omitempty"`
}

// FieldModelSpec is one numeric field's additive linear predictor as
// generation consumes it: the Spec-facing counterpart of FieldModel,
// carrying only what a draw needs.
//
// It is a distinct type from FieldModel rather than a re-use of it for
// the same reason every *PairProfile has its own *PairSpec: the profile
// shape is a MEASUREMENT (it carries n_obs, R2 and a residual reservoir
// that describe how well the fit was supported), while the spec shape is
// an INSTRUCTION (it carries the clamp bounds the profile keeps on the
// numeric field summary instead). Collapsing them would drag capture
// diagnostics into a hand-authorable generation surface.
type FieldModelSpec struct {
	// Field is the numeric field this model draws.
	Field string `json:"field"`
	// Intercept is the constant term — the prediction for a row sitting
	// at every categorical predictor's reference level with no set
	// option selected.
	Intercept float64 `json:"intercept"`
	// Predictors are the per-(field, level) contributions summed into
	// the prediction. A row whose drawn value for a predictor's Field is
	// not that predictor's Level contributes nothing for it.
	Predictors []ModelPredictorSpec `json:"predictors"`
	// ResidualStd is the scale of the residual added on top of the
	// linear prediction. Zero means a deterministic prediction, which is
	// a legal (if degenerate) model rather than an error.
	ResidualStd float64 `json:"residual_std"`
	// Min/Max are the numeric field's observed bounds, carried alongside
	// HasClamp exactly as CategoricalNumericPairSpec and
	// SetNumericPairSpec already carry them. They are the same bounds
	// the field's own reconstructed marginal would clamp to; whether and
	// where the draw applies them is the generation stage's decision,
	// and this slot only guarantees the numbers are available to it
	// without a second lookup into the profile.
	Min      float64 `json:"min,omitempty"`
	Max      float64 `json:"max,omitempty"`
	HasClamp bool    `json:"has_clamp,omitempty"`
}

// ModelPredictorSpec is one (field, level) contribution of a
// FieldModelSpec.
//
// Kind distinguishes the two indicator semantics and is not derivable
// from (Field, Level): a categorical_level column is one arm of a
// partition whose omitted reference level is the zero baseline, while a
// set_option column is an independent indicator with no reference at
// all. The internal design-column name the solver used is deliberately
// absent — (Field, Level) is the addressing key here exactly as it is on
// the profile document.
type ModelPredictorSpec struct {
	// Kind is one of the ModelPredictor* constants
	// (ModelPredictorCategoricalLevel / ModelPredictorSetOption /
	// ModelPredictorNumeric).
	Kind string `json:"kind"`
	// Field is the predictor's source field.
	Field string `json:"field"`
	// Level is the categorical level text or set option text that turns
	// this contribution on.
	Level string `json:"level"`
	// Coefficient is added to the prediction when the indicator is 1.
	Coefficient float64 `json:"coefficient"`
}

// SetCategoricalPairSpec is one set-option x categorical pair's
// generation-time reconstruction input — the Spec-facing counterpart of
// SetCategoricalPairProfile. Cells reuse CategoricalPairCellSpec
// verbatim: AValue is always "selected"/"not_selected" (the set
// option's own fixed two-value domain), BValue is the categorical
// field's observed value.
type SetCategoricalPairSpec struct {
	Set         string                    `json:"set"`
	Option      string                    `json:"option"`
	Categorical string                    `json:"categorical"`
	Cells       []CategoricalPairCellSpec `json:"cells"`
}

// SetNumericPairSpec is one set-option x numeric pair's generation-time
// reconstruction input — the Spec-facing counterpart of
// SetNumericPairProfile. Categories carries at most two entries keyed
// "selected" / "not_selected", reusing CategoricalNumericCategorySpec
// verbatim. Min/Max/HasClamp mirror CategoricalNumericPairSpec: the
// numeric field's own observed range, applied identically regardless of
// which bucket the conditional draw lands in.
type SetNumericPairSpec struct {
	Set        string                           `json:"set"`
	Option     string                           `json:"option"`
	Numeric    string                           `json:"numeric"`
	Categories []CategoricalNumericCategorySpec `json:"categories"`
	Min        float64                          `json:"min,omitempty"`
	Max        float64                          `json:"max,omitempty"`
	HasClamp   bool                             `json:"has_clamp,omitempty"`
	// Bernoulli is RETIRED: accepted so every document written before
	// the retirement still parses, and READ BY NOTHING.
	//
	// What it used to declare — the numeric target is a BOOLEAN
	// marginal, so the cell's captured Mean is a prevalence and the
	// draw is Bernoulli(Mean) rather than Normal(Mean, Std) — is now
	// DERIVED from the target's own FieldSpec at generate() setup
	// (buildBernoulliConditionals), exactly as the `discrete` staircase
	// always was.
	//
	// The retirement is the point, not a tidy-up. A flag can DISAGREE
	// with the field's own reconstruction, and a marginal disagreeing
	// with the draw that overwrites it is the failure class the boolean
	// and small-integer work exists to remove — invisible, because every
	// cell still renders a plausible prevalence and only the number is
	// wrong. It was not hypothetical: SpecFromProfile's SET-numeric arm
	// admitted a DistBernoulli target, said in a comment that the flag
	// was "carried the same way" as the categorical-numeric arm's, and
	// did not set it, so a profile-derived set-numeric pair over a
	// boolean target reproduced the clamped-normal defect once PER CELL.
	// Deriving makes that omission unrepresentable.
	//
	// A spec still declaring it over a target whose marginal is NOT
	// bernoulli gets a warning naming the pair (bernoulliFlagWarnings),
	// never a refusal and never silence; declare the FIELD `bernoulli`
	// instead. Do not reintroduce a read of this slot.
	//
	// Deprecated: declare the target field's distribution as
	// DistBernoulli; this slot is ignored.
	Bernoulli bool `json:"bernoulli,omitempty"`
}

// SetSetPairSpec is one option x option pair's generation-time
// reconstruction input between two DIFFERENT set_* fields — the
// Spec-facing counterpart of SetSetPairProfile. Cells reuse
// CategoricalPairCellSpec verbatim: AValue is SetA's OptionA state
// ("selected"/"not_selected"), BValue is SetB's OptionB state.
type SetSetPairSpec struct {
	SetA    string                    `json:"set_a"`
	OptionA string                    `json:"option_a"`
	SetB    string                    `json:"set_b"`
	OptionB string                    `json:"option_b"`
	Cells   []CategoricalPairCellSpec `json:"cells"`
}

// CategoricalPairSpec is one categorical-categorical pair's generation-time
// reconstruction input — the Spec-facing counterpart of
// CategoricalPairProfile, carrying only what a conditional resample needs
// (the cells; N is capture-time provenance and is not needed here).
type CategoricalPairSpec struct {
	A     string                    `json:"a"`
	B     string                    `json:"b"`
	Cells []CategoricalPairCellSpec `json:"cells"`
}

// CategoricalPairCellSpec is one observed (a_value, b_value) co-occurrence
// count — the Spec-facing counterpart of ContingencyCell.
type CategoricalPairCellSpec struct {
	AValue string `json:"a_value"`
	BValue string `json:"b_value"`
	Count  int    `json:"count"`
}

// CategoricalNumericPairSpec is one categorical-numeric pair's
// generation-time reconstruction input — the Spec-facing counterpart of
// CategoricalNumericPairProfile, carrying only what a conditional resample
// needs (each category's mean/std, plus the numeric field's own overall
// min/max so the conditional draw clamps exactly as the field's
// unconditional `normal` reconstruction already does; N is capture-time
// provenance and is not needed here).
type CategoricalNumericPairSpec struct {
	A          string                           `json:"a"`
	B          string                           `json:"b"`
	Categories []CategoricalNumericCategorySpec `json:"categories"`
	// Min/Max clamp the conditional draw exactly as field B's own
	// unconditional normal reconstruction clamps to its observed range
	// (see SpecFromProfile). HasClamp distinguishes "clamp to [0,0]"
	// (rare, but a legitimate observed range for a constant field) from
	// "no clamp was declared" — a hand-authored from-schema spec that
	// omits Min/Max gets no clamping, exactly as normal's own optional
	// min/max params behave when absent.
	Min      float64 `json:"min,omitempty"`
	Max      float64 `json:"max,omitempty"`
	HasClamp bool    `json:"has_clamp,omitempty"`
	// Bernoulli is RETIRED, exactly as
	// CategoricalNumericPairSpec.Bernoulli is — accepted so an older
	// document still parses, read by nothing, derived from the target's
	// own FieldSpec instead. THIS is the slot whose silent omission
	// proved the case: SpecFromProfile never set it, so every
	// profile-derived set-numeric pair over a boolean target drew a
	// clamped normal. See CategoricalNumericPairSpec.Bernoulli for the
	// full reasoning.
	//
	// Deprecated: declare the target field's distribution as
	// DistBernoulli; this slot is ignored.
	Bernoulli bool `json:"bernoulli,omitempty"`
}

// CategoricalNumericCategorySpec is the numeric field's conditional
// mean/std for one observed category value (or the "other" catch-all) of
// the paired categorical field — the Spec-facing counterpart of
// CategoricalNumericCategoryStat.
type CategoricalNumericCategorySpec struct {
	Category string  `json:"category"`
	Mean     float64 `json:"mean"`
	Std      float64 `json:"std"`
}

// FieldSpec is a single column declaration.
type FieldSpec struct {
	Name         string         `json:"name"`
	Type         string         `json:"type"`
	Nullable     bool           `json:"nullable,omitempty"`
	Description  string         `json:"description,omitempty"`
	Distribution string         `json:"distribution"`
	Params       map[string]any `json:"params,omitempty"`

	// Precision and Scale apply to decimal128.
	Precision uint8 `json:"precision,omitempty"`
	Scale     uint8 `json:"scale,omitempty"`

	// NullRate is the per-row probability that the field will be null.
	// Only meaningful when Nullable is true; ignored otherwise.
	NullRate float64 `json:"null_rate,omitempty"`
}

// ConstraintSpec wraps an expression evaluated against the in-memory row.
type ConstraintSpec struct {
	Expr string `json:"expr"`
}

// CorrelationSpec declares a target Pearson correlation between two
// numeric fields.
type CorrelationSpec struct {
	A           string  `json:"a"`
	B           string  `json:"b"`
	Correlation float64 `json:"correlation"`
}

// Options modulates how the spec is realized.
type Options struct {
	// Seed makes the output deterministic. Same spec + same seed must
	// produce a byte-identical .pulse file.
	Seed int64

	// SourceCohort activates the tagged top-up path (AugmentFromProfile)
	// when non-empty: real rows from this cohort are copied into the
	// output tagged _synthetic=false, spec.RowCount new rows are
	// generated and tagged _synthetic=true, and the source is never
	// opened for write. Leaving it empty (the default) reproduces
	// today's behavior exactly — a plain synthesis with no tag column,
	// used by synth from-schema and any caller building a Spec directly.
	SourceCohort string

	// FidelityReportPath activates the post-generation fidelity report
	// (`synth from-profile --fidelity-report`) when non-empty: after
	// generation completes, AugmentFromProfile drives TEST_KS (numeric
	// fields, SplitBy=_synthetic) / TEST_CHISQ (categorical fields,
	// contingency against _synthetic) against the tagged output cohort
	// and writes the resulting FidelityReport JSON document to this
	// path. Only meaningful alongside SourceCohort — the plain Synth
	// path (SourceCohort empty) has no _synthetic partition to compare
	// against and ignores this field entirely.
	FidelityReportPath string

	// FidelityWarnings, when non-empty, are copied verbatim onto the
	// written FidelityReport's own Warnings slot — the mechanism that
	// surfaces thin-pair warnings (e.g. Profile.Warnings from a
	// `profile create --conditional` capture) beside the pairwise
	// correlation deltas they qualify in the same document (FR-18),
	// rather than requiring a caller to cross-reference the profile
	// file separately. This package has no Profile type of its own to
	// read Warnings from directly — the caller (internal/cli's `synth
	// from-profile`, or any programmatic caller building Options from a
	// Profile it already has in hand) is expected to populate this.
	// Ignored when FidelityReportPath is empty, exactly like every
	// other Fidelity* field.
	FidelityWarnings []string
}

// Result is what the writer reports after a successful Synth call.
type Result struct {
	RowsGenerated int      `json:"rows_generated"`
	RowsRejected  int      `json:"rows_rejected"`
	OutputPath    string   `json:"output_path"`
	Warnings      []string `json:"warnings,omitempty"`

	// FidelityReportPath echoes Options.FidelityReportPath when a
	// fidelity report was written alongside the output cohort. Empty
	// when the flag was not set.
	FidelityReportPath string `json:"fidelity_report_path,omitempty"`
}

// ParseSpec parses Spec JSON. Returns SERVICE_VALIDATION if shape is wrong.
func ParseSpec(raw []byte) (*Spec, error) {
	var s Spec
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_VALIDATION, "parsing synth spec")
	}
	if err := validateSpec(&s); err != nil {
		return nil, err
	}
	return &s, nil
}

func validateSpec(s *Spec) error {
	if s.RowCount <= 0 {
		return errors.NewCodedError(errors.SERVICE_VALIDATION, "row_count must be > 0")
	}
	if len(s.Fields) == 0 {
		return errors.NewCodedError(errors.SERVICE_VALIDATION, "spec must declare at least one field")
	}
	seen := make(map[string]bool, len(s.Fields))
	for i, f := range s.Fields {
		if f.Name == "" {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"field has empty name", map[string]any{"index": i})
		}
		if seen[f.Name] {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"duplicate field name", map[string]any{"name": f.Name})
		}
		seen[f.Name] = true
		if f.Type == "" {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"field has empty type", map[string]any{"name": f.Name})
		}
		if f.Distribution == "" {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"field has empty distribution", map[string]any{"name": f.Name})
		}
		if f.NullRate < 0 || f.NullRate > 1 {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"null_rate must be in [0, 1]", map[string]any{"name": f.Name, "null_rate": f.NullRate})
		}
	}
	for _, c := range s.Correlations {
		if c.A == c.B {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"correlation a and b must differ", map[string]any{"a": c.A})
		}
		if c.Correlation < -1 || c.Correlation > 1 {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"correlation must be in [-1, 1]",
				map[string]any{"a": c.A, "b": c.B, "value": c.Correlation})
		}
		if !seen[c.A] || !seen[c.B] {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"correlation references unknown field",
				map[string]any{"a": c.A, "b": c.B})
		}
	}
	for _, cp := range s.CategoricalPairs {
		if cp.A == cp.B {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"categorical pair a and b must differ", map[string]any{"a": cp.A})
		}
		if !seen[cp.A] || !seen[cp.B] {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"categorical pair references unknown field",
				map[string]any{"a": cp.A, "b": cp.B})
		}
		if len(cp.Cells) == 0 {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"categorical pair must declare at least one cell",
				map[string]any{"a": cp.A, "b": cp.B})
		}
	}
	for _, cnp := range s.CategoricalNumericPairs {
		if cnp.A == cnp.B {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"categorical-numeric pair a and b must differ", map[string]any{"a": cnp.A})
		}
		if !seen[cnp.A] || !seen[cnp.B] {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"categorical-numeric pair references unknown field",
				map[string]any{"a": cnp.A, "b": cnp.B})
		}
		if len(cnp.Categories) == 0 {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"categorical-numeric pair must declare at least one category",
				map[string]any{"a": cnp.A, "b": cnp.B})
		}
	}
	for _, scp := range s.SetCategoricalPairs {
		if !seen[scp.Set] || !seen[scp.Categorical] {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"set-categorical pair references unknown field",
				map[string]any{"set": scp.Set, "categorical": scp.Categorical})
		}
		if len(scp.Cells) == 0 {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"set-categorical pair must declare at least one cell",
				map[string]any{"set": scp.Set, "categorical": scp.Categorical})
		}
	}
	for _, snp := range s.SetNumericPairs {
		if !seen[snp.Set] || !seen[snp.Numeric] {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"set-numeric pair references unknown field",
				map[string]any{"set": snp.Set, "numeric": snp.Numeric})
		}
		if len(snp.Categories) == 0 {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"set-numeric pair must declare at least one category",
				map[string]any{"set": snp.Set, "numeric": snp.Numeric})
		}
	}
	for _, ssp := range s.SetSetPairs {
		if ssp.SetA == ssp.SetB {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"set-set pair set_a and set_b must differ", map[string]any{"set_a": ssp.SetA})
		}
		if !seen[ssp.SetA] || !seen[ssp.SetB] {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"set-set pair references unknown field",
				map[string]any{"set_a": ssp.SetA, "set_b": ssp.SetB})
		}
		if len(ssp.Cells) == 0 {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"set-set pair must declare at least one cell",
				map[string]any{"set_a": ssp.SetA, "set_b": ssp.SetB})
		}
	}
	modelled := make(map[string]bool, len(s.Models))
	for _, m := range s.Models {
		if !seen[m.Field] {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"model references unknown field", map[string]any{"field": m.Field})
		}
		// One field, one model. Two additive predictors for the same
		// target are not a conflict this can arbitrate the way
		// resolveConflicts arbitrates competing pair claims — a pair
		// claim loses a relationship, whereas a second model would
		// silently redefine the whole draw — so it is refused outright.
		if modelled[m.Field] {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"duplicate model for field", map[string]any{"field": m.Field})
		}
		modelled[m.Field] = true
		if m.ResidualStd < 0 {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"model residual_std must be >= 0",
				map[string]any{"field": m.Field, "residual_std": m.ResidualStd})
		}
		for _, pr := range m.Predictors {
			if !seen[pr.Field] {
				return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
					"model predictor references unknown field",
					map[string]any{"field": m.Field, "predictor": pr.Field})
			}
			if pr.Field == m.Field {
				return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
					"model predictor may not be its own target",
					map[string]any{"field": m.Field})
			}
		}
	}
	// A residual correlation is a statement about two MODELS, not about
	// two fields, so "both endpoints carry a model" is a validity
	// condition rather than an arbitration outcome: a field with no
	// model has no residual for the figure to describe, and there is no
	// weaker reading to fall back on. Refusing here keeps the
	// hand-authored surface honest; the profile-derived path bypasses
	// validateSpec and instead drops such an entry with a warning at
	// generate() time (buildResidualCorrelator), because there the
	// unmodelled endpoint is the downstream consequence of a model drop
	// that was already reported on its own terms.
	for _, rc := range s.ResidualCorrelations {
		if rc.A == rc.B {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"residual correlation a and b must differ", map[string]any{"a": rc.A})
		}
		if rc.Correlation < -1 || rc.Correlation > 1 {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"residual correlation must be in [-1, 1]",
				map[string]any{"a": rc.A, "b": rc.B, "value": rc.Correlation})
		}
		if !seen[rc.A] || !seen[rc.B] {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"residual correlation references unknown field",
				map[string]any{"a": rc.A, "b": rc.B})
		}
		if !modelled[rc.A] || !modelled[rc.B] {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"residual correlation references a field with no model",
				map[string]any{"a": rc.A, "b": rc.B})
		}
	}
	if s.MaxRejectionRate < 0 || s.MaxRejectionRate >= 1 {
		return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			"max_rejection_rate must be in [0, 1)", map[string]any{"value": s.MaxRejectionRate})
	}
	// Rules are validated LAST, so a rule naming a field is reported
	// only once the field list itself is known good — a duplicate or
	// unnamed field would otherwise make "rule names an unknown field"
	// the first thing an author sees about a spec whose fields are the
	// real problem.
	if err := validateRules(s); err != nil {
		return err
	}
	return nil
}

// paramFloat extracts a float64 parameter from FieldSpec.Params.
func paramFloat(name string, params map[string]any, key string, def float64) (float64, bool, error) {
	v, ok := params[key]
	if !ok {
		return def, false, nil
	}
	switch x := v.(type) {
	case float64:
		return x, true, nil
	case int:
		return float64(x), true, nil
	case int64:
		return float64(x), true, nil
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return 0, false, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				fmt.Sprintf("field %q: param %q must be a number", name, key),
				map[string]any{"value": fmt.Sprint(v)})
		}
		return f, true, nil
	default:
		return 0, false, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: param %q must be a number", name, key),
			map[string]any{"value": fmt.Sprint(v)})
	}
}

// paramInt extracts an integer parameter.
func paramInt(name string, params map[string]any, key string, def int64) (int64, bool, error) {
	f, ok, err := paramFloat(name, params, key, float64(def))
	if err != nil {
		return 0, false, err
	}
	return int64(f), ok, nil
}

// paramString extracts a string parameter.
func paramString(name string, params map[string]any, key, def string) (string, bool, error) {
	v, ok := params[key]
	if !ok {
		return def, false, nil
	}
	s, isStr := v.(string)
	if !isStr {
		return "", false, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: param %q must be a string", name, key),
			map[string]any{"value": fmt.Sprint(v)})
	}
	return s, true, nil
}

// paramStringSlice extracts a []string from params.
func paramStringSlice(name string, params map[string]any, key string) ([]string, bool, error) {
	v, ok := params[key]
	if !ok {
		return nil, false, nil
	}
	arr, isArr := v.([]any)
	if !isArr {
		return nil, false, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: param %q must be an array", name, key), nil)
	}
	out := make([]string, 0, len(arr))
	for i, e := range arr {
		s, ok := e.(string)
		if !ok {
			return nil, false, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				fmt.Sprintf("field %q: param %q[%d] must be a string", name, key, i), nil)
		}
		out = append(out, s)
	}
	return out, true, nil
}

// paramFloatSlice extracts a []float64 from params.
func paramFloatSlice(name string, params map[string]any, key string) ([]float64, bool, error) {
	v, ok := params[key]
	if !ok {
		return nil, false, nil
	}
	arr, isArr := v.([]any)
	if !isArr {
		return nil, false, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: param %q must be an array", name, key), nil)
	}
	out := make([]float64, 0, len(arr))
	for i, e := range arr {
		switch x := e.(type) {
		case float64:
			out = append(out, x)
		case int:
			out = append(out, float64(x))
		case int64:
			out = append(out, float64(x))
		default:
			return nil, false, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				fmt.Sprintf("field %q: param %q[%d] must be a number", name, key, i), nil)
		}
	}
	return out, true, nil
}
