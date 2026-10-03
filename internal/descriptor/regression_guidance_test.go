package descriptor

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestRegressionPurposes_CoverEveryType: every registered REG_* type
// declares a Purpose and an Interpretation list (U08 FR-26), each valid,
// and its intents ride the manifest. The type list comes from the
// registry, never a hardcoded count.
func TestRegressionPurposes_CoverEveryType(t *testing.T) {
	regs := types.AllRegressionTypes()
	if len(regs) == 0 {
		t.Fatal("no regression types registered")
	}
	resolve := BuiltinPurposeResolver()
	for _, r := range regs {
		name := string(r)
		p, ok := regressionPurposes[name]
		if !ok {
			t.Errorf("%s declares no Purpose", name)
			continue
		}
		if v := ValidatePurpose(name, p, resolve); len(v) != 0 {
			t.Errorf("%s: %v", name, v)
		}
		if !slices.Contains(p.Intents, IntentDrivers) {
			t.Errorf("%s intents = %v, want %q among them", name, p.Intents, IntentDrivers)
		}
		if _, ok := regressionInterpretations[name]; !ok {
			t.Errorf("%s declares no Interpretation", name)
		}
	}
	if len(regressionPurposes) != len(regs) || len(regressionInterpretations) != len(regs) {
		t.Errorf("regression guidance covers %d purposes / %d interpretation lists, %d types registered",
			len(regressionPurposes), len(regressionInterpretations), len(regs))
	}
	for _, r := range BuildManifest().Regressions {
		if len(r.Intents) == 0 {
			t.Errorf("manifest regression %s carries no intents", r.Name)
		}
	}
}

// TestRegressionInterpretations_MatchApplicability: each type reads
// exactly the outputs its applicability row lists (map-valued ones as
// "<key>.*"), so no Interpretation names an inapplicable output (no r2
// on REG_GLM, no p_values on REG_BAYES_LINEAR, pseudo_r2 on REG_GLM
// only) and no applicable output is left unread.
func TestRegressionInterpretations_MatchApplicability(t *testing.T) {
	tags := jsonTags(reflect.TypeOf(types.RegressionResult{}))
	for name, ins := range regressionInterpretations {
		var want []string
		for _, k := range regressionOutputs[name] {
			if tags[k].Kind() == reflect.Map {
				k += ".*"
			}
			want = append(want, k)
		}
		var got []string
		for _, in := range ins {
			got = append(got, in.Field)
		}
		slices.Sort(want)
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("%s reads %v, applicability row is %v", name, got, want)
		}
	}
	forbidden := map[string][]string{
		string(types.REG_GLM):          {"r2", "adj_r2", "credible_intervals.*", "residual_std_err"},
		string(types.REG_BAYES_LINEAR): {"p_values.*", "pseudo_r2", "deviance"},
		string(types.REG_OLS):          {"pseudo_r2", "credible_intervals.*", "deviance"},
	}
	for name, fields := range forbidden {
		for _, in := range regressionInterpretations[name] {
			if slices.Contains(fields, in.Field) {
				t.Errorf("%s reads inapplicable output %s", name, in.Field)
			}
		}
	}
}

// TestRegressionInterpretations_Readings pins the readings FR-26 names:
// p-values cite the shared rule set; the Bayesian credible interval is
// a probability statement about the parameter given the prior and the
// model; only REG_OLS r2 is banded, by the Cohen R-squared convention;
// every coefficient reading is signed and says association, not
// causation, holding the other predictors fixed.
func TestRegressionInterpretations_Readings(t *testing.T) {
	byField := func(name string) map[string]map[string]string {
		out := map[string]map[string]string{}
		for _, in := range regressionInterpretations[name] {
			out[in.Field] = map[string]string{"means": in.Means, "shared": in.Shared, "convention": in.Convention,
				"caveats": strings.Join(in.Caveats, " "), "sign+": in.Sign["+"], "sign-": in.Sign["-"]}
		}
		return out
	}
	for _, name := range []string{"REG_OLS", "REG_GLM"} {
		if got := byField(name)["p_values.*"]["shared"]; got != SharedPValue {
			t.Errorf("%s p_values.*: Shared = %q, want %q", name, got, SharedPValue)
		}
	}
	ci := byField("REG_BAYES_LINEAR")["credible_intervals.*"]
	for _, want := range []string{"probability", "given the prior and the model"} {
		if !strings.Contains(ci["means"]+" "+ci["caveats"], want) {
			t.Errorf("REG_BAYES_LINEAR credible_intervals.* does not say %q", want)
		}
	}
	for name := range regressionInterpretations {
		coef := byField(name)["coefficients.*"]
		if coef["sign+"] == "" || coef["sign-"] == "" {
			t.Errorf("%s coefficients.*: want both sign meanings", name)
		}
		if !strings.Contains(coef["means"], "holding the other predictors fixed") {
			t.Errorf("%s coefficients.*: Means does not say the other predictors are held fixed", name)
		}
		if !strings.Contains(coef["caveats"], "not causation") {
			t.Errorf("%s coefficients.*: caveats do not say association, not causation", name)
		}
		for field, in := range byField(name) {
			banded := in["convention"] != ""
			if want := name == "REG_OLS" && field == "r2"; banded != want {
				t.Errorf("%s %s: banded = %v, want %v", name, field, banded, want)
			}
		}
	}
	if got := byField("REG_OLS")["r2"]["convention"]; got != conventionCitation(ConventionCohenR2) {
		t.Errorf("REG_OLS r2 convention = %q, want %q", got, conventionCitation(ConventionCohenR2))
	}
}
