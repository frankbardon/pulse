package feature

import (
	"encoding/json"
	"math"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// streamCompute drives a StreamingComputer across the records in
// iteration order and assembles the per-row outputs into the same
// map[label]Output shape Compute returns. Used for buffered/streaming
// parity assertions.
func streamCompute(t *testing.T, sc StreamingComputer, records []Record, field string) map[string]Output {
	t.Helper()
	for _, r := range records {
		if err := sc.PrePass(r, field); err != nil {
			t.Fatalf("PrePass: %v", err)
		}
	}
	if err := sc.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	out := make(map[string]Output)
	for i, r := range records {
		row, err := sc.EmitRow(r, field)
		if err != nil {
			t.Fatalf("EmitRow row %d: %v", i, err)
		}
		for label, o := range row {
			cur, ok := out[label]
			if !ok {
				cur = Output{
					Values: make([]float64, len(records)),
					Nulls:  make([]bool, len(records)),
				}
				out[label] = cur
			}
			if len(o.Nulls) > 0 && o.Nulls[0] {
				cur.Nulls[i] = true
			} else {
				cur.Values[i] = o.Values[0]
			}
		}
	}
	return out
}

// assertOutputsEqual compares two map[label]Output payloads. Treats a
// missing or all-false Nulls slice as equivalent. Used to compare
// buffered Compute output against streamCompute output.
func assertOutputsEqual(t *testing.T, label string, want, got map[string]Output) {
	t.Helper()
	wantKeys := keysOf(want)
	gotKeys := keysOf(got)
	if !equalStrings(wantKeys, gotKeys) {
		t.Fatalf("%s: column sets differ\nwant=%v\ngot=%v", label, wantKeys, gotKeys)
	}
	for _, k := range wantKeys {
		w := want[k]
		g := got[k]
		if len(w.Values) != len(g.Values) {
			t.Fatalf("%s[%s]: length differs want=%d got=%d", label, k, len(w.Values), len(g.Values))
		}
		for i := range w.Values {
			wn := i < len(w.Nulls) && w.Nulls[i]
			gn := i < len(g.Nulls) && g.Nulls[i]
			if wn != gn {
				t.Errorf("%s[%s][%d]: null bit want=%v got=%v", label, k, i, wn, gn)
				continue
			}
			if wn {
				continue // both null; values undefined
			}
			if math.Abs(w.Values[i]-g.Values[i]) > 1e-9 {
				t.Errorf("%s[%s][%d]: value want=%f got=%f", label, k, i, w.Values[i], g.Values[i])
			}
		}
	}
}

func keysOf(m map[string]Output) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func mkNumeric(field string, values []float64, nulls []bool) []Record {
	out := make([]Record, len(values))
	for i := range values {
		r := newFakeRecord()
		if nulls != nil && nulls[i] {
			r.nulls[field] = true
		} else {
			r.num[field] = values[i]
		}
		out[i] = r
	}
	return out
}

func mkCategorical(field string, values []string, nulls []bool) []Record {
	out := make([]Record, len(values))
	for i := range values {
		r := newFakeRecord()
		if nulls != nil && nulls[i] {
			r.nulls[field] = true
		} else {
			r.str[field] = values[i]
		}
		out[i] = r
	}
	return out
}

func TestStreamingParity_Log(t *testing.T) {
	records := mkNumeric("x", []float64{0, math.E - 1, 99, -2, 0}, []bool{false, false, false, false, true})
	feat := &types.Feature{Type: types.FEAT_LOG, Field: "x"}

	buffered, err := newLog(feat, nil)
	if err != nil {
		t.Fatalf("buffered factory: %v", err)
	}
	wantOut, err := buffered.Compute(records, "x")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	stream, _ := newLog(feat, nil)
	gotOut := streamCompute(t, stream.(StreamingComputer), records, "x")
	assertOutputsEqual(t, "LOG", wantOut, gotOut)
}

func TestStreamingParity_Sqrt(t *testing.T) {
	records := mkNumeric("x", []float64{0, 4, 9, -1, 0}, []bool{false, false, false, false, true})
	feat := &types.Feature{Type: types.FEAT_SQRT, Field: "x"}

	buffered, _ := newSqrt(feat, nil)
	wantOut, err := buffered.Compute(records, "x")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	stream, _ := newSqrt(feat, nil)
	gotOut := streamCompute(t, stream.(StreamingComputer), records, "x")
	assertOutputsEqual(t, "SQRT", wantOut, gotOut)
}

func TestStreamingParity_BucketizeExplicit(t *testing.T) {
	records := mkNumeric("x", []float64{1, 11, 25, 0, 100}, []bool{false, false, false, true, false})
	feat := &types.Feature{
		Type:   types.FEAT_BUCKETIZE,
		Field:  "x",
		Params: json.RawMessage(`{"boundaries":[10,20,30]}`),
	}
	buffered, _ := newBucketize(feat, nil)
	wantOut, err := buffered.Compute(records, "x")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	stream, _ := newBucketize(feat, nil)
	gotOut := streamCompute(t, stream.(StreamingComputer), records, "x")
	assertOutputsEqual(t, "BUCKETIZE-explicit", wantOut, gotOut)
}

func TestStreamingParity_BucketizeQuantile(t *testing.T) {
	records := mkNumeric("x",
		[]float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 0},
		[]bool{false, false, false, false, false, false, false, false, false, false, true},
	)
	feat := &types.Feature{
		Type:   types.FEAT_BUCKETIZE,
		Field:  "x",
		Params: json.RawMessage(`{"quantiles":4}`),
	}
	buffered, _ := newBucketize(feat, nil)
	wantOut, err := buffered.Compute(records, "x")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	stream, _ := newBucketize(feat, nil)
	gotOut := streamCompute(t, stream.(StreamingComputer), records, "x")
	assertOutputsEqual(t, "BUCKETIZE-quantile", wantOut, gotOut)
}

