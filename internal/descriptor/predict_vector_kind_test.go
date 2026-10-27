package descriptor

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func kindReq(kind types.VectorKind, params string) *types.Request {
	return &types.Request{
		Vectors:  []types.VectorSpec{{Name: "v", Fields: []string{"q_1", "q_3"}, Kind: kind}},
		Matrices: []types.MatrixSpec{{Name: "d", Type: types.MAT_DISTANCE, Vector: "v", Params: json.RawMessage(params)}},
	}
}

const kindBetween = `"between": {"type": "GROUP_CATEGORY", "field": "region"}`

// TestPredict_MetricDefaultsApplied: a MAT_DISTANCE reading a kind
// vector with no metric reports a "metric" DefaultApplied and validates
// the filled request; under DisableDefaults it is still reported but
// the request runs as written. An explicit metric and a kind-free
// vector report none. The caller's request is never written to.
func TestPredict_MetricDefaultsApplied(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		kind    types.VectorKind
		params  string
		want    string
		reason  string
		disable bool
	}{
		{"scale", types.VectorKindScale, `{` + kindBetween + `}`, "euclidean", "kind scale → euclidean (rule: scale default)", false},
		{"composition", types.VectorKindComposition, `{` + kindBetween + `}`, "aitchison", "kind composition → aitchison (rule: composition default)", false},
		{"measure falls back", types.VectorKindMeasure, `{` + kindBetween + `}`, "euclidean", "kind measure → euclidean (rule: measure default; std_euclidean is not offered by MAT_DISTANCE here)", false},
		{"disabled still reported", types.VectorKindBinary, `{` + kindBetween + `}`, "cosine", "kind binary → cosine (rule: binary default; hamming is not offered by MAT_DISTANCE here)", true},
		{"explicit wins", types.VectorKindComposition, `{` + kindBetween + `, "metric": "manhattan"}`, "", "", false},
		{"no kind", "", `{` + kindBetween + `}`, "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := kindReq(c.kind, c.params)
			before := string(req.Matrices[0].Params)
			env := predictFromBytes(vectorPredictSchema(t), req, &PredictOptions{DisableDefaults: c.disable, EchoRequest: true})
			if len(env.Errors) != 0 {
				t.Fatalf("errors %v", env.Errors)
			}
			if string(req.Matrices[0].Params) != before {
				t.Errorf("predict wrote the caller's params: %s", req.Matrices[0].Params)
			}
			res := env.Data.(*descriptor.PredictResult)
			var got []descriptor.DefaultApplied
			for _, d := range res.DefaultsApplied {
				if d.Category == "metric" {
					got = append(got, d)
				}
			}
			if c.want == "" {
				if len(got) != 0 {
					t.Fatalf("metric defaults %+v", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("metric defaults %+v", got)
			}
			d := got[0]
			if !slices.Equal(d.Path, []string{"Matrices", "0", "Params", "metric"}) || d.Field != "v" || d.Type != c.want || d.Reason != c.reason {
				t.Errorf("default %+v", d)
			}
			echo := env.Request.(*types.Request)
			var p map[string]any
			_ = json.Unmarshal(echo.Matrices[0].Params, &p)
			if c.disable {
				if _, set := p["metric"]; set {
					t.Errorf("DisableDefaults echo filled the metric: %s", echo.Matrices[0].Params)
				}
			} else if p["metric"] != c.want {
				t.Errorf("echoed params %s, want metric %s", echo.Matrices[0].Params, c.want)
			}
		})
	}
}

// TestPredict_MetricUnsuited: an explicit poor-fit metric is a
// PULSE_VECTOR_METRIC_UNSUITED warning (the request stays valid), an
// error under strict (the request is invalid); a fitting or defaulted
// metric raises neither. The expression static pass reports it too.
func TestPredict_MetricUnsuited(t *testing.T) {
	t.Parallel()

	formula := &types.Request{
		Vectors:    []types.VectorSpec{{Name: "v", Fields: []string{"q_1", "q_3"}, Kind: types.VectorKindScale}},
		Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "sim", Expression: "cosine(v, v)"}},
	}
	cases := []struct {
		name string
		req  *types.Request
		hits int
	}{
		{"matrix cosine on scale", kindReq(types.VectorKindScale, `{`+kindBetween+`, "metric": "cosine"}`), 1},
		{"matrix euclidean on composition", kindReq(types.VectorKindComposition, `{`+kindBetween+`, "metric": "euclidean"}`), 1},
		{"formula cosine on scale", formula, 1},
		{"matrix cosine on binary", kindReq(types.VectorKindBinary, `{`+kindBetween+`, "metric": "cosine"}`), 0},
		{"defaulted", kindReq(types.VectorKindComposition, `{`+kindBetween+`}`), 0},
	}
	count := func(entries []*descriptor.EnvelopeEntry) int {
		n := 0
		for _, e := range entries {
			if e.Code == string(errors.PULSE_VECTOR_METRIC_UNSUITED) {
				n++
			}
		}
		return n
	}
	for _, c := range cases {
		for _, strict := range []bool{false, true} {
			name := c.name
			if strict {
				name += "/strict"
			}
			t.Run(name, func(t *testing.T) {
				env := predictFromBytes(vectorPredictSchema(t), c.req, &PredictOptions{Strict: strict})
				res := env.Data.(*descriptor.PredictResult)
				warns, errs := count(env.Warnings), count(env.Errors)
				if strict {
					if errs != c.hits || warns != 0 {
						t.Fatalf("strict: %d errors %d warnings, want %d errors (%v / %v)", errs, warns, c.hits, env.Errors, env.Warnings)
					}
					if res.Valid != (c.hits == 0) {
						t.Errorf("strict valid = %v with %d hits", res.Valid, c.hits)
					}
					return
				}
				if warns != c.hits || errs != 0 {
					t.Fatalf("%d warnings %d errors, want %d warnings (%v / %v)", warns, errs, c.hits, env.Warnings, env.Errors)
				}
				if !res.Valid {
					t.Errorf("a warning invalidated the request: %v", env.Errors)
				}
			})
		}
	}
}
