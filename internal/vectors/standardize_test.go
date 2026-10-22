package vectors

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestResolve_Standardize: a known standardize is carried on the
// resolved vector, an unknown one is PULSE_VECTOR_INVALID
// unknown_standardize listing the valid names.
func TestResolve_Standardize(t *testing.T) {
	got, err := Resolve([]types.VectorSpec{
		{Name: "z", Fields: []string{"q_1", "q_2"}, Standardize: types.VectorStandardizeZScore},
		{Name: "raw", Fields: []string{"q_1", "q_2"}},
	}, testSchema())
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Standardize != types.VectorStandardizeZScore || got[1].Standardize != "" {
		t.Errorf("standardize = %q / %q", got[0].Standardize, got[1].Standardize)
	}
	_, err = Resolve([]types.VectorSpec{{Name: "z", Fields: []string{"q_1"}, Standardize: "minmax"}}, testSchema())
	if err == nil || err.Code != errors.PULSE_VECTOR_INVALID || err.Details["reason"] != "unknown_standardize" {
		t.Fatalf("unknown standardize: %v", err)
	}
	if v, _ := err.Details["valid"].([]string); len(v) != 1 || v[0] != "zscore" {
		t.Errorf("valid = %v", err.Details["valid"])
	}
}

// TestResolveMatrices_StandardizeConsumers: a standardized vector is
// read by a listwise co-moment MAT_* slot only. Every other consumer —
// a test, attribute, grouper or aggregation vector input, an
// expression, a MAT_DISTANCE between grouper or member vector,
// missing "pairwise", a rank method — is refused with
// PULSE_VECTOR_INVALID standardize_unsupported naming the slot, even
// on a request with no matrix.
func TestResolveMatrices_StandardizeConsumers(t *testing.T) {
	vec := []types.VectorSpec{
		{Name: "raw", Fields: []string{"q_1", "q_2"}},
		{Name: "z", Fields: []string{"q_1", "q_2", "q_3"}, Standardize: types.VectorStandardizeZScore},
	}
	params := func(s string) json.RawMessage { return json.RawMessage(s) }
	accepted := []types.MatrixSpec{
		{Type: types.MAT_COVARIANCE, Vector: "z"},
		{Name: "r", Type: types.MAT_CORRELATION, Vector: "z"},
		{Name: "pc", Type: types.MAT_PARTIAL_CORRELATION, Vector: "z", Params: params(`{"control": ["score"]}`)},
		{Name: "sem", Type: types.MAT_SEM, Vector: "z"},
		{Name: "pca", Type: types.MAT_PCA, Vector: "z"},
	}
	ms, err := ResolveMatrices(&types.Request{Vectors: vec, Matrices: accepted}, testSchema(), nil)
	if err != nil {
		t.Fatalf("listwise co-moment slots refused: %v", err)
	}
	for _, m := range ms {
		if m.Members.Standardize != types.VectorStandardizeZScore || !m.Streamable || !m.Mergeable {
			t.Errorf("%s: standardize %q, tier %v/%v", m.Name, m.Members.Standardize, m.Streamable, m.Mergeable)
		}
	}

	between := params(`{"between": {"type": "GROUP_KMEANS", "vector": "z", "params": {"k": 2}}}`)
	cases := []struct {
		name string
		req  types.Request
		slot string
	}{
		{"pairwise", types.Request{Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Vector: "z", Params: params(`{"missing": "pairwise"}`)}}}, "matrices[0].vector"},
		{"rank method", types.Request{Matrices: []types.MatrixSpec{{Type: types.MAT_CORRELATION, Vector: "z", Params: params(`{"method": "spearman"}`)}}}, "matrices[0].vector"},
		{"distance members", types.Request{Matrices: []types.MatrixSpec{{Type: types.MAT_DISTANCE, Vector: "z",
			Params: params(`{"between": {"type": "GROUP_CATEGORY", "field": "region"}}`)}}}, "matrices[0].vector"},
		{"distance between", types.Request{Matrices: []types.MatrixSpec{{Type: types.MAT_DISTANCE, Vector: "raw", Params: between}}}, "matrices[0].params.between.vector"},
		{"test", types.Request{Tests: []*types.Test{{Type: types.TEST_HOTELLING_T2, Vector: "z"}}}, "tests[0].vector"},
		{"test vector2", types.Request{Tests: []*types.Test{{Type: types.TEST_HOTELLING_T2, Vector: "raw", Vector2: "z"}}}, "tests[0].vector2"},
		{"attribute", types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_SCALE_SCORE, Vector: "z", Label: "s"}}}, "attributes[0].vector"},
		{"grouper", types.Request{Groups: []*types.Group{{Type: types.GROUP_KMEANS, Vector: "z"}}}, "groups[0].vector"},
		{"aggregation", types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_VEC_MEAN, Vector: "z"}}}, "aggregations[0].vector"},
		{"filter expression", types.Request{Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "len(z) > 0"}}}, "filterers[0].expression"},
		{"formula", types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "len(z)", Label: "n"}}}, "attributes[0].expression"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := c.req
			req.Vectors = vec
			_, err := ResolveMatrices(&req, testSchema(), nil)
			if err == nil || err.Code != errors.PULSE_VECTOR_INVALID || err.Details["reason"] != "standardize_unsupported" {
				t.Fatalf("got %v, want PULSE_VECTOR_INVALID standardize_unsupported", err)
			}
			if err.Details["slot"] != c.slot || err.Details["name"] != "z" || err.Details["vector"] != "vectors[1]" {
				t.Errorf("details = %v, want slot %s on vectors[1] z", err.Details, c.slot)
			}
		})
	}

	// The raw twin is read by every one of them.
	if _, err := ResolveMatrices(&types.Request{Vectors: vec[:1],
		Tests: []*types.Test{{Type: types.TEST_HOTELLING_T2, Vector: "raw"}}}, testSchema(), nil); err != nil {
		t.Errorf("raw vector refused: %v", err)
	}
}
