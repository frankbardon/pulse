package processing

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/sem"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
	"gonum.org/v1/gonum/mat"
)

// matrix_sem.go is MAT_SEM: a structural equation model fitted by
// maximum likelihood to the slot's merged co-moment (internal/sem), so
// the operator is streamable and mergeable like MAT_COVARIANCE.
//
// S is the members' population covariance (CoMoment.Cov(0): divisor W,
// semopy's numpy bias=True), reordered to the model's observed-variable
// order (vectors.SEMPlan.Order), and N is W — the row count unweighted,
// Σw under a frequency weight (exactly the fit on the expanded rows).
// The fit (sem.FitCov) and the fit statistics (sem.ComputeStats) are
// semopy's under the default options.
//
// Outputs: primary the model-implied covariance Σ(θ̂) over the members
// in member order (the slot's encoding); auxiliary.estimates the
// parameter table — MatrixKindRectangular, a row per semopy inspect
// row keyed "lval op rval", columns vectors.SEMEstimateColumns (SE, z and p
// null on a fixed parameter); auxiliary.residual_covariance S − Σ(θ̂);
// vectors.parameter (each row's parameter name: an auto "_bN" / "_cN"
// or the model's label) and vectors.free; scalars semScalarKeys.
//
// An undefined input (fewer than 2 rows, no mass, a zero-spread or
// non-finite member) nulls every figure — the co-moment warnings say
// why. A rank-deficient S (a member a linear combination of others —
// the one rule, singular_rule.go) refuses the matrix with
// PULSE_MATRIX_SINGULAR: semopy would substitute statsmodels'
// cov_nearest, Pulse never fits a matrix the data did not produce. A
// solver stop short of success (SLSQP's exit mode, or Fisher scoring
// before grad_tol) keeps the last iterate with PULSE_MATRIX_NOT_CONVERGED;
// a model with negative degrees of freedom is fitted as semopy fits it,
// with PULSE_MATRIX_NOT_IDENTIFIED.
func finalizeSEM(in *matrixFinalizeInput) (matrixOutput, error) {
	m := in.slot
	plan := in.Plan()
	spec := plan.SEM
	cm := in.CM
	cov := cm.Cov(0)
	out := matrixOutput{Warnings: m.warnings(cm, cov)}
	p := cov.N()
	k := len(spec.Model.Observed)
	defined := cm.N() >= 2 && cm.W() > 0 && judgeable(cov)
	if !defined {
		in.semParts(&out, nil, nil)
		return out, nil
	}
	if err := singularUnderRule(cov); err != nil {
		return matrixOutput{}, singularMatrixError(err, plan.Name, in.Members())
	}
	s := mat.NewSymDense(k, nil)
	for a := 0; a < k; a++ {
		for b := a; b < k; b++ {
			s.SetSym(a, b, cov.At(spec.Order[a], spec.Order[b]))
		}
	}
	fit, err := sem.FitCov(spec.Model, s, cm.W(), spec.Options)
	if err == nil {
		err = sem.ComputeStats(fit, spec.Desc)
	}
	if err != nil {
		return matrixOutput{}, errors.NewCodedErrorWithDetails(errors.PULSE_MATRIX_SINGULAR,
			"matrix "+plan.Name+": the SEM fit is undefined: "+err.Error(),
			map[string]any{"matrix": plan.Name, "error": err.Error()})
	}
	if !fit.Converged {
		out.Warnings = append(out.Warnings, matrixWarning(errors.PULSE_MATRIX_NOT_CONVERGED,
			"matrix "+plan.Name+": "+fit.Status+" after the solver's iterations; the estimates are its last iterate",
			map[string]any{"matrix": plan.Name, "solver": "sem_" + string(fit.Options.Solver), "status": fit.Status, "iterations": fit.Iterations}))
	}
	if fit.Stats.DoF < 0 {
		out.Warnings = append(out.Warnings, matrixWarning(errors.PULSE_MATRIX_NOT_IDENTIFIED,
			"matrix "+plan.Name+": the model has more free parameters than the covariance has distinct moments (negative degrees of freedom); it is fitted as semopy fits it, but its estimates are not identified",
			map[string]any{"matrix": plan.Name, "dof": fit.Stats.DoF, "n_params": fit.Stats.NParams}))
	}
	// Σ(θ̂) back in member order.
	pos := make([]int, p)
	for a, i := range spec.Order {
		pos[i] = a
	}
	in.semParts(&out, fit, func(r, c int) float64 { return fit.Implied.At(pos[r], pos[c]) })
	return out, nil
}

