package processing

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// makeSetTestSchema builds a one-field schema with a set_u8 column
// whose dictionary holds 4 issuers. Bit 0 = VISA, 1 = MC, 2 = AMEX,
// 3 = DISC.
func makeSetTestSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	dict := encoding.NewDictionary()
	for _, v := range []string{"VISA", "MC", "AMEX", "DISC"} {
		if _, err := dict.Add(v); err != nil {
			t.Fatalf("dict.Add: %v", err)
		}
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "tags", Type: encoding.FieldTypeSetU8, Dictionary: dict},
		},
	}
}

// makeSetRecord builds a Record carrying the given mask on field "tags".
func makeSetRecord(schema *encoding.Schema, mask uint64) *Record {
	r := NewRecordWithWide(schema, map[string]float64{}, nil, map[string]any{"tags": mask})
	return r
}

// expectScalar pumps records through the aggregator's buffered path and
// asserts the returned scalar.
func expectScalar(t *testing.T, agg Aggregator, recs []*Record, want float64) {
	t.Helper()
	got, err := agg.Aggregate(recs, "tags")
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if got != want {
		t.Errorf("scalar = %v, want %v", got, want)
	}
}

func TestSetUnion_AggregateAndRich(t *testing.T) {
	schema := makeSetTestSchema(t)
	agg, err := newSetUnionAggregator(&types.Aggregation{Type: types.AGG_SET_UNION, Field: "tags"}, schema)
	if err != nil {
		t.Fatalf("newSetUnionAggregator: %v", err)
	}
	recs := []*Record{
		makeSetRecord(schema, 0b0011), // VISA, MC
		makeSetRecord(schema, 0b0110), // MC, AMEX
	}
	expectScalar(t, agg, recs, 3) // popcount(0b0111) = 3

	rich, err := agg.(RichAggregator).Rich()
	if err != nil {
		t.Fatalf("Rich: %v", err)
	}
	labels, ok := rich.([]string)
	if !ok {
		t.Fatalf("rich is %T, want []string", rich)
	}
	want := []string{"VISA", "MC", "AMEX"}
	if len(labels) != len(want) {
		t.Fatalf("labels = %v, want %v", labels, want)
	}
	for i := range want {
		if labels[i] != want[i] {
			t.Errorf("labels[%d] = %s, want %s (dict-bit order)", i, labels[i], want[i])
		}
	}
}

func TestSetIntersection_OnlyCommonBitsSurvive(t *testing.T) {
	schema := makeSetTestSchema(t)
	agg, err := newSetIntersectionAggregator(&types.Aggregation{Type: types.AGG_SET_INTERSECTION, Field: "tags"}, schema)
	if err != nil {
		t.Fatalf("newSetIntersectionAggregator: %v", err)
	}
	recs := []*Record{
		makeSetRecord(schema, 0b0111), // VISA, MC, AMEX
		makeSetRecord(schema, 0b0011), // VISA, MC
		makeSetRecord(schema, 0b1011), // VISA, MC, DISC
	}
	expectScalar(t, agg, recs, 2) // VISA + MC

	rich, err := agg.(RichAggregator).Rich()
	if err != nil {
		t.Fatalf("Rich: %v", err)
	}
	labels := rich.([]string)
	if len(labels) != 2 || labels[0] != "VISA" || labels[1] != "MC" {
		t.Errorf("labels = %v, want [VISA MC]", labels)
	}
}

func TestSetIntersection_EmptyInput(t *testing.T) {
	schema := makeSetTestSchema(t)
	agg, _ := newSetIntersectionAggregator(&types.Aggregation{Type: types.AGG_SET_INTERSECTION, Field: "tags"}, schema)
	expectScalar(t, agg, nil, 0)
	rich, _ := agg.(RichAggregator).Rich()
	if labels := rich.([]string); len(labels) != 0 {
		t.Errorf("empty intersection rich = %v, want []", labels)
	}
}

