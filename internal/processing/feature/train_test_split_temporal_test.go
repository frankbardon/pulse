package feature

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// splitDigest is the SHA-256 of a split column's values (big-endian
// float64 bits, in record order).
func splitDigest(values []float64) string {
	h := sha256.New()
	var b [8]byte
	for _, v := range values {
		binary.BigEndian.PutUint64(b[:], math.Float64bits(v))
		h.Write(b[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// stratifiedSplitRecords is n records whose "g" level cycles a / b / c.
func stratifiedSplitRecords(n int) []Record {
	out := make([]Record, n)
	for i := range out {
		r := newFakeRecord()
		r.str["g"] = []string{"a", "b", "c"}[i%3]
		out[i] = r
	}
	return out
}

// TestTrainTestSplit_RandomModeByteIdentical pins the random split's
// assignments to the digests the operator produced before temporal
// mode existed: an absent mode and an explicit "random" are the same
// split, bit for bit, on the buffered and the streaming arm.
func TestTrainTestSplit_RandomModeByteIdentical(t *testing.T) {
	t.Parallel()

	catSchema := &encoding.Schema{Fields: []encoding.Field{{Name: "g", Type: encoding.FieldTypeCategoricalU8}}}
	cases := []struct {
		name, params string
		stratified   bool
		want         string
	}{
		{"three ratios seeded", `{"ratios":[0.7,0.15,0.15],"seed":42}`, false, "027cda352c052b0d10db2185c9dd3f329fdd325232f9b815be85e2b871f60836"},
		{"two ratios, seed absent", `{"ratios":[0.8,0.2]}`, false, "11dbf724a00347a7e410ec3f6e71f999880bd36dd9a0ff53d5a31dfce1053227"},
		{"stratified", `{"ratios":[0.6,0.2,0.2],"seed":9,"stratify":"g"}`, true, "46b98568cdfba027289543fae3bcc1138d04983988622e162a4a0bc05a8847c9"},
	}
	for _, c := range cases {
		for _, params := range []string{c.params, c.params[:len(c.params)-1] + `,"mode":"random"}`} {
			records := makeRecordsForSplit(257)
			var schema *encoding.Schema
			if c.stratified {
				records, schema = stratifiedSplitRecords(257), catSchema
			}
			comp, err := newTrainTestSplit(&types.Feature{Params: json.RawMessage(params)}, schema)
			if err != nil {
				t.Fatalf("%s %s: %v", c.name, params, err)
			}
			out, err := comp.Compute(records, "")
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if got := splitDigest(out["split"].Values); got != c.want {
				t.Errorf("%s %s buffered digest %s, want %s", c.name, params, got, c.want)
			}
			sc, err := newTrainTestSplit(&types.Feature{Params: json.RawMessage(params)}, schema)
			if err != nil {
				t.Fatal(err)
			}
			s := sc.(StreamingComputer)
			for _, r := range records {
				if err := s.PrePass(r, ""); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Finalize(); err != nil {
				t.Fatal(err)
			}
			streamed := make([]float64, 0, len(records))
			for _, r := range records {
				o, err := s.EmitRow(r, "")
				if err != nil {
					t.Fatal(err)
				}
				streamed = append(streamed, o["split"].Values[0])
			}
			if got := splitDigest(streamed); got != c.want {
				t.Errorf("%s %s streamed digest %s, want %s", c.name, params, got, c.want)
			}
		}
	}
}

// orderedSplitRecords builds one record per entry of order, whose "t"
// is that value; a NaN entry leaves "t" null.
func orderedSplitRecords(order []float64) []Record {
	out := make([]Record, len(order))
	for i, v := range order {
		r := newFakeRecord()
		if math.IsNaN(v) {
			r.nulls["t"] = true
		} else {
			r.num["t"] = v
		}
		out[i] = r
	}
	return out
}

func temporalSplit(t *testing.T, params string, order []float64) Output {
	t.Helper()
	c, err := newTrainTestSplit(&types.Feature{Params: json.RawMessage(params)}, nil)
	if err != nil {
		t.Fatalf("newTrainTestSplit(%s): %v", params, err)
	}
	out, err := c.Compute(orderedSplitRecords(order), "")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	return out["split"]
}

// TestTrainTestSplit_TemporalBoundaries: the earliest rows by order_by
// are train, the latest test, val between — whatever the record order —
// and the cut sizes are the random split's round(n·r).
func TestTrainTestSplit_TemporalBoundaries(t *testing.T) {
	t.Parallel()

	// Ten distinct instants, shuffled in record order.
	order := []float64{7, 2, 9, 0, 5, 1, 8, 3, 6, 4}
	got := temporalSplit(t, `{"ratios":[0.6,0.2,0.2],"mode":"temporal","order_by":"t"}`, order)
	if got.Nulls != nil {
		t.Fatalf("no null order value, yet Nulls = %v", got.Nulls)
	}
	for i, v := range order {
		want := SplitTrain
		switch {
		case v >= 8:
			want = SplitTest
		case v >= 6:
			want = SplitVal
		}
		if got.Values[i] != want {
			t.Errorf("row %d (t=%v): split %v, want %v", i, v, got.Values[i], want)
		}
	}

	// Two ratios: train then val, no test.
	got = temporalSplit(t, `{"ratios":[0.7,0.3],"mode":"temporal","order_by":"t"}`, order)
	for i, v := range order {
		want := SplitTrain
		if v >= 7 {
			want = SplitVal
		}
		if got.Values[i] != want {
			t.Errorf("two ratios row %d (t=%v): split %v, want %v", i, v, got.Values[i], want)
		}
	}
}

// TestTrainTestSplit_TemporalTiesNeverStraddle: rows sharing an
// order_by value land in one partition; the boundary snaps to the
// nearest edge between distinct values (the earlier one on a tie).
func TestTrainTestSplit_TemporalTiesNeverStraddle(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		ratios string
		order  []float64
		want   []float64
	}{
		// round(4·0.5) = 2 falls inside the run of 2s; edges 0, 1, 4:
		// 1 is nearer, so the whole run is val.
		{"run after the cut", `[0.5,0.5]`, []float64{1, 2, 2, 2}, []float64{0, 1, 1, 1}},
		// Edges 0, 3, 4: 3 is nearer than 0, so the run of 1s is train.
		{"run before the cut", `[0.5,0.5]`, []float64{1, 1, 1, 2}, []float64{0, 0, 0, 1}},
		// Edges 0, 2, 4 around a cut of 1 (round(4·0.25)): equidistant,
		// the earlier edge wins — the run of 1s is val.
		{"equidistant", `[0.25,0.75]`, []float64{1, 1, 2, 2}, []float64{1, 1, 1, 1}},
		// Ten rows, cuts at 6 and 8 — the run of 5s (positions 5..8)
		// spans both: edges 0..5, 9, 10 snap 6 → 5 and 8 → 9, so the
		// run is val in one piece and test is the last row.
		{"run spans both cuts", `[0.6,0.2,0.2]`, []float64{0, 1, 2, 3, 4, 5, 5, 5, 5, 6}, []float64{0, 0, 0, 0, 0, 1, 1, 1, 1, 2}},
		// One instant: every row in one partition.
		{"one instant", `[0.7,0.3]`, []float64{3, 3, 3, 3, 3}, []float64{0, 0, 0, 0, 0}},
	}
	for _, c := range cases {
		got := temporalSplit(t, `{"ratios":`+c.ratios+`,"mode":"temporal","order_by":"t"}`, c.order)
		for i := range c.order {
			if got.Values[i] != c.want[i] {
				t.Errorf("%s: splits %v, want %v", c.name, got.Values, c.want)
				break
			}
		}
		// Whatever the cut, no value is in two partitions.
		seen := map[float64]float64{}
		for i, v := range c.order {
			if s, ok := seen[v]; ok && s != got.Values[i] {
				t.Errorf("%s: t=%v straddles partitions %v and %v", c.name, v, s, got.Values[i])
			}
			seen[v] = got.Values[i]
		}
	}
}

// TestTrainTestSplit_TemporalNullOrderJoinsNoPartition: a row whose
// order_by is null has no place in time — its split value is null (in no
// partition) and the ratios cut the placeable rows only.
func TestTrainTestSplit_TemporalNullOrderJoinsNoPartition(t *testing.T) {
	t.Parallel()

	nan := math.NaN()
	order := []float64{4, nan, 1, 3, nan, 2}
	got := temporalSplit(t, `{"ratios":[0.5,0.5],"mode":"temporal","order_by":"t"}`, order)
	wantNull := []bool{false, true, false, false, true, false}
	if len(got.Nulls) != len(order) {
		t.Fatalf("Nulls = %v, want one per row", got.Nulls)
	}
	for i := range order {
		if got.Nulls[i] != wantNull[i] {
			t.Errorf("row %d null = %v, want %v", i, got.Nulls[i], wantNull[i])
		}
	}
	// Four placeable rows: t 1, 2 train; 3, 4 val.
	for i, want := range map[int]float64{0: SplitVal, 2: SplitTrain, 3: SplitVal, 5: SplitTrain} {
		if got.Values[i] != want {
			t.Errorf("row %d (t=%v): split %v, want %v", i, order[i], got.Values[i], want)
		}
	}

	// Every row null: an all-null column, no panic.
	got = temporalSplit(t, `{"ratios":[0.5,0.5],"mode":"temporal","order_by":"t"}`, []float64{nan, nan})
	if len(got.Nulls) != 2 || !got.Nulls[0] || !got.Nulls[1] {
		t.Errorf("all-null order: Nulls = %v", got.Nulls)
	}
}

// TestTrainTestSplit_TemporalIndependentOfRecordOrder: the assignment
// is a function of the order values alone.
func TestTrainTestSplit_TemporalIndependentOfRecordOrder(t *testing.T) {
	t.Parallel()

	const params = `{"ratios":[0.7,0.15,0.15],"mode":"temporal","order_by":"t"}`
	order := make([]float64, 101)
	for i := range order {
		order[i] = float64((i * 37) % 23) // repeats: ties throughout
	}
	byValue := map[float64]float64{}
	base := temporalSplit(t, params, order)
	for i, v := range order {
		byValue[v] = base.Values[i]
	}
	rev := make([]float64, len(order))
	for i := range order {
		rev[i] = order[len(order)-1-i]
	}
	got := temporalSplit(t, params, rev)
	for i, v := range rev {
		if got.Values[i] != byValue[v] {
			t.Fatalf("reversed records: t=%v split %v, forward %v", v, got.Values[i], byValue[v])
		}
	}
}

// TestTrainTestSplit_TemporalParamRefusals: the mode rule
// (internal/splitmode) and the order_by type rule (internal/fieldaccept)
// refuse at construction.
func TestTrainTestSplit_TemporalParamRefusals(t *testing.T) {
	t.Parallel()

	dict := encoding.NewDictionary()
	dict.Add("a")
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "week", Type: encoding.FieldTypeU16},
		{Name: "day", Type: encoding.FieldTypeDate},
		{Name: "at", Type: encoding.FieldTypeDateTime},
		{Name: "score", Type: encoding.FieldTypeF64},
		{Name: "g", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
	}}
	cases := []struct {
		params string
		code   errors.Code
	}{
		{`{"ratios":[0.8,0.2],"mode":"temporal"}`, errors.PROCESSING_CONFIG},
		{`{"ratios":[0.8,0.2],"mode":"temporal","order_by":"week","seed":0}`, errors.PROCESSING_CONFIG},
		{`{"ratios":[0.8,0.2],"mode":"temporal","order_by":"week","stratify":"g"}`, errors.PROCESSING_CONFIG},
		{`{"ratios":[0.8,0.2],"order_by":"week"}`, errors.PROCESSING_CONFIG},
		{`{"ratios":[0.8,0.2],"mode":"random","order_by":"week"}`, errors.PROCESSING_CONFIG},
		{`{"ratios":[0.8,0.2],"mode":"time"}`, errors.PROCESSING_CONFIG},
		{`{"ratios":[0.8,0.2],"mode":"temporal","order_by":"score"}`, errors.PULSE_FIELD_TYPE_UNSUPPORTED},
		{`{"ratios":[0.8,0.2],"mode":"temporal","order_by":"g"}`, errors.PULSE_FIELD_TYPE_UNSUPPORTED},
	}
	for _, c := range cases {
		_, err := newTrainTestSplit(&types.Feature{Params: json.RawMessage(c.params)}, schema)
		ce, ok := err.(*errors.CodedError)
		if !ok || ce.Code != c.code {
			t.Errorf("%s: err %v, want %s", c.params, err, c.code)
		}
	}
	for _, field := range []string{"week", "day", "at"} {
		if _, err := newTrainTestSplit(&types.Feature{Params: json.RawMessage(`{"ratios":[0.8,0.2],"mode":"temporal","order_by":"` + field + `"}`)}, schema); err != nil {
			t.Errorf("order_by %s: %v", field, err)
		}
	}
}

// TestTrainTestSplit_TemporalNeverStreams: temporal mode routes buffered
// (featstream); the streaming pre-pass refuses it rather than fall back
// to a random split.
func TestTrainTestSplit_TemporalNeverStreams(t *testing.T) {
	t.Parallel()

	feat := &types.Feature{Type: types.FEAT_TRAIN_TEST_SPLIT, Params: json.RawMessage(`{"ratios":[0.8,0.2],"mode":"temporal","order_by":"t"}`)}
	if IsStreamable([]*types.Feature{feat}, nil) {
		t.Error("temporal split reported streamable")
	}
	if _, err := BuildStreaming([]*types.Feature{feat}, nil); err == nil {
		t.Error("BuildStreaming accepted a temporal split")
	}
	c, err := newTrainTestSplit(feat, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.(StreamingComputer).Finalize(); err == nil {
		t.Error("Finalize ran a temporal split on the streaming arm")
	}
	random := &types.Feature{Type: types.FEAT_TRAIN_TEST_SPLIT, Params: json.RawMessage(`{"ratios":[0.8,0.2]}`)}
	if !IsStreamable([]*types.Feature{random}, nil) {
		t.Error("random split no longer streamable")
	}
}
