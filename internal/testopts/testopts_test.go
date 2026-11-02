package testopts

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestParse_DefaultsAndRefusals: absent, null and non-object params
// read as the defaults (the constructor's decode judges a non-object);
// each option reads on its own test only; each misuse is
// PROCESSING_CONFIG naming the param.
func TestParse_DefaultsAndRefusals(t *testing.T) {
	t.Parallel()

	spec := func(op types.TestType, split, params string) *types.Test {
		s := &types.Test{Type: op, SplitBy: split}
		if params != "" {
			s.Params = json.RawMessage(params)
		}
		return s
	}
	for _, c := range []struct {
		name string
		t    *types.Test
		want Options
	}{
		{"absent", spec(types.TEST_T, "g", ""), Options{}},
		{"null", spec(types.TEST_T, "g", "null"), Options{}},
		{"not an object", spec(types.TEST_T, "g", "[1]"), Options{}},
		{"pooled", spec(types.TEST_T, "g", `{"variance":"pooled"}`), Options{Pooled: true}},
		{"unequal", spec(types.TEST_T, "", `{"variance":"unequal"}`), Options{}},
		{"welch ignores", spec(types.TEST_WELCH, "g", `{"variance":"pooled"}`), Options{}},
		{"exact", spec(types.TEST_MANN_WHITNEY_U, "g", `{"exact":true}`), Options{Exact: true}},
		{"chisq correct", spec(types.TEST_CHISQ, "", `{"correct":true}`), Options{Correct: true}},
		{"prop wilson correct", spec(types.TEST_PROP_Z, "", `{"p0":0.3,"interval":"wilson","correct":true}`), Options{Wilson: true, Correct: true}},
	} {
		got, cerr := Parse(c.t)
		if cerr != nil || got != c.want {
			t.Errorf("%s: %+v, %v; want %+v", c.name, got, cerr, c.want)
		}
	}
	for _, c := range []struct {
		name, param string
		t           *types.Test
	}{
		{"variance value", VarianceKey, spec(types.TEST_T, "g", `{"variance":"equal"}`)},
		{"pooled one-sample", VarianceKey, spec(types.TEST_T, "", `{"variance":"pooled"}`)},
		{"exact type", ExactKey, spec(types.TEST_MANN_WHITNEY_U, "g", `{"exact":null}`)},
		{"correct type", CorrectKey, spec(types.TEST_CHISQ, "", `{"correct":"yes"}`)},
		{"wilson two-proportion", IntervalKey, spec(types.TEST_PROP_Z, "g", `{"interval":"wilson"}`)},
		{"correct with covariate", CorrectKey, spec(types.TEST_PROP_Z, "g", `{"correct":true,"covariate":"x"}`)},
	} {
		_, cerr := Parse(c.t)
		if cerr == nil || cerr.Code != errors.PROCESSING_CONFIG || cerr.Details["param"] != c.param {
			t.Errorf("%s: %v, want PROCESSING_CONFIG on %s", c.name, cerr, c.param)
		}
	}
}