func TestSetFrequency_PerBitCounts(t *testing.T) {
	schema := makeSetTestSchema(t)
	agg, err := newSetFrequencyAggregator(&types.Aggregation{Type: types.AGG_SET_FREQUENCY, Field: "tags"}, schema)
	if err != nil {
		t.Fatalf("newSetFrequencyAggregator: %v", err)
	}
	recs := []*Record{
		makeSetRecord(schema, 0b0011), // VISA, MC
		makeSetRecord(schema, 0b0011), // VISA, MC
		makeSetRecord(schema, 0b0100), // AMEX
	}
	expectScalar(t, agg, recs, 2) // max count = 2 (VISA / MC tie)

	rich, err := agg.(RichAggregator).Rich()
	if err != nil {
		t.Fatalf("Rich: %v", err)
	}
	counts := rich.(map[string]int)
	if counts["VISA"] != 2 || counts["MC"] != 2 || counts["AMEX"] != 1 {
		t.Errorf("counts = %v, want VISA=2 MC=2 AMEX=1", counts)
	}
	if _, present := counts["DISC"]; present {
		t.Errorf("counts contains zero-count label DISC: %v", counts)
	}
}

func TestSetCardinalitySum_PopcountSummation(t *testing.T) {
	schema := makeSetTestSchema(t)
	agg, _ := newSetCardinalitySumAggregator(&types.Aggregation{Type: types.AGG_SET_CARDINALITY_SUM, Field: "tags"}, schema)
	recs := []*Record{
		makeSetRecord(schema, 0b0011), // 2
		makeSetRecord(schema, 0b0111), // 3
		makeSetRecord(schema, 0b0001), // 1
	}
	expectScalar(t, agg, recs, 6)
}

func TestSetCardinalityAvg_MeanPopcount(t *testing.T) {
	schema := makeSetTestSchema(t)
	agg, _ := newSetCardinalityAvgAggregator(&types.Aggregation{Type: types.AGG_SET_CARDINALITY_AVG, Field: "tags"}, schema)
	recs := []*Record{
		makeSetRecord(schema, 0b0011), // 2
		makeSetRecord(schema, 0b0111), // 3
	}
	expectScalar(t, agg, recs, 2.5)
}

func TestSetDistinctValues_AtomicCombinations(t *testing.T) {
	schema := makeSetTestSchema(t)
	agg, _ := newSetDistinctValuesAggregator(&types.Aggregation{Type: types.AGG_SET_DISTINCT_VALUES, Field: "tags"}, schema)
	recs := []*Record{
		makeSetRecord(schema, 0b0011),
		makeSetRecord(schema, 0b0011),
		makeSetRecord(schema, 0b0111),
		makeSetRecord(schema, 0b0000),
	}
	expectScalar(t, agg, recs, 3) // {0b0011, 0b0111, 0b0000}
}

func TestSetUnion_MergeOnline(t *testing.T) {
	schema := makeSetTestSchema(t)
	a, _ := newSetUnionAggregator(&types.Aggregation{Type: types.AGG_SET_UNION, Field: "tags"}, schema)
	b, _ := newSetUnionAggregator(&types.Aggregation{Type: types.AGG_SET_UNION, Field: "tags"}, schema)
	if err := a.(OnlineAggregator).UpdateRow(makeSetRecord(schema, 0b0011), "tags"); err != nil {
		t.Fatalf("UpdateRow: %v", err)
	}
	if err := b.(OnlineAggregator).UpdateRow(makeSetRecord(schema, 0b1100), "tags"); err != nil {
		t.Fatalf("UpdateRow: %v", err)
	}
	if err := a.(MergeableAggregator).MergeOnline(b.(OnlineAggregator)); err != nil {
		t.Fatalf("MergeOnline: %v", err)
	}
	got, err := a.(OnlineAggregator).Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if got != 4 {
		t.Errorf("merged popcount = %v, want 4", got)
	}
}

func TestSetIntersection_MergeOnline(t *testing.T) {
	schema := makeSetTestSchema(t)
	a, _ := newSetIntersectionAggregator(&types.Aggregation{Type: types.AGG_SET_INTERSECTION, Field: "tags"}, schema)
	b, _ := newSetIntersectionAggregator(&types.Aggregation{Type: types.AGG_SET_INTERSECTION, Field: "tags"}, schema)
	a.(OnlineAggregator).UpdateRow(makeSetRecord(schema, 0b0111), "tags")
	a.(OnlineAggregator).UpdateRow(makeSetRecord(schema, 0b0011), "tags")
	b.(OnlineAggregator).UpdateRow(makeSetRecord(schema, 0b1011), "tags")
	if err := a.(MergeableAggregator).MergeOnline(b.(OnlineAggregator)); err != nil {
		t.Fatalf("MergeOnline: %v", err)
	}
	// (0b0111 AND 0b0011) AND 0b1011 = 0b0011 (VISA + MC)
	got, _ := a.(OnlineAggregator).Finalize()
	if got != 2 {
		t.Errorf("merged intersection popcount = %v, want 2", got)
	}
}
