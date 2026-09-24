package processing

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// Wide-set coverage for the six set aggregators. The narrow cases live
// in aggregator_set_test.go / aggregator_set_components_test.go and must
// keep passing unmodified; everything here runs a dictionary that goes
// past bit 63, which is where a uint64-shaped aggregator returns a
// plausible wrong answer instead of an error.
//
// The motivating shape is the SPSS 206-option multiple-response set, so
// aggWideDictSize is 206 — wide enough that bits land in all four words
// of encoding.SetMask and narrow enough to leave the top of a set_u256
// unused, which is where an off-by-one in a width cap shows up.

const aggWideDictSize = 206

// aggWideBoundaryBits are the bit indices a [4]uint64 mask gets wrong
// when a word index is computed with the wrong divisor or a shift
// overflows: the first and last bit of the address space Pulse actually
// populates here, plus both sides of every 64-bit word boundary.
var aggWideBoundaryBits = []int{0, 63, 64, 127, 128, 191, 192, 205}

// aggWideLabel is the dictionary label assigned to bit i. Zero-padded so
// insertion order (which is bit order) is also lexical order.
func aggWideLabel(i int) string { return fmt.Sprintf("W%03d", i) }

// aggWideSchema builds a one-field set_u256 schema whose dictionary
// carries aggWideDictSize entries, bit i ↔ aggWideLabel(i).
func aggWideSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	dict := encoding.NewDictionary()
	for i := 0; i < aggWideDictSize; i++ {
		if _, err := dict.Add(aggWideLabel(i)); err != nil {
			t.Fatalf("dict.Add(%d): %v", i, err)
		}
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "tags", Type: encoding.FieldTypeSetU256, Nullable: true, Dictionary: dict},
		},
	}
}

// aggWideRecord builds a Record carrying the given mask on "tags" the
// way the wide decode path does: an encoding.SetMask in the wide map.
func aggWideRecord(schema *encoding.Schema, m encoding.SetMask) *Record {
	return NewRecordWithWide(schema, map[string]float64{}, nil, map[string]any{"tags": m})
}

// aggWideNullRecord builds a Record whose "tags" field is null — the
// per-record bitmap signal, not an empty mask.
func aggWideNullRecord(schema *encoding.Schema) *Record {
	return NewRecordWithNulls(schema,
		map[string]float64{"tags": 0},
		map[string]bool{"tags": true})
}

// aggMask returns a SetMask with exactly the listed bits set.
func aggMask(bits ...int) encoding.SetMask {
	var m encoding.SetMask
	for _, b := range bits {
		m = m.WithBit(b)
	}
	return m
}

// aggWideLabels maps bit indices to their dictionary labels, in the
// ascending bit order every label-emitting set surface promises.
func aggWideLabels(bits ...int) []string {
	out := make([]string, 0, len(bits))
	for _, b := range bits {
		out = append(out, aggWideLabel(b))
	}
	return out
}

// newWideSetAgg constructs the named set aggregator against the wide
// schema.
func newWideSetAgg(t *testing.T, typ types.AggregationType, schema *encoding.Schema) Aggregator {
	t.Helper()
	return makeAggregator(t, typ, "tags", schema)
}

// streamWideSetAgg drives the streaming (UpdateRow + Finalize) arm and
// returns the scalar. Every set aggregator must produce the same answer
// here as through the buffered Aggregate arm.
func streamWideSetAgg(t *testing.T, agg Aggregator, recs []*Record) float64 {
	t.Helper()
	online, ok := agg.(OnlineAggregator)
	if !ok {
		t.Fatalf("%T does not implement OnlineAggregator", agg)
	}
	for i, r := range recs {
		if err := online.UpdateRow(r, "tags"); err != nil {
			t.Fatalf("UpdateRow(%d): %v", i, err)
		}
	}
	got, err := online.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	return got
}

