package descriptor

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// nullableFormulaCohort: x non-nullable, n and m nullable f64 columns.
func nullableFormulaCohort(t *testing.T) []byte {
	t.Helper()
	return buildTestPulseFile(t, &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64, Description: "Measured score per respondent"},
		{Name: "n", Type: encoding.FieldTypeF64, Nullable: true, Description: "Optional follow-up score"},
		{Name: "m", Type: encoding.FieldTypeF64, Nullable: true, Description: "Optional second follow-up score"},
	}})
}

func formulaNullRequest(exprs ...string) *types.Request {
	req := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x"}}}
	for i, e := range exprs {
		req.Attributes = append(req.Attributes, &types.Attribute{Type: types.ATTR_FORMULA, Expression: e, Label: "f" + string(rune('0'+i))})
	}
	return req
}

// TestPredict_AdvisoryFormulaNullableResult: one
// PULSE_ADVISORY_FORMULA_NULLABLE_RESULT per ATTR_FORMULA whose result
// can be nil — an unguarded nullable field read or a literal nil in
// result position — naming the slot, label, fields and literal_nil; a
// guarded read (`??`, `== nil` / `!= nil` ternary), a non-nullable read,
// a null fed to an operator and a hidden ATTR_FORMULA never fire.
func TestPredict_AdvisoryFormulaNullableResult(t *testing.T) {
	t.Parallel()

	code := string(errors.PULSE_ADVISORY_FORMULA_NULLABLE_RESULT)
	type want struct {
		slot, label string
		fields      []string
		literal     bool
	}
	cases := []struct {
		name  string
		exprs []string
		inst  *InstanceSnapshot
		want  []want
	}{
		{"bare nullable field", []string{"n"}, nil, []want{{"attributes[0]", "f0", []string{"n"}, false}}},
		{"literal nil branch", []string{"x > 0 ? x : nil"}, nil, []want{{"attributes[0]", "f0", []string{}, true}}},
		{"both sources", []string{"x > 0 ? m : nil"}, nil, []want{{"attributes[0]", "f0", []string{"m"}, true}}},
		{"coalesce of two nullables", []string{"x * 2", "n ?? m"}, nil, []want{{"attributes[1]", "f1", []string{"n", "m"}, false}}},
		{"coalesce guard", []string{"n ?? 0"}, nil, nil},
		{"== nil guard", []string{"n == nil ? 0 : n"}, nil, nil},
		{"!= nil guard", []string{"n != nil ? n : 0"}, nil, nil},
		{"non-nullable read", []string{"x"}, nil, nil},
		{"null under an operator", []string{"n * 2"}, nil, nil},
		{"hidden ATTR_FORMULA", []string{"n"}, scopedExcept("ATTR_FORMULA"), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []descriptor.Advisory
			res := predictFromBytes(nullableFormulaCohort(t), formulaNullRequest(c.exprs...), &PredictOptions{Instance: c.inst}).Data.(*descriptor.PredictResult)
			for _, a := range res.Advisories {
				if a.Code == code {
					got = append(got, a)
				}
			}
			if len(got) != len(c.want) {
				t.Fatalf("advisories = %+v, want %d %s", got, len(c.want), code)
			}
			for i, w := range c.want {
				a := got[i]
				wantDetails := map[string]any{"slot": w.slot, "operator": "ATTR_FORMULA", "label": w.label,
					"fields": w.fields, "literal_nil": w.literal}
				if !reflect.DeepEqual(a.Details, wantDetails) {
					t.Errorf("advisory %d details = %v, want %v", i, a.Details, wantDetails)
				}
				if !strings.Contains(a.Message, w.slot) || !strings.Contains(a.Message, w.label) {
					t.Errorf("advisory %d message %q does not name %s (%s)", i, a.Message, w.slot, w.label)
				}
				for _, f := range w.fields {
					if !strings.Contains(a.Message, f+" ?? 0") {
						t.Errorf("advisory %d message %q does not suggest guarding %s", i, a.Message, f)
					}
				}
				if hits := LintGuidanceText(a.Message); len(hits) > 0 {
					t.Errorf("advisory %d lint hits %+v", i, hits)
				}
			}
		})
	}
}

// TestPredict_AdvisoryFormulaNullableResultNeverEscalated: strict
// predict keeps the advisory in its slot (valid, no error, no warning);
// suppressing the code drops it and the predict JSON then equals a
// guarded formula's, carrying no "advisories" key.
func TestPredict_AdvisoryFormulaNullableResultNeverEscalated(t *testing.T) {
	t.Parallel()

	code := string(errors.PULSE_ADVISORY_FORMULA_NULLABLE_RESULT)
	req := formulaNullRequest("n")
	env := predictFromBytes(nullableFormulaCohort(t), req, &PredictOptions{Strict: true})
	res := env.Data.(*descriptor.PredictResult)
	if len(res.Advisories) != 1 || res.Advisories[0].Code != code {
		t.Fatalf("vacuous: advisories = %+v", res.Advisories)
	}
	if !res.Valid || len(env.Errors) != 0 || len(env.Warnings) != 0 {
		t.Errorf("strict: valid=%v errors=%v warnings=%v, want a clean valid result", res.Valid, env.Errors, env.Warnings)
	}
	suppressed := &PredictOptions{Instance: (*InstanceSnapshot)(nil).WithSuppressedAdvisories([]string{code})}
	b, err := json.Marshal(predictFromBytes(nullableFormulaCohort(t), req, suppressed))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"advisories"`) {
		t.Errorf("suppressed predict carries the key: %s", b)
	}
}

// TestComposePredict_FormulaNullableResultAttributed: a Compose slot's
// nullable formula raises the advisory attributed to that slot.
func TestComposePredict_FormulaNullableResultAttributed(t *testing.T) {
	t.Parallel()

	slot := formulaNullRequest("x > 0 ? x : nil")
	slot.Cohort = &types.Cohort{Filename: "a.pulse"}
	req := &types.ComposedRequest{Requests: []*types.Request{
		{Cohort: &types.Cohort{Filename: "a.pulse"}, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x"}}},
		slot,
	}}
	opts := composeAdvisoryOpts(t)
	opts.SidecarLoader = nil
	res := composeResult(t, req, opts)
	if len(res.Advisories) != 1 {
		t.Fatalf("advisories = %+v, want one", res.Advisories)
	}
	a := res.Advisories[0]
	if a.Code != string(errors.PULSE_ADVISORY_FORMULA_NULLABLE_RESULT) || a.Details["request"] != 1 || a.Details["slot"] != "requests[1].attributes[0]" {
		t.Errorf("advisory = %+v, want FORMULA_NULLABLE_RESULT on request 1 at requests[1].attributes[0]", a)
	}
}
