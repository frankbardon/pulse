package integration_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// showcaseReadout is the A/B showcase's one-call readout over ab.pulse:
// six guardrail checks and six scorecard metrics, each slot under its
// own holm request family (family), a lone Mann-Whitney sensitivity
// test and two uncorrected OLS fits.
func showcaseReadout(t *testing.T, family string) *types.ComposedRequest {
	t.Helper()
	block := `"multiplicity":{"method":"holm","family":"` + family + `"}`
	raw := `{"requests":[
	{"label":"guardrails","cohort":{"filename":"ab.pulse"},` + block + `,"tests":[
		{"type":"TEST_CHISQ","rows":"arm","cols":"assign_week","label":"srm_by_week"},
		{"type":"TEST_CHISQ","rows":"arm","cols":"device","label":"balance_device"},
		{"type":"TEST_CHISQ","rows":"arm","cols":"plan_tier","label":"balance_plan_tier"},
		{"type":"TEST_CHISQ","rows":"arm","cols":"region","label":"balance_region"},
		{"type":"TEST_CHISQ","rows":"arm","cols":"user_type","label":"balance_user_type"},
		{"type":"TEST_WELCH","field":"prior_sessions","split_by":"arm","label":"balance_prior_sessions"}]},
	{"label":"scorecard","cohort":{"filename":"ab.pulse"},` + block + `,"tests":[
		{"type":"TEST_PROP_Z","field":"trial_started","split_by":"arm","label":"trial_start","params":{"success":"yes","reference":"old_page"}},
		{"type":"TEST_PROP_Z","field":"paid_converted","split_by":"arm","label":"paid_conversion","params":{"success":"yes","reference":"old_page"}},
		{"type":"TEST_WELCH","field":"revenue","split_by":"arm","label":"revenue_per_user","params":{"reference":"old_page"}},
		{"type":"TEST_WELCH","field":"pages_viewed","split_by":"arm","label":"pages_cuped","params":{"reference":"old_page","covariate":"prior_sessions"}},
		{"type":"TEST_WELCH","field":"days_to_convert","split_by":"arm","label":"days_to_convert","params":{"reference":"old_page"}},
		{"type":"TEST_PROP_Z","field":"retained_30d","split_by":"arm","label":"retention_30d","params":{"success":"yes","reference":"old_page"}}]},
	{"label":"revenue_sensitivity","cohort":{"filename":"ab.pulse"},"tests":[
		{"type":"TEST_MANN_WHITNEY_U","field":"revenue","split_by":"arm","label":"revenue_mann_whitney","params":{"reference":"old_page"}}]},
	{"label":"regression_adjustment","cohort":{"filename":"ab.pulse"},"regressions":[
		{"type":"REG_OLS","name":"pages_unadjusted","target":"pages_viewed","predictors":["is_variant"]},
		{"type":"REG_OLS","name":"pages_adjusted","target":"pages_viewed","predictors":["is_variant","prior_sessions"]}]}]}`
	var c types.ComposedRequest
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatal(err)
	}
	return &c
}

// TestPredictCompose_ShowcaseReadoutFamilies: on the A/B showcase
// readout, predict-compose reports each slot's streamability exactly as
// Process predict does for the slot alone, and correction families
// whose counts equal the m the runtime fold reports on every member
// test; the two request families (6 + 6, 16 p-values in all) raise
// PULSE_ADVISORY_COMPOSE_FAMILIES_SPLIT, and family compose silences it
// with the runtime pooling the twelve into one family.
func TestPredictCompose_ShowcaseReadoutFamilies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatal(err)
	}
	showcaseABCohort(t, p)
	code := string(errors.PULSE_ADVISORY_COMPOSE_FAMILIES_SPLIT)

	for _, tc := range []struct {
		family string
		fire   bool
		ids    []string
		m      int
	}{
		{"request", true, []string{"requests[0]#request", "requests[1]#request"}, 6},
		{"compose", false, []string{"compose", "compose"}, 12},
	} {
		t.Run(tc.family, func(t *testing.T) {
			batch := showcaseReadout(t, tc.family)
			env, err := p.PredictCompose(ctx, batch)
			if err != nil {
				t.Fatal(err)
			}
			res := env.Data.(*pulse.ComposePredictResult)
			if !res.Valid || len(res.Slots) != 4 {
				t.Fatalf("verdict valid %v slots %d: %+v", res.Valid, len(res.Slots), env.Errors)
			}
			for i, s := range res.Slots {
				direct, err := p.Predict(ctx, batch.Requests[i])
				if err != nil {
					t.Fatal(err)
				}
				if s.Streamable != direct.Streamable || !reflect.DeepEqual(s.StreamableReasons, direct.StreamableReasons) {
					t.Errorf("slot %d streamable %v %v, Process predict %v %v", i, s.Streamable, s.StreamableReasons, direct.Streamable, direct.StreamableReasons)
				}
			}
			for i, id := range tc.ids {
				f := res.Slots[i].Families
				if len(f) != 1 || f[0].ID != id || f[0].Count != 6 {
					t.Errorf("slot %d families %+v, want %s x6", i, f, id)
				}
			}
			if res.Slots[2].PValues.Total != 1 || res.Slots[2].Families != nil || res.Slots[3].PValues.Uncorrected != 3 {
				t.Errorf("uncorrected slots: %+v / %+v", res.Slots[2].PValues, res.Slots[3].PValues)
			}
			var split map[string]any
			for _, a := range res.Advisories {
				if a.Code == code {
					split = a.Details
				}
			}
			if (split != nil) != tc.fire {
				t.Fatalf("fired %v, want %v: %+v", split != nil, tc.fire, res.Advisories)
			}
			if tc.fire && (split["total"] != 16 || split["uncorrected"] != 4) {
				t.Errorf("details %+v, want total 16 uncorrected 4", split)
			}

			// The runtime fold corrects every member test in a family of
			// exactly the predicted size.
			out, err := p.Compose(ctx, batch)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				for _, r := range out.Responses[i].Tests {
					if r.Multiplicity == nil || r.Multiplicity.M != tc.m || string(r.Multiplicity.Family) != tc.family {
						t.Errorf("slot %d test %s multiplicity %+v, want %s m %d", i, r.Label, r.Multiplicity, tc.family, tc.m)
					}
				}
			}
		})
	}
}