// mergeWideSetAgg folds `left` and `right` through two independent
// aggregator instances and merges the second into the first — the shape
// the per-shard and per-segment parallel reducers use. Returns the
// merged instance post-Finalize.
func mergeWideSetAgg(t *testing.T, typ types.AggregationType, schema *encoding.Schema,
	left, right []*Record,
) (Aggregator, float64) {
	t.Helper()
	a := newWideSetAgg(t, typ, schema)
	b := newWideSetAgg(t, typ, schema)
	aOnline, aOK := a.(OnlineAggregator)
	bOnline, bOK := b.(OnlineAggregator)
	if !aOK || !bOK {
		t.Fatalf("%s: both instances must be OnlineAggregator", typ)
	}
	for _, r := range left {
		if err := aOnline.UpdateRow(r, "tags"); err != nil {
			t.Fatalf("left UpdateRow: %v", err)
		}
	}
	for _, r := range right {
		if err := bOnline.UpdateRow(r, "tags"); err != nil {
			t.Fatalf("right UpdateRow: %v", err)
		}
	}
	merger, ok := aOnline.(interface{ MergeOnline(OnlineAggregator) error })
	if !ok {
		t.Fatalf("%s does not implement MergeOnline", typ)
	}
	if err := merger.MergeOnline(bOnline); err != nil {
		t.Fatalf("MergeOnline: %v", err)
	}
	scalar, err := aOnline.Finalize()
	if err != nil {
		t.Fatalf("merged Finalize: %v", err)
	}
	return a, scalar
}

// aggSetComponentsOf reads the MetaAggregator operator map off an aggregator
// that has already been finalized.
func aggSetComponentsOf(t *testing.T, agg Aggregator) map[string]any {
	t.Helper()
	meta, ok := agg.(MetaAggregator)
	if !ok {
		t.Fatalf("%T does not implement MetaAggregator", agg)
	}
	got, err := meta.Components()
	if err != nil {
		t.Fatalf("Components: %v", err)
	}
	return got
}

// TestSetAggWide_Union_FoldAcrossAllFourWords folds four records that
// between them touch every word of the 256-bit address space. Under the
// old uint64 accumulator the three upper words were unreachable: the
// union collapsed to the two bits below 64 and reported popcount 2.
func TestSetAggWide_Union_FoldAcrossAllFourWords(t *testing.T) {
	schema := aggWideSchema(t)
	recs := []*Record{
		aggWideRecord(schema, aggMask(0, 63)),
		aggWideRecord(schema, aggMask(64, 127)),
		aggWideNullRecord(schema),
		aggWideRecord(schema, aggMask(128, 191)),
		aggWideRecord(schema, aggMask(192, 205)),
	}
	wantMask := aggMask(aggWideBoundaryBits...)
	wantLabels := aggWideLabels(aggWideBoundaryBits...)

	// Buffered arm.
	agg := newWideSetAgg(t, types.AGG_SET_UNION, schema)
	scalar, err := agg.Aggregate(recs, "tags")
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if scalar != float64(len(aggWideBoundaryBits)) {
		t.Errorf("buffered scalar = %v, want %d", scalar, len(aggWideBoundaryBits))
	}
	rich, err := agg.(RichAggregator).Rich()
	if err != nil {
		t.Fatalf("Rich: %v", err)
	}
	if !reflect.DeepEqual(rich, wantLabels) {
		t.Errorf("buffered Rich labels = %v, want %v", rich, wantLabels)
	}
	comps := aggSetComponentsOf(t, agg)
	wantWords := wantMask.Words()
	if !reflect.DeepEqual(comps["mask_union"], wantWords[:]) {
		t.Errorf("mask_union = %v, want %v", comps["mask_union"], wantWords[:])
	}
	if comps["popcount"] != len(aggWideBoundaryBits) {
		t.Errorf("popcount = %v, want %d", comps["popcount"], len(aggWideBoundaryBits))
	}
	if !reflect.DeepEqual(comps["labels"], wantLabels) {
		t.Errorf("components labels = %v, want %v", comps["labels"], wantLabels)
	}

	// Streaming arm must agree with the buffered arm.
	streamed := newWideSetAgg(t, types.AGG_SET_UNION, schema)
	if got := streamWideSetAgg(t, streamed, recs); got != scalar {
		t.Errorf("streaming scalar = %v, want %v", got, scalar)
	}
	if got := aggSetComponentsOf(t, streamed); !reflect.DeepEqual(got, comps) {
		t.Errorf("streaming components = %v, want %v", got, comps)
	}

	// Parallel-reducer arm: two partials merged must equal the whole.
	merged, mergedScalar := mergeWideSetAgg(t, types.AGG_SET_UNION, schema, recs[:2], recs[2:])
	if mergedScalar != scalar {
		t.Errorf("merged scalar = %v, want %v", mergedScalar, scalar)
	}
	if got := aggSetComponentsOf(t, merged); !reflect.DeepEqual(got, comps) {
		t.Errorf("merged components = %v, want %v", got, comps)
	}
}

