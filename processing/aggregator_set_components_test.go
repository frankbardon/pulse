package processing

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// runSetAggregateForComponents drives Aggregate over the supplied
// records via the buffered path and returns the emitted operator-
// specific map plus scalar. Mirrors runAggregateForComponents in
// aggregator_welford_components_test.go.
func runSetAggregateForComponents(t *testing.T, aggType types.AggregationType,
	schema *encoding.Schema, records []*Record,
) (map[string]any, float64) {
	t.Helper()
	agg := makeAggregator(t, aggType, "tags", schema)
	scalar, err := agg.Aggregate(records, "tags")
	if err != nil {
		t.Fatalf("%s.Aggregate: %v", aggType, err)
	}
	meta, ok := agg.(MetaAggregator)
	if !ok {
		t.Fatalf("%s does not implement MetaAggregator", aggType)
	}
	got, err := meta.Components()
	if err != nil {
		t.Fatalf("%s.Components: %v", aggType, err)
	}
	return got, scalar
}

// TestMetaAggregator_SetUnion_Components covers AGG_SET_UNION across
// multi-row, single-row, and empty input. mask_union must be the
// bitwise OR of every contributing row's set mask; popcount must match
// the bit count; labels must be the dictionary-decoded slice (ascending
// bit order, matches resolveMaskLabels semantics).
func TestMetaAggregator_SetUnion_Components(t *testing.T) {
	schema := makeSetTestSchema(t)
	tests := []struct {
		name string
		recs []*Record
		want map[string]any
	}{
		{
			name: "multiRow_orFold",
			recs: []*Record{
				makeSetRecord(schema, 0b0011), // VISA, MC
				makeSetRecord(schema, 0b0110), // MC, AMEX
			},
			want: map[string]any{
				"mask_union": uint64(0b0111),
				"popcount":   3,
				"labels":     []string{"VISA", "MC", "AMEX"},
			},
		},
		{
			name: "singleRow",
			recs: []*Record{makeSetRecord(schema, 0b1000)},
			want: map[string]any{
				"mask_union": uint64(0b1000),
				"popcount":   1,
				"labels":     []string{"DISC"},
			},
		},
		{
			name: "empty",
			recs: nil,
			want: map[string]any{
				"mask_union": uint64(0),
				"popcount":   0,
				"labels":     []string{},
			},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, _ := runSetAggregateForComponents(t, types.AGG_SET_UNION, schema, tc.recs)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("components = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// TestMetaAggregator_SetIntersection_Components covers AGG_SET_INTERSECTION
// across multi-row, single-row, and empty input. mask_intersection must
// be the bitwise AND of every contributing row's set mask.
func TestMetaAggregator_SetIntersection_Components(t *testing.T) {
	schema := makeSetTestSchema(t)
	tests := []struct {
		name string
		recs []*Record
		want map[string]any
	}{
		{
			name: "multiRow_andFold",
			recs: []*Record{
				makeSetRecord(schema, 0b0111), // VISA, MC, AMEX
				makeSetRecord(schema, 0b0011), // VISA, MC
				makeSetRecord(schema, 0b1011), // VISA, MC, DISC
			},
			want: map[string]any{
				"mask_intersection": uint64(0b0011),
				"popcount":          2,
				"labels":            []string{"VISA", "MC"},
			},
		},
		{
			name: "singleRow",
			recs: []*Record{makeSetRecord(schema, 0b0101)},
			want: map[string]any{
				"mask_intersection": uint64(0b0101),
				"popcount":          2,
				"labels":            []string{"VISA", "AMEX"},
			},
		},
		{
			name: "emptyAndCollapse",
			// First row has VISA only; second row has DISC only → AND collapses to 0
			recs: []*Record{
				makeSetRecord(schema, 0b0001),
				makeSetRecord(schema, 0b1000),
			},
			want: map[string]any{
				"mask_intersection": uint64(0),
				"popcount":          0,
				"labels":            []string{},
			},
		},
		{
			name: "empty",
			recs: nil,
			want: map[string]any{
				"mask_intersection": uint64(0),
				"popcount":          0,
				"labels":            []string{},
			},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, _ := runSetAggregateForComponents(t, types.AGG_SET_INTERSECTION, schema, tc.recs)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("components = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// TestMetaAggregator_SetFrequency_Components covers AGG_SET_FREQUENCY.
// Verifies per_label_count map keyed by decoded label, plus the
// aggregate counters total_label_observations and distinct_labels.
func TestMetaAggregator_SetFrequency_Components(t *testing.T) {
	schema := makeSetTestSchema(t)
	tests := []struct {
		name string
		recs []*Record
		want map[string]any
	}{
		{
			name: "multiRow_perBitCounts",
			recs: []*Record{
				makeSetRecord(schema, 0b0011), // VISA, MC
				makeSetRecord(schema, 0b0011), // VISA, MC
				makeSetRecord(schema, 0b0100), // AMEX
			},
			want: map[string]any{
				"total_label_observations": 5, // 2 + 2 + 1
				"distinct_labels":          3,
				"per_label_count":          map[string]int{"VISA": 2, "MC": 2, "AMEX": 1},
			},
		},
		{
			name: "singleRow",
			recs: []*Record{makeSetRecord(schema, 0b0101)},
			want: map[string]any{
				"total_label_observations": 2,
				"distinct_labels":          2,
				"per_label_count":          map[string]int{"VISA": 1, "AMEX": 1},
			},
		},
		{
			name: "emptyMaskRow",
			// Row contributes mask=0 → no bits set → no per-label counts
			recs: []*Record{makeSetRecord(schema, 0)},
			want: map[string]any{
				"total_label_observations": 0,
				"distinct_labels":          0,
				"per_label_count":          map[string]int{},
			},
		},
		{
			name: "empty",
			recs: nil,
			want: map[string]any{
				"total_label_observations": 0,
				"distinct_labels":          0,
				"per_label_count":          map[string]int{},
			},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, _ := runSetAggregateForComponents(t, types.AGG_SET_FREQUENCY, schema, tc.recs)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("components = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// TestMetaAggregator_SetCardinalitySum_Components covers
// AGG_SET_CARDINALITY_SUM. sum_cardinality is the sum of popcounts
// across contributing rows.
func TestMetaAggregator_SetCardinalitySum_Components(t *testing.T) {
	schema := makeSetTestSchema(t)
	tests := []struct {
		name string
		recs []*Record
		want map[string]any
	}{
		{
			name: "multiRow",
			recs: []*Record{
				makeSetRecord(schema, 0b0011), // 2
				makeSetRecord(schema, 0b0111), // 3
				makeSetRecord(schema, 0b0001), // 1
			},
			want: map[string]any{"sum_cardinality": 6},
		},
		{
			name: "singleRow",
			recs: []*Record{makeSetRecord(schema, 0b1111)},
			want: map[string]any{"sum_cardinality": 4},
		},
		{
			name: "empty",
			recs: nil,
			want: map[string]any{"sum_cardinality": 0},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, _ := runSetAggregateForComponents(t, types.AGG_SET_CARDINALITY_SUM, schema, tc.recs)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("components = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// TestMetaAggregator_SetCardinalityAvg_Components covers
// AGG_SET_CARDINALITY_AVG. avg_cardinality == sum_cardinality /
// n_contributing_rows; empty input collapses to {0, 0.0}.
func TestMetaAggregator_SetCardinalityAvg_Components(t *testing.T) {
	schema := makeSetTestSchema(t)
	tests := []struct {
		name string
		recs []*Record
		want map[string]any
	}{
		{
			name: "multiRow",
			recs: []*Record{
				makeSetRecord(schema, 0b0011), // 2
				makeSetRecord(schema, 0b0111), // 3
			},
			want: map[string]any{
				"sum_cardinality": 5,
				"avg_cardinality": 2.5,
			},
		},
		{
			name: "singleRow",
			recs: []*Record{makeSetRecord(schema, 0b1010)},
			want: map[string]any{
				"sum_cardinality": 2,
				"avg_cardinality": 2.0,
			},
		},
		{
			name: "empty",
			recs: nil,
			want: map[string]any{
				"sum_cardinality": 0,
				"avg_cardinality": 0.0,
			},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, _ := runSetAggregateForComponents(t, types.AGG_SET_CARDINALITY_AVG, schema, tc.recs)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("components = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// TestMetaAggregator_SetDistinctValues_Components covers
// AGG_SET_DISTINCT_VALUES. The declared component schema surfaces the
// bitwise-OR union of every distinct mask seen — a strict superset of
// "any bit any row contributed". The scalar (handled separately by the
// existing TestSetDistinctValues_AtomicCombinations) is the count of
// distinct exact masks; Components() carries the union mask + popcount
// + decoded labels per descriptor/capabilities_aggregators.go.
func TestMetaAggregator_SetDistinctValues_Components(t *testing.T) {
	schema := makeSetTestSchema(t)
	tests := []struct {
		name string
		recs []*Record
		want map[string]any
	}{
		{
			name: "multiRow_distinctMasks",
			recs: []*Record{
				makeSetRecord(schema, 0b0011),
				makeSetRecord(schema, 0b0011), // duplicate
				makeSetRecord(schema, 0b0100),
			},
			// Union = 0b0011 | 0b0100 = 0b0111 → VISA, MC, AMEX
			want: map[string]any{
				"mask_union": uint64(0b0111),
				"popcount":   3,
				"labels":     []string{"VISA", "MC", "AMEX"},
			},
		},
		{
			name: "singleRow",
			recs: []*Record{makeSetRecord(schema, 0b1001)},
			want: map[string]any{
				"mask_union": uint64(0b1001),
				"popcount":   2,
				"labels":     []string{"VISA", "DISC"},
			},
		},
		{
			name: "empty",
			recs: nil,
			want: map[string]any{
				"mask_union": uint64(0),
				"popcount":   0,
				"labels":     []string{},
			},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, _ := runSetAggregateForComponents(t, types.AGG_SET_DISTINCT_VALUES, schema, tc.recs)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("components = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// TestMetaAggregator_SetOps_InterfaceLock confirms all six set
// aggregators implement MetaAggregator at runtime. The compile-time
// `var _ MetaAggregator = (*setUnionAggregator)(nil)` etc. in
// aggregator_set.go catches drift at build time; this test catches the
// inverse drift (an embedder casting an aggregator returned from the
// factory must succeed).
func TestMetaAggregator_SetOps_InterfaceLock(t *testing.T) {
	schema := makeSetTestSchema(t)
	for _, aggType := range []types.AggregationType{
		types.AGG_SET_UNION,
		types.AGG_SET_INTERSECTION,
		types.AGG_SET_FREQUENCY,
		types.AGG_SET_CARDINALITY_SUM,
		types.AGG_SET_CARDINALITY_AVG,
		types.AGG_SET_DISTINCT_VALUES,
	} {
		aggType := aggType
		t.Run(string(aggType), func(t *testing.T) {
			factory, ok := aggregatorRegistry[aggType]
			if !ok {
				t.Fatalf("no aggregator registered for %s", aggType)
			}
			agg, err := factory(&types.Aggregation{Type: aggType, Field: "tags"}, schema)
			if err != nil {
				t.Fatalf("factory %s: %v", aggType, err)
			}
			if _, ok := agg.(MetaAggregator); !ok {
				t.Fatalf("%s aggregator does not implement MetaAggregator", aggType)
			}
		})
	}
}

// TestMetaAggregator_SetOps_DictWidthOverflowGuard exercises the
// dictionary-width overflow guard contract: when the dictionary holds
// more entries than the mask type's bit width can encode, the runtime
// path errors via PULSE_SHARD_DICT_WIDTH_OVERFLOW at insert time. This
// test does NOT trigger that error path — it confirms that
// Components() reads post-finalize state safely even when the
// dictionary holds entries beyond the mask width (e.g. 9 dictionary
// entries on a set_u8 with the upper bits never observed). The
// resolveMaskLabels helper clamps to min(dictLen, 64) so emission is
// non-panicking regardless of dictionary shape; this is the runtime-
// safety contract the story names.
func TestMetaAggregator_SetOps_DictWidthOverflowGuard(t *testing.T) {
	// Build a schema where the dictionary holds 9 entries but the
	// declared field type is set_u8 (8-bit max). The aggregator should
	// still construct cleanly (per the existing setAggDict path); Rich()
	// and Components() must clamp safely without panic.
	dict := encoding.NewDictionary()
	for _, v := range []string{"A", "B", "C", "D", "E", "F", "G", "H", "I"} {
		if _, err := dict.Add(v); err != nil {
			t.Fatalf("dict.Add: %v", err)
		}
	}
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "tags", Type: encoding.FieldTypeSetU8, Dictionary: dict},
		},
	}
	// Only the lower 8 bits can be set under set_u8.
	rec := makeSetRecord(schema, 0xFF)
	for _, aggType := range []types.AggregationType{
		types.AGG_SET_UNION,
		types.AGG_SET_INTERSECTION,
		types.AGG_SET_FREQUENCY,
		types.AGG_SET_CARDINALITY_SUM,
		types.AGG_SET_CARDINALITY_AVG,
		types.AGG_SET_DISTINCT_VALUES,
	} {
		aggType := aggType
		t.Run(string(aggType), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%s Components() panicked under wide-dict guard: %v", aggType, r)
				}
			}()
			factory := aggregatorRegistry[aggType]
			agg, err := factory(&types.Aggregation{Type: aggType, Field: "tags"}, schema)
			if err != nil {
				t.Fatalf("factory %s: %v", aggType, err)
			}
			if _, err := agg.Aggregate([]*Record{rec}, "tags"); err != nil {
				t.Fatalf("%s.Aggregate: %v", aggType, err)
			}
			meta := agg.(MetaAggregator)
			got, err := meta.Components()
			if err != nil {
				t.Fatalf("%s.Components: %v", aggType, err)
			}
			if got == nil {
				t.Fatalf("%s.Components returned nil map under wide-dict guard", aggType)
			}
		})
	}
}
