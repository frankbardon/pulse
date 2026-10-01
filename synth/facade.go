// Public alias facade over internal/synth, narrowed to the v1 kept
// subset: the Spec and Profile document families, Options / Result /
// ProfileOptions, the FidelityReport family, the distribution and
// model-predictor vocabulary constants, and the entry points Synth,
// SynthBytes, ProfileFile, ProfileBytes, SpecFromProfile, ParseSpec and
// WriteSpec. Every type is an alias, every constant a re-declaration and
// every function a one-line forward; generators, profile capture,
// structural-rule detectors and the fidelity builders live in
// internal/synth. Type doc comments are the implementation's own.

package synth

import (
	isynth "github.com/frankbardon/pulse/internal/synth"
	"github.com/spf13/afero"
)

// Distribution kind constants, as they appear on FieldSpec.Distribution.
const (
	DistUniform             = isynth.DistUniform
	DistNormal              = isynth.DistNormal
	DistLogNormal           = isynth.DistLogNormal
	DistExponential         = isynth.DistExponential
	DistPoisson             = isynth.DistPoisson
	DistPareto              = isynth.DistPareto
	DistBernoulli           = isynth.DistBernoulli
	DistMonotonicFrom       = isynth.DistMonotonicFrom
	DistWeightedCategorical = isynth.DistWeightedCategorical
	DistUniformDate         = isynth.DistUniformDate
	DistRegex               = isynth.DistRegex
	DistConstant            = isynth.DistConstant
	DistMixture             = isynth.DistMixture
	DistDiscrete            = isynth.DistDiscrete
	DistSetBernoulli        = isynth.DistSetBernoulli
)

// Model predictor kinds, as they appear on ModelPredictor.Kind and
// ModelPredictorSpec.Kind.
const (
	ModelPredictorCategoricalLevel = isynth.ModelPredictorCategoricalLevel
	ModelPredictorSetOption        = isynth.ModelPredictorSetOption
	ModelPredictorNumeric          = isynth.ModelPredictorNumeric
)

// RecoveryScaleProbitScore is ModelFidelity.Scale for a target recovered
// through the calibrated interval-midpoint probit score rather than
// through a point latent inverse.
//
// It is on the wire because the two comparisons are not the same
// measurement and a reader holding one must be able to tell which. A
// latent-inverse entry's recovered coefficient is a direct estimate; a
// score-scale entry's has been divided by a retention factor that is
// itself exact but that widens the standard error, so the same numeric
// gap carries less evidence.
const RecoveryScaleProbitScore = isynth.RecoveryScaleProbitScore

// ResidualUnmeasuredNoModelFit is a RECOVERY-side reason with no
// capture-side counterpart: the pair's endpoints both participated in
// generation's correlated draw, but one of their models could not be
// refitted on the synthetic partition at all (see ModelFidelity.Error),
// so there is no residual vector to correlate.
//
// It is defined here rather than beside the ResidualUnmeasured*
// constants in internal/synth/residual_corr.go on purpose. That vocabulary is
// the closed set a `residual_correlations.unmeasured` entry in a
// PROFILE DOCUMENT can carry, and a capture can never produce this
// reason — there is no refit at capture time. Mixing it in would widen
// a documented file-format vocabulary for a value that will never
// appear in a file.
const ResidualUnmeasuredNoModelFit = isynth.ResidualUnmeasuredNoModelFit

// ResidualUnmeasuredNoOverlap means fewer than
// minResidualPairObservations rows carried BOTH residuals. Listwise
// deletion is per model, so two fields with disjoint null patterns
// can each have thousands of residuals and share almost none.
const ResidualUnmeasuredNoOverlap = isynth.ResidualUnmeasuredNoOverlap

// ResidualUnmeasuredNoVariance means the rows DID overlap but at
// least one side's residual was constant across them, so the
// correlation is 0/0. A perfectly fitted model on the overlap is the
// ordinary cause.
const ResidualUnmeasuredNoVariance = isynth.ResidualUnmeasuredNoVariance

// CategoricalNumericCategoryFidelity is one observed category's
// conditional mean/std delta — the categorical-numeric analogue of
// PairwiseFidelity, one entry per category rather than one entry per
// pair, since a categorical-numeric pair reconstructs a whole
// distribution per category rather than a single scalar.
type CategoricalNumericCategoryFidelity = isynth.CategoricalNumericCategoryFidelity

// CategoricalNumericCategorySpec is the numeric field's conditional
// mean/std for one observed category value (or the "other" catch-all) of
// the paired categorical field — the Spec-facing counterpart of
// CategoricalNumericCategoryStat.
type CategoricalNumericCategorySpec = isynth.CategoricalNumericCategorySpec

