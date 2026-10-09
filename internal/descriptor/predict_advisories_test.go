package descriptor

import (
	"encoding/json"
	stderrors "errors"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// advisoryCohort: x numeric, g a 4-entry categorical, h a 3-entry
// categorical, d a 2-entry categorical, b a packed_bool. Every field is
// described, so strict predict raises nothing of its own.
func advisoryCohort(t *testing.T) []byte {
	t.Helper()
	return buildTestPulseFile(t, &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64, Description: "Measured score per respondent"},
		{Name: "g", Type: encoding.FieldTypeCategoricalU8, Description: "Region of the respondent", Dictionary: makeDictionary(t, "n", "s", "e", "w")},
		{Name: "h", Type: encoding.FieldTypeCategoricalU8, Description: "Answer to the survey question", Dictionary: makeDictionary(t, "yes", "no", "maybe")},
		{Name: "d", Type: encoding.FieldTypeCategoricalU8, Description: "Arm the respondent was assigned to", Dictionary: makeDictionary(t, "control", "treatment")},
		{Name: "b", Type: encoding.FieldTypePackedBool, Description: "Whether the respondent opted in"},
	}})
}

func predictAdvisories(t *testing.T, req *types.Request, opts *PredictOptions) []descriptor.Advisory {
	t.Helper()
	return predictFromBytes(advisoryCohort(t), req, opts).Data.(*descriptor.PredictResult).Advisories
}

func oneSampleTests(n int) []*types.Test {
	out := make([]*types.Test, n)
	for i := range out {
		out[i] = &types.Test{Type: types.TEST_T, Field: "x"}
	}
	return out
}

// TestPredict_AdvisoryTwoGroup: a TEST_T / TEST_WELCH whose split_by
// resolves to more than two dictionary groups fires once per slot,
// suggesting Welch's ANOVA; two groups, a packed_bool, no split and a
// many-group test never fire.
func TestPredict_AdvisoryTwoGroup(t *testing.T) {
	code := string(errors.PULSE_ADVISORY_TWO_GROUP_TEST_MANY_GROUPS)
	cases := []struct {
		name  string
		req   *types.Request
		slots []string
		want  []int // groups per firing slot
	}{
		{"TEST_T over a 4-group split", &types.Request{Tests: []*types.Test{{Type: types.TEST_T, Field: "x", SplitBy: "g"}}},
			[]string{"tests[0]"}, []int{4}},
		{"TEST_WELCH over a 3-group split as a post-test", &types.Request{PostTests: []*types.Test{{Type: types.TEST_WELCH, Field: "x", SplitBy: "h"}}},
			[]string{"post_tests[0]"}, []int{3}},
		{"exactly two groups", &types.Request{Tests: []*types.Test{{Type: types.TEST_T, Field: "x", SplitBy: "d"}}}, nil, nil},
		{"packed_bool split", &types.Request{Tests: []*types.Test{{Type: types.TEST_WELCH, Field: "x", SplitBy: "b"}}}, nil, nil},
		{"one-sample", &types.Request{Tests: []*types.Test{{Type: types.TEST_T, Field: "x"}}}, nil, nil},
		{"many-group test", &types.Request{Tests: []*types.Test{{Type: types.TEST_ANOVA_WELCH, Field: "x", SplitBy: "g"}}}, nil, nil},
		{"one slot of three fires", &types.Request{Tests: []*types.Test{
			{Type: types.TEST_T, Field: "x", SplitBy: "d"},
			{Type: types.TEST_ANOVA_F, Field: "x", SplitBy: "g"},
			{Type: types.TEST_WELCH, Field: "x", SplitBy: "g"},
		}}, []string{"tests[2]"}, []int{4}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := predictAdvisories(t, c.req, nil)
			if len(got) != len(c.slots) {
				t.Fatalf("advisories = %+v, want %d", got, len(c.slots))
			}
			for i, a := range got {
				if a.Code != code || a.Details["slot"] != c.slots[i] || a.Details["groups"] != c.want[i] {
					t.Errorf("advisory %d = %+v, want %s at %s with %d groups", i, a, code, c.slots[i], c.want[i])
				}
				if a.Details["suggested"] != string(types.TEST_ANOVA_WELCH) || !strings.Contains(a.Message, string(types.TEST_ANOVA_WELCH)) {
					t.Errorf("advisory %d does not suggest TEST_ANOVA_WELCH: %+v", i, a)
				}
			}
		})
	}
}

