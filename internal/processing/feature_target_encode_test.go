package processing

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// teSchema: a categorical g (levels a..d), an outcome y and a stored
// training flag s.
func teSchema() *encoding.Schema {
	dict := encoding.NewDictionary()
	for _, l := range []string{"a", "b", "c", "d"} {
		dict.Add(l)
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "g", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
		{Name: "y", Type: encoding.FieldTypeF64},
		{Name: "s", Type: encoding.FieldTypeU8},
	}}
}

// teRecords builds n deterministic records; y(i) supplies the outcome.
func teRecords(schema *encoding.Schema, n int, y func(i int) float64) []*Record {
	out := make([]*Record, n)
	for i := range out {
		s := 0.0
		if i%3 == 0 {
			s = 1
		}
		out[i] = NewRecord(schema, map[string]float64{"g": float64(i % 4), "y": y(i), "s": s})
	}
	return out
}

func teBaseY(i int) float64 { return float64((i*37)%11) + 0.25 }

func teFeatures(teParams string) []*types.Feature {
	return []*types.Feature{
		{Type: types.FEAT_TRAIN_TEST_SPLIT, Label: "split", Params: json.RawMessage(`{"ratios":[0.6,0.2,0.2],"seed":3}`)},
		{Type: types.FEAT_TARGET_ENCODE, Field: "g", Label: "te", Params: json.RawMessage(teParams)},
	}
}

func columnOf(t *testing.T, recs []*Record, name string) ([]float64, []bool) {
	t.Helper()
	vals := make([]float64, len(recs))
	nulls := make([]bool, len(recs))
	for i, r := range recs {
		v, ok := r.NumericValue(name)
		vals[i], nulls[i] = v, !ok
	}
	return vals, nulls
}

// TestTargetEncode_FitOnSplit_HoldoutNeverLeaks drives the engine's own
// fit_on compile (featureResolver → FILTER_EXPRESSION build) over an
// EARLIER feature's column: FEAT_TRAIN_TEST_SPLIT then a target encoder
// with fit_on "split == 0". Changing every validation / test record's
// outcome leaves every record's encoding bit-identical, with and
// without folds; a holdout record carries the training-only mean.
func TestTargetEncode_FitOnSplit_HoldoutNeverLeaks(t *testing.T) {
	schema := teSchema()
	p := NewProcessor(schema)
	for _, params := range []string{
		`{"target":"y","fit_on":"split == 0"}`,
		`{"target":"y","fit_on":"split == 0","folds":5,"rng_seed":9}`,
	} {
		base := teRecords(schema, 240, teBaseY)
		if err := p.applyFeatures(teFeatures(params), base); err != nil {
			t.Fatalf("%s: %v", params, err)
		}
		split, _ := columnOf(t, base, "split")
		te, teNull := columnOf(t, base, "te")

		holdout := 0
		perturbed := teRecords(schema, 240, func(i int) float64 {
			if split[i] != 0 {
				return teBaseY(i)*-3 + 1000
			}
			return teBaseY(i)
		})
		for i := range split {
			if split[i] != 0 {
				holdout++
			}
		}
		if holdout == 0 || holdout == len(split) {
			t.Fatalf("degenerate split: %d holdout rows", holdout)
		}
		if err := p.applyFeatures(teFeatures(params), perturbed); err != nil {
			t.Fatal(err)
		}
		te2, teNull2 := columnOf(t, perturbed, "te")
		for i := range te {
			if teNull[i] != teNull2[i] || math.Float64bits(te[i]) != math.Float64bits(te2[i]) {
				t.Fatalf("%s row %d (split %v): %v -> %v after perturbing holdout outcomes", params, i, split[i], te[i], te2[i])
			}
		}

		// Without folds a holdout row reads the training mean of its level.
		if params == `{"target":"y","fit_on":"split == 0"}` {
			var sum float64
			var n int
			for i, r := range base {
				if split[i] == 0 && i%4 == 1 {
					y, _ := r.NumericValue("y")
					sum += y
					n++
				}
			}
			for i := range base {
				if split[i] != 0 && i%4 == 1 {
					if te[i] != sum/float64(n) {
						t.Errorf("holdout row %d = %v, want training mean %v", i, te[i], sum/float64(n))
					}
					break
				}
			}
		}
	}
}

