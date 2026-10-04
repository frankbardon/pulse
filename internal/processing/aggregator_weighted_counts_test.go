package processing

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// Weighted counting aggregators (weighting-descriptive E2-S2):
// AGG_FREQUENCY (Σw of matches; share Σw_match/Σw), AGG_RATIO
// (Σw·num / Σw·den), AGG_SET_FREQUENCY (Σw per member),
// AGG_SET_CARDINALITY_SUM (Σw·card) and AGG_SET_CARDINALITY_AVG
// (Σw·card / Σw).

func wcountSchema() *encoding.Schema {
	dict := encoding.NewDictionary()
	for _, v := range []string{"VISA", "MC", "AMEX", "DISC", "JCB"} {
		if _, err := dict.Add(v); err != nil {
			panic(err)
		}
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "value", Type: encoding.FieldTypeF64},
		{Name: "weight", Type: encoding.FieldTypeF64},
		{Name: "num", Type: encoding.FieldTypeF64},
		{Name: "den", Type: encoding.FieldTypeF64},
		{Name: "tags", Type: encoding.FieldTypeSetU8, Dictionary: dict},
	}}
}

// wcountRow is one fixture row; a NaN value / num / den is a null.
type wcountRow struct {
	value, w, num, den float64
	mask               uint64
	wNull, maskNull    bool
}

func wcountRecords(rows []wcountRow) []*Record {
	s := wcountSchema()
	out := make([]*Record, len(rows))
	for i, r := range rows {
		vals := map[string]float64{"value": r.value, "weight": r.w, "num": r.num, "den": r.den}
		nulls := map[string]bool{"weight": r.wNull, "tags": r.maskNull}
		for k, v := range vals {
			if math.IsNaN(v) && k != "weight" {
				nulls[k] = true
			}
		}
		wide := map[string]any{}
		if !r.maskNull {
			wide["tags"] = r.mask
		}
		out[i] = NewRecordWithWide(s, vals, nulls, wide)
	}
	return out
}

// wcountBase: 400 deterministic rows — coarse values (so params.value
// matches often), integer num / den (exact sums), masks over five
// members, with nulls sprinkled through every column.
func wcountBase(weight func(i int) float64) []wcountRow {
	rows := make([]wcountRow, 400)
	seed := uint64(0x9e3779b97f4a7c15)
	for i := range rows {
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		r := wcountRow{
			value: float64(seed % 7),
			num:   float64(seed%97) - 13,
			den:   float64(seed%53) + 1,
			mask:  (seed >> 11) % 32,
			w:     weight(i),
		}
		if i%11 == 5 {
			r.value = math.NaN()
		}
		if i%13 == 7 {
			r.den = math.NaN()
		}
		if i%17 == 3 {
			r.num = math.NaN()
		}
		r.maskNull = i%19 == 9
		rows[i] = r
	}
	return rows
}

type wcountSpec struct {
	name   string
	op     types.AggregationType
	field  string
	params json.RawMessage
}

var wcountSpecs = []wcountSpec{
	{name: "frequency", op: types.AGG_FREQUENCY, field: "value", params: json.RawMessage(`{"value":"3"}`)},
	{name: "frequency_unmatched", op: types.AGG_FREQUENCY, field: "value", params: json.RawMessage(`{"value":"99"}`)},
	{name: "ratio", op: types.AGG_RATIO, field: "value", params: json.RawMessage(`{"numerator_field":"num","denominator_field":"den"}`)},
	{name: "set_frequency", op: types.AGG_SET_FREQUENCY, field: "tags"},
	{name: "set_cardinality_sum", op: types.AGG_SET_CARDINALITY_SUM, field: "tags"},
	{name: "set_cardinality_avg", op: types.AGG_SET_CARDINALITY_AVG, field: "tags"},
}

func (s wcountSpec) agg(w types.SlotWeight) *types.Aggregation {
	return &types.Aggregation{Type: s.op, Field: s.field, Label: "x", Params: s.params, Weight: w}
}

func (s wcountSpec) weighted(kind types.WeightKind) *types.Aggregation {
	return s.agg(types.SlotWeightOf(types.WeightSpec{Field: "weight", Kind: kind}))
}

func wcountNew(t *testing.T, a *types.Aggregation) Aggregator {
	t.Helper()
	agg, err := aggregatorRegistry[a.Type](a, wcountSchema())
	if err != nil {
		t.Fatalf("%s: %v", a.Type, err)
	}
	return agg
}