// TestSetAggWide_Intersection_FoldAcrossAllFourWords pins the AND fold
// at 256 bits. The shared bits are deliberately placed one per word so a
// word-index bug drops exactly one of them.
func TestSetAggWide_Intersection_FoldAcrossAllFourWords(t *testing.T) {
	schema := aggWideSchema(t)
	shared := []int{7, 70, 140, 205}
	recs := []*Record{
		aggWideRecord(schema, aggMask(append([]int{1, 65}, shared...)...)),
		aggWideRecord(schema, aggMask(append([]int{1, 130}, shared...)...)),
		aggWideNullRecord(schema),
		aggWideRecord(schema, aggMask(append([]int{200}, shared...)...)),
	}
	wantMask := aggMask(shared...)
	wantLabels := aggWideLabels(shared...)

	agg := newWideSetAgg(t, types.AGG_SET_INTERSECTION, schema)
	scalar, err := agg.Aggregate(recs, "tags")
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if scalar != float64(len(shared)) {
		t.Errorf("buffered scalar = %v, want %d", scalar, len(shared))
	}
	rich, err := agg.(RichAggregator).Rich()
	if err != nil {
		t.Fatalf("Rich: %v", err)
	}
	if !reflect.DeepEqual(rich, wantLabels) {
		t.Errorf("Rich labels = %v, want %v", rich, wantLabels)
	}
	comps := aggSetComponentsOf(t, agg)
	wantWords := wantMask.Words()
	if !reflect.DeepEqual(comps["mask_intersection"], wantWords[:]) {
		t.Errorf("mask_intersection = %v, want %v", comps["mask_intersection"], wantWords[:])
	}
	if comps["popcount"] != len(shared) {
		t.Errorf("popcount = %v, want %d", comps["popcount"], len(shared))
	}

	streamed := newWideSetAgg(t, types.AGG_SET_INTERSECTION, schema)
	if got := streamWideSetAgg(t, streamed, recs); got != scalar {
		t.Errorf("streaming scalar = %v, want %v", got, scalar)
	}
	if got := aggSetComponentsOf(t, streamed); !reflect.DeepEqual(got, comps) {
		t.Errorf("streaming components = %v, want %v", got, comps)
	}

	merged, mergedScalar := mergeWideSetAgg(t, types.AGG_SET_INTERSECTION, schema, recs[:2], recs[2:])
	if mergedScalar != scalar {
		t.Errorf("merged scalar = %v, want %v", mergedScalar, scalar)
	}
	if got := aggSetComponentsOf(t, merged); !reflect.DeepEqual(got, comps) {
		t.Errorf("merged components = %v, want %v", got, comps)
	}
}

// TestSetAggWide_Frequency_206Members is the motivating SPSS case: every
// one of the 206 dictionary members must appear in the per-label map
// with its true row count, including the 142 members that live at or
// above bit 64. Row r selects member i when i%4 >= r, so member i is
// counted (i%4)+1 times.
func TestSetAggWide_Frequency_206Members(t *testing.T) {
	schema := aggWideSchema(t)
	const rows = 4
	recs := make([]*Record, 0, rows+1)
	for r := 0; r < rows; r++ {
		var m encoding.SetMask
		for i := 0; i < aggWideDictSize; i++ {
			if i%4 >= r {
				m = m.WithBit(i)
			}
		}
		recs = append(recs, aggWideRecord(schema, m))
	}
	recs = append(recs, aggWideNullRecord(schema))

	wantPerLabel := make(map[string]int, aggWideDictSize)
	wantTotal := 0
	for i := 0; i < aggWideDictSize; i++ {
		c := i%4 + 1
		wantPerLabel[aggWideLabel(i)] = c
		wantTotal += c
	}

	agg := newWideSetAgg(t, types.AGG_SET_FREQUENCY, schema)
	scalar, err := agg.Aggregate(recs, "tags")
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	// Scalar fallback is the max-bin count: members with i%4 == 3 are
	// selected by all four rows.
	if scalar != 4 {
		t.Errorf("scalar = %v, want 4", scalar)
	}
	rich, err := agg.(RichAggregator).Rich()
	if err != nil {
		t.Fatalf("Rich: %v", err)
	}
	gotMap, ok := rich.(map[string]int)
	if !ok {
		t.Fatalf("Rich is %T, want map[string]int", rich)
	}
	if !reflect.DeepEqual(gotMap, wantPerLabel) {
		if len(gotMap) != len(wantPerLabel) {
			t.Fatalf("Rich map has %d entries, want %d — members above bit 63 dropped",
				len(gotMap), len(wantPerLabel))
		}
		for _, bit := range aggWideBoundaryBits {
			label := aggWideLabel(bit)
			if gotMap[label] != wantPerLabel[label] {
				t.Errorf("Rich[%s] = %d, want %d", label, gotMap[label], wantPerLabel[label])
			}
		}
		t.Errorf("Rich map differs from expectation")
	}

	comps := aggSetComponentsOf(t, agg)
	if comps["total_label_observations"] != wantTotal {
		t.Errorf("total_label_observations = %v, want %d", comps["total_label_observations"], wantTotal)
	}
	if comps["distinct_labels"] != aggWideDictSize {
		t.Errorf("distinct_labels = %v, want %d", comps["distinct_labels"], aggWideDictSize)
	}
	if !reflect.DeepEqual(comps["per_label_count"], wantPerLabel) {
		t.Errorf("per_label_count differs from the expected 206-entry map")
	}

	streamed := newWideSetAgg(t, types.AGG_SET_FREQUENCY, schema)
	if got := streamWideSetAgg(t, streamed, recs); got != scalar {
		t.Errorf("streaming scalar = %v, want %v", got, scalar)
	}
	if got := aggSetComponentsOf(t, streamed); !reflect.DeepEqual(got, comps) {
		t.Errorf("streaming components differ from buffered")
	}

	merged, _ := mergeWideSetAgg(t, types.AGG_SET_FREQUENCY, schema, recs[:2], recs[2:])
	if got := aggSetComponentsOf(t, merged); !reflect.DeepEqual(got, comps) {
		t.Errorf("merged components differ from buffered — per-bit counters did not merge above bit 63")
	}
}

