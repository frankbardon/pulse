package descriptor

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// experimentCohort: y, z, w numeric and arm a two-level categorical —
// enough for two-group tests and an OLS fit.
func experimentCohort(t *testing.T) []byte {
	t.Helper()
	return buildTestPulseFile(t, &encoding.Schema{Fields: []encoding.Field{
		{Name: "y", Type: encoding.FieldTypeF64, Description: "Outcome measured after assignment"},
		{Name: "z", Type: encoding.FieldTypeF64, Description: "Pre-period activity per user"},
		{Name: "w", Type: encoding.FieldTypeF64, Description: "Tenure of the user in days"},
		{Name: "arm", Type: encoding.FieldTypeCategoricalU8, Description: "Arm the user was assigned to", Dictionary: makeDictionary(t, "control", "treatment")},
	}})
}

func experimentOpts(t *testing.T) *PredictOptions {
	return &PredictOptions{SchemaLoader: composeSchemaLoader(t, map[string][]byte{"e.pulse": experimentCohort(t)})}
}

// welchTests is n two-group Welch tests of y by arm.
func welchTests(n int) []*types.Test {
	out := make([]*types.Test, n)
	for i := range out {
		out[i] = &types.Test{Type: types.TEST_WELCH, Field: "y", SplitBy: "arm", Label: "t" + strconv.Itoa(i)}
	}
	return out
}

// testSlot is a slot running n Welch tests, under block when non-nil.
func testSlot(n int, block *types.Multiplicity) *types.Request {
	return &types.Request{Cohort: &types.Cohort{Filename: "e.pulse"}, Multiplicity: block, Tests: welchTests(n)}
}

func holmIn(family types.MultiplicityFamily) *types.Multiplicity {
	return &types.Multiplicity{Method: types.MultiplicityMethodHolm, Family: family}
}

