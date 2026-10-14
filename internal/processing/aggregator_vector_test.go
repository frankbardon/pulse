package processing

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// vecAggSchema: three nullable f64 members and a weight column.
func vecAggSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "b", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "c", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "w", Type: encoding.FieldTypeF64, Nullable: true},
	}}
}

// vecRec builds one record; a NaN argument is a NULL (absent) column.
func vecRec(schema *encoding.Schema, a, b, c, w float64) *Record {
	vals := map[string]float64{}
	for name, v := range map[string]float64{"a": a, "b": b, "c": c, "w": w} {
		if !math.IsNaN(v) {
			vals[name] = v
		}
	}
	return NewRecord(schema, vals)
}

func newVecAggForTest(t *testing.T, typ types.AggregationType, weighted bool, params string) *vecAggregator {
	t.Helper()
	agg := &types.Aggregation{Type: typ, Fields: []string{"a", "b", "c"}}
	if params != "" {
		agg.Params = json.RawMessage(params)
	}
	if weighted {
		agg.Weight = types.SlotWeightField("w")
	}
	a, err := newVecAggregator(agg, vecAggSchema())
	if err != nil {
		t.Fatal(err)
	}
	return a.(*vecAggregator)
}

// TestVecAgg_PerElementSemantics: a null member skips only its element
// (element_n differs per member), an element no row answered is NaN in
// the mean and 0 in the sum, a weighted slot folds Σw·x / Σw over rows
// with a valid weight > 0 while element_n stays raw, and labels follow
// params.labels.
func TestVecAgg_PerElementSemantics(t *testing.T) {
	s := vecAggSchema()
	rows := []*Record{
		vecRec(s, 1, 10, nan, 1),
		vecRec(s, 3, nan, nan, 3),
		vecRec(s, nan, 20, nan, 0), // zero weight: counted raw, not folded when weighted
		vecRec(s, 5, 30, nan, nan), // null weight: likewise
	}
	cases := []struct {
		name     string
		typ      types.AggregationType
		weighted bool
		want     []float64
		elemN    []int
		sumW     []float64
	}{
		{"mean", types.AGG_VEC_MEAN, false, []float64{3, 20, nan}, []int{3, 3, 0}, nil},
		{"sum", types.AGG_VEC_SUM, false, []float64{9, 60, 0}, []int{3, 3, 0}, nil},
		{"weighted mean", types.AGG_VEC_MEAN, true, []float64{(1 + 9) / 4.0, 10, nan}, []int{3, 3, 0}, []float64{4, 1, 0}},
		{"weighted sum", types.AGG_VEC_SUM, true, []float64{10, 10, 0}, []int{3, 3, 0}, []float64{4, 1, 0}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, arm := range []string{"buffered", "streaming"} {
				a := newVecAggForTest(t, c.typ, c.weighted, `{"labels":["x","y","z"]}`)
				var v float64
				var err error
				if arm == "buffered" {
					v, err = a.Aggregate(rows, "")
				} else {
					for _, r := range rows {
						if err = a.UpdateRow(r, ""); err != nil {
							t.Fatal(err)
						}
					}
					v, err = a.Finalize()
				}
				if err != nil || !math.IsNaN(v) {
					t.Fatalf("%s: scalar = %v, %v; want NaN fallback", arm, v, err)
				}
				rich, _ := a.Rich()
				got := rich.([]float64)
				if len(got) != len(c.want) {
					t.Fatalf("%s: value = %v", arm, got)
				}
				for i := range got {
					if got[i] != c.want[i] && !(math.IsNaN(got[i]) && math.IsNaN(c.want[i])) {
						t.Errorf("%s: element %d = %v, want %v", arm, i, got[i], c.want[i])
					}
				}
				comp, _ := a.Components()
				if !reflect.DeepEqual(comp["element_n"], c.elemN) {
					t.Errorf("%s: element_n = %v, want %v", arm, comp["element_n"], c.elemN)
				}
				if !reflect.DeepEqual(comp["labels"], []string{"x", "y", "z"}) || !reflect.DeepEqual(a.ElementLabels(), []string{"x", "y", "z"}) {
					t.Errorf("%s: labels = %v / %v", arm, comp["labels"], a.ElementLabels())
				}
				sw, has := comp["element_sum_weights"]
				if c.sumW == nil {
					if has {
						t.Errorf("%s: unweighted slot carries element_sum_weights", arm)
					}
				} else if !reflect.DeepEqual(sw, c.sumW) {
					t.Errorf("%s: element_sum_weights = %v, want %v", arm, sw, c.sumW)
				}
			}
		})
	}
}