func TestStreamingParity_OneHot(t *testing.T) {
	schema := makeCategoricalSchema(t, "region", "north", "south", "east")
	records := []Record{
		newStringRecord("region", "north"),
		newStringRecord("region", "south"),
		newStringRecord("region", "north"),
	}
	null := newFakeRecord()
	null.nulls["region"] = true
	records = append(records, null)

	feat := &types.Feature{Type: types.FEAT_ONE_HOT, Field: "region"}
	buffered, err := newOneHot(feat, schema)
	if err != nil {
		t.Fatalf("buffered factory: %v", err)
	}
	wantOut, err := buffered.Compute(records, "region")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	stream, _ := newOneHot(feat, schema)
	gotOut := streamCompute(t, stream.(StreamingComputer), records, "region")
	assertOutputsEqual(t, "ONE_HOT", wantOut, gotOut)
}

func TestStreamingParity_DateFeatures(t *testing.T) {
	// Days since Unix epoch — the encoding.FieldTypeDate convention.
	records := mkNumeric("d", []float64{0, 365, 18262, 0}, []bool{false, false, false, true})
	feat := &types.Feature{Type: types.FEAT_DATE_FEATURES, Field: "d"}

	buffered, err := newDateFeatures(feat, nil)
	if err != nil {
		t.Fatalf("buffered factory: %v", err)
	}
	wantOut, err := buffered.Compute(records, "d")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	stream, _ := newDateFeatures(feat, nil)
	gotOut := streamCompute(t, stream.(StreamingComputer), records, "d")
	assertOutputsEqual(t, "DATE_FEATURES", wantOut, gotOut)
}

func TestStreamingParity_FrequencyEncode(t *testing.T) {
	records := mkCategorical("region",
		[]string{"north", "north", "north", "south", ""},
		[]bool{false, false, false, false, true},
	)
	feat := &types.Feature{Type: types.FEAT_FREQUENCY_ENCODE, Field: "region"}

	buffered, _ := newFrequencyEncode(feat, nil)
	wantOut, err := buffered.Compute(records, "region")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	stream, _ := newFrequencyEncode(feat, nil)
	gotOut := streamCompute(t, stream.(StreamingComputer), records, "region")
	assertOutputsEqual(t, "FREQUENCY_ENCODE", wantOut, gotOut)
}

func TestStreamingParity_TargetEncode(t *testing.T) {
	mk := func(region string, target float64, regionNull, targetNull bool) Record {
		r := newFakeRecord()
		if regionNull {
			r.nulls["region"] = true
		} else {
			r.str["region"] = region
		}
		if targetNull {
			r.nulls["price"] = true
		} else {
			r.num["price"] = target
		}
		return r
	}
	records := []Record{
		mk("north", 10, false, false),
		mk("north", 20, false, false),
		mk("south", 4, false, false),
		mk("south", 6, false, false),
		mk("", 0, true, false),
		mk("east", 0, false, true),
	}
	feat := &types.Feature{
		Type:   types.FEAT_TARGET_ENCODE,
		Field:  "region",
		Params: json.RawMessage(`{"target":"price","smoothing":2.0}`),
	}

	buffered, err := newTargetEncode(feat, nil)
	if err != nil {
		t.Fatalf("buffered factory: %v", err)
	}
	wantOut, err := buffered.Compute(records, "region")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	stream, _ := newTargetEncode(feat, nil)
	gotOut := streamCompute(t, stream.(StreamingComputer), records, "region")
	assertOutputsEqual(t, "TARGET_ENCODE", wantOut, gotOut)
}

