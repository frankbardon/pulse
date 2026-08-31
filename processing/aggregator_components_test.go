package processing

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

func TestMetaAggregator_ScalarOps_Components(t *testing.T) {
	schema := numericSchema()
	makeRecs := func(values []float64, nullIdxs []int) []*Record {
		return makeRecordsWithNulls(schema, "score", values, nullIdxs)
	}

	type expectation struct {
		n        int
		nNull    int
		operator map[string]any
	}
	type tc struct {
		name    string
		aggType types.AggregationType
		records []*Record
		want    expectation
	}

	populated := []float64{10, 20, 30}
	empty := []float64{}
	nullHeavy := []float64{0, 0, 0}
	nullHeavyIdx := []int{0, 1, 2}

	tests := []tc{
		// AGG_COUNT — floor only.
		{
			name:    "COUNT_populated",
			aggType: types.AGG_COUNT,
			records: makeRecs(populated, nil),
			want:    expectation{n: 3, nNull: 0, operator: nil},
		},
		{
			name:    "COUNT_empty",
			aggType: types.AGG_COUNT,
			records: makeRecs(empty, nil),
			want:    expectation{n: 0, nNull: 0, operator: nil},
		},
		{
			name:    "COUNT_nullHeavy",
			aggType: types.AGG_COUNT,
			records: makeRecs(nullHeavy, nullHeavyIdx),
			want:    expectation{n: 0, nNull: 3, operator: nil},
		},

		// AGG_SUM — {sum}.
		{
			name:    "SUM_populated",
			aggType: types.AGG_SUM,
			records: makeRecs(populated, nil),
			want:    expectation{n: 3, nNull: 0, operator: map[string]any{"sum": 60.0}},
		},
		{
			name:    "SUM_empty",
			aggType: types.AGG_SUM,
			records: makeRecs(empty, nil),
			want:    expectation{n: 0, nNull: 0, operator: map[string]any{"sum": 0.0}},
		},
		{
			name:    "SUM_nullHeavy",
			aggType: types.AGG_SUM,
			records: makeRecs(nullHeavy, nullHeavyIdx),
			want:    expectation{n: 0, nNull: 3, operator: map[string]any{"sum": 0.0}},
		},

		// AGG_AVERAGE — {sum} only (mean derivable as sum / n).
		{
			name:    "AVERAGE_populated",
			aggType: types.AGG_AVERAGE,
			records: makeRecs(populated, nil),
			want:    expectation{n: 3, nNull: 0, operator: map[string]any{"sum": 60.0}},
		},
		{
			name:    "AVERAGE_empty",
			aggType: types.AGG_AVERAGE,
			records: makeRecs(empty, nil),
			want:    expectation{n: 0, nNull: 0, operator: map[string]any{"sum": 0.0}},
		},
		{
			name:    "AVERAGE_nullHeavy",
			aggType: types.AGG_AVERAGE,
			records: makeRecs(nullHeavy, nullHeavyIdx),
			want:    expectation{n: 0, nNull: 3, operator: map[string]any{"sum": 0.0}},
		},

		// AGG_MIN — {min}.
		{
			name:    "MIN_populated",
			aggType: types.AGG_MIN,
			records: makeRecs(populated, nil),
			want:    expectation{n: 3, nNull: 0, operator: map[string]any{"min": 10.0}},
		},
		{
			name:    "MIN_empty",
			aggType: types.AGG_MIN,
			records: makeRecs(empty, nil),
			want:    expectation{n: 0, nNull: 0, operator: map[string]any{"min": 0.0}},
		},
		{
			name:    "MIN_nullHeavy",
			aggType: types.AGG_MIN,
			records: makeRecs(nullHeavy, nullHeavyIdx),
			want:    expectation{n: 0, nNull: 3, operator: map[string]any{"min": 0.0}},
		},

		// AGG_MAX — {max}.
		{
			name:    "MAX_populated",
			aggType: types.AGG_MAX,
			records: makeRecs(populated, nil),
			want:    expectation{n: 3, nNull: 0, operator: map[string]any{"max": 30.0}},
		},
		{
			name:    "MAX_empty",
			aggType: types.AGG_MAX,
			records: makeRecs(empty, nil),
			want:    expectation{n: 0, nNull: 0, operator: map[string]any{"max": 0.0}},
		},
		{
			name:    "MAX_nullHeavy",
			aggType: types.AGG_MAX,
			records: makeRecs(nullHeavy, nullHeavyIdx),
			want:    expectation{n: 0, nNull: 3, operator: map[string]any{"max": 0.0}},
		},

		// AGG_RANGE — {min, max}.
		{
			name:    "RANGE_populated",
			aggType: types.AGG_RANGE,
			records: makeRecs(populated, nil),
			want:    expectation{n: 3, nNull: 0, operator: map[string]any{"min": 10.0, "max": 30.0}},
		},
		{
			name:    "RANGE_empty",
			aggType: types.AGG_RANGE,
			records: makeRecs(empty, nil),
			want:    expectation{n: 0, nNull: 0, operator: map[string]any{"min": 0.0, "max": 0.0}},
		},
		{
			name:    "RANGE_nullHeavy",
			aggType: types.AGG_RANGE,
			records: makeRecs(nullHeavy, nullHeavyIdx),
			want:    expectation{n: 0, nNull: 3, operator: map[string]any{"min": 0.0, "max": 0.0}},
		},

		// AGG_NULL_COUNT — floor only.
		{
			name:    "NULL_COUNT_populated",
			aggType: types.AGG_NULL_COUNT,
			records: makeRecs(populated, nil),
			want:    expectation{n: 3, nNull: 0, operator: nil},
		},
		{
			name:    "NULL_COUNT_empty",
			aggType: types.AGG_NULL_COUNT,
			records: makeRecs(empty, nil),
			want:    expectation{n: 0, nNull: 0, operator: nil},
		},
		{
			name:    "NULL_COUNT_nullHeavy",
			aggType: types.AGG_NULL_COUNT,
			records: makeRecs(nullHeavy, nullHeavyIdx),
			want:    expectation{n: 0, nNull: 3, operator: nil},
		},
	}

	for _, c := range tests {
		c := c
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{
				Aggregations: []*types.Aggregation{
					{Type: c.aggType, Field: "score", Label: "primary"},
				},
			}
			// Force buffered path via direct processRecords so the test
			// is single-pass deterministic; the streaming exit shares
			// the same buildAggregationComponents emit helper and is
			// covered separately by TestMetaAggregator_StreamingEmits.
			proc := NewProcessor(schema)
			resp, err := proc.processRecords(context.Background(), req, c.records)
			if err != nil {
				t.Fatalf("processRecords: %v", err)
			}
			if resp.Components == nil {
				t.Fatalf("Response.Components nil; want populated")
			}
			if got := len(resp.Components.Aggregations); got != 1 {
				t.Fatalf("Aggregations slots = %d, want 1", got)
			}
			entry := resp.Components.Aggregations[0]
			if entry.Label != "primary" {
				t.Errorf("Label = %q, want %q", entry.Label, "primary")
			}
			if entry.N != c.want.n {
				t.Errorf("N = %d, want %d", entry.N, c.want.n)
			}
			if entry.NNull != c.want.nNull {
				t.Errorf("NNull = %d, want %d", entry.NNull, c.want.nNull)
			}
			if !reflect.DeepEqual(entry.Operator, c.want.operator) {
				t.Errorf("Operator = %#v, want %#v", entry.Operator, c.want.operator)
			}
		})
	}
}

