package synth

import (
	"bytes"
	"fmt"
	"math"

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

// FidelityReport is the JSON document written to
// Options.FidelityReportPath, comparing the freshly generated rows
// against the source cohort's rows inside the one combined output.
// Fields is the per-field marginal section (E1-S2); Pairwise is the
// numeric-numeric correlation-delta section (E2-S3), populated only
// when the spec carried at least one CorrelationSpec — absent
// (omitempty), never an empty placeholder array, on every plain
// synthesis and every from-profile run with no captured correlations.
// Warnings mirrors any thin-pair warnings the caller supplied (see
// Options.FidelityWarnings) — typically Profile.Warnings from a
// --conditional capture — so a reader sees "how well did it match"
// (Pairwise) and "which parts were built on thin data" (Warnings) in
// one document (FR-18).
type FidelityReport struct {
	SourceRows    int                 `json:"source_rows"`
	SyntheticRows int                 `json:"synthetic_rows"`
	Fields        []*FieldFidelity    `json:"fields,omitempty"`
	Pairwise      []*PairwiseFidelity `json:"pairwise,omitempty"`
	Warnings      []string            `json:"warnings,omitempty"`
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
// fields, TEST_CHISQ for categorical fields, both driven through run.
// Fields of any other on-wire type (date, datetime, packed_bool, u4,
// set_*) are out of scope for this section; later stories may widen
// coverage.
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
func BuildPairwise(report *FidelityReport, mergedSchema *encoding.Schema, records []byte, pairs []CorrelationSpec, warnings []string) {
	for _, p := range pairs {
		entry := &PairwiseFidelity{A: p.A, B: p.B, SourceRho: p.Correlation}
		rho, n, ok := computeSyntheticPairRho(mergedSchema, records, p.A, p.B)
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

// computeSyntheticPairRho decodes mergedSchema's records once, computing
// the realized Pearson correlation of fields a/b over exactly the rows
// where _synthetic is true (non-zero) and both a and b are
// simultaneously non-null — the same co-occurrence discipline
// computeConditionalNumericPairs applies at capture time, applied here
// to the OUTPUT cohort's generated partition instead of a source
// capture. Returns ok=false (mirroring pearson's own NaN contract) when
// fewer than two such rows exist.
func computeSyntheticPairRho(mergedSchema *encoding.Schema, records []byte, a, b string) (rho float64, n int, ok bool) {
	rr := encoding.NewRecordReader(bytes.NewReader(records), mergedSchema)
	values := make(map[string]float64, len(mergedSchema.Fields))
	nulls := make(map[string]bool, len(mergedSchema.Fields))
	var av, bv []float64
	for {
		if err := rr.ReadRecordWithWide(values, nulls, nil); err != nil {
			break
		}
		if values[SyntheticFieldName] == 0 {
			continue
		}
		if nulls[a] || nulls[b] {
			continue
		}
		av = append(av, values[a])
		bv = append(bv, values[b])
	}
	rho = pearson(av, bv)
	if math.IsNaN(rho) {
		return 0, len(av), false
	}
	return rho, len(av), true
}
