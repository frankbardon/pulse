package pulse

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"reflect"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// compose_vector_fits_test.go pins Compose vector fits at the facade:
// predict refuses exactly what the runtime refuses (every new
// PULSE_FIT_INVALID reason, the matrix params.fit rules, the kind
// mismatch and an unbound name), lists a vector fit statically, and the
// wire carries a vector fit as {"vector": …} beside a regression fit
// whose entry is byte-identical to the regression-only shape.

const vecFacadeCohort = "vecfit_facade.pulse"

func vecFacadeFS(t *testing.T) afero.Fs {
	t.Helper()
	fsys := afero.NewMemMapFs()
	var rows [][]string
	for i := 0; i < 90; i++ {
		a := float64((i*37)%17) + 0.25*float64(i%3)
		b := 0.5*a + float64((i*11)%9)
		c := -0.3*a + 0.7*b + float64((i*7)%5)
		rows = append(rows, []string{strconv.Itoa(i), jsonNumber(a), jsonNumber(b), jsonNumber(c), []string{"north", "south"}[i%2]})
	}
	createTestPulseFile(t, fsys, vecFacadeCohort, []string{"week", "a", "b", "c", "region"}, rows)
	return fsys
}

func vecFacadeRequest(label string) *Request {
	return &Request{Label: label, Cohort: &types.Cohort{Filename: vecFacadeCohort}}
}

func vecFacadeMatrix(typ types.MatrixType, params string) *Request {
	r := vecFacadeRequest("m")
	r.Matrices = []types.MatrixSpec{{Type: typ, Name: "m", Fields: []string{"a", "b", "c"}}}
	if params != "" {
		r.Matrices[0].Params = json.RawMessage(params)
	}
	return r
}

func vecFacadeRegression() *Request {
	r := vecFacadeRequest("")
	r.Regressions = []*types.RegressionSpec{{Type: types.REG_OLS, Name: "r", Target: "c", Predictors: []string{"a", "b"}}}
	return r
}

// TestComposeVectorFits_PredictRuntimeParity: every vector-fit refusal
// PredictCompose reports is exactly the error Compose and
// ComposeParallel return — code, message and details.
func TestComposeVectorFits_PredictRuntimeParity(t *testing.T) {
	p, err := New(Options{FS: vecFacadeFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	mixed := vecFacadeRegression()
	mixed.Matrices = vecFacadeMatrix(types.MAT_COVARIANCE, "").Matrices
	grouped := vecFacadeMatrix(types.MAT_COVARIANCE, "")
	grouped.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
	groupedSlot := vecFacadeMatrix(types.MAT_COVARIANCE, `{"fit":"pop"}`)
	groupedSlot.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
	predictSlot := vecFacadeRequest("s")
	predictSlot.Attributes = []*types.Attribute{{Type: types.ATTR_REG_PREDICT, Label: "yhat", Params: json.RawMessage(`{"fit":"pop"}`)}}
	predictSlot.Aggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "yhat", Label: "s"}}
	mahal := vecFacadeRequest("s")
	mahal.Attributes = []*types.Attribute{{Type: types.ATTR_MAHALANOBIS, Label: "d", Fields: []string{"a", "b", "c"}, Params: json.RawMessage(`{"fit":"reg"}`)}}
	mahal.Aggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "d", Label: "s"}}
	pop := types.FitSpec{Name: "pop", Request: vecFacadeMatrix(types.MAT_COVARIANCE, "")}
	reg := types.FitSpec{Name: "reg", Request: vecFacadeRegression()}
	cases := []struct {
		name   string
		fits   []types.FitSpec
		slot   *Request
		code   perr.Code
		reason string
	}{
		{"kind_ambiguous", []types.FitSpec{{Name: "v", Request: mixed}}, nil, perr.PULSE_FIT_INVALID, "kind_ambiguous"},
		{"vector grouped", []types.FitSpec{{Name: "v", Request: grouped}}, nil, perr.PULSE_FIT_INVALID, "grouped"},
		{"vector_source", []types.FitSpec{{Name: "v", Request: vecFacadeMatrix(types.MAT_CORRELATION, `{"missing":"pairwise"}`)}}, nil, perr.PULSE_FIT_INVALID, "vector_source"},
		{"vector save model suffix", []types.FitSpec{{Name: "v", Request: vecFacadeMatrix(types.MAT_COVARIANCE, ""), Save: "v.pulse-model.json"}}, nil, perr.PULSE_MODEL_SAVE_INVALID, "suffix"},
		{"matrix fit unknown", []types.FitSpec{pop}, vecFacadeMatrix(types.MAT_CORRELATION, `{"fit":"nope"}`), perr.PULSE_MODEL_UNKNOWN, ""},
		{"matrix fit names a regression", []types.FitSpec{pop, reg}, vecFacadeMatrix(types.MAT_PCA, `{"fit":"reg"}`), perr.PULSE_MODEL_KIND_MISMATCH, ""},
		{"attribute fit names a regression", []types.FitSpec{pop, reg}, mahal, perr.PULSE_MODEL_KIND_MISMATCH, ""},
		{"regression score names a vector fit", []types.FitSpec{pop}, predictSlot, perr.PULSE_MODEL_KIND_MISMATCH, ""},
		{"fit_and_missing", []types.FitSpec{pop}, vecFacadeMatrix(types.MAT_COVARIANCE, `{"fit":"pop","missing":"pairwise"}`), perr.SERVICE_VALIDATION, "fit_and_missing"},
		{"fit_unsupported rank", []types.FitSpec{pop}, vecFacadeMatrix(types.MAT_CORRELATION, `{"fit":"pop","method":"spearman"}`), perr.SERVICE_VALIDATION, "fit_unsupported"},
		{"fit_grouped", []types.FitSpec{pop}, groupedSlot, perr.SERVICE_VALIDATION, "fit_grouped"},
	}
	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			slot := tc.slot
			if slot == nil {
				slot = vecFacadeMatrix(types.MAT_COVARIANCE, "")
			}
			c := &ComposedRequest{Fits: tc.fits, Requests: []*Request{slot}}
			env, err := p.PredictCompose(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) != 1 {
				t.Fatalf("predict errors = %+v, want exactly one", env.Errors)
			}
			pe := env.Errors[0]
			if perr.Code(pe.Code) != tc.code || (tc.reason != "" && pe.Details["reason"] != tc.reason) {
				t.Fatalf("predict %s %v, want %s %s", pe.Code, pe.Details, tc.code, tc.reason)
			}
			for name, run := range map[string]func() error{
				"Compose": func() error { _, err := p.Compose(ctx, c); return err },
				"ComposeParallel": func() error {
					_, err := p.ComposeParallel(ctx, c, ComposeOptions{MaxWorkers: 2, FailFast: true})
					return err
				},
			} {
				var ce *perr.CodedError
				if err := run(); !stderrors.As(err, &ce) {
					t.Fatalf("%s: uncoded %v", name, err)
				}
				// The wire form is what predict reports (an int index
				// reads back as a number on both).
				got, _ := json.Marshal(ce.Details)
				want, _ := json.Marshal(pe.Details)
				if string(ce.Code) != pe.Code || ce.Message != pe.Message || !bytes.Equal(got, want) {
					t.Errorf("%s: runtime %s %q %s\n predict %s %q %s", name, ce.Code, ce.Message, got, pe.Code, pe.Message, want)
				}
			}
		})
	}
}

