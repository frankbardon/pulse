package pulse_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// sem_invariance_test.go is MAT_SEM's multi-group equality set
// (params.group_equal / group_partial, lavaan's group.equal /
// group.partial) and per-group modifiers c(...)*x end to end: the
// invariance ladder and the modifier fits against lavaan, the
// refusals (config-time, predict == runtime), and a compose sweep over
// group_equal reproducing lavaan's configural / metric / scalar ladder.

var invarianceCases = []struct{ ref, csv, model string }{
	{"brand_mg_metric", "brand", "brand_ms"},
	{"brand_mg_scalar", "brand", "brand_ms"},
	{"brand_mg_partial", "brand", "brand_ms"},
	{"brand_mg_modifiers", "brand_uneq", "brand_mgmod"},
	{"brand_mg_strict", "brand", "brand_mgmod"},
}

// equalityParams are a lavaan reference's group.equal / group.partial
// as MAT_SEM params (as lists).
func equalityParams(ref mgLavaan) map[string]any {
	out := map[string]any{}
	if ref.Options.GroupEqual != nil {
		out["group_equal"] = ref.Options.GroupEqual
	}
	if ref.Options.GroupPartial != nil {
		out["group_partial"] = ref.Options.GroupPartial
	}
	return out
}

// TestSEM_InvarianceProcessMatchesLavaan: metric, scalar, partial
// scalar, per-group modifiers and the full equality set match lavaan's
// group.equal / group.partial fits through the engine, on a
// categorical and a packed_bool wave (the modifiers read the level
// index either way).
func TestSEM_InvarianceProcessMatchesLavaan(t *testing.T) {
	for _, c := range invarianceCases {
		for _, wave := range []encoding.FieldType{encoding.FieldTypeCategoricalU8, encoding.FieldTypePackedBool} {
			t.Run(c.ref+"/"+wave.String(), func(t *testing.T) {
				ref := loadMGLavaan(t, c.ref)
				if wave == encoding.FieldTypePackedBool {
					ref.Labels = []string{"0", "1"}
					for i := range ref.Params {
						ref.Params[i].Group = map[string]string{"1": "0", "2": "1"}[ref.Params[i].Group]
					}
				}
				p, _ := brandGroupCohort{csv: c.csv, wave: wave}.build(t, pulse.Options{})
				checkMGAgainstLavaan(t, semGroupRun(t, p, semGroupRequest(t, c.model, equalityParams(ref))), ref, 0)
			})
		}
	}
	// The comma-separated string form decodes to the same fit.
	ref := loadMGLavaan(t, "brand_mg_partial")
	p, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	checkMGAgainstLavaan(t, semGroupRun(t, p, semGroupRequest(t, "brand_ms", map[string]any{
		"group_equal": "loadings, intercepts", "group_partial": strings.Join(ref.Options.GroupPartial, ","),
	})), ref, 0)
}

// TestSEM_InvarianceModifierLevels: a per-group modifier's entries bind
// to the group field's level INDEX — a dictionary level without rows
// keeps its position (and is no group of the fit) — so a three-entry
// c(...) over the dictionary [0, 1, 2], level 0 empty, fits exactly
// lavaan's two-group modifier fit over levels 1 and 2.
func TestSEM_InvarianceModifierLevels(t *testing.T) {
	ref := loadMGLavaan(t, "brand_mg_modifiers")
	desc, err := os.ReadFile(semGroupTestdata + "brand_mgmod_model.txt")
	if err != nil {
		t.Fatal(err)
	}
	three := strings.NewReplacer("c(lv2, lv2)", "c(z, lv2, lv2)", "c(0.8, NA)", "c(0.5, 0.8, NA)", "c(b1, b2)", "c(b0, b1, b2)").Replace(string(desc))
	if three == string(desc) {
		t.Fatal("the modifiers were not rewritten")
	}
	params := equalityParams(ref)
	params["model"] = three
	p, _ := brandGroupCohort{csv: "brand_uneq", wave: encoding.FieldTypeCategoricalU8, dict: []string{"0", "1", "2"}}.build(t, pulse.Options{})
	checkMGAgainstLavaan(t, semGroupRun(t, p, semGroupRequest(t, "brand_mgmod", params)), ref, 0)
}

// TestSEM_InvarianceReferenceLevel: group_reference is the
// identification reference too — under equal intercepts its latent
// means stay 0 and the other level's are freed.
func TestSEM_InvarianceReferenceLevel(t *testing.T) {
	p, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	res := semGroupRun(t, p, semGroupRequest(t, "brand_ms", map[string]any{"group_equal": []string{"loadings", "intercepts"}, "group_reference": "2"}))
	if res.Error != nil {
		t.Fatal(res.Error)
	}
	est := res.Auxiliary["estimates"]
	free, _ := res.Vectors["free"].([]bool)
	for i, k := range est.RowKeys {
		for _, f := range []string{"quality", "value", "service"} {
			switch k {
			case "[1] " + f + " ~1":
				if !free[i] {
					t.Errorf("%s: want free outside the reference level", k)
				}
			case "[2] " + f + " ~1":
				if free[i] || est.Values[i][0] != 0 {
					t.Errorf("%s: free %v value %g, want fixed 0 in the reference level", k, free[i], est.Values[i][0])
				}
			}
		}
	}
}