// TestMetaAggregator_StreamingEmits exercises the streaming-path exit
// (processStreaming) and confirms that the same per-slot
// AggregationComponents entry is emitted byte-equal to the buffered
// path. The cohort uses fields with no two-pass attribute / grouper /
// feature so canStream() returns true.
func TestMetaAggregator_StreamingEmits(t *testing.T) {
	schema := numericSchema()
	records := makeRecordsWithNulls(schema, "score",
		[]float64{10, 0, 30, 0, 50}, []int{1, 3})

	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "total"},
			{Type: types.AGG_MIN, Field: "score", Label: "lo"},
			{Type: types.AGG_MAX, Field: "score", Label: "hi"},
		},
	}
	proc := NewProcessor(schema)
	resp, err := proc.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if proc.LastPath() != PathStreaming {
		t.Fatalf("LastPath = %v, want PathStreaming", proc.LastPath())
	}
	if resp.Components == nil {
		t.Fatalf("Response.Components nil; want populated")
	}
	if got := len(resp.Components.Aggregations); got != 3 {
		t.Fatalf("Aggregations slots = %d, want 3", got)
	}
	wantOps := []map[string]any{
		{"sum": 90.0},
		{"min": 10.0},
		{"max": 50.0},
	}
	wantLabels := []string{"total", "lo", "hi"}
	for i, entry := range resp.Components.Aggregations {
		if entry.Label != wantLabels[i] {
			t.Errorf("Label[%d] = %q, want %q", i, entry.Label, wantLabels[i])
		}
		if entry.N != 3 {
			t.Errorf("N[%d] = %d, want 3", i, entry.N)
		}
		if entry.NNull != 2 {
			t.Errorf("NNull[%d] = %d, want 2", i, entry.NNull)
		}
		if !reflect.DeepEqual(entry.Operator, wantOps[i]) {
			t.Errorf("Operator[%d] = %#v, want %#v", i, entry.Operator, wantOps[i])
		}
	}
}

