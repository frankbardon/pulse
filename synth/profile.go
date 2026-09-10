package synth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// conditionalJointCap bounds the number of row-aligned snapshots
// ProfileOptions.IncludeConditional retains for pairwise reconstruction
// — both jointRows (numeric-numeric) and jointCatRows
// (categorical-categorical). Mirrors the existing 10000-sample cap used
// elsewhere in this file (percentiles, the plain Pairwise correlation
// stats) so profile size stays bounded regardless of source cohort
// size. The categorical-categorical arm (jointCatRows) fills this cap
// via genuine Algorithm R reservoir sampling (E7-S2), so the captured
// contingency table is unbiased regardless of source row ordering; the
// numeric-numeric arm (jointRows) is unchanged first-N truncation and
// was not in this story's scope.
const conditionalJointCap = 10000

// MinPairObservations is the minimum number of co-occurring, non-null
// observations a captured pair needs before its reconstruction
// parameters are trusted. This threshold and the warning mechanism
// below are deliberately generic across pair *kind* — numeric-numeric
// is the only kind this story populates, but categorical and set_*
// pairs (later epics) reuse the same threshold and the same
// thinPairWarning helper rather than inventing their own. Below the
// threshold the profile still ships the pair — a thin pair is never a
// refusal — but records a warning on Profile.Warnings so a caller
// downstream of profile capture knows the figure may be unstable.
const MinPairObservations = 30

// otherCategoryLabel marks a category value collapsed into a catch-all
// bucket by categorical-categorical contingency capture. The same
// sentinel serves two independent collapse points —
// ProfileOptions.TopK's existing per-field cap (a value outside a
// field's own top-K becomes "other" before the joint table is built)
// and ContingencyCellCap's joint-cell cap (a cell outside the top
// ContingencyCellCap-1 co-occurrence counts is folded into one
// ("other","other") catch-all cell) — so a caller reading the document
// sees one "other" concept, not two differently-spelled ones. See
// collapseCells.
const otherCategoryLabel = "other"

// ContingencyCellCap bounds the number of distinct (a_value, b_value)
// cells CategoricalPairProfile.Cells retains per categorical-categorical
// pair. A pair of two 50-plus category fields can still produce
// thousands of joint combinations even after each field's own
// ProfileOptions.TopK collapse (e.g. two fields each capped at the
// default TopK=32 allow up to 33*33=1089 joint cells) — this second,
// joint-cardinality cap keeps the profile document bounded regardless
// of either field's own cardinality. 128 was chosen as generous enough
// to preserve real structure for the common case while still bounding
// a pathological high-cardinality pair; every cell beyond the cap
// collapses into one ("other","other") catch-all cell rather than
// being dropped silently.
const ContingencyCellCap = 128

// thinSupportWarning is the one spelling of "this figure rests on too
// few observations to trust" in the whole package, and every capture
// stage that has such a figure routes through it.
//
// subject names WHAT is thin, a and b the two things it relates, and
// consequence what the reader should expect as a result. The first two
// were already generic across pair KIND; consequence is the third axis,
// added when model-coefficient shrinkage became the first caller whose
// outcome is not "a reconstructed correlation may be unstable" but "the
// coefficient was pulled toward its baseline". Splitting it out rather
// than adding a second warning builder is what keeps a reader scanning
// Profile.Warnings able to recognise every thin-support line by its
// shape, whatever produced it — and every existing caller's text is
// byte-identical to what it was, because thinPairWarning below still
// supplies the same two literals it always inlined.
//
// Returns "" when n meets the threshold, so a caller can append
// unconditionally.
func thinSupportWarning(subject, a, b string, n, threshold int, consequence string) string {
	if n >= threshold {
		return ""
	}
	return fmt.Sprintf(
		"thin %s %s x %s: only %d supporting observation(s) (below %d) — %s",
		subject, a, b, n, threshold, consequence)
}

// thinPairWarning returns a warning string when n falls below
// threshold, or "" when the pair has enough support. kind names the
// pair type in the message (e.g. "numeric" today; "categorical" / "set"
// once later stories call this same helper) so every warning this
// mechanism produces reads the same shape regardless of which capture
// path produced it.
func thinPairWarning(kind, a, b string, n, threshold int) string {
	return thinSupportWarning(kind+" pair", a, b, n, threshold,
		"reconstructed correlation may be unstable")
}

// ProfileOptions modulates how Profile summarizes a cohort.
type ProfileOptions struct {
	// TopK is the number of top categorical values to capture per
	// categorical field. Defaults to 32 when zero.
	TopK int
	// IncludeStats turns on percentile / stdev / kurtosis collection.
	// When false, only mean / min / max / null-rate are recorded for
	// numeric fields. Defaults to true.
	IncludeStats bool
	// IncludeCorrelations enables pairwise Pearson correlation capture
	// between numeric fields. Off by default to keep profile size bounded.
	IncludeCorrelations bool
	// CorrelationTopK caps the number of strongest |rho| pairs retained.
	// Defaults to 16 when IncludeCorrelations or IncludeConditional is
	// true.
	CorrelationTopK int
	// IncludeConditional enables capture of the joint reconstruction
	// structure (`profile create --conditional`): for numeric-numeric
	// field pairs, a row-aligned correlation + observation count
	// (Profile.Conditional.NumericPairs) that SpecFromProfile uses to
	// drive the exact conditional-Gaussian reconstruction in
	// synth/copula.go, in place of the plain (and, for cohorts with
	// nulls, only approximately aligned) Pairwise correlation stats.
	// Off by default; the profile document's `conditional` key is
	// entirely absent (omitempty) when this is false, and every
	// pre-existing profile document — which has no such key — remains
	// valid SpecFromProfile input either way.
	IncludeConditional bool
	// SampleLimit caps the number of records ingested for the profile.
	// Zero = unlimited.
	SampleLimit int
	// FitShape enables shape-fitting for numeric fields
	// (`profile create --fit-shape`, E4-S2): instead of always
	// summarizing a numeric field as a single normal(mean, std),
	// attempt a 2-component Gaussian mixture fit and keep it — as
	// FieldProfile.Numeric.Shape — only when it is a genuine
	// improvement over the plain normal by BIC (see fitNumericShape in
	// shape.go for the full selection rule and its documented
	// limitations). Off by default; a field whose fit is not kept (not
	// enough data, or the source is already close to normal) carries no
	// Shape, and SpecFromProfile reconstructs it exactly as it always
	// has. Forces sample retention (as if IncludeStats were also set)
	// for numeric fields so there is something to fit against, even
	// when IncludeStats itself is false.
	FitShape bool
	// FitModels enables per-numeric linear model capture
	// (`profile create --fit-models`, E1-S2): for each numeric field,
	// fit one linear predictor of that field on the cohort's
	// categorical LEVELS and set OPTIONS, driven off this same single
	// scan, and retain the fitted coefficients, the residual scale and
	// a bounded row-aligned residual vector.
	//
	// Off by default. When on, the fits are written to the document as
	// the additive `models` section (Profile.Models) and nothing else in
	// the document moves: every other section is byte-identical to the
	// same capture with the flag off, and a capture WITHOUT the flag
	// emits no `models` key at all. It draws from its own RNG stream, so
	// --conditional's captured output does not depend on whether this
	// flag was also passed.
	//
	// It does change what SpecFromProfile builds: a document carrying
	// `models` reconstructs its numeric fields from the additive linear
	// predictors instead of from --conditional's one-pair-at-a-time
	// numeric-target arms, which are then not populated at all.
	FitModels bool
	// FitResidualCorrelations enables capture of the correlation
	// structure among the fitted model RESIDUALS
	// (`profile create --residual-correlations`, E3-S1), written as the
	// additive `residual_correlations` section
	// (Profile.ResidualCorrelations).
	//
	// It requires FitModels — residuals are what the models produce, so
	// with no models there is nothing to correlate, and asking for this
	// alone warns rather than silently emitting nothing.
	//
	// It is deliberately its own flag rather than an implication of
	// FitModels: the section is QUADRATIC in modelled fields (105
	// targets is 5,460 pairs) and no existing caller asked for it, so
	// switching it on with --fit-models would grow every such document
	// without notice. It is likewise deliberately NOT wired to
	// IncludeCorrelations / CorrelationTopK, which keep their exact
	// meaning — the strongest top-K pairs of RAW values. This section is
	// a different measurement (residuals, not raw values) with a
	// different shape (a full submatrix, not a ranked list), and sharing
	// either flag would silently change what those two already mean.
	FitResidualCorrelations bool
	// Seed drives the reservoir-sampling RNG used by
	// IncludeConditional's categorical-categorical contingency capture
	// (E7-S2) once the source cohort exceeds conditionalJointCap rows.
	// Zero (the default) is itself a valid, deterministic seed — same
	// as synth.Options.Seed's convention — so two profile runs against
	// the same source with the same Seed (including the unset zero
	// value) produce byte-identical captured output. It does not affect
	// any other capture path: every other section in this file is
	// either an exact online accumulation or a first-N reservoir with
	// no random draws.
	Seed int64
}

// Profile is a serialization-friendly statistical summary of a cohort.
// It contains everything needed to drive synth from-profile without
// retaining any individual rows from the source data.
type Profile struct {
	RowCount int               `json:"row_count"`
	Fields   []FieldProfile    `json:"fields"`
	Pairwise []CorrelationStat `json:"pairwise,omitempty"`
	// Conditional carries joint-reconstruction structure captured only
	// when ProfileOptions.IncludeConditional was set. Additive and
	// omitempty: absent on every profile document captured without
	// --conditional, including every pre-existing one, and
	// SpecFromProfile treats a nil Conditional exactly as it always
	// has (falling back to Pairwise).
	Conditional *ConditionalProfile `json:"conditional,omitempty"`
	// Models carries the per-numeric additive linear predictors captured
	// only when ProfileOptions.FitModels was set (`profile create
	// --fit-models`). Additive and omitempty, exactly like Conditional:
	// the key is entirely absent from every document captured without
	// the flag — including every document written before this section
	// existed — and SpecFromProfile treats an empty Models exactly as it
	// always has, falling back to Conditional's numeric-target pairs.
	//
	// The section is a REPLACEMENT for those pairs, not a supplement:
	// when it is present SpecFromProfile stops populating
	// Spec.CategoricalNumericPairs / Spec.SetNumericPairs entirely,
	// because one additive model already accounts for every predictor at
	// once and the pairs exist only to be applied one overwrite at a
	// time. See SpecFromProfile for the full rule.
	//
	// Not every part of a captured FieldModel is durable: the residual
	// reservoir and the internal design-column names are `json:"-"`, so
	// a Profile read back from JSON carries coefficients but no
	// per-row residuals. See FieldModel for why.
	Models []FieldModel `json:"models,omitempty"`
	// ResidualCorrelations carries the correlation structure among the
	// fitted model residuals, captured only when
	// ProfileOptions.FitResidualCorrelations was set (`profile create
	// --residual-correlations`) alongside --fit-models. Additive and
	// omitempty, exactly like Conditional and Models.
	//
	// It is a SEPARATE measurement from Pairwise and
	// Conditional.NumericPairs, not a refinement of either: those two
	// correlate raw values and this correlates what is left after each
	// field's model has explained what it can. All three can coexist in
	// one document, and none of them replaces another.
	//
	// SpecFromProfile translates the MEASURED pairs onto
	// Spec.ResidualCorrelations, where generation consumes them as the
	// shared source of randomness every modelled field's residual is
	// drawn from (synth/residual_draw.go). The `unmeasured` list is
	// translated into nothing at all: an absent pair is completed as
	// independent by the generator, which counts and names the
	// assumption, whereas writing it out as a zero would present it as
	// a measurement. See ResidualCorrelationProfile.
	ResidualCorrelations *ResidualCorrelationProfile `json:"residual_correlations,omitempty"`
	Warnings             []string                    `json:"warnings,omitempty"`
	Meta                 map[string]any              `json:"meta,omitempty"`
}

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
type ConditionalProfile struct {
	// NumericPairs lists the numeric-numeric pairs captured: the
	// row-aligned Pearson correlation and the count of rows where both
	// fields were simultaneously non-null (N) — the input
	// thinPairWarning checks against MinPairObservations. Capped at
	// ProfileOptions.CorrelationTopK strongest |rho| pairs, same
	// convention as Profile.Pairwise.
	NumericPairs []NumericPairProfile `json:"numeric_pairs,omitempty"`
	// CategoricalPairs lists the categorical-categorical pairs
	// captured: a bounded contingency table of observed
	// (a_value, b_value) co-occurrence counts per pair. See
	// CategoricalPairProfile for the two composed collapse points
	// (per-field top-K, then joint-cell ContingencyCellCap) that keep
	// this section bounded regardless of either field's cardinality.
	CategoricalPairs []CategoricalPairProfile `json:"categorical_pairs,omitempty"`
	// CategoricalNumericPairs lists the categorical-numeric pairs
	// captured: the numeric field's conditional mean/std broken out per
	// observed category of the categorical field. See
	// CategoricalNumericPairProfile for the single collapse point
	// (per-field top-K) that keeps this section bounded — no separate
	// joint-cardinality cap is needed here, unlike CategoricalPairs,
	// because a conditional numeric summary is one row per already-capped
	// category rather than a joint cell per pair of categories.
	CategoricalNumericPairs []CategoricalNumericPairProfile `json:"categorical_numeric_pairs,omitempty"`
	// SetCategoricalPairs lists the set-option x categorical pairs
	// captured (E5-S2): one bounded contingency table per (set field,
	// option, categorical field) combination, the set-option axis fixed
	// to the two-value {"selected","not_selected"} Bernoulli domain. See
	// SetCategoricalPairProfile.
	SetCategoricalPairs []SetCategoricalPairProfile `json:"set_categorical_pairs,omitempty"`
	// SetNumericPairs lists the set-option x numeric pairs captured
	// (E5-S2): one conditional mean/std pair (selected vs. not_selected)
	// per (set field, option, numeric field) combination. See
	// SetNumericPairProfile.
	SetNumericPairs []SetNumericPairProfile `json:"set_numeric_pairs,omitempty"`
	// SetSetPairs lists the option x option pairs captured between two
	// DIFFERENT set_* fields (E5-S2): a 2x2 contingency table per
	// (fieldA option, fieldB option) combination. Never captured between
	// two options of the same field. See SetSetPairProfile.
	SetSetPairs []SetSetPairProfile `json:"set_set_pairs,omitempty"`
}

