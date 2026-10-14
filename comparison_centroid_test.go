package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"testing"

	"github.com/spf13/afero"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// comparison_centroid_test.go pins the centroid and population
// references of ATTR_PROFILE_SIMILARITY / ATTR_VEC_DISTANCE (U30 E3-S1)
// at the facade: predict refuses exactly as the runtime, predicts the
// two-pass routing (streamable, crosstab fusion) the runtime takes, and
// a centroid scores bit-identically on the streaming two-pass drive and
// the buffered arm; a population binds from a loaded file and from an
// in-batch Compose vector fit.

// vecFacadeRow is row i of vecFacadeFS: a, b, c.
func vecFacadeRow(i int) []float64 {
	a := float64((i*37)%17) + 0.25*float64(i%3)
	b := 0.5*a + float64((i*11)%9)
	c := -0.3*a + 0.7*b + float64((i*7)%5)
	return []float64{a, b, c}
}

func cosineOracle(x, r []float64) float64 {
	var d, nx, nr float64
	for i := range x {
		d += x[i] * r[i]
		nx += x[i] * x[i]
		nr += r[i] * r[i]
	}
	return d / math.Sqrt(nx*nr)
}

func centroidScoring(typ types.AttributeType, params string) *Request {
	r := vecFacadeRequest("score")
	r.Attributes = []*types.Attribute{{Type: typ, Label: "s", Fields: []string{"a", "b", "c"}, Params: json.RawMessage(params)}}
	r.Aggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "s", Label: "sum_s"}, {Type: types.AGG_COUNT, Field: "s", Label: "n_s"}}
	return r
}

// TestComparisonCentroid_PredictRuntimeParity: every centroid /
// population refusal predict reports is the error Process returns —
// code, message and details.
func TestComparisonCentroid_PredictRuntimeParity(t *testing.T) {
	fsys := populationFacadeFS(t)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cohort, err := afero.ReadFile(fsys, vecFacadeCohort)
	if err != nil {
		t.Fatal(err)
	}
	withPop := func(r *Request) *Request {
		r.Populations = []types.PopulationRef{popRef("brand", facadePopulation)}
		return r
	}
	twoMembers := withPop(centroidScoring(types.ATTR_PROFILE_SIMILARITY, `{"metric":"cosine","reference":{"population":"brand"}}`))
	twoMembers.Attributes[0].Fields = []string{"a", "b"}
	asModel := centroidScoring(types.ATTR_PROFILE_SIMILARITY, `{"metric":"cosine","reference":{"population":"brand"}}`)
	writeFacadeModel(t, fsys, "pops/brand"+model.FileSuffix, "a")
	asModel.Models = []types.ModelRef{{Name: "brand", Path: "pops/brand" + model.FileSuffix}}
	for _, tc := range []struct {
		name string
		req  *Request
		code perr.Code
	}{
		{"by unknown", centroidScoring(types.ATTR_VEC_DISTANCE, `{"metric":"euclidean","reference":{"centroid":{"by":"nope"}}}`), perr.SERVICE_VALIDATION},
		{"by float", centroidScoring(types.ATTR_VEC_DISTANCE, `{"metric":"euclidean","reference":{"centroid":{"by":"a"}}}`), perr.SERVICE_VALIDATION},
		{"centroid filter unknown field", centroidScoring(types.ATTR_PROFILE_SIMILARITY, `{"metric":"cosine","reference":{"centroid":{"filter":[{"type":"FILTER_INCLUDE","field":"nope","values":["x"]}]}}}`), perr.SERVICE_VALIDATION},
		{"population unbound", centroidScoring(types.ATTR_PROFILE_SIMILARITY, `{"metric":"cosine","reference":{"population":"brand"}}`), perr.PULSE_VECTOR_REFERENCE_UNKNOWN},
		{"population labels differ", twoMembers, perr.PULSE_VECTOR_REFERENCE_MISMATCH},
		{"population names a regression", asModel, perr.PULSE_MODEL_KIND_MISMATCH},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, err := p.PredictBytes(ctx, cohort, tc.req)
			if err != nil {
				t.Fatal(err)
			}
			pe := findEnvError(t, env, tc.code)
			_, rerr := p.Process(ctx, tc.req)
			if tc.code == perr.SERVICE_VALIDATION {
				// The field-reference walk's message wording is predict's
				// own; the code and reason are the shared rule's.
				var ce *perr.CodedError
				if !asCoded(rerr, &ce) || ce.Code != tc.code {
					t.Fatalf("Process: %v, want %s", rerr, tc.code)
				}
				return
			}
			assertSameRefusal(t, "Process", rerr, pe)
		})
	}
	// A loaded population binds: predict clean, Process scores every
	// row against its stored mean.
	good := withPop(centroidScoring(types.ATTR_PROFILE_SIMILARITY, `{"metric":"cosine","reference":{"population":"brand"}}`))
	env, err := p.PredictBytes(ctx, cohort, good)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("predict errors %+v", env.Errors)
	}
	if _, err := p.Process(ctx, good); err != nil {
		t.Fatal(err)
	}
}