// aggParityFixture pairs an aggregation type with the schema, field
// name, params, and 10-row record set (with 2 nulls where the
// operator's input field accepts them) needed to drive a single
// Process round-trip for the manifest-vs-runtime parity sweep below.
// The expectedN/expectedNNull pair documents the universal-floor
// outcome the orchestrator's per-record bookkeeping is expected to
// reach after the records pass through the operator's null-handling
// branch (e.g. AGG_NULL_COUNT inverts the floor — its "non-null
// inputs" are the rows whose field IS null).
type aggParityFixture struct {
	schema       *encoding.Schema
	field        string
	params       json.RawMessage
	records      []*Record
	expectedN    int
	expectedNull int
	// filteredRecords is the total number of records the orchestrator
	// sees (post-filter). N + NNull must be ≤ filteredRecords for the
	// universal-floor invariant. The fixture pins this to len(records)
	// since the parity sweep applies no filterers.
	filteredRecords int
}

// numericParityRecords builds a 10-row cohort of numeric scores with
// two nulls at indices 4 and 7. Values are well-spread so every
// numeric aggregator produces a defensible result (no zero-variance
// degeneracies for Welford-family operators).
func numericParityRecords(schema *encoding.Schema) []*Record {
	values := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	return makeRecordsWithNulls(schema, "score", values, []int{4, 7})
}

func setParityRecords(schema *encoding.Schema) []*Record {
	masks := []uint64{
		0b0001, // VISA
		0b0010, // MC
		0b0011, // VISA, MC
		0b0100, // AMEX
		0b0000, // empty
		0b0111, // VISA, MC, AMEX
		0b1000, // DISC
		0b0000, // empty
		0b1100, // AMEX, DISC
		0b1111, // all
	}
	records := make([]*Record, len(masks))
	for i, mask := range masks {
		records[i] = makeSetRecord(schema, mask)
	}
	return records
}