// TestComposePredict_SlotDetail: every slot of the effective batch gets
// an entry — its resolved label, the streamability verdict Process
// predict gives the same request, its p-value count (tests and
// regression predictors) and the correction families its p-values join.
func TestComposePredict_SlotDetail(t *testing.T) {
	t.Parallel()

	ols := &types.RegressionSpec{Type: types.REG_OLS, Name: "fit", Target: "y", Predictors: []string{"z", "w"},
		Multiplicity: &types.RegressionMultiplicity{Method: types.MultiplicityMethodHolm},
		Covariance:   &types.RegressionCovariance{Type: types.CovarianceHC1}}
	slots := []*types.Request{
		{Label: "guardrails", Cohort: &types.Cohort{Filename: "e.pulse"}, Multiplicity: holmIn(types.MultiplicityFamilyRequest), Tests: welchTests(3)},
		{Cohort: &types.Cohort{Filename: "e.pulse"}, Multiplicity: holmIn(types.MultiplicityFamilyRequest), Tests: welchTests(2), Regressions: []*types.RegressionSpec{ols}},
		{Cohort: &types.Cohort{Filename: "e.pulse"}, Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "y", Label: "m"}}},
	}
	env := ValidateComposeWithOptions(&types.ComposedRequest{Requests: slots}, experimentOpts(t))
	if len(env.Errors) != 0 {
		t.Fatalf("fixture invalid: %+v", env.Errors)
	}
	res := env.Data.(*ComposeValidationResult)
	if len(res.Slots) != len(slots) {
		t.Fatalf("slots = %+v, want %d entries", res.Slots, len(slots))
	}
	for i, want := range []struct {
		label    string
		families []descriptor.PValueFamily
	}{
		{"guardrails", []descriptor.PValueFamily{{ID: "requests[0]#request", Family: types.MultiplicityFamilyRequest, Method: types.MultiplicityMethodHolm, Count: 3, Basis: descriptor.PValueBasisExact}}},
		// The regression's own block joins its predictors to the slot's
		// request family beside the tests.
		{"request_2", []descriptor.PValueFamily{{ID: "requests[1]#request", Family: types.MultiplicityFamilyRequest, Method: types.MultiplicityMethodHolm, Count: 4, Basis: descriptor.PValueBasisExact}}},
		{"request_3", nil},
	} {
		got := res.Slots[i]
		if got.Request != i || got.Label != want.label {
			t.Errorf("slot %d = request %d label %q, want %d %q", i, got.Request, got.Label, i, want.label)
		}
		if !reflect.DeepEqual(got.Families, want.families) {
			t.Errorf("slot %d families = %+v, want %+v", i, got.Families, want.families)
		}
		// Parity with Process predict on the slot's own request.
		direct := predictFromBytes(experimentCohort(t), slots[i], nil).Data.(*descriptor.PredictResult)
		if got.Streamable != direct.Streamable || !reflect.DeepEqual(got.StreamableReasons, direct.StreamableReasons) {
			t.Errorf("slot %d streamable %v %v, Process predict %v %v", i, got.Streamable, got.StreamableReasons, direct.Streamable, direct.StreamableReasons)
		}
		if !reflect.DeepEqual(got.PValues, direct.PValues) {
			t.Errorf("slot %d p_values %+v, Process predict %+v", i, got.PValues, direct.PValues)
		}
	}
	// Tests-only streams; a robust-covariance regression (beside tier-1
	// tests, which alone no longer buffer it) runs buffered.
	if !res.Slots[0].Streamable || res.Slots[1].Streamable {
		t.Errorf("streamable = %v / %v, want true / false", res.Slots[0].Streamable, res.Slots[1].Streamable)
	}
	if pv := res.Slots[1].PValues; pv == nil || pv.Total != 4 || pv.Uncorrected != 0 {
		t.Errorf("slot 1 p_values = %+v, want 4 corrected (2 tests + 2 predictors)", pv)
	}

	// A batch-wide compose block: every family is the one `compose`.
	inherited := &types.ComposedRequest{Multiplicity: holmIn(types.MultiplicityFamilyCompose), Requests: []*types.Request{testSlot(2, nil), testSlot(3, nil)}}
	res = composeResult(t, inherited, experimentOpts(t))
	for i, n := range []int{2, 3} {
		want := []descriptor.PValueFamily{{ID: "compose", Family: types.MultiplicityFamilyCompose, Method: types.MultiplicityMethodHolm, Count: n, Basis: descriptor.PValueBasisExact}}
		if !reflect.DeepEqual(res.Slots[i].Families, want) {
			t.Errorf("compose block: slot %d families %+v, want %+v", i, res.Slots[i].Families, want)
		}
	}

	// capability:multiplicity hidden: the verdict stays, the counts go.
	res = composeResult(t, &types.ComposedRequest{Requests: []*types.Request{testSlot(2, nil)}},
		&PredictOptions{SchemaLoader: experimentOpts(t).SchemaLoader, Instance: hideOnly(featMultiplicity)})
	if len(res.Slots) != 1 || !res.Slots[0].Streamable || res.Slots[0].PValues != nil || res.Slots[0].Families != nil {
		t.Errorf("multiplicity hidden: slots %+v, want the verdict without counts", res.Slots)
	}
}