// CategoricalNumericCategoryStat is the numeric field's conditional
// mean/std/observation-count for one observed category value (or the
// otherCategoryLabel catch-all) of the paired categorical field.
type CategoricalNumericCategoryStat = isynth.CategoricalNumericCategoryStat

// CategoricalNumericPairFidelity is one row of
// FidelityReport.CategoricalNumericPairwise: the per-category delta
// between a captured categorical-numeric pair's source conditional
// mean/std (CategoricalNumericPairSpec.Categories — the same figures
// internal/synth/conditional_sample.go's categoricalNumericPairSampler draws
// from) and that category's REALIZED conditional mean/std over the
// _synthetic=true partition of the output cohort.
//
// # This section and Models are DISJOINT by construction (E5-S2)
//
// A numeric target reached by a linear model has NO entry here, ever.
// The pick-one sampler this section scores does not run for such a
// field — the model owns its value — so an entry would be a delta for a
// mechanism that never executed, which is the v0.32.2 bug class. Two
// independent guards make it impossible rather than merely unlikely:
// SpecFromProfile leaves a modelled target's captured pair off the Spec
// entirely (per TARGET, not per document — an unmodelled numeric in the
// same profile keeps its pair and is still scored here), and
// resolveConflicts pre-claims every modelled field ahead of the
// categorical-numeric stage, which catches a hand-authored spec
// declaring both. So this section covers exactly the numerics that kept
// the pick-one mechanism, `models` covers exactly the ones that did
// not, and the two field sets never intersect. The same rule applies
// verbatim to SetNumericPairFidelity; the three NON-numeric-target
// sections (CategoricalPairFidelity, SetCategoricalPairFidelity,
// SetSetPairFidelity) are untouched by any of it, because a linear
// model targets a numeric and subsumes nothing they describe.
type CategoricalNumericPairFidelity = isynth.CategoricalNumericPairFidelity

// CategoricalNumericPairProfile is one captured categorical-numeric
// pair's reconstruction input: the numeric field B's conditional
// mean/std, broken out per observed category of the categorical field
// A. Categories already reflect A's own per-field top-K cap
// (ProfileOptions.TopK, via FieldProfile.Categorical.Top) — any
// category outside A's own top-K collapses into one otherCategoryLabel
// bucket before its conditional numeric summary is computed, the same
// per-field collapse CategoricalPairProfile applies on each of its own
// axes. Unlike CategoricalPairProfile there is no second, joint-cell
// cap: the number of retained Categories entries is already bounded by
// ProfileOptions.TopK (plus one "other" bucket), since this section
// captures one numeric summary per category rather than a joint table
// over two categorical axes.
type CategoricalNumericPairProfile = isynth.CategoricalNumericPairProfile

// CategoricalNumericPairSpec is one categorical-numeric pair's
// generation-time reconstruction input — the Spec-facing counterpart of
// CategoricalNumericPairProfile, carrying only what a conditional resample
// needs (each category's mean/std, plus the numeric field's own overall
// min/max so the conditional draw clamps exactly as the field's
// unconditional `normal` reconstruction already does; N is capture-time
// provenance and is not needed here).
type CategoricalNumericPairSpec = isynth.CategoricalNumericPairSpec

// CategoricalPairCellSpec is one observed (a_value, b_value) co-occurrence
// count — the Spec-facing counterpart of ContingencyCell.
type CategoricalPairCellSpec = isynth.CategoricalPairCellSpec

// CategoricalPairFidelity is one row of
// FidelityReport.CategoricalPairwise: the delta between a captured
// categorical-categorical pair's source joint-frequency table
// (CategoricalPairSpec.Cells — the same contingency cells
// internal/synth/conditional_sample.go's categoricalPairSampler was built from)
// and that pair's REALIZED joint-frequency table over the
// _synthetic=true partition of the output cohort, expressed as total
// variation distance (see categoricalTVD) — the categorical-pair
// analogue of PairwiseFidelity's numeric-numeric correlation delta.
type CategoricalPairFidelity = isynth.CategoricalPairFidelity

// CategoricalPairProfile is one captured categorical-categorical pair's
// reconstruction input: a bounded contingency table of observed
// (a_value, b_value) co-occurrence counts. Values already reflect each
// field's own per-field top-K cap (ProfileOptions.TopK, via each
// field's own FieldProfile.Categorical.Top) — any category outside a
// field's own top-K collapses to "other" before the joint table is
// built — and the table is further bounded to at most
// ContingencyCellCap cells by co-occurrence count, with everything past
// that cut folded into one ("other","other") catch-all cell. The two
// caps compose: the existing per-field cap is an input bound to this
// story's new joint-cell cap, never replaced by it.
type CategoricalPairProfile = isynth.CategoricalPairProfile