// allAggParityFixtures returns one fixture per registered aggregator
// type — the parity sweep below exercises every entry against the
// manifest's components_schemas.aggregators block.
//
// The 10-row cohort + 2-null convention bounds the universal-floor
// assertion at N+NNull <= 10. Set aggregators carry NNull=0 because
// the set_* field type's zero mask is a valid "no selection" value
// distinct from null — the fixture pins them at N=10. AGG_NULL_COUNT
// inverts the floor: its "non-null inputs" are the rows whose field
// IS null, so N=2 and NNull=8 against the same 10-row cohort.
func allAggParityFixtures(t *testing.T) map[types.AggregationType]aggParityFixture {
	t.Helper()
	numSchema := numericSchema()
	numRecs := numericParityRecords(numSchema)

	cohSchema := cohortSchema()
	// 10 rows: value, weight, num, den. Two rows have weight==0 (which
	// the WEIGHTED_MEAN aggregator skips internally) but the cohort's
	// `value` field is non-null throughout, so the orchestrator's
	// universal floor counts ten non-null contributions.
	cohRecs := []*Record{
		cohortRec(10, 1, 5, 2, nil),
		cohortRec(20, 1, 10, 4, nil),
		cohortRec(30, 2, 15, 6, nil),
		cohortRec(40, 1, 20, 8, nil),
		cohortRec(50, 0, 25, 10, nil),
		cohortRec(60, 2, 30, 12, nil),
		cohortRec(70, 1, 35, 14, nil),
		cohortRec(80, 0, 40, 16, nil),
		cohortRec(90, 1, 45, 18, nil),
		cohortRec(100, 2, 50, 20, nil),
	}

	setSchema := makeSetTestSchema(t)
	setRecs := setParityRecords(setSchema)

	num := func() aggParityFixture {
		return aggParityFixture{
			schema:          numSchema,
			field:           "score",
			records:         numRecs,
			expectedN:       8,
			expectedNull:    2,
			filteredRecords: 10,
		}
	}
	coh := func(params json.RawMessage) aggParityFixture {
		return aggParityFixture{
			schema:          cohSchema,
			field:           "value",
			params:          params,
			records:         cohRecs,
			expectedN:       10,
			expectedNull:    0,
			filteredRecords: 10,
		}
	}
	set := func() aggParityFixture {
		return aggParityFixture{
			schema:          setSchema,
			field:           "tags",
			records:         setRecs,
			expectedN:       0,
			expectedNull:    10,
			filteredRecords: 10,
		}
	}

	fixtures := map[types.AggregationType]aggParityFixture{
		// Scalar / numeric ops over the f64 score cohort. AGG_NULL_COUNT
		// inverts the floor (N=null-count, NNull=non-null-count) per the
		// per-record bookkeeping contract in service/orchestrator.
		types.AGG_COUNT:          num(),
		types.AGG_SUM:            num(),
		types.AGG_AVERAGE:        num(),
		types.AGG_MIN:            num(),
		types.AGG_MAX:            num(),
		types.AGG_STDDEV:         num(),
		types.AGG_RANGE:          num(),
		types.AGG_FREQUENCY:      num(),
		types.AGG_ZSCORE:         num(),
		types.AGG_MEDIAN:         num(),
		types.AGG_VARIANCE:       num(),
		types.AGG_MODE:           num(),
		types.AGG_SKEWNESS:       num(),
		types.AGG_KURTOSIS:       num(),
		types.AGG_DISTINCT_COUNT: num(),
		// AGG_NULL_COUNT — the orchestrator's universal floor measures
		// "rows whose field was null" (NNull) and "rows whose field was
		// non-null" (N) via Record.NumericValue. The aggregator's own
		// scalar return inverts these (it counts null rows); the floor
		// stays semantically tied to the input bookkeeping, not the
		// operator's output meaning. Mirrors AGG_COUNT here.
		types.AGG_NULL_COUNT: num(),
		types.AGG_PERCENTILE: {
			schema:          numSchema,
			field:           "score",
			params:          json.RawMessage(`{"percentile":75}`),
			records:         numRecs,
			expectedN:       8,
			expectedNull:    2,
			filteredRecords: 10,
		},

		// Welford-strict-scalar — uses the numeric cohort; the WELFORD
		// factory restricts to f32/f64/u8/u16/u32/u64 (no decimal).
		types.AGG_WELFORD:  num(),
		types.AGG_CI_LOWER: num(),
		types.AGG_CI_UPPER: num(),

		// Composite ops use the cohort schema (value + weight + num + den).
		types.AGG_WEIGHTED_MEAN: coh(json.RawMessage(`{"weight_field":"weight"}`)),
		types.AGG_RATIO:         coh(json.RawMessage(`{"numerator_field":"num","denominator_field":"den"}`)),
		types.AGG_DISTINCT_SUM:  coh(json.RawMessage(`{"distinct_by":"num"}`)),

		// Set ops use the set_u8 dictionary cohort.
		types.AGG_SET_UNION:           set(),
		types.AGG_SET_INTERSECTION:    set(),
		types.AGG_SET_FREQUENCY:       set(),
		types.AGG_SET_CARDINALITY_SUM: set(),
		types.AGG_SET_CARDINALITY_AVG: set(),
		types.AGG_SET_DISTINCT_VALUES: set(),
	}

	// Sanity: the fixture map must cover every registered aggregator.
	for _, op := range types.AllAggregationTypes() {
		if _, ok := fixtures[op]; !ok {
			t.Fatalf("allAggParityFixtures missing fixture for %s — add a row above", op)
		}
	}
	return fixtures
}