// TestSetAggWide_CardinalityOver64 covers AGG_SET_CARDINALITY_SUM and
// AGG_SET_CARDINALITY_AVG when a SINGLE row selects more than 64
// members. bits.OnesCount64 over a narrowed low word would have capped
// each row's contribution at 64.
func TestSetAggWide_CardinalityOver64(t *testing.T) {
	schema := aggWideSchema(t)
	rowA := aggMask(aggSeqBits(0, 100)...)   // popcount 100
	rowB := aggMask(aggSeqBits(100, 106)...) // popcount 106, all at or above bit 100
	recs := []*Record{
		aggWideRecord(schema, rowA),
		aggWideRecord(schema, rowB),
		aggWideNullRecord(schema),
	}
	if got := rowA.PopCount(); got != 100 {
		t.Fatalf("fixture rowA popcount = %d, want 100", got)
	}
	if got := rowB.PopCount(); got != 106 {
		t.Fatalf("fixture rowB popcount = %d, want 106", got)
	}
	const wantSum = 206
	const wantAvg = 103.0

	sumAgg := newWideSetAgg(t, types.AGG_SET_CARDINALITY_SUM, schema)
	gotSum, err := sumAgg.Aggregate(recs, "tags")
	if err != nil {
		t.Fatalf("CARDINALITY_SUM Aggregate: %v", err)
	}
	if gotSum != wantSum {
		t.Errorf("CARDINALITY_SUM = %v, want %d", gotSum, wantSum)
	}
	if got := aggSetComponentsOf(t, sumAgg)["sum_cardinality"]; got != wantSum {
		t.Errorf("sum_cardinality = %v, want %d", got, wantSum)
	}

	avgAgg := newWideSetAgg(t, types.AGG_SET_CARDINALITY_AVG, schema)
	gotAvg, err := avgAgg.Aggregate(recs, "tags")
	if err != nil {
		t.Fatalf("CARDINALITY_AVG Aggregate: %v", err)
	}
	if gotAvg != wantAvg {
		t.Errorf("CARDINALITY_AVG = %v, want %v", gotAvg, wantAvg)
	}
	avgComps := aggSetComponentsOf(t, avgAgg)
	if avgComps["sum_cardinality"] != wantSum {
		t.Errorf("avg.sum_cardinality = %v, want %d", avgComps["sum_cardinality"], wantSum)
	}
	if avgComps["avg_cardinality"] != wantAvg {
		t.Errorf("avg_cardinality = %v, want %v", avgComps["avg_cardinality"], wantAvg)
	}

	// Streaming + merge parity on both.
	for _, typ := range []types.AggregationType{
		types.AGG_SET_CARDINALITY_SUM,
		types.AGG_SET_CARDINALITY_AVG,
	} {
		want := gotSum
		if typ == types.AGG_SET_CARDINALITY_AVG {
			want = gotAvg
		}
		streamed := newWideSetAgg(t, typ, schema)
		if got := streamWideSetAgg(t, streamed, recs); got != want {
			t.Errorf("%s streaming = %v, want %v", typ, got, want)
		}
		if _, got := mergeWideSetAgg(t, typ, schema, recs[:1], recs[1:]); got != want {
			t.Errorf("%s merged = %v, want %v", typ, got, want)
		}
	}
}