// NumericPairProfile is one captured numeric-numeric pair's
// reconstruction input.
type NumericPairProfile struct {
	A   string  `json:"a"`
	B   string  `json:"b"`
	Rho float64 `json:"rho"`
	// N is the number of rows in the capture where both A and B were
	// simultaneously non-null — the pair's true co-occurrence count,
	// unlike Profile.Pairwise's Rho (computed from two independently
	// capped reservoirs that can drift out of alignment once either
	// field has nulls).
	N int `json:"n"`
}

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
type CategoricalPairProfile struct {
	A     string            `json:"a"`
	B     string            `json:"b"`
	Cells []ContingencyCell `json:"cells"`
	// N is the number of rows in the capture where both A and B were
	// simultaneously non-null — the pair's true co-occurrence count,
	// summed across every cell (the cells kept plus whatever is folded
	// into the "other"/"other" catch-all).
	N int `json:"n"`
}

// ContingencyCell is one observed (a_value, b_value) combination and
// its co-occurrence count, as captured by --conditional's
// categorical-categorical contingency table. AValue/BValue read
// "other" (otherCategoryLabel) when the underlying value fell outside
// its field's own top-K, or when the cell itself fell outside the
// pair's ContingencyCellCap.
type ContingencyCell struct {
	AValue string `json:"a_value"`
	BValue string `json:"b_value"`
	Count  int    `json:"count"`
}

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
type CategoricalNumericPairProfile struct {
	A          string                           `json:"a"` // categorical field name
	B          string                           `json:"b"` // numeric field name
	Categories []CategoricalNumericCategoryStat `json:"categories"`
	// N is the number of rows in the capture where both A and B were
	// simultaneously non-null, summed across every category (including
	// "other") — the pair's true co-occurrence count, mirroring
	// CategoricalPairProfile.N and NumericPairProfile.N.
	N int `json:"n"`
}

// CategoricalNumericCategoryStat is the numeric field's conditional
// mean/std/observation-count for one observed category value (or the
// otherCategoryLabel catch-all) of the paired categorical field.
type CategoricalNumericCategoryStat struct {
	Category string  `json:"category"`
	Mean     float64 `json:"mean"`
	Std      float64 `json:"std"`
	// N is the number of non-null B observations conditioned on this
	// category — the input thinPairWarning checks against
	// MinPairObservations, exactly as NumericPairProfile.N and each
	// ContingencyCell.Count already do for the other two pair kinds.
	N int `json:"n"`
}

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
type SetCategoricalPairProfile struct {
	Set         string            `json:"set"`
	Option      string            `json:"option"`
	Categorical string            `json:"categorical"`
	Cells       []ContingencyCell `json:"cells"`
	// N is the number of rows where both the set field and the
	// categorical field were simultaneously non-null — this option's
	// true co-occurrence count, mirroring CategoricalPairProfile.N.
	N int `json:"n"`
}

// SetNumericPairProfile is one captured set-option x numeric pair's
// reconstruction input (E5-S2): the numeric field's conditional
// mean/std broken out by whether the set field's option (bit position)
// was selected — the set-field analogue of
// CategoricalNumericPairProfile, with the categorical axis fixed to the
// two-value {"selected","not_selected"} domain instead of an arbitrary
// category set.
type SetNumericPairProfile struct {
	Set     string `json:"set"`
	Option  string `json:"option"`
	Numeric string `json:"numeric"`
	// Categories carries at most two entries, keyed "selected" /
	// "not_selected" — reuses CategoricalNumericCategoryStat verbatim
	// rather than a bespoke two-field struct, so a caller already
	// handling CategoricalNumericPairProfile.Categories needs no second
	// shape to learn.
	Categories []CategoricalNumericCategoryStat `json:"categories"`
	// N mirrors CategoricalNumericPairProfile.N: the total non-null
	// co-occurrence count summed across both categories.
	N int `json:"n"`
}

// SetSetPairProfile is one captured option x option pair's
// reconstruction input between two DIFFERENT set_* fields (E5-S2): a
// bounded 2x2-cell contingency table of
// {"selected","not_selected"} x {"selected","not_selected"}
// co-occurrence counts for one option of each field. Never captured
// between two options of the SAME set field — E5-S2's scope is
// cross-field option association only, per the PRD's "do two different
// set_* fields' option selections correlate with each other?" framing.
type SetSetPairProfile struct {
	SetA    string            `json:"set_a"`
	OptionA string            `json:"option_a"`
	SetB    string            `json:"set_b"`
	OptionB string            `json:"option_b"`
	Cells   []ContingencyCell `json:"cells"`
	// N is the number of rows where both fields were simultaneously
	// non-null — this option pair's true co-occurrence count.
	N int `json:"n"`
}

// setPairCellAcc accumulates a two-way {"selected","not_selected"} x
// value contingency table online, exactly as
// computeConditionalCategoricalPairs' raw counts map does for two
// arbitrary categorical fields — the only difference is one axis (the
// set option) is fixed to the two-value Bernoulli domain rather than an
// arbitrary categorical value set. Shared by both set x categorical
// (value = the categorical field's raw category) and set x set
// (value = the other set field's own "selected"/"not_selected")
// capture.
type setPairCellAcc struct {
	counts map[[2]string]int
}

func newSetPairCellAcc() *setPairCellAcc {
	return &setPairCellAcc{counts: make(map[[2]string]int)}
}

// condCatNumAcc accumulates a numeric field's count/sum/sumSq
// conditioned on one observed (raw, uncapped) category value of a
// paired categorical field during the streaming pass. Collapsed to the
// categorical field's own top-K (as computed for its marginal
// CategoricalProfile) plus otherCategoryLabel afterward, in
// computeConditionalCategoricalNumericPairs — the same two-phase
// discipline computeConditionalCategoricalPairs applies to raw
// (a_value, b_value) contingency counts.
type condCatNumAcc struct {
	count      int
	sum, sumSq float64
}

// FieldProfile holds per-field summary statistics. Exactly one of
// Numeric, Categorical, Date, or Set is populated based on the field's
// type.
type FieldProfile struct {
	Name        string              `json:"name"`
	Type        string              `json:"type"`
	Description string              `json:"description,omitempty"`
	NullRate    float64             `json:"null_rate"`
	Numeric     *NumericProfile     `json:"numeric,omitempty"`
	Categorical *CategoricalProfile `json:"categorical,omitempty"`
	Date        *DateProfile        `json:"date,omitempty"`
	// Set carries per-option marginal selection-frequency stats for a
	// set_* (multi-select bitmask) field (E5-S1). Populated instead of
	// Numeric/Categorical/Date — exactly one of the four is non-nil per
	// field, based on the field's schema type.
	Set *SetProfile `json:"set,omitempty"`
	// Precision/Scale carry decimal128 metadata so synth-from-profile can
	// reconstruct the original field shape.
	Precision uint8 `json:"precision,omitempty"`
	Scale     uint8 `json:"scale,omitempty"`
}

// NumericProfile is the detail block for numeric fields.
type NumericProfile struct {
	Min  float64 `json:"min"`
	Max  float64 `json:"max"`
	Mean float64 `json:"mean"`
	Std  float64 `json:"std"`
	// Percentiles holds {p1, p5, p25, p50, p75, p95, p99} when
	// IncludeStats was on; nil otherwise.
	Percentiles []float64 `json:"percentiles,omitempty"`
	// Shape carries a fitted 2-component Gaussian mixture, captured
	// only when ProfileOptions.FitShape was set AND the mixture is a
	// genuine improvement over the plain Mean/Std normal above (BIC
	// selection — see fitNumericShape in shape.go). Additive +
	// omitempty: absent from every profile document captured without
	// --fit-shape (including every pre-existing document), and absent
	// for any numeric field whose observed distribution is already
	// close to normal — SpecFromProfile falls back to the ordinary
	// Mean/Std/Min/Max normal reconstruction whenever this is nil,
	// exactly as it always has.
	Shape *ShapeProfile `json:"shape,omitempty"`
}

// ShapeProfile is a fitted 2-component Gaussian mixture: parallel
// means/stds/weights, directly usable as the "mixture" distribution's
// (synth.DistMixture, E4-S1) own "means"/"stds"/"weights" params with
// no translation needed at generation time. See fitNumericShape in
// shape.go for how and when this gets populated.
type ShapeProfile struct {
	Means   []float64 `json:"means"`
	Stds    []float64 `json:"stds"`
	Weights []float64 `json:"weights"`
}

// CategoricalProfile holds the top-K observed values and the total
// distinct count.
type CategoricalProfile struct {
	Cardinality int           `json:"cardinality"`
	Top         []CategoryHit `json:"top"`
}

// CategoryHit records a categorical value with its observed weight.
type CategoryHit struct {
	Value  string  `json:"value"`
	Weight float64 `json:"weight"`
}

// DateProfile holds the (start, end) range of date values plus a weekday
// histogram for Mode-A reconstruction.
type DateProfile struct {
	Start    string `json:"start"`
	End      string `json:"end"`
	Weekdays [7]int `json:"weekdays"`
}

// CorrelationStat is a captured pairwise correlation entry.
type CorrelationStat struct {
	A   string  `json:"a"`
	B   string  `json:"b"`
	Rho float64 `json:"rho"`
}

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
type SetProfile struct {
	// N is the number of non-null rows Options' frequencies are computed
	// over — the same denominator FieldProfile.NullRate already implies,
	// surfaced here so a caller reading only the Set block can interpret
	// Count as a rate without cross-referencing NullRate.
	N int `json:"n"`
	// Options lists EVERY dictionary entry in bit/dictionary order (bit i
	// first) — NOT sorted by frequency like CategoricalProfile.Top — so
	// two profiles of the same schema are directly comparable
	// position-for-position regardless of which option happens to be
	// more popular.
	Options []SetOptionStat `json:"options"`
}

// SetOptionStat is one dictionary entry (bit position)'s observed
// marginal selection frequency within a set_* field's non-null rows.
type SetOptionStat struct {
	Value string `json:"value"`
	// Count is the number of non-null rows with this option's bit set.
	Count int `json:"count"`
	// Frequency is Count / SetProfile.N — P(bit set), the per-option
	// marginal Bernoulli rate.
	Frequency float64 `json:"frequency"`
}