// CategoricalPairSpec is one categorical-categorical pair's generation-time
// reconstruction input — the Spec-facing counterpart of
// CategoricalPairProfile, carrying only what a conditional resample needs
// (the cells; N is capture-time provenance and is not needed here).
type CategoricalPairSpec = isynth.CategoricalPairSpec

// CategoricalProfile holds the top-K observed values and the total
// distinct count.
type CategoricalProfile = isynth.CategoricalProfile

// CategoryHit records a categorical value with its observed weight.
type CategoryHit = isynth.CategoryHit

// ConditionalProfile carries the joint reconstruction structure
// `profile create --conditional` captures beyond independent per-field
// marginals. E2-S1 covers numeric-numeric pairs; E3-S1 adds
// categorical-categorical pairs; E3-S2 adds categorical-numeric pairs
// — all to the same struct rather than a new top-level section, so the
// same nil-check keeps gating all of them. E5-S2 extends this to pairs
// involving a set_* field: SetCategoricalPairs, SetNumericPairs and
// SetSetPairs, one entry PER OPTION (bit position) rather than per
// field — E5-S1's design decision that each dictionary entry is its
// own independent Bernoulli sub-field composes directly with E3's
// pairwise machinery by running it once per option, reusing
// ContingencyCell / ContingencyCellCap / condCatNumAcc verbatim rather
// than inventing a parallel mechanism for set_* fields.
type ConditionalProfile = isynth.ConditionalProfile

// ConstraintSpec wraps an expression evaluated against the in-memory row.
type ConstraintSpec = isynth.ConstraintSpec

// ContingencyCell is one observed (a_value, b_value) combination and
// its co-occurrence count, as captured by --conditional's
// categorical-categorical contingency table. AValue/BValue read
// "other" (otherCategoryLabel) when the underlying value fell outside
// its field's own top-K, or when the cell itself fell outside the
// pair's ContingencyCellCap.
type ContingencyCell = isynth.ContingencyCell

// CorrelationSpec declares a target Pearson correlation between two
// numeric fields.
type CorrelationSpec = isynth.CorrelationSpec

// CorrelationStat is a captured pairwise correlation entry.
type CorrelationStat = isynth.CorrelationStat

// DateProfile holds the (start, end) range of date values plus a weekday
// histogram for Mode-A reconstruction.
type DateProfile = isynth.DateProfile

// DiscreteLevel is one observed integer level and its support.
type DiscreteLevel = isynth.DiscreteLevel

// DiscreteProfile is an integer-quantized numeric column's exact
// per-level histogram, in ascending level order.
//
// Value/Count/Frequency mirrors SetOptionStat deliberately: a reader who
// has learnt one per-option table in this document has learnt them all.
// Count is the load-bearing figure — SpecFromProfile hands the raw counts
// to the `discrete` distribution's weights, so the reconstruction carries
// no float round trip — and Frequency is the human-readable share,
// normalised by the field's NON-NULL count (the same base
// SetProfile.Frequency uses), never by the cohort's row count.
type DiscreteProfile = isynth.DiscreteProfile

// FidelityReport is the JSON document written to
// Options.FidelityReportPath, comparing the freshly generated rows
// against the source cohort's rows inside the one combined output.
// Fields is the per-field marginal section (E1-S2); Pairwise is the
// numeric-numeric correlation-delta section (E2-S3); CategoricalPairwise
// and CategoricalNumericPairwise are the categorical-categorical
// contingency-delta and categorical-numeric conditional-mean/std-delta
// sections (E3-S4); SetFields is the set_* per-option marginal
// frequency-delta section, and SetCategoricalPairwise /
// SetNumericPairwise / SetSetPairwise are the three set_*
// joint-structure delta sections (E5-S4) — all populated only when the
// spec/schema carried at least one entry of the matching kind — absent
// (omitempty), never an empty placeholder array, on every plain
// synthesis and every from-profile run with no captured structure of
// that kind. Warnings mirrors any thin-pair/thin-cell warnings the
// caller supplied (see Options.FidelityWarnings) — typically
// Profile.Warnings from a --conditional capture, covering every pair
// kind through the same shared shape — so a reader sees "how well did
// it match" (the pairwise sections) and "which parts were built on thin
// data" (Warnings) in one document (FR-18).
type FidelityReport = isynth.FidelityReport

// FieldContinuation is one field's run-continuation rate.
type FieldContinuation = isynth.FieldContinuation