// TestComparisonCentroid_PredictRouting: predict reports the routing the
// runtime takes — a centroid is two-pass (ungrouped streamable through
// the two-pass drive, grouped buffered, a crosstab over it declines
// fusion) for a row-local metric too, while a population over a
// row-local metric is row-local (grouped streamable, fusable). Each
// request also runs.
func TestComparisonCentroid_PredictRouting(t *testing.T) {
	fsys := populationFacadeFS(t)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pops := []types.PopulationRef{popRef("brand", facadePopulation)}
	for _, c := range []struct {
		name, params   string
		streamUngroup  bool
		streamGrouped  bool
		crosstabFusion bool
	}{
		{"centroid", `{"metric":"cosine","reference":{"centroid":{}}}`, true, false, false},
		{"centroid by", `{"metric":"cosine","reference":{"centroid":{"by":"region"}}}`, true, false, false},
		{"population", `{"metric":"cosine","reference":{"population":"brand"}}`, true, true, true},
		{"literal", `{"metric":"cosine","reference":[1,2,3]}`, true, true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			check := func(what string, r *Request, want bool, fuse bool) {
				r.Populations = pops
				pr, err := p.Predict(ctx, r)
				if err != nil || !pr.Valid {
					t.Fatalf("%s: predict %v %+v", what, err, pr)
				}
				if fuse {
					if pr.CrosstabFusable == nil || *pr.CrosstabFusable != want {
						t.Errorf("%s: crosstab_fusable %v, want %v (%v)", what, pr.CrosstabFusable, want, pr.CrosstabFusionReasons)
					}
				} else if pr.Streamable != want {
					t.Errorf("%s: streamable %v, want %v", what, pr.Streamable, want)
				}
				if _, err := p.Process(ctx, r); err != nil {
					t.Fatalf("%s: Process: %v", what, err)
				}
			}
			check("ungrouped", centroidScoring(types.ATTR_PROFILE_SIMILARITY, c.params), c.streamUngroup, false)
			grouped := centroidScoring(types.ATTR_PROFILE_SIMILARITY, c.params)
			grouped.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
			check("grouped", grouped, c.streamGrouped, false)
			ct := centroidScoring(types.ATTR_PROFILE_SIMILARITY, c.params)
			ct.Aggregations = nil
			ct.Crosstab = &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Columns: []*types.Group{{Type: types.GROUP_RANGE, Field: "week", Interval: 30}},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "s"},
			}
			check("crosstab", ct, c.crosstabFusion, true)
		})
	}
}

