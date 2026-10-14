package service

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// compose_vector_fits_test.go pins Compose vector fits (E2-S2): a
// `fits` entry over one MAT_* or fitted-attribute battery runs once,
// binds its closed listwise state by name, and every consuming slot —
// MAT_* and fitted attributes alike — emits from that state, never from
// its own rows.

const vecFitsCohort = "vecfits.pulse"

func vecFitsSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "week", Type: encoding.FieldTypeU32, ByteOffset: 0},
		{Name: "a", Type: encoding.FieldTypeF64, ByteOffset: 4},
		{Name: "b", Type: encoding.FieldTypeF64, ByteOffset: 12},
		{Name: "c", Type: encoding.FieldTypeF64, ByteOffset: 20},
	}}
}

func vecFitsRecords() [][]uint64 {
	out := make([][]uint64, 120)
	for i := range out {
		a := float64((i*37)%17) + 0.25*float64(i%3)
		b := 0.5*a + float64((i*11)%9) - 0.01*float64(i)
		c := -0.3*a + 0.7*b + float64((i*7)%5) + 0.002*float64(i*i)
		out[i] = []uint64{uint64(i), math.Float64bits(a), math.Float64bits(b), math.Float64bits(c)}
	}
	return out
}

func vecFitsService(t *testing.T) *Service {
	t.Helper()
	return New(setupTestFS(t, vecFitsCohort, vecFitsSchema(), vecFitsRecords()))
}

// vecWeeks is a request over weeks [lo, hi].
func vecWeeks(label, lo, hi string) *types.Request {
	return &types.Request{
		Label:     label,
		Cohort:    &types.Cohort{Filename: vecFitsCohort},
		Filterers: []*types.Filterer{{Type: types.FILTER_RANGE, Field: "week", Values: []string{lo, hi}}},
	}
}

func vecMatrix(typ types.MatrixType, name, params string) types.MatrixSpec {
	m := types.MatrixSpec{Type: typ, Name: name, Fields: []string{"a", "b", "c"}}
	if params != "" {
		m.Params = json.RawMessage(params)
	}
	return m
}

// mahalSlot scores ATTR_MAHALANOBIS over a, b, c under params and sums
// and maxes the score (both order-fixed: the same rows give the same
// bits).
func mahalSlot(label, lo, hi, params string) *types.Request {
	r := vecWeeks(label, lo, hi)
	r.Attributes = []*types.Attribute{{Type: types.ATTR_MAHALANOBIS, Label: "d", Fields: []string{"a", "b", "c"}, Params: json.RawMessage(params)}}
	r.Aggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "d", Label: "sum_d"}, {Type: types.AGG_MAX, Field: "d", Label: "max_d"}}
	return r
}

func matrixBits(t *testing.T, resp *types.Response, name string) [][]uint64 {
	t.Helper()
	for _, m := range resp.Matrices {
		if m.Name == name {
			out := make([][]uint64, len(m.Primary.Values))
			for i, row := range m.Primary.Values {
				out[i] = make([]uint64, len(row))
				for j, v := range row {
					out[i][j] = math.Float64bits(v)
				}
			}
			return out
		}
	}
	t.Fatalf("no matrix %q in %+v", name, resp.Matrices)
	return nil
}

func sameBitRows(a, b [][]uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}

func dataBits(t *testing.T, resp *types.Response, key string) uint64 {
	t.Helper()
	if len(resp.Data) != 1 {
		t.Fatalf("want one data row, got %+v", resp.Data)
	}
	v, ok := resp.Data[0][key].(float64)
	if !ok {
		t.Fatalf("data[%s] = %v", key, resp.Data[0][key])
	}
	return math.Float64bits(v)
}

