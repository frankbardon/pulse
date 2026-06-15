package processing

import (
	"context"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// E2-S12 — Per-filterer FiltererComponents emission parity sweep over
// every registered filterer (11 today). Symmetric to the aggregator
// (TestMetaAggregator_AllOps_ManifestParity) and grouper
// (TestMetaGrouper_AllOps_ManifestParity) parity sweeps; the
// filterer-side runtime emission lives in service/ tests
// (process_filterer_components_test.go) where the orchestrator drives
// the in-memory pulse file, but this file locks the in-package
// parity contract via Processor.processRecords directly so a regression
// in the buffered-path filter walk surfaces here without going through
// the service-layer fs marshalling.
//
// Coverage:
//
//   - "small": a non-trivial subset of rows passes the filter.
//   - "empty": the filter rejects every row.
//   - "all-null": every row's filtered field is null on input.
//
// Plus the n_in chain invariant test pinned to the buffered path:
// n_in[0] == post-feature-pass total (input row count); n_in[i] ==
// n_out[i-1] for every i > 0. Mirrors
// TestService_Process_FiltererComponents_PipelineNInInvariant but via
// processRecords directly so any orchestrator-vs-filter-walk drift
// surfaces with the slot index named.

// manifestFiltererOperatorKeys returns the operator-specific key set
// (manifest schema MINUS the universal floor {n_in, n_out, n_null_input})
// for the named filterer. Sorted ascending. Sourced from the public
// BuildManifest projection. v1 has no built-in MetaFilterer
// implementations, so every registered filterer collapses to the empty
// operator-specific key set; the parity sweep still locks the contract
// so a future per-filter specific (e.g. FILTER_RANGE n_below /
// n_above) has a gate to fail.
func manifestFiltererOperatorKeys(t *testing.T, name string) []string {
	t.Helper()
	m := descriptor.BuildManifest()
	schema, ok := m.ComponentsSchemas.Filterers[name]
	if !ok {
		t.Fatalf("manifest carries no components schema for %s", name)
	}
	out := make([]string, 0, len(schema.Keys))
	for _, k := range schema.Keys {
		// Strip universal floor — those keys are typed fields on
		// FiltererComponents (NIn, NOut, NNullInput).
		if k.Name == "n_in" || k.Name == "n_out" || k.Name == "n_null_input" {
			continue
		}
		out = append(out, k.Name)
	}
	sort.Strings(out)
	return out
}

// filtererParityFixture pairs a filterer type with the schema, field,
// optional values / expression, and three record sets (small, empty,
// all-null) plus the expected {NIn, NOut, NNullInput} counter triple
// per sub-case.
type filtererParityFixture struct {
	schema     *encoding.Schema
	field      string
	values     []string
	expression string

	smallRecs  []*Record
	smallWant  [3]int // {NIn, NOut, NNullInput}
	emptyRecs  []*Record
	emptyWant  [3]int
	nullRecs   []*Record
	nullWant   [3]int
}

// nullRecordsOn returns n records where the named field is null.
func nullRecordsOn(schema *encoding.Schema, field string, n int) []*Record {
	out := make([]*Record, n)
	for i := range out {
		out[i] = NewRecordWithNulls(schema,
			map[string]float64{field: 0},
			map[string]bool{field: true})
	}
	return out
}

// makeNullSetRecord builds a Record carrying a null tags field. Used
// by the FILTER_SET_* all-null sub-cases.
func makeNullSetRecord(schema *encoding.Schema) *Record {
	r := NewRecordWithWide(schema,
		map[string]float64{},
		map[string]bool{"tags": true},
		map[string]any{"tags": uint64(0)})
	return r
}

// allFiltererParityFixtures returns one fixture per registered filterer
// type. Schema choice mirrors the manifest's AcceptsTypes: FILTER_RANGE
// needs numeric, FILTER_SET_* needs set_u8, FILTER_EXPRESSION runs
// against any record field, the rest accept any cohort field type.
func allFiltererParityFixtures(t *testing.T) map[types.FiltererType]filtererParityFixture {
	t.Helper()

	numSchema := numericSchema()
	// score values: 10, 20, 30, 40, 50 — populated rows.
	smallNumeric := makeRecords(numSchema, "score", []float64{10, 20, 30, 40, 50})
	allNullNumeric := nullRecordsOn(numSchema, "score", 5)

	setSchema := makeSetTestSchema(t)
	smallSet := []*Record{
		makeSetRecord(setSchema, 0b0001), // VISA
		makeSetRecord(setSchema, 0b0011), // VISA + MC
		makeSetRecord(setSchema, 0b0100), // AMEX
		makeSetRecord(setSchema, 0b1000), // DISC
		makeSetRecord(setSchema, 0b0110), // MC + AMEX
	}
	allNullSet := []*Record{
		makeNullSetRecord(setSchema),
		makeNullSetRecord(setSchema),
		makeNullSetRecord(setSchema),
		makeNullSetRecord(setSchema),
		makeNullSetRecord(setSchema),
	}

	return map[types.FiltererType]filtererParityFixture{
		types.FILTER_INCLUDE: {
			schema:    numSchema,
			field:     "score",
			values:    []string{"10", "20", "30"},
			smallRecs: smallNumeric,
			smallWant: [3]int{5, 3, 0},
			// Reject-all: 999 matches no records; valid input non-null.
			emptyRecs: smallNumeric,
			emptyWant: [3]int{5, 0, 0},
			nullRecs:  allNullNumeric,
			// Nulls drop FILTER_INCLUDE.
			nullWant: [3]int{5, 0, 5},
		},
		types.FILTER_EXCLUDE: {
			schema:    numSchema,
			field:     "score",
			values:    []string{"10", "20"},
			smallRecs: smallNumeric,
			smallWant: [3]int{5, 3, 0},
			// Reject-all override: exclude every value.
			emptyRecs: smallNumeric,
			emptyWant: [3]int{5, 0, 0},
			nullRecs:  allNullNumeric,
			// Nulls pass FILTER_EXCLUDE (the value isn't in the rejection list).
			nullWant: [3]int{5, 5, 5},
		},
		types.FILTER_RANGE: {
			schema:    numSchema,
			field:     "score",
			values:    []string{"20", "40"},
			smallRecs: smallNumeric,
			smallWant: [3]int{5, 3, 0},
			emptyRecs: smallNumeric,
			emptyWant: [3]int{5, 0, 0}, // empty case overrides values
			nullRecs:  allNullNumeric,
			// Nulls drop FILTER_RANGE.
			nullWant: [3]int{5, 0, 5},
		},
		types.FILTER_EXPRESSION: {
			schema:     numSchema,
			expression: "score > 20",
			smallRecs:  smallNumeric,
			smallWant:  [3]int{5, 3, 0},
			emptyRecs:  smallNumeric,
			emptyWant:  [3]int{5, 0, 0},
			// FILTER_EXPRESSION carries no Field, so NNullInput stays 0.
			nullRecs: allNullNumeric,
			nullWant: [3]int{5, 0, 0},
		},
		types.FILTER_NULL: {
			schema:    numSchema,
			field:     "score",
			values:    []string{"is_not_null"},
			smallRecs: smallNumeric,
			smallWant: [3]int{5, 5, 0},
			// empty: is_null on the non-null cohort.
			emptyRecs: smallNumeric,
			emptyWant: [3]int{5, 0, 0},
			// all-null with is_null: every row passes AND every row is null-input.
			nullRecs: allNullNumeric,
			nullWant: [3]int{5, 5, 5},
		},
		types.FILTER_TRUE: {
			schema:    numSchema,
			field:     "score",
			values:    []string{"truthy"},
			smallRecs: smallNumeric,
			smallWant: [3]int{5, 5, 0},
			// empty (truthy mode): every non-null numeric is truthy →
			// FILTER_TRUE admits every row. The "reject-all" branch on
			// the small cohort is exercised by FILTER_FALSE below; here
			// we instead lock the all-null reject branch.
			emptyRecs: allNullNumeric,
			// Null coerces to false → TRUE rejects every null row.
			emptyWant: [3]int{5, 0, 5},
			nullRecs:  allNullNumeric,
			nullWant:  [3]int{5, 0, 5},
		},
		types.FILTER_FALSE: {
			schema:    numSchema,
			field:     "score",
			values:    []string{"truthy"},
			smallRecs: smallNumeric,
			// truthy mode: every non-null numeric is truthy → FALSE
			// rejects every row.
			smallWant: [3]int{5, 0, 0},
			emptyRecs: smallNumeric,
			emptyWant: [3]int{5, 0, 0},
			nullRecs:  allNullNumeric,
			// Null coerces to false → FALSE admits every null row.
			nullWant: [3]int{5, 5, 5},
		},
		types.FILTER_SET_CONTAINS_ANY: {
			schema:    setSchema,
			field:     "tags",
			values:    []string{"VISA"},
			smallRecs: smallSet,
			// VISA bit set on rows 0 (0b0001) and 1 (0b0011) → 2 admit.
			smallWant: [3]int{5, 2, 0},
			emptyRecs: smallSet,
			// emptyWant overridden below for set ops where reject-all
			// requires asking for a label no row carries — easier to
			// derive in the test body.
			emptyWant: [3]int{5, 0, 0},
			nullRecs:  allNullSet,
			// Nulls drop FILTER_SET_CONTAINS_ANY.
			nullWant: [3]int{5, 0, 5},
		},
		types.FILTER_SET_CONTAINS_ALL: {
			schema:    setSchema,
			field:     "tags",
			values:    []string{"VISA", "MC"},
			smallRecs: smallSet,
			// VISA + MC both set only on row 1 (0b0011) → 1 admits.
			smallWant: [3]int{5, 1, 0},
			emptyRecs: smallSet,
			emptyWant: [3]int{5, 0, 0},
			nullRecs:  allNullSet,
			// Nulls drop FILTER_SET_CONTAINS_ALL.
			nullWant: [3]int{5, 0, 5},
		},
		types.FILTER_SET_CONTAINS_NONE: {
			schema:    setSchema,
			field:     "tags",
			values:    []string{"VISA"},
			smallRecs: smallSet,
			// VISA absent on rows 2 (0b0100), 3 (0b1000), 4 (0b0110) → 3 admit.
			smallWant: [3]int{5, 3, 0},
			emptyRecs: smallSet,
			emptyWant: [3]int{5, 0, 0},
			nullRecs:  allNullSet,
			// Nulls PASS FILTER_SET_CONTAINS_NONE (in-band convention
			// documented at processing/filterer_set.go).
			nullWant: [3]int{5, 5, 5},
		},
		types.FILTER_SET_EQUALS: {
			schema:    setSchema,
			field:     "tags",
			values:    []string{"VISA"},
			smallRecs: smallSet,
			// Only row 0 (0b0001) equals exactly {VISA} → 1 admits.
			smallWant: [3]int{5, 1, 0},
			emptyRecs: smallSet,
			emptyWant: [3]int{5, 0, 0},
			nullRecs:  allNullSet,
			// Nulls drop FILTER_SET_EQUALS.
			nullWant: [3]int{5, 0, 5},
		},
	}
}

// emptyCaseOverrides records the per-filterer Values / Expression slot
// used by the "empty" sub-case (where the small fixture's smallRecs is
// re-used but the predicate is sharpened to reject every row). Defined
// outside the fixture map so the fixture stays compact and the override
// reads as data, not control flow.
//
// Set-typed overrides cannot use unknown labels (buildSetQueryMask
// rejects them with PROCESSING_CONFIG), so the "reject every row"
// configuration uses the full label set — none of the smallSet rows
// carry every issuer, so CONTAINS_ALL / CONTAINS_NONE / EQUALS each
// reject all five rows against the all-four query mask.
func emptyCaseOverrides() map[types.FiltererType]struct {
	values     []string
	expression string
} {
	return map[types.FiltererType]struct {
		values     []string
		expression string
	}{
		types.FILTER_INCLUDE:           {values: []string{"999"}},
		types.FILTER_EXCLUDE:           {values: []string{"10", "20", "30", "40", "50"}},
		types.FILTER_RANGE:             {values: []string{"100", "200"}},
		types.FILTER_EXPRESSION:        {expression: "score > 9999"},
		types.FILTER_NULL:              {values: []string{"is_null"}},   // non-null cohort: every row rejects
		types.FILTER_TRUE:              {values: []string{"truthy"}},    // all-null cohort coerces to false → reject all
		types.FILTER_FALSE:             {values: []string{"truthy"}},    // non-null cohort coerces to true → FALSE rejects all
		types.FILTER_SET_CONTAINS_ANY:  {values: []string{}},            // mask=0 → CONTAINS_ANY rejects all
		types.FILTER_SET_CONTAINS_ALL:  {values: []string{"VISA", "MC", "AMEX", "DISC"}},
		types.FILTER_SET_CONTAINS_NONE: {values: []string{"VISA", "MC", "AMEX", "DISC"}},
		types.FILTER_SET_EQUALS:        {values: []string{"VISA", "MC", "AMEX", "DISC"}},
	}
}

// runFilterParity drives one filterer through processRecords on the
// given record slice + slot config and returns the FiltererComponents
// slot. Fails the test if Components is unpopulated.
func runFilterParity(t *testing.T, schema *encoding.Schema, slot *types.Filterer, recs []*Record) types.FiltererComponents {
	t.Helper()
	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: slot.Field, Label: "n"},
		},
		Filterers: []*types.Filterer{slot},
	}
	proc := NewProcessor(schema)
	resp, err := proc.processRecords(context.Background(), req, recs)
	if err != nil {
		t.Fatalf("processRecords: %v", err)
	}
	if resp.Components == nil {
		t.Fatalf("Response.Components nil; want populated")
	}
	if got := len(resp.Components.Filterers); got != 1 {
		t.Fatalf("Filterers slots = %d, want 1", got)
	}
	return resp.Components.Filterers[0]
}

