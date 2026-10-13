package vectors

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// kindSchema has two native vector fields with a schema kind — emb
// (scale) and m (measure) — beside a scalar column.
func kindSchema() *encoding.Schema {
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "emb", Type: encoding.FieldTypeVecF64},
			{Name: "m", Type: encoding.FieldTypeVecF32},
			{Name: "x", Type: encoding.FieldTypeF64},
		},
		Vectors: []encoding.VectorField{
			{FieldIndex: 0, Dim: 3, Kind: encoding.VectorKindScale},
			{FieldIndex: 1, Dim: 2, Kind: encoding.VectorKindMeasure},
		},
	}
}

// TestNativeKind_DrivesMetricHelpers: a native vector field named
// directly carries its SCHEMA kind into every kind-driven helper — the
// metric defaults (matrix and comparison attribute), the fitted-metric
// two-pass predicate and the poor-fit issue list (operator slots and
// expressions) — exactly as a Request.Vectors spec's kind does. With no
// schema the same request reads no kind.
func TestNativeKind_DrivesMetricHelpers(t *testing.T) {
	s := kindSchema()
	if k := KindIn(nil, s, "m"); k != types.VectorKindMeasure {
		t.Errorf("KindIn(m) = %q, want measure", k)
	}
	if k := KindIn(nil, s, "x"); k != "" {
		t.Errorf("KindIn(scalar x) = %q, want none", k)
	}
	if k := KindIn([]types.VectorSpec{{Name: "v", Kind: types.VectorKindBinary}}, s, "v"); k != types.VectorKindBinary {
		t.Errorf("KindIn(spec v) = %q, want binary", k)
	}

	dist := &types.Request{Matrices: []types.MatrixSpec{{Type: types.MAT_DISTANCE, Vector: "m"}}}
	if got := MatrixMetricDefaults(dist, s); len(got) != 1 || got[0].Metric != "euclidean" || got[0].TableDefault != "std_euclidean" || got[0].Kind != types.VectorKindMeasure {
		t.Errorf("MatrixMetricDefaults over native m = %+v, want euclidean (measure; MAT_DISTANCE offers no std_euclidean)", got)
	}
	if got := MatrixMetricDefaults(dist, nil); len(got) != 0 {
		t.Errorf("MatrixMetricDefaults without a schema = %+v, want none", got)
	}

	vd := &types.Attribute{Type: types.ATTR_VEC_DISTANCE, Vector: "m", Params: json.RawMessage(`{"reference":[1,2]}`)}
	attr := &types.Request{Attributes: []*types.Attribute{vd}}
	if got := AttributeMetricDefaults(attr, s); len(got) != 1 || got[0].Metric != "std_euclidean" {
		t.Errorf("AttributeMetricDefaults over native m = %+v, want std_euclidean", got)
	}
	if !ComparisonFitted(vd.Type, vd, nil, s) {
		t.Error("ComparisonFitted over native measure m = false, want true (std_euclidean default fits)")
	}
	if ComparisonFitted(vd.Type, vd, nil, nil) {
		t.Error("ComparisonFitted without a schema = true, want false")
	}

	poor := &types.Request{
		Attributes: []*types.Attribute{{Type: types.ATTR_PROFILE_SIMILARITY, Vector: "emb",
			Params: json.RawMessage(`{"metric":"cosine","reference":[1,2,3]}`)}},
		Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "cosine(emb, emb) > 0"}},
	}
	issues := MetricUnsuitedIssues(poor, s)
	if len(issues) != 2 {
		t.Fatalf("MetricUnsuitedIssues over native emb = %d issues, want 2 (slot + expression): %v", len(issues), issues)
	}
	for _, w := range issues {
		if w.Details["vector"] != "emb" || w.Details["kind"] != "scale" {
			t.Errorf("issue details = %v, want vector emb kind scale", w.Details)
		}
	}
	if got := MetricUnsuitedIssues(poor, nil); len(got) != 0 {
		t.Errorf("MetricUnsuitedIssues without a schema = %v, want none", got)
	}
}

// TestNativeKindsMatter: true exactly when a slot MetricUnsuitedIssues
// judges names a vector no spec defines (the runtime then opens the
// schema); a spec-only request never needs it.
func TestNativeKindsMatter(t *testing.T) {
	cases := []struct {
		name string
		req  *types.Request
		want bool
	}{
		{"nil", nil, false},
		{"spec-only matrix", &types.Request{Vectors: []types.VectorSpec{{Name: "v", Fields: []string{"x"}}},
			Matrices: []types.MatrixSpec{{Type: types.MAT_DISTANCE, Vector: "v"}}}, false},
		{"native matrix", &types.Request{Matrices: []types.MatrixSpec{{Type: types.MAT_DISTANCE, Vector: "m"}}}, true},
		{"metric-free matrix", &types.Request{Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Vector: "m"}}}, false},
		{"native comparison", &types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_VEC_DISTANCE, Vector: "m"}}}, true},
		{"native expression", &types.Request{Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "cosine(m, m) > 0"}}}, true},
		{"spec expression", &types.Request{Vectors: []types.VectorSpec{{Name: "v", Fields: []string{"x"}}},
			Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "cosine(v, v) > 0"}}}, false},
	}
	for _, c := range cases {
		if got := NativeKindsMatter(c.req); got != c.want {
			t.Errorf("%s: NativeKindsMatter = %v, want %v", c.name, got, c.want)
		}
	}
}