// TestComposeVectorFits_OneFoldManyConsumers: one matrix vector fit
// over weeks 0–59 serves four slots over EVERY week — MAT_COVARIANCE,
// MAT_CORRELATION, MAT_PCA and ATTR_MAHALANOBIS, each with params.fit —
// and runs exactly once, serial and parallel alike. The covariance slot
// emits the fit request's own covariance to the bit (the bound state,
// not the slot's 120 rows: a fit-free twin over the same rows differs),
// and the batch's `fits` entry is the vector summary.
func TestComposeVectorFits_OneFoldManyConsumers(t *testing.T) {
	svc := vecFitsService(t)
	fitReq := vecWeeks("", "0", "59")
	fitReq.Matrices = []types.MatrixSpec{vecMatrix(types.MAT_COVARIANCE, "cov", "")}

	slot := func(label string, typ types.MatrixType, params string) *types.Request {
		r := vecWeeks(label, "0", "119")
		r.Matrices = []types.MatrixSpec{vecMatrix(typ, "m", params)}
		return r
	}
	c := &types.ComposedRequest{
		Fits: []types.FitSpec{{Name: "pop", Request: fitReq}},
		Requests: []*types.Request{
			slot("cov", types.MAT_COVARIANCE, `{"fit":"pop"}`),
			slot("corr", types.MAT_CORRELATION, `{"fit":"pop"}`),
			slot("pca", types.MAT_PCA, `{"fit":"pop","components":2}`),
			mahalSlot("mahal", "0", "119", `{"fit":"pop"}`),
			slot("own", types.MAT_COVARIANCE, ""),
		},
	}
	obs := observeFits(t)
	runs := composeBoth(t, svc, c)
	if obs.runs["pop"] != 2 { // once per run, serial + parallel
		t.Fatalf("fit runs %v, want pop once per batch", obs.runs)
	}
	standalone, err := svc.Process(context.Background(), fitReq)
	if err != nil {
		t.Fatal(err)
	}
	want := matrixBits(t, standalone, "cov")
	for name, resp := range runs {
		got := matrixBits(t, resp.Responses[0], "m")
		if !sameBitRows(got, want) {
			t.Fatalf("%s: the fit-bound covariance is not the fit request's own:\n got %v\nwant %v", name, got, want)
		}
		if sameBitRows(matrixBits(t, resp.Responses[4], "m"), want) {
			t.Fatalf("%s: the slot's own 120-row covariance equals the 60-row fit: the test cannot bite", name)
		}
		for i := 1; i < 4; i++ {
			if r := resp.Responses[i]; (i < 3 && len(r.Matrices) != 1) || (i == 3 && len(r.Data) != 1) {
				t.Fatalf("%s: slot %d emitted nothing: %+v", name, i, r)
			}
		}
		fit := resp.Fits["pop"]
		if fit == nil || fit.RegressionResult != nil || fit.Vector == nil {
			t.Fatalf("%s: fits[pop] = %+v, want the vector arm", name, fit)
		}
		v := fit.Vector
		if v.Operator != string(types.MAT_COVARIANCE) || v.N != 60 || v.Basis != "unweighted" || v.NStar != 60 ||
			len(v.Labels) != 3 || v.Labels[2] != "c" || len(v.Mean) != 3 {
			t.Fatalf("%s: summary %+v", name, v)
		}
		// The summary's covariance is the fit's own M2*/(N*−1): the
		// standalone ddof-1 covariance up to the order of arithmetic.
		for i, row := range v.Covariance.Values {
			for j, x := range row {
				w := math.Float64frombits(want[i][j])
				if math.Abs(x-w) > 1e-9*math.Max(1, math.Abs(w)) {
					t.Fatalf("%s: summary covariance[%d][%d] = %v, want %v", name, i, j, x, w)
				}
			}
		}
	}
	serial, _ := json.Marshal(runs["serial"])
	par, _ := json.Marshal(runs["parallel"])
	if string(serial) != string(par) {
		t.Fatalf("serial and parallel differ:\n%s\n%s", serial, par)
	}
}

