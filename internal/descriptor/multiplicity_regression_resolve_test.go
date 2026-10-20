package descriptor

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// multiplicity_regression_resolve_test.go pins the regression surface of
// the one multiplicity resolver (U48 E2-S1): a RegressionSpec's own
// block is the only way in (an inherited block never reaches a
// regression), its fields fall through like any slot's, its families
// (`request` default, `compose` inside Compose), the member
// coefficients, and the refusals — an unknown or repeated coefficient,
// a family the surface does not offer, and any correcting method on
// REG_BAYES_LINEAR.

func regm(method types.MultiplicityMethod, family types.MultiplicityFamily, alpha float64, coefs ...string) *types.RegressionMultiplicity {
	return &types.RegressionMultiplicity{Method: method, Family: family, Alpha: alpha, Coefficients: coefs}
}

func rspec(typ types.RegressionType, m *types.RegressionMultiplicity) *types.RegressionSpec {
	return &types.RegressionSpec{Type: typ, Target: "y", Predictors: []string{"a", "b", "c"}, Multiplicity: m}
}

func TestResolveMultiplicity_Regressions(t *testing.T) {
	type want struct {
		method   types.MultiplicityMethod
		family   types.MultiplicityFamily
		alpha    float64
		member   bool
		coefs    []string
		explicit bool
	}
	cases := []struct {
		name string
		req  *types.Request
		def  *types.Multiplicity
		want []want
	}{
		{
			name: "own block, every predictor by default",
			req:  &types.Request{Regressions: []*types.RegressionSpec{rspec(types.REG_OLS, regm(mHolm, "", 0))}},
			want: []want{{mHolm, fRequest, 0.05, true, []string{"a", "b", "c"}, false}},
		},
		{
			name: "explicit coefficients, intercept allowed, own alpha",
			req:  &types.Request{Regressions: []*types.RegressionSpec{rspec(types.REG_GLM, regm(mBH, fRequest, 0.1, "b", "(intercept)"))}},
			want: []want{{mBH, fRequest, 0.1, true, []string{"b", "(intercept)"}, true}},
		},
		{
			name: "an empty own block inherits the request's method and alpha",
			req: &types.Request{Multiplicity: mult(mBonf, "", 0.01),
				Regressions: []*types.RegressionSpec{rspec(types.REG_OLS, regm("", "", 0))}},
			want: []want{{mBonf, fRequest, 0.01, true, []string{"a", "b", "c"}, false}},
		},
		{
			name: "an empty own block inherits the instance default",
			req:  &types.Request{Regressions: []*types.RegressionSpec{rspec(types.REG_OLS, regm("", "", 0, "a"))}},
			def:  mult(mHolm, "", 0),
			want: []want{{mHolm, fRequest, 0.05, true, []string{"a"}, true}},
		},
		{
			name: "an inherited block never reaches a regression without one",
			req: &types.Request{Multiplicity: mult(mHolm, "", 0),
				Regressions: []*types.RegressionSpec{rspec(types.REG_OLS, nil)}},
			def:  mult(mHolm, "", 0),
			want: []want{{mNone, fRequest, 0, false, nil, false}},
		},
		{
			name: "explicit none is no member",
			req:  &types.Request{Regressions: []*types.RegressionSpec{rspec(types.REG_OLS, regm(mNone, "", 0))}},
			def:  mult(mHolm, "", 0),
			want: []want{{mNone, fRequest, 0.05, false, nil, false}},
		},
		{
			name: "REG_BAYES_LINEAR with method none is accepted and no member",
			req:  &types.Request{Regressions: []*types.RegressionSpec{rspec(types.REG_BAYES_LINEAR, regm(mNone, "", 0))}},
			want: []want{{mNone, fRequest, 0, false, nil, false}},
		},
		{
			name: "an inherited compose family falls back to request outside Compose",
			req: &types.Request{Multiplicity: mult(mHolm, "", 0),
				Regressions: []*types.RegressionSpec{rspec(types.REG_OLS, regm("", "", 0))}},
			def:  mult("", fCompose, 0),
			want: []want{{mHolm, fRequest, 0.05, true, []string{"a", "b", "c"}, false}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plan, err := ResolveMultiplicity(c.req, c.def, nil)
			if err != nil {
				t.Fatal(err)
			}
			if plan == nil || len(plan.Regressions) != len(c.want) {
				t.Fatalf("plan regressions = %+v, want %d", plan, len(c.want))
			}
			for i, w := range c.want {
				got := plan.Regressions[i]
				if got.Method != w.method || got.Family != w.family || got.Member != w.member ||
					got.ExplicitCoefficients != w.explicit || !reflect.DeepEqual(got.Coefficients, w.coefs) {
					t.Errorf("regressions[%d] = %+v, want %+v", i, got, w)
				}
				if w.member && got.Alpha != w.alpha {
					t.Errorf("regressions[%d] alpha = %v, want %v", i, got.Alpha, w.alpha)
				}
				if got.Slot != "regressions[0]" || got.Operator != string(c.req.Regressions[i].Type) {
					t.Errorf("regressions[%d] slot / operator = %q / %q", i, got.Slot, got.Operator)
				}
			}
		})
	}
}

