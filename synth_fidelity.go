package pulse

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/synth"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// writeSynthFidelityReport re-reads the just-written output cohort at
// path, drives synth.BuildFidelityReport against its records (TEST_KS
// for numeric fields, TEST_CHISQ for categorical, a direct per-option
// frequency comparison for set_* fields, all compared against
// synth.SyntheticFieldName), folds in synth.BuildPairwise's
// numeric-numeric correlation-delta section (see synth.PairwiseFidelity)
// plus synth.BuildCategoricalPairwise / synth.BuildCategoricalNumericPairwise's
// categorical-categorical contingency-delta and categorical-numeric
// conditional-mean/std-delta sections (E3-S4; see CategoricalPairFidelity
// / CategoricalNumericPairFidelity), plus synth.BuildSetCategoricalPairwise
// / BuildSetNumericPairwise / BuildSetSetPairwise's three set_*
// joint-structure delta sections (E5-S4), plus any fidelityWarnings
// (typically Options.FidelityWarnings, itself typically Profile.Warnings,
// covering every pair kind through the same shared shape), and writes
// the resulting JSON document to reportPath.
//
// Every pairwise section is built from synth.ResolveConflicts(spec), NOT
// spec's own (unpruned) pair slices: spec carries every relationship
// SpecFromProfile captured, but generate() only ever applies the subset
// resolveConflicts resolves out of it — one conditioning relationship
// per target field, first-claimed-wins. Re-resolving here against the
// SAME spec generate() was given (see Pulse.Synth) reproduces that exact
// resolution deterministically, so this fidelity-checks only
// relationships the generator actually modeled; a conflict-dropped
// pair's own warning (already on fidelityWarnings) is what explains its
// absence from the corresponding pairwise section instead of a
// misleading delta computed against a relationship that was never
// applied.
//
// This bridge lives at the pulse facade level rather than inside
// synth/ because synth cannot import processing directly without
// creating an import cycle: descriptor imports synth for the built-in
// distribution registry, and processing's own internal test files
// import descriptor — synth importing processing back would close
// that loop. pulse.go already imports both packages, so the bridge —
// and the only implementation of synth.TestRunner — lives here. Every
// pairwise section needs no processing/ import at all — each is pure
// statistics over already-decoded record values — but stays in this
// same bridge so the one JSON document is assembled and written in one
// place.
func writeSynthFidelityReport(fs afero.Fs, path, reportPath string, spec *synth.Spec, fidelityWarnings []string) error {
	data, err := afero.ReadFile(fs, path)
	if err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE, "reading synth output for fidelity report")
	}
	r := bytes.NewReader(data)
	if err := encoding.ReadHeader(r); err != nil {
		return err
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		return err
	}
	records := data[len(data)-r.Len():]

	sourceRows, syntheticRows := countSyntheticPartitions(schema, records)
	report := synth.BuildFidelityReport(schema, records, sourceRows, syntheticRows, runFidelityTest)

	// spec carries the FULL captured relationship set (SpecFromProfile
	// never prunes it — see synth.ResolveConflicts's own doc). generate()
	// only ever applies the subset resolveConflicts resolves out of that
	// set — one conditioning relationship per target field — so
	// re-resolving here against the SAME spec generate() was given (see
	// pulse.go Synth) restricts every pairwise section to relationships
	// that were actually modeled. Fidelity-checking a conflict-dropped
	// pair would score a delta for a relationship the generator never
	// applied, which is misleading rather than merely redundant; the
	// dropped pair's own conflict warning (already on fidelityWarnings)
	// is what explains its absence here.
	catPairs, catNumPairs, setCatPairs, setNumPairs, setSetPairs, correlations := synth.ResolveConflicts(spec)
	synth.BuildPairwise(report, schema, records, correlations, fidelityWarnings)
	synth.BuildCategoricalPairwise(report, schema, records, catPairs)
	synth.BuildCategoricalNumericPairwise(report, schema, records, catNumPairs)
	synth.BuildSetCategoricalPairwise(report, schema, records, setCatPairs)
	synth.BuildSetNumericPairwise(report, schema, records, setNumPairs)
	synth.BuildSetSetPairwise(report, schema, records, setSetPairs)

	out, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return errors.WrapCodedError(err, errors.CLI_OUTPUT, "marshaling fidelity report")
	}
	if err := afero.WriteFile(fs, reportPath, out, 0644); err != nil {
		return errors.WrapCodedError(err, errors.CLI_OUTPUT, "writing fidelity report")
	}
	return nil
}