// wcountRun runs a on the buffered, streamed and merged paths; per path
// the scalar's bits, the components and the routed (Rich-or-scalar)
// figure, each as JSON.
func wcountRun(t *testing.T, a *types.Aggregation, records []*Record, field string) map[string][3]string {
	t.Helper()
	pack := func(v float64, agg Aggregator) [3]string {
		c, err := agg.(MetaAggregator).Components()
		if err != nil {
			t.Fatal(err)
		}
		routed, err := dispatchAggregatorResult(agg, v)
		if err != nil {
			t.Fatal(err)
		}
		return [3]string{jsonFloatBits(v), string(mustJSON(t, c)), string(mustJSON(t, routed))}
	}
	out := map[string][3]string{}
	buf := wcountNew(t, a)
	v, err := buf.Aggregate(records, field)
	if err != nil {
		t.Fatal(err)
	}
	out["buffered"] = pack(v, buf)

	s := wcountNew(t, a).(OnlineAggregator)
	for _, r := range records {
		if err := s.UpdateRow(r, field); err != nil {
			t.Fatal(err)
		}
	}
	v, _ = s.Finalize()
	out["streamed"] = pack(v, s.(Aggregator))

	l := wcountNew(t, a).(MergeableAggregator)
	var cuts []int
	for _, c := range []int{0, 3, 41, 42, 157, 230, 311, 377} {
		if c < len(records) {
			cuts = append(cuts, c)
		}
	}
	cuts = append(cuts, len(records))
	for i := 0; i+1 < len(cuts); i++ {
		part := wcountNew(t, a).(OnlineAggregator)
		for _, r := range records[cuts[i]:cuts[i+1]] {
			_ = part.UpdateRow(r, field)
		}
		if err := l.MergeOnline(part); err != nil {
			t.Fatal(err)
		}
	}
	v, _ = l.(OnlineAggregator).Finalize()
	out["merged"] = pack(v, l.(Aggregator))
	return out
}

// TestWeightedCounts_UnityParity: an all-ones weight reproduces the
// unweighted scalar bit for bit, and the components and the routed
// figure (the Rich map for AGG_SET_FREQUENCY) byte for byte, on the
// buffered, streamed and merged paths, under both kinds.
func TestWeightedCounts_UnityParity(t *testing.T) {
	records := wcountRecords(wcountBase(func(int) float64 { return 1 }))
	for _, s := range wcountSpecs {
		for _, kind := range []types.WeightKind{types.WeightKindProbability, types.WeightKindFrequency} {
			t.Run(s.name+"/"+string(kind), func(t *testing.T) {
				if reflect.TypeOf(wcountNew(t, s.weighted(kind))) == reflect.TypeOf(wcountNew(t, s.agg(types.SlotWeight{}))) &&
					s.op != types.AGG_RATIO {
					t.Fatal("the weighted factory returned the unweighted type: the parity would be vacuous")
				}
				plain := wcountRun(t, s.agg(types.SlotWeight{}), records, s.field)
				got := wcountRun(t, s.weighted(kind), records, s.field)
				for path, want := range plain {
					if got[path] != want {
						t.Errorf("%s: unweighted %v, unit-weighted %v", path, want, got[path])
					}
				}
			})
		}
	}
}

// TestWeightedCounts_FrequencyExpansion: integer weights (0..3) equal
// the physically duplicated rows run unweighted — exactly, on every
// path: every figure here is a sum of integers or one exact division.
func TestWeightedCounts_FrequencyExpansion(t *testing.T) {
	base := wcountBase(func(i int) float64 { return float64((i * 5) % 4) })
	var ex []wcountRow
	for _, r := range base {
		for k := 0; k < int(r.w); k++ {
			c := r
			c.w = 1
			ex = append(ex, c)
		}
	}
	weighted, expanded := wcountRecords(base), wcountRecords(ex)
	for _, s := range wcountSpecs {
		t.Run(s.name, func(t *testing.T) {
			want := wcountRun(t, s.agg(types.SlotWeight{}), expanded, s.field)
			got := wcountRun(t, s.weighted(types.WeightKindFrequency), weighted, s.field)
			for path, w := range want {
				if got[path] != w {
					t.Errorf("%s: expanded %v, weighted %v", path, w, got[path])
				}
			}
		})
	}
}

