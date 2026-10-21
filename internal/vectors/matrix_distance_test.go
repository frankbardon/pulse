package vectors

import (
	"encoding/json"
	stderrors "errors"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg/metric"
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
		if a.Metric != DistanceDefaultMetric || a.MinValid != 1 || a.Standardize || a.Between == nil || a.Between.Field != "region" || !slices.Equal(a.Between.Include, []string{"a"}) || !a.Streamable || !a.Mergeable {
			t.Errorf("category between = %+v / %+v", a, a.Between)
		}
		if b.Metric != "cosine" || !b.Standardize || b.Between.Vector != "" || !slices.Equal(b.Between.Fields, []string{"score", "mid"}) || b.Streamable || b.Mergeable {
			t.Errorf("k-means between = %+v / %+v", b, b.Between)
		}
		if !Referenced(req)["seg"] {
			t.Error("a vector named by a between is unreferenced")
		}
		if m, ok := MatrixMembers(&types.Request{Matrices: req.Matrices[:1]}, testSchema()); !ok || !slices.Contains(m, "region") {
			t.Errorf("MatrixMembers = %v, %v; want the between field", m, ok)
		}
	})
	// Every metric the registry's MAT_DISTANCE consumer accepts
	// resolves (the list is the registry's, not local), and min_valid
	// is kept.
	t.Run("registry metrics", func(t *testing.T) {
		for _, name := range metric.Accepted(metric.ConsumerMatDistance) {
			got, err := ResolveMatrices(&types.Request{Matrices: []types.MatrixSpec{
				distanceMatrixSpec(`{"between": {"type": "GROUP_CATEGORY", "field": "region"}, "metric": "` + name + `", "min_valid": 2}`),
			}}, testSchema(), nil)
			if err != nil {
				t.Fatalf("%s refused: %v", name, err)
			}
			if got[0].Metric != name || got[0].MinValid != 2 {
				t.Errorf("%s: metric %q min_valid %d", name, got[0].Metric, got[0].MinValid)
			}
		}
		_, err := ResolveMatrices(&types.Request{Matrices: []types.MatrixSpec{
			distanceMatrixSpec(`{"between": {"type": "GROUP_CATEGORY", "field": "region"}, "metric": "nope"}`),
		}}, testSchema(), nil)
		if valid, _ := detailsOf(err)["valid"].([]string); !slices.Equal(valid, metric.Accepted(metric.ConsumerMatDistance)) {
			t.Errorf("valid = %v, want the registry's MAT_DISTANCE list", detailsOf(err)["valid"])
		}
	})
	cases := []struct {
		name, params, reason string
		code                 errors.Code
	}{
		{"no params", ``, "between_required", errors.SERVICE_VALIDATION},
		{"no between", `{"metric": "manhattan"}`, "between_required", errors.SERVICE_VALIDATION},
		{"bad metric", `{"between": {"type": "GROUP_CATEGORY", "field": "region"}, "metric": "hamming"}`, "bad_params", errors.SERVICE_VALIDATION},
		{"pre-registry sqeuclidean spelling", `{"between": {"type": "GROUP_CATEGORY", "field": "region"}, "metric": "sqeuclidean"}`, "bad_params", errors.SERVICE_VALIDATION},
		{"similarity-only metric", `{"between": {"type": "GROUP_CATEGORY", "field": "region"}, "metric": "correlation"}`, "bad_params", errors.SERVICE_VALIDATION},
		{"hellinger beside standardize", `{"between": {"type": "GROUP_CATEGORY", "field": "region"}, "metric": "hellinger", "standardize": true}`, "bad_params", errors.SERVICE_VALIDATION},
		{"aitchison beside standardize", `{"between": {"type": "GROUP_CATEGORY", "field": "region"}, "metric": "aitchison", "standardize": true}`, "bad_params", errors.SERVICE_VALIDATION},
		{"min_valid zero", `{"between": {"type": "GROUP_CATEGORY", "field": "region"}, "min_valid": 0}`, "bad_params", errors.SERVICE_VALIDATION},
		{"min_valid above the members", `{"between": {"type": "GROUP_CATEGORY", "field": "region"}, "min_valid": 3}`, "bad_params", errors.SERVICE_VALIDATION},
		{"min_valid not an integer", `{"between": {"type": "GROUP_CATEGORY", "field": "region"}, "min_valid": 1.5}`, "bad_params", errors.SERVICE_VALIDATION},
		{"pairwise", `{"between": {"type": "GROUP_CATEGORY", "field": "region"}, "missing": "pairwise"}`, "pairwise_unsupported", errors.SERVICE_VALIDATION},
		{"unknown key", `{"between": {"type": "GROUP_CATEGORY", "field": "region"}, "ddof": 1}`, "bad_params", errors.SERVICE_VALIDATION},
		{"between unknown key", `{"between": {"type": "GROUP_CATEGORY", "field": "region", "nope": 1}}`, "bad_params", errors.SERVICE_VALIDATION},
		{"unknown between", `{"between": {"type": "GROUP_NOPE", "field": "region"}}`, "unknown_between_type", errors.SERVICE_VALIDATION},
		{"between weight", `{"between": {"type": "GROUP_CATEGORY", "field": "region", "weight": null}}`, "bad_params", errors.SERVICE_VALIDATION},
		{"between tz", `{"between": {"type": "GROUP_CATEGORY", "field": "region", "tz": "UTC"}}`, "bad_params", errors.SERVICE_VALIDATION},
		{"between tz_field", `{"between": {"type": "GROUP_CATEGORY", "field": "region", "tz_field": "region"}}`, "bad_params", errors.SERVICE_VALIDATION},
		{"extension type unregistered", `{"between": {"type": "GROUP_ACME_PARITY", "field": "region"}}`, "unknown_between_type", errors.SERVICE_VALIDATION},
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
	// TODO #411: the date family resolves as a between (its zone is the
	// zone pass's, at slot matrices[i].params.between) and may carry a
	// tz; no other between may.
	t.Run("date family", func(t *testing.T) {
		for _, params := range []string{
			`{"between": {"type": "GROUP_DATE", "field": "ts", "params": {"component": "day"}}}`,
			`{"between": {"type": "GROUP_DATE", "field": "ts", "tz": "Europe/Berlin"}}`,
			`{"between": {"type": "GROUP_DATE_RANGES", "field": "day", "params": {"ranges": [{"label": "a", "start": "2026-01-01", "end": "2026-01-31"}]}}}`,
		} {
			got, err := ResolveMatrices(&types.Request{Matrices: []types.MatrixSpec{distanceMatrixSpec(params)}}, testSchema(), nil)
			if err != nil {
				t.Fatalf("%s refused: %v", params, err)
			}
			b := got[0].Between
			var want types.Group
			if jerr := json.Unmarshal([]byte(params), &struct {
				Between *types.Group `json:"between"`
			}{&want}); jerr != nil {
				t.Fatal(jerr)
			}
			if b == nil || b.Type != want.Type || b.Field != want.Field || b.TimeZone != want.TimeZone || string(b.Params) != string(want.Params) {
				t.Errorf("%s: between %+v, want %+v", params, b, want)
			}
		}
	})
	// TODO #411: a registered extension grouper is a between; its
	// vector / fields are judged by the member rules and rewritten into
	// Fields, its field (when set) must exist, and it takes no tz.
	t.Run("extension between", func(t *testing.T) {
		ext := func(name string) bool { return name == "GROUP_ACME_PARITY" }
		resolve := func(params string) ([]Matrix, *errors.CodedError) {
			return ResolveMatricesRouted(&types.Request{Vectors: vec, Matrices: []types.MatrixSpec{distanceMatrixSpec(params)}}, testSchema(), nil, nil, ext)
		}
		got, err := resolve(`{"between": {"type": "GROUP_ACME_PARITY", "field": "region", "params": {"own": 1}}}`)
		if err != nil {
			t.Fatalf("field extension between refused: %v", err)
		}
		if b := got[0].Between; b.Field != "region" || string(b.Params) != `{"own": 1}` || got[0].Streamable || got[0].Mergeable {
			t.Errorf("field extension between = %+v (streamable %v)", b, got[0].Streamable)
		}
		got, err = resolve(`{"between": {"type": "GROUP_ACME_PARITY", "vector": "seg"}}`)
		if err != nil {
			t.Fatalf("vector extension between refused: %v", err)
		}
		if b := got[0].Between; b.Vector != "" || !slices.Equal(b.Fields, []string{"score", "mid"}) {
			t.Errorf("vector extension between = %+v", b)
		}
		for _, c := range []struct{ params, reason string }{
			{`{"between": {"type": "GROUP_ACME_PARITY"}}`, "bad_params"},
			{`{"between": {"type": "GROUP_ACME_PARITY", "field": "nope"}}`, "unknown_field"},
			{`{"between": {"type": "GROUP_ACME_PARITY", "vector": "seg", "field": "nope"}}`, "unknown_field"},
			{`{"between": {"type": "GROUP_ACME_PARITY", "vector": "seg", "fields": ["q_1"]}}`, "vector_and_fields"},
			{`{"between": {"type": "GROUP_ACME_PARITY", "field": "region", "tz": "UTC"}}`, "bad_params"},
		} {
			if _, err := resolve(c.params); err == nil || err.Details["reason"] != c.reason {
				t.Errorf("%s: %v, want %s", c.params, err, c.reason)
			}
		}
		if _, err := resolve(`{"between": {"type": "GROUP_ACME_PARITY", "vector": "nope"}}`); err == nil || err.Code != errors.PULSE_VECTOR_UNKNOWN {
			t.Errorf("unknown vector: %v", err)
		}
	})
	t.Run("hidden between", func(t *testing.T) {
		route := func(g types.GroupType) types.GroupType {
			if g == types.GROUP_KMEANS {
				return ""
			}
			return g
		}
		req := &types.Request{Matrices: []types.MatrixSpec{distanceMatrixSpec(`{"between": {"type": "GROUP_KMEANS", "fields": ["q_1"], "params": {"k": 2}}}`)}}
		_, err := ResolveMatricesRouted(req, testSchema(), nil, route, nil)
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
		{"GROUP_ROUNDED", true, true},
		{"GROUP_QUANTILE", false, false},
		{"GROUP_KMEANS", false, false},
		{"GROUP_DATE", false, false},
		{"GROUP_DATE_RANGES", true, true},
		{"GROUP_ACME_PARITY", false, false}, // an extension: buffered, serial
	} {
		s := distanceMatrixSpec(`{"between": {"type": "` + c.between + `", "field": "x"}}`)
		if s.Streamable() != c.stream || s.Mergeable() != c.mergeable {
			t.Errorf("%s: streamable %v mergeable %v, want %v / %v", c.between, s.Streamable(), s.Mergeable(), c.stream, c.mergeable)
		}
	}
}
