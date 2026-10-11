package vectors

import (
	"encoding/json"
	stderrors "errors"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func distanceMatrixSpec(params string) types.MatrixSpec {
	s := types.MatrixSpec{Type: types.MAT_DISTANCE, Fields: []string{"q_1", "q_2"}}
	if params != "" {
		s.Params = json.RawMessage(params)
	}
	return s
}

// TestResolveMatrices_Distance: params.between resolves — a field
// grouper kept as written, a GROUP_KMEANS between's named vector moved
// into Fields — with the metric (euclidean by default) and standardize;
// every malformed shape is refused with its reason.
func TestResolveMatrices_Distance(t *testing.T) {
	vec := []types.VectorSpec{{Name: "seg", Fields: []string{"score", "mid"}}}
	t.Run("resolves", func(t *testing.T) {
		req := &types.Request{Vectors: vec, Matrices: []types.MatrixSpec{
			distanceMatrixSpec(`{"between": {"type": "GROUP_CATEGORY", "field": "region", "include": ["a"]}}`),
			distanceMatrixSpec(`{"between": {"type": "GROUP_KMEANS", "vector": "seg", "params": {"k": 3}}, "metric": "cosine", "standardize": true}`),
		}}
		req.Matrices[1].Name = "km"
		got, err := ResolveMatrices(req, testSchema(), nil)
		if err != nil {
			t.Fatalf("refused: %v", err)
		}
		a, b := got[0], got[1]
		if a.Metric != DistanceEuclidean || a.Standardize || a.Between == nil || a.Between.Field != "region" || !slices.Equal(a.Between.Include, []string{"a"}) || !a.Streamable || !a.Mergeable {
			t.Errorf("category between = %+v / %+v", a, a.Between)
		}
		if b.Metric != DistanceCosine || !b.Standardize || b.Between.Vector != "" || !slices.Equal(b.Between.Fields, []string{"score", "mid"}) || b.Streamable || b.Mergeable {
			t.Errorf("k-means between = %+v / %+v", b, b.Between)
		}
		if !Referenced(req)["seg"] {
			t.Error("a vector named by a between is unreferenced")
		}
		if m, ok := MatrixMembers(&types.Request{Matrices: req.Matrices[:1]}, testSchema()); !ok || !slices.Contains(m, "region") {
			t.Errorf("MatrixMembers = %v, %v; want the between field", m, ok)
		}
	})
	cases := []struct {
		name, params, reason string
		code                 errors.Code
	}{
		{"no params", ``, "between_required", errors.SERVICE_VALIDATION},
		{"no between", `{"metric": "manhattan"}`, "between_required", errors.SERVICE_VALIDATION},
		{"bad metric", `{"between": {"type": "GROUP_CATEGORY", "field": "region"}, "metric": "hamming"}`, "bad_params", errors.SERVICE_VALIDATION},
		{"pairwise", `{"between": {"type": "GROUP_CATEGORY", "field": "region"}, "missing": "pairwise"}`, "pairwise_unsupported", errors.SERVICE_VALIDATION},
		{"unknown key", `{"between": {"type": "GROUP_CATEGORY", "field": "region"}, "ddof": 1}`, "bad_params", errors.SERVICE_VALIDATION},
		{"between unknown key", `{"between": {"type": "GROUP_CATEGORY", "field": "region", "nope": 1}}`, "bad_params", errors.SERVICE_VALIDATION},
		{"date between", `{"between": {"type": "GROUP_DATE", "field": "day"}}`, "between_unsupported", errors.SERVICE_VALIDATION},
		{"date ranges between", `{"between": {"type": "GROUP_DATE_RANGES", "field": "day"}}`, "between_unsupported", errors.SERVICE_VALIDATION},
		{"unknown between", `{"between": {"type": "GROUP_NOPE", "field": "region"}}`, "unknown_between_type", errors.SERVICE_VALIDATION},
		{"between weight", `{"between": {"type": "GROUP_CATEGORY", "field": "region", "weight": null}}`, "bad_params", errors.SERVICE_VALIDATION},
		{"between tz", `{"between": {"type": "GROUP_CATEGORY", "field": "region", "tz": "UTC"}}`, "bad_params", errors.SERVICE_VALIDATION},
		{"no field", `{"between": {"type": "GROUP_CATEGORY"}}`, "bad_params", errors.SERVICE_VALIDATION},
		{"unknown field", `{"between": {"type": "GROUP_CATEGORY", "field": "nope"}}`, "unknown_field", errors.SERVICE_VALIDATION},
		{"vector on a field grouper", `{"between": {"type": "GROUP_CATEGORY", "fields": ["q_1"]}}`, "vector_not_accepted", errors.SERVICE_VALIDATION},
		{"k-means without k", `{"between": {"type": "GROUP_KMEANS", "fields": ["q_1"]}}`, "k_required", errors.SERVICE_VALIDATION},
		{"k-means unknown vector", `{"between": {"type": "GROUP_KMEANS", "vector": "nope", "params": {"k": 2}}}`, "", errors.PULSE_VECTOR_UNKNOWN},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ResolveMatrices(&types.Request{Vectors: vec, Matrices: []types.MatrixSpec{distanceMatrixSpec(c.params)}}, testSchema(), nil)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != c.code || (c.reason != "" && ce.Details["reason"] != c.reason) {
				t.Fatalf("got %v (details %v), want %s / %s", err, detailsOf(err), c.code, c.reason)
			}
		})
	}
	t.Run("hidden between", func(t *testing.T) {
		route := func(g types.GroupType) types.GroupType {
			if g == types.GROUP_KMEANS {
				return ""
			}
			return g
		}
		req := &types.Request{Matrices: []types.MatrixSpec{distanceMatrixSpec(`{"between": {"type": "GROUP_KMEANS", "fields": ["q_1"], "params": {"k": 2}}}`)}}
		_, err := ResolveMatricesRouted(req, testSchema(), nil, route)
		if err == nil || err.Details["reason"] != "unknown_between_type" {
			t.Fatalf("hidden between: %v", err)
		}
	})
}

func detailsOf(err error) map[string]any {
	var ce *errors.CodedError
	if stderrors.As(err, &ce) {
		return ce.Details
	}
	return nil
}

// TestMatrixSpec_DistanceStreamability: a MAT_DISTANCE spec streams when
// its between grouper does and merges when that grouper also merges.
func TestMatrixSpec_DistanceStreamability(t *testing.T) {
	for _, c := range []struct {
		between           string
		stream, mergeable bool
	}{
		{"GROUP_CATEGORY", true, true},
		{"GROUP_RANGE", true, true},
		{"GROUP_SET_PER_ELEMENT", true, true},
		{"GROUP_ROUNDED", true, false},
		{"GROUP_QUANTILE", false, false},
		{"GROUP_KMEANS", false, false},
	} {
		s := distanceMatrixSpec(`{"between": {"type": "` + c.between + `", "field": "x"}}`)
		if s.Streamable() != c.stream || s.Mergeable() != c.mergeable {
			t.Errorf("%s: streamable %v mergeable %v, want %v / %v", c.between, s.Streamable(), s.Mergeable(), c.stream, c.mergeable)
		}
	}
}
