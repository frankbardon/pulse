package regression

import (
	"context"
	"math"
	"slices"

	"github.com/frankbardon/pulse/internal/slotfail"
	"github.com/frankbardon/pulse/types"
)

// extension_modifiers.go drives the Resample and Selection modifiers
// around an extension (registered) REG_* model with the built-in
// drivers' own replicate loops (resample_core.go) and greedy search
// (selection.go). The model is opaque: every refit is one fresh fit of
// the registration over a row subset (resample) or under a predictor
// subset (selection), and a replicate is the fit's reported
// coefficients in the base fit's order.

// ExtensionFit is one fit of an extension model over recs with
// predictors as its active set: the mapped result and the fit's
// coefficient names in the order the model reported them. A fresh
// model per call — the caller builds it from the slot's spec with
// predictors substituted and the modifier keys cleared.
type ExtensionFit func(recs []Record, predictors []string) (res *types.RegressionResult, keys []string, err error)

// RunExtensionModifiers fits an extension model under spec's Selection
// (first) and Resample modifiers, ctx read before every refit.
//
// Selection (the registration declares SupportsSelection) is the
// built-in greedy search over spec.Predictors, each candidate scored by
// the information criterion of its fit's reported deviance (−2·log
// likelihood up to a constant every candidate shares): AIC = deviance +
// 2k, BIC = deviance + ln(n)·k, with k the fit's coefficient count and
// n its n_obs. A candidate fit that reports no deviance is
// PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH (reason missing_deviance)
// via missingDeviance; a candidate's data-dependent refusal (the
// per-slot codes) drops it, any other error stops the run.
//
// Resample refits the model over the fitted rows (the target, every
// active predictor and, weighted, a valid positive weight present —
// plus the jackknife_by value) exactly as REG_OLS does: the jackknife
// (leave-one-out, or delete-a-group with jackknife_groups /
// jackknife_by) and the bootstrap with the same seed rule. Each
// coefficient's standard error becomes the replicate SE, its statistic
// the Wald z (estimate / SE) and its p-value the Wald-z two-sided p
// (jackknife) or the percentile p (bootstrap) — NaN on every key when
// pValues is false (the registration declares no p-values, so predict
// counts none and no multiplicity family reads them); a spec-requested vcov
// becomes the replicate covariance. A replicate whose coefficient names
// differ from the base fit's is PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH
// (reason replicate_coefficients) via mismatch.
func RunExtensionModifiers(
	ctx context.Context,
	spec *types.RegressionSpec,
	pValues bool,
	records []Record,
	fit ExtensionFit,
	missingDeviance func() error,
	mismatch func(base, got []string) error,
) (*types.RegressionResult, error) {
	if spec.Resample == "jackknife" && spec.JackknifeBy != "" {
		kept := make([]Record, 0, len(records))
		for _, rec := range records {
			if _, ok := clusterKey(rec, spec.JackknifeBy); ok {
				kept = append(kept, rec)
			}
		}
		records = kept
	}
	active := spec.Predictors
	var base *types.RegressionResult
	var keys []string
	if spec.Selection != "" {
		out, outKeys, err := extensionSelection(ctx, spec, records, fit, missingDeviance)
		if err != nil {
			return nil, err
		}
		base, keys, active = out, outKeys, out.SelectedFeatures
	} else {
		if err := ctxErr(ctx); err != nil {
			return nil, err
		}
		var err error
		if base, keys, err = fit(records, active); err != nil {
			return nil, err
		}
	}
	if spec.Resample == "" {
		return base, nil
	}
	return extensionResample(ctx, spec, pValues, records, active, base, keys, fit, mismatch)
}

// extensionFatal reports whether a refit error must stop a modifier
// rather than drop one candidate or replicate: anything but the
// data-dependent refusals that fail a regression slot alone.
func extensionFatal(err error) bool {
	_, ok := slotfail.From(slotfail.KindRegressions, err)
	return !ok
}

