package pulse

import (
	"context"
	"slices"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// TestFeatureProfile_ExtensionRegressionMeetsRegEdges: the built-in
// REG_* any-of groups of ATTR_REG_PREDICT, capability:model_save and
// capability:compose_fits are satisfied by an enabled extension
// regression — a scored one where the dependent scores or saves — so a
// profile enabling only a custom model resolves and scores, fits and
// saves it; without the custom model (or with one carrying no Scorer
// where scoring is needed) the profile still fails as before.
func TestFeatureProfile_ExtensionRegressionMeetsRegEdges(t *testing.T) {
	t.Parallel()

	base := []string{
		"capability:compose", "capability:compose_fits", "capability:model_save",
		"ATTR_REG_PREDICT", "FILTER_RANGE", "GROUP_RANGE", "AGG_MIN",
	}
	open := func(features []string, reg RegressionRegistration) (*Pulse, error) {
		return New(Options{FS: fitsFacadeFS(t), Extensions: Extensions{Regressions: []RegressionRegistration{reg}},
			FeatureProfile: &FeatureProfile{Features: features}})
	}

	t.Run("custom_only_resolves_and_runs", func(t *testing.T) {
		p, err := open(append(slices.Clone(base), string(regScoredName)), centredRegistration(centredScorerFactory))
		if err != nil {
			t.Fatalf("pulse.New: %v", err)
		}
		for _, hidden := range []string{"REG_OLS", "REG_GLM", "REG_BAYES_LINEAR"} {
			if !p.svc.InstanceSnapshot().Hidden(hidden) {
				t.Fatalf("%s is not hidden by the profile", hidden)
			}
		}
		out, err := p.Compose(context.Background(), &ComposedRequest{
			Fits:     []types.FitSpec{{Name: "m", Request: centredFitRequest(), Save: "models/m" + model.FileSuffix}},
			Requests: []*Request{centredSlot(`{"fit":"m"}`)},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Responses) != 1 || len(out.Responses[0].Data) == 0 || out.Responses[0].Data[0]["p"] == nil {
			t.Fatalf("the custom fit did not score: %+v", out.Responses)
		}
	})

	t.Run("no_regression_still_refused", func(t *testing.T) {
		_, err := open(slices.Clone(base), centredRegistration(centredScorerFactory))
		ce := requireCoded(t, err, perr.PULSE_FEATURE_PROFILE_DEPENDENCY)
		unmet, _ := ce.Details["features"].([]string)
		for _, want := range []string{"ATTR_REG_PREDICT", "capability:compose_fits", "capability:model_save"} {
			if !slices.Contains(unmet, want) {
				t.Errorf("unmet features %v lack %s", unmet, want)
			}
		}
	})

	t.Run("unscored_regression_meets_fits_only", func(t *testing.T) {
		_, err := open(append(slices.Clone(base), string(regScoredName)), centredRegistration(nil))
		ce := requireCoded(t, err, perr.PULSE_FEATURE_PROFILE_DEPENDENCY)
		unmet, _ := ce.Details["features"].([]string)
		if !slices.Equal(unmet, []string{"ATTR_REG_PREDICT", "capability:model_save"}) {
			t.Errorf("unmet features = %v, want ATTR_REG_PREDICT and capability:model_save", unmet)
		}
	})
}