// internal accumulators used by profileRecords. Lifted to package scope
// so computeCorrelations can take them by name.
type numAcc struct {
	count       int
	nulls       int
	sum, sumSq  float64
	min, max    float64
	samples     []float64
	reservoirOn bool
}
type catAcc struct {
	count int
	nulls int
	hist  map[string]int
}
type dateAcc struct {
	count    int
	nulls    int
	minDays  int64
	maxDays  int64
	weekdays [7]int
}

// setAcc accumulates a set_* field's per-option (bit-position) selection
// counts. selected[i] counts non-null rows with bit i set; its length is
// the field's dictionary size (capped at the type's MaxSetEntries), the
// same bit-index-to-dictionary-entry mapping resolveSetLabels-style
// helpers elsewhere in the codebase use.
type setAcc struct {
	count    int
	nulls    int
	selected []int
}

// ProfileBytes summarizes a .pulse file given its raw bytes.
func ProfileBytes(data []byte, opts ProfileOptions) (*Profile, error) {
	r := bytes.NewReader(data)
	if err := encoding.ReadHeader(r); err != nil {
		return nil, err
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		return nil, err
	}
	return profileRecords(schema, r, opts)
}

// ProfileFile reads a .pulse file from fs and produces a Profile.
func ProfileFile(fs afero.Fs, path string, opts ProfileOptions) (*Profile, error) {
	data, err := afero.ReadFile(fs, path)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE, "reading cohort for profile")
	}
	return ProfileBytes(data, opts)
}

// MarshalJSON serializes the profile.
func (p *Profile) MarshalJSON() ([]byte, error) {
	type alias Profile
	return json.Marshal((*alias)(p))
}