// countSyntheticPartitions does one decode pass over records under
// schema's real (physical) layout, tallying rows by the on-wire value
// of synth.SyntheticFieldName (0 = copied from source, non-zero =
// newly generated) for FidelityReport's informational row counts.
func countSyntheticPartitions(schema *encoding.Schema, records []byte) (sourceRows, syntheticRows int) {
	rr := encoding.NewRecordReader(bytes.NewReader(records), schema)
	values := make(map[string]float64, len(schema.Fields))
	nulls := make(map[string]bool)
	for {
		if err := rr.ReadRecordWithWide(values, nulls, nil); err != nil {
			break
		}
		if values[synth.SyntheticFieldName] != 0 {
			syntheticRows++
		} else {
			sourceRows++
		}
	}
	return sourceRows, syntheticRows
}

// runFidelityTest is the synth.TestRunner implementation: it drives a
// single Test through the ordinary Process pipeline against an
// in-memory iterator over records — the existing TEST_KS / TEST_CHISQ
// operators, never new statistic math.
func runFidelityTest(physicalSchema, presentedSchema *encoding.Schema, records []byte, test *types.Test) (*types.TestResult, error) {
	req := &types.Request{Tests: []*types.Test{test}}
	resp, err := processing.NewProcessor(presentedSchema).Process(
		context.Background(), req, newFidelityIterator(records, physicalSchema, presentedSchema))
	if err != nil {
		return nil, err
	}
	if len(resp.Tests) != 1 {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG, "fidelity test produced no result")
	}
	return resp.Tests[0], nil
}

// fidelityIterator implements processing.RecordIterator over an
// in-memory encoded record buffer: it decodes against physical (the
// buffer's real byte layout) but attaches presented to each
// constructed Record, so Record.StringValue / NumericValue resolve
// types and dictionaries against the caller's view schema instead —
// see synth.TestRunner for why the two schemas diverge.
//
// Each Next() call decodes into freshly allocated maps rather than
// reusing one set across calls: the buffered Process path
// materializes every iterated Record into a slice before evaluating
// tests, so reusing maps would let every retained Record alias the
// same (subsequently overwritten) data.
type fidelityIterator struct {
	data      []byte
	physical  *encoding.Schema
	presented *encoding.Schema
	rr        *encoding.RecordReader
	rec       *processing.Record
}

func newFidelityIterator(data []byte, physical, presented *encoding.Schema) *fidelityIterator {
	it := &fidelityIterator{data: data, physical: physical, presented: presented}
	it.Reset()
	return it
}

// Next advances to the next record, decoding it against physical.
func (it *fidelityIterator) Next() bool {
	values := make(map[string]float64, len(it.physical.Fields))
	nulls := make(map[string]bool)
	wide := make(map[string]any)
	if err := it.rr.ReadRecordWithWide(values, nulls, wide); err != nil {
		it.rec = nil
		return false
	}
	it.rec = processing.NewRecordWithWide(it.presented, values, nulls, wide)
	return true
}

// Record returns the current record. Only valid after Next returns true.
func (it *fidelityIterator) Record() *processing.Record {
	return it.rec
}

// Reset rewinds the iterator to the beginning of data.
func (it *fidelityIterator) Reset() {
	it.rr = encoding.NewRecordReader(bytes.NewReader(it.data), it.physical)
	it.rec = nil
}
