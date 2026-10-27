package model

import (
	"bytes"
	"context"
	stderrors "errors"
	"go/parser"
	"go/token"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func coded(t *testing.T, err error) *errors.CodedError {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("want a coded error, got %v", err)
	}
	return ce
}

func olsReq(regs ...*types.RegressionSpec) *types.Request {
	return &types.Request{Cohort: &types.Cohort{Filename: "c.pulse"}, Regressions: regs}
}

func ols() *types.RegressionSpec {
	return &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}}
}

func TestCheckFits(t *testing.T) {
	t.Parallel()

	grouped := olsReq(ols())
	grouped.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}
	crosstab := olsReq(ols())
	crosstab.Crosstab = &types.CrosstabSpec{}
	mat := func(typ types.MatrixType, params string) types.MatrixSpec {
		m := types.MatrixSpec{Type: typ, Fields: []string{"a", "b"}}
		if params != "" {
			m.Params = []byte(params)
		}
		return m
	}
	vec := func(ms ...types.MatrixSpec) *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: "c.pulse"}, Matrices: ms}
	}
	mahal := &types.Attribute{Type: types.ATTR_MAHALANOBIS, Fields: []string{"a", "b"}}
	attrFit := &types.Request{Cohort: &types.Cohort{Filename: "c.pulse"}, Attributes: []*types.Attribute{mahal}}
	mixed := olsReq(ols())
	mixed.Matrices = []types.MatrixSpec{mat(types.MAT_COVARIANCE, "")}
	twoVec := vec(mat(types.MAT_COVARIANCE, ""))
	twoVec.Attributes = []*types.Attribute{mahal}
	groupedVec := vec(mat(types.MAT_COVARIANCE, ""))
	groupedVec.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}
	cases := []struct {
		name    string
		fits    []types.FitSpec
		code    errors.Code
		reason  string
		fit     int
		details map[string]any
	}{
		{"empty list", nil, "", "", 0, nil},
		{"valid pair", []types.FitSpec{{Name: "a", Request: olsReq(ols())}, {Name: "b", Request: olsReq(ols())}}, "", "", 0, nil},
		{"no name", []types.FitSpec{{Request: olsReq(ols())}}, errors.PULSE_FIT_INVALID, ReasonNameMissing, 0, nil},
		{"no request", []types.FitSpec{{Name: "a"}}, errors.PULSE_FIT_INVALID, ReasonRequestMissing, 0, nil},
		{"zero regressions", []types.FitSpec{{Name: "a", Request: olsReq()}}, errors.PULSE_FIT_INVALID, ReasonRegressionCount, 0, map[string]any{"count": 0}},
		{"two regressions", []types.FitSpec{{Name: "a", Request: olsReq(ols())}, {Name: "b", Request: olsReq(ols(), ols())}}, errors.PULSE_FIT_INVALID, ReasonRegressionCount, 1, map[string]any{"count": 2}},
		{"null regression", []types.FitSpec{{Name: "a", Request: olsReq(nil)}}, errors.PULSE_FIT_INVALID, ReasonRegressionCount, 0, nil},
		{"grouped", []types.FitSpec{{Name: "a", Request: grouped}}, errors.PULSE_FIT_INVALID, ReasonGrouped, 0, nil},
		{"crosstab", []types.FitSpec{{Name: "a", Request: crosstab}}, errors.PULSE_FIT_INVALID, ReasonGrouped, 0, nil},
		{"duplicate", []types.FitSpec{{Name: "a", Request: olsReq(ols())}, {Name: "b", Request: olsReq(ols())}, {Name: "a", Request: olsReq(ols())}}, errors.PULSE_MODEL_NAME_COLLISION, "", 2, map[string]any{"name": "a"}},
		// Vector fits: one MAT_* or fitted attribute battery, no
		// regression.
		{"vector matrix", []types.FitSpec{{Name: "v", Request: vec(mat(types.MAT_COVARIANCE, `{"ddof":0}`))}}, "", "", 0, nil},
		{"vector attribute", []types.FitSpec{{Name: "v", Request: attrFit}, {Name: "r", Request: olsReq(ols())}}, "", "", 0, nil},
		{"kind ambiguous", []types.FitSpec{{Name: "v", Request: mixed}}, errors.PULSE_FIT_INVALID, ReasonKindAmbiguous, 0, map[string]any{"regressions": 1, "vectors": 1}},
		{"two batteries", []types.FitSpec{{Name: "v", Request: twoVec}}, errors.PULSE_FIT_INVALID, ReasonVectorCount, 0, map[string]any{"count": 2}},
		{"two matrices", []types.FitSpec{{Name: "v", Request: vec(mat(types.MAT_COVARIANCE, ""), mat(types.MAT_CORRELATION, ""))}}, errors.PULSE_FIT_INVALID, ReasonVectorCount, 0, nil},
		{"vector grouped", []types.FitSpec{{Name: "v", Request: groupedVec}}, errors.PULSE_FIT_INVALID, ReasonGrouped, 0, nil},
		{"vector pairwise", []types.FitSpec{{Name: "v", Request: vec(mat(types.MAT_CORRELATION, `{"missing":"pairwise"}`))}}, errors.PULSE_FIT_INVALID, ReasonVectorSource, 0, map[string]any{"param": "missing"}},
		{"vector distance", []types.FitSpec{{Name: "v", Request: vec(mat(types.MAT_DISTANCE, `{"between":{"type":"GROUP_CATEGORY","field":"g"}}`))}}, errors.PULSE_FIT_INVALID, ReasonVectorSource, 0, map[string]any{"type": "MAT_DISTANCE"}},
		{"vector reversed", []types.FitSpec{{Name: "v", Request: vec(mat(types.MAT_RELIABILITY, `{"reverse":["b"],"scale_min":1,"scale_max":5}`))}}, errors.PULSE_FIT_INVALID, ReasonVectorSource, 0, map[string]any{"param": "reverse"}},
		{"vector scale only", []types.FitSpec{{Name: "v", Request: vec(mat(types.MAT_RELIABILITY, `{"scale_min":1,"scale_max":5}`))}}, "", "", 0, nil},
		// U59 E4-S4: an ordinal MAT_SEM battery binds a SEM fit (EBM
		// scores) and may save one, as an ML one does.
		{"sem ordinal", []types.FitSpec{{Name: "s", Request: vec(mat(types.MAT_SEM, `{"model":"f =~ a + b","estimator":"wlsmv","ordered":["a","b"]}`))}}, "", "", 0, nil},
		{"sem dwls save", []types.FitSpec{{Name: "s", Request: vec(mat(types.MAT_SEM, `{"model":"f =~ a + b","estimator":"dwls","ordered":"a"}`)), Save: "s.pulse-sem.json"}}, "", "", 0, nil},
		{"sem ml", []types.FitSpec{{Name: "s", Request: vec(mat(types.MAT_SEM, `{"model":"f =~ a + b","estimator":"ml"}`))}}, "", "", 0, nil},
		// A vector fit saves a population file; a model-file path (or a
		// regression fit's population path) is the wrong suffix.
		{"vector save", []types.FitSpec{{Name: "v", Request: attrFit, Save: "pops/v.pulse-population.json"}}, "", "", 0, nil},
		{"vector save model suffix", []types.FitSpec{{Name: "v", Request: attrFit, Save: "v.pulse-model.json"}}, errors.PULSE_MODEL_SAVE_INVALID, ReasonSaveSuffix, 0, map[string]any{"name": "v", "path": "v.pulse-model.json"}},
		{"vector save escapes", []types.FitSpec{{Name: "v", Request: attrFit, Save: "../v.pulse-population.json"}}, errors.PULSE_MODEL_SAVE_INVALID, ReasonSaveEscapesRoot, 0, nil},
		{"regression save population suffix", []types.FitSpec{{Name: "r", Request: olsReq(ols()), Save: "r.pulse-population.json"}}, errors.PULSE_MODEL_SAVE_INVALID, ReasonSaveSuffix, 0, nil},
		{"vector name collides with a regression", []types.FitSpec{{Name: "a", Request: olsReq(ols())}, {Name: "a", Request: attrFit}}, errors.PULSE_MODEL_NAME_COLLISION, "", 1, map[string]any{"name": "a"}},
		// The first fault in entry order wins.
		{"first fault wins", []types.FitSpec{{Name: "a", Request: olsReq()}, {Name: ""}}, errors.PULSE_FIT_INVALID, ReasonRegressionCount, 0, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckFits(tc.fits)
			if tc.code == "" {
				if err != nil {
					t.Fatalf("unexpected refusal: %v", err)
				}
				return
			}
			ce := coded(t, err)
			if ce.Code != tc.code {
				t.Fatalf("code = %s, want %s", ce.Code, tc.code)
			}
			if ce.Details["fit"] != tc.fit {
				t.Errorf("details.fit = %v, want %d", ce.Details["fit"], tc.fit)
			}
			if tc.reason != "" && ce.Details["reason"] != tc.reason {
				t.Errorf("reason = %v, want %s", ce.Details["reason"], tc.reason)
			}
			for k, v := range tc.details {
				if ce.Details[k] != v {
					t.Errorf("details.%s = %v, want %v", k, ce.Details[k], v)
				}
			}
		})
	}
}