// TestComparisonCentroid_StreamingEqualsBuffered: a centroid slot (all
// rows, by region, weighted) gives the same bits on the streaming
// two-pass drive and the buffered arm (an ATTR_PERCENTILE forces it),
// and its centroid is AGG_VEC_MEAN's over the same rows.
func TestComparisonCentroid_StreamingEqualsBuffered(t *testing.T) {
	fsys := vecFacadeFS(t)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// The oracle centroid of every row (no nulls in the facade).
	mean := make([]float64, 3)
	for i := 0; i < 90; i++ {
		for j, v := range vecFacadeRow(i) {
			mean[j] += v
		}
	}
	for j := range mean {
		mean[j] /= 90
	}
	for _, params := range []string{
		`{"metric":"cosine","reference":{"centroid":{}}}`,
		`{"metric":"cosine","reference":{"centroid":{"by":"region","filter":[{"type":"FILTER_RANGE","field":"week","values":["0","59"]}]}}}`,
	} {
		run := func(buffered bool, weighted bool) *Response {
			r := centroidScoring(types.ATTR_PROFILE_SIMILARITY, params)
			if weighted {
				r.Weight = &types.WeightSpec{Field: "week", Kind: types.WeightKindFrequency}
			}
			if buffered {
				r.Attributes = append(r.Attributes, &types.Attribute{Type: types.ATTR_PERCENTILE, Field: "a", Label: "pct", Weight: types.NullSlotWeight()})
			}
			resp, err := p.Process(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			return resp
		}
		for _, weighted := range []bool{false, true} {
			s, b := run(false, weighted), run(true, weighted)
			sv, bv := s.Data[0]["sum_s"].(float64), b.Data[0]["sum_s"].(float64)
			if math.Float64bits(sv) != math.Float64bits(bv) {
				t.Errorf("%s weighted=%v: streaming %v, buffered %v", params, weighted, sv, bv)
			}
			if params == `{"metric":"cosine","reference":{"centroid":{}}}` && !weighted {
				want := 0.0
				for i := 0; i < 90; i++ {
					want += cosineOracle(vecFacadeRow(i), mean)
				}
				if math.Abs(sv-want) > 1e-9*math.Abs(want) {
					t.Errorf("sum_s %v, oracle %v", sv, want)
				}
			}
		}
	}
}

// TestComparisonPopulation_ComposeFit: a {population} reference reads an
// in-batch Compose vector fit by name — the fit's mean, so the scores
// are the cosine to it.
func TestComparisonPopulation_ComposeFit(t *testing.T) {
	p, err := New(Options{FS: vecFacadeFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	c := &ComposedRequest{
		Fits:     []types.FitSpec{{Name: "brand", Request: vecFacadeMatrix(types.MAT_COVARIANCE, "")}},
		Requests: []*Request{centroidScoring(types.ATTR_PROFILE_SIMILARITY, `{"metric":"cosine","reference":{"population":"brand"}}`)},
	}
	env, err := p.PredictCompose(ctx, c)
	if err != nil || len(env.Errors) != 0 {
		t.Fatalf("predict %v %+v", err, env)
	}
	out, err := p.Compose(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	mean := out.Fits["brand"].Vector.Mean
	want := 0.0
	for i := 0; i < 90; i++ {
		want += cosineOracle(vecFacadeRow(i), mean)
	}
	got := out.Responses[0].Data[0]["sum_s"].(float64)
	if math.Abs(got-want) > 1e-9*math.Abs(want) {
		t.Errorf("sum_s %v, oracle %v", got, want)
	}
	bad := &ComposedRequest{Fits: c.Fits, Requests: []*Request{centroidScoring(types.ATTR_PROFILE_SIMILARITY, `{"metric":"cosine","reference":{"population":"nope"}}`)}}
	env, err = p.PredictCompose(ctx, bad)
	if err != nil {
		t.Fatal(err)
	}
	pe := findEnvError(t, env, perr.PULSE_VECTOR_REFERENCE_UNKNOWN)
	if _, rerr := p.Compose(ctx, bad); rerr == nil {
		t.Fatal("Compose ran an unbound population")
	} else {
		var ce *perr.CodedError
		if !asCoded(rerr, &ce) || string(ce.Code) != pe.code {
			t.Errorf("Compose %v, predict %+v", rerr, pe)
		}
	}
}

// asCoded is errors.As onto a *perr.CodedError.
func asCoded(err error, ce **perr.CodedError) bool { return stderrors.As(err, ce) }