// manifestAggregatorOperatorKeys returns the operator-specific key set
// (manifest schema MINUS the universal floor {"n", "n_null"}) for the
// named aggregator. Sorted ascending. Sourced from the
// public BuildManifest projection so this test gates the same surface
// LLM clients consume rather than the private capabilities table.
func manifestAggregatorOperatorKeys(t *testing.T, name string) []string {
	t.Helper()
	m := descriptor.BuildManifest()
	schema, ok := m.ComponentsSchemas.Aggregators[name]
	if !ok {
		t.Fatalf("manifest carries no components schema for %s", name)
	}
	out := make([]string, 0, len(schema.Keys))
	for _, k := range schema.Keys {
		// Strip universal floor — those keys are typed fields on
		// AggregationComponents (N, NNull), not in the per-operator
		// Operator map.
		if k.Name == "n" || k.Name == "n_null" {
			continue
		}
		out = append(out, k.Name)
	}
	sort.Strings(out)
	return out
}

// mapKeysSorted returns the sorted key set of m. Used to compare the
// runtime-emitted operator map against the manifest's declared keys.
func mapKeysSorted(m map[string]any) []string {
	if len(m) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestMetaAggregator_AllOps_ManifestParity(t *testing.T) {
	fixtures := allAggParityFixtures(t)
	for _, op := range types.AllAggregationTypes() {
		op := op
		fix := fixtures[op]
		t.Run(string(op), func(t *testing.T) {
			req := &types.Request{
				Aggregations: []*types.Aggregation{
					{Type: op, Field: fix.field, Label: "primary", Params: fix.params},
				},
			}
			proc := NewProcessor(fix.schema)
			resp, err := proc.processRecords(context.Background(), req, fix.records)
			if err != nil {
				t.Fatalf("processRecords: %v", err)
			}
			if resp.Components == nil {
				t.Fatalf("Response.Components nil; want populated")
			}
			if got := len(resp.Components.Aggregations); got != 1 {
				t.Fatalf("Aggregations slots = %d, want 1", got)
			}
			entry := resp.Components.Aggregations[0]
			if entry.Label != "primary" {
				t.Errorf("Label = %q, want %q", entry.Label, "primary")
			}
			if entry.N != fix.expectedN {
				t.Errorf("N = %d, want %d", entry.N, fix.expectedN)
			}
			if entry.NNull != fix.expectedNull {
				t.Errorf("NNull = %d, want %d", entry.NNull, fix.expectedNull)
			}

			wantKeys := manifestAggregatorOperatorKeys(t, string(op))
			gotKeys := mapKeysSorted(entry.Operator)
			if !reflect.DeepEqual(gotKeys, wantKeys) {
				t.Errorf("%s operator key set mismatch:\n  runtime emit: %v\n  manifest schema: %v",
					op, gotKeys, wantKeys)
			}
		})
	}
}

func TestComponentsUniversalFloor(t *testing.T) {
	fixtures := allAggParityFixtures(t)
	for _, op := range types.AllAggregationTypes() {
		op := op
		fix := fixtures[op]
		t.Run(string(op), func(t *testing.T) {
			req := &types.Request{
				Aggregations: []*types.Aggregation{
					{Type: op, Field: fix.field, Label: "x", Params: fix.params},
				},
			}
			proc := NewProcessor(fix.schema)
			resp, err := proc.processRecords(context.Background(), req, fix.records)
			if err != nil {
				t.Fatalf("processRecords: %v", err)
			}
			if resp.Components == nil || len(resp.Components.Aggregations) != 1 {
				t.Fatalf("Components shape wrong: %+v", resp.Components)
			}
			entry := resp.Components.Aggregations[0]
			if entry.N < 0 {
				t.Errorf("%s: N = %d, want >= 0", op, entry.N)
			}
			if entry.NNull < 0 {
				t.Errorf("%s: NNull = %d, want >= 0", op, entry.NNull)
			}
			if entry.N+entry.NNull > fix.filteredRecords {
				t.Errorf("%s: N (%d) + NNull (%d) = %d, want <= %d",
					op, entry.N, entry.NNull, entry.N+entry.NNull, fix.filteredRecords)
			}
		})
	}
}