func TestFitRequest_ClearsReturnOnAClone(t *testing.T) {
	t.Parallel()

	r := olsReq(ols())
	r.Return = &types.Return{Preset: "minimal"}
	got := FitRequest(types.FitSpec{Name: "a", Request: r})
	if got.Return != nil {
		t.Fatal("fit request keeps its return")
	}
	if r.Return == nil || got == r {
		t.Fatal("FitRequest mutated the caller's request")
	}
	if FitRequest(types.FitSpec{Name: "a"}) != nil {
		t.Fatal("nil request should stay nil")
	}
}

func TestBind(t *testing.T) {
	t.Parallel()

	res := &types.RegressionResult{
		Type:         types.REG_OLS,
		Coefficients: map[string]float64{InterceptKey: 1.5, "x": 2.25, "z": -0.125},
		StdErrors:    map[string]float64{InterceptKey: 0.5, "x": 0.25},
	}
	spec := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"z", "x"}}
	b, err := Bind("m", spec, res)
	if err != nil {
		t.Fatal(err)
	}
	if b.Name != "m" || b.Target != "y" || b.Intercept != 1.5 || b.InterceptStdErr != 0.5 || b.Result != res {
		t.Fatalf("bound = %+v", b)
	}
	if !reflect.DeepEqual(b.Predictors, []string{"z", "x"}) || !reflect.DeepEqual(b.Coefficients, []float64{-0.125, 2.25}) {
		t.Fatalf("order not the spec's: %v %v", b.Predictors, b.Coefficients)
	}
	if !math.IsNaN(b.StdErrors[0]) || b.StdErrors[1] != 0.25 {
		t.Fatalf("std errors = %v", b.StdErrors)
	}
	// The caller's predictor slice is copied, not aliased.
	spec.Predictors[0] = "mutated"
	if b.Predictors[0] != "z" {
		t.Fatal("Bound aliases the spec's predictors")
	}

	// Under selection the retained predictors are the bound ones.
	sel := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x", "z", "w"}, Selection: "forward", Criterion: "aic"}
	res.SelectedFeatures = []string{"x"}
	b, err = Bind("s", sel, res)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(b.Predictors, []string{"x"}) || !reflect.DeepEqual(b.Coefficients, []float64{2.25}) {
		t.Fatalf("selection bound %v %v", b.Predictors, b.Coefficients)
	}

	// A fitted predictor with no coefficient is an engine invariant.
	missing := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"q"}}
	if ce := coded(t, ignoreBound(Bind("q", missing, res))); ce.Code != errors.PROCESSING_INTERNAL {
		t.Fatalf("code = %s", ce.Code)
	}
	noIntercept := &types.RegressionResult{Type: types.REG_OLS, Coefficients: map[string]float64{"x": 1}}
	if ce := coded(t, ignoreBound(Bind("q", spec, noIntercept))); ce.Code != errors.PROCESSING_INTERNAL {
		t.Fatalf("code = %s", ce.Code)
	}
	if ce := coded(t, ignoreBound(Bind("q", nil, res))); ce.Code != errors.PROCESSING_INTERNAL {
		t.Fatalf("code = %s", ce.Code)
	}
}

