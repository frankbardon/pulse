package descriptor

import (
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestPredict_AdvisoryExtraPass: one PULSE_ADVISORY_EXTRA_PASS per
// ATTR_FORMULA calling total(expr), in declared order, naming the slot,
// the label, the distinct total() arguments and the one scan it adds; a
// plain formula, another two-pass attribute and a hidden ATTR_FORMULA
// never fire.
func TestPredict_AdvisoryExtraPass(t *testing.T) {
	code := string(errors.PULSE_ADVISORY_EXTRA_PASS)
	formula := func(expr, label string) *types.Attribute {
		return &types.Attribute{Type: types.ATTR_FORMULA, Expression: expr, Label: label}
	}
	type want struct {
		slot, label string
		totals      int
	}
	cases := []struct {
		name  string
		attrs []*types.Attribute
		inst  *InstanceSnapshot
		want  []want
	}{
		{"lone total formula", []*types.Attribute{formula("x / total(x)", "share")}, nil,
			[]want{{"attributes[0]", "share", 1}}},
		{"two distinct arguments, one repeated", []*types.Attribute{formula("x / total(x) + x / total(x * 2) - total(x)", "mix")}, nil,
			[]want{{"attributes[0]", "mix", 2}}},
		{"two total formulas around a plain one", []*types.Attribute{
			formula("total(x)", "t"), formula("x * 2", "plain"), formula("plain / total(plain)", "s2"),
		}, nil, []want{{"attributes[0]", "t", 1}, {"attributes[2]", "s2", 1}}},
		{"plain formula", []*types.Attribute{formula("x / 2", "half")}, nil, nil},
		{"z-score", []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "x", Label: "z"}}, nil, nil},
		{"hidden ATTR_FORMULA", []*types.Attribute{formula("x / total(x)", "share")}, scopedExcept("ATTR_FORMULA"), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x"}}, Attributes: c.attrs}
			var got []descriptor.Advisory
			for _, a := range predictAdvisories(t, req, &PredictOptions{Instance: c.inst}) {
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
					"totals": w.totals, "added_scans": 1}
				if !reflect.DeepEqual(a.Details, wantDetails) {
					t.Errorf("advisory %d details = %v, want %v", i, a.Details, wantDetails)
				}
				if !strings.Contains(a.Message, w.slot) || !strings.Contains(a.Message, w.label) {
					t.Errorf("advisory %d message %q does not name %s (%s)", i, a.Message, w.slot, w.label)
				}
				if hits := LintGuidanceText(a.Message); len(hits) > 0 {
					t.Errorf("advisory %d lint hits %+v", i, hits)
				}
			}
		})
	}
}

// TestPredict_AdvisoryExtraPassNeverEscalated: strict predict keeps the
// advisory in its slot (valid, no error, no warning); suppressing the
// code drops it; a grouped total formula still fires and predicts
// buffered with the two-pass reason.
func TestPredict_AdvisoryExtraPassNeverEscalated(t *testing.T) {
	code := string(errors.PULSE_ADVISORY_EXTRA_PASS)
	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "share"}},
		Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "x / total(x)", Label: "share"}},
	}
	env := predictFromBytes(advisoryCohort(t), req, &PredictOptions{Strict: true})
	res := env.Data.(*descriptor.PredictResult)
	if len(res.Advisories) != 1 || res.Advisories[0].Code != code {
		t.Fatalf("vacuous: advisories = %+v", res.Advisories)
	}
	if !res.Valid || len(env.Errors) != 0 || len(env.Warnings) != 0 || !res.Streamable {
		t.Errorf("strict: valid=%v errors=%v warnings=%v streamable=%v, want a clean valid streamable result",
			res.Valid, env.Errors, env.Warnings, res.Streamable)
	}
	if got := predictAdvisories(t, req, &PredictOptions{Instance: (*InstanceSnapshot)(nil).WithSuppressedAdvisories([]string{code})}); len(got) != 0 {
		t.Errorf("suppressed: advisories = %+v, want none", got)
	}

	grouped := *req
	grouped.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}
	res = predictFromBytes(advisoryCohort(t), &grouped, nil).Data.(*descriptor.PredictResult)
	if len(res.Advisories) != 1 || res.Advisories[0].Code != code {
		t.Errorf("grouped: advisories = %+v, want one %s", res.Advisories, code)
	}
	if res.Streamable || !slicesContainsSubstring(res.StreamableReasons, "two-pass attribute ATTR_FORMULA with groupers or features") {
		t.Errorf("grouped: streamable=%v reasons=%v, want buffered with the two-pass reason", res.Streamable, res.StreamableReasons)
	}
}

// TestComposePredict_ExtraPassAttributed: a Compose slot's total formula
// raises the advisory attributed to that slot.
func TestComposePredict_ExtraPassAttributed(t *testing.T) {
	req := &types.ComposedRequest{Requests: []*types.Request{
		{Cohort: &types.Cohort{Filename: "a.pulse"}, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x"}}},
		{Cohort: &types.Cohort{Filename: "a.pulse"}, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "s"}},
			Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "x / total(x)", Label: "s"}}},
	}}
	opts := composeAdvisoryOpts(t)
	opts.SidecarLoader = nil
	res := composeResult(t, req, opts)
	if len(res.Advisories) != 1 {
		t.Fatalf("advisories = %+v, want one", res.Advisories)
	}
	a := res.Advisories[0]
	if a.Code != string(errors.PULSE_ADVISORY_EXTRA_PASS) || a.Details["request"] != 1 || a.Details["slot"] != "requests[1].attributes[0]" {
		t.Errorf("advisory = %+v, want EXTRA_PASS on request 1 at requests[1].attributes[0]", a)
	}
}

func slicesContainsSubstring(ss []string, sub string) bool {
	for _, s := range ss {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
