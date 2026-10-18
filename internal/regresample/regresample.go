// Package regresample is the one rule over a regression's jackknife
// knobs (types.RegressionSpec JackknifeGroups / JackknifeBy), shared by
// the no-execute predict pass (internal/descriptor) and the regression
// engines (internal/processing/regression) so the two refuse the same
// requests with the same code, message and details. It reads the spec
// only — never a schema or a record — and imports neither side.
//
// It also holds the leave-one-out cost threshold predict's
// PULSE_ADVISORY_JACKKNIFE_COST reads, so the figure the advisory names
// lives beside the knobs it recommends.
package regresample

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// LeaveOneOutCostRows is the cohort record count at or above which a
// leave-one-out jackknife (Resample "jackknife" with neither
// JackknifeGroups nor JackknifeBy) draws PULSE_ADVISORY_JACKKNIFE_COST.
// Leave-one-out refits the model once per row, so its cost grows with
// n²: a two-predictor binomial REG_GLM leave-one-out jackknife takes
// ~1.4 s at 2,000 rows (internal/processing/regression
// BenchmarkJackknife, Apple M-series), so ~35 s at 10,000 and ~10 min
// at 42,000, while a delete-a-group jackknife with SuggestedGroups
// groups takes ~0.6 s at 42,000 rows.
const LeaveOneOutCostRows = 10000

// SuggestedGroups is the jackknife_groups value the cost advisory
// proposes: a conventional replicate count (survey JK1 designs commonly
// use 20–100), whose standard error carries roughly 1/√(2(g − 1)) ≈ 10%
// relative noise from the replicate count — at g refits instead of n.
const SuggestedGroups = 50

// Grouped reports whether spec asks for a delete-a-group jackknife
// (JackknifeGroups or JackknifeBy set).
func Grouped(spec *types.RegressionSpec) bool {
	return spec != nil && (spec.JackknifeGroups != 0 || spec.JackknifeBy != "")
}

// LeaveOneOut reports whether spec runs the leave-one-out jackknife.
func LeaveOneOut(spec *types.RegressionSpec) bool {
	return spec != nil && spec.Resample == "jackknife" && !Grouped(spec)
}

// Check returns the PROCESSING_CONFIG refusal of spec's jackknife
// knobs, or nil. Rules (every regression type; REG_BAYES_LINEAR refuses
// Resample itself, so it never reaches a jackknife):
//
//   - jackknife_groups and jackknife_by are mutually exclusive.
//   - either one is read only by resample "jackknife".
//   - jackknife_groups, when set, is at least 2 (zero is absent). The
//     upper bound (at most the fitted row count) is data and is judged
//     by the engine (PROCESSING_REGRESSION_INSUFFICIENT_DATA).
func Check(spec *types.RegressionSpec) *errors.CodedError {
	if spec == nil || !Grouped(spec) {
		return nil
	}
	t := string(spec.Type)
	if spec.JackknifeGroups != 0 && spec.JackknifeBy != "" {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			t+" jackknife_groups and jackknife_by are mutually exclusive: set jackknife_groups for random delete groups or jackknife_by for a field's clusters, not both",
			map[string]any{"jackknife_groups": spec.JackknifeGroups, "jackknife_by": spec.JackknifeBy, "type": t},
		)
	}
	if spec.Resample != "jackknife" {
		key, value := "jackknife_groups", any(spec.JackknifeGroups)
		if spec.JackknifeBy != "" {
			key, value = "jackknife_by", spec.JackknifeBy
		}
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			t+" "+key+" is read only by resample \"jackknife\"",
			map[string]any{key: value, "resample": spec.Resample, "type": t},
		)
	}
	if spec.JackknifeGroups != 0 && spec.JackknifeGroups < 2 {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			t+" jackknife_groups must be at least 2 (omit it for the leave-one-out jackknife)",
			map[string]any{"jackknife_groups": spec.JackknifeGroups, "minimum": 2, "type": t},
		)
	}
	return nil
}
