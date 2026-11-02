package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"reflect"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// ddof_test.go pins AGG_STDDEV / AGG_VARIANCE params.ddof
// (reference-validation E3) at the facade: predict ≡ runtime for every
// refusal (top-level slot, decimal field, crosstab cell and margin
// aggregation, Process and ProcessStream), ddof 1 equal to AGG_WELFORD's
// sample figures, and the result invariant across decode workers.

func ddofSlot(op types.AggregationType, field, params string) *types.Aggregation {
	a := &types.Aggregation{Type: op, Field: field, Label: "v"}
	if params != "" {
		a.Params = json.RawMessage(params)
	}
	return a
}

// TestDDOF_PredictMatchesRuntime: a ddof other than 0 / 1 is refused by
// predict exactly as the runtime refuses it (code, message, details),
// on a float and a decimal field, in a crosstab cell and margin
// aggregation, and on the streaming entry point; 0 and 1 run on both.
func TestDDOF_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	fsys := parityMalformedFS(t)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	data, err := afero.ReadFile(fsys, parityDecimalCohort)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	shapes := map[string]func(op types.AggregationType, field, params string) *Request{
		"aggregation": func(op types.AggregationType, field, params string) *Request {
			return &Request{Cohort: &types.Cohort{Filename: parityDecimalCohort},
				Aggregations: []*types.Aggregation{ddofSlot(op, field, params)}}
		},
		"crosstab cell": func(op types.AggregationType, field, params string) *Request {
			return &Request{Cohort: &types.Cohort{Filename: parityDecimalCohort},
				Crosstab: &types.CrosstabSpec{Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
					Cell: ddofSlot(op, field, params), Margins: types.CrosstabMargins{Grand: true}}}
		},
		"margin aggregation": func(op types.AggregationType, field, params string) *Request {
			return &Request{Cohort: &types.Cohort{Filename: parityDecimalCohort},
				Crosstab: &types.CrosstabSpec{Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
					Cell:               &types.Aggregation{Type: types.AGG_COUNT, Field: "age"},
					MarginAggregations: []*types.Aggregation{ddofSlot(op, field, params)},
					Margins:            types.CrosstabMargins{Grand: true}}}
		},
	}
	for shape, build := range shapes {
		for _, op := range []types.AggregationType{types.AGG_STDDEV, types.AGG_VARIANCE} {
			for _, field := range []string{"age", "amount"} {
				for _, tc := range []struct {
					params string
					refuse bool
				}{
					{"", false}, {`{"ddof":0}`, false}, {`{"ddof":1}`, false},
					{`{"ddof":2}`, true}, {`{"ddof":0.5}`, true}, {`{"ddof":"1"}`, true},
				} {
					t.Run(shape+"/"+string(op)+"/"+field+tc.params, func(t *testing.T) {
						env, err := p.PredictBytes(ctx, data, build(op, field, tc.params))
						if err != nil {
							t.Fatal(err)
						}
						_, rerr := p.Process(ctx, build(op, field, tc.params))
						if !tc.refuse {
							if len(env.Errors) != 0 || rerr != nil {
								t.Fatalf("accepted slot refused: predict %+v runtime %v", env.Errors, rerr)
							}
							return
						}
						var ce *perr.CodedError
						if !stderrors.As(rerr, &ce) || ce.Code != perr.PROCESSING_CONFIG {
							t.Fatalf("runtime err = %v, want PROCESSING_CONFIG", rerr)
						}
						if len(env.Errors) == 0 {
							t.Fatalf("predict accepted what the runtime refused (%v)", rerr)
						}
						pe := env.Errors[0]
						if string(ce.Code) != pe.Code || ce.Message != pe.Message || !reflect.DeepEqual(ce.Details, pe.Details) {
							t.Errorf("runtime %s %q %v\n predict %s %q %v", ce.Code, ce.Message, ce.Details, pe.Code, pe.Message, pe.Details)
						}
						if shape == "aggregation" {
							_, serr := p.ProcessStream(ctx, build(op, field, tc.params))
							var se *perr.CodedError
							if !stderrors.As(serr, &se) || se.Message != ce.Message {
								t.Errorf("stream err = %v, want %q", serr, ce.Message)
							}
						}
					})
				}
			}
		}
	}
}

// TestDDOF_SampleMatchesWelford: AGG_STDDEV / AGG_VARIANCE with ddof 1
// report AGG_WELFORD's sample variance (and its square root), per group,
// and the figure is bit-identical across decode-worker counts.
func TestDDOF_SampleMatchesWelford(t *testing.T) {
	t.Parallel()

	fsys := parityMalformedFS(t)
	ctx := context.Background()
	req := func() *Request {
		return &Request{Cohort: &types.Cohort{Filename: parityDecimalCohort},
			Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_VARIANCE, Field: "age", Label: "var", Params: json.RawMessage(`{"ddof":1}`)},
				{Type: types.AGG_STDDEV, Field: "age", Label: "sd", Params: json.RawMessage(`{"ddof":1}`)},
				{Type: types.AGG_VARIANCE, Field: "age", Label: "pop"},
				{Type: types.AGG_WELFORD, Field: "age", Label: "w"},
			}}
	}
	var first []map[string]any
	for _, workers := range []int{1, 4} {
		p, err := New(Options{FS: fsys, DecodeWorkers: workers})
		if err != nil {
			t.Fatal(err)
		}
		resp, err := p.Process(ctx, req())
		if err != nil {
			t.Fatal(err)
		}
		if len(resp.Data) == 0 {
			t.Fatal("no rows")
		}
		for _, row := range resp.Data {
			w := welfordOf(t, row["w"])
			v, sd, pop := row["var"].(float64), row["sd"].(float64), row["pop"].(float64)
			if math.Abs(v-w.Variance) > 1e-12*math.Abs(w.Variance) {
				t.Errorf("ddof 1 variance %v, AGG_WELFORD %v", v, w.Variance)
			}
			if math.Abs(sd-math.Sqrt(w.Variance)) > 1e-12*sd {
				t.Errorf("ddof 1 stddev %v, √welford %v", sd, math.Sqrt(w.Variance))
			}
			n := float64(w.N)
			if math.Abs(pop*n/(n-1)-v) > 1e-12*v {
				t.Errorf("population %v · n/(n−1) ≠ sample %v", pop, v)
			}
		}
		if first == nil {
			first = resp.Data
			continue
		}
		for i := range first {
			for _, k := range []string{"var", "sd"} {
				if math.Float64bits(first[i][k].(float64)) != math.Float64bits(resp.Data[i][k].(float64)) {
					t.Errorf("row %d %s differs across decode workers: %v vs %v", i, k, first[i][k], resp.Data[i][k])
				}
			}
		}
	}
}

// welfordOf reads an AGG_WELFORD cell value (its typed triple, or the
// map a JSON round trip makes of it).
func welfordOf(t *testing.T, v any) struct {
	Variance float64
	N        uint64
} {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Variance float64 `json:"variance"`
		N        uint64  `json:"n"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("welford value %v: %v", v, err)
	}
	return struct {
		Variance float64
		N        uint64
	}{out.Variance, out.N}
}
