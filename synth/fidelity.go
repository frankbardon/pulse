package synth

import (
	"bytes"
	"fmt"
	"math"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

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
type FieldFidelity struct {
	Field  string            `json:"field"`
	Test   types.TestType    `json:"test"`
	Result *types.TestResult `json:"result,omitempty"`
	Error  string            `json:"error,omitempty"`
}

// PairwiseFidelity is one row of FidelityReport.Pairwise: the delta
// between a captured numeric-numeric pair's source correlation (the
// same rho synth/copula.go's conditional-Gaussian reconstruction was
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
type PairwiseFidelity struct {
	A            string  `json:"a"`
	B            string  `json:"b"`
	SourceRho    float64 `json:"source_rho"`
	SyntheticRho float64 `json:"synthetic_rho,omitempty"`
	// Delta is abs(SourceRho - SyntheticRho). Zero-valued and
	// meaningless when Error is set.
	Delta float64 `json:"delta,omitempty"`
	// N is the number of _synthetic=true rows with both A and B
	// simultaneously non-null — the realized pair's own co-occurrence
	// count, distinct from (and typically much larger than) the source
	// capture's own N.
	N int `json:"n"`
	// Error is set instead of SyntheticRho/Delta when the synthetic
	// partition could not produce a defined correlation for this pair
	// (fewer than two co-occurring non-null observations) — mirrors
	// FieldFidelity's own per-field failure contract: one pair's
	// failure never withholds the rest of the report.
	Error string `json:"error,omitempty"`
}

// CategoricalPairFidelity is one row of
// FidelityReport.CategoricalPairwise: the delta between a captured
// categorical-categorical pair's source joint-frequency table
// (CategoricalPairSpec.Cells — the same contingency cells
// synth/conditional_sample.go's categoricalPairSampler was built from)
// and that pair's REALIZED joint-frequency table over the
// _synthetic=true partition of the output cohort, expressed as total
// variation distance (see categoricalTVD) — the categorical-pair
// analogue of PairwiseFidelity's numeric-numeric correlation delta.
type CategoricalPairFidelity struct {
	A string `json:"a"`
	B string `json:"b"`
	// Delta is the total variation distance between the source and
	// synthetic joint-frequency tables (each normalized to proportions
	// before comparison), in [0, 1] — 0 when the two tables carry
	// identical support and proportions, 1 when they share none. Chosen
	// over a chi-square-style distance because it stays well-defined and
	// boundedly comparable to a fixed tolerance even when synthesis
	// realizes a cell the capture never saw (or vice versa) — a
	// chi-square statistic diverges when an expected cell is zero. Zero
	// valued and meaningless when Error is set.
	Delta float64 `json:"delta,omitempty"`
	// N is the number of _synthetic=true rows with both A and B
	// simultaneously non-null and dictionary-resolvable — the realized
	// pair's own co-occurrence count. CategoricalPairSpec carries no
	// source N of its own; the source total used to normalize its side
	// of the comparison is recovered by summing Cells' Count.
	N int `json:"n"`
	// Error is set instead of Delta when the synthetic partition
	// produced no co-occurring observation at all for this pair — mirrors
	// PairwiseFidelity's own per-pair non-fatal failure contract.
	Error string `json:"error,omitempty"`
}

// CategoricalNumericPairFidelity is one row of
// FidelityReport.CategoricalNumericPairwise: the per-category delta
// between a captured categorical-numeric pair's source conditional
// mean/std (CategoricalNumericPairSpec.Categories — the same figures
// synth/conditional_sample.go's categoricalNumericPairSampler draws
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
type CategoricalNumericPairFidelity struct {
	A          string                                `json:"a"`
	B          string                                `json:"b"`
	Categories []*CategoricalNumericCategoryFidelity `json:"categories,omitempty"`
}

// CategoricalNumericCategoryFidelity is one observed category's
// conditional mean/std delta — the categorical-numeric analogue of
// PairwiseFidelity, one entry per category rather than one entry per
// pair, since a categorical-numeric pair reconstructs a whole
// distribution per category rather than a single scalar.
type CategoricalNumericCategoryFidelity struct {
	Category string `json:"category"`
	// SourceMean/SourceStd echo the captured
	// CategoricalNumericCategorySpec verbatim.
	SourceMean float64 `json:"source_mean"`
	SourceStd  float64 `json:"source_std"`
	// SyntheticMean/SyntheticStd are the realized conditional mean/std
	// over the _synthetic=true partition. MeanDelta/StdDelta are
	// abs(source-synthetic). All four are zero-valued and meaningless
	// when Error is set.
	SyntheticMean float64 `json:"synthetic_mean,omitempty"`
	SyntheticStd  float64 `json:"synthetic_std,omitempty"`
	MeanDelta     float64 `json:"mean_delta,omitempty"`
	StdDelta      float64 `json:"std_delta,omitempty"`
	// N is the number of _synthetic=true rows carrying this category
	// value with a non-null B — the realized category's own observation
	// count.
	N int `json:"n"`
	// Error is set instead of the Synthetic*/*Delta fields when the
	// synthetic partition realized no observation of this category at
	// all — mirrors PairwiseFidelity's own per-entry non-fatal contract.
	Error string `json:"error,omitempty"`
}

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
type SetFieldFidelity struct {
	Field   string               `json:"field"`
	Options []*SetOptionFidelity `json:"options,omitempty"`
}

// SetOptionFidelity is one dictionary entry (bit position)'s realized
// selection-frequency comparison between the source (_synthetic=false)
// and synthetic (_synthetic=true) partitions of the output cohort —
// the set_* analogue of FieldFidelity, one entry per option rather than
// one entry per field, mirroring CategoricalNumericCategoryFidelity's
// per-category breakout for the same reason: a set_* field reconstructs
// one independent Bernoulli marginal per option rather than a single
// scalar.
type SetOptionFidelity struct {
	Value string `json:"value"`
	// SourceFrequency/SyntheticFrequency are P(bit set) over each
	// partition's non-null rows of Field. Delta is
	// abs(SourceFrequency - SyntheticFrequency). SyntheticFrequency and
	// Delta are zero-valued and meaningless when Error is set.
	SourceFrequency    float64 `json:"source_frequency"`
	SyntheticFrequency float64 `json:"synthetic_frequency,omitempty"`
	Delta              float64 `json:"delta,omitempty"`
	// N is the number of _synthetic=true rows with a non-null Field —
	// the realized synthetic partition's own denominator for this
	// option's frequency, shared across every option of the same field.
	N int `json:"n"`
	// Error is set instead of SyntheticFrequency/Delta when the
	// synthetic partition has no non-null observation of Field at all —
	// mirrors every other Fidelity entry kind's non-fatal per-entry
	// contract.
	Error string `json:"error,omitempty"`
}

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
type SetCategoricalPairFidelity struct {
	Set         string  `json:"set"`
	Option      string  `json:"option"`
	Categorical string  `json:"categorical"`
	Delta       float64 `json:"delta,omitempty"`
	// N is the number of _synthetic=true rows with both the set field
	// and the categorical field simultaneously non-null — the realized
	// pair's own co-occurrence count.
	N int `json:"n"`
	// Error is set instead of Delta when the synthetic partition
	// produced no co-occurring observation at all for this pair —
	// mirrors CategoricalPairFidelity's own per-pair contract.
	Error string `json:"error,omitempty"`
}

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
type SetNumericPairFidelity struct {
	Set        string                                `json:"set"`
	Option     string                                `json:"option"`
	Numeric    string                                `json:"numeric"`
	Categories []*CategoricalNumericCategoryFidelity `json:"categories,omitempty"`
}

// SetSetPairFidelity is one row of FidelityReport.SetSetPairwise: the
// delta between a captured option x option pair's source 2x2
// contingency table (SetSetPairSpec.Cells) between two DIFFERENT set_*
// fields, and that pair's REALIZED 2x2 contingency table over the
// _synthetic=true partition — the cross-field-option analogue of
// CategoricalPairFidelity, with both axes fixed to their own option's
// {"selected","not_selected"} domain.
type SetSetPairFidelity struct {
	SetA    string  `json:"set_a"`
	OptionA string  `json:"option_a"`
	SetB    string  `json:"set_b"`
	OptionB string  `json:"option_b"`
	Delta   float64 `json:"delta,omitempty"`
	// N is the number of _synthetic=true rows with both set fields
	// simultaneously non-null — the realized pair's own co-occurrence
	// count.
	N int `json:"n"`
	// Error is set instead of Delta when the synthetic partition
	// produced no co-occurring observation at all for this pair.
	Error string `json:"error,omitempty"`
}

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
type FidelityReport struct {
	SourceRows                 int                               `json:"source_rows"`
	SyntheticRows              int                               `json:"synthetic_rows"`
	Fields                     []*FieldFidelity                  `json:"fields,omitempty"`
	Pairwise                   []*PairwiseFidelity               `json:"pairwise,omitempty"`
	CategoricalPairwise        []*CategoricalPairFidelity        `json:"categorical_pairwise,omitempty"`
	CategoricalNumericPairwise []*CategoricalNumericPairFidelity `json:"categorical_numeric_pairwise,omitempty"`
	SetFields                  []*SetFieldFidelity               `json:"set_fields,omitempty"`
	SetCategoricalPairwise     []*SetCategoricalPairFidelity     `json:"set_categorical_pairwise,omitempty"`
	SetNumericPairwise         []*SetNumericPairFidelity         `json:"set_numeric_pairwise,omitempty"`
	SetSetPairwise             []*SetSetPairFidelity             `json:"set_set_pairwise,omitempty"`
	// Models is the per-field MODEL-RECOVERY section (E5-S1): for every
	// linear model generation actually applied, the captured coefficients
	// beside the ones a refit on the generated partition recovers. It
	// answers a different question from every section above — those ask
	// whether the generated rows LOOK like the source, this asks whether
	// the captured STRUCTURE survived generation — and it is the only
	// section that can tell a faithful generation whose marginal
	// contrasts are merely compressed apart from one that silently lost
	// its conditioning. Absent (omitempty) for every spec carrying no
	// `models`, which is every spec predating `profile create
	// --fit-models`. See synth/fidelity_models.go.
	Models []*ModelFidelity `json:"models,omitempty"`
	// ModelResidualCorrelations is the residual-correlation half of the
	// same model-recovery question (E5-S2): for every residual
	// correlation generation applied, the captured rho beside the rho
	// recovered from the generated partition. It is deliberately NOT
	// folded into Pairwise, which scores the value-scale copula arm over
	// a DISJOINT set of fields — a modelled field is excluded from
	// Spec.Correlations by resolveConflicts precisely so the two arms
	// never both fire on one field. The two section names are what tell
	// a reader scanning this document which mechanism a given number
	// describes. Absent (omitempty) for every spec carrying no
	// `residual_correlations`, which is every spec predating `profile
	// create --residual-correlations`. See synth/fidelity_residual.go.
	ModelResidualCorrelations *ModelResidualCorrelationFidelity `json:"model_residual_correlations,omitempty"`
	Warnings                  []string                          `json:"warnings,omitempty"`
}

// TestRunner executes a single statistical Test against an encoded
// record buffer and returns its result. BuildFidelityReport calls it
// once per eligible field rather than reimplementing KS/chi-square
// math itself.
//
// This package cannot import processing directly to provide its own
// implementation: descriptor imports synth (for the built-in
// distribution registry), and processing's own internal test files
// import descriptor — synth importing processing back would close
// that cycle. pulse.go, which already imports both synth and
// processing, supplies the real implementation and is the only
// intended caller of BuildFidelityReport.
//
// physicalSchema is records' actual on-wire layout and MUST be used to
// decode it. presentedSchema is a view schema — same fields, same
// order, but with SyntheticFieldName's type widened from packed_bool
// to categorical_u8 (see SyntheticAsCategoricalSchema) — that the
// implementation must attach to each decoded record for type and
// dictionary lookups instead of physicalSchema. The two diverge only
// so the provenance column can satisfy TEST_KS.SplitBy / TEST_CHISQ's
// categorical requirement without changing the on-wire byte format.
type TestRunner func(physicalSchema, presentedSchema *encoding.Schema, records []byte, test *types.Test) (*types.TestResult, error)

// BuildFidelityReport compares every eligible field of mergedSchema
// (skipping SyntheticFieldName itself) between the _synthetic=false
// and _synthetic=true partitions of records: TEST_KS for numeric
// fields, TEST_CHISQ for categorical fields, both driven through run;
// a direct per-option frequency comparison (computeSetFieldFidelity,
// no run/TestRunner involved) for set_* fields, appended onto
// report.SetFields instead of report.Fields (E5-S4 — see
// SetFieldFidelity for why this section does not reuse TEST_KS/
// TEST_CHISQ). Fields of any other on-wire type (date, datetime,
// packed_bool, u4) are out of scope for both sections; later stories
// may widen coverage.
//
// A single field's failing test (entry.Error set, entry.Result nil)
// never aborts the rest of the report — run is called once per field
// on its own Test, not once for every field on a shared Request.
func BuildFidelityReport(mergedSchema *encoding.Schema, records []byte, sourceRows, syntheticRows int, run TestRunner) *FidelityReport {
	presented := SyntheticAsCategoricalSchema(mergedSchema)

	report := &FidelityReport{SourceRows: sourceRows, SyntheticRows: syntheticRows}
	for i := range mergedSchema.Fields {
		f := &mergedSchema.Fields[i]
		if f.Name == SyntheticFieldName {
			continue
		}

		if f.Type.IsSet() {
			report.SetFields = append(report.SetFields, computeSetFieldFidelity(mergedSchema, records, f.Name))
			continue
		}

		var test *types.Test
		switch {
		case f.Type.IsNumeric():
			test = &types.Test{Type: types.TEST_KS, Field: f.Name, SplitBy: SyntheticFieldName}
		case f.Type.IsCategorical():
			test = &types.Test{Type: types.TEST_CHISQ, Rows: f.Name, Cols: SyntheticFieldName}
		default:
			continue
		}

		entry := &FieldFidelity{Field: f.Name, Test: test.Type}
		result, err := run(mergedSchema, presented, records, test)
		if err != nil {
			entry.Error = err.Error()
		} else {
			entry.Result = result
		}
		report.Fields = append(report.Fields, entry)
	}
	return report
}

// computeSetFieldFidelity computes fieldName's per-option marginal
// selection-frequency comparison directly (no TestRunner) between the
// _synthetic=false (source) and _synthetic=true (synthetic) partitions
// of mergedSchema's records, in a single decode pass — the same
// wide[fieldName].(uint64) mask-reading discipline
// synth/profile.go's marginal/joint capture already uses. Every
// dictionary entry within fieldName's type's MaxSetEntries bound gets
// an entry, in bit/dictionary order, mirroring SetProfile.Options'
// own ordering contract.
func computeSetFieldFidelity(mergedSchema *encoding.Schema, records []byte, fieldName string) *SetFieldFidelity {
	entry := &SetFieldFidelity{Field: fieldName}
	f := mergedSchema.Field(fieldName)
	if f == nil || f.Dictionary == nil {
		return entry
	}
	n := f.Dictionary.Count()
	if max := int(f.Type.MaxSetEntries()); n > max {
		n = max
	}
	names := make([]string, n)
	for i := 0; i < n; i++ {
		names[i] = f.Dictionary.Resolve(uint32(i))
	}

	srcSel := make([]int, n)
	synSel := make([]int, n)
	var srcN, synN int

	rr := encoding.NewRecordReader(bytes.NewReader(records), mergedSchema)
	values := make(map[string]float64, len(mergedSchema.Fields))
	nulls := make(map[string]bool, len(mergedSchema.Fields))
	wide := make(map[string]any, len(mergedSchema.Fields))
	for {
		if err := rr.ReadRecordWithWide(values, nulls, wide); err != nil {
			break
		}
		if nulls[fieldName] {
			continue
		}
		synthetic := values[SyntheticFieldName] != 0
		if synthetic {
			synN++
		} else {
			srcN++
		}
		mask, _ := wide[fieldName].(uint64)
		for i := 0; i < n; i++ {
			if mask&(uint64(1)<<uint(i)) == 0 {
				continue
			}
			if synthetic {
				synSel[i]++
			} else {
				srcSel[i]++
			}
		}
	}

	for i, name := range names {
		if name == "" {
			continue
		}
		opt := &SetOptionFidelity{Value: name}
		if srcN > 0 {
			opt.SourceFrequency = float64(srcSel[i]) / float64(srcN)
		}
		if synN == 0 {
			opt.Error = fmt.Sprintf("synthetic partition has no non-null observations of %s", fieldName)
		} else {
			opt.SyntheticFrequency = float64(synSel[i]) / float64(synN)
			opt.Delta = math.Abs(opt.SourceFrequency - opt.SyntheticFrequency)
			opt.N = synN
		}
		entry.Options = append(entry.Options, opt)
	}
	return entry
}

// SyntheticAsCategoricalSchema returns a copy of schema whose
// SyntheticFieldName field is redeclared as categorical_u8 with a
// two-entry dictionary ({"false","true"} at IDs 0/1 — matching
// packed_bool's 0/1 on-wire values verbatim, no value remapping).
// TEST_KS.SplitBy and TEST_CHISQ's Rows/Cols both require a
// categorical field, but _synthetic itself is packed_bool (see
// CLAUDE.md Byte-layout invariants) — this presentation-only schema
// is what lets the two unmodified operators treat it as their
// split/contingency field.
//
// It is never used to decode bytes — see TestRunner's physicalSchema
// contract — only to give a TestRunner's constructed Record something
// whose Field(SyntheticFieldName).Type reads back as categorical.
func SyntheticAsCategoricalSchema(schema *encoding.Schema) *encoding.Schema {
	fields := make([]encoding.Field, len(schema.Fields))
	copy(fields, schema.Fields)
	for i := range fields {
		if fields[i].Name != SyntheticFieldName {
			continue
		}
		dict := encoding.NewDictionary()
		_, _ = dict.Add("false")
		_, _ = dict.Add("true")
		fields[i].Type = encoding.FieldTypeCategoricalU8
		fields[i].Dictionary = dict
	}
	return &encoding.Schema{Fields: fields}
}

// BuildPairwise computes each pair's correlation delta and appends the
// resulting PairwiseFidelity entries onto report.Pairwise, then copies
// warnings verbatim onto report.Warnings. mergedSchema/records must be
// the same physical schema and record buffer BuildFidelityReport was
// given for report's Fields section — this walks that buffer a second
// time (once per pair) rather than sharing BuildFidelityReport's own
// pass, since the two sections need different per-record state (a
// per-field TestRunner request vs. a raw two-field co-occurrence scan)
// and neither needs to be online with the other.
//
// pairs is typically Spec.Correlations — the source's captured/target
// rho each pair's copula reconstruction in synth/copula.go targeted,
// via CorrelationSpec.Correlation — not a fresh re-derivation of it.
// warnings is typically Profile.Warnings (e.g. a thin-pair warning from
// --conditional capture); this function has no Profile type of its own
// to read it from, so the caller supplies it (see Options.
// FidelityWarnings, the field internal/cli's `synth from-profile`
// populates from the profile document it already has in hand).
//
// A pair whose synthetic partition cannot produce a defined
// correlation (fewer than two co-occurring non-null observations)
// gets an Error entry rather than aborting the rest of the pairs or
// the report — the same non-fatal contract BuildFidelityReport's own
// per-field entries follow.
// syntheticRow is one decoded row of mergedSchema's _synthetic=true
// partition, retained in memory by decodeSyntheticRows so every pair a
// Build*Pairwise call evaluates reads from that cache instead of
// re-decoding the full merged record set per pair.
type syntheticRow struct {
	values map[string]float64
	nulls  map[string]bool
	wide   map[string]any
}

// decodeSyntheticRows decodes mergedSchema's records exactly once,
// retaining only the rows where SyntheticFieldName is non-zero. Every
// Build*Pairwise function previously called its own compute* helper
// once per pair, and each of THOSE re-decoded the full merged record
// set (source + synthetic rows) from scratch — O(pairs × records). A
// profile with ~1,700 captured pairs over a 380k-row source cohort took
// close to an hour to fidelity-report because of it. Decoding once per
// Build*Pairwise call and handing every pair the same small retained
// partition (typically the --rows count, far smaller than the source)
// collapses that to O(records + pairs × synthetic_rows) — six full
// decodes across the whole report instead of one per pair.
func decodeSyntheticRows(mergedSchema *encoding.Schema, records []byte) []syntheticRow {
	rr := encoding.NewRecordReader(bytes.NewReader(records), mergedSchema)
	var out []syntheticRow
	for {
		values := make(map[string]float64, len(mergedSchema.Fields))
		nulls := make(map[string]bool, len(mergedSchema.Fields))
		wide := make(map[string]any, len(mergedSchema.Fields))
		if err := rr.ReadRecordWithWide(values, nulls, wide); err != nil {
			break
		}
		if values[SyntheticFieldName] == 0 {
			continue
		}
		out = append(out, syntheticRow{values: values, nulls: nulls, wide: wide})
	}
	return out
}

func BuildPairwise(report *FidelityReport, mergedSchema *encoding.Schema, records []byte, pairs []CorrelationSpec, warnings []string) {
	rows := decodeSyntheticRows(mergedSchema, records)
	for _, p := range pairs {
		entry := &PairwiseFidelity{A: p.A, B: p.B, SourceRho: p.Correlation}
		rho, n, ok := computeSyntheticPairRho(rows, p.A, p.B)
		entry.N = n
		if !ok {
			entry.Error = fmt.Sprintf(
				"synthetic partition has fewer than two co-occurring non-null observations for %s x %s", p.A, p.B)
		} else {
			entry.SyntheticRho = rho
			entry.Delta = math.Abs(rho - p.Correlation)
		}
		report.Pairwise = append(report.Pairwise, entry)
	}
	report.Warnings = append(report.Warnings, warnings...)
}

// computeSyntheticPairRho computes the realized Pearson correlation of
// fields a/b over rows (the cached _synthetic=true partition from
// decodeSyntheticRows) where both a and b are simultaneously non-null —
// the same co-occurrence discipline computeConditionalNumericPairs
// applies at capture time, applied here to the OUTPUT cohort's
// generated partition instead of a source capture. Returns ok=false
// (mirroring pearson's own NaN contract) when fewer than two such rows
// exist.
func computeSyntheticPairRho(rows []syntheticRow, a, b string) (rho float64, n int, ok bool) {
	var av, bv []float64
	for _, row := range rows {
		if row.nulls[a] || row.nulls[b] {
			continue
		}
		av = append(av, row.values[a])
		bv = append(bv, row.values[b])
	}
	rho = pearson(av, bv)
	if math.IsNaN(rho) {
		return 0, len(av), false
	}
	return rho, len(av), true
}

// BuildCategoricalPairwise computes each categorical-categorical pair's
// contingency-table delta (categoricalTVD) and appends the resulting
// CategoricalPairFidelity entries onto report.CategoricalPairwise.
// mergedSchema/records must be the same physical schema and record
// buffer BuildFidelityReport was given for report's Fields section.
//
// pairs is typically Spec.CategoricalPairs — the source's captured
// contingency cells synth/conditional_sample.go's categoricalPairSampler
// was built from, not a fresh re-derivation of it.
//
// A pair whose synthetic partition produced no co-occurring observation
// at all gets an Error entry rather than aborting the rest of the pairs
// or the report — the same non-fatal contract BuildPairwise's own
// per-pair entries follow. This function takes no warnings parameter of
// its own: thin-cell warnings for this pair kind already ride
// Profile.Warnings alongside the numeric-pair warnings BuildPairwise
// copies onto report.Warnings, so giving this function a second copy
// point would either duplicate them or require the caller to split one
// warnings slice by kind for no reason — one shared shape, one place it
// lands.
func BuildCategoricalPairwise(report *FidelityReport, mergedSchema *encoding.Schema, records []byte, pairs []CategoricalPairSpec) {
	rows := decodeSyntheticRows(mergedSchema, records)
	for _, p := range pairs {
		entry := &CategoricalPairFidelity{A: p.A, B: p.B}
		counts, n := computeSyntheticCategoricalJoint(mergedSchema, rows, p.A, p.B)
		entry.N = n
		if n == 0 {
			entry.Error = fmt.Sprintf(
				"synthetic partition has no co-occurring non-null observations for %s x %s", p.A, p.B)
		} else {
			sourceN := 0
			for _, c := range p.Cells {
				sourceN += c.Count
			}
			entry.Delta = categoricalTVD(p.Cells, sourceN, counts, n)
		}
		report.CategoricalPairwise = append(report.CategoricalPairwise, entry)
	}
}

// computeSyntheticCategoricalJoint decodes mergedSchema's records once,
// building the realized joint frequency table of categorical fields a
// and b over exactly the rows where _synthetic is true (non-zero) and
// both a and b are simultaneously non-null and dictionary-resolvable —
// the same co-occurrence discipline computeConditionalCategoricalPairs
// applies at capture time, applied here to the OUTPUT cohort's
// generated partition instead of a source capture.
func computeSyntheticCategoricalJoint(mergedSchema *encoding.Schema, rows []syntheticRow, a, b string) (counts map[[2]string]int, n int) {
	counts = make(map[[2]string]int)
	fa := mergedSchema.Field(a)
	fb := mergedSchema.Field(b)
	if fa == nil || fb == nil || fa.Dictionary == nil || fb.Dictionary == nil {
		return counts, 0
	}
	for _, row := range rows {
		if row.nulls[a] || row.nulls[b] {
			continue
		}
		av := fa.Dictionary.Resolve(uint32(row.values[a]))
		bv := fb.Dictionary.Resolve(uint32(row.values[b]))
		if av == "" || bv == "" {
			continue
		}
		counts[[2]string{av, bv}]++
		n++
	}
	return counts, n
}

// categoricalTVD computes the total variation distance between a
// pair's captured source joint-frequency table (sourceCells, normalized
// by sourceN) and its realized synthetic joint-frequency table
// (syntheticCounts, normalized by syntheticN): half the sum, over the
// union of every (a_value, b_value) cell observed on either side, of
// the absolute difference between the two sides' proportions. See
// CategoricalPairFidelity.Delta for why this metric (rather than a
// chi-square-style distance) was chosen: it stays well-defined and
// boundedly comparable to a fixed tolerance even when one side realizes
// a cell the other never saw.
func categoricalTVD(sourceCells []CategoricalPairCellSpec, sourceN int, syntheticCounts map[[2]string]int, syntheticN int) float64 {
	srcProp := make(map[[2]string]float64, len(sourceCells))
	if sourceN > 0 {
		for _, c := range sourceCells {
			srcProp[[2]string{c.AValue, c.BValue}] += float64(c.Count) / float64(sourceN)
		}
	}
	synProp := make(map[[2]string]float64, len(syntheticCounts))
	if syntheticN > 0 {
		for k, v := range syntheticCounts {
			synProp[k] = float64(v) / float64(syntheticN)
		}
	}
	seen := make(map[[2]string]bool, len(srcProp)+len(synProp))
	for k := range srcProp {
		seen[k] = true
	}
	for k := range synProp {
		seen[k] = true
	}
	// The union is accumulated in SORTED cell order, not map order, for
	// the same reason the conditional capture's top-K collapse folds
	// sorted (see computeConditionalCategoricalNumericPairs in
	// profile.go): this is a float64 sum over many addends of
	// potentially very different magnitudes, float addition is not
	// associative, and Go randomizes map iteration — so a map-order
	// fold makes the reported Delta drift in its last bits between two
	// runs over identical inputs. A fidelity number that moves when
	// nothing moved is unusable for the regression-gating this report
	// exists to support, and it moves silently.
	keys := make([][2]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	var sum float64
	for _, k := range keys {
		sum += math.Abs(srcProp[k] - synProp[k])
	}
	return 0.5 * sum
}

// BuildCategoricalNumericPairwise computes each categorical-numeric
// pair's per-category conditional mean/std delta and appends the
// resulting CategoricalNumericPairFidelity entries onto
// report.CategoricalNumericPairwise. mergedSchema/records must be the
// same physical schema and record buffer BuildFidelityReport was given.
//
// pairs is typically Spec.CategoricalNumericPairs — the source's
// captured per-category conditional mean/std
// synth/conditional_sample.go's categoricalNumericPairSampler draws
// from. A category with no realized synthetic observation gets an
// Error entry rather than aborting the rest of that pair's categories
// or the report — the same non-fatal contract every other Fidelity
// entry kind follows. As with BuildCategoricalPairwise, this function
// takes no warnings parameter: thin-cell warnings for this pair kind
// already ride Profile.Warnings through BuildPairwise's copy.
func BuildCategoricalNumericPairwise(report *FidelityReport, mergedSchema *encoding.Schema, records []byte, pairs []CategoricalNumericPairSpec) {
	rows := decodeSyntheticRows(mergedSchema, records)
	for _, p := range pairs {
		perCategory, _ := computeSyntheticConditionalNumeric(mergedSchema, rows, p.A, p.B)
		entry := &CategoricalNumericPairFidelity{A: p.A, B: p.B}
		for _, c := range p.Categories {
			catEntry := &CategoricalNumericCategoryFidelity{
				Category:   c.Category,
				SourceMean: c.Mean,
				SourceStd:  c.Std,
			}
			mom, ok := perCategory[c.Category]
			if !ok || mom.n == 0 {
				catEntry.Error = fmt.Sprintf(
					"synthetic partition has no observations of %s=%s for %s", p.A, c.Category, p.B)
			} else {
				catEntry.SyntheticMean = mom.mean
				catEntry.SyntheticStd = mom.std
				catEntry.MeanDelta = math.Abs(mom.mean - c.Mean)
				catEntry.StdDelta = math.Abs(mom.std - c.Std)
				catEntry.N = mom.n
			}
			entry.Categories = append(entry.Categories, catEntry)
		}
		report.CategoricalNumericPairwise = append(report.CategoricalNumericPairwise, entry)
	}
}

// categoricalNumericMoment is the realized conditional mean/std/count
// for one category value, computed by computeSyntheticConditionalNumeric.
type categoricalNumericMoment struct {
	mean, std float64
	n         int
}

// computeSyntheticConditionalNumeric decodes mergedSchema's records
// once, computing the realized per-category mean/std of numeric field
// numField conditioned on categorical field catField, over exactly the
// rows where _synthetic is true (non-zero) and both fields are
// simultaneously non-null and dictionary-resolvable — the same
// co-occurrence discipline computeConditionalCategoricalNumericPairs
// applies at capture time, applied here to the OUTPUT cohort's
// generated partition instead of a source capture.
func computeSyntheticConditionalNumeric(mergedSchema *encoding.Schema, rows []syntheticRow, catField, numField string) (map[string]categoricalNumericMoment, int) {
	out := make(map[string]categoricalNumericMoment)
	fa := mergedSchema.Field(catField)
	if fa == nil || fa.Dictionary == nil {
		return out, 0
	}
	type acc struct {
		count      int
		sum, sumSq float64
	}
	accs := make(map[string]*acc)
	total := 0
	for _, row := range rows {
		if row.nulls[catField] || row.nulls[numField] {
			continue
		}
		catVal := fa.Dictionary.Resolve(uint32(row.values[catField]))
		if catVal == "" {
			continue
		}
		a := accs[catVal]
		if a == nil {
			a = &acc{}
			accs[catVal] = a
		}
		v := row.values[numField]
		a.count++
		a.sum += v
		a.sumSq += float64(v * v)
		total++
	}
	for catVal, a := range accs {
		mean := a.sum / float64(a.count)
		variance := sampleVariance(a.count, a.sum, a.sumSq)
		out[catVal] = categoricalNumericMoment{mean: mean, std: math.Sqrt(variance), n: a.count}
	}
	return out, total
}

// BuildSetCategoricalPairwise computes each set-option x categorical
// pair's contingency-table delta (categoricalTVD, reused verbatim from
// BuildCategoricalPairwise — SetCategoricalPairSpec.Cells is the same
// []CategoricalPairCellSpec shape) and appends the resulting
// SetCategoricalPairFidelity entries onto report.SetCategoricalPairwise
// (E5-S4). mergedSchema/records must be the same physical schema and
// record buffer BuildFidelityReport was given.
//
// pairs is typically Spec.SetCategoricalPairs — the source's captured
// contingency cells synth/conditional_sample.go's
// setCategoricalPairSampler was built from. A pair whose synthetic
// partition produced no co-occurring observation at all gets an Error
// entry rather than aborting the rest of the pairs or the report — the
// same non-fatal contract BuildCategoricalPairwise's own per-pair
// entries follow.
func BuildSetCategoricalPairwise(report *FidelityReport, mergedSchema *encoding.Schema, records []byte, pairs []SetCategoricalPairSpec) {
	rows := decodeSyntheticRows(mergedSchema, records)
	for _, p := range pairs {
		entry := &SetCategoricalPairFidelity{Set: p.Set, Option: p.Option, Categorical: p.Categorical}
		counts, n := computeSyntheticSetOptionCategoricalJoint(mergedSchema, rows, p.Set, p.Option, p.Categorical)
		entry.N = n
		if n == 0 {
			entry.Error = fmt.Sprintf(
				"synthetic partition has no co-occurring non-null observations for %s[%s] x %s", p.Set, p.Option, p.Categorical)
		} else {
			sourceN := 0
			for _, c := range p.Cells {
				sourceN += c.Count
			}
			entry.Delta = categoricalTVD(p.Cells, sourceN, counts, n)
		}
		report.SetCategoricalPairwise = append(report.SetCategoricalPairwise, entry)
	}
}

// computeSyntheticSetOptionCategoricalJoint decodes mergedSchema's
// records once, building the realized joint frequency table of set
// field setField's option (bit position) — expressed as
// "selected"/"not_selected" — against categorical field catField's
// resolved value, over exactly the rows where _synthetic is true
// (non-zero) and both fields are simultaneously non-null and
// dictionary-resolvable. Mirrors computeSyntheticCategoricalJoint's
// discipline, applied to a set-option axis instead of an arbitrary
// categorical field. Returns n=0 (and no counts) when setField carries
// no dictionary entry named option.
func computeSyntheticSetOptionCategoricalJoint(mergedSchema *encoding.Schema, rows []syntheticRow, setField, option, catField string) (counts map[[2]string]int, n int) {
	counts = make(map[[2]string]int)
	fs := mergedSchema.Field(setField)
	fc := mergedSchema.Field(catField)
	if fs == nil || fs.Dictionary == nil || fc == nil || fc.Dictionary == nil {
		return counts, 0
	}
	optIdx, ok := fs.Dictionary.IDFor(option)
	if !ok {
		return counts, 0
	}
	for _, row := range rows {
		if row.nulls[setField] || row.nulls[catField] {
			continue
		}
		cv := fc.Dictionary.Resolve(uint32(row.values[catField]))
		if cv == "" {
			continue
		}
		mask, _ := row.wide[setField].(uint64)
		sel := "not_selected"
		if mask&(uint64(1)<<optIdx) != 0 {
			sel = "selected"
		}
		counts[[2]string{sel, cv}]++
		n++
	}
	return counts, n
}

// BuildSetNumericPairwise computes each set-option x numeric pair's
// per-bucket conditional mean/std delta and appends the resulting
// SetNumericPairFidelity entries onto report.SetNumericPairwise
// (E5-S4). mergedSchema/records must be the same physical schema and
// record buffer BuildFidelityReport was given.
//
// pairs is typically Spec.SetNumericPairs. A bucket with no realized
// synthetic observation gets an Error entry rather than aborting the
// rest of that pair's categories or the report — the same non-fatal
// contract BuildCategoricalNumericPairwise's own entries follow.
func BuildSetNumericPairwise(report *FidelityReport, mergedSchema *encoding.Schema, records []byte, pairs []SetNumericPairSpec) {
	rows := decodeSyntheticRows(mergedSchema, records)
	for _, p := range pairs {
		perBucket, _ := computeSyntheticSetOptionConditionalNumeric(mergedSchema, rows, p.Set, p.Option, p.Numeric)
		entry := &SetNumericPairFidelity{Set: p.Set, Option: p.Option, Numeric: p.Numeric}
		for _, c := range p.Categories {
			catEntry := &CategoricalNumericCategoryFidelity{
				Category:   c.Category,
				SourceMean: c.Mean,
				SourceStd:  c.Std,
			}
			mom, ok := perBucket[c.Category]
			if !ok || mom.n == 0 {
				catEntry.Error = fmt.Sprintf(
					"synthetic partition has no observations of %s[%s]=%s for %s", p.Set, p.Option, c.Category, p.Numeric)
			} else {
				catEntry.SyntheticMean = mom.mean
				catEntry.SyntheticStd = mom.std
				catEntry.MeanDelta = math.Abs(mom.mean - c.Mean)
				catEntry.StdDelta = math.Abs(mom.std - c.Std)
				catEntry.N = mom.n
			}
			entry.Categories = append(entry.Categories, catEntry)
		}
		report.SetNumericPairwise = append(report.SetNumericPairwise, entry)
	}
}

// computeSyntheticSetOptionConditionalNumeric decodes mergedSchema's
// records once, computing the realized conditional mean/std of numeric
// field numField, bucketed by whether set field setField's option (bit
// position) was selected — keyed "selected"/"not_selected", mirroring
// SetNumericPairProfile.Categories' own bucket keys — over exactly the
// rows where _synthetic is true (non-zero) and both fields are
// simultaneously non-null. Mirrors computeSyntheticConditionalNumeric's
// discipline, applied to a set-option axis instead of an arbitrary
// categorical field. Returns no buckets when setField carries no
// dictionary entry named option.
func computeSyntheticSetOptionConditionalNumeric(mergedSchema *encoding.Schema, rows []syntheticRow, setField, option, numField string) (map[string]categoricalNumericMoment, int) {
	out := make(map[string]categoricalNumericMoment)
	fs := mergedSchema.Field(setField)
	if fs == nil || fs.Dictionary == nil {
		return out, 0
	}
	optIdx, ok := fs.Dictionary.IDFor(option)
	if !ok {
		return out, 0
	}
	type acc struct {
		count      int
		sum, sumSq float64
	}
	accs := make(map[string]*acc, 2)
	total := 0
	for _, row := range rows {
		if row.nulls[setField] || row.nulls[numField] {
			continue
		}
		mask, _ := row.wide[setField].(uint64)
		sel := "not_selected"
		if mask&(uint64(1)<<optIdx) != 0 {
			sel = "selected"
		}
		a := accs[sel]
		if a == nil {
			a = &acc{}
			accs[sel] = a
		}
		v := row.values[numField]
		a.count++
		a.sum += v
		a.sumSq += float64(v * v)
		total++
	}
	for sel, a := range accs {
		mean := a.sum / float64(a.count)
		variance := sampleVariance(a.count, a.sum, a.sumSq)
		out[sel] = categoricalNumericMoment{mean: mean, std: math.Sqrt(variance), n: a.count}
	}
	return out, total
}

// BuildSetSetPairwise computes each option x option pair's 2x2
// contingency-table delta between two DIFFERENT set_* fields
// (categoricalTVD, reused verbatim — SetSetPairSpec.Cells is the same
// []CategoricalPairCellSpec shape) and appends the resulting
// SetSetPairFidelity entries onto report.SetSetPairwise (E5-S4).
// mergedSchema/records must be the same physical schema and record
// buffer BuildFidelityReport was given.
//
// pairs is typically Spec.SetSetPairs. A pair whose synthetic partition
// produced no co-occurring observation at all gets an Error entry
// rather than aborting the rest of the pairs or the report.
func BuildSetSetPairwise(report *FidelityReport, mergedSchema *encoding.Schema, records []byte, pairs []SetSetPairSpec) {
	rows := decodeSyntheticRows(mergedSchema, records)
	for _, p := range pairs {
		entry := &SetSetPairFidelity{SetA: p.SetA, OptionA: p.OptionA, SetB: p.SetB, OptionB: p.OptionB}
		counts, n := computeSyntheticSetSetJoint(mergedSchema, rows, p.SetA, p.OptionA, p.SetB, p.OptionB)
		entry.N = n
		if n == 0 {
			entry.Error = fmt.Sprintf(
				"synthetic partition has no co-occurring non-null observations for %s[%s] x %s[%s]", p.SetA, p.OptionA, p.SetB, p.OptionB)
		} else {
			sourceN := 0
			for _, c := range p.Cells {
				sourceN += c.Count
			}
			entry.Delta = categoricalTVD(p.Cells, sourceN, counts, n)
		}
		report.SetSetPairwise = append(report.SetSetPairwise, entry)
	}
}

// computeSyntheticSetSetJoint decodes mergedSchema's records once,
// building the realized 2x2 joint frequency table of setA's optionA and
// setB's optionB — each expressed as "selected"/"not_selected" — over
// exactly the rows where _synthetic is true (non-zero) and both set
// fields are simultaneously non-null. Returns n=0 (and no counts) when
// either field carries no dictionary entry named for its declared
// option.
func computeSyntheticSetSetJoint(mergedSchema *encoding.Schema, rows []syntheticRow, setA, optionA, setB, optionB string) (counts map[[2]string]int, n int) {
	counts = make(map[[2]string]int)
	fa := mergedSchema.Field(setA)
	fb := mergedSchema.Field(setB)
	if fa == nil || fa.Dictionary == nil || fb == nil || fb.Dictionary == nil {
		return counts, 0
	}
	optIdxA, ok := fa.Dictionary.IDFor(optionA)
	if !ok {
		return counts, 0
	}
	optIdxB, ok := fb.Dictionary.IDFor(optionB)
	if !ok {
		return counts, 0
	}
	for _, row := range rows {
		if row.nulls[setA] || row.nulls[setB] {
			continue
		}
		maskA, _ := row.wide[setA].(uint64)
		maskB, _ := row.wide[setB].(uint64)
		selA := "not_selected"
		if maskA&(uint64(1)<<optIdxA) != 0 {
			selA = "selected"
		}
		selB := "not_selected"
		if maskB&(uint64(1)<<optIdxB) != 0 {
			selB = "selected"
		}
		counts[[2]string{selA, selB}]++
		n++
	}
	return counts, n
}
