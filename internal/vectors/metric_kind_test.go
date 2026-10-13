package vectors

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg/metric"
	"github.com/frankbardon/pulse/types"
)

func kindVectors() []types.VectorSpec {
	return []types.VectorSpec{
		{Name: "m", Fields: []string{"q_1", "q_2"}, Kind: types.VectorKindMeasure},
		{Name: "s", Fields: []string{"q_1", "q_2"}, Kind: types.VectorKindScale},
		{Name: "c", Fields: []string{"q_1", "q_2"}, Kind: types.VectorKindComposition},
		{Name: "b", Fields: []string{"q_1", "q_2"}, Kind: types.VectorKindBinary},
		{Name: "n", Fields: []string{"q_1", "q_2"}},
	}
}

func distSpec(vector, params string) types.MatrixSpec {
	return types.MatrixSpec{Type: types.MAT_DISTANCE, Vector: vector, Params: json.RawMessage(params)}
}

const between = `"between": {"type": "GROUP_CATEGORY", "field": "region"}`

// TestMatrixMetricDefaults: a MAT_DISTANCE slot reading a kind vector
// with no metric gets the kind's default for the MAT_DISTANCE consumer
// (falling back to the first suitable accepted metric); an explicit
// metric, a kind-free vector, inline fields and a non-metric type are
// left alone; standardize skips the non-negative-domain metrics.
func TestMatrixMetricDefaults(t *testing.T) {
	cases := []struct {
		name         string
		spec         types.MatrixSpec
		want         string // "" = no fill
		tableDefault string
	}{
		{"scale", distSpec("s", `{`+between+`}`), "euclidean", "euclidean"},
		{"measure falls back", distSpec("m", `{`+between+`}`), "euclidean", "std_euclidean"},
		{"composition", distSpec("c", `{`+between+`}`), "aitchison", "aitchison"},
		{"composition under standardize", distSpec("c", `{`+between+`, "standardize": true}`), "manhattan", "aitchison"},
		{"binary falls back", distSpec("b", `{`+between+`}`), "cosine", "hamming"},
		{"null metric is absent", distSpec("s", `{`+between+`, "metric": null}`), "euclidean", "euclidean"},
		{"explicit metric wins", distSpec("c", `{`+between+`, "metric": "euclidean"}`), "", ""},
		{"kind-free vector", distSpec("n", `{`+between+`}`), "", ""},
		{"inline fields", types.MatrixSpec{Type: types.MAT_DISTANCE, Fields: []string{"q_1"}, Params: json.RawMessage(`{` + between + `}`)}, "", ""},
		{"malformed params", distSpec("s", `[1]`), "", ""},
		{"non-metric type", types.MatrixSpec{Type: types.MAT_CORRELATION, Vector: "s"}, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{Vectors: kindVectors(), Matrices: []types.MatrixSpec{c.spec}}
			got := MatrixMetricDefaults(req, nil)
			if c.want == "" {
				if len(got) != 0 {
					t.Fatalf("filled %+v", got)
				}
				return
			}
			if len(got) != 1 || got[0].Metric != c.want || got[0].TableDefault != c.tableDefault || got[0].Index != 0 || got[0].Vector != c.spec.Vector {
				t.Fatalf("got %+v, want %s (table %s)", got, c.want, c.tableDefault)
			}
			var p map[string]any
			if err := json.Unmarshal(got[0].Params, &p); err != nil || p["metric"] != c.want || p["between"] == nil {
				t.Errorf("params %s", got[0].Params)
			}
			if string(req.Matrices[0].Params) == string(got[0].Params) {
				t.Error("the request's own params were rewritten")
			}
			// The filled request resolves, and the filled metric is no
			// poor fit (so it never warns).
			req.Matrices[0].Params = got[0].Params
			ms, err := ResolveMatrices(req, testSchema(), nil)
			if err != nil || ms[0].Metric != c.want {
				t.Fatalf("resolve filled: %v %+v", err, ms)
			}
			if w := MetricUnsuitedIssues(req, nil); len(w) != 0 {
				t.Errorf("a defaulted metric warns: %v", w)
			}
		})
	}
}

// TestResolveKindMetric: explicit wins (and is judged by the registry),
// a kind fills its default, and neither is PULSE_VECTOR_METRIC_REQUIRED.
func TestResolveKindMetric(t *testing.T) {
	cases := []struct {
		name      string
		c         metric.Consumer
		explicit  string
		kind      types.VectorKind
		f         metric.Form
		want      string
		defaulted bool
		code      errors.Code
	}{
		{"explicit wins over kind", metric.ConsumerVecDistance, "manhattan", types.VectorKindScale, metric.Distance, "manhattan", false, ""},
		{"explicit unknown", metric.ConsumerVecDistance, "nope", types.VectorKindScale, metric.Distance, "", false, errors.PULSE_VECTOR_METRIC_UNKNOWN},
		{"kind default", metric.ConsumerVecDistance, "", types.VectorKindMeasure, metric.Distance, "std_euclidean", true, ""},
		{"similarity default", metric.ConsumerProfileSimilarity, "", types.VectorKindBinary, metric.Similarity, "jaccard", true, ""},
		{"no kind no metric", metric.ConsumerVecDistance, "", "", metric.Distance, "", false, errors.PULSE_VECTOR_METRIC_REQUIRED},
		{"kind without a default in the form", metric.ConsumerProfileSimilarity, "", types.VectorKindMeasure, metric.Similarity, "", false, errors.PULSE_VECTOR_METRIC_REQUIRED},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, defaulted, err := ResolveKindMetric(c.c, c.explicit, c.kind, c.f)
			if c.code != "" {
				if err == nil || err.Code != c.code {
					t.Fatalf("err %v, want %s", err, c.code)
				}
				return
			}
			if err != nil || m.Name() != c.want || defaulted != c.defaulted {
				t.Fatalf("got %s %v %v, want %s %v", m.Name(), defaulted, err, c.want, c.defaulted)
			}
		})
	}
}