// TestComposePredict_AdvisoryFamiliesSplit: PULSE_ADVISORY_COMPOSE_FAMILIES_SPLIT
// fires when two or more slots emit p-values, the corrected ones sit in
// two or more distinct non-compose families, and the slots emit at
// least MultiplicityTriggerThreshold p-values in total.
func TestComposePredict_AdvisoryFamiliesSplit(t *testing.T) {
	t.Parallel()

	code := string(errors.PULSE_ADVISORY_COMPOSE_FAMILIES_SPLIT)
	req := holmIn(types.MultiplicityFamilyRequest)
	cmp := holmIn(types.MultiplicityFamilyCompose)
	ols := func() *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: "e.pulse"},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "y", Label: "n"}},
			Regressions:  []*types.RegressionSpec{{Type: types.REG_OLS, Name: "fit", Target: "y", Predictors: []string{"z", "w"}}}}
	}
	cases := []struct {
		name     string
		batch    *types.ComposedRequest
		fire     bool
		slots    []int
		families []string
		total    int
	}{
		{"showcase 6+6+1", &types.ComposedRequest{Requests: []*types.Request{testSlot(6, req), testSlot(6, req), testSlot(1, nil)}},
			true, []int{0, 1}, []string{"requests[0]#request", "requests[1]#request"}, 13},
		{"at threshold 5+5", &types.ComposedRequest{Requests: []*types.Request{testSlot(5, req), testSlot(5, req)}},
			true, []int{0, 1}, []string{"requests[0]#request", "requests[1]#request"}, 10},
		{"under threshold 5+4", &types.ComposedRequest{Requests: []*types.Request{testSlot(5, req), testSlot(4, req)}}, false, nil, nil, 0},
		{"regression predictors count 4+4+2", &types.ComposedRequest{Requests: []*types.Request{testSlot(4, req), testSlot(4, req), ols()}},
			true, []int{0, 1}, []string{"requests[0]#request", "requests[1]#request"}, 10},
		{"one family beside uncorrected", &types.ComposedRequest{Requests: []*types.Request{testSlot(6, req), testSlot(6, nil)}}, false, nil, nil, 0},
		{"one slot emits", &types.ComposedRequest{Requests: []*types.Request{testSlot(12, req), ols()}}, false, nil, nil, 0},
		{"every slot compose", &types.ComposedRequest{Requests: []*types.Request{testSlot(6, cmp), testSlot(6, cmp), testSlot(1, nil)}}, false, nil, nil, 0},
		{"one compose one request", &types.ComposedRequest{Requests: []*types.Request{testSlot(6, cmp), testSlot(6, req)}}, false, nil, nil, 0},
		{"batch compose block", &types.ComposedRequest{Multiplicity: cmp, Requests: []*types.Request{testSlot(6, nil), testSlot(6, nil)}}, false, nil, nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := ValidateComposeWithOptions(tc.batch, experimentOpts(t))
			if len(env.Errors) != 0 {
				t.Fatalf("fixture invalid: %+v", env.Errors)
			}
			var got []descriptor.Advisory
			for _, a := range env.Data.(*ComposeValidationResult).Advisories {
				if a.Code == code {
					got = append(got, a)
				}
			}
			if !tc.fire {
				if len(got) != 0 {
					t.Fatalf("fired: %+v", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("advisories %+v, want one %s", env.Data.(*ComposeValidationResult).Advisories, code)
			}
			d := got[0].Details
			if !reflect.DeepEqual(d["slots"], tc.slots) || !reflect.DeepEqual(d["families"], tc.families) || d["total"] != tc.total ||
				d["threshold"] != descriptor.MultiplicityTriggerThreshold {
				t.Errorf("details = %+v, want slots %v families %v total %d", d, tc.slots, tc.families, tc.total)
			}
			want := map[string]any{"multiplicity": map[string]any{"method": "holm", "family": "compose"}}
			if !reflect.DeepEqual(d["suggested"], want) {
				t.Errorf("suggested = %+v, want %+v", d["suggested"], want)
			}
			if _, located := d["request"]; located {
				t.Errorf("a batch advisory carries details.request: %+v", d)
			}
			for _, f := range tc.families {
				if !strings.Contains(got[0].Message, f) {
					t.Errorf("message %q does not name family %s", got[0].Message, f)
				}
			}
			if hits := LintGuidanceText(got[0].Message); len(hits) > 0 {
				t.Errorf("lint hits %+v", hits)
			}
		})
	}

	// Suppression drops it; the per-slot detail stays.
	showcase := cases[0].batch
	opts := experimentOpts(t)
	opts.Instance = (*InstanceSnapshot)(nil).WithSuppressedAdvisories([]string{code})
	res := composeResult(t, showcase, opts)
	if len(res.Advisories) != 0 || len(res.Slots) != 3 {
		t.Errorf("suppressed: advisories %+v slots %d, want none and 3", res.Advisories, len(res.Slots))
	}
	// Never a warning, never an error; the wire carries it in advisories.
	env := ValidateComposeWithOptions(showcase, &PredictOptions{SchemaLoader: experimentOpts(t).SchemaLoader, Strict: true})
	if len(env.Warnings) != 0 || len(env.Errors) != 0 || !env.Data.(*ComposeValidationResult).Valid {
		t.Errorf("strict: warnings %+v errors %+v, want none", env.Warnings, env.Errors)
	}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"`+code+`"`) || !strings.Contains(string(b), `"slots":[{"request":0,"label":"request_1","streamable":true`) {
		t.Errorf("wire lacks the advisory or slot detail: %s", b)
	}
}