// TestBind_Intervals: a bound fit carries its confidence bounds aligned
// with Predictors at full precision (the result's own float64s), NaN
// where the result has none, and the result's conf_level.
func TestBind_Intervals(t *testing.T) {
	t.Parallel()

	res := &types.RegressionResult{
		Type:         types.REG_OLS,
		Coefficients: map[string]float64{InterceptKey: 1.5, "x": 2.25, "z": -0.125},
		StdErrors:    map[string]float64{InterceptKey: 0.5, "x": 0.25},
		CILower:      map[string]float64{InterceptKey: 0.1234567890123456789, "x": 1.7000000000000002},
		CIUpper:      map[string]float64{InterceptKey: 2.9, "x": 2.8000000000000003},
		ConfLevel:    0.9,
	}
	spec := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"z", "x"}}
	b, err := Bind("m", spec, res)
	if err != nil {
		t.Fatal(err)
	}
	if b.InterceptCILower != res.CILower[InterceptKey] || b.InterceptCIUpper != 2.9 || b.ConfLevel != 0.9 {
		t.Fatalf("intercept interval [%v, %v] level %v", b.InterceptCILower, b.InterceptCIUpper, b.ConfLevel)
	}
	if !math.IsNaN(b.CILower[0]) || !math.IsNaN(b.CIUpper[0]) || b.CILower[1] != 1.7000000000000002 || b.CIUpper[1] != 2.8000000000000003 {
		t.Fatalf("intervals %v %v, want [NaN 1.7000000000000002] [NaN 2.8000000000000003]", b.CILower, b.CIUpper)
	}
	none, err := Bind("n", spec, &types.RegressionResult{Coefficients: res.Coefficients})
	if err != nil {
		t.Fatal(err)
	}
	if !math.IsNaN(none.InterceptCILower) || !math.IsNaN(none.CIUpper[1]) || none.ConfLevel != 0 {
		t.Fatalf("no intervals: %+v", none)
	}
}