// TestVecAgg_NaNElementMarshalsNull: a non-finite element is null on
// the wire, the array kept.
func TestVecAgg_NaNElementMarshalsNull(t *testing.T) {
	b, err := types.MarshalFinite([]float64{1.5, math.NaN(), math.Inf(1)})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `[1.5,null,null]` {
		t.Errorf("marshal = %s", b)
	}
}

// TestVecAgg_FactoryRefusals: an unresolved vector is an engine fault,
// a field slot a config error, bad params refused.
func TestVecAgg_FactoryRefusals(t *testing.T) {
	for _, c := range []struct {
		name string
		agg  *types.Aggregation
		code errors.Code
	}{
		{"unresolved vector", &types.Aggregation{Type: types.AGG_VEC_MEAN, Vector: "v"}, errors.PROCESSING_INTERNAL},
		{"field", &types.Aggregation{Type: types.AGG_VEC_MEAN, Field: "a"}, errors.PROCESSING_CONFIG},
		{"no fields", &types.Aggregation{Type: types.AGG_VEC_SUM}, errors.PROCESSING_CONFIG},
		{"bad params", &types.Aggregation{Type: types.AGG_VEC_SUM, Fields: []string{"a"}, Params: json.RawMessage(`{"nope":1}`)}, errors.SERVICE_VALIDATION},
		{"unnarrowed element", &types.Aggregation{Type: types.AGG_VEC_SUM, Fields: []string{"a", "b"}, Params: json.RawMessage(`{"element":"b"}`)}, errors.PROCESSING_INTERNAL},
		{"element not its member", &types.Aggregation{Type: types.AGG_VEC_SUM, Fields: []string{"a"}, Params: json.RawMessage(`{"element":"b"}`)}, errors.PROCESSING_INTERNAL},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := newVecAggregator(c.agg, vecAggSchema())
			var ce *errors.CodedError
			if !asCoded(err, &ce) || ce.Code != c.code {
				t.Fatalf("err = %v, want %s", err, c.code)
			}
		})
	}
}

// vecRandomRows: n rows of non-dyadic floats (so summation order
// reaches the bits), ~10% nulls per member, stamped on shard (whose
// first global ordinal is start).
func vecRandomRows(s *encoding.Schema, n, shard, start int, seed int64) []*Record {
	rng := rand.New(rand.NewSource(seed))
	out := make([]*Record, n)
	val := func() float64 {
		if rng.Intn(10) == 0 {
			return nan
		}
		return rng.NormFloat64()*1e3 + 1/3.0
	}
	for i := range out {
		out[i] = vecRec(s, val(), val(), val(), 0.1+rng.Float64())
		out[i].SetMergePosition(shard, start, i)
	}
	return out
}

func vecBits(t *testing.T, a *vecAggregator) []uint64 {
	t.Helper()
	if _, err := a.Finalize(); err != nil {
		t.Fatal(err)
	}
	rich, _ := a.Rich()
	v := rich.([]float64)
	out := make([]uint64, len(v))
	for i, x := range v {
		out[i] = math.Float64bits(x)
	}
	return out
}

// TestVecAgg_MergeBitExact: any partition of the rows at block
// boundaries, merged in any order, gives the serial fold's exact bits
// — one shard or several.
func TestVecAgg_MergeBitExact(t *testing.T) {
	s := vecAggSchema()
	n := 5*linalg.MergeBlockSize + 77
	var rows []*Record
	rows = append(rows, vecRandomRows(s, n, 0, 0, 1)...)
	rows = append(rows, vecRandomRows(s, n/2, 1, n, 2)...)
	for _, typ := range []types.AggregationType{types.AGG_VEC_MEAN, types.AGG_VEC_SUM} {
		for _, weighted := range []bool{false, true} {
			serial := newVecAggForTest(t, typ, weighted, "")
			for _, r := range rows {
				_ = serial.UpdateRow(r, "")
			}
			want := vecBits(t, serial)
			for trial := 0; trial < 6; trial++ {
				rng := rand.New(rand.NewSource(int64(trial)))
				// Cut at block boundaries into k partitions of
				// contiguous rows, then merge in a shuffled order.
				k := 2 + trial
				parts := make([]*vecAggregator, k)
				for i := range parts {
					parts[i] = newVecAggForTest(t, typ, weighted, "")
				}
				for _, r := range rows {
					key, _ := r.MergeBlock()
					_ = parts[(key.Shard*97+key.Block)%k].UpdateRow(r, "")
				}
				order := rng.Perm(k)
				root := parts[order[0]]
				for _, i := range order[1:] {
					if err := root.MergeOnline(parts[i]); err != nil {
						t.Fatal(err)
					}
				}
				if got := vecBits(t, root); !reflect.DeepEqual(got, want) {
					t.Errorf("%s weighted=%v trial %d: merged bits %x, serial %x", typ, weighted, trial, got, want)
				}
			}
		}
	}
}