// TestTargetEncode_FitOn_StreamingMatchesBuffered: a fit_on over a
// STORED column streams (the pre-pass reads it from the raw record) and
// the streamed response equals the buffered one; a fit_on over an
// earlier feature's column routes buffered.
func TestTargetEncode_FitOn_StreamingMatchesBuffered(t *testing.T) {
	schema := teSchema()
	req := func(params string) *types.Request {
		return &types.Request{
			Features: []*types.Feature{{Type: types.FEAT_TARGET_ENCODE, Field: "g", Label: "te", Params: json.RawMessage(params)}},
			Groups:   []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_SUM, Field: "te", Label: "sum"},
				{Type: types.AGG_COUNT, Field: "te", Label: "n"},
			},
		}
	}
	for _, params := range []string{
		`{"target":"y","fit_on":"s == 0"}`,
		`{"target":"y","fit_on":"s == 0 && y > 1","folds":3,"rng_seed":4,"smoothing":1.5}`,
	} {
		ps := NewProcessor(schema)
		streamed, err := ps.Process(context.Background(), req(params), NewSliceIterator(teRecords(schema, 97, teBaseY)))
		if err != nil {
			t.Fatalf("%s streaming: %v", params, err)
		}
		if ps.lastPath != PathStreaming {
			t.Fatalf("%s: ran %v, want streaming", params, ps.lastPath)
		}
		pb := NewProcessor(schema)
		buffered, err := pb.processRecords(context.Background(), pb.prepare(req(params)), teRecords(schema, 97, teBaseY))
		if err != nil {
			t.Fatalf("%s buffered: %v", params, err)
		}
		if !reflect.DeepEqual(streamed.Data, buffered.Data) {
			t.Errorf("%s: streamed %v != buffered %v", params, streamed.Data, buffered.Data)
		}
	}

	derived := req(`{"target":"y","fit_on":"split == 0"}`)
	derived.Features = append([]*types.Feature{{Type: types.FEAT_TRAIN_TEST_SPLIT, Label: "split", Params: json.RawMessage(`{"ratios":[0.5,0.5]}`)}}, derived.Features...)
	if CanStreamRequest(derived, schema) {
		t.Error("a fit_on over an earlier feature's column must route buffered")
	}
	pd := NewProcessor(schema)
	if _, err := pd.Process(context.Background(), derived, NewSliceIterator(teRecords(schema, 40, teBaseY))); err != nil {
		t.Fatal(err)
	}
	if pd.lastPath != PathBuffered {
		t.Errorf("derived fit_on ran %v, want buffered", pd.lastPath)
	}
}

// TestTargetEncode_FitOn_ReadsSlotZone: the engine compiles fit_on with
// the feature slot's `tz` (written by descriptor.ZonedExpressions), so
// to_day reads the local day: 2023-11-15T00:00Z is 2023-11-14 in New
// York (epoch day 19675), still 19676 in UTC.
func TestTargetEncode_FitOn_ReadsSlotZone(t *testing.T) {
	dict := encoding.NewDictionary()
	dict.Add("a")
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "g", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
		{Name: "y", Type: encoding.FieldTypeF64},
		{Name: "ts", Type: encoding.FieldTypeDateTime},
	}}
	const midnightUTC = 1700006400
	run := func(tz string) ([]float64, []bool) {
		recs := []*Record{
			NewRecord(schema, map[string]float64{"g": 0, "y": 10, "ts": midnightUTC}),
			NewRecord(schema, map[string]float64{"g": 0, "y": 2, "ts": midnightUTC + 3*86400}),
		}
		feats := []*types.Feature{{Type: types.FEAT_TARGET_ENCODE, Field: "g", Label: "te", TimeZone: tz,
			Params: json.RawMessage(`{"target":"y","fit_on":"to_day(ts) == 19675"}`)}}
		if err := NewProcessor(schema).applyFeatures(feats, recs); err != nil {
			t.Fatalf("tz %q: %v", tz, err)
		}
		return columnOf(t, recs, "te")
	}
	if v, nulls := run("America/New_York"); nulls[0] || v[0] != 10 || v[1] != 10 {
		t.Errorf("New York: %v (nulls %v), want the 2023-11-14 row's 10 on both", v, nulls)
	}
	if _, nulls := run(""); !nulls[0] || !nulls[1] {
		t.Errorf("UTC: no row is on day 19675, want every row null; nulls %v", nulls)
	}
}

// TestFeatures_DerivedPrePassInputs_StreamedEqualsBuffered: a global-pass
// pre-pass over an earlier feature's column (a target encoder's target, a
// quantile bucketize's field) routes buffered, so Process gives the
// buffered figures — the streaming drive used to fold nothing there (all
// nulls, every row in bucket 0).
func TestFeatures_DerivedPrePassInputs_StreamedEqualsBuffered(t *testing.T) {
	schema := teSchema()
	for name, feats := range map[string][]*types.Feature{
		"target encode of a log": {
			{Type: types.FEAT_LOG, Field: "y", Label: "ly"},
			{Type: types.FEAT_TARGET_ENCODE, Field: "g", Label: "out", Params: json.RawMessage(`{"target":"ly"}`)},
		},
		"quantiles of a log": {
			{Type: types.FEAT_LOG, Field: "y", Label: "ly"},
			{Type: types.FEAT_BUCKETIZE, Field: "ly", Label: "out", Params: json.RawMessage(`{"quantiles":4}`)},
		},
	} {
		req := &types.Request{Features: feats, Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "out", Label: "sum"},
			{Type: types.AGG_NULL_COUNT, Field: "out", Label: "nn"},
		}}
		p := NewProcessor(schema)
		got, err := p.Process(context.Background(), req, NewSliceIterator(teRecords(schema, 40, teBaseY)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		pb := NewProcessor(schema)
		want, err := pb.processRecords(context.Background(), pb.prepare(req), teRecords(schema, 40, teBaseY))
		if err != nil {
			t.Fatal(err)
		}
		if p.lastPath != PathBuffered || !reflect.DeepEqual(got.Data, want.Data) {
			t.Errorf("%s: ran %v with %v, want buffered %v", name, p.lastPath, got.Data, want.Data)
		}
	}
}