// semScalarKeys are MAT_SEM's scalars, in manifest order.
var semScalarKeys = []string{
	"dof", "dof_baseline", "chi2", "chi2_p_value", "chi2_baseline",
	"cfi", "gfi", "agfi", "nfi", "tli", "rmsea", "aic", "bic", "loglik",
	"objective", "n", "n_params", "iterations", "converged",
}

// semParts fills the outputs from fit and implied, its Σ(θ̂) in member
// coordinates (both nil: an undefined input, every figure null; the
// estimate rows still list the specified parameters).
func (in *matrixFinalizeInput) semParts(out *matrixOutput, fit *sem.Fit, implied func(r, c int) float64) {
	spec := in.Plan().SEM
	cov := in.CM.Cov(0)
	if implied == nil {
		out.Primary = in.Square(func(int, int) float64 { return math.NaN() })
	} else {
		out.Primary = in.Square(implied)
	}
	var rows []sem.Estimate
	if fit != nil {
		rows = fit.Estimates
	} else {
		rows = sem.SpecifiedRows(spec.Model)
	}
	if in.WantAuxiliary() {
		keys := make([]string, len(rows))
		values := make([][]float64, len(rows))
		for i, e := range rows {
			keys[i] = e.Key()
			if fit == nil {
				values[i] = nanVector(len(vectors.SEMEstimateColumns()))
				continue
			}
			values[i] = []float64{e.Value, e.StdValue, e.SE, e.Z, e.P}
		}
		resid := func(int, int) float64 { return math.NaN() }
		if implied != nil {
			resid = func(r, c int) float64 { return cov.At(r, c) - implied(r, c) }
		}
		out.Auxiliary = map[string]*types.MatrixValues{
			"estimates": {
				Kind:       types.MatrixKindRectangular,
				Encoding:   types.MatrixEncodingFull,
				RowKeys:    keys,
				ColumnKeys: vectors.SEMEstimateColumns(),
				Values:     values,
			},
			"residual_covariance": in.Square(resid),
		}
	}
	if in.WantVectors() {
		names := make([]string, len(rows))
		free := make([]bool, len(rows))
		for i, e := range rows {
			names[i], free[i] = e.Param, e.Free
		}
		out.Vectors = map[string]any{"parameter": names, "free": free}
	}
	if in.WantScalars() {
		sc := make(map[string]float64, len(semScalarKeys))
		for _, k := range semScalarKeys {
			sc[k] = math.NaN()
		}
		if fit != nil {
			st := fit.Stats
			conv := 0.0
			if fit.Converged {
				conv = 1
			}
			for k, v := range map[string]float64{
				"dof": st.DoF, "dof_baseline": st.DoFBaseline, "chi2": st.Chi2, "chi2_p_value": st.Chi2P,
				"chi2_baseline": st.Chi2Baseline, "cfi": st.CFI, "gfi": st.GFI, "agfi": st.AGFI, "nfi": st.NFI,
				"tli": st.TLI, "rmsea": st.RMSEA, "aic": st.AIC, "bic": st.BIC, "loglik": st.LogLik,
				"objective": fit.Objective, "n": fit.N, "n_params": float64(st.NParams),
				"iterations": float64(fit.Iterations), "converged": conv,
			} {
				sc[k] = v
			}
		}
		out.Scalars = sc
	}
}
