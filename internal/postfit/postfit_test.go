package postfit

import (
	"encoding/json"
	stderrors "errors"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/resultcall"
	"github.com/frankbardon/pulse/types"
)

const opName types.PostFitType = "POSTFIT_TEST_ECHO"

func testDecls() map[types.PostFitType]Decl {
	return map[types.PostFitType]Decl{opName: {
		Name:    opName,
		Params:  []descriptor.OperatorParamMeta{{Name: "scale", JSONType: "number", Required: true}},
		Outputs: extend.PostFitSchema{Scalars: []string{"a"}},
	}}
}

func testFits() []types.FitSpec {
	reg := &types.Request{Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}}}}
	vec := &types.Request{Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Fields: []string{"x", "y"}}}}
	return []types.FitSpec{{Name: "m", Request: reg}, {Name: "s", FromSweep: &types.FitFromSweep{Regression: "r"}}, {Name: "v", Request: vec}}
}

// paramsStub refuses any params carrying "bad".
func paramsStub(path, op string, _ []descriptor.OperatorParamMeta, raw json.RawMessage) *errors.CodedError {
	if string(raw) == `{"bad":1}` {
		return errors.NewCodedErrorWithDetails(errors.PULSE_REQUEST_UNKNOWN_FIELD, "bad", map[string]any{"path": path, "op": op})
	}
	return nil
}

// TestCheck_Reasons: every structural fault is PULSE_POSTFIT_INVALID
// with its reason, entry index and label; the params check runs last
// with the entry's path; a valid batch (a regression fit and a
// sweep-bound one) passes.
func TestCheck_Reasons(t *testing.T) {
	t.Parallel()

	ok := types.PostFitSpec{Label: "p", Type: opName, Fits: []string{"m", "s"}, Params: json.RawMessage(`{"scale":1}`)}
	cases := []struct {
		name   string
		specs  []types.PostFitSpec
		reason string
		index  int
	}{
		{"label missing", []types.PostFitSpec{{Type: opName}}, ReasonLabelMissing, 0},
		{"label duplicate", []types.PostFitSpec{ok, ok}, ReasonLabelDuplicate, 1},
		{"type missing", []types.PostFitSpec{{Label: "p"}}, ReasonTypeMissing, 0},
		{"type unknown", []types.PostFitSpec{{Label: "p", Type: "POSTFIT_TEST_NOPE"}}, ReasonTypeUnknown, 0},
		{"fit missing", []types.PostFitSpec{{Label: "p", Type: opName, Fits: []string{""}}}, ReasonFitMissing, 0},
		{"fit duplicate", []types.PostFitSpec{{Label: "p", Type: opName, Fits: []string{"m", "m"}}}, ReasonFitDuplicate, 0},
		{"fit unknown", []types.PostFitSpec{{Label: "p", Type: opName, Fits: []string{"zz"}}}, ReasonFitUnknown, 0},
		{"fit kind", []types.PostFitSpec{{Label: "p", Type: opName, Fits: []string{"v"}}}, ReasonFitKind, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Check(c.specs, testFits(), testDecls(), paramsStub)
			if err == nil || err.Code != errors.PULSE_POSTFIT_INVALID || err.Details["reason"] != c.reason || err.Details[Slot] != c.index {
				t.Fatalf("got %v %+v, want reason %s at %d", err, err, c.reason, c.index)
			}
		})
	}
	if err := Check([]types.PostFitSpec{ok}, testFits(), testDecls(), paramsStub); err != nil {
		t.Fatalf("valid batch refused: %v", err)
	}
	if err := Check([]types.PostFitSpec{{Label: "p", Type: "POSTFIT_TEST_NOPE"}}, nil, testDecls(), nil); !reflect.DeepEqual(err.Details["available"], []string{string(opName)}) {
		t.Errorf("type_unknown available %v", err.Details["available"])
	}
	bad := ok
	bad.Label = "q"
	bad.Params = json.RawMessage(`{"bad":1}`)
	err := Check([]types.PostFitSpec{ok, bad}, testFits(), testDecls(), paramsStub)
	if err == nil || err.Code != errors.PULSE_REQUEST_UNKNOWN_FIELD || err.Details["path"] != "post_fit[1]" || err.Details["op"] != string(opName) {
		t.Fatalf("params refusal: %v %+v", err, err)
	}
	// An operator declaring no params passes any params unchecked.
	decls := testDecls()
	d := decls[opName]
	d.Params = nil
	decls[opName] = d
	if err := Check([]types.PostFitSpec{bad}, testFits(), decls, paramsStub); err != nil {
		t.Errorf("undeclared params checked: %v", err)
	}
}