// TestVecAgg_ShardSplitBitExact: the same rows stamped as one file and
// as a shard archive split unevenly — shard edges inside blocks, 1-row
// shards, an empty shard — give the single file's exact bits, each
// shard folded on its own partition and the partitions merged in any
// order (the per-shard reducer), or every row on one (the serial shard
// iterator). Falsified by keying blocks per shard (start 0).
func TestVecAgg_ShardSplitBitExact(t *testing.T) {
	s := vecAggSchema()
	B := linalg.MergeBlockSize
	n := 3*B + 500
	single := vecRandomRows(s, n, 0, 0, 7)
	splits := map[string][]int{
		"mid-block edges":     {B + 13, 1, 0, 2*B - 400, n - (3*B - 386)},
		"one-row shards":      {1, 1, 1, B - 3, 1, 0, n - B - 1},
		"aligned plus tail":   {B, B, B, 500},
		"one shard":           {n},
		"leading empty shard": {0, 2, n - 2},
	}
	for _, typ := range []types.AggregationType{types.AGG_VEC_MEAN, types.AGG_VEC_SUM} {
		for _, weighted := range []bool{false, true} {
			ref := newVecAggForTest(t, typ, weighted, "")
			for _, r := range single {
				_ = ref.UpdateRow(r, "")
			}
			want := vecBits(t, ref)
			for name, sizes := range splits {
				total := 0
				for _, z := range sizes {
					total += z
				}
				if total != n {
					t.Fatalf("%s: split sums to %d, want %d", name, total, n)
				}
				restamp := func() [][]*Record {
					var shards [][]*Record
					start := 0
					for sh, z := range sizes {
						var part []*Record
						for i := 0; i < z; i++ {
							r := single[start+i]
							r.SetMergePosition(sh, start, i)
							part = append(part, r)
						}
						shards = append(shards, part)
						start += z
					}
					return shards
				}
				shards := restamp()
				serial := newVecAggForTest(t, typ, weighted, "")
				for _, part := range shards {
					for _, r := range part {
						_ = serial.UpdateRow(r, "")
					}
				}
				if got := vecBits(t, serial); !reflect.DeepEqual(got, want) {
					t.Errorf("%s weighted=%v %s serial: bits %x, single file %x", typ, weighted, name, got, want)
				}
				for trial := 0; trial < 3; trial++ {
					parts := make([]*vecAggregator, len(shards))
					for i, part := range shards {
						parts[i] = newVecAggForTest(t, typ, weighted, "")
						for _, r := range part {
							_ = parts[i].UpdateRow(r, "")
						}
					}
					order := rand.New(rand.NewSource(int64(trial))).Perm(len(parts))
					root := parts[order[0]]
					for _, i := range order[1:] {
						if err := root.MergeOnline(parts[i]); err != nil {
							t.Fatal(err)
						}
					}
					if got := vecBits(t, root); !reflect.DeepEqual(got, want) {
						t.Errorf("%s weighted=%v %s merge order %v: bits %x, single file %x", typ, weighted, name, order, got, want)
					}
				}
				for i, r := range single { // back to the single-file stamp
					r.SetMergePosition(0, 0, i)
				}
			}
		}
	}
}

// TestVecAgg_MergeRefusals: a block on both sides, or unpositioned
// rows on both sides, refuses PROCESSING_INTERNAL; a type mismatch
// refuses too.
func TestVecAgg_MergeRefusals(t *testing.T) {
	s := vecAggSchema()
	mk := func(pos bool) *vecAggregator {
		a := newVecAggForTest(t, types.AGG_VEC_SUM, false, "")
		r := vecRec(s, 1, 2, 3, 1)
		if pos {
			r.SetMergePosition(0, 0, 0)
		}
		_ = a.UpdateRow(r, "")
		return a
	}
	for _, c := range []struct {
		name string
		a, b *vecAggregator
	}{
		{"duplicate block", mk(true), mk(true)},
		{"loose on both", mk(false), mk(false)},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := c.a.MergeOnline(c.b)
			var ce *errors.CodedError
			if !asCoded(err, &ce) || ce.Code != errors.PROCESSING_INTERNAL {
				t.Fatalf("err = %v, want PROCESSING_INTERNAL", err)
			}
		})
	}
	// One loose side merges (the joined / chain-stage arm).
	if err := mk(false).MergeOnline(mk(true)); err != nil {
		t.Errorf("one loose side refused: %v", err)
	}
	if err := mk(true).MergeOnline(newVecAggForTest(t, types.AGG_VEC_MEAN, false, "")); err == nil {
		t.Error("mean merged into sum")
	}
}