func ignoreBound(_ *Bound, err error) error { return err }

func TestSet(t *testing.T) {
	t.Parallel()

	var nilSet *Set
	if nilSet.Len() != 0 || nilSet.Names() != nil {
		t.Fatal("nil set is not empty")
	}
	ce := coded(t, ignoreBound(nilSet.Lookup("m")))
	if ce.Code != errors.PULSE_MODEL_UNKNOWN || ce.Details["name"] != "m" || !reflect.DeepEqual(ce.Details["available"], []string{}) {
		t.Fatalf("nil lookup = %v %v", ce.Code, ce.Details)
	}

	s := &Set{}
	for _, n := range []string{"b", "a"} {
		if err := s.Add(&Bound{Name: n}); err != nil {
			t.Fatal(err)
		}
	}
	if ce := coded(t, s.Add(&Bound{Name: "a"})); ce.Code != errors.PULSE_MODEL_NAME_COLLISION || ce.Details["name"] != "a" {
		t.Fatalf("collision = %v %v", ce.Code, ce.Details)
	}
	if b, err := s.Lookup("a"); err != nil || b.Name != "a" {
		t.Fatalf("lookup a = %v %v", b, err)
	}
	ce = coded(t, ignoreBound(s.Lookup("zz")))
	if ce.Code != errors.PULSE_MODEL_UNKNOWN || !reflect.DeepEqual(ce.Details["available"], []string{"a", "b"}) {
		t.Fatalf("unknown = %v %v", ce.Code, ce.Details)
	}
	if s.Len() != 2 || !reflect.DeepEqual(s.Names(), []string{"a", "b"}) {
		t.Fatalf("names %v", s.Names())
	}

	ctx := context.Background()
	if SetFrom(ctx) != nil || WithSet(ctx, nil) != ctx {
		t.Fatal("empty ctx carries a set")
	}
	if SetFrom(WithSet(ctx, s)) != s {
		t.Fatal("ctx does not carry the set")
	}
}

const modulePrefix = "github.com/frankbardon/pulse"

// TestModel_ImportBoundary: the model namespace is shared by the runtime
// and the no-execute predict path (internal/descriptor), so it imports
// only the standard library, types, errors and the stdlib-only params
// read recorder internal/paramread — never the engine, the service or
// the descriptor packages.
func TestModel_ImportBoundary(t *testing.T) {
	t.Parallel()

	allowed := map[string]bool{modulePrefix + "/types": true, modulePrefix + "/errors": true, modulePrefix + "/internal/paramread": true}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if strings.Contains(path, ".") && !allowed[path] {
				t.Errorf("%s imports %q; internal/model may import only the standard library, types, errors and internal/paramread", name, path)
			}
		}
	}
	cmd := exec.Command("go", "list", "-deps", modulePrefix+"/internal/model")
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, stderr.String())
	}
	deps := strings.Fields(out.String())
	if len(deps) == 0 {
		t.Fatal("go list -deps returned nothing")
	}
	for _, dep := range deps {
		for _, bad := range []string{"/internal/processing", "/internal/service", "/internal/descriptor", "/descriptor"} {
			if dep == modulePrefix+bad || strings.HasPrefix(dep, modulePrefix+bad+"/") {
				t.Errorf("internal/model transitively imports %q", dep)
			}
		}
	}
}