// TestSEM_InvarianceProcessRefusals: an unknown or reserved group_equal value,
// a group_partial row the model lacks, equality knobs without a group,
// per-group modifiers whose length is not the level count or on an
// integer group field — each a SERVICE_VALIDATION bad_params naming
// the knob, predict refusing identically.
func TestSEM_InvarianceProcessRefusals(t *testing.T) {
	ctx := context.Background()
	modifiers := "quality =~ q1 + q2 + q3\nvalue =~ v1 + c(a, b, c)*v2 + v3\nservice =~ s1 + s2 + s3\nrecommend ~ quality + value + service"
	for _, c := range []struct {
		name   string
		wave   encoding.FieldType
		params map[string]any
		want   map[string]any
	}{
		{"unknown group_equal", encoding.FieldTypeCategoricalU8, map[string]any{"group_equal": []string{"loadings", "weights"}},
			map[string]any{"param": "group_equal", "value": "weights"}},
		{"thresholds without an ordinal estimator", encoding.FieldTypeCategoricalU8, map[string]any{"group_equal": "thresholds"},
			map[string]any{"param": "group_equal", "value": "thresholds"}},
		{"absent group_partial row", encoding.FieldTypeCategoricalU8, map[string]any{"group_equal": []string{"loadings"}, "group_partial": []string{"quality=~v1"}},
			map[string]any{"param": "group_partial", "value": "quality=~v1"}},
		{"group_equal without group", encoding.FieldTypeCategoricalU8, map[string]any{"group": nil, "group_equal": []string{"loadings"}},
			map[string]any{"param": "group_equal"}},
		{"group_partial without group", encoding.FieldTypeCategoricalU8, map[string]any{"group": nil, "group_partial": []string{"v2~1"}},
			map[string]any{"param": "group_partial"}},
		{"modifier arity", encoding.FieldTypeCategoricalU8, map[string]any{"model": modifiers},
			map[string]any{"param": "model", "arity": 3, "groups": 2}},
		{"modifiers on an integer group", encoding.FieldTypeU8, map[string]any{"model": strings.Replace(modifiers, "c(a, b, c)", "c(a, b)", 1)},
			map[string]any{"param": "model", "field_type": "u8"}},
		{"modifiers without a group", encoding.FieldTypeCategoricalU8, map[string]any{"group": nil, "model": strings.Replace(modifiers, "c(a, b, c)", "c(a, b)", 1)},
			map[string]any{"param": "model"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, fsys := brandGroupCohort{csv: "brand_uneq", wave: c.wave}.build(t, pulse.Options{})
			req := func() *types.Request { return semGroupRequest(t, "brand_ms", c.params) }
			_, rerr := p.Process(ctx, req())
			var ce *errors.CodedError
			if !asCoded(rerr, &ce) || ce.Code != errors.SERVICE_VALIDATION || ce.Details["reason"] != "bad_params" {
				t.Fatalf("runtime error %v (details %v), want SERVICE_VALIDATION bad_params", rerr, semDetailsOf(ce))
			}
			for k, v := range c.want {
				if ce.Details[k] != v {
					t.Errorf("details %s = %v, want %v (details %v)", k, ce.Details[k], v, ce.Details)
				}
			}
			// A group_equal value is judged against the vocabulary,
			// which the refusal lists.
			if c.want["param"] == "group_equal" && c.want["value"] != nil && ce.Details["valid"] == nil {
				t.Errorf("details %v: want the valid group_equal values", ce.Details)
			}
			env := predictEnvelope(t, p, fsys, "brand.pulse", req())
			sameEntry(t, env, rerr)
			if env.Data.(*descriptor.PredictResult).Valid {
				t.Fatal("predict Valid=true")
			}
		})
	}
}

// TestSEM_InvarianceComposeSweep: a compose sweep over group_equal (a
// zip of the step name and its comma-separated equality set) yields the
// configural / metric / scalar ladder, each slot's fit indices
// lavaan's, so the comparison reads as lavaan's: dof rises step by
// step and the chi-square differences are lavaan's.
func TestSEM_InvarianceComposeSweep(t *testing.T) {
	p, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	body, err := json.Marshal(semGroupRequest(t, "brand_ms", map[string]any{"group_equal": "{{eq}}"}))
	if err != nil {
		t.Fatal(err)
	}
	creq := &pulse.ComposedRequest{Sweep: &types.SweepSpec{
		Axes: []types.SweepAxis{
			{Name: "step", Values: []any{"configural", "metric", "scalar"}},
			{Name: "eq", Values: []any{"", "loadings", "loadings,intercepts"}},
		},
		Mode:    types.SweepModeZip,
		Label:   "{{step}}",
		Request: body,
	}}
	resp, err := p.Compose(context.Background(), creq)
	if err != nil {
		t.Fatal(err)
	}
	steps := []string{"configural", "metric", "scalar"}
	if len(resp.Responses) != len(steps) {
		t.Fatalf("%d responses", len(resp.Responses))
	}
	var chi2, dof []float64
	for i, step := range steps {
		r := resp.Responses[i]
		if r.Label != step || len(r.Matrices) != 1 {
			t.Fatalf("slot %d: label %q, %d matrices", i, r.Label, len(r.Matrices))
		}
		ref := loadMGLavaan(t, "brand_mg_"+step)
		checkMGAgainstLavaan(t, r.Matrices[0], ref, 0)
		chi2, dof = append(chi2, *ref.Fit["chisq"]), append(dof, *ref.Fit["df"])
		if i == 0 {
			continue
		}
		sc, prev := r.Matrices[0].Scalars, resp.Responses[i-1].Matrices[0].Scalars
		if d := sc["dof"] - prev["dof"]; d != dof[i]-dof[i-1] || d <= 0 {
			t.Errorf("%s: dof step %g, lavaan %g", step, d, dof[i]-dof[i-1])
		}
		if d, w := sc["chi2"]-prev["chi2"], chi2[i]-chi2[i-1]; !(mgRel(d, w) <= 1e-2) {
			t.Errorf("%s: chi-square step %g, lavaan %g", step, d, w)
		}
	}
}