// TestCheckSchema: each malformed declaration names its reason; a well
// formed one passes.
func TestCheckSchema(t *testing.T) {
	t.Parallel()

	col := func(n string, k extend.PostFitColumnKind) extend.PostFitColumn {
		return extend.PostFitColumn{Name: n, Kind: k}
	}
	cases := map[string]extend.PostFitSchema{
		"empty_output_schema":   {},
		"empty_output_name":     {Scalars: []string{""}},
		"duplicate_output_name": {Scalars: []string{"a"}, Tables: []extend.PostFitTableSchema{{Name: "a", Columns: []extend.PostFitColumn{col("x", extend.PostFitFloat)}}}},
		"empty_table":           {Tables: []extend.PostFitTableSchema{{Name: "t"}}},
		"empty_column_name":     {Tables: []extend.PostFitTableSchema{{Name: "t", Columns: []extend.PostFitColumn{col("", extend.PostFitFloat)}}}},
		"duplicate_column_name": {Tables: []extend.PostFitTableSchema{{Name: "t", Columns: []extend.PostFitColumn{col("x", extend.PostFitFloat), col("x", extend.PostFitString)}}}},
		"invalid_column_kind":   {Tables: []extend.PostFitTableSchema{{Name: "t", Columns: []extend.PostFitColumn{col("x", "int")}}}},
	}
	for want, s := range cases {
		if got, msg := CheckSchema(s); got != want || msg == "" {
			t.Errorf("%s: got %q %q", want, got, msg)
		}
	}
	good := extend.PostFitSchema{Scalars: []string{"a"}, Tables: []extend.PostFitTableSchema{{Name: "t", Columns: []extend.PostFitColumn{col("x", extend.PostFitFloat), col("y", extend.PostFitString)}}}}
	if got, _ := CheckSchema(good); got != "" {
		t.Errorf("well-formed schema refused: %s", got)
	}
}

func tableDecl() Decl {
	return Decl{Name: opName, Outputs: extend.PostFitSchema{Scalars: []string{"a"}, Tables: []extend.PostFitTableSchema{{Name: "t",
		Columns: []extend.PostFitColumn{{Name: "k", Kind: extend.PostFitString}, {Name: "v", Kind: extend.PostFitFloat}}}}}}
}

func goodOutput() extend.PostFitOutput {
	return extend.PostFitOutput{Scalars: map[string]float64{"a": math.NaN()},
		Tables: map[string]*types.PostFitTable{"t": {Columns: []string{"k", "v"}, Rows: [][]any{{"x", 1.5}, {nil, nil}}}}}
}

// TestCheckOutput: an output exactly matching the declaration (NaN
// scalar and null cells included) passes; each breach is
// PULSE_EXTENSION_POSTFIT_OUTPUT_MISMATCH with its reason.
func TestCheckOutput(t *testing.T) {
	t.Parallel()

	if err := CheckOutput(0, "p", tableDecl(), goodOutput()); err != nil {
		t.Fatalf("valid output refused: %v", err)
	}
	cases := map[string]func(o *extend.PostFitOutput){
		"missing_scalar":    func(o *extend.PostFitOutput) { delete(o.Scalars, "a") },
		"undeclared_scalar": func(o *extend.PostFitOutput) { o.Scalars["b"] = 1 },
		"missing_table":     func(o *extend.PostFitOutput) { delete(o.Tables, "t") },
		"undeclared_table":  func(o *extend.PostFitOutput) { o.Tables["u"] = &types.PostFitTable{} },
		"columns_mismatch":  func(o *extend.PostFitOutput) { o.Tables["t"].Columns = []string{"v", "k"} },
		"row_width":         func(o *extend.PostFitOutput) { o.Tables["t"].Rows[1] = []any{nil} },
		"cell_kind":         func(o *extend.PostFitOutput) { o.Tables["t"].Rows[0][1] = 2 },
	}
	for reason, edit := range cases {
		o := goodOutput()
		edit(&o)
		err := CheckOutput(3, "p", tableDecl(), o)
		if err == nil || err.Code != errors.PULSE_EXTENSION_POSTFIT_OUTPUT_MISMATCH || err.Details["reason"] != reason || err.Details[Slot] != 3 {
			t.Errorf("%s: got %v %+v", reason, err, err)
		}
	}
	o := goodOutput()
	o.Tables["t"].Rows[0][0] = 1.0
	if err := CheckOutput(0, "p", tableDecl(), o); err == nil || err.Details["column"] != "k" {
		t.Errorf("float in a string column: %v", err)
	}
}