// TestComposeVectorFits_AttributeBattery: a vector fit taken from a
// fitted attribute (ATTR_MAHALANOBIS, fit_on its rows) and bound back
// into a slot over the same rows scores every row to the bit of the fit
// request run standalone; over different rows it still scores against
// the fit (a fit_on twin over those rows differs).
func TestComposeVectorFits_AttributeBattery(t *testing.T) {
	svc := vecFitsService(t)
	fitReq := mahalSlot("", "0", "59", `{}`)
	fitReq.Label = ""
	c := &types.ComposedRequest{
		Fits: []types.FitSpec{{Name: "pop", Request: fitReq}},
		Requests: []*types.Request{
			mahalSlot("same", "0", "59", `{"fit":"pop"}`),
			mahalSlot("later", "60", "119", `{"fit":"pop"}`),
		},
	}
	obs := observeFits(t)
	runs := composeBoth(t, svc, c)
	if obs.runs["pop"] != 2 {
		t.Fatalf("fit runs %v", obs.runs)
	}
	standalone, err := svc.Process(context.Background(), fitReq)
	if err != nil {
		t.Fatal(err)
	}
	ownLater, err := svc.Process(context.Background(), mahalSlot("x", "60", "119", `{}`))
	if err != nil {
		t.Fatal(err)
	}
	for name, resp := range runs {
		for _, k := range []string{"sum_d", "max_d"} {
			if dataBits(t, resp.Responses[0], k) != dataBits(t, standalone, k) {
				t.Fatalf("%s: %s over the fit's own rows differs from the fit request's", name, k)
			}
		}
		if dataBits(t, resp.Responses[1], "sum_d") == dataBits(t, ownLater, "sum_d") {
			t.Fatalf("%s: the bound slot scored against its own rows", name)
		}
		if v := resp.Fits["pop"].Vector; v == nil || v.Operator != string(types.ATTR_MAHALANOBIS) || v.N != 60 {
			t.Fatalf("%s: summary %+v", name, v)
		}
	}
}

// TestComposeVectorFits_KindMismatch: one namespace, two kinds — a
// matrix or fitted attribute naming a regression fit, and
// ATTR_REG_PREDICT naming a vector fit, are PULSE_MODEL_KIND_MISMATCH
// (details.fit, located by details.request); an unbound name is
// PULSE_MODEL_UNKNOWN listing the vector fits only.
func TestComposeVectorFits_KindMismatch(t *testing.T) {
	svc := vecFitsService(t)
	vecFit := vecWeeks("", "0", "59")
	vecFit.Matrices = []types.MatrixSpec{vecMatrix(types.MAT_COVARIANCE, "cov", "")}
	regFit := vecWeeks("", "0", "59")
	regFit.Regressions = []*types.RegressionSpec{{Type: types.REG_OLS, Target: "c", Predictors: []string{"a", "b"}}}
	matrixSlot := func(fit string) *types.Request {
		r := vecWeeks("s", "0", "119")
		r.Matrices = []types.MatrixSpec{vecMatrix(types.MAT_CORRELATION, "m", `{"fit":"`+fit+`"}`)}
		return r
	}
	predictSlot := vecWeeks("s", "0", "119")
	predictSlot.Attributes = []*types.Attribute{{Type: types.ATTR_REG_PREDICT, Label: "yhat", Params: json.RawMessage(`{"fit":"pop"}`)}}
	predictSlot.Aggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "yhat", Label: "s"}}
	cases := []struct {
		name     string
		slot     *types.Request
		code     perr.Code
		kind     string
		expected string
	}{
		{"matrix names a regression", matrixSlot("reg"), perr.PULSE_MODEL_KIND_MISMATCH, "regression", "vector"},
		{"attribute names a regression", mahalSlot("s", "0", "119", `{"fit":"reg"}`), perr.PULSE_MODEL_KIND_MISMATCH, "regression", "vector"},
		{"regression score names a vector fit", predictSlot, perr.PULSE_MODEL_KIND_MISMATCH, "vector", "regression"},
		{"unbound", matrixSlot("nope"), perr.PULSE_MODEL_UNKNOWN, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &types.ComposedRequest{
				Fits:     []types.FitSpec{{Name: "pop", Request: vecFit}, {Name: "reg", Request: regFit}},
				Requests: []*types.Request{tc.slot},
			}
			_, err := svc.Compose(context.Background(), c)
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Code != tc.code {
				t.Fatalf("err = %v, want %s", err, tc.code)
			}
			if ce.Details["request"] != 0 {
				t.Errorf("not located: %v", ce.Details)
			}
			if tc.kind != "" && (ce.Details["kind"] != tc.kind || ce.Details["expected"] != tc.expected || ce.Details["fit"] == nil) {
				t.Errorf("details %v", ce.Details)
			}
			if tc.code == perr.PULSE_MODEL_UNKNOWN {
				if av, _ := ce.Details["available"].([]string); len(av) != 1 || av[0] != "pop" {
					t.Errorf("available %v, want the vector fits only", ce.Details["available"])
				}
			}
		})
	}
}