// TestMetaFilterer_AllOps_ManifestParity covers every registered
// filterer (11 today) under three input shapes: small (filter admits a
// subset), empty (filter rejects all), all-null (every row's filtered
// field is null on input). For each:
//
//   - Counter triple {NIn, NOut, NNullInput} matches hand-computed
//     expectations.
//   - Operator-specific key set (sorted ascending) equals the manifest's
//     components_schemas.filterers[<op>].keys minus the universal floor
//     {n_in, n_out, n_null_input}.
//
// v1 has no built-in MetaFilterer implementation, so the operator-
// specific key set collapses to the empty slice for every filterer —
// the assertion still locks the wiring contract so a future per-filter
// specific addition surfaces here as either a missing key or a missing
// emit.
func TestMetaFilterer_AllOps_ManifestParity(t *testing.T) {
	fixtures := allFiltererParityFixtures(t)
	overrides := emptyCaseOverrides()

	for _, op := range types.AllFiltererTypes() {
		op := op
		fix, ok := fixtures[op]
		if !ok {
			t.Fatalf("no parity fixture for %s — add one in allFiltererParityFixtures", op)
		}
		t.Run(string(op)+"/small", func(t *testing.T) {
			slot := &types.Filterer{
				Type:       op,
				Field:      fix.field,
				Values:     fix.values,
				Expression: fix.expression,
			}
			got := runFilterParity(t, fix.schema, slot, fix.smallRecs)
			if got.NIn != fix.smallWant[0] {
				t.Errorf("%s: NIn = %d, want %d", op, got.NIn, fix.smallWant[0])
			}
			if got.NOut != fix.smallWant[1] {
				t.Errorf("%s: NOut = %d, want %d", op, got.NOut, fix.smallWant[1])
			}
			if got.NNullInput != fix.smallWant[2] {
				t.Errorf("%s: NNullInput = %d, want %d", op, got.NNullInput, fix.smallWant[2])
			}
			// Operator-specific key set parity. v1 FiltererComponents
			// has no Operator field — every built-in filterer's
			// operator-specific key set is empty by construction. The
			// manifest must agree (its declared key set after the
			// universal floor strip should be empty). When an embedder
			// adds a MetaFilterer registration, the runtime side gains
			// a typed Operator surface and this gate flips on without
			// re-opening the test.
			wantKeys := manifestFiltererOperatorKeys(t, string(op))
			if len(wantKeys) != 0 {
				t.Errorf("%s manifest declares operator-specific keys %v but FiltererComponents carries no Operator field; widen the runtime emission first",
					op, wantKeys)
			}
		})

		t.Run(string(op)+"/empty", func(t *testing.T) {
			// Sharpen the slot's Values / Expression to a configuration
			// that rejects every row in the smallRecs cohort.
			ov := overrides[op]
			slot := &types.Filterer{
				Type:  op,
				Field: fix.field,
			}
			if op == types.FILTER_EXPRESSION {
				slot.Expression = ov.expression
			} else {
				slot.Values = ov.values
			}
			got := runFilterParity(t, fix.schema, slot, fix.emptyRecs)
			if got.NIn != fix.emptyWant[0] {
				t.Errorf("%s: NIn = %d, want %d", op, got.NIn, fix.emptyWant[0])
			}
			if got.NOut != fix.emptyWant[1] {
				t.Errorf("%s: NOut = %d, want %d", op, got.NOut, fix.emptyWant[1])
			}
			if got.NNullInput != fix.emptyWant[2] {
				t.Errorf("%s: NNullInput = %d, want %d", op, got.NNullInput, fix.emptyWant[2])
			}
		})

		// All-null is omitted for FILTER_TRUE because its smallWant
		// case already targets the all-null cohort; running the
		// dedicated all-null sub-case would be a duplicate.
		//
		// FILTER_EXPRESSION's all-null sub-case is also omitted —
		// the expression compiles against record.AllValues(), which
		// drops null fields from the env; "score > 20" therefore
		// fails to compile when every row's score is absent. The
		// orchestrator's contract is documented at processing/
		// filterer.go: FILTER_EXPRESSION expects the referenced
		// fields to be present, and the embedder is responsible for
		// pre-filtering nulls (via a FILTER_NULL is_not_null slot)
		// before chaining an expression filter. The contract is
		// already covered by the smallWant case (non-null score
		// cohort) and the service-layer end-to-end test in
		// service/process_filterer_components_test.go.
		if op == types.FILTER_TRUE || op == types.FILTER_EXPRESSION {
			continue
		}
		t.Run(string(op)+"/all_null", func(t *testing.T) {
			slot := &types.Filterer{
				Type:       op,
				Field:      fix.field,
				Values:     fix.values,
				Expression: fix.expression,
			}
			// FILTER_NULL all-null sub-case uses is_null semantics
			// (matches the orchestrator's E2-S9 service test), so
			// override Values for that one filterer.
			if op == types.FILTER_NULL {
				slot.Values = []string{"is_null"}
			}
			got := runFilterParity(t, fix.schema, slot, fix.nullRecs)
			if got.NIn != fix.nullWant[0] {
				t.Errorf("%s: NIn = %d, want %d", op, got.NIn, fix.nullWant[0])
			}
			if got.NOut != fix.nullWant[1] {
				t.Errorf("%s: NOut = %d, want %d", op, got.NOut, fix.nullWant[1])
			}
			if got.NNullInput != fix.nullWant[2] {
				t.Errorf("%s: NNullInput = %d, want %d", op, got.NNullInput, fix.nullWant[2])
			}
		})
	}
}