// extensionSelection runs the greedy search and fits the selected set.
func extensionSelection(ctx context.Context, spec *types.RegressionSpec, records []Record, fit ExtensionFit, missingDeviance func() error) (*types.RegressionResult, []string, error) {
	refit := func(recs []Record, active []string) (*refitResult, error) {
		res, keys, err := fit(recs, active)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(res.ReportedFigures, "deviance") {
			return nil, missingDeviance()
		}
		return &refitResult{N: res.NObs, Deviance: res.Deviance, Params: len(keys)}, nil
	}
	wantBIC := spec.Criterion == "bic"
	crit := func(f *refitResult, _ int) float64 {
		k := float64(f.Params)
		if wantBIC {
			return f.Deviance + math.Log(float64(f.N))*k
		}
		return f.Deviance + 2*k
	}
	var finalKeys []string
	finalize := func(recs []Record, active []string) (*types.RegressionResult, error) {
		res, keys, err := fit(recs, active)
		finalKeys = keys
		return res, err
	}
	out, err := runSelectionCtx(ctx, spec, records, spec.Predictors, crit, refit, finalize, extensionFatal)
	if err != nil {
		return nil, nil, err
	}
	return out, finalKeys, nil
}

// extensionResample replaces base's uncertainty figures with the
// replicate ones.
func extensionResample(ctx context.Context, spec *types.RegressionSpec, pValues bool, records []Record, active []string, base *types.RegressionResult, keys []string, fit ExtensionFit, mismatch func(base, got []string) error) (*types.RegressionResult, error) {
	q := len(keys)
	rep := func(recs []Record) ([]float64, error) {
		res, got, err := fit(recs, active)
		if err != nil {
			return nil, err
		}
		if !sameKeySet(keys, got) {
			return nil, mismatch(keys, got)
		}
		row := make([]float64, q)
		for j, k := range keys {
			row[j] = res.Coefficients[k]
		}
		return row, nil
	}
	rows := jackknifeRows(spec, records, active)
	var (
		se, bootP []float64
		cov       [][]float64
		err       error
	)
	out := *base
	switch spec.Resample {
	case "jackknife":
		if spec.JackknifeGroups != 0 || spec.JackknifeBy != "" {
			var g int
			se, cov, g, err = groupedJackknifeCore(ctx, spec, records, active, rows, q, rep)
			out.JackknifeGroups = g
		} else {
			se, cov, err = jackknifeCore(ctx, spec, rows, q, rep)
		}
	case "bootstrap":
		var run *bootstrapRun
		if run, err = bootstrapCore(ctx, spec, rows, active, q, rep, extensionFatal); err == nil {
			se, cov, bootP = run.se, run.cov, run.p
			out.BootstrapRequested, out.BootstrapSucceeded = run.requested, run.succeeded
		}
	default:
		err = unknownResample(spec)
	}
	if err != nil {
		return nil, err
	}
	out.StdErrors = make(map[string]float64, q)
	out.Statistics = make(map[string]float64, q)
	out.PValues = make(map[string]float64, q)
	for j, k := range keys {
		est := base.Coefficients[k]
		out.StdErrors[k] = se[j]
		out.Statistics[k] = waldZ(est, se[j])
		switch {
		case !pValues:
			out.PValues[k] = math.NaN()
		case bootP != nil:
			out.PValues[k] = bootP[j]
		default:
			out.PValues[k] = waldZTwoSidedP(est, se[j])
		}
	}
	out.Resample = spec.Resample
	out.Vcov, out.Correlation = nil, nil
	if spec.Vcov && base.Vcov != nil {
		AttachVcovKeys(&out, keys, cov)
	}
	return &out, nil
}

// waldZ is estimate / se, NaN where the ratio is undefined (a zero or
// undefined standard error under a zero estimate).
func waldZ(est, se float64) float64 {
	if math.IsNaN(est) || math.IsNaN(se) || (se == 0 && est == 0) {
		return math.NaN()
	}
	return est / se
}

// sameKeySet reports whether got names exactly base's coefficients.
func sameKeySet(base, got []string) bool {
	if len(base) != len(got) {
		return false
	}
	for _, k := range got {
		if !slices.Contains(base, k) {
			return false
		}
	}
	return true
}