// TestComposeVectorFits_PredictListsAndWire: a valid batch with a
// regression and a vector fit predicts valid, listing the vector fit's
// battery and resolved labels statically; the runtime's `fits` carries
// the regression entry exactly as the regression-only shape and the
// vector entry under its `vector` key.
func TestComposeVectorFits_PredictListsAndWire(t *testing.T) {
	p, err := New(Options{FS: vecFacadeFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	attrFit := vecFacadeRequest("")
	attrFit.Vectors = []types.VectorSpec{{Name: "battery", Fields: []string{"a", "b", "c"}, Labels: []string{"A", "B", "C"}}}
	attrFit.Attributes = []*types.Attribute{{Type: types.ATTR_MAHALANOBIS, Label: "d", Vector: "battery"}}
	c := &ComposedRequest{
		Fits: []types.FitSpec{
			{Name: "reg", Request: vecFacadeRegression()},
			{Name: "pop", Request: vecFacadeMatrix(types.MAT_COVARIANCE, "")},
			{Name: "attr", Request: attrFit},
		},
		Requests: []*Request{vecFacadeMatrix(types.MAT_CORRELATION, `{"fit":"pop"}`)},
	}
	env, err := p.PredictCompose(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) > 0 {
		t.Fatalf("errors: %+v", env.Errors)
	}
	res := env.Data.(*ComposePredictResult)
	want := []descriptor.FitSummary{
		{Name: "reg", Type: types.REG_OLS, Target: "c", Predictors: []string{"a", "b"}, Cohort: vecFacadeCohort},
		{Name: "pop", Cohort: vecFacadeCohort, Vector: &descriptor.VectorFitSummary{Operator: "MAT_COVARIANCE", Labels: []string{"a", "b", "c"}, Members: []string{"a", "b", "c"}}},
		{Name: "attr", Cohort: vecFacadeCohort, Vector: &descriptor.VectorFitSummary{Operator: "ATTR_MAHALANOBIS", Labels: []string{"A", "B", "C"}, Members: []string{"a", "b", "c"}}},
	}
	if !res.Valid || !reflect.DeepEqual(res.Fits, want) {
		got, _ := json.Marshal(res.Fits)
		t.Fatalf("valid=%v fits=%s", res.Valid, got)
	}

	out, err := p.Compose(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	// The runtime binds the attribute fit under the vector's labels —
	// what predict listed.
	if v := out.Fits["attr"].Vector; v == nil || !reflect.DeepEqual(v.Labels, []string{"A", "B", "C"}) || !reflect.DeepEqual(v.Members, []string{"a", "b", "c"}) {
		t.Fatalf("attr fit %+v", out.Fits["attr"])
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Fits map[string]json.RawMessage `json:"fits"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	regOnly, err := json.Marshal(out.Fits["reg"].RegressionResult)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(wire.Fits["reg"], regOnly) || bytes.Contains(wire.Fits["reg"], []byte(`"vector"`)) {
		t.Fatalf("regression entry is not the regression-only shape:\n%s\n%s", wire.Fits["reg"], regOnly)
	}
	if !bytes.HasPrefix(wire.Fits["pop"], []byte(`{"vector":{"operator":"MAT_COVARIANCE"`)) {
		t.Fatalf("vector entry %s", wire.Fits["pop"])
	}
	// Read back, each entry keeps its arm.
	var back ComposedResponse
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Fits["reg"].RegressionResult == nil || back.Fits["reg"].Vector != nil || back.Fits["pop"].Vector == nil || back.Fits["pop"].RegressionResult != nil {
		t.Fatalf("read back: reg %+v pop %+v", back.Fits["reg"], back.Fits["pop"])
	}
}

// TestComposeVectorFits_EveryMatrixEmitsFromFit: every co-moment MAT_*
// operator — the built-ins and an extension (MAT_ACME_COV) — with
// params.fit emits, bit for bit, the result the same operator gives
// over the fit request's own rows (one frequency-weighted region of a
// three-block cohort with scattered nulls), though the slot itself
// reads every row unweighted: the bound state, its basis included, is
// all it reads. Serial decode and DecodeWorkers 4 agree.
func TestComposeVectorFits_EveryMatrixEmitsFromFit(t *testing.T) {
	fsys := extMatrixCohort(t)
	north := []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{"north"}}}
	fitReq := extMatrixRequest(types.MAT_COVARIANCE, "", false, true)
	fitReq.Filterers = north
	cases := []struct {
		typ    types.MatrixType
		params string
	}{
		{types.MAT_COVARIANCE, `{"ddof":0}`},
		{types.MAT_CORRELATION, `{"inference":true,"summary":{"top_pairs":2}}`},
		{types.MAT_PARTIAL_CORRELATION, `{"inference":true}`},
		{types.MAT_RELIABILITY, ""},
		{types.MAT_PCA, `{"components":2}`},
		{types.MAT_FACTOR, `{"factors":1}`},
		{types.MAT_COLLINEARITY, ""},
		{"MAT_ACME_COV", ""},
	}
	withFit := func(params string) string {
		if params == "" {
			return `{"fit":"pop"}`
		}
		return `{"fit":"pop",` + params[1:]
	}
	for _, workers := range []int{1, 4} {
		p := extMatrixPulse(t, fsys, Options{DecodeWorkers: workers}, extCovReg(true, true, nil))
		for _, tc := range cases {
			t.Run(string(tc.typ)+"/workers"+strconv.Itoa(workers), func(t *testing.T) {
				own := extMatrixRequest(tc.typ, tc.params, false, true)
				own.Filterers = north
				want, err := p.Process(context.Background(), own)
				if err != nil {
					t.Fatal(err)
				}
				slot := extMatrixRequest(tc.typ, withFit(tc.params), false, false)
				out, err := p.Compose(context.Background(), &ComposedRequest{
					Fits:     []types.FitSpec{{Name: "pop", Request: fitReq}},
					Requests: []*Request{slot, extMatrixRequest(tc.typ, tc.params, false, false)},
				})
				if err != nil {
					t.Fatal(err)
				}
				sameMatrixBits(t, string(tc.typ), out.Responses[0].Matrices, want.Matrices)
				if reflect.DeepEqual(out.Responses[1].Matrices[0].Primary.Values, want.Matrices[0].Primary.Values) {
					t.Fatal("the slot's own rows give the fit's result: the test cannot bite")
				}
				// The components floor rests on the bound state: its n and
				// its basis's weight keys, no row dropped.
				mc := out.Responses[0].Components.Matrices[0]
				wc := want.Components.Matrices[0]
				if mc.N != wc.N || mc.NListwiseDropped != 0 || mc.SumWeights == nil || *mc.SumWeights != *wc.SumWeights {
					t.Fatalf("components %+v, want n %d sum_weights %v", mc, wc.N, *wc.SumWeights)
				}
			})
		}
	}
}