// TestSetAggWide_DistinctValues_DiffersOnlyAboveBit63 is the sharpest
// distinct-values case: three masks whose low 64 bits are IDENTICAL and
// which differ only at bits 64, 130 and 200. A uint64-keyed seen-set
// collapsed all three into one bucket and reported 1 distinct
// combination; the correct answer is 3.
func TestSetAggWide_DistinctValues_DiffersOnlyAboveBit63(t *testing.T) {
	schema := aggWideSchema(t)
	recs := []*Record{
		aggWideRecord(schema, aggMask(1, 2, 64)),
		aggWideRecord(schema, aggMask(1, 2, 130)),
		aggWideRecord(schema, aggMask(1, 2, 200)),
		aggWideRecord(schema, aggMask(1, 2, 130)), // repeat — not a new value
		aggWideNullRecord(schema),
	}

	agg := newWideSetAgg(t, types.AGG_SET_DISTINCT_VALUES, schema)
	scalar, err := agg.Aggregate(recs, "tags")
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if scalar != 3 {
		t.Errorf("distinct values = %v, want 3", scalar)
	}
	wantUnion := aggMask(1, 2, 64, 130, 200)
	wantWords := wantUnion.Words()
	comps := aggSetComponentsOf(t, agg)
	if !reflect.DeepEqual(comps["mask_union"], wantWords[:]) {
		t.Errorf("mask_union = %v, want %v", comps["mask_union"], wantWords[:])
	}
	if comps["popcount"] != 5 {
		t.Errorf("popcount = %v, want 5", comps["popcount"])
	}
	if !reflect.DeepEqual(comps["labels"], aggWideLabels(1, 2, 64, 130, 200)) {
		t.Errorf("labels = %v, want %v", comps["labels"], aggWideLabels(1, 2, 64, 130, 200))
	}

	streamed := newWideSetAgg(t, types.AGG_SET_DISTINCT_VALUES, schema)
	if got := streamWideSetAgg(t, streamed, recs); got != scalar {
		t.Errorf("streaming = %v, want %v", got, scalar)
	}
	merged, mergedScalar := mergeWideSetAgg(t, types.AGG_SET_DISTINCT_VALUES, schema, recs[:2], recs[2:])
	if mergedScalar != scalar {
		t.Errorf("merged = %v, want %v", mergedScalar, scalar)
	}
	if got := aggSetComponentsOf(t, merged); !reflect.DeepEqual(got, comps) {
		t.Errorf("merged components differ from buffered")
	}
}

// TestSetAggWide_UniversalFloor drives the orchestrator so the universal
// {n, n_null} floor is exercised rather than the aggregator in
// isolation. An EMPTY MASK IS PRESENT — a respondent who selected
// nothing still answered — so only the bitmap-null rows land in n_null.
func TestSetAggWide_UniversalFloor(t *testing.T) {
	schema := aggWideSchema(t)
	recs := []*Record{
		aggWideRecord(schema, aggMask(0, 205)),
		aggWideRecord(schema, encoding.SetMask{}), // empty selection, NOT null
		aggWideRecord(schema, aggMask(64)),
		aggWideNullRecord(schema),
		aggWideNullRecord(schema),
	}
	for _, typ := range []types.AggregationType{
		types.AGG_SET_UNION,
		types.AGG_SET_INTERSECTION,
		types.AGG_SET_FREQUENCY,
		types.AGG_SET_CARDINALITY_SUM,
		types.AGG_SET_CARDINALITY_AVG,
		types.AGG_SET_DISTINCT_VALUES,
		types.AGG_COUNT,
		types.AGG_NULL_COUNT,
	} {
		typ := typ
		t.Run(string(typ), func(t *testing.T) {
			req := &types.Request{
				Aggregations: []*types.Aggregation{
					{Type: typ, Field: "tags", Label: "primary"},
				},
			}
			proc := NewProcessor(schema)
			resp, err := proc.processRecords(context.Background(), req, recs)
			if err != nil {
				t.Fatalf("processRecords: %v", err)
			}
			if resp.Components == nil || len(resp.Components.Aggregations) != 1 {
				t.Fatalf("Components shape wrong: %+v", resp.Components)
			}
			entry := resp.Components.Aggregations[0]
			if entry.N != 3 {
				t.Errorf("N = %d, want 3", entry.N)
			}
			if entry.NNull != 2 {
				t.Errorf("NNull = %d, want 2", entry.NNull)
			}
		})
	}
}

