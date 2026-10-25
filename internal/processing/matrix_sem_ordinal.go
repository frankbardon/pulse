package processing

import (
	stderrors "errors"
	"math"
	"slices"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/sem"
	"github.com/frankbardon/pulse/internal/vectors"
)

// matrix_sem_ordinal.go is MAT_SEM under an ordinal estimator
// (params.estimator "dwls" / "wlsmv", params.ordered): lavaan's
// two-step fit (sem.FitOrdinal) over the slot's buffered rows — the
// casewise scores behind the statistics' asymptotic covariance (Gamma)
// need every row, so the plan is never streamable
// (types.MatrixSpec.Streamable) and runs serially.
//
// Each ordered member is coded by its sorted distinct values over the
// rows the fit reads (code 0 the smallest): lavaan's ordered factor of
// the observed values, so a category no row holds is no category. A
// numeric member enters as is. The rows are the slot's listwise rows,
// each with its frequency weight (1 unweighted).
//
// The outputs are finalizeSEM's: primary the implied covariance (each
// ordered member's diagonal 1), the estimates (the model's rows, an
// `x | tk` row per threshold, then the := rows on the robust vcov),
// residual_covariance (polychoric / polyserial minus implied),
// implied_means / observed_means, and semScalarKeys — chi2_p_value,
// loglik, aic and bic null (lavaan's NA) — plus, under wlsmv, the
// scaled test (vectors.SEMOrdinalScalarKeys).
//
// A two-step refusal (sem.OrdinalError) fails the slot alone: a
// constant member (or a singular Gamma) PULSE_MATRIX_SINGULAR, an empty
// category or no data PULSE_MATRIX_INSUFFICIENT_N, details.reason the
// OrdinalError's reason. Fewer than 2 rows null every figure, as the ML
// fit's undefined input.
func finalizeSEMOrdinal(in *matrixFinalizeInput) (matrixOutput, error) {
	m := in.slot
	plan := in.Plan()
	spec := plan.SEM
	rows := in.Rows
	out := matrixOutput{}
	if in.CM != nil {
		out.Warnings = m.warnings(in.CM, in.CM.Cov(0))
	}
	if rows == nil || rows.Len() < 2 || !(rows.SumW() > 0) {
		if m.capture != nil {
			return matrixOutput{}, errors.NewCodedErrorWithDetails(errors.PULSE_MATRIX_INSUFFICIENT_N,
				"matrix "+plan.Name+": the SEM fit is undefined — its input has fewer than 2 complete rows or no weight — so the fit binds no model",
				map[string]any{"matrix": plan.Name})
		}
		in.semParts(&out, nil, nil, nil)
		semOrdinalScalars(out.Scalars, spec, nil)
		return out, nil
	}
	input := semOrdinalInput(rows, spec)
	fit, err := sem.FitOrdinal(spec.Model, input, spec.Estimator, spec.Options)
	if err != nil {
		return matrixOutput{}, semOrdinalError(plan.Name, spec, err)
	}
	if !fit.Converged {
		out.Warnings = append(out.Warnings, matrixWarning(errors.PULSE_MATRIX_NOT_CONVERGED,
			"matrix "+plan.Name+": "+fit.Status+" after the solver's iterations; the estimates are its last iterate",
			map[string]any{"matrix": plan.Name, "solver": "sem_" + string(fit.Options.Solver), "status": fit.Status, "iterations": fit.Iterations}))
	}
	if fit.Stats.DoF < 0 {
		out.Warnings = append(out.Warnings, matrixWarning(errors.PULSE_MATRIX_NOT_IDENTIFIED,
			"matrix "+plan.Name+": the model has more free parameters than the two-step statistics have moments (negative degrees of freedom); its estimates are not identified",
			map[string]any{"matrix": plan.Name, "dof": fit.Stats.DoF, "n_params": fit.Stats.NParams}))
	}
	out.Warnings = append(out.Warnings, semDefinedWarnings(plan.Name, fit.Defined)...)
	pos := make([]int, len(spec.Order))
	for a, i := range spec.Order {
		pos[i] = a
	}
	in.semParts(&out, fit,
		func(r, c int) float64 { return fit.Implied.At(pos[r], pos[c]) },
		func(r, c int) float64 { return fit.Sample.At(pos[r], pos[c]) })
	if fit.ImpliedMean != nil && in.WantAuxiliary() {
		out.Auxiliary["implied_means"] = in.Rect(semMeanColumns, func(r, _ int) float64 { return fit.ImpliedMean[pos[r]] })
		out.Auxiliary["observed_means"] = in.Rect(semMeanColumns, func(r, _ int) float64 { return fit.SampleMean[pos[r]] })
	}
	semOrdinalScalars(out.Scalars, spec, fit.Stats.Scaled)
	return out, nil
}