// FieldFidelity is one row of FidelityReport.Fields: the marginal
// comparison between the source and generated partitions of a single
// output-cohort field, computed by driving the field's numeric values
// through TEST_KS (SplitBy=_synthetic) or its categorical values
// through TEST_CHISQ (contingency against _synthetic) — never new
// comparison math, always the existing operator run through the
// ordinary Process pipeline (see TestRunner).
//
// Result is nil (Error carries the reason instead) when the operator
// itself could not produce a statistic for this field — e.g. a
// categorical column with fewer than 2 distinct values, or a numeric
// field with fewer than 2 observations in one partition — which never
// fails the surrounding synth call; a fidelity check that cannot run
// for one field must not withhold the report for every other field,
// let alone the generated cohort itself.
type FieldFidelity = isynth.FieldFidelity

// FieldModel is one numeric field's fitted linear predictor.
//
// The additive form is Intercept + Σ Coefficient·column, where each
// column is 1 when the row carries that categorical level or has that
// set option selected and 0 otherwise. A categorical contributes one
// column per level EXCEPT its reference level (see modelColumns), whose
// effect is folded into Intercept — that is what keeps the design
// full-rank alongside an intercept.
type FieldModel = isynth.FieldModel

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
type FieldModelSpec = isynth.FieldModelSpec

// FieldProfile holds per-field summary statistics. Exactly one of
// Numeric, Categorical, Date, or Set is populated based on the field's
// type.
type FieldProfile = isynth.FieldProfile

// FieldSpec is a single column declaration.
type FieldSpec = isynth.FieldSpec

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
type ModelFidelity = isynth.ModelFidelity

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
type ModelPredictor = isynth.ModelPredictor

// ModelPredictorFidelity is one compiled model term's recovery: the
// captured coefficient generation used, on the latent scale, beside the
// coefficient a refit of the same term on the generated rows recovers.
type ModelPredictorFidelity = isynth.ModelPredictorFidelity

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
type ModelPredictorSpec = isynth.ModelPredictorSpec

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
type ModelReference = isynth.ModelReference

// ModelResidualCorrelationFidelity is FidelityReport's
// `model_residual_correlations` section: how well the generated
// partition reproduces the residual correlation structure generation
// was asked to impose.
//
// The aggregate counters describe EVERY compared pair; Pairs carries a
// bounded worst-first sample of them and Omitted counts the rest. See
// this file's header for why the listing is bounded and why nothing is
// lost by it.
type ModelResidualCorrelationFidelity = isynth.ModelResidualCorrelationFidelity

// ModelResidualPairFidelity is one applied residual correlation's
// recovery.
type ModelResidualPairFidelity = isynth.ModelResidualPairFidelity

// ModelResidualUnmeasuredPair records one applied residual correlation
// the synthetic partition could not be measured for, and why. It
// deliberately carries no recovered-rho slot at all — see
// ResidualUnmeasuredPair, whose reasoning this mirrors exactly.
type ModelResidualUnmeasuredPair = isynth.ModelResidualUnmeasuredPair

// NumericPairProfile is one captured numeric-numeric pair's
// reconstruction input.
type NumericPairProfile = isynth.NumericPairProfile

// NumericProfile is the detail block for numeric fields.
type NumericProfile = isynth.NumericProfile

// Options modulates how the spec is realized.
type Options = isynth.Options

// PairwiseFidelity is one row of FidelityReport.Pairwise: the delta
// between a captured numeric-numeric pair's source correlation (the
// same rho internal/synth/copula.go's conditional-Gaussian reconstruction was
// asked to hit — CorrelationSpec.Correlation, itself sourced from
// Profile.Conditional.NumericPairs when --conditional captured it, or
// Profile.Pairwise otherwise, via SpecFromProfile) and that pair's
// REALIZED Pearson correlation over the _synthetic=true partition of
// the output cohort. The realized figure is computed with the same
// `pearson` helper Profile capture itself uses (see
// computeConditionalNumericPairs) — never a second, divergent
// implementation, and the exact computation
// TestSynth_CorrelationReconstructionWithinTolerance and its siblings
// already assert against.
type PairwiseFidelity = isynth.PairwiseFidelity

// Profile is a serialization-friendly statistical summary of a cohort.
// It contains everything needed to drive synth from-profile without
// retaining any individual rows from the source data.
type Profile = isynth.Profile

// ProfileOptions modulates how Profile summarizes a cohort.
type ProfileOptions = isynth.ProfileOptions

// ResidualCorrelation is one MEASURED residual pair.
//
// Every entry in ResidualCorrelationProfile.Pairs is a real measurement
// over N co-present rows, including one whose Rho is 0 — that is the
// measured-zero case this section exists to keep distinguishable from a
// gap, and it is meaningful information (these two residuals were
// watched together and did not move together).
type ResidualCorrelation = isynth.ResidualCorrelation