// TestSetAggWide_CountAndNullCountScalars pins the AGG_COUNT /
// AGG_NULL_COUNT SCALARS on a wide-set column, not just their floor.
// Both accept every cohort field type, so both must count presence —
// AGG_COUNT read 0 for an entire multi-select column while it asked its
// question through the numeric accessor.
func TestSetAggWide_CountAndNullCountScalars(t *testing.T) {
	schema := aggWideSchema(t)
	recs := []*Record{
		aggWideRecord(schema, aggMask(0, 205)),
		aggWideRecord(schema, encoding.SetMask{}),
		aggWideRecord(schema, aggMask(64)),
		aggWideNullRecord(schema),
		aggWideNullRecord(schema),
	}
	cases := []struct {
		typ  types.AggregationType
		want float64
	}{
		{types.AGG_COUNT, 3},
		{types.AGG_NULL_COUNT, 2},
	}
	for _, c := range cases {
		c := c
		t.Run(string(c.typ), func(t *testing.T) {
			// Buffered arm.
			agg := newWideSetAgg(t, c.typ, schema)
			got, err := agg.Aggregate(recs, "tags")
			if err != nil {
				t.Fatalf("Aggregate: %v", err)
			}
			if got != c.want {
				t.Errorf("buffered %s = %v, want %v", c.typ, got, c.want)
			}
			// Streaming arm.
			streamed := newWideSetAgg(t, c.typ, schema)
			if got := streamWideSetAgg(t, streamed, recs); got != c.want {
				t.Errorf("streaming %s = %v, want %v", c.typ, got, c.want)
			}
		})
	}
}

// TestRecordNumericValue_RefusesSetFields pins the guard: a set field is
// never numeric, at any rung. The float64 echo the decoder writes is the
// low 64 bits of a wide mask and is lossy from set_u64 up, so returning
// it is a plausible wrong number rather than an error. Presence rides
// FieldPresent instead.
func TestRecordNumericValue_RefusesSetFields(t *testing.T) {
	wideSchema := aggWideSchema(t)
	narrowSchema := makeSetTestSchema(t)

	// A wide record carrying bit 200 — the decoder's echo for this row
	// would be 0 (the low word is empty), which is indistinguishable
	// from a row that selected nothing.
	wide := aggWideRecord(wideSchema, aggMask(200))
	wide.values["tags"] = 0 // the low-64 echo the decoder writes
	if v, ok := wide.NumericValue("tags"); ok {
		t.Errorf("NumericValue on set_u256 returned (%v, true); want refusal", v)
	}
	if !FieldPresent(wide, "tags") {
		t.Error("FieldPresent on a present wide set field = false, want true")
	}

	// Narrow rungs refuse identically — one rule at every width.
	narrow := makeSetRecord(narrowSchema, 0b1011)
	narrow.values["tags"] = 11
	if v, ok := narrow.NumericValue("tags"); ok {
		t.Errorf("NumericValue on set_u8 returned (%v, true); want refusal", v)
	}
	if !FieldPresent(narrow, "tags") {
		t.Error("FieldPresent on a present narrow set field = false, want true")
	}

	// A null set field is absent on both accessors.
	null := aggWideNullRecord(wideSchema)
	if _, ok := null.NumericValue("tags"); ok {
		t.Error("NumericValue on a null set field reported present")
	}
	if FieldPresent(null, "tags") {
		t.Error("FieldPresent on a null set field = true, want false")
	}

	// Non-set fields are untouched: the guard must not swallow a plain
	// numeric column that happens to sit beside a set column.
	numeric := NewRecordWithWide(wideSchema,
		map[string]float64{"score": 42},
		nil,
		map[string]any{"tags": aggMask(3)})
	if v, ok := numeric.NumericValue("score"); !ok || v != 42 {
		t.Errorf("NumericValue(score) = (%v, %v), want (42, true)", v, ok)
	}
}

// aggSeqBits returns n consecutive bit indices starting at start.
func aggSeqBits(start, n int) []int {
	out := make([]int, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, start+i)
	}
	return out
}