// TestVecAgg_WriteAggregationResult: the row writer puts an array slot's
// value under its label and an expanding slot's figures under one
// "<label>[<element label>]" column each (no label key); a scalar
// aggregator keeps the plain dispatch.
func TestVecAgg_WriteAggregationResult(t *testing.T) {
	s := vecAggSchema()
	rows := []*Record{vecRec(s, 1, 2, math.NaN(), 1), vecRec(s, 3, math.NaN(), math.NaN(), 1)}
	cases := []struct {
		name   string
		params string
		want   map[string]any
	}{
		{"array", "", map[string]any{"m": []float64{2, 2, math.NaN()}}},
		{"expand", `{"expand":true}`, map[string]any{"m[a]": 2.0, "m[b]": 2.0, "m[c]": math.NaN()}},
		{"expand labels", `{"expand":true,"labels":["x","y","z"]}`, map[string]any{"m[x]": 2.0, "m[y]": 2.0, "m[z]": math.NaN()}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newVecAggForTest(t, types.AGG_VEC_MEAN, false, c.params)
			for _, r := range rows {
				if err := a.UpdateRow(r, ""); err != nil {
					t.Fatal(err)
				}
			}
			val, err := a.Finalize()
			if err != nil {
				t.Fatal(err)
			}
			row := map[string]any{}
			if err := WriteAggregationResult(row, "m", a, val); err != nil {
				t.Fatal(err)
			}
			if len(row) != len(c.want) {
				t.Fatalf("row = %v, want %v", row, c.want)
			}
			for k, w := range c.want {
				g, ok := row[k]
				if !ok {
					t.Fatalf("row lacks %s: %v", k, row)
				}
				gs, ws := fmt.Sprint(g), fmt.Sprint(w)
				if gs != ws {
					t.Errorf("%s = %s, want %s", k, gs, ws)
				}
			}
		})
	}
	row := map[string]any{}
	if err := WriteAggregationResult(row, "n", &countAggregator{}, 7); err != nil || row["n"] != 7.0 {
		t.Errorf("scalar write = %v, %v", row, err)
	}
}

// TestVecAgg_ElementScalar: an element slot (the prepared one-member
// form) returns its element's figure from Aggregate and Finalize, no
// Rich payload (the crosstab cell takes the scalar), and one-entry
// components — equal to the same element of the full array slot.
func TestVecAgg_ElementScalar(t *testing.T) {
	s := vecAggSchema()
	nan := math.NaN()
	rows := []*Record{vecRec(s, 1, 2, nan, 1), vecRec(s, 3, nan, nan, 2), vecRec(s, 5, 7, nan, 1)}
	for _, typ := range []types.AggregationType{types.AGG_VEC_MEAN, types.AGG_VEC_SUM} {
		full := newVecAggForTest(t, typ, true, "")
		if _, err := full.Aggregate(rows, ""); err != nil {
			t.Fatal(err)
		}
		arr, _ := full.Rich()
		comp, _ := full.Components()
		for k, m := range []string{"a", "b", "c"} {
			agg := &types.Aggregation{Type: typ, Fields: []string{m}, Weight: types.SlotWeightField("w"),
				Params: json.RawMessage(`{"labels":["` + m + `"],"element":"` + m + `"}`)}
			inst, err := newVecAggregator(agg, s)
			if err != nil {
				t.Fatal(err)
			}
			a := inst.(*vecAggregator)
			want := arr.([]float64)[k]
			got, err := a.Aggregate(rows, "")
			if err != nil || !sameFloatBits(got, want) {
				t.Errorf("%s element %s: Aggregate = %v (%v), want %v", typ, m, got, err, want)
			}
			for _, r := range rows {
				_ = a.UpdateRow(r, "")
			}
			if f, _ := a.Finalize(); !sameFloatBits(f, want) {
				t.Errorf("%s element %s: Finalize = %v, want %v", typ, m, f, want)
			}
			if v, err := a.Rich(); v != nil || err != nil {
				t.Errorf("%s element %s: Rich = %v %v, want none", typ, m, v, err)
			}
			c, _ := a.Components()
			if !reflect.DeepEqual(c["element_n"], []int{comp["element_n"].([]int)[k]}) ||
				!reflect.DeepEqual(c["labels"], []string{m}) {
				t.Errorf("%s element %s: components %v", typ, m, c)
			}
		}
	}
}

func sameFloatBits(a, b float64) bool {
	return math.Float64bits(a) == math.Float64bits(b) || (math.IsNaN(a) && math.IsNaN(b))
}