func TestStreamingParity_TrainTestSplit(t *testing.T) {
	records := make([]Record, 100)
	for i := range records {
		records[i] = newFakeRecord()
	}
	feat := &types.Feature{
		Type:   types.FEAT_TRAIN_TEST_SPLIT,
		Params: json.RawMessage(`{"ratios":[0.7,0.15,0.15],"seed":42}`),
	}
	buffered, err := newTrainTestSplit(feat, nil)
	if err != nil {
		t.Fatalf("buffered factory: %v", err)
	}
	wantOut, err := buffered.Compute(records, "")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	stream, _ := newTrainTestSplit(feat, nil)
	gotOut := streamCompute(t, stream.(StreamingComputer), records, "")
	assertOutputsEqual(t, "TRAIN_TEST_SPLIT", wantOut, gotOut)
}

func TestStreamingParity_TrainTestSplit_Stratified(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "label", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
	}}
	records := make([]Record, 60)
	for i := range records {
		r := newFakeRecord()
		if i%2 == 0 {
			r.str["label"] = "A"
		} else {
			r.str["label"] = "B"
		}
		records[i] = r
	}
	feat := &types.Feature{
		Type:   types.FEAT_TRAIN_TEST_SPLIT,
		Params: json.RawMessage(`{"ratios":[0.6,0.2,0.2],"seed":7,"stratify":"label"}`),
	}
	buffered, err := newTrainTestSplit(feat, schema)
	if err != nil {
		t.Fatalf("buffered factory: %v", err)
	}
	wantOut, err := buffered.Compute(records, "")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	stream, _ := newTrainTestSplit(feat, schema)
	gotOut := streamCompute(t, stream.(StreamingComputer), records, "")
	assertOutputsEqual(t, "TRAIN_TEST_SPLIT-stratified", wantOut, gotOut)
}

func TestIsStreamable_UnknownType(t *testing.T) {
	if IsStreamable([]*types.Feature{{Type: "FEAT_UNREGISTERED_TEST"}}, nil) {
		t.Error("unknown feature type should not be streamable")
	}
}

func TestIsStreamable_FactoryError(t *testing.T) {
	// FEAT_LOG without a Field is a PROCESSING_CONFIG error from its
	// factory; IsStreamable must surface that as not-streamable so the
	// buffered path can re-run and produce the canonical error.
	if IsStreamable([]*types.Feature{{Type: types.FEAT_LOG}}, nil) {
		t.Error("factory error should mark feature as not streamable")
	}
}

func TestIsStreamable_AllRegisteredOperators(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64, Description: "numeric for log/sqrt/bucketize"},
		{Name: "d", Type: encoding.FieldTypeDate, Description: "date for date_features"},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8,
			Dictionary: encoding.NewDictionary(), Description: "category for one_hot/freq/target"},
		{Name: "price", Type: encoding.FieldTypeF64, Description: "target for target_encode"},
	}}

	cases := []*types.Feature{
		{Type: types.FEAT_LOG, Field: "x"},
		{Type: types.FEAT_SQRT, Field: "x"},
		{Type: types.FEAT_BUCKETIZE, Field: "x", Params: json.RawMessage(`{"boundaries":[1,2,3]}`)},
		{Type: types.FEAT_ONE_HOT, Field: "region"},
		{Type: types.FEAT_DATE_FEATURES, Field: "d"},
		{Type: types.FEAT_FREQUENCY_ENCODE, Field: "region"},
		{Type: types.FEAT_TARGET_ENCODE, Field: "region", Params: json.RawMessage(`{"target":"price"}`)},
		{Type: types.FEAT_TRAIN_TEST_SPLIT, Params: json.RawMessage(`{"ratios":[0.5,0.5],"seed":1}`)},
	}
	for _, c := range cases {
		if !IsStreamable([]*types.Feature{c}, schema) {
			t.Errorf("%s: expected IsStreamable=true", c.Type)
		}
	}
}
