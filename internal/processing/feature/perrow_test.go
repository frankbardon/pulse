package feature

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

func makeRecords(values []float64, nulls []bool, field string) []Record {
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

func TestLog_Compute(t *testing.T) {
	records := makeRecords(
		[]float64{0, math.E - 1, 99},
		[]bool{false, false, false},
		"x",
	)

	c, err := newLog(&types.Feature{Field: "x"}, nil)
	if err != nil {
		t.Fatalf("newLog: %v", err)
	}
	out, err := c.Compute(records, "x")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	got, ok := out["LOG_x"]
	if !ok {
		t.Fatalf("expected LOG_x output, got %v", out)
	}
	if got.Values[0] != 0 {
		t.Errorf("log(0+1) = 0; got %f", got.Values[0])
	}
	if math.Abs(got.Values[1]-1.0) > 1e-9 {
		t.Errorf("log(e-1+1) = 1; got %f", got.Values[1])
	}
}

func TestLog_NullPropagation(t *testing.T) {
	records := makeRecords([]float64{1, 0}, []bool{false, true}, "x")

	c, _ := newLog(&types.Feature{Field: "x"}, nil)
	out, _ := c.Compute(records, "x")

	if !out["LOG_x"].Nulls[1] {
		t.Errorf("expected null mask at index 1 for null input")
	}
	if out["LOG_x"].Nulls[0] {
		t.Errorf("did not expect null at index 0")
	}
}

func TestLog_NegativeBeyondMinusOneIsNull(t *testing.T) {
	records := makeRecords([]float64{-2, -1, -0.5}, nil, "x")
	c, _ := newLog(&types.Feature{Field: "x"}, nil)
	out, _ := c.Compute(records, "x")

	got := out["LOG_x"]
	if !got.Nulls[0] {
		t.Errorf("expected null for x=-2")
	}
	if !got.Nulls[1] {
		t.Errorf("expected null for x=-1")
	}
	if got.Nulls[2] {
		t.Errorf("did not expect null for x=-0.5")
	}
}

func TestLog_RequiresField(t *testing.T) {
	if _, err := newLog(&types.Feature{}, nil); err == nil {
		t.Error("expected error for missing field")
	}
}

func TestSqrt_Compute(t *testing.T) {
	records := makeRecords([]float64{0, 4, 9}, nil, "x")
	c, _ := newSqrt(&types.Feature{Field: "x"}, nil)
	out, _ := c.Compute(records, "x")

	got := out["SQRT_x"]
	if got.Values[0] != 0 {
		t.Errorf("sqrt(0)=0; got %f", got.Values[0])
	}
	if got.Values[1] != 2 {
		t.Errorf("sqrt(4)=2; got %f", got.Values[1])
	}
	if got.Values[2] != 3 {
		t.Errorf("sqrt(9)=3; got %f", got.Values[2])
	}
}

func TestSqrt_NegativeIsNull(t *testing.T) {
	records := makeRecords([]float64{-1, 4}, nil, "x")
	c, _ := newSqrt(&types.Feature{Field: "x"}, nil)
	out, _ := c.Compute(records, "x")

	if !out["SQRT_x"].Nulls[0] {
		t.Errorf("expected null for negative input")
	}
}

func TestSqrt_RequiresField(t *testing.T) {
	if _, err := newSqrt(&types.Feature{}, nil); err == nil {
		t.Error("expected error for missing field")
	}
}

func TestBucketize_ExplicitBoundaries(t *testing.T) {
	records := makeRecords([]float64{5, 15, 25, 35}, nil, "x")
	c, err := newBucketize(&types.Feature{
		Field:  "x",
		Params: json.RawMessage(`{"boundaries":[10,20,30]}`),
	}, nil)
	if err != nil {
		t.Fatalf("newBucketize: %v", err)
	}
	out, _ := c.Compute(records, "x")

	got := out["BUCKET_x"]
	wantIdx := []float64{0, 1, 2, 3}
	for i, w := range wantIdx {
		if got.Values[i] != w {
			t.Errorf("record %d: got bucket %f, want %f", i, got.Values[i], w)
		}
	}
}

func TestBucketize_BoundaryEdge(t *testing.T) {
	records := makeRecords([]float64{10, 20}, nil, "x")
	c, _ := newBucketize(&types.Feature{
		Field:  "x",
		Params: json.RawMessage(`{"boundaries":[10,20]}`),
	}, nil)
	out, _ := c.Compute(records, "x")

	got := out["BUCKET_x"]
	if got.Values[0] != 0 {
		t.Errorf("v=10 at boundary[0] should be bucket 0; got %f", got.Values[0])
	}
	if got.Values[1] != 1 {
		t.Errorf("v=20 at boundary[1] should be bucket 1; got %f", got.Values[1])
	}
}

func TestBucketize_NullsPropagate(t *testing.T) {
	records := makeRecords([]float64{5, 0, 25}, []bool{false, true, false}, "x")
	c, _ := newBucketize(&types.Feature{
		Field:  "x",
		Params: json.RawMessage(`{"boundaries":[10,20]}`),
	}, nil)
	out, _ := c.Compute(records, "x")

	if !out["BUCKET_x"].Nulls[1] {
		t.Errorf("expected null for null input")
	}
}

func TestBucketize_Quantile(t *testing.T) {
	vals := []float64{1, 2, 3, 4, 5, 6, 7, 8}
	records := makeRecords(vals, nil, "x")
	c, _ := newBucketize(&types.Feature{
		Field:  "x",
		Params: json.RawMessage(`{"quantiles":4}`),
	}, nil)
	out, _ := c.Compute(records, "x")

	got := out["BUCKET_x"]
	for i := range got.Values {
		if got.Nulls[i] {
			t.Errorf("record %d unexpectedly null", i)
		}
	}
	// Smallest values must land in bucket 0; largest in bucket >=2.
	if got.Values[0] != 0 {
		t.Errorf("smallest value should be in bucket 0, got %f", got.Values[0])
	}
	if got.Values[len(got.Values)-1] < 2 {
		t.Errorf("largest value should be in upper buckets, got %f", got.Values[len(got.Values)-1])
	}
}

func TestBucketize_RequiresParams(t *testing.T) {
	_, err := newBucketize(&types.Feature{Field: "x"}, nil)
	if err == nil {
		t.Error("expected error when neither boundaries nor quantiles specified")
	}
}

func TestBucketize_RejectsBothParams(t *testing.T) {
	_, err := newBucketize(&types.Feature{
		Field:  "x",
		Params: json.RawMessage(`{"boundaries":[1],"quantiles":2}`),
	}, nil)
	if err == nil {
		t.Error("expected error when both boundaries and quantiles specified")
	}
}

func TestBucketize_RequiresField(t *testing.T) {
	_, err := newBucketize(&types.Feature{
		Params: json.RawMessage(`{"quantiles":4}`),
	}, nil)
	if err == nil {
		t.Error("expected error for missing field")
	}
}

func TestPerRowFeatures_RegisteredViaInit(t *testing.T) {
	for _, ft := range []types.FeatureType{types.FEAT_LOG, types.FEAT_SQRT, types.FEAT_BUCKETIZE} {
		if _, ok := Lookup(ft); !ok {
			t.Errorf("expected %s to be registered via init()", ft)
		}
	}
}