// ResidualCorrelationProfile is the captured correlation structure
// among model residuals: the FULL submatrix over participants, split
// into what was measured and what was not.
//
// The representation is two lists over a declared participant set
// rather than an N x N array of numbers, and that is the load-bearing
// choice. A dense array has one slot per pair and every slot must hold
// a number, so "unmeasured" has nowhere to live except as a sentinel —
// and the sentinel that reads most naturally is 0, which is also a
// perfectly ordinary measurement. Splitting the pairs by whether they
// were measured makes the two states structurally different rather than
// conventionally different, and a JSON round trip preserves a
// structural difference for free.
//
// Fields + Pairs + Unmeasured is exhaustive and non-overlapping: every
// unordered pair among Fields appears in exactly one of the two lists.
type ResidualCorrelationProfile = isynth.ResidualCorrelationProfile

// ResidualUnmeasuredPair records one pair among participants that could
// NOT be measured, and why.
//
// It is written down rather than left as an absence from Pairs because
// the whole subject of this section is the difference between "we
// looked and there is no relationship" and "we could not look". A
// reader holding Fields and Pairs can already derive the complement,
// but deriving a fact is not the same as being told it, and the reason
// is not derivable at all.
type ResidualUnmeasuredPair = isynth.ResidualUnmeasuredPair

// Result is what the writer reports after a successful Synth call.
type Result = isynth.Result

// RuleEvidence is the one slot on RuleSpec that DOES NOTHING.
//
// Read that first, because every other member of RuleSpec executes and
// this is exactly the place a later reader will assume execution. The
// rule pass (internal/synth/rules_apply.go) never looks at it, compileRules never
// compiles it, validateRules never reads a field of it, and nothing in
// it can name a field, an expression or a value that changes a generated
// byte. It is carried so that a PROPOSED rule and the measurement that
// proposed it cannot be separated.
//
// # Why it lives on RuleSpec rather than beside it
//
// `profile create --suggest-rules` writes a file that
// `synth from-profile --rules` must consume WITHOUT MODIFICATION, and
// E1-S2 fixed that format as a BARE JSON ARRAY of rule objects. That
// rules out a sibling metadata block: wrapping the array in
// {"rules": [...], "evidence": [...]} is precisely the shape E2-S1's
// loader refuses, deliberately, because silently reading zero rules out
// of a wrapper object is the failure the eager-validation rule exists to
// remove. JSON has no comments, so the third option does not exist.
//
// A sidecar file was the remaining alternative and was rejected on the
// editing workflow: the file exists to be EDITED — an analyst deletes
// the candidates they do not believe and corrects the `when` of the ones
// they do — and evidence in a second document drifts from its rule on
// the first deletion, silently, in the direction of describing a rule
// that is no longer there. Carried on the rule, a deleted candidate
// takes its evidence with it.
//
// # Why the key is spelled `_evidence`
//
// The leading underscore is this repo's existing marker for a block that
// is documentation rather than payload — `examples/<dir>/*.json` carries
// `_meta` for the same reason, and the template renderer refuses a body
// that still has one attached. A reader scanning the file sees four
// executable keys spelled like verbs and one spelled like a note.
//
// # What it is NOT allowed to become
//
// A slot generation reads. If a future detector needs to influence
// generation it declares an ordinary executable slot and validates it;
// widening this one would make a document's meaning depend on a block
// authors are told they may delete freely.
//
// Hand-authoring it is harmless and unvalidated: an author who writes
// nonsense here gets a rule that behaves exactly as it would with the
// key absent. A rule carrying ONLY this key still fails
// PULSE_SYNTH_RULE_EMPTY, because it declares no action — see
// validateRule, which counts the four action slots and not this one.
type RuleEvidence = isynth.RuleEvidence

// RuleEvidenceBlockMember is one field of a co-missing block.
type RuleEvidenceBlockMember = isynth.RuleEvidenceBlockMember

// RuleEvidenceDependency is one target of an exact-dependency
// candidate, with the measured lookup that proposed it.
//
// The rendered EXPRESSION is deliberately absent: it is already the
// rule's own `set_expr` entry for this field, and a second copy would go
// stale the first time an analyst edits the one that executes — in the
// direction of describing a rule that is no longer there, which is the
// exact failure the whole `_evidence` design (see RuleEvidence) exists
// to avoid.
type RuleEvidenceDependency = isynth.RuleEvidenceDependency

// RuleEvidenceLevel is one level of the gate field.
type RuleEvidenceLevel = isynth.RuleEvidenceLevel

// RuleEvidenceMapValue is one arm of a measured lookup: a level of the
// source, the value the target took at it, and the rows behind it.
type RuleEvidenceMapValue = isynth.RuleEvidenceMapValue

// RuleEvidenceTarget is one field the candidate proposes to null, with
// the conditional rates that proposed it.
type RuleEvidenceTarget = isynth.RuleEvidenceTarget