// TestSlotErrorOf: a coded error keeps its code and details, any other
// is PULSE_POSTFIT_FAILED.
func TestSlotErrorOf(t *testing.T) {
	t.Parallel()

	spec := &types.PostFitSpec{Label: "p", Type: opName}
	se := SlotErrorOf(spec, errors.NewCodedErrorWithDetails(errors.PULSE_RESULT_INVALID, "m", map[string]any{"x": 1}))
	if se.Code != string(errors.PULSE_RESULT_INVALID) || se.Details["x"] != 1 {
		t.Errorf("coded: %+v", se)
	}
	se = SlotErrorOf(spec, stderrors.New("boom"))
	if se.Code != string(errors.PULSE_POSTFIT_FAILED) || se.Details["label"] != "p" {
		t.Errorf("uncoded: %+v", se)
	}
}

// handleSet binds "m": a media-mix-like fit with a vcov, an adstock
// record and outputs.draws; and "bad", a failed fit.
func handleSet(t *testing.T) *model.Set {
	t.Helper()
	decay := 0.5
	var raw map[string]any
	if err := json.Unmarshal([]byte(`{"names":["(intercept)","tv"],"draws":[[[1,2],[3,4]],[[5,6],[7,8]]],"credible_level":0.9}`), &raw); err != nil {
		t.Fatal(err)
	}
	set := &model.Set{}
	b := &model.Bound{Name: "m", Type: types.REG_MMM_BAYES, Target: "sales",
		Vcov:     &types.MatrixValues{RowKeys: []string{"tv"}, ColumnKeys: []string{"tv"}, Values: [][]float64{{0.25}}},
		Adstocks: map[string]model.Adstock{"tv": {Kernel: "geometric", Decay: &decay, MaxLag: 2}},
		Result: &types.RegressionResult{Coefficients: map[string]float64{"(intercept)": 1, "tv": 2},
			StdErrors: map[string]float64{"tv": 0.5}, Outputs: map[string]any{model.MMMDrawsKey: raw}}}
	if err := set.Add(b); err != nil {
		t.Fatal(err)
	}
	if err := set.AddFailed(model.Signature{Name: "bad", Type: types.REG_OLS}, &types.SlotError{Code: string(errors.PROCESSING_REGRESSION_RANK_DEFICIENT), Message: "rank"}); err != nil {
		t.Fatal(err)
	}
	return set
}