// TestWeightedCounts_Oracle: hand-computed fractional-weight figures.
func TestWeightedCounts_Oracle(t *testing.T) {
	nan := math.NaN()
	records := wcountRecords([]wcountRow{
		{value: 1, w: 0.5, num: 1, den: 2, mask: 0b01},
		{value: 2, w: 1, num: 2, den: 2, mask: 0b11},
		{value: 1, w: 2, num: 3, den: 2, mask: 0b10},
		{value: 3, w: 0.25, num: 4, den: nan, mask: 0b100, maskNull: true},
	})
	cases := []struct {
		spec   wcountSpec
		scalar float64
		comps  string
		routed string
	}{
		// Σw over present values 3.75; matches (value 1) 2.5.
		{wcountSpec{name: "frequency", op: types.AGG_FREQUENCY, field: "value", params: json.RawMessage(`{"value":1}`)}, 2.5, `{"match_count":2.5,"share":0.6666666666666666}`, `2.5`},
		// The null-den row is skipped: Σw·num = 0.5+2+6, Σw·den = 1+2+4.
		{wcountSpecs[2], 8.5 / 7, `{"denominator":7,"numerator":8.5,"ratio":1.2142857142857142}`, `1.2142857142857142`},
		// VISA: 0.5 + 1; MC: 1 + 2. The null-mask row contributes nothing.
		{wcountSpecs[3], 3, `{"distinct_labels":2,"per_label_count":{"MC":3,"VISA":1.5},"total_label_observations":4.5}`, `{"MC":3,"VISA":1.5}`},
		// Σw·card = 0.5·1 + 1·2 + 2·1 = 4.5; Σw = 3.5.
		{wcountSpecs[4], 4.5, `{"sum_cardinality":4.5}`, `4.5`},
		{wcountSpecs[5], 4.5 / 3.5, `{"avg_cardinality":1.2857142857142858,"sum_cardinality":4.5}`, `1.2857142857142858`},
	}
	for _, tc := range cases {
		t.Run(tc.spec.name, func(t *testing.T) {
			for path, got := range wcountRun(t, tc.spec.weighted(types.WeightKindProbability), records, tc.spec.field) {
				if got[0] != jsonFloatBits(tc.scalar) || got[1] != tc.comps || got[2] != tc.routed {
					t.Errorf("%s: scalar %s comps %s routed %s; want %v %s %s", path, got[0], got[1], got[2], tc.scalar, tc.comps, tc.routed)
				}
			}
		})
	}
}

// TestWeightedCounts_InvalidWeightsExcluded: null, negative, NaN/Inf
// and zero weights drop the row — never coerced — on every operator.
func TestWeightedCounts_InvalidWeightsExcluded(t *testing.T) {
	cleanRows := wcountBase(func(i int) float64 { return 0.5 + float64(i%3) })[:40]
	dirtyRows := append([]wcountRow{}, cleanRows...)
	for _, w := range []float64{-3, math.NaN(), math.Inf(1), 0} {
		dirtyRows = append(dirtyRows, wcountRow{value: 3, w: w, num: 50, den: 1, mask: 0b11111})
	}
	dirtyRows = append(dirtyRows, wcountRow{value: 3, wNull: true, num: 50, den: 1, mask: 0b11111})
	clean, dirty := wcountRecords(cleanRows), wcountRecords(dirtyRows)
	for _, s := range wcountSpecs {
		t.Run(s.name, func(t *testing.T) {
			want := wcountRun(t, s.weighted(types.WeightKindProbability), clean, s.field)
			got := wcountRun(t, s.weighted(types.WeightKindProbability), dirty, s.field)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("invalid weights changed the figure:\n got  %v\n want %v", got, want)
			}
		})
	}
}

// TestWeightedCounts_ExecutionClassUnchanged: a weight keeps every
// counting operator streamable and mergeable, exactly as unweighted.
func TestWeightedCounts_ExecutionClassUnchanged(t *testing.T) {
	schema := wcountSchema()
	for _, s := range wcountSpecs {
		plain := &types.Request{Aggregations: []*types.Aggregation{s.agg(types.SlotWeight{})}}
		weighted := StampWeights(&types.Request{Weight: &types.WeightSpec{Field: "weight"},
			Aggregations: []*types.Aggregation{s.agg(types.SlotWeight{})}}, nil)
		if slotWeight(weighted.Aggregations[0]) == nil {
			t.Fatalf("%s: the weight was not stamped", s.name)
		}
		if CanStreamRequest(weighted, schema) != CanStreamRequest(plain, schema) ||
			CanMergeRequest(weighted, schema) != CanMergeRequest(plain, schema) ||
			!CanMergeRequest(weighted, schema) {
			t.Errorf("%s: weighted streamable=%v mergeable=%v, unweighted %v / %v", s.name,
				CanStreamRequest(weighted, schema), CanMergeRequest(weighted, schema),
				CanStreamRequest(plain, schema), CanMergeRequest(plain, schema))
		}
		if _, ok := wcountNew(t, s.weighted("")).(valueAggregator); ok {
			t.Errorf("%s: the weighted form must not take a pre-collected value slice", s.name)
		}
	}
}