// RuleSpec is one STRUCTURAL rule: a statement about which fields a row
// may carry a value for, and what that value is, imposed on top of
// whatever the distributions, conditional pairs, correlations and models
// produced. It is the mechanism for the class of survey fact no
// statistical summary can express — "the perception block is not asked
// of a respondent who has never heard of the brand", "promoter is nps >=
// 9" — which generation otherwise reproduces as soft variation with a
// plausible-looking marginal and no gate at all.
//
// Five slots, exactly one of which (`when`) is a predicate and four of
// which are actions, plus one MODIFIER (`owns_nulls`) that changes what
// a `set_null` means for the fields it names rather than adding an
// action of its own:
//
//	{"when": "familiarity == 1",
//	 "set_null": ["perception_1", "perception_2"],
//	 "owns_nulls": true,
//	 "set": {"segment": "unaware"},
//	 "set_expr": {"promoter": "nps >= 9"},
//	 "null_together": ["nps", "nps_reason"]}
//
// `set` and `set_expr` are SIBLING KEYS rather than one map carrying a
// {"$expr": ...} marker, and that is a settled decision rather than an
// accident of implementation: one map leaves {"set": {"region": "west"}}
// undecidable between a categorical literal and a bare identifier, while
// two keys make the question impossible to ask. Do not merge them.
//
// # Application semantics
//
// Rules apply in DECLARATION ORDER, sequentially, LAST WRITE WINS. An
// expression reads whatever the row holds at the moment its rule runs,
// which a later rule may still change — so a `set_expr` reading a field
// an EARLIER rule wrote sees the new value, and one reading a field a
// LATER rule will write sees the old one. That is correct and
// surprising, and it is why the order is the author's to see: the rules
// are deliberately NOT topologically sorted, because a sort would make
// the applied order implicit and declaration order is the one ordering
// an author can read off the document.
//
// The pass runs ONCE per row, LAST — after the model stage, the final
// thing to touch a row before it is encoded. The semantics that buys are
// `if gate then null else inferred`: a gated field is still drawn
// normally, through its own sampler and whatever model or conditional
// pair owns it, and the rule then masks or replaces it on the rows the
// `when` selects, leaving the inferred value everywhere else.
//
// The pass consumes no RNG, so a spec declaring no rules generates
// byte-identical output to the same spec with the slot absent.
//
// Validation is EAGER — every fault below is refused at spec parse, not
// at row 400,000 (see validateRules).
//
// The standalone rules-file format is this array itself, so an inline
// `rules` declaration and a `--rules` file are the same JSON.
type RuleSpec = isynth.RuleSpec

// RunContinuationProfile is the measured run-continuation of a cohort
// (`pulse profile create --run-continuation`,
// ProfileOptions.RunContinuation): per field, the fraction of adjacent
// row pairs whose on-wire bytes for that field — and, for a nullable
// field, its null bit — are identical.
//
// It is the exact per-field hit rate of the run-skip decode
// (internal/encoding/reader_runskip.go), which rewrites only the fields whose
// bytes or null bit changed since the previous row. Because the
// optimisation is a property of the DATA rather than the format, a
// cohort re-imported without its upstream ORDER BY loses it with nothing
// on the wire saying so; this section is how it becomes visible.
//
// Pairs never span a shard boundary. Each shard of an archive is a
// separately written payload and the decode restarts its comparison
// baseline at each one, so a pair across two shards measures nothing the
// optimisation can use. Shards counts the payloads scanned (1 for a
// single-file cohort); Pairs is the sum over shards of (rows − 1).
//
// Additive and omitempty: absent from every document captured without
// the flag. SpecFromProfile never reads it — it describes the row ORDER
// of the source, which generation does not reproduce.
type RunContinuationProfile = isynth.RunContinuationProfile

// SetCategoricalPairFidelity is one row of
// FidelityReport.SetCategoricalPairwise: the delta between a captured
// set-option x categorical pair's source contingency table
// (SetCategoricalPairSpec.Cells) and that pair's REALIZED contingency
// table over the _synthetic=true partition of the output cohort —
// the set-option analogue of CategoricalPairFidelity, with the A axis
// fixed to the option's {"selected","not_selected"} Bernoulli domain
// instead of an arbitrary categorical value set. Delta reuses
// categoricalTVD verbatim (SetCategoricalPairSpec.Cells is the same
// []CategoricalPairCellSpec shape CategoricalPairSpec.Cells is).
type SetCategoricalPairFidelity = isynth.SetCategoricalPairFidelity