// TestPredict_AdvisoryTwoGroupNeverProposesHidden: the suggested ANOVA
// is one the instance offers — Welch's hidden falls back to the classic
// F, both hidden suggests nothing and names neither; a hidden TEST_T is
// a never-registered test and fires nothing.
func TestPredict_AdvisoryTwoGroupNeverProposesHidden(t *testing.T) {
	req := &types.Request{Tests: []*types.Test{{Type: types.TEST_T, Field: "x", SplitBy: "g"}}}
	for _, c := range []struct {
		name    string
		hidden  []string
		want    any // details.suggested; nil = absent
		fires   bool
		unnamed []types.TestType
	}{
		{"unscoped", nil, string(types.TEST_ANOVA_WELCH), true, nil},
		{"Welch ANOVA hidden", []string{"TEST_ANOVA_WELCH"}, string(types.TEST_ANOVA_F), true, []types.TestType{types.TEST_ANOVA_WELCH}},
		{"both ANOVAs hidden", []string{"TEST_ANOVA_WELCH", "TEST_ANOVA_F"}, nil, true, []types.TestType{types.TEST_ANOVA_WELCH, types.TEST_ANOVA_F}},
		{"TEST_T hidden", []string{"TEST_T"}, nil, false, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			opts := &PredictOptions{}
			if c.hidden != nil {
				opts.Instance = hideOnly(c.hidden...)
			}
			got := predictAdvisories(t, req, opts)
			if !c.fires {
				if len(got) != 0 {
					t.Fatalf("advisories = %+v, want none", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("advisories = %+v, want one", got)
			}
			if s, ok := got[0].Details["suggested"]; c.want == nil && ok || c.want != nil && s != c.want {
				t.Errorf("suggested = %v (present %v), want %v", s, ok, c.want)
			}
			body, _ := json.Marshal(got[0])
			for _, h := range c.unnamed {
				if strings.Contains(string(body), string(h)) {
					t.Errorf("advisory names hidden %s: %s", h, body)
				}
			}
		})
	}
}

// TestPredict_AdvisoryManyTests: fires at the threshold with no
// multiplicity block anywhere; any block (none included, the instance
// default included) or one p-value fewer silences it; the message says
// "at least" / "an estimated" off an inexact basis.
func TestPredict_AdvisoryManyTests(t *testing.T) {
	code := string(errors.PULSE_ADVISORY_MANY_TESTS)
	n := descriptor.MultiplicityTriggerThreshold
	withMult := func(m *types.Multiplicity) *types.Request {
		return &types.Request{Multiplicity: m, Tests: oneSampleTests(n)}
	}
	crosstabbed := func(rows string) *types.Request {
		return &types.Request{
			Crosstab: pvCrosstab(rows, "h"),
			Tests:    oneSampleTests(n),
			Overlays: []types.OverlaySpec{pvOverlay(types.OverlayKindPairwisePropZ, types.OverlayScopeRow, nil)},
		}
	}
	for _, c := range []struct {
		name   string
		req    *types.Request
		def    *types.Multiplicity
		fires  bool
		phrase string
	}{
		{"at the threshold", withMult(nil), nil, true, "emits 10 uncorrected"},
		{"one below", &types.Request{Tests: oneSampleTests(n - 1)}, nil, false, ""},
		{"request block", withMult(mult(mHolm, "", 0)), nil, false, ""},
		{"explicit none", withMult(mult(mNone, "", 0)), nil, false, ""},
		{"instance default", withMult(nil), mult(mHolm, "", 0), false, ""},
		{"lower-bound basis", crosstabbed("x"), nil, true, "at least 11 uncorrected"},
		{"dictionary basis", crosstabbed("g"), nil, true, "an estimated 28 uncorrected"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := predictAdvisories(t, c.req, &PredictOptions{DefaultMultiplicity: c.def})
			if !c.fires {
				if len(got) != 0 {
					t.Fatalf("advisories = %+v, want none", got)
				}
				return
			}
			if len(got) != 1 || got[0].Code != code {
				t.Fatalf("advisories = %+v, want one %s", got, code)
			}
			if !strings.Contains(got[0].Message, c.phrase) {
				t.Errorf("message %q lacks %q", got[0].Message, c.phrase)
			}
			want := map[string]any{"multiplicity": map[string]any{"method": "holm"}}
			if !reflect.DeepEqual(got[0].Details["suggested"], want) {
				t.Errorf("suggested = %v, want %v", got[0].Details["suggested"], want)
			}
		})
	}
}