func TestResolveMultiplicity_RegressionRefusals(t *testing.T) {
	inv := errors.PULSE_MULTIPLICITY_INVALID
	cases := []struct {
		name string
		req  *types.Request
		def  *types.Multiplicity
		code errors.Code
		want map[string]any
	}{
		{"unknown coefficient", &types.Request{Regressions: []*types.RegressionSpec{rspec(types.REG_OLS, regm(mHolm, "", 0, "a", "zzz"))}}, nil, inv,
			map[string]any{"slot": "regressions[0].multiplicity", "key": "coefficients", "value": "zzz", "valid": []string{"(intercept)", "a", "b", "c"}}},
		{"repeated coefficient", &types.Request{Regressions: []*types.RegressionSpec{rspec(types.REG_OLS, regm(mHolm, "", 0, "a", "a"))}}, nil, inv,
			map[string]any{"key": "coefficients", "value": "a", "reason": multReasonCoefficientDup}},
		{"layer family", &types.Request{Regressions: []*types.RegressionSpec{rspec(types.REG_OLS, regm(mHolm, fLayer, 0))}}, nil, inv,
			map[string]any{"key": "family", "value": "layer", "reason": multReasonSurface}},
		{"row family", &types.Request{Regressions: []*types.RegressionSpec{rspec(types.REG_OLS, regm(mHolm, fRow, 0))}}, nil, inv,
			map[string]any{"key": "family", "value": "row", "reason": multReasonSurface}},
		{"matrix family", &types.Request{Regressions: []*types.RegressionSpec{rspec(types.REG_OLS, regm(mHolm, fMatrix, 0))}}, nil, inv,
			map[string]any{"key": "family", "reason": multReasonMatrixFamily}},
		{"compose outside Compose", &types.Request{Regressions: []*types.RegressionSpec{rspec(types.REG_OLS, regm(mHolm, fCompose, 0))}}, nil, inv,
			map[string]any{"key": "family", "reason": multReasonComposeOutside}},
		{"unknown method", &types.Request{Regressions: []*types.RegressionSpec{rspec(types.REG_OLS, regm("sidak", "", 0))}}, nil, inv,
			map[string]any{"key": "method", "slot": "regressions[0].multiplicity"}},
		{"alpha out of range", &types.Request{Regressions: []*types.RegressionSpec{rspec(types.REG_OLS, regm(mHolm, "", 2))}}, nil, inv,
			map[string]any{"key": "alpha"}},
		{"Bayes with an own method", &types.Request{Regressions: []*types.RegressionSpec{rspec(types.REG_BAYES_LINEAR, regm(mHolm, "", 0))}}, nil, inv,
			map[string]any{"slot": "regressions[0].multiplicity", "key": "method", "value": "holm", "reason": multReasonBayes, "valid": []string{"none"}}},
		{"Bayes with an inherited method", &types.Request{Multiplicity: mult(mBH, "", 0),
			Regressions: []*types.RegressionSpec{rspec(types.REG_BAYES_LINEAR, regm("", "", 0))}}, nil, inv,
			map[string]any{"key": "method", "value": "bh", "reason": multReasonBayes}},
		{"Bayes with an instance method", &types.Request{Regressions: []*types.RegressionSpec{rspec(types.REG_BAYES_LINEAR, regm("", "", 0))}}, mult(mHolm, "", 0), inv,
			map[string]any{"key": "method", "value": "holm"}},
		{"request family mixes a test and a regression", &types.Request{Tests: []*types.Test{ttest(mult(mHolm, "", 0))},
			Regressions: []*types.RegressionSpec{rspec(types.REG_OLS, regm(mBH, "", 0))}}, nil, errors.PULSE_MULTIPLICITY_CONFLICT,
			map[string]any{"family": "request", "methods": []string{"bh", "holm"}, "slots": []string{"regressions[0]", "tests[0]"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ResolveMultiplicity(c.req, c.def, nil)
			wantCoded(t, err, c.code, c.want)
		})
	}
}

// TestResolveMultiplicity_RegressionHiddenTypeJoinsNoFamily: a regression
// type the instance hides routes as never-registered — the regression
// validators refuse it, so the resolver neither refuses it as Bayes nor
// makes it a member.
func TestResolveMultiplicity_RegressionHiddenTypeJoinsNoFamily(t *testing.T) {
	inst := NewInstanceSnapshot(nil, FeatureSet{
		Enabled: []string{featProcess, featMultiplicity, string(types.REG_OLS)},
		Hidden:  []string{string(types.REG_BAYES_LINEAR), string(types.REG_GLM)},
	})
	req := &types.Request{Regressions: []*types.RegressionSpec{
		rspec(types.REG_BAYES_LINEAR, regm(mHolm, "", 0)),
		rspec(types.REG_GLM, regm(mHolm, "", 0)),
		rspec(types.REG_OLS, regm(mHolm, "", 0)),
	}}
	plan, err := ResolveMultiplicity(req, nil, inst)
	if err != nil {
		t.Fatal(err)
	}
	for i, wantMember := range []bool{false, false, true} {
		if plan.Regressions[i].Member != wantMember {
			t.Errorf("regressions[%d] (%s) member = %v, want %v", i, req.Regressions[i].Type, plan.Regressions[i].Member, wantMember)
		}
	}
}

func TestResolveComposeMultiplicity_RegressionSlots(t *testing.T) {
	slot := func(m *types.RegressionMultiplicity) *types.Request {
		return &types.Request{Regressions: []*types.RegressionSpec{rspec(types.REG_OLS, m)}}
	}
	plan, err := ResolveComposeMultiplicity(&types.ComposedRequest{Requests: []*types.Request{
		slot(regm(mHolm, fCompose, 0, "a")), {Tests: []*types.Test{ttest(mult(mHolm, fCompose, 0))}},
	}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Requests[0].Regressions[0]; !got.Member || got.Family != fCompose || got.Method != mHolm {
		t.Errorf("compose slot regression = %+v, want a holm compose member", got)
	}
	// The ComposedRequest's block reaches a regression's empty own block.
	plan, err = ResolveComposeMultiplicity(&types.ComposedRequest{Multiplicity: mult(mBH, fCompose, 0),
		Requests: []*types.Request{slot(regm("", "", 0))}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Requests[0].Regressions[0]; !got.Member || got.Family != fCompose || got.Method != mBH {
		t.Errorf("inherited compose block = %+v, want a bh compose member", got)
	}
	// A compose family mixing methods across a regression and a test.
	_, err = ResolveComposeMultiplicity(&types.ComposedRequest{Requests: []*types.Request{
		slot(regm(mHolm, fCompose, 0)), {Tests: []*types.Test{ttest(mult(mBH, fCompose, 0))}},
	}}, nil, nil)
	wantCoded(t, err, errors.PULSE_MULTIPLICITY_CONFLICT, map[string]any{
		"family": "compose", "slots": []string{"requests[1].tests[0]", "requests[0].regressions[0]"}})
	// Bayes refuses inside Compose too, located by the slot.
	_, err = ResolveComposeMultiplicity(&types.ComposedRequest{Requests: []*types.Request{
		{Regressions: []*types.RegressionSpec{rspec(types.REG_BAYES_LINEAR, regm(mHolm, fCompose, 0))}},
	}}, nil, nil)
	wantCoded(t, err, errors.PULSE_MULTIPLICITY_INVALID, map[string]any{"request": 0, "reason": multReasonBayes})
}

// TestPredict_RegressionPValues: a REG_OLS / REG_GLM regression counts
// one p-value per non-intercept predictor in PredictResult.PValues —
// uncorrected unless its own block claims them — and
// PULSE_ADVISORY_MANY_TESTS sees them; REG_BAYES_LINEAR counts none.
func TestPredict_RegressionPValues(t *testing.T) {
	preds := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	fields := []encoding.Field{{Name: "y", Type: encoding.FieldTypeF64, Description: "Measured outcome y"}}
	for _, p := range preds {
		fields = append(fields, encoding.Field{Name: p, Type: encoding.FieldTypeF64, Description: "Measured predictor " + p})
	}
	cohort := buildTestPulseFile(t, &encoding.Schema{Fields: fields})
	reg := func(typ types.RegressionType, m *types.RegressionMultiplicity) *types.RegressionSpec {
		s := &types.RegressionSpec{Type: typ, Target: "y", Predictors: preds, Multiplicity: m}
		if typ == types.REG_GLM {
			s.Family = "poisson"
		}
		return s
	}
	pen := func(s *types.RegressionSpec, penalty, resample string) *types.RegressionSpec {
		s.Penalty, s.Alpha, s.Resample = penalty, 0.1, resample
		if penalty == "elasticnet" {
			s.L1Ratio = 0.5
		}
		return s
	}
	run := func(req *types.Request) (bool, *descriptor.PValueCount) {
		t.Helper()
		env := predictFromBytes(cohort, req, &PredictOptions{})
		if len(env.Errors) > 0 {
			t.Fatalf("predict errors: %+v", env.Errors)
		}
		res := env.Data.(*descriptor.PredictResult)
		for _, a := range res.Advisories {
			if a.Code == string(errors.PULSE_ADVISORY_MANY_TESTS) {
				return true, res.PValues
			}
		}
		return false, res.PValues
	}
	cases := []struct {
		name               string
		req                *types.Request
		advisory           bool
		total, uncorrected int
	}{
		{"uncorrected OLS", &types.Request{Regressions: []*types.RegressionSpec{reg(types.REG_OLS, nil)}}, true, 10, 10},
		{"uncorrected GLM", &types.Request{Regressions: []*types.RegressionSpec{reg(types.REG_GLM, nil)}}, true, 10, 10},
		{"member OLS", &types.Request{Regressions: []*types.RegressionSpec{reg(types.REG_OLS, regm(mHolm, "", 0))}}, false, 10, 0},
		{"member subset", &types.Request{Regressions: []*types.RegressionSpec{reg(types.REG_OLS, regm(mHolm, "", 0, "a", "(intercept)"))}}, false, 11, 9},
		{"Bayes", &types.Request{Regressions: []*types.RegressionSpec{reg(types.REG_BAYES_LINEAR, nil)}}, false, 0, 0},
		// reference-oracles E2-S3: an l1 / elasticnet fit without resample
		// reports every p-value null (regpenalty.InferenceUndefined), so
		// none is counted, corrected or not; under resample they count.
		{"OLS l1", &types.Request{Regressions: []*types.RegressionSpec{pen(reg(types.REG_OLS, nil), "l1", "")}}, false, 0, 0},
		{"GLM elasticnet member", &types.Request{Regressions: []*types.RegressionSpec{pen(reg(types.REG_GLM, regm(mHolm, "", 0)), "elasticnet", "")}}, false, 0, 0},
		{"OLS l1 under bootstrap", &types.Request{Regressions: []*types.RegressionSpec{pen(reg(types.REG_OLS, nil), "l1", "bootstrap")}}, true, 10, 10},
		{"OLS l2", &types.Request{Regressions: []*types.RegressionSpec{pen(reg(types.REG_OLS, nil), "l2", "")}}, true, 10, 10},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, pv := run(c.req)
			if got != c.advisory {
				t.Errorf("advisory = %v, want %v (p-values %+v)", got, c.advisory, pv)
			}
			if c.total == 0 {
				if pv != nil {
					t.Errorf("p-values = %+v, want nil", pv)
				}
				return
			}
			if pv == nil || pv.Total != c.total || pv.Uncorrected != c.uncorrected || pv.Basis != descriptor.PValueBasisExact {
				t.Errorf("p-values = %+v, want total %d uncorrected %d exact", pv, c.total, c.uncorrected)
			}
		})
	}
}