// SetCategoricalPairProfile is one captured set-option x categorical
// pair's reconstruction input (E5-S2): the bounded contingency table
// between one dictionary option (bit position) of a set_* field —
// treated as its own two-valued "selected"/"not_selected" Bernoulli
// sub-field, per E5-S1's per-option representation — and a categorical
// field's observed values. Reuses the exact same ContingencyCell /
// ContingencyCellCap / otherCategoryLabel machinery
// CategoricalPairProfile already established for two categorical
// fields; the only difference is the A axis is always the fixed
// two-value domain {"selected","not_selected"} rather than an
// arbitrary categorical value set, so it needs no per-field top-K
// collapse of its own — only B's (the categorical field's) values are
// collapsed, exactly as CategoricalPairProfile already does for either
// of its two axes.
type SetCategoricalPairProfile = isynth.SetCategoricalPairProfile

// SetCategoricalPairSpec is one set-option x categorical pair's
// generation-time reconstruction input — the Spec-facing counterpart of
// SetCategoricalPairProfile. Cells reuse CategoricalPairCellSpec
// verbatim: AValue is always "selected"/"not_selected" (the set
// option's own fixed two-value domain), BValue is the categorical
// field's observed value.
type SetCategoricalPairSpec = isynth.SetCategoricalPairSpec

// SetFieldFidelity is one row of FidelityReport.SetFields: the
// per-option marginal frequency comparison for a set_* (multi-select
// bitmask) field, extending E1-S2's per-field marginal section to
// set_* fields (E5-S4). It deliberately does NOT reuse TEST_KS/
// TEST_CHISQ the way FieldFidelity does — driving each option through
// the ordinary Process pipeline would need a presentation schema
// widening every option into its own pseudo-categorical field, purely
// to satisfy TEST_CHISQ's Rows/Cols contract (the same trick
// SyntheticAsCategoricalSchema plays for _synthetic itself, multiplied
// by option count). A direct frequency comparison is simpler, equally
// rigorous for a Bernoulli marginal (E5-S1's own representation), and
// composes with the same per-option addressing SetProfile.Options and
// ConditionalProfile's three set-pair kinds already use — see
// computeSetFieldFidelity.
type SetFieldFidelity = isynth.SetFieldFidelity

// SetNumericPairFidelity is one row of
// FidelityReport.SetNumericPairwise: the per-bucket delta between a
// captured set-option x numeric pair's source conditional mean/std
// (SetNumericPairSpec.Categories, keyed "selected"/"not_selected") and
// that bucket's REALIZED conditional mean/std over the _synthetic=true
// partition — the set-option analogue of CategoricalNumericPairFidelity.
//
// It is a NUMERIC-TARGET section, so the model-retirement rule on
// CategoricalNumericPairFidelity applies to it verbatim: a numeric
// reached by a linear model has no entry here.
type SetNumericPairFidelity = isynth.SetNumericPairFidelity

// SetNumericPairProfile is one captured set-option x numeric pair's
// reconstruction input (E5-S2): the numeric field's conditional
// mean/std broken out by whether the set field's option (bit position)
// was selected — the set-field analogue of
// CategoricalNumericPairProfile, with the categorical axis fixed to the
// two-value {"selected","not_selected"} domain instead of an arbitrary
// category set.
type SetNumericPairProfile = isynth.SetNumericPairProfile

// SetNumericPairSpec is one set-option x numeric pair's generation-time
// reconstruction input — the Spec-facing counterpart of
// SetNumericPairProfile. Categories carries at most two entries keyed
// "selected" / "not_selected", reusing CategoricalNumericCategorySpec
// verbatim. Min/Max/HasClamp mirror CategoricalNumericPairSpec: the
// numeric field's own observed range, applied identically regardless of
// which bucket the conditional draw lands in.
type SetNumericPairSpec = isynth.SetNumericPairSpec

// SetOptionFidelity is one dictionary entry (bit position)'s realized
// selection-frequency comparison between the source (_synthetic=false)
// and synthetic (_synthetic=true) partitions of the output cohort —
// the set_* analogue of FieldFidelity, one entry per option rather than
// one entry per field, mirroring CategoricalNumericCategoryFidelity's
// per-category breakout for the same reason: a set_* field reconstructs
// one independent Bernoulli marginal per option rather than a single
// scalar.
type SetOptionFidelity = isynth.SetOptionFidelity

// SetOptionStat is one dictionary entry (bit position)'s observed
// marginal selection frequency within a set_* field's non-null rows.
type SetOptionStat = isynth.SetOptionStat

