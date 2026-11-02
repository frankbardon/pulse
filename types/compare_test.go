package types

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/returnplan"
)

// TestCompare_ComposedCompareFreeByteIdentity: a ComposedRequest /
// ComposedResponse without comparisons marshals and hashes exactly as
// the shape before the slot existed; a set comparison moves both.
func TestCompare_ComposedCompareFreeByteIdentity(t *testing.T) {
	t.Parallel()

	type preCompareComposedRequest struct {
		Requests []*Request `json:"requests"`
		Fits     []FitSpec  `json:"fits,omitempty"`
	}
	reg := func() *Request {
		return &Request{Cohort: &Cohort{Filename: "a.pulse"},
			Regressions: []*RegressionSpec{{Type: REG_OLS, Target: "y", Predictors: []string{"x"}}}}
	}
	reqs := []*Request{{Label: "a", Cohort: &Cohort{Filename: "a.pulse"}, Aggregations: []*Aggregation{{Type: AGG_SUM, Field: "x"}}}}
	cur := &ComposedRequest{Requests: reqs, Fits: []FitSpec{{Name: "m", Request: reg()}}}
	legacy := &preCompareComposedRequest{Requests: reqs, Fits: []FitSpec{{Name: "m", Request: reg()}}}
	got, _ := json.Marshal(cur)
	want, _ := json.Marshal(legacy)
	if string(got) != string(want) {
		t.Fatalf("wire diverged:\n got %s\nwant %s", got, want)
	}
	if h, w := cur.Hash(), CanonicalHash("composed", legacy); h != w {
		t.Fatalf("Hash diverged: got %s want %s", h, w)
	}
	cmp := *cur
	cmp.Compare = []FitComparisonSpec{{Name: "c", Restricted: "m0", Full: "m"}}
	if cmp.Hash() == cur.Hash() {
		t.Fatal("a comparison does not reach the hash")
	}
	if b, _ := json.Marshal(&cmp); !strings.Contains(string(b), `"compare":[{"name":"c","restricted":"m0","full":"m"}]`) {
		t.Fatalf("compare absent from the wire: %s", b)
	}
}

// TestCompare_ResultWire: a result writes its figures with a non-finite
// one as null and the chi-square-only keys absent; a failed comparison
// writes {name, restricted, full, test, error} only.
func TestCompare_ResultWire(t *testing.T) {
	t.Parallel()

	r := FitComparisonResult{Name: "c", Restricted: "m0", Full: "m1", Test: ComparisonTestLRT,
		Distribution: ComparisonDistributionChiSquare, Terms: []string{"x2"}, Statistic: math.NaN(), DF: 1, PValue: math.NaN(),
		NObs: 10, RestrictedDFResidual: 8, FullDFResidual: 7, RestrictedDeviance: 2, FullDeviance: 1, Dispersion: 1}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"statistic":null`, `"p_value":null`, `"distribution":"chisq"`, `"dispersion":1`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("result lacks %s: %s", want, b)
		}
	}
	for _, absent := range []string{"df_residual\":", "two_log_lik", "p_adjusted", "error"} {
		if strings.Contains(string(b), `"`+absent) {
			t.Errorf("result carries %s: %s", absent, b)
		}
	}
	r.Error = &SlotError{Code: "PULSE_COMPARISON_ROWS_DIFFER", Message: "m"}
	b, _ = json.Marshal(r)
	if string(b) != `{"name":"c","restricted":"m0","full":"m1","test":"lrt","error":{"code":"PULSE_COMPARISON_ROWS_DIFFER","message":"m"}}` {
		t.Fatalf("failed comparison %s", b)
	}
}

// TestCompare_PinnedOutsideComposeReturn: a Compose-level `return`
// selecting only the overlays at precision 1 still writes the
// comparisons, whole and unrounded — like `fits`.
func TestCompare_PinnedOutsideComposeReturn(t *testing.T) {
	t.Parallel()

	r := ComposedResponse{
		Responses: []*Response{},
		Comparisons: []FitComparisonResult{{Name: "c", Restricted: "m0", Full: "m1", Test: ComparisonTestF,
			Distribution: ComparisonDistributionF, Terms: []string{"x2"}, Statistic: 7.154923915373687, DF: 1,
			DFResidual: 56, PValue: 0.0017092765401457088, NObs: 60}},
	}
	var roots []string
	for _, f := range jsonFields(reflect.ValueOf(ComposedResponse{})) {
		roots = append(roots, f.name)
	}
	r.plan = returnplan.New("minimal", []returnplan.Path{{Segments: []returnplan.Segment{returnplan.Key("overlays")}}}, nil, nil, 1, roots)
	if r.plan.Identity() || r.plan.SelectsAll {
		t.Fatal("the plan is the identity; the test would not exercise the planned encoder")
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"statistic":7.154923915373687`, `"p_value":0.0017092765401457088`, `"df_residual":56`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("shaped wire lacks %s: %s", want, b)
		}
	}
}