func profileRecords(schema *encoding.Schema, r io.Reader, opts ProfileOptions) (*Profile, error) {
	if opts.TopK == 0 {
		opts.TopK = 32
	}
	if (opts.IncludeCorrelations || opts.IncludeConditional) && opts.CorrelationTopK == 0 {
		opts.CorrelationTopK = 16
	}
	// Default IncludeStats to true if the caller didn't set it explicitly.
	includeStats := opts.IncludeStats || (!opts.IncludeStats && !opts.IncludeCorrelations)

	rr := encoding.NewRecordReader(r, schema)

	numAccs := make(map[string]*numAcc)
	catAccs := make(map[string]*catAcc)
	dateAccs := make(map[string]*dateAcc)
	setAccs := make(map[string]*setAcc)
	warnings := []string{}

	// jointFieldNames, jointRows and jointNulls back
	// ProfileOptions.IncludeConditional only: a row-aligned snapshot of
	// every numeric field's (value, isNull) on each captured row, used
	// to compute a numeric-numeric pair's TRUE co-occurrence Rho/N —
	// unlike na.samples below (per-field, filled independently), this
	// stays aligned across fields even when some rows carry nulls.
	var jointFieldNames []string
	var jointRows [][]float64
	var jointNulls [][]bool

	// jointCatFieldNames, jointCatRows and jointCatNulls mirror
	// jointFieldNames/jointRows/jointNulls above, but for categorical
	// fields: a row-aligned snapshot of every categorical field's
	// (resolved value, isNull) on each captured row, used by
	// computeConditionalCategoricalPairs to build each pair's true
	// co-occurrence contingency table (E3-S1).
	var jointCatFieldNames []string
	var jointCatRows [][]string
	var jointCatNulls [][]bool

	// jointSetFieldNames and setOptionNames back
	// ProfileOptions.IncludeConditional's set_* pair capture (E5-S2).
	// setOptionNames[f.Name] lists field f's dictionary entries in
	// bit/dictionary order (bit i first) — the exact same ordering
	// SetProfile.Options already uses for marginals — so every set x
	// categorical / set x numeric / set x set capture below addresses
	// options the same way a caller reading FieldProfile.Set already
	// does. No row-aligned reservoir snapshot is needed for set fields:
	// unlike numeric/categorical joint capture, the online per-row
	// accumulators below read directly from the current row's `nulls`/
	// `wide` maps (still populated with this row's decode when this
	// block runs), so set-involving pairs are captured EXACTLY (no
	// conditionalJointCap reservoir bound), the same online discipline
	// catNumAccs below already uses for categorical-numeric pairs.
	var jointSetFieldNames []string
	setOptionNames := make(map[string][]string)

	for _, f := range schema.Fields {
		switch {
		case f.Type == encoding.FieldTypeDate:
			dateAccs[f.Name] = &dateAcc{minDays: math.MaxInt64, maxDays: math.MinInt64}
		case f.Type.IsCategorical():
			catAccs[f.Name] = &catAcc{hist: make(map[string]int)}
			if opts.IncludeConditional {
				jointCatFieldNames = append(jointCatFieldNames, f.Name)
			}
		case f.Type.IsSet():
			// A set_* field is NOT categorical (encoding.FieldType.IsSet()
			// and IsCategorical() are separate, non-overlapping checks) and
			// must never fall into the numeric default branch below — its
			// on-wire value is a bitmask, not a scalar, and averaging raw
			// masks as floats produces a nonsense mean/std silently. See
			// setAcc and the marginal build-out below.
			n := 0
			if f.Dictionary != nil {
				n = f.Dictionary.Count()
			}
			if max := int(f.Type.MaxSetEntries()); n > max {
				n = max
			}
			setAccs[f.Name] = &setAcc{selected: make([]int, n)}
			if opts.IncludeConditional && n > 0 {
				names := make([]string, n)
				for i := 0; i < n; i++ {
					names[i] = f.Dictionary.Resolve(uint32(i))
				}
				jointSetFieldNames = append(jointSetFieldNames, f.Name)
				setOptionNames[f.Name] = names
			}
		default:
			numAccs[f.Name] = &numAcc{
				min: math.Inf(1), max: math.Inf(-1),
				// FitShape needs the same reservoir sample the
				// percentile computation below uses, even when
				// IncludeStats itself is off — there is nothing to fit
				// a shape against otherwise.
				reservoirOn: includeStats || opts.FitShape,
			}
			if opts.IncludeConditional {
				jointFieldNames = append(jointFieldNames, f.Name)
			}
		}
	}
	trackCatJoint := len(jointCatFieldNames) >= 2
	// trackCatNumJoint gates the categorical-numeric conditional capture
	// this story (E3-S2) adds: it needs only ONE categorical field and
	// ONE numeric field (unlike trackCatJoint's >=2 categorical fields,
	// which builds a joint table over two categorical axes). needCatRow
	// is the union of both row-snapshot consumers so catRowValues /
	// catRowNulls populate whenever either capture needs them.
	trackCatNumJoint := opts.IncludeConditional && len(jointCatFieldNames) >= 1 && len(jointFieldNames) >= 1
	// trackSetCatJoint / trackSetNumJoint / trackSetSetJoint gate E5-S2's
	// three set_*-involving pair kinds. Each needs only ONE set field
	// (the per-option Bernoulli axis) plus one field of the paired kind
	// — set x set additionally needs a SECOND, DIFFERENT set field
	// (jointSetFieldNames >= 2), mirroring trackCatJoint's >=2
	// requirement for two categorical axes. trackSetCatJoint folds into
	// needCatRow below because it, like trackCatJoint/trackCatNumJoint,
	// consumes catRowValues/catRowNulls for the categorical side of the
	// pair.
	trackSetCatJoint := opts.IncludeConditional && len(jointSetFieldNames) >= 1 && len(jointCatFieldNames) >= 1
	trackSetNumJoint := opts.IncludeConditional && len(jointSetFieldNames) >= 1 && len(jointFieldNames) >= 1
	trackSetSetJoint := opts.IncludeConditional && len(jointSetFieldNames) >= 2
	needCatRow := trackCatJoint || trackCatNumJoint || trackSetCatJoint

	// catNumAccs accumulates condCatNumAcc keyed
	// [categorical field name][numeric field name][raw category value] —
	// only populated when trackCatNumJoint is true. Unlike jointRows'
	// reservoir cap, this is an exact online accumulation over every
	// admitted row (bounded in practice by observed category
	// cardinality, the same unbounded-until-output-time discipline
	// catAcc.hist already uses for the plain per-field top-K).
	var catNumAccs map[string]map[string]map[string]*condCatNumAcc
	if trackCatNumJoint {
		catNumAccs = make(map[string]map[string]map[string]*condCatNumAcc, len(jointCatFieldNames))
		for _, cf := range jointCatFieldNames {
			perNum := make(map[string]map[string]*condCatNumAcc, len(jointFieldNames))
			for _, nf := range jointFieldNames {
				perNum[nf] = make(map[string]*condCatNumAcc)
			}
			catNumAccs[cf] = perNum
		}
	}

	// setCatAccs[setField][option][catField] accumulates the online
	// {"selected","not_selected"} x raw-category-value contingency
	// counts for set x categorical capture (E5-S2). Populated only when
	// trackSetCatJoint is true.
	var setCatAccs map[string]map[string]map[string]*setPairCellAcc
	if trackSetCatJoint {
		setCatAccs = make(map[string]map[string]map[string]*setPairCellAcc, len(jointSetFieldNames))
		for _, sf := range jointSetFieldNames {
			perOpt := make(map[string]map[string]*setPairCellAcc, len(setOptionNames[sf]))
			for _, opt := range setOptionNames[sf] {
				if opt == "" {
					continue
				}
				perCat := make(map[string]*setPairCellAcc, len(jointCatFieldNames))
				for _, cf := range jointCatFieldNames {
					perCat[cf] = newSetPairCellAcc()
				}
				perOpt[opt] = perCat
			}
			setCatAccs[sf] = perOpt
		}
	}

	// setNumAccs[setField][option][numField]["selected"|"not_selected"]
	// accumulates the online conditional mean/std inputs for set x
	// numeric capture (E5-S2), reusing condCatNumAcc verbatim — the
	// categorical axis is just fixed to the two-value Bernoulli domain.
	var setNumAccs map[string]map[string]map[string]map[string]*condCatNumAcc
	if trackSetNumJoint {
		setNumAccs = make(map[string]map[string]map[string]map[string]*condCatNumAcc, len(jointSetFieldNames))
		for _, sf := range jointSetFieldNames {
			perOpt := make(map[string]map[string]map[string]*condCatNumAcc, len(setOptionNames[sf]))
			for _, opt := range setOptionNames[sf] {
				if opt == "" {
					continue
				}
				perNum := make(map[string]map[string]*condCatNumAcc, len(jointFieldNames))
				for _, nf := range jointFieldNames {
					perNum[nf] = map[string]*condCatNumAcc{
						"selected":     {},
						"not_selected": {},
					}
				}
				perOpt[opt] = perNum
			}
			setNumAccs[sf] = perOpt
		}
	}

	// setSetAccs[fieldA][optionA][fieldB][optionB] accumulates the
	// online 2x2 contingency counts for set x set capture (E5-S2). Only
	// built for (fieldA, fieldB) pairs where fieldA precedes fieldB in
	// jointSetFieldNames, so each unordered field pair is captured
	// exactly once.
	var setSetAccs map[string]map[string]map[string]map[string]*setPairCellAcc
	if trackSetSetJoint {
		setSetAccs = make(map[string]map[string]map[string]map[string]*setPairCellAcc, len(jointSetFieldNames))
		for ai := 0; ai < len(jointSetFieldNames); ai++ {
			sfA := jointSetFieldNames[ai]
			perOptA := make(map[string]map[string]map[string]*setPairCellAcc, len(setOptionNames[sfA]))
			for bi := ai + 1; bi < len(jointSetFieldNames); bi++ {
				sfB := jointSetFieldNames[bi]
				for _, optA := range setOptionNames[sfA] {
					if optA == "" {
						continue
					}
					perFieldB := perOptA[optA]
					if perFieldB == nil {
						perFieldB = make(map[string]map[string]*setPairCellAcc)
						perOptA[optA] = perFieldB
					}
					perOptB := make(map[string]*setPairCellAcc, len(setOptionNames[sfB]))
					for _, optB := range setOptionNames[sfB] {
						if optB == "" {
							continue
						}
						perOptB[optB] = newSetPairCellAcc()
					}
					perFieldB[sfB] = perOptB
				}
			}
			setSetAccs[sfA] = perOptA
		}
	}

	values := make(map[string]float64, len(schema.Fields))
	nulls := make(map[string]bool, len(schema.Fields))
	wide := make(map[string]any, len(schema.Fields))
	catRowValues := make(map[string]string, len(jointCatFieldNames))
	catRowNulls := make(map[string]bool, len(jointCatFieldNames))

	// catReservoirRNG and catJointSeen drive Algorithm R reservoir
	// sampling for jointCatRows/jointCatNulls below (E7-S2). Built
	// unconditionally (cheap, no draws consumed until actually used) so
	// there is no conditional-declaration dance when trackCatJoint is
	// false. catJointSeen is the 0-indexed count of rows OFFERED to the
	// reservoir so far — distinct from rowCount, since a row is only
	// offered when trackCatJoint gates it on.
	catReservoirRNG := newRng(opts.Seed)
	catJointSeen := 0

	// fitter is nil unless ProfileOptions.FitModels asked for per-numeric
	// linear models AND at least one field is fittable, so the per-row
	// cost below is exactly zero for every existing caller. It rides
	// this loop rather than a second scan — see synth/profile_models.go.
	//
	// opts.TopK is handed over rather than re-derived: the model
	// capture's dummy expansion applies the SAME per-field top-K
	// collapse FieldProfile.Categorical.Top and the conditional
	// contingency tables apply, so a document's collapsed buckets mean
	// one thing across every section of it. The default is already
	// resolved at the top of this function, so the fitter never sees a
	// zero.
	var fitter *modelFitter
	if opts.FitModels {
		fitter = newModelFitter(schema, opts.TopK, opts.Seed, &warnings)
	}

	rowCount := 0
	for {
		err := rr.ReadRecordWithWide(values, nulls, wide)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		rowCount++
		if opts.SampleLimit > 0 && rowCount > opts.SampleLimit {
			break
		}
		for _, f := range schema.Fields {
			isNull := nulls[f.Name]
			switch {
			case f.Type == encoding.FieldTypeDate:
				da := dateAccs[f.Name]
				if isNull {
					da.nulls++
					continue
				}
				da.count++
				days := int64(values[f.Name])
				if days < da.minDays {
					da.minDays = days
				}
				if days > da.maxDays {
					da.maxDays = days
				}
				wd := dayOfWeek(days)
				da.weekdays[wd]++
			case f.Type.IsCategorical():
				ca := catAccs[f.Name]
				if isNull {
					ca.nulls++
					if needCatRow {
						catRowNulls[f.Name] = true
					}
					continue
				}
				id := uint32(values[f.Name])
				if f.Dictionary != nil {
					name := f.Dictionary.Resolve(id)
					if name == "" {
						if needCatRow {
							catRowNulls[f.Name] = true
						}
						continue
					}
					ca.hist[name]++
					ca.count++
					if needCatRow {
						catRowNulls[f.Name] = false
						catRowValues[f.Name] = name
					}
				} else if needCatRow {
					catRowNulls[f.Name] = true
				}
			case f.Type.IsSet():
				sa := setAccs[f.Name]
				if sa == nil {
					continue
				}
				if isNull {
					sa.nulls++
					continue
				}
				sa.count++
				// wide[f.Name] carries the exact uint64 mask (see
				// encoding.RecordReader.readRecord) — required for
				// set_u64, whose bits can exceed float64's 2^53 exact-
				// integer range; values[f.Name]'s float64 echo is not
				// used here for that reason.
				mask, _ := wide[f.Name].(uint64)
				for i := range sa.selected {
					if mask&(uint64(1)<<uint(i)) != 0 {
						sa.selected[i]++
					}
				}
			default:
				na := numAccs[f.Name]
				if na == nil {
					continue
				}
				if isNull {
					na.nulls++
					continue
				}
				v := values[f.Name]
				na.count++
				na.sum += v
				na.sumSq += float64(v * v)
				if v < na.min {
					na.min = v
				}
				if v > na.max {
					na.max = v
				}
				if na.reservoirOn && len(na.samples) < 10000 {
					na.samples = append(na.samples, v)
				}
			}
		}
		if fitter != nil {
			if err := fitter.observe(values, nulls, wide); err != nil {
				return nil, err
			}
		}
		if len(jointFieldNames) >= 2 && len(jointRows) < conditionalJointCap {
			rowVals := make([]float64, len(jointFieldNames))
			rowNulls := make([]bool, len(jointFieldNames))
			for idx, name := range jointFieldNames {
				rowNulls[idx] = nulls[name]
				if !rowNulls[idx] {
					rowVals[idx] = values[name]
				}
			}
			jointRows = append(jointRows, rowVals)
			jointNulls = append(jointNulls, rowNulls)
		}
		if trackCatJoint {
			// Genuine Algorithm R reservoir sampling, not first-N
			// truncation (E7-S2 — the prior `len(jointCatRows) <
			// conditionalJointCap` gate here silently favored whichever
			// block of a block-ordered source cohort, e.g. one sorted
			// by region, was read first). The first conditionalJointCap
			// rows fill the reservoir directly (no draw — matches the
			// classic algorithm, which never rejects while the
			// reservoir is still filling). Every row after that is
			// offered a uniformly-random reservoir slot with
			// probability conditionalJointCap/(catJointSeen+1) — drawn
			// via catReservoirRNG, seeded from ProfileOptions.Seed so
			// the same (source, seed) still produces byte-identical
			// captured output.
			rowVals := make([]string, len(jointCatFieldNames))
			rowNulls := make([]bool, len(jointCatFieldNames))
			for idx, name := range jointCatFieldNames {
				rowNulls[idx] = catRowNulls[name]
				if !rowNulls[idx] {
					rowVals[idx] = catRowValues[name]
				}
			}
			if len(jointCatRows) < conditionalJointCap {
				jointCatRows = append(jointCatRows, rowVals)
				jointCatNulls = append(jointCatNulls, rowNulls)
			} else if j := catReservoirRNG.IntN(catJointSeen + 1); j < conditionalJointCap {
				jointCatRows[j] = rowVals
				jointCatNulls[j] = rowNulls
			}
			catJointSeen++
		}
		if trackCatNumJoint {
			for _, cf := range jointCatFieldNames {
				if catRowNulls[cf] {
					continue
				}
				catVal := catRowValues[cf]
				perNum := catNumAccs[cf]
				for _, nf := range jointFieldNames {
					if nulls[nf] {
						continue
					}
					v := values[nf]
					acc := perNum[nf][catVal]
					if acc == nil {
						acc = &condCatNumAcc{}
						perNum[nf][catVal] = acc
					}
					acc.count++
					acc.sum += v
					acc.sumSq += float64(v * v)
				}
			}
		}
		if trackSetCatJoint {
			for _, sf := range jointSetFieldNames {
				if nulls[sf] {
					continue
				}
				mask, _ := wide[sf].(uint64)
				perOpt := setCatAccs[sf]
				for i, opt := range setOptionNames[sf] {
					if opt == "" {
						continue
					}
					sel := "not_selected"
					if mask&(uint64(1)<<uint(i)) != 0 {
						sel = "selected"
					}
					perCat := perOpt[opt]
					for _, cf := range jointCatFieldNames {
						if catRowNulls[cf] {
							continue
						}
						acc := perCat[cf]
						acc.counts[[2]string{sel, catRowValues[cf]}]++
					}
				}
			}
		}
		if trackSetNumJoint {
			for _, sf := range jointSetFieldNames {
				if nulls[sf] {
					continue
				}
				mask, _ := wide[sf].(uint64)
				perOpt := setNumAccs[sf]
				for i, opt := range setOptionNames[sf] {
					if opt == "" {
						continue
					}
					sel := "not_selected"
					if mask&(uint64(1)<<uint(i)) != 0 {
						sel = "selected"
					}
					perNum := perOpt[opt]
					for _, nf := range jointFieldNames {
						if nulls[nf] {
							continue
						}
						v := values[nf]
						acc := perNum[nf][sel]
						acc.count++
						acc.sum += v
						acc.sumSq += float64(v * v)
					}
				}
			}
		}
		if trackSetSetJoint {
			for ai := 0; ai < len(jointSetFieldNames); ai++ {
				sfA := jointSetFieldNames[ai]
				if nulls[sfA] {
					continue
				}
				maskA, _ := wide[sfA].(uint64)
				perOptA := setSetAccs[sfA]
				for bi := ai + 1; bi < len(jointSetFieldNames); bi++ {
					sfB := jointSetFieldNames[bi]
					if nulls[sfB] {
						continue
					}
					maskB, _ := wide[sfB].(uint64)
					for oi, optA := range setOptionNames[sfA] {
						if optA == "" {
							continue
						}
						selA := "not_selected"
						if maskA&(uint64(1)<<uint(oi)) != 0 {
							selA = "selected"
						}
						perFieldB := perOptA[optA][sfB]
						for oj, optB := range setOptionNames[sfB] {
							if optB == "" {
								continue
							}
							selB := "not_selected"
							if maskB&(uint64(1)<<uint(oj)) != 0 {
								selB = "selected"
							}
							acc := perFieldB[optB]
							acc.counts[[2]string{selA, selB}]++
						}
					}
				}
			}
		}
	}

	// Build the field profiles in declaration order.
	pf := &Profile{RowCount: rowCount}
	for _, f := range schema.Fields {
		fp := FieldProfile{
			Name:        f.Name,
			Type:        f.Type.String(),
			Description: f.Description,
			Precision:   f.Precision,
			Scale:       f.Scale,
		}
		switch {
		case f.Type == encoding.FieldTypeDate:
			da := dateAccs[f.Name]
			total := da.count + da.nulls
			if total > 0 {
				fp.NullRate = float64(da.nulls) / float64(total)
			}
			if da.count > 0 {
				fp.Date = &DateProfile{
					Start:    daysToISO(da.minDays),
					End:      daysToISO(da.maxDays),
					Weekdays: da.weekdays,
				}
			}
		case f.Type.IsCategorical():
			ca := catAccs[f.Name]
			total := ca.count + ca.nulls
			if total > 0 {
				fp.NullRate = float64(ca.nulls) / float64(total)
			}
			if ca.count > 0 {
				fp.Categorical = &CategoricalProfile{
					Cardinality: len(ca.hist),
					Top:         topNCategorical(ca.hist, opts.TopK),
				}
			}
		case f.Type.IsSet():
			sa := setAccs[f.Name]
			if sa == nil {
				break
			}
			total := sa.count + sa.nulls
			if total > 0 {
				fp.NullRate = float64(sa.nulls) / float64(total)
			}
			if sa.count > 0 && f.Dictionary != nil {
				setOpts := make([]SetOptionStat, 0, len(sa.selected))
				for i, c := range sa.selected {
					label := f.Dictionary.Resolve(uint32(i))
					if label == "" {
						continue
					}
					setOpts = append(setOpts, SetOptionStat{
						Value:     label,
						Count:     c,
						Frequency: float64(c) / float64(sa.count),
					})
				}
				fp.Set = &SetProfile{N: sa.count, Options: setOpts}
			}
		default:
			na := numAccs[f.Name]
			if na == nil {
				break
			}
			total := na.count + na.nulls
			if total > 0 {
				fp.NullRate = float64(na.nulls) / float64(total)
			}
			if na.count > 0 {
				mean := na.sum / float64(na.count)
				variance := sampleVariance(na.count, na.sum, na.sumSq)
				num := &NumericProfile{
					Min:  na.min,
					Max:  na.max,
					Mean: mean,
					Std:  math.Sqrt(variance),
				}
				if includeStats && len(na.samples) > 0 {
					num.Percentiles = computePercentiles(na.samples,
						[]float64{0.01, 0.05, 0.25, 0.5, 0.75, 0.95, 0.99})
				}
				if opts.FitShape && len(na.samples) > 0 {
					num.Shape = fitNumericShape(na.samples, mean, num.Std)
				}
				fp.Numeric = num
			}
		}
		pf.Fields = append(pf.Fields, fp)
	}

	if opts.IncludeCorrelations {
		pf.Pairwise = computeCorrelations(pf, numAccs, opts.CorrelationTopK)
	}

	if opts.IncludeConditional {
		numericPairs := computeConditionalNumericPairs(
			jointFieldNames, jointRows, jointNulls, opts.CorrelationTopK, &warnings)
		topKByField := make(map[string]map[string]bool, len(jointCatFieldNames))
		for _, fp := range pf.Fields {
			if fp.Categorical == nil {
				continue
			}
			set := make(map[string]bool, len(fp.Categorical.Top))
			for _, hit := range fp.Categorical.Top {
				set[hit.Value] = true
			}
			topKByField[fp.Name] = set
		}
		categoricalPairs := computeConditionalCategoricalPairs(
			jointCatFieldNames, jointCatRows, jointCatNulls, topKByField, &warnings)
		catNumericPairs := computeConditionalCategoricalNumericPairs(
			jointCatFieldNames, jointFieldNames, catNumAccs, topKByField, &warnings)
		setCategoricalPairs := computeSetCategoricalPairs(
			jointSetFieldNames, setOptionNames, setCatAccs, jointCatFieldNames, topKByField, &warnings)
		setNumericPairs := computeSetNumericPairs(
			jointSetFieldNames, setOptionNames, setNumAccs, jointFieldNames, &warnings)
		setSetPairs := computeSetSetPairs(
			jointSetFieldNames, setOptionNames, setSetAccs, &warnings)
		if len(numericPairs) > 0 || len(categoricalPairs) > 0 || len(catNumericPairs) > 0 ||
			len(setCategoricalPairs) > 0 || len(setNumericPairs) > 0 || len(setSetPairs) > 0 {
			pf.Conditional = &ConditionalProfile{
				NumericPairs:            numericPairs,
				CategoricalPairs:        categoricalPairs,
				CategoricalNumericPairs: catNumericPairs,
				SetCategoricalPairs:     setCategoricalPairs,
				SetNumericPairs:         setNumericPairs,
				SetSetPairs:             setSetPairs,
			}
		}
	}

	if fitter != nil {
		pf.Models = fitter.finish(&warnings)
	}

	// The residual submatrix runs after the fits because it is measured
	// FROM them: FieldModel.Residuals does not exist until finish() has
	// solved every model and replayed the snapshot. It needs no cohort
	// access of its own — the residual reservoir is already in memory —
	// so it adds no read to the single scan.
	if opts.FitResidualCorrelations {
		switch {
		case !opts.FitModels:
			// Refusing outright would be disproportionate for a flag
			// combination whose only consequence is an absent section,
			// but staying silent would leave a caller believing they
			// captured a submatrix they did not. Say so and carry on.
			warnings = append(warnings,
				"residual correlations requested without --fit-models: residuals come from fitted "+
					"models, so no models means nothing to correlate and no residual_correlations section is written")
		default:
			pf.ResidualCorrelations = computeResidualCorrelations(pf.Models, &warnings)
		}
	}

	if len(warnings) > 0 {
		pf.Warnings = warnings
	}

	return pf, nil
}