// TestPredict_AdvisoryManyTestsFollowsInstance: a hidden
// capability:multiplicity never proposes a multiplicity block.
func TestPredict_AdvisoryManyTestsFollowsInstance(t *testing.T) {
	req := &types.Request{Tests: oneSampleTests(descriptor.MultiplicityTriggerThreshold)}
	if len(predictAdvisories(t, req, nil)) != 1 {
		t.Fatal("vacuous: the unscoped instance fires no advisory")
	}
	if got := predictAdvisories(t, req, &PredictOptions{Instance: scopedExcept(featMultiplicity)}); len(got) != 0 {
		t.Errorf("advisories = %+v with capability:multiplicity hidden, want none", got)
	}
}

// TestPredict_AdvisoriesSuppressed: an instance suppressing one code
// drops exactly that code.
func TestPredict_AdvisoriesSuppressed(t *testing.T) {
	req := &types.Request{Tests: append(oneSampleTests(descriptor.MultiplicityTriggerThreshold),
		&types.Test{Type: types.TEST_T, Field: "x", SplitBy: "g"})}
	codes := func(inst *InstanceSnapshot) []string {
		var out []string
		for _, a := range predictAdvisories(t, req, &PredictOptions{Instance: inst}) {
			out = append(out, a.Code)
		}
		return out
	}
	two, many := string(errors.PULSE_ADVISORY_TWO_GROUP_TEST_MANY_GROUPS), string(errors.PULSE_ADVISORY_MANY_TESTS)
	if got := codes(nil); !reflect.DeepEqual(got, []string{two, many}) {
		t.Fatalf("unsuppressed = %v, want [%s %s]", got, two, many)
	}
	if got := codes((*InstanceSnapshot)(nil).WithSuppressedAdvisories([]string{two})); !reflect.DeepEqual(got, []string{many}) {
		t.Errorf("suppressing %s = %v, want [%s]", two, got, many)
	}
}

// TestPredict_StrictNeverEscalatesAdvisories: strict predict keeps
// every advisory in its slot — no error, no warning, still valid.
func TestPredict_StrictNeverEscalatesAdvisories(t *testing.T) {
	req := &types.Request{Tests: append(oneSampleTests(descriptor.MultiplicityTriggerThreshold),
		&types.Test{Type: types.TEST_T, Field: "x", SplitBy: "g"})}
	env := predictFromBytes(advisoryCohort(t), req, &PredictOptions{Strict: true})
	res := env.Data.(*descriptor.PredictResult)
	if len(res.Advisories) != 2 {
		t.Fatalf("vacuous: advisories = %+v", res.Advisories)
	}
	if !res.Valid || len(env.Errors) != 0 || len(env.Warnings) != 0 {
		t.Errorf("strict: valid=%v errors=%v warnings=%v, want a clean valid result", res.Valid, env.Errors, env.Warnings)
	}
}