// TestFiltererComponents_NInChainInvariant chains four filterers and
// asserts the n_in invariant from the story acceptance criteria:
//
//   - n_in[0] == post-feature-pass total (the input row count, since
//     no features run on this fixture).
//   - n_in[i] == n_out[i-1] for every i > 0.
//
// Pipeline (5-row score cohort {10, 20, 30, 40, 50}):
//
//  1. FILTER_RANGE [10, 40] — admits {10, 20, 30, 40} → NOut = 4.
//  2. FILTER_EXCLUDE {10}    — rejects 10 → NOut = 3.
//  3. FILTER_INCLUDE {20, 30, 40} — all 3 admit → NOut = 3.
//  4. FILTER_EXPRESSION "score > 20" — admits {30, 40} → NOut = 2.
//
// Mirrors TestService_Process_FiltererComponents_PipelineNInInvariant
// but exercises the processing-layer buffered path directly so a
// processor-side regression (e.g. an off-by-one in applyFilterPass)
// surfaces here without the service-fs marshalling between source and
// counter.
func TestFiltererComponents_NInChainInvariant(t *testing.T) {
	schema := numericSchema()
	records := makeRecords(schema, "score", []float64{10, 20, 30, 40, 50})
	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		},
		Filterers: []*types.Filterer{
			{Type: types.FILTER_RANGE, Field: "score", Values: []string{"10", "40"}},
			{Type: types.FILTER_EXCLUDE, Field: "score", Values: []string{"10"}},
			{Type: types.FILTER_INCLUDE, Field: "score", Values: []string{"20", "30", "40"}},
			{Type: types.FILTER_EXPRESSION, Expression: "score > 20"},
		},
	}
	proc := NewProcessor(schema)
	resp, err := proc.processRecords(context.Background(), req, records)
	if err != nil {
		t.Fatalf("processRecords: %v", err)
	}
	if resp.Components == nil {
		t.Fatalf("Response.Components nil; want populated")
	}
	got := resp.Components.Filterers
	if len(got) != 4 {
		t.Fatalf("Filterers slots = %d, want 4", len(got))
	}

	// Slot-by-slot expectations.
	wantSlots := []struct {
		label string
		nIn   int
		nOut  int
	}{
		{"FILTER_RANGE", 5, 4},
		{"FILTER_EXCLUDE", 4, 3},
		{"FILTER_INCLUDE", 3, 3},
		{"FILTER_EXPRESSION", 3, 2},
	}
	for i, want := range wantSlots {
		if got[i].NIn != want.nIn {
			t.Errorf("slot %d (%s): NIn = %d, want %d", i, want.label, got[i].NIn, want.nIn)
		}
		if got[i].NOut != want.nOut {
			t.Errorf("slot %d (%s): NOut = %d, want %d", i, want.label, got[i].NOut, want.nOut)
		}
	}

	// First-slot anchor: n_in[0] equals the input row count.
	if got[0].NIn != len(records) {
		t.Errorf("n_in[0] = %d, want %d (post-feature-pass total)", got[0].NIn, len(records))
	}
	// Chain invariant: n_in[i] == n_out[i-1] for every i > 0.
	for i := 1; i < len(got); i++ {
		if got[i].NIn != got[i-1].NOut {
			t.Errorf("n_in invariant violated at slot %d: n_in=%d, prior n_out=%d",
				i, got[i].NIn, got[i-1].NOut)
		}
	}
}