func dayOfWeek(daysSinceEpoch int64) int {
	// 1970-01-01 was a Thursday (weekday index 4 with Sunday=0).
	wd := (int(daysSinceEpoch%7) + 4) % 7
	if wd < 0 {
		wd += 7
	}
	return wd
}

func daysToISO(days int64) string {
	t := time.Unix(days*86400, 0).UTC()
	return t.Format("2006-01-02")
}

func topNCategorical(hist map[string]int, n int) []CategoryHit {
	type pair struct {
		k string
		v int
	}
	pairs := make([]pair, 0, len(hist))
	for k, v := range hist {
		pairs = append(pairs, pair{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].v != pairs[j].v {
			return pairs[i].v > pairs[j].v
		}
		return pairs[i].k < pairs[j].k
	})
	if len(pairs) > n {
		pairs = pairs[:n]
	}
	total := 0
	for _, p := range pairs {
		total += p.v
	}
	out := make([]CategoryHit, len(pairs))
	for i, p := range pairs {
		w := 0.0
		if total > 0 {
			w = float64(p.v) / float64(total)
		}
		out[i] = CategoryHit{Value: p.k, Weight: w}
	}
	return out
}

func computePercentiles(samples []float64, qs []float64) []float64 {
	sorted := make([]float64, len(samples))
	copy(sorted, samples)
	sort.Float64s(sorted)
	out := make([]float64, len(qs))
	for i, q := range qs {
		if len(sorted) == 0 {
			out[i] = 0
			continue
		}
		idx := float64(q * float64(len(sorted)-1))
		lo := int(math.Floor(idx))
		hi := int(math.Ceil(idx))
		if lo == hi {
			out[i] = sorted[lo]
			continue
		}
		frac := idx - float64(lo)
		out[i] = float64(sorted[lo]*(1-frac)) + float64(sorted[hi]*frac)
	}
	return out
}

// computeCorrelations returns up to topK strongest absolute Pearson
// correlations among numeric fields whose accumulator captured samples.
func computeCorrelations(pf *Profile, numAccs map[string]*numAcc, topK int) []CorrelationStat {
	// Collect sample arrays for fields that retained them.
	type kept struct {
		name    string
		samples []float64
		mean    float64
	}
	var fields []kept
	for _, fp := range pf.Fields {
		if fp.Numeric == nil {
			continue
		}
		na, ok := numAccs[fp.Name]
		if !ok || len(na.samples) == 0 {
			continue
		}
		mean := na.sum / float64(na.count)
		fields = append(fields, kept{name: fp.Name, samples: na.samples, mean: mean})
	}
	if len(fields) < 2 {
		return nil
	}

	pairs := make([]CorrelationStat, 0, len(fields)*(len(fields)-1)/2)
	for i := 0; i < len(fields); i++ {
		for j := i + 1; j < len(fields); j++ {
			rho := pearson(fields[i].samples, fields[j].samples)
			if math.IsNaN(rho) {
				continue
			}
			pairs = append(pairs, CorrelationStat{
				A: fields[i].name, B: fields[j].name, Rho: rho,
			})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		return math.Abs(pairs[i].Rho) > math.Abs(pairs[j].Rho)
	})
	if len(pairs) > topK {
		pairs = pairs[:topK]
	}
	return pairs
}

// pearson returns the Pearson correlation of two equal-length samples.
// When the lengths differ we truncate to min(len(a), len(b)) — the
// reservoir cap keeps both bounded so this is a no-op in practice.
func pearson(a, b []float64) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n < 2 {
		return math.NaN()
	}
	var sumA, sumB float64
	for i := 0; i < n; i++ {
		sumA += a[i]
		sumB += b[i]
	}
	mA := sumA / float64(n)
	mB := sumB / float64(n)
	var num, dA, dB float64
	for i := 0; i < n; i++ {
		da := a[i] - mA
		db := b[i] - mB
		num += float64(da * db)
		dA += float64(da * da)
		dB += float64(db * db)
	}
	if dA == 0 || dB == 0 {
		return math.NaN()
	}
	return num / math.Sqrt(dA*dB)
}

// computeConditionalNumericPairs derives (rho, n) for every
// numeric-numeric pair from row-aligned joint samples — rows[r][i] /
// nulls[r][i] is field fields[i]'s value/null-flag on captured row r,
// exactly as jointRows/jointNulls were built during the streaming pass.
// Only rows where BOTH fields of a pair were simultaneously non-null
// count toward that pair's N and its Rho — the co-occurrence discipline
// Profile.Pairwise's computeCorrelations cannot guarantee once any
// field carries nulls, since its per-field reservoirs are filled
// independently. Returns nil when no field pair produced a defined
// correlation (fewer than two co-occurring observations). Pairs below
// MinPairObservations still ship — appended as a warning to *warnings,
// never dropped.
func computeConditionalNumericPairs(fields []string, rows [][]float64, nulls [][]bool, topK int, warnings *[]string) []NumericPairProfile {
	n := len(fields)
	if n < 2 {
		return nil
	}
	pairs := make([]NumericPairProfile, 0, n*(n-1)/2)
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			var a, b []float64
			for r := range rows {
				if nulls[r][i] || nulls[r][j] {
					continue
				}
				a = append(a, rows[r][i])
				b = append(b, rows[r][j])
			}
			rho := pearson(a, b)
			if math.IsNaN(rho) {
				continue
			}
			pairs = append(pairs, NumericPairProfile{A: fields[i], B: fields[j], Rho: rho, N: len(a)})
		}
	}
	if len(pairs) == 0 {
		return nil
	}
	sort.Slice(pairs, func(i, j int) bool {
		return math.Abs(pairs[i].Rho) > math.Abs(pairs[j].Rho)
	})
	if len(pairs) > topK {
		pairs = pairs[:topK]
	}
	for _, p := range pairs {
		if w := thinPairWarning("numeric", p.A, p.B, p.N, MinPairObservations); w != "" {
			*warnings = append(*warnings, w)
		}
	}
	return pairs
}

// computeConditionalCategoricalPairs derives a bounded contingency
// table for every categorical-categorical pair from row-aligned joint
// samples — rows[r][i]/nulls[r][i] is field fields[i]'s resolved
// category name / null-flag on captured row r, built the same way
// jointCatRows/jointCatNulls were built during the streaming pass, in
// the same co-occurrence discipline computeConditionalNumericPairs
// applies to numeric fields: only rows where BOTH fields of a pair
// were simultaneously non-null count toward that pair's table.
//
// topKByField supplies each field's own already-computed top-K set
// (FieldProfile.Categorical.Top, itself bounded by ProfileOptions.TopK)
// — any value not in that set collapses to otherCategoryLabel before
// the joint table is built, composing the existing per-field cap with
// this story's new joint-cell cap (collapseCells) rather than
// replacing it. A field absent from topKByField (e.g. entirely null)
// collapses every one of its values to "other", which is harmless
// because such a field never contributes a non-null row to any pair.
//
// Returns nil when no categorical-categorical pair produced any
// co-occurring observation. Every returned pair's Cells respects
// ContingencyCellCap; cells below MinPairObservations still ship
// (never dropped) but are recorded via the same thinPairWarning helper
// E2-S1 introduced for numeric pairs — no second, divergent warning
// mechanism.
func computeConditionalCategoricalPairs(fields []string, rows [][]string, nulls [][]bool, topKByField map[string]map[string]bool, warnings *[]string) []CategoricalPairProfile {
	n := len(fields)
	if n < 2 {
		return nil
	}
	var pairs []CategoricalPairProfile
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			allowedA := topKByField[fields[i]]
			allowedB := topKByField[fields[j]]
			counts := make(map[[2]string]int)
			total := 0
			for r := range rows {
				if nulls[r][i] || nulls[r][j] {
					continue
				}
				a := rows[r][i]
				if !allowedA[a] {
					a = otherCategoryLabel
				}
				b := rows[r][j]
				if !allowedB[b] {
					b = otherCategoryLabel
				}
				counts[[2]string{a, b}]++
				total++
			}
			if total == 0 {
				continue
			}
			cells := collapseCells(counts, ContingencyCellCap)
			for _, c := range cells {
				aLabel := fmt.Sprintf("%s=%s", fields[i], c.AValue)
				bLabel := fmt.Sprintf("%s=%s", fields[j], c.BValue)
				if w := thinPairWarning("categorical", aLabel, bLabel, c.Count, MinPairObservations); w != "" {
					*warnings = append(*warnings, w)
				}
			}
			pairs = append(pairs, CategoricalPairProfile{A: fields[i], B: fields[j], Cells: cells, N: total})
		}
	}
	return pairs
}