// TestPredict_AdvisoryFreeOutputOmitsTheKey: a request no rule fires on
// carries no "advisories" key, so its predict JSON is unchanged.
func TestPredict_AdvisoryFreeOutputOmitsTheKey(t *testing.T) {
	env := predictFromBytes(advisoryCohort(t), &types.Request{Tests: []*types.Test{{Type: types.TEST_T, Field: "x", SplitBy: "d"}}}, nil)
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"advisories"`) {
		t.Errorf("advisory-free predict carries the key: %s", b)
	}
}

// TestAdvisoryMessagesPassProseLint: every advisory message reads as
// guidance prose (LintGuidanceText) on each basis it can take.
func TestAdvisoryMessagesPassProseLint(t *testing.T) {
	reqs := []*types.Request{
		{Tests: append(oneSampleTests(descriptor.MultiplicityTriggerThreshold), &types.Test{Type: types.TEST_T, Field: "x", SplitBy: "g"})},
		{Crosstab: pvCrosstab("x", "h"), Tests: oneSampleTests(10), Overlays: []types.OverlaySpec{pvOverlay(types.OverlayKindPairwisePropZ, types.OverlayScopeRow, nil)}},
		{Crosstab: pvCrosstab("g", "h"), Tests: oneSampleTests(10), Overlays: []types.OverlaySpec{pvOverlay(types.OverlayKindPairwisePropZ, types.OverlayScopeRow, nil)}},
	}
	seen := 0
	for _, req := range reqs {
		for _, a := range predictAdvisories(t, req, nil) {
			seen++
			if hits := LintGuidanceText(a.Message); len(hits) > 0 {
				t.Errorf("%s message %q: lint hits %+v", a.Code, a.Message, hits)
			}
		}
	}
	if seen < 4 {
		t.Errorf("vacuous: only %d advisories linted", seen)
	}
}

// TestAdvisoryCodesRegistered: the advisory registry and the error
// catalog agree — every advisory code is a registered PULSE_ADVISORY_*
// code and every registered PULSE_ADVISORY_* code is an advisory.
func TestAdvisoryCodesRegistered(t *testing.T) {
	all := map[string]bool{}
	for _, c := range errors.AllCodes() {
		all[string(c)] = true
	}
	adv := map[string]bool{}
	for _, c := range AdvisoryCodes() {
		adv[c] = true
		if !all[c] || !strings.HasPrefix(c, "PULSE_ADVISORY_") {
			t.Errorf("advisory code %s is not a registered PULSE_ADVISORY_* code", c)
		}
	}
	for c := range all {
		if strings.HasPrefix(c, "PULSE_ADVISORY_") && !adv[c] {
			t.Errorf("registered %s is missing from the advisory registry", c)
		}
	}
}

// TestValidateSuppressAdvisories: registered codes pass; anything else
// is PULSE_SUPPRESS_ADVISORY_UNKNOWN naming the code and the valid set.
func TestValidateSuppressAdvisories(t *testing.T) {
	if err := ValidateSuppressAdvisories(nil); err != nil {
		t.Errorf("nil: %v", err)
	}
	if err := ValidateSuppressAdvisories(AdvisoryCodes()); err != nil {
		t.Errorf("every advisory code: %v", err)
	}
	for _, bad := range []string{"PULSE_ADVISORY_NOPE", string(errors.PULSE_LIMIT_EXCEEDED), ""} {
		err := ValidateSuppressAdvisories([]string{string(errors.PULSE_ADVISORY_MANY_TESTS), bad})
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_SUPPRESS_ADVISORY_UNKNOWN || ce.Details["code"] != bad {
			t.Errorf("%q: err = %v, want %s naming it", bad, err, errors.PULSE_SUPPRESS_ADVISORY_UNKNOWN)
		}
	}
}