// TestMetricUnsuitedIssues: explicit poor-fit pairs only — cosine on
// scale / measure, the Euclidean family on composition — on a matrix
// slot or a bare-identifier argument of a metric built-in in any
// expression slot; cosine on binary, a wrapped argument, a kind-free
// vector and a suited metric raise nothing. Each issue names the kind's
// default.
func TestMetricUnsuitedIssues(t *testing.T) {
	type want struct {
		slot, vector, metric, suggested, function string
	}
	cases := []struct {
		name string
		req  types.Request
		want []want
	}{
		{"cosine on scale (matrix)", types.Request{Matrices: []types.MatrixSpec{distSpec("s", `{`+between+`, "metric": "cosine"}`)}},
			[]want{{"matrices[0].params.metric", "s", "cosine", "euclidean", ""}}},
		{"cosine on measure (matrix)", types.Request{Matrices: []types.MatrixSpec{distSpec("m", `{`+between+`, "metric": "cosine"}`)}},
			[]want{{"matrices[0].params.metric", "m", "cosine", "euclidean", ""}}},
		{"euclidean on composition (matrix)", types.Request{Matrices: []types.MatrixSpec{distSpec("c", `{`+between+`, "metric": "euclidean"}`)}},
			[]want{{"matrices[0].params.metric", "c", "euclidean", "aitchison", ""}}},
		{"sq_euclidean on composition (matrix)", types.Request{Matrices: []types.MatrixSpec{distSpec("c", `{`+between+`, "metric": "sq_euclidean"}`)}},
			[]want{{"matrices[0].params.metric", "c", "sq_euclidean", "aitchison", ""}}},
		{"cosine on binary is fine", types.Request{Matrices: []types.MatrixSpec{distSpec("b", `{`+between+`, "metric": "cosine"}`)}}, nil},
		{"suited metric", types.Request{Matrices: []types.MatrixSpec{distSpec("c", `{`+between+`, "metric": "manhattan"}`)}}, nil},
		{"kind-free vector", types.Request{Matrices: []types.MatrixSpec{distSpec("n", `{`+between+`, "metric": "cosine"}`)}}, nil},
		{"implicit metric", types.Request{Matrices: []types.MatrixSpec{distSpec("c", `{`+between+`}`)}}, nil},
		{"formula cosine on scale", types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "x", Expression: "cosine(s, b) + cosine(s, b)"}}},
			[]want{{"attributes[0].expression", "s", "cosine", "correlation", "cosine"}}},
		{"formula dist on composition", types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "x", Expression: "dist(c, n)"}}},
			[]want{{"attributes[0].expression", "c", "euclidean", "aitchison", "dist"}}},
		{"filter cosine on measure", types.Request{Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "cosine(m, m) > 0.5"}}},
			[]want{{"filterers[0].expression", "m", "cosine", "", "cosine"}}},
		{"wrapped argument not judged", types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "x", Expression: "cosine(vcenter(s), vcenter(s))"}}}, nil},
		{"cosine on composition is fine", types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "x", Expression: "cosine(c, c)"}}}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := c.req
			req.Vectors = kindVectors()
			got := MetricUnsuitedIssues(&req, nil)
			if len(got) != len(c.want) {
				t.Fatalf("got %d issues %v, want %d", len(got), got, len(c.want))
			}
			for i, w := range c.want {
				g := got[i]
				if g.Code != errors.PULSE_VECTOR_METRIC_UNSUITED || g.Details["slot"] != w.slot || g.Details["vector"] != w.vector ||
					g.Details["metric"] != w.metric {
					t.Errorf("issue %d = %s %v, want %+v", i, g.Code, g.Details, w)
				}
				if s, has := g.Details["suggested"]; (w.suggested == "") == has || (has && s != w.suggested) {
					t.Errorf("issue %d suggested = %v, want %q", i, s, w.suggested)
				}
				if fn, _ := g.Details["function"].(string); fn != w.function {
					t.Errorf("issue %d function = %q, want %q", i, fn, w.function)
				}
			}
		})
	}
	if MetricUnsuitedIssues(&types.Request{Matrices: []types.MatrixSpec{distSpec("s", `{"metric": "cosine"}`)}}, nil) != nil {
		t.Error("a vector-free request raises an issue")
	}
}