// computeConditionalCategoricalNumericPairs derives the numeric field's
// conditional mean/std per observed category, for every
// categorical-numeric pair, from the online condCatNumAcc accumulators
// built during the streaming pass (catNumAccs[catField][numField]
// [rawCategoryValue]) — unlike the row-aligned snapshots
// computeConditionalNumericPairs and computeConditionalCategoricalPairs
// consume, this needs no such snapshot: a running (count, sum, sumSq)
// per raw category value is sufficient to derive a conditional mean/std
// exactly, with no reservoir cap on the underlying observation count.
//
// topKByField supplies each categorical field's own already-computed
// top-K set (FieldProfile.Categorical.Top) — a raw category value not
// in that set has its accumulator folded into one otherCategoryLabel
// bucket before the conditional mean/std is computed, the same
// per-field collapse computeConditionalCategoricalPairs applies. A
// field absent from topKByField (e.g. entirely null) collapses every
// value to "other", harmless because such a field never contributes an
// accumulator entry.
//
// Returns nil when no categorical-numeric pair produced any
// co-occurring observation. A category (or "other") whose N falls below
// MinPairObservations still ships — never dropped — but is recorded via
// the same thinPairWarning helper E2-S1/E3-S1 already use, so this
// third pair kind carries no separate warning mechanism.
func computeConditionalCategoricalNumericPairs(catFields, numFields []string, catNumAccs map[string]map[string]map[string]*condCatNumAcc, topKByField map[string]map[string]bool, warnings *[]string) []CategoricalNumericPairProfile {
	if len(catFields) == 0 || len(numFields) == 0 {
		return nil
	}
	var pairs []CategoricalNumericPairProfile
	for _, cf := range catFields {
		perNum := catNumAccs[cf]
		allowed := topKByField[cf]
		for _, nf := range numFields {
			raw := perNum[nf]
			if len(raw) == 0 {
				continue
			}
			// Collapse raw (uncapped) category values into the field's
			// own top-K plus one "other" bucket, mirroring the
			// per-field collapse computeConditionalCategoricalPairs
			// applies before building its joint table.
			//
			// The fold walks raw in SORTED key order rather than in map
			// order, and that is load-bearing rather than tidiness.
			// Every out-of-top-K category lands in the ONE shared
			// otherCategoryLabel accumulator, so that bucket's
			// sum/sumSq are a pairwise float64 sum over many addends;
			// float addition is not associative and Go randomizes map
			// iteration, so folding in map order gives a different
			// sumSq per process. The variance below is the
			// cancellation-prone (sumSq - mean*sum) form, which
			// amplifies rather than absorbs that: on a cohort of
			// ~1e4-magnitude values sumSq lands near 1e11 against a
			// variance near 1e8, turning a single last-bit difference
			// in the fold into a ~1e-10 relative drift in the emitted
			// Std. That was a real, reproduced break of the
			// byte-reproducibility `pulse profile create` promises, and
			// its signature was diagnostic: ONLY "other" entries
			// drifted, because "other" is the only multi-source
			// accumulator here — an in-top-K key has exactly one source
			// and no fold order to get wrong. Sorting is the fix rather
			// than compensated summation because the goal is a
			// REPRODUCIBLE answer, and Kahan summation is still
			// order-dependent in principle. The cost is bounded by
			// TopK + the raw distinct-value count and paid once per
			// pair, not per record.
			rawKeys := make([]string, 0, len(raw))
			for catVal := range raw {
				rawKeys = append(rawKeys, catVal)
			}
			sort.Strings(rawKeys)
			collapsed := make(map[string]*condCatNumAcc)
			for _, catVal := range rawKeys {
				acc := raw[catVal]
				key := catVal
				if !allowed[catVal] {
					key = otherCategoryLabel
				}
				dst := collapsed[key]
				if dst == nil {
					dst = &condCatNumAcc{}
					collapsed[key] = dst
				}
				dst.count += acc.count
				dst.sum += acc.sum
				dst.sumSq += acc.sumSq
			}
			total := 0
			cats := make([]CategoricalNumericCategoryStat, 0, len(collapsed))
			for catVal, acc := range collapsed {
				if acc.count == 0 {
					continue
				}
				mean := acc.sum / float64(acc.count)
				variance := sampleVariance(acc.count, acc.sum, acc.sumSq)
				cats = append(cats, CategoricalNumericCategoryStat{
					Category: catVal,
					Mean:     mean,
					Std:      math.Sqrt(variance),
					N:        acc.count,
				})
				total += acc.count
			}
			if total == 0 {
				continue
			}
			sort.Slice(cats, func(i, j int) bool {
				if cats[i].N != cats[j].N {
					return cats[i].N > cats[j].N
				}
				return cats[i].Category < cats[j].Category
			})
			for _, c := range cats {
				label := fmt.Sprintf("%s=%s", cf, c.Category)
				if w := thinPairWarning("categorical-numeric", label, nf, c.N, MinPairObservations); w != "" {
					*warnings = append(*warnings, w)
				}
			}
			pairs = append(pairs, CategoricalNumericPairProfile{A: cf, B: nf, Categories: cats, N: total})
		}
	}
	return pairs
}

// computeSetCategoricalPairs derives a bounded contingency table for
// every (set field option) x categorical field combination from the
// online setCatAccs accumulators (E5-S2) — one entry per option, per
// E5-S1's per-option Bernoulli representation, reusing the exact
// ContingencyCellCap / otherCategoryLabel collapse
// computeConditionalCategoricalPairs already applies to two arbitrary
// categorical fields. topKByField supplies the categorical field's own
// already-computed top-K set — a raw category value outside it
// collapses to otherCategoryLabel before the table is built, exactly as
// the categorical-categorical path does on its B axis. The set-option
// axis itself never needs a top-K collapse: it is always the fixed
// two-value {"selected","not_selected"} domain.
//
// Returns nil when no set field or no categorical field is present.
// Every returned pair's Cells respects ContingencyCellCap; cells below
// MinPairObservations still ship (never dropped) but are recorded via
// the same thinPairWarning helper every other pair kind uses.
func computeSetCategoricalPairs(setFields []string, setOptionNames map[string][]string, accs map[string]map[string]map[string]*setPairCellAcc, catFields []string, topKByField map[string]map[string]bool, warnings *[]string) []SetCategoricalPairProfile {
	if len(setFields) == 0 || len(catFields) == 0 {
		return nil
	}
	var pairs []SetCategoricalPairProfile
	for _, sf := range setFields {
		for _, opt := range setOptionNames[sf] {
			if opt == "" {
				continue
			}
			for _, cf := range catFields {
				acc := accs[sf][opt][cf]
				if acc == nil || len(acc.counts) == 0 {
					continue
				}
				allowed := topKByField[cf]
				collapsed := make(map[[2]string]int, len(acc.counts))
				total := 0
				for k, v := range acc.counts {
					catVal := k[1]
					if !allowed[catVal] {
						catVal = otherCategoryLabel
					}
					collapsed[[2]string{k[0], catVal}] += v
					total += v
				}
				if total == 0 {
					continue
				}
				cells := collapseCells(collapsed, ContingencyCellCap)
				for _, c := range cells {
					aLabel := fmt.Sprintf("%s[%s]=%s", sf, opt, c.AValue)
					bLabel := fmt.Sprintf("%s=%s", cf, c.BValue)
					if w := thinPairWarning("set-categorical", aLabel, bLabel, c.Count, MinPairObservations); w != "" {
						*warnings = append(*warnings, w)
					}
				}
				pairs = append(pairs, SetCategoricalPairProfile{
					Set: sf, Option: opt, Categorical: cf, Cells: cells, N: total,
				})
			}
		}
	}
	return pairs
}

// computeSetNumericPairs derives the numeric field's conditional
// mean/std (selected vs. not_selected) for every (set field option) x
// numeric field combination, from the online setNumAccs accumulators
// (E5-S2) — the set-field analogue of
// computeConditionalCategoricalNumericPairs, with the categorical axis
// fixed to the two-value {"selected","not_selected"} domain rather than
// an arbitrary category set, so no per-field top-K collapse is needed.
//
// Returns nil when no set field or no numeric field is present. A
// bucket (selected or not_selected) whose N falls below
// MinPairObservations still ships — never dropped — but is recorded via
// the same thinPairWarning helper every other pair kind uses.
func computeSetNumericPairs(setFields []string, setOptionNames map[string][]string, accs map[string]map[string]map[string]map[string]*condCatNumAcc, numFields []string, warnings *[]string) []SetNumericPairProfile {
	if len(setFields) == 0 || len(numFields) == 0 {
		return nil
	}
	var pairs []SetNumericPairProfile
	for _, sf := range setFields {
		for _, opt := range setOptionNames[sf] {
			if opt == "" {
				continue
			}
			for _, nf := range numFields {
				buckets := accs[sf][opt][nf]
				if buckets == nil {
					continue
				}
				total := 0
				var cats []CategoricalNumericCategoryStat
				for _, key := range [2]string{"selected", "not_selected"} {
					acc := buckets[key]
					if acc == nil || acc.count == 0 {
						continue
					}
					mean := acc.sum / float64(acc.count)
					variance := sampleVariance(acc.count, acc.sum, acc.sumSq)
					cats = append(cats, CategoricalNumericCategoryStat{
						Category: key, Mean: mean, Std: math.Sqrt(variance), N: acc.count,
					})
					total += acc.count
				}
				if total == 0 {
					continue
				}
				for _, c := range cats {
					label := fmt.Sprintf("%s[%s]=%s", sf, opt, c.Category)
					if w := thinPairWarning("set-numeric", label, nf, c.N, MinPairObservations); w != "" {
						*warnings = append(*warnings, w)
					}
				}
				pairs = append(pairs, SetNumericPairProfile{
					Set: sf, Option: opt, Numeric: nf, Categories: cats, N: total,
				})
			}
		}
	}
	return pairs
}

// computeSetSetPairs derives a bounded 2x2 contingency table for every
// option x option combination between two DIFFERENT set_* fields, from
// the online setSetAccs accumulators (E5-S2) — reuses collapseCells /
// ContingencyCellCap for consistency with the other pair kinds, though
// a 2x2 table never actually reaches the cap in practice since both
// axes are the fixed two-value {"selected","not_selected"} domain.
//
// Returns nil when fewer than two set fields are present. Every cell
// below MinPairObservations still ships — never dropped — but is
// recorded via the same thinPairWarning helper every other pair kind
// uses.
func computeSetSetPairs(setFields []string, setOptionNames map[string][]string, accs map[string]map[string]map[string]map[string]*setPairCellAcc, warnings *[]string) []SetSetPairProfile {
	n := len(setFields)
	if n < 2 {
		return nil
	}
	var pairs []SetSetPairProfile
	for ai := 0; ai < n; ai++ {
		sfA := setFields[ai]
		for bi := ai + 1; bi < n; bi++ {
			sfB := setFields[bi]
			for _, optA := range setOptionNames[sfA] {
				if optA == "" {
					continue
				}
				for _, optB := range setOptionNames[sfB] {
					if optB == "" {
						continue
					}
					acc := accs[sfA][optA][sfB][optB]
					if acc == nil || len(acc.counts) == 0 {
						continue
					}
					total := 0
					for _, v := range acc.counts {
						total += v
					}
					if total == 0 {
						continue
					}
					cells := collapseCells(acc.counts, ContingencyCellCap)
					for _, c := range cells {
						aLabel := fmt.Sprintf("%s[%s]=%s", sfA, optA, c.AValue)
						bLabel := fmt.Sprintf("%s[%s]=%s", sfB, optB, c.BValue)
						if w := thinPairWarning("set-set", aLabel, bLabel, c.Count, MinPairObservations); w != "" {
							*warnings = append(*warnings, w)
						}
					}
					pairs = append(pairs, SetSetPairProfile{
						SetA: sfA, OptionA: optA, SetB: sfB, OptionB: optB, Cells: cells, N: total,
					})
				}
			}
		}
	}
	return pairs
}

