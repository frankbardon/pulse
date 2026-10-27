package regression

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// tableFit is an ExtensionFit whose deviance is a fixed table keyed by
// the sorted active set — so the selection criterion's arithmetic, not
// a data fit, decides the search. Every fit reports n_obs 82 and one
// coefficient per active predictor plus an intercept.
func tableFit(dev map[string]float64, report bool) ExtensionFit {
	return func(recs []Record, predictors []string) (*types.RegressionResult, []string, error) {
		key := slices.Clone(predictors)
		slices.Sort(key)
		d, ok := dev[strings.Join(key, ",")]
		if !ok {
			return nil, nil, errors.NewCodedError(errors.PROCESSING_REGRESSION_RANK_DEFICIENT, "no such candidate")
		}
		keys := append([]string{InterceptKey}, predictors...)
		res := &types.RegressionResult{NObs: 82, Coefficients: map[string]float64{}}
		for i, k := range keys {
			res.Coefficients[k] = float64(i + 1)
		}
		if report {
			res.Deviance = d
			res.ReportedFigures = []string{"deviance"}
		}
		return res, keys, nil
	}
}

// TestRunExtensionModifiers_SelectionCriterion: AIC charges 2 per
// coefficient and BIC ln(n) per coefficient on the fit's deviance, so
// AIC keeps both predictors (87.9 + 6 = 93.9 < 90 + 4 = 94) where BIC
// (ln 82 ≈ 4.41: 87.9 + 13.2 > 90 + 8.8) keeps only b, under every
// search; a candidate fit reporting no deviance stops the search with
// the missing-deviance refusal; a data-dependent candidate refusal only
// drops the candidate.
func TestRunExtensionModifiers_SelectionCriterion(t *testing.T) {
	t.Parallel()

	dev := map[string]float64{"": 100, "a": 97.5, "b": 90, "a,b": 87.9}
	recs := []Record{mockRecord{values: map[string]float64{"y": 1}}}
	for _, sel := range []string{"forward", "backward", "stepwise"} {
		for crit, want := range map[string][]string{"aic": {"a", "b"}, "bic": {"b"}} {
			spec := &types.RegressionSpec{Target: "y", Predictors: []string{"a", "b"}, Selection: sel, Criterion: crit}
			res, err := RunExtensionModifiers(context.Background(), spec, true, recs, tableFit(dev, true), nil, nil)
			if err != nil {
				t.Fatalf("%s/%s: %v", sel, crit, err)
			}
			got := slices.Clone(res.SelectedFeatures)
			slices.Sort(got)
			if !slices.Equal(got, want) || res.Selection != sel || res.Criterion != crit {
				t.Errorf("%s/%s selected %v (%q %q), want %v", sel, crit, res.SelectedFeatures, res.Selection, res.Criterion, want)
			}
		}
	}

	missing := errors.NewCodedError(errors.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH, "no deviance")
	spec := &types.RegressionSpec{Target: "y", Predictors: []string{"a", "b"}, Selection: "forward", Criterion: "aic"}
	_, err := RunExtensionModifiers(context.Background(), spec, true, recs, tableFit(dev, false), func() error { return missing }, nil)
	if err != missing {
		t.Errorf("a fit without deviance: err = %v, want the missing-deviance refusal", err)
	}

	sparse := map[string]float64{"": 100, "b": 90, "a,b": 87.9} // {a} alone refuses
	res, err := RunExtensionModifiers(context.Background(), spec, true, recs, tableFit(sparse, true), nil, nil)
	if err != nil || len(res.SelectedFeatures) != 2 {
		t.Errorf("a data-dependent candidate refusal stopped the search: %v %+v", err, res)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := RunExtensionModifiers(ctx, spec, true, recs, tableFit(dev, true), nil, nil); err != context.Canceled {
		t.Errorf("a cancelled ctx: err = %v, want context.Canceled", err)
	}
}