// TestRun_HandlesAreCopies: the operator reads the named fit's
// coefficients, vcov, adstock and pooled draws, a slot value and the
// optimize solutions; mutating what it read never reaches the bound
// fit; a name the entry does not list has no handle.
func TestRun_HandlesAreCopies(t *testing.T) {
	t.Parallel()

	set := handleSet(t)
	reqs := []*types.Request{{Label: "s", Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Label: "n"}}}}
	env := Env{Fits: set,
		Results:       &resultcall.Reader{Catalog: resultcall.CatalogOf(reqs), Responses: []*types.Response{{Data: []map[string]any{{"n": 7.0}}}}},
		Optimizations: []types.OptimizeResult{{Name: "o", Objective: 3, Converged: true, Status: "ok", Variables: []types.OptimizeVariableResult{{Name: "x", Value: 2}}}, {Name: "f", Error: &types.SlotError{Code: "C"}}}}
	var seen struct {
		coef, se, n float64
		vcov        float64
		ad          extend.PostFitAdstock
		draws       extend.PostFitDraws
		sols        []extend.PostFitSolution
		other       extend.PostFitFit
		chans       []string
	}
	op := &Operator{Decl: Decl{Name: opName, Outputs: extend.PostFitSchema{Scalars: []string{"a"}}}, Run: func(in extend.PostFitInput) (extend.PostFitOutput, error) {
		f := in.Fit("m")
		c := f.Coefficients()
		seen.coef = c["tv"]
		c["tv"] = 99
		seen.se = f.StdErrors()["tv"]
		v := f.Vcov()
		seen.vcov = v.Values[0][0]
		v.Values[0][0] = 99
		seen.chans = f.AdstockChannels()
		seen.ad, _ = f.Adstock("tv")
		seen.draws, _ = f.Draws()
		seen.n, _ = in.Result("s", "n")
		seen.sols = in.Optimizations()
		seen.other = in.Fit("bad")
		return extend.PostFitOutput{Scalars: map[string]float64{"a": seen.coef}}, nil
	}}
	spec := &types.PostFitSpec{Label: "p", Type: opName, Fits: []string{"m"}}
	out, err := Run(0, spec, op, env)
	if err != nil || out.Failed || out.Result.Scalars["a"] != 2 {
		t.Fatalf("run: %+v %v", out, err)
	}
	b, _ := set.Lookup("m")
	if b.Result.Coefficients["tv"] != 2 || b.Vcov.Values[0][0] != 0.25 {
		t.Error("a handle aliased the bound fit")
	}
	if seen.se != 0.5 || seen.vcov != 0.25 || seen.n != 7 || seen.other != nil || !reflect.DeepEqual(seen.chans, []string{"tv"}) {
		t.Errorf("reads: %+v", seen)
	}
	if seen.ad.Kernel != "geometric" || seen.ad.Decay != 0.5 || !math.IsNaN(seen.ad.Peak) || len(seen.ad.Weights) != 3 {
		t.Errorf("adstock %+v", seen.ad)
	}
	if !reflect.DeepEqual(seen.draws.Names, []string{"(intercept)", "tv"}) || !reflect.DeepEqual(seen.draws.Draws[1], []float64{3, 4, 7, 8}) || seen.draws.CredibleLevel != 0.9 {
		t.Errorf("draws %+v", seen.draws)
	}
	if len(seen.sols) != 2 || seen.sols[0].Variables["x"] != 2 || seen.sols[0].Objective != 3 || !seen.sols[1].Failed || seen.sols[1].ErrorCode != "C" {
		t.Errorf("solutions %+v", seen.sols)
	}

	// A named fit that failed fails the entry alone with its code, before
	// the operator runs.
	ran := false
	op.Run = func(extend.PostFitInput) (extend.PostFitOutput, error) {
		ran = true
		return extend.PostFitOutput{}, nil
	}
	out, err = Run(0, &types.PostFitSpec{Label: "p", Type: opName, Fits: []string{"bad"}}, op, env)
	if err != nil || !out.Failed || ran || out.Result.Error.Code != string(errors.PROCESSING_REGRESSION_RANK_DEFICIENT) || out.Result.Error.Details["fit"] != "bad" {
		t.Fatalf("failed fit: %+v %v ran=%v", out, err, ran)
	}
	// An undeclared output is the fatal mismatch.
	op.Run = func(extend.PostFitInput) (extend.PostFitOutput, error) {
		return extend.PostFitOutput{Scalars: map[string]float64{"a": 1, "b": 2}}, nil
	}
	if _, err := Run(0, spec, op, env); err == nil {
		t.Fatal("an undeclared scalar passed")
	}
	// A draws-free fit reports none.
	set2 := &model.Set{}
	_ = set2.Add(&model.Bound{Name: "m", Result: &types.RegressionResult{}})
	op.Run = func(in extend.PostFitInput) (extend.PostFitOutput, error) {
		if _, ok := in.Fit("m").Draws(); ok {
			return extend.PostFitOutput{}, stderrors.New("draws on a draws-free fit")
		}
		return extend.PostFitOutput{Scalars: map[string]float64{"a": 0}}, nil
	}
	if out, err := Run(0, spec, op, Env{Fits: set2}); err != nil || out.Failed {
		t.Fatalf("draws-free: %+v %v", out, err)
	}
}