// collapseCells bounds counts — a raw (a_value, b_value) -> count
// contingency map — to at most cap cells, sorted by count descending
// (ties broken lexicographically for determinism). When the raw map
// already fits within cap it is returned unchanged, in cap-sized
// (count desc) order.
//
// Otherwise a pre-existing ("other","other") cell — the caller's own
// per-field top-K collapse already folds every excluded category into
// that combination, so it is frequently the single largest cell in the
// raw map — is pulled out of ranking contention first, rather than
// competing for one of the kept "real" slots on the same basis as any
// other cell. The top cap-1 REMAINING (non-"other","other") cells are
// kept as-is; every cell excluded from that top cap-1 — the ranked
// tail plus the pulled-out pre-existing ("other","other") cell, if any
// — is folded into exactly one merged ("other","other") catch-all.
// This is deliberate, not incidental: a cell that already represents
// "value excluded by the per-field cap" is definitionally catch-all
// mass, not a competing observation, so it must never occupy a kept
// slot only to have the joint cap's own tail merge into it in place
// (which would waste a slot and make the output land at cap-1 instead
// of cap). Folding it out first instead guarantees the result
// saturates to EXACTLY cap cells whenever the raw distinct cell count
// exceeds the cap — deterministic regardless of where the pre-existing
// cell would otherwise have ranked.
func collapseCells(counts map[[2]string]int, cellCap int) []ContingencyCell {
	if cellCap < 1 {
		cellCap = 1
	}
	cells := make([]ContingencyCell, 0, len(counts))
	for k, v := range counts {
		cells = append(cells, ContingencyCell{AValue: k[0], BValue: k[1], Count: v})
	}
	sort.Slice(cells, func(i, j int) bool {
		if cells[i].Count != cells[j].Count {
			return cells[i].Count > cells[j].Count
		}
		if cells[i].AValue != cells[j].AValue {
			return cells[i].AValue < cells[j].AValue
		}
		return cells[i].BValue < cells[j].BValue
	})
	if len(cells) <= cellCap {
		return cells
	}

	preOtherCount := 0
	real := make([]ContingencyCell, 0, len(cells))
	for _, c := range cells {
		if c.AValue == otherCategoryLabel && c.BValue == otherCategoryLabel {
			preOtherCount = c.Count
			continue
		}
		real = append(real, c)
	}

	keepN := cellCap - 1
	if keepN > len(real) {
		keepN = len(real)
	}
	kept := real[:keepN]
	tail := real[keepN:]

	otherCount := preOtherCount
	for _, c := range tail {
		otherCount += c.Count
	}

	out := make([]ContingencyCell, len(kept), len(kept)+1)
	copy(out, kept)
	return append(out, ContingencyCell{AValue: otherCategoryLabel, BValue: otherCategoryLabel, Count: otherCount})
}

// SpecFromProfile builds a Spec the synth pipeline can execute. Numeric
// fields are reconstructed as normal distributions (mean, std clipped
// at min/max), categorical fields as weighted_categorical, and date
// fields as uniform_date over the observed range.
//
// # Choosing between `models` and `conditional`'s numeric-target arms
//
// A numeric field that lands a model on the Spec (`profile create
// --fit-models`) is reconstructed from that additive linear predictor,
// and the two NUMERIC-TARGET conditional arms — CategoricalNumericPairs
// and SetNumericPairs — are then not populated FOR THAT FIELD. This is a
// REPLACEMENT, not a preference between equals: those arms exist to
// resample a numeric from one paired field's conditional moments, so on
// a cohort with several categorical and set fields every one of them
// claims the same numeric target and all but the first are dropped by
// resolveConflicts, one warning each. A model already accounts for all
// of those predictors simultaneously, so the pairs have nothing left to
// add and populating them would generate thousands of conflict warnings
// about relationships the spec no longer needs. Leaving the slots empty
// is what retires them as claimants — resolveConflicts itself is
// untouched and still arbitrates exactly as before for every spec that
// does populate them.
//
// The retirement is PER TARGET, deliberately, and it did not start that
// way. It was first written per DOCUMENT — one `models` section retired
// both arms wholesale — on the reasoning that a spec mixing model-driven
// and pair-driven numerics would give two fields in the same cohort two
// different reconstruction semantics with nothing on the wire saying
// which got which. That reasoning does not survive contact with a real
// cohort. Predictor selection means a target can legitimately end up
// with no model (its marginal reconstructed to something a linear
// predictor cannot ride, or its design refused as unsolvable), and under
// the document-wide rule such a field lost its captured conditional pair
// as well and came out reconstructed from NOTHING — strictly worse than
// before --fit-models existed. The mixed-semantics objection is also
// answerable: the spec names both sets explicitly (Spec.Models versus
// Spec.CategoricalNumericPairs / Spec.SetNumericPairs), so the wire does
// say which got which, and resolveConflicts already claims a modelled
// target before any pair stage bids, so a field can never be reached by
// both. Falling back per field costs nothing and keeps a skipped model
// from silently deleting structure the capture measured.
//
// The three NON-numeric-target arms (CategoricalPairs,
// SetCategoricalPairs, SetSetPairs) are unaffected and populate exactly
// as they always have: a model predicts a numeric FROM categorical and
// set structure, it says nothing about how that structure co-varies
// with itself. A user may legitimately pass --conditional and
// --fit-models together and gets both halves.
//
// Absent `models`, every arm populates exactly as before — which is the
// path every document written before the section existed takes, and the
// same silent-fallback shape Conditional.NumericPairs-over-Pairwise
// already uses below.
//
// The second return value carries any conditional-relationship conflict
// warnings resolveConflicts (synth/conflict.go, E6-S1) produces when run
// against the just-composed Spec — e.g. two captured pairs both
// legitimately targeting the same field. generate() (synth/writer.go)
// independently runs the same pass again at its own setup time, so these
// two calls always agree; this one exists only because
// Profile.Warnings is serialized to disk at `profile create` time,
// before SpecFromProfile ever runs at `synth from-profile` time — there
// is no way to retroactively write a synth-time conflict warning back
// into that already-persisted document. Returning the warnings here lets
// a caller (internal/cli's `synth from-profile`) fold them into the same
// FidelityWarnings channel it already uses for prof.Warnings, so both
// surface together through `--fidelity-report`. Callers that don't need
// them may discard with `_`.
func SpecFromProfile(p *Profile, rowCount int) (*Spec, []string) {
	s := &Spec{RowCount: rowCount}
	for _, fp := range p.Fields {
		fs := FieldSpec{
			Name:        fp.Name,
			Type:        fp.Type,
			Description: fp.Description,
			// FieldProfile carries no direct "was this field declared
			// nullable" flag — only the observed NullRate (nulls/total
			// over the profiled sample, computed above in ProfileCohort).
			// NullRate > 0 is nonetheless an exact proxy, not a heuristic:
			// a non-nullable schema field can never contribute a counted
			// null, so NullRate is strictly 0 for one and NullRate == 0
			// already short-circuits buildSampler's nullableSampler wrap
			// (synth/distributions.go) — so this is the only bit that can
			// possibly matter. Without it, FieldSpec.Nullable stays at its
			// zero value false while NullRate still drives buildSampler to
			// sample nulls; encodeRow (synth/writer.go) then only ever
			// consults encoding.Field.Nullable (set from fs.Nullable, not
			// NullRate) to decide whether a null sample reaches the
			// per-record bitmap, so every "null" draw for such a field was
			// silently written as an in-band 0 — indistinguishable from a
			// genuine zero.
			Nullable:  fp.NullRate > 0,
			NullRate:  fp.NullRate,
			Precision: fp.Precision,
			Scale:     fp.Scale,
		}
		switch {
		case fp.Set != nil:
			// E5-S3: each dictionary entry's per-option marginal
			// Bernoulli frequency (E5-S1) becomes one entry of the
			// set_bernoulli sampler's parallel options/frequencies
			// params — every option drawn independently unless a
			// captured joint pair (below) overrides one option's draw.
			options := make([]any, len(fp.Set.Options))
			freqs := make([]any, len(fp.Set.Options))
			for i, o := range fp.Set.Options {
				options[i] = o.Value
				freqs[i] = o.Frequency
			}
			fs.Distribution = DistSetBernoulli
			fs.Params = map[string]any{"options": options, "frequencies": freqs}
		case fp.Date != nil:
			fs.Distribution = DistUniformDate
			fs.Params = map[string]any{
				"start": fp.Date.Start, "end": fp.Date.End,
			}
		case fp.Categorical != nil:
			vals := make([]any, len(fp.Categorical.Top))
			weights := make([]any, len(fp.Categorical.Top))
			for i, c := range fp.Categorical.Top {
				vals[i] = c.Value
				weights[i] = c.Weight
			}
			fs.Distribution = DistWeightedCategorical
			fs.Params = map[string]any{"values": vals, "weights": weights}
		case fp.Numeric != nil && fp.Numeric.Shape != nil:
			// Captured shape (--fit-shape, E4-S2) takes precedence over
			// the plain normal reconstruction below — it exists
			// precisely because BIC judged it a genuine improvement at
			// capture time (fitNumericShape). Reuses DistMixture (E4-S1)
			// verbatim: no new sampling logic, just the captured
			// means/stds/weights handed straight through as its params.
			// NOTE: DistMixture declares no min/max clamp params
			// (unlike DistNormal above), so a shape-fitted field's
			// independent draw is unbounded; a MODELLED one takes its
			// bound from FieldModelSpec.Min/Max instead (see
			// modelSpecFromProfile).
			//
			// Conditioning: since E4-S1 a shape-fitted field DOES accept
			// a captured linear model — the mixture becomes Q in
			// value = Q(Phi(mu(row) + sigma*z)) and the predictors shift
			// the latent, so shape and conditioning compose rather than
			// one silently deleting the other. A shape-fitted field with
			// no model still keeps its independent marginal and a
			// captured categorical-numeric pair naming it is still
			// excluded by resolveConflicts' captured-shape pre-claim,
			// with a warning; that half is unchanged.
			means := make([]any, len(fp.Numeric.Shape.Means))
			stds := make([]any, len(fp.Numeric.Shape.Stds))
			weights := make([]any, len(fp.Numeric.Shape.Weights))
			for i := range fp.Numeric.Shape.Means {
				means[i] = fp.Numeric.Shape.Means[i]
				stds[i] = fp.Numeric.Shape.Stds[i]
				weights[i] = fp.Numeric.Shape.Weights[i]
			}
			fs.Distribution = DistMixture
			fs.Params = map[string]any{"means": means, "stds": stds, "weights": weights}
		case fp.Numeric != nil:
			fs.Distribution = DistNormal
			fs.Params = map[string]any{
				"mean": fp.Numeric.Mean,
				"std":  math.Max(fp.Numeric.Std, 1e-9),
				"min":  fp.Numeric.Min,
				"max":  fp.Numeric.Max,
			}
		default:
			// Field type the profiler couldn't summarize. Use a constant
			// of 0 so synth still produces a record-shaped output.
			fs.Distribution = DistConstant
			fs.Params = map[string]any{"value": float64(0)}
		}
		s.Fields = append(s.Fields, fs)
	}
	// Drop correlations whose endpoints are not numeric in the
	// reconstructed spec (the copula only operates on numeric fields).
	//
	// Prefer the row-aligned p.Conditional.NumericPairs (captured only
	// when --conditional was set, with an accurate co-occurrence N)
	// over the plain p.Pairwise stats; fall back to Pairwise when no
	// Conditional section is present so a profile captured without
	// --conditional — including every document from before this
	// section existed — still reconstructs its best-effort correlation
	// exactly as before. Both funnel into the same Spec.Correlations
	// shape: synth/copula.go's conditional-Gaussian reconstruction
	// applies identically either way, so this choice affects only
	// which Rho/pair-set gets used, never how it is reconstructed.
	typeOf := make(map[string]string, len(s.Fields))
	distOf := make(map[string]string, len(s.Fields))
	for _, fs := range s.Fields {
		typeOf[fs.Name] = fs.Type
		distOf[fs.Name] = fs.Distribution
	}
	addCorrelation := func(a, b string, rho float64) {
		if !isNumericFieldType(typeOf[a]) || !isNumericFieldType(typeOf[b]) {
			return
		}
		// A --fit-shape field that reconstructed to DistMixture (E4-S2)
		// does not participate in numeric-numeric correlation
		// reconstruction — not for want of moments (fieldMoments gained
		// exact mixture moments at E4-S1) but because resolveConflicts
		// pre-claims an unmodelled shape-fitted field, and a MODELLED
		// one is excluded from the value-scale matrix permanently — its
		// correlation structure rides Spec.ResidualCorrelations
		// instead. This used to be excluded here directly (a
		// silent drop); it is added unconditionally now and left to
		// resolveConflicts (E6-S1,
		// synth/conflict.go) at generate() setup time — a shape-fit field
		// is pre-claimed there under "captured shape (--fit-shape)" before
		// any correlation stage runs, so the outcome (the correlation
		// still excludes that field, the rest of the matrix still
		// correlates) is unchanged, but it now surfaces as a warning
		// through generate()'s shared conflict-detection mechanism instead
		// of vanishing without a trace.
		s.Correlations = append(s.Correlations, CorrelationSpec{A: a, B: b, Correlation: rho})
	}
	if p.Conditional != nil && len(p.Conditional.NumericPairs) > 0 {
		for _, c := range p.Conditional.NumericPairs {
			addCorrelation(c.A, c.B, c.Rho)
		}
	} else {
		for _, c := range p.Pairwise {
			addCorrelation(c.A, c.B, c.Rho)
		}
	}

	// Categorical-categorical and categorical-numeric joint structure:
	// analogous wiring to the numeric-numeric correlation above, but with
	// no Pairwise-style fallback — CategoricalPairs/CategoricalNumericPairs
	// only ever exist under ConditionalProfile, captured by --conditional.
	// Both drop unless BOTH sides of the pair reconstructed to the
	// expected distribution kind (weighted_categorical for a categorical
	// axis, normal for a numeric axis) in THIS spec — defensive against a
	// field the profiler could not summarize (falls back to `constant` in
	// the switch above) still being named in a stale/foreign profile
	// document's Conditional section.
	numericMoments := make(map[string]NumericProfile, len(p.Fields))
	for _, fp := range p.Fields {
		if fp.Numeric != nil {
			numericMoments[fp.Name] = *fp.Numeric
		}
	}

	// modelled names the numeric targets that actually landed a model on
	// the Spec — not the targets the DOCUMENT carries a model for. The
	// difference is the whole of the per-target retirement rule
	// described above: a captured model that modelSpecFromProfile
	// refuses to apply must leave its field's conditional pair standing,
	// because the alternative is a field reconstructed from nothing at
	// all.
	modelled := make(map[string]bool, len(p.Models))
	var modelWarnings []string
	for _, m := range p.Models {
		spec, why := modelSpecFromProfile(m, distOf, numericMoments)
		if why != "" {
			modelWarnings = append(modelWarnings, fmt.Sprintf(
				"model for numeric field %q not applied: %s", m.Field, why))
			continue
		}
		s.Models = append(s.Models, spec)
		modelled[spec.Field] = true
	}

	// Residual correlation structure among the models that actually
	// landed (`profile create --residual-correlations`). Only MEASURED
	// pairs are translated, and only when both endpoints kept a model on
	// this Spec.
	//
	// The document's `unmeasured` list is deliberately NOT translated
	// into anything. It exists so a reader can tell a pair measured at
	// rho = 0 from a pair nobody could measure, and the generator's
	// completion policy already treats an absent pair as an assumption
	// it names out loud (factorCorrelations, synth/copula.go). Writing
	// an unmeasured pair out as a zero here would collapse exactly the
	// distinction the capture side was built to preserve, and would do
	// it silently, since a supplied zero is counted as measured.
	//
	// A pair whose endpoint lost its model needs no warning of its own:
	// the model drop was already reported above, by field, with its
	// reason — and buildResidualCorrelator names the consequence again
	// at generate() time for a spec that reaches it with an unmodelled
	// endpoint by some other route.
	if p.ResidualCorrelations != nil {
		for _, rc := range p.ResidualCorrelations.Pairs {
			if !modelled[rc.A] || !modelled[rc.B] {
				continue
			}
			s.ResidualCorrelations = append(s.ResidualCorrelations,
				CorrelationSpec{A: rc.A, B: rc.B, Correlation: rc.Rho})
		}
	}

	if p.Conditional != nil {
		// distOf and numericMoments were already built above — reused
		// here rather than rebuilt.
		for _, cp := range p.Conditional.CategoricalPairs {
			if distOf[cp.A] != DistWeightedCategorical || distOf[cp.B] != DistWeightedCategorical {
				continue
			}
			cells := make([]CategoricalPairCellSpec, len(cp.Cells))
			for i, c := range cp.Cells {
				cells[i] = CategoricalPairCellSpec(c)
			}
			s.CategoricalPairs = append(s.CategoricalPairs, CategoricalPairSpec{A: cp.A, B: cp.B, Cells: cells})
		}
		for _, cnp := range p.Conditional.CategoricalNumericPairs {
			if modelled[cnp.B] {
				// Retired for THIS target: its model already carries
				// every predictor the pairs could contribute, all at
				// once. A target whose model was captured but not
				// applied keeps its pair — see the doc comment above for
				// why the retirement is per field and not per document.
				continue
			}
			// distOf[cnp.B] == DistMixture (a --fit-shape reconstruction,
			// E4-S2) is deliberately ALLOWED through here rather than
			// filtered out — categoricalNumericPairSampler.transform
			// (synth/conditional_sample.go) overwrites row[B] outright
			// with its own captured per-category moments regardless of
			// what B's independent sampler would have drawn, so it has no
			// functional need for B's own distribution to be normal.
			// Whether this pair actually gets to run is decided uniformly
			// by resolveConflicts (E6-S1, synth/conflict.go) at generate()
			// setup time: a shape-fit B is pre-claimed there, so the pair
			// is excluded with an explicit warning. (Only an UNMODELLED
			// shape-fit B can reach this line at all — a modelled one was
			// retired by the `modelled[cnp.B]` check above, since E4-S1
			// lets a shape-fitted field carry a model — so the pre-claim
			// is still what decides it.) That replaces what used
			// to be this loop's own silent "shape fit wins" special case
			// (distOf[cnp.B] != DistNormal) with the SAME shared mechanism
			// every other conditional-relationship conflict routes through.
			// Any OTHER non-normal, non-mixture reconstruction (e.g. the
			// DistConstant fallback for a field the profiler could not
			// summarize) still excludes the pair here — that is a data-
			// integrity guard, not a claim conflict.
			if distOf[cnp.A] != DistWeightedCategorical ||
				(distOf[cnp.B] != DistNormal && distOf[cnp.B] != DistMixture) {
				continue
			}
			num, ok := numericMoments[cnp.B]
			if !ok {
				continue
			}
			cats := make([]CategoricalNumericCategorySpec, len(cnp.Categories))
			for i, c := range cnp.Categories {
				cats[i] = CategoricalNumericCategorySpec{Category: c.Category, Mean: c.Mean, Std: c.Std}
			}
			s.CategoricalNumericPairs = append(s.CategoricalNumericPairs, CategoricalNumericPairSpec{
				A: cnp.A, B: cnp.B, Categories: cats,
				Min: num.Min, Max: num.Max, HasClamp: true,
			})
		}

		// Set-option joint structure (E5-S3): the same "both sides must
		// have reconstructed to the expected distribution kind" guard,
		// with the set axis fixed to DistSetBernoulli.
		for _, scp := range p.Conditional.SetCategoricalPairs {
			if distOf[scp.Set] != DistSetBernoulli || distOf[scp.Categorical] != DistWeightedCategorical {
				continue
			}
			cells := make([]CategoricalPairCellSpec, len(scp.Cells))
			for i, c := range scp.Cells {
				cells[i] = CategoricalPairCellSpec(c)
			}
			s.SetCategoricalPairs = append(s.SetCategoricalPairs, SetCategoricalPairSpec{
				Set: scp.Set, Option: scp.Option, Categorical: scp.Categorical, Cells: cells,
			})
		}
		for _, snp := range p.Conditional.SetNumericPairs {
			if modelled[snp.Numeric] {
				// Retired for THIS target, exactly as
				// CategoricalNumericPairs above.
				continue
			}
			if distOf[snp.Set] != DistSetBernoulli || distOf[snp.Numeric] != DistNormal {
				continue
			}
			num, ok := numericMoments[snp.Numeric]
			if !ok {
				continue
			}
			cats := make([]CategoricalNumericCategorySpec, len(snp.Categories))
			for i, c := range snp.Categories {
				cats[i] = CategoricalNumericCategorySpec{Category: c.Category, Mean: c.Mean, Std: c.Std}
			}
			s.SetNumericPairs = append(s.SetNumericPairs, SetNumericPairSpec{
				Set: snp.Set, Option: snp.Option, Numeric: snp.Numeric, Categories: cats,
				Min: num.Min, Max: num.Max, HasClamp: true,
			})
		}
		for _, ssp := range p.Conditional.SetSetPairs {
			if distOf[ssp.SetA] != DistSetBernoulli || distOf[ssp.SetB] != DistSetBernoulli {
				continue
			}
			cells := make([]CategoricalPairCellSpec, len(ssp.Cells))
			for i, c := range ssp.Cells {
				cells[i] = CategoricalPairCellSpec(c)
			}
			s.SetSetPairs = append(s.SetSetPairs, SetSetPairSpec{
				SetA: ssp.SetA, OptionA: ssp.OptionA, SetB: ssp.SetB, OptionB: ssp.OptionB, Cells: cells,
			})
		}
	}
	conflicts := resolveConflicts(s)
	// Model-drop warnings ride the same channel as conflict warnings and
	// come FIRST: a dropped model is why a numeric field fell back to
	// its independent marginal, and reading that after the conflicts it
	// prevented would invert the causal order. They are worded
	// distinctly ("not applied") so neither kind can be mistaken for the
	// other by a reader or by a test matching on the message.
	if len(modelWarnings) > 0 {
		return s, append(modelWarnings, conflicts.warnings...)
	}
	return s, conflicts.warnings
}

