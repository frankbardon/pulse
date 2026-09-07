package synth

import (
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

// FidelityReport is the JSON document written to
// Options.FidelityReportPath, comparing the freshly generated rows
// against the source cohort's rows inside the one combined output.
// Fields is this story's per-field marginal section.
//
// A later epic (E2-S3) adds a pairwise conditional-structure delta
// section under a "pairwise" key. That key is deliberately absent from
// this type today rather than declared as an empty placeholder — its
// eventual shape (array vs. object) is not yet decided, and locking in
// the wrong one now would force a breaking reshape later. Absence, not
// an empty value, is what "leaves room" here.
type FidelityReport struct {
	SourceRows    int              `json:"source_rows"`
	SyntheticRows int              `json:"synthetic_rows"`
	Fields        []*FieldFidelity `json:"fields,omitempty"`
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