// semOrdinalInput is the two-step input of the slot's rows: columns in
// the model's observed order (SEMPlan.Order), each ordered column coded
// by its sorted distinct values.
func semOrdinalInput(rows *matrixRows, spec *vectors.SEMPlan) sem.TwoStepInput {
	obs := spec.Model.Observed
	k := len(obs)
	in := sem.TwoStepInput{Kinds: make([]sem.OrdinalKind, k), Levels: make([]int, k),
		Rows: make([][]float64, rows.Len()), Weights: make([]float64, rows.Len())}
	codes := make([]map[float64]int, k)
	for a, v := range obs {
		if !slices.Contains(spec.Ordered, v) {
			in.Kinds[a] = sem.KindNumeric
			continue
		}
		in.Kinds[a] = sem.KindOrdered
		var vals []float64
		for r := 0; r < rows.Len(); r++ {
			vals = append(vals, rows.Row(r)[spec.Order[a]])
		}
		slices.Sort(vals)
		vals = slices.Compact(vals)
		codes[a] = make(map[float64]int, len(vals))
		for c, x := range vals {
			codes[a][x] = c
		}
		in.Levels[a] = len(vals)
	}
	for r := 0; r < rows.Len(); r++ {
		row := rows.Row(r)
		x := make([]float64, k)
		for a := range obs {
			x[a] = row[spec.Order[a]]
			if codes[a] != nil {
				x[a] = float64(codes[a][x[a]])
			}
		}
		in.Rows[r] = x
		in.Weights[r] = rows.Weight(r)
	}
	return in
}

// semOrdinalError maps a refused ordinal fit to the slot's coded error:
// a two-step refusal by its reason (details.reason), anything else
// PULSE_MATRIX_SINGULAR (the fit is undefined on this data).
func semOrdinalError(name string, spec *vectors.SEMPlan, err error) error {
	details := map[string]any{"matrix": name, "estimator": string(spec.Estimator), "error": err.Error()}
	var oe *sem.OrdinalError
	if !stderrors.As(err, &oe) {
		return errors.NewCodedErrorWithDetails(errors.PULSE_MATRIX_SINGULAR,
			"matrix "+name+": the ordinal SEM fit is undefined: "+err.Error(), details)
	}
	details["reason"] = oe.Reason
	if oe.Var >= 0 && oe.Var < len(spec.Model.Observed) {
		details["variable"] = spec.Model.Observed[oe.Var]
	}
	code := errors.PULSE_MATRIX_SINGULAR
	switch oe.Reason {
	case sem.ReasonEmptyCategory, sem.ReasonNoData:
		code = errors.PULSE_MATRIX_INSUFFICIENT_N
	case sem.ReasonInvalidInput:
		code = errors.PROCESSING_INTERNAL
	}
	return errors.NewCodedErrorWithDetails(code, "matrix "+name+": the ordinal SEM fit is undefined: "+oe.Error(), details)
}

// semOrdinalScalars writes a WLSMV fit's scaled test into sc (nil sc:
// scalars skipped; nil scaled: null, an undefined input) — nothing
// under dwls.
func semOrdinalScalars(sc map[string]float64, spec *vectors.SEMPlan, scaled *sem.ScaledStats) {
	if sc == nil || spec.Estimator != sem.EstimatorWLSMV {
		return
	}
	keys := vectors.SEMOrdinalScalarKeys()
	vals := []float64{math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN()}
	if scaled != nil {
		vals = []float64{scaled.Chi2, scaled.Chi2P, scaled.ScalingFactor, scaled.ShiftParameter,
			scaled.Chi2Baseline, scaled.CFI, scaled.TLI, scaled.RMSEA}
	}
	for i, k := range keys {
		sc[k] = vals[i]
	}
}