// modelSpecFromProfile translates one captured FieldModel into its
// generation-facing FieldModelSpec, or returns the reason it cannot be
// applied to THIS reconstructed spec.
//
// The guards mirror the "both sides must have reconstructed to the
// expected distribution kind" rule the conditional pair wiring above
// already applies, with one difference that follows from the shape: a
// pair is one relationship and can be dropped on its own, whereas a
// model is a single expression whose Intercept is defined relative to
// every one of its predictors at once. Dropping a predictor from it
// would leave the remaining coefficients being read against a baseline
// that no longer exists — silently biased rather than merely poorer —
// so an unusable predictor takes the whole model with it and says so.
func modelSpecFromProfile(m FieldModel, distOf map[string]string, moments map[string]NumericProfile) (FieldModelSpec, string) {
	dist, present := distOf[m.Field]
	if !present {
		return FieldModelSpec{}, "target field is not present in the profile"
	}
	switch dist {
	case DistNormal:
	case DistMixture:
		// A --fit-shape reconstruction. This USED to be a refusal — the
		// mixture had no quantile function a linear predictor could ride
		// — and refusing it here was the upstream half of the same
		// exclusivity resolveConflicts enforced downstream. E4-S1
		// retires both: the mixture now carries an exact (mean, std) and
		// a numerically inverted quantile (synth/mixture_quantile.go),
		// so the fitted shape simply becomes Q in
		// value = Q(Phi(mu(row) + sigma*z)) and the predictors shift the
		// latent. The field keeps its shape AND gains its conditioning,
		// which is the whole point: the refusal was silently deleting
		// every measured relationship on precisely the fields whose
		// distributions were interesting enough to fit.
		//
		// The effect is on the LATENT scale and therefore non-linear in
		// value space for a non-normal Q — a coefficient is not "this
		// many units of the field" here. See synth/mixture_quantile.go.
	default:
		return FieldModelSpec{}, "target did not reconstruct to a normal distribution"
	}
	num, ok := moments[m.Field]
	if !ok {
		return FieldModelSpec{}, "target carries no numeric summary"
	}

	out := FieldModelSpec{
		Field:       m.Field,
		Intercept:   m.Intercept,
		ResidualStd: m.ResidualStd,
		Min:         num.Min,
		Max:         num.Max,
		HasClamp:    true,
		Predictors:  make([]ModelPredictorSpec, 0, len(m.Predictors)),
	}
	for _, pr := range m.Predictors {
		want := ""
		switch pr.Kind {
		case ModelPredictorCategoricalLevel:
			want = DistWeightedCategorical
		case ModelPredictorSetOption:
			want = DistSetBernoulli
		default:
			return FieldModelSpec{}, fmt.Sprintf("predictor %q carries unsupported kind %q", pr.Field, pr.Kind)
		}
		got, present := distOf[pr.Field]
		if !present {
			return FieldModelSpec{}, fmt.Sprintf(
				"predictor field %q is not present in the profile", pr.Field)
		}
		if got != want {
			return FieldModelSpec{}, fmt.Sprintf(
				"predictor field %q reconstructed as %q, not %q", pr.Field, got, want)
		}
		out.Predictors = append(out.Predictors, ModelPredictorSpec{
			Kind:        pr.Kind,
			Field:       pr.Field,
			Level:       pr.Level,
			Coefficient: pr.Coefficient,
		})
	}
	if len(out.Predictors) == 0 {
		return FieldModelSpec{}, "model carries no predictors"
	}
	return out, ""
}