// SetProfile holds per-option (marginal) selection-frequency statistics
// for a set_* (multi-select bitmask) field. E5-S1's design decision:
// each dictionary entry (bit position) is profiled as an independent
// Bernoulli-selected sub-field — Frequency is P(bit i set | field
// non-null) — matching "select all that apply" semantics, where each
// option genuinely is its own independent yes/no question a respondent
// answers. This is deliberately NOT "whole mask as one categorical"
// (which would treat every distinct combination as an arbitrary,
// combinatorially exploding category) nor "N fully independent binaries
// with no shared identity" (which would drop the fact that every option
// rides one bounded, shared dictionary — the same per-option addressing
// E5-S2's joint/conditional capture reuses). Full rationale:
// skills/synthetic-data.md ("Set (multi-select) field profiling").
//
// This section is marginal-only: joint/conditional structure between
// options, or between a set_* field and another field, is E5-S2's job.
type SetProfile = isynth.SetProfile

// SetSetPairFidelity is one row of FidelityReport.SetSetPairwise: the
// delta between a captured option x option pair's source 2x2
// contingency table (SetSetPairSpec.Cells) between two DIFFERENT set_*
// fields, and that pair's REALIZED 2x2 contingency table over the
// _synthetic=true partition — the cross-field-option analogue of
// CategoricalPairFidelity, with both axes fixed to their own option's
// {"selected","not_selected"} domain.
type SetSetPairFidelity = isynth.SetSetPairFidelity

// SetSetPairProfile is one captured option x option pair's
// reconstruction input between two DIFFERENT set_* fields (E5-S2): a
// bounded 2x2-cell contingency table of
// {"selected","not_selected"} x {"selected","not_selected"}
// co-occurrence counts for one option of each field. Never captured
// between two options of the SAME set field — E5-S2's scope is
// cross-field option association only, per the PRD's "do two different
// set_* fields' option selections correlate with each other?" framing.
type SetSetPairProfile = isynth.SetSetPairProfile

// SetSetPairSpec is one option x option pair's generation-time
// reconstruction input between two DIFFERENT set_* fields — the
// Spec-facing counterpart of SetSetPairProfile. Cells reuse
// CategoricalPairCellSpec verbatim: AValue is SetA's OptionA state
// ("selected"/"not_selected"), BValue is SetB's OptionB state.
type SetSetPairSpec = isynth.SetSetPairSpec

// ShapeProfile is a fitted 2-component Gaussian mixture: parallel
// means/stds/weights, directly usable as the "mixture" distribution's
// (synth.DistMixture, E4-S1) own "means"/"stds"/"weights" params with
// no translation needed at generation time. See fitNumericShape in
// shape.go for how and when this gets populated.
type ShapeProfile = isynth.ShapeProfile

// Spec is the parsed top-level synthesis request. It is the in-memory
// shape of from-schema JSON. A from-profile call builds a Spec internally
// from the Profile and shares the rest of the writer pipeline.
type Spec = isynth.Spec

// Synth materializes a synthetic .pulse file from a Spec into output on
// fs. When opts.SourceCohort is non-empty the source cohort's real rows
// are copied into output tagged _synthetic=false and spec.RowCount new
// rows are appended tagged _synthetic=true; the source is never opened
// for write. Output is byte-identical for the same (spec, seed) pair.
func Synth(fs afero.Fs, spec *Spec, output string, opts Options) (*Result, error) {
	return isynth.Synth(fs, spec, output, opts)
}

// SynthBytes returns the bytes Synth would have written, without
// touching a filesystem.
func SynthBytes(spec *Spec, opts Options) ([]byte, *Result, error) {
	return isynth.SynthBytes(spec, opts)
}

// ProfileFile reads a .pulse cohort (single file or shard archive) from
// fs and summarizes it as a Profile.
func ProfileFile(fs afero.Fs, path string, opts ProfileOptions) (*Profile, error) {
	return isynth.ProfileFile(fs, path, opts)
}

// ProfileBytes summarizes a .pulse cohort given its raw bytes.
func ProfileBytes(data []byte, opts ProfileOptions) (*Profile, error) {
	return isynth.ProfileBytes(data, opts)
}

// SpecFromProfile composes a generation Spec of rowCount rows from a
// captured Profile. The second return value carries any
// conditional-relationship conflict warnings resolved against the
// composed Spec; callers that do not need them may discard it.
func SpecFromProfile(p *Profile, rowCount int) (*Spec, []string) {
	return isynth.SpecFromProfile(p, rowCount)
}

// ParseSpec parses Spec JSON. It returns SERVICE_VALIDATION if the shape
// is wrong.
func ParseSpec(raw []byte) (*Spec, error) {
	return isynth.ParseSpec(raw)
}

// WriteSpec writes spec to path on fs as indented JSON. Feeding the file
// back through ParseSpec and Synth at the same seed reproduces the same
// cohort byte for byte.
func WriteSpec(fs afero.Fs, spec *Spec, path string) error {
	return isynth.WriteSpec(fs, spec, path)
}
