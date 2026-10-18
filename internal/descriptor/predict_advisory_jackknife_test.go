package descriptor

import (
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/regresample"
	"github.com/frankbardon/pulse/types"
)

// jackknifeCohort is a two-f64 cohort of `records` zero rows: the
// header + schema, then records × 16 payload bytes, so predict's
// file-length record count is exactly `records`.
func jackknifeCohort(t *testing.T, records int) []byte {
	t.Helper()
	data := buildTestPulseFile(t, &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64, Description: "Predictor measured per account"},
		{Name: "y", Type: encoding.FieldTypeF64, Description: "Outcome measured per account"},
	}})
	return append(data, make([]byte, records*16)...)
}

func jackknifeAdvisories(t *testing.T, records int, regs []*types.RegressionSpec, opts *PredictOptions) []descriptor.Advisory {
	t.Helper()
	res := predictFromBytes(jackknifeCohort(t, records), &types.Request{Regressions: regs}, opts).Data.(*descriptor.PredictResult)
	var out []descriptor.Advisory
	for _, a := range res.Advisories {
		if a.Code == string(errors.PULSE_ADVISORY_JACKKNIFE_COST) {
			out = append(out, a)
		}
	}
	return out
}

// TestPredict_AdvisoryJackknifeCost: a leave-one-out jackknife on
// REG_OLS / REG_GLM over a cohort of at least LeaveOneOutCostRows
// records fires once per slot, suggesting jackknife_groups; one record
// fewer, a delete-a-group jackknife, a bootstrap, an unresampled fit and
// a hidden type never fire.
func TestPredict_AdvisoryJackknifeCost(t *testing.T) {
	at := regresample.LeaveOneOutCostRows
	reg := func(typ types.RegressionType, mut func(*types.RegressionSpec)) *types.RegressionSpec {
		r := &types.RegressionSpec{Type: typ, Target: "y", Predictors: []string{"x"}, Resample: "jackknife"}
		if typ == types.REG_GLM {
			r.Family = "poisson"
		}
		if mut != nil {
			mut(r)
		}
		return r
	}
	cases := []struct {
		name    string
		records int
		regs    []*types.RegressionSpec
		inst    *InstanceSnapshot
		slots   []string
	}{
		{"ols at the threshold", at, []*types.RegressionSpec{reg(types.REG_OLS, nil)}, nil, []string{"regressions[0]"}},
		{"glm beside an unresampled fit", at + 5, []*types.RegressionSpec{
			reg(types.REG_OLS, func(r *types.RegressionSpec) { r.Resample = "" }), reg(types.REG_GLM, nil),
		}, nil, []string{"regressions[1]"}},
		{"one record short", at - 1, []*types.RegressionSpec{reg(types.REG_OLS, nil)}, nil, nil},
		{"jackknife_groups", at, []*types.RegressionSpec{reg(types.REG_OLS, func(r *types.RegressionSpec) { r.JackknifeGroups = 20 })}, nil, nil},
		{"jackknife_by", at, []*types.RegressionSpec{reg(types.REG_GLM, func(r *types.RegressionSpec) { r.JackknifeBy = "x" })}, nil, nil},
		{"bootstrap", at, []*types.RegressionSpec{reg(types.REG_OLS, func(r *types.RegressionSpec) { r.Resample = "bootstrap" })}, nil, nil},
		{"hidden REG_OLS", at, []*types.RegressionSpec{reg(types.REG_OLS, nil)}, scopedExcept("REG_OLS"), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := jackknifeAdvisories(t, c.records, c.regs, &PredictOptions{Instance: c.inst})
			if len(got) != len(c.slots) {
				t.Fatalf("advisories = %+v, want %d", got, len(c.slots))
			}
			for i, slot := range c.slots {
				a := got[i]
				op := string(c.regs[len(c.regs)-1].Type)
				want := map[string]any{"slot": slot, "operator": op, "records": int64(c.records),
					"threshold": regresample.LeaveOneOutCostRows,
					"suggested": map[string]any{"jackknife_groups": regresample.SuggestedGroups}}
				if !reflect.DeepEqual(a.Details, want) {
					t.Errorf("details = %v, want %v", a.Details, want)
				}
				if !strings.Contains(a.Message, slot) || !strings.Contains(a.Message, "jackknife_groups") {
					t.Errorf("message %q names neither the slot nor the remedy", a.Message)
				}
				if hits := LintGuidanceText(a.Message); len(hits) > 0 {
					t.Errorf("lint hits %+v", hits)
				}
			}
		})
	}
}

// TestPredict_AdvisoryJackknifeCostNeverEscalated: strict predict keeps
// the advisory out of errors and warnings (valid stays true), and
// Options.SuppressAdvisories drops it.
func TestPredict_AdvisoryJackknifeCostNeverEscalated(t *testing.T) {
	code := string(errors.PULSE_ADVISORY_JACKKNIFE_COST)
	req := &types.Request{Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Resample: "jackknife"}}}
	env := predictFromBytes(jackknifeCohort(t, regresample.LeaveOneOutCostRows), req, &PredictOptions{Strict: true})
	res := env.Data.(*descriptor.PredictResult)
	if len(res.Advisories) != 1 || res.Advisories[0].Code != code {
		t.Fatalf("vacuous: advisories = %+v", res.Advisories)
	}
	if !res.Valid || len(env.Errors) != 0 || len(env.Warnings) != 0 {
		t.Errorf("strict: valid=%v errors=%v warnings=%v", res.Valid, env.Errors, env.Warnings)
	}
	suppressed := (*InstanceSnapshot)(nil).WithSuppressedAdvisories([]string{code})
	if got := jackknifeAdvisories(t, regresample.LeaveOneOutCostRows, req.Regressions, &PredictOptions{Instance: suppressed}); len(got) != 0 {
		t.Errorf("suppressed: %+v", got)
	}
}

// TestPredict_JackknifeKnobRefusals: predict refuses the jackknife knob
// misuses with the shared rule's code and details (internal/regresample,
// the runtime's ValidateRegression too), and an unknown jackknife_by is
// the field-reference refusal.
func TestPredict_JackknifeKnobRefusals(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*types.RegressionSpec)
		code errors.Code
	}{
		{"both", func(r *types.RegressionSpec) { r.JackknifeGroups, r.JackknifeBy = 5, "x" }, errors.PROCESSING_CONFIG},
		{"groups one", func(r *types.RegressionSpec) { r.JackknifeGroups = 1 }, errors.PROCESSING_CONFIG},
		{"groups on bootstrap", func(r *types.RegressionSpec) { r.Resample, r.JackknifeGroups = "bootstrap", 5 }, errors.PROCESSING_CONFIG},
		{"unknown jackknife_by", func(r *types.RegressionSpec) { r.JackknifeBy = "nope" }, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Resample: "jackknife"}
			c.mut(r)
			env := predictFromBytes(jackknifeCohort(t, 10), &types.Request{Regressions: []*types.RegressionSpec{r}}, nil)
			if len(env.Errors) == 0 {
				t.Fatal("predict accepted the spec")
			}
			if c.code == "" {
				if !strings.Contains(env.Errors[0].Message, "jackknife_by") {
					t.Errorf("unknown field refusal %+v does not name jackknife_by", env.Errors[0])
				}
				return
			}
			want := regresample.Check(r)
			if env.Errors[0].Code != string(c.code) || env.Errors[0].Message != want.Message {
				t.Errorf("predict %+v, shared rule %v", env.Errors[0], want)
			}
		})
	}
}
