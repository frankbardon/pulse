package processing

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/internal/processing/regression"
	"github.com/frankbardon/pulse/internal/sem"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/linalg"
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
// An undefined input (fewer than 2 rows, no mass, a non-finite member,
// or a zero-spread one under params.singular "refuse") nulls every
// figure — the co-moment warnings say why. A rank-deficient S (a member
// a linear combination of others — the one rule, singular_rule.go) is
// fitted as semopy's default fit fits it under params.singular
// "nearest" (the default): raw while its Cholesky factors, else
// semopy's substitute, statsmodels' cov_nearest (sem.CovNearest), with
// PULSE_MATRIX_NEAREST_PD. A zero-spread member (raw, or the zeros of a
// standardized one) has no substitute — semopy raises LinAlgError — so
// it refuses the matrix with PULSE_MATRIX_SINGULAR. params.singular
// "refuse" refuses every rank-deficient S with PULSE_MATRIX_SINGULAR. A
// solver stop short of success (SLSQP's exit mode, or Fisher scoring
// before grad_tol) keeps the last iterate with PULSE_MATRIX_NOT_CONVERGED;
// a model with negative degrees of freedom is fitted as semopy fits it,
// with PULSE_MATRIX_NOT_IDENTIFIED.
//
// A mean structure (params.meanstructure, or a `~ 1` line) switches the
// fit to lavaan's conventions (sem.Model.Lavaan): the co-moment's means
// (in model order) join S, the objective gains the mean term, the
// default solver converges to the optimum, the scalars are lavaan's,
// the estimates gain `~1` rows and the Wald ci_lower / ci_upper
// columns, and auxiliary implied_means / observed_means (member order,
// one "mean" column) are emitted. A `name := expr` line (a defined
// parameter, sem/defined.go) switches to lavaan's conventions too,
// mean structure or not, and appends a row "name := expr" per line
// after the parameter table: its value at the estimate and the delta
// method's SE, z, p and interval (vectors.parameter its name, free
// false). A covariance-only model without one is the semopy fit above,
// bit for bit. params.group fits one joint
// multi-group model instead (finalizeSEMGroups, matrix_sem_group.go).
func finalizeSEM(in *matrixFinalizeInput) (matrixOutput, error) {
	if in.Plan().SEM.Grouped() {
		return finalizeSEMGroups(in)
	}
	m := in.slot
	plan := in.Plan()
	spec := plan.SEM
	cm := in.CM
	cov := cm.Cov(0)
	out := matrixOutput{Warnings: m.warnings(cm, cov)}
	p := cov.N()
	k := len(spec.Model.Observed)
	nearest := spec.Singular == vectors.SEMSingularNearest
	// Under nearest a zero-spread member is not an undefined input but
	// a singular S without a substitute: cov_nearest refuses it, as
	// semopy's raises, so the slot fails rather than nulling.
	zeroOK := nearest
	defined := cm.N() >= 2 && cm.W() > 0 && semJudgeable(cov, zeroOK)
	if !defined && m.capture != nil {
		// A Compose SEM fit request's battery has no fit to bind: the
		// fit fails on its data (its slot alone), never binds nulls.
		code, why := errors.PULSE_MATRIX_INSUFFICIENT_N, "has fewer than 2 complete rows or no weight"
		if cm.N() >= 2 && cm.W() > 0 {
			code, why = errors.PULSE_MATRIX_SINGULAR, "has a member without finite spread"
		}
		return matrixOutput{}, errors.NewCodedErrorWithDetails(code,
			"matrix "+plan.Name+": the SEM fit is undefined — its input "+why+" — so the fit binds no model",
			map[string]any{"matrix": plan.Name, "n": cm.N(), "sum_weights": cm.W()})
	}
	if !defined {
		in.semParts(&out, nil, nil, nil)
		return out, nil
	}
	singular := singularUnderRule(cov)
	if singular != nil && !nearest {
		return matrixOutput{}, singularMatrixError(singular, plan.Name, in.Members())
	}
	s := mat.NewSymDense(k, nil)
	for a := 0; a < k; a++ {
		for b := a; b < k; b++ {
			s.SetSym(a, b, cov.At(spec.Order[a], spec.Order[b]))
		}
	}
	// semopy substitutes only when its Cholesky (load_cov's dpotrf)
	// fails. The one singular rule is the wider test: a near-exact
	// dependency whose factorization still succeeds is fitted raw under
	// nearest, as semopy fits it, with no warning.
	raw := s
	var nearestMin *float64
	if nearest && !sem.PositiveDefinite(s) {
		adj, err := sem.CovNearest(s, sem.NearestThreshold)
		if err != nil {
			return matrixOutput{}, errors.NewCodedErrorWithDetails(errors.PULSE_MATRIX_SINGULAR,
				"matrix "+plan.Name+": no nearest positive-definite substitute: "+err.Error(),
				map[string]any{"matrix": plan.Name, "error": err.Error()})
		}
		s = adj.Cov
		nearestMin = &adj.MinEigenvalue
		out.Warnings = append(out.Warnings, matrixWarning(errors.PULSE_MATRIX_NEAREST_PD,
			"matrix "+plan.Name+": the sample covariance is not positive definite; the model is fitted to its nearest positive-definite substitute (statsmodels cov_nearest, threshold 0.01, as semopy), and every figure is against that adjusted matrix",
			map[string]any{"matrix": plan.Name, "n": cm.W(), "p": k, "min_eigenvalue": adj.MinEigenvalue}))
	}
	// The means (a mean structure only) in model order.
	var mean []float64
	if spec.Model.MeanStructure {
		mv := cm.Mean()
		mean = make([]float64, k)
		for a := 0; a < k; a++ {
			mean[a] = mv.At(spec.Order[a])
		}
	}
	fit, err := sem.FitMoments(spec.Model, s, raw, mean, cm.W(), spec.Options)
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
	out.Warnings = append(out.Warnings, semDefinedWarnings(plan.Name, fit.Defined)...)
	if m.capture != nil {
		// A Compose SEM fit request's battery: bind the point fit.
		mv := cm.Mean()
		ybar := make([]float64, k)
		for a := 0; a < k; a++ {
			ybar[a] = mv.At(spec.Order[a])
		}
		sf := m.semCaptureFit(cm, fit.Converged, fit.Status, fit.Iterations, fit.Options, fit.Stats, nearestMin)
		params, _ := fit.Params()
		if params.SampleMean == nil {
			params.SampleMean = ybar
		}
		sf.Groups = []model.SEMGroup{semCaptureGroup("", cm, params)}
		m.capture.OfferSEM(sf)
	}
	var boot *sem.Bootstrap
	if spec.Bootstrap() {
		rows := sem.BootstrapRows(fit)
		samples := []sem.BootstrapSample{{Rows: semModelRows(in.Rows, spec.Order)}}
		if boot, err = semBootstrap(in, samples, rows); err != nil {
			return matrixOutput{}, err
		}
		copy(fit.Estimates, rows)
		copy(fit.Defined, rows[len(fit.Estimates):])
	}
	// Σ(θ̂) back in member order.
	pos := make([]int, p)
	for a, i := range spec.Order {
		pos[i] = a
	}
	in.semParts(&out, fit,
		func(r, c int) float64 { return fit.Implied.At(pos[r], pos[c]) },
		func(r, c int) float64 { return s.At(pos[r], pos[c]) })
	if fit.ImpliedMean != nil && in.WantAuxiliary() {
		out.Auxiliary["implied_means"] = in.Rect(semMeanColumns, func(r, _ int) float64 { return fit.ImpliedMean[pos[r]] })
		out.Auxiliary["observed_means"] = in.Rect(semMeanColumns, func(r, _ int) float64 { return fit.SampleMean[pos[r]] })
	}
	semBootstrapScalars(out.Scalars, boot)
	return out, nil
}

// semModelRows is a buffered slot's admitted rows, row-major, each
// reordered from member order to the model's observed order (order:
// SEMPlan.Order).
func semModelRows(rows *matrixRows, order []int) []float64 {
	k := len(order)
	out := make([]float64, 0, rows.Len()*k)
	for r := 0; r < rows.Len(); r++ {
		row := rows.Row(r)
		for _, i := range order {
			out = append(out, row[i])
		}
	}
	return out
}

// semBootstrap runs the plan's bootstrap (params.resample "bootstrap")
// over samples — the fit's rows, one sample per group in fit order —
// and writes its SE, z, p and percentile interval into rows, the fit's
// table rows (sem.BootstrapRows / BootstrapGroupRows), in place. Fewer
// than sem.BootstrapMinSuccesses successful replicates fail the slot
// alone with PULSE_MATRIX_NOT_CONVERGED (details.reason
// bootstrap_too_few).
func semBootstrap(in *matrixFinalizeInput, samples []sem.BootstrapSample, rows []sem.Estimate) (*sem.Bootstrap, error) {
	plan := in.Plan()
	spec := plan.SEM
	seed := spec.RNGSeed
	if seed == 0 {
		seed = semDeriveSeed(in.slot.semSeedSpec, samples, len(spec.Order))
	}
	b, err := sem.RunBootstrap(spec.Model, samples, spec.Options, sem.BootstrapOptions{
		Iters: spec.BootstrapIters, Seed: uint64(seed), Level: spec.CILevel,
		Standardize: plan.Members.Standardize != "", Ctx: in.slot.ctx,
	}, rows)
	if stderrors.Is(err, context.Canceled) || stderrors.Is(err, context.DeadlineExceeded) {
		return nil, err // the request's deadline or cancel, as is
	}
	var be *sem.BootstrapError
	if stderrors.As(err, &be) {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_MATRIX_NOT_CONVERGED,
			"matrix "+plan.Name+": "+be.Error()+"; the refit is unstable on this data (raise bootstrap_iters, simplify the model, or drop resample)",
			map[string]any{"matrix": plan.Name, "reason": "bootstrap_too_few", "bootstrap_requested": be.Requested,
				"bootstrap_attempted": be.Attempted, "bootstrap_succeeded": be.Succeeded, "minimum": sem.BootstrapMinSuccesses})
	}
	if err != nil {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"matrix "+plan.Name+": the bootstrap is misconfigured: "+err.Error(),
			map[string]any{"matrix": plan.Name, "error": err.Error()})
	}
	b.Apply(rows)
	return b, nil
}

// semSeedSpec is the wire form a MAT_SEM bootstrap's rng_seed 0
// derivation hashes: the slot's spec with params.rng_seed dropped, so
// an absent seed and an explicit 0 derive alike (REG_RESAMPLE's
// omitempty RNGSeed does the same).
func semSeedSpec(spec types.MatrixSpec) []byte {
	if len(spec.Params) > 0 {
		var params map[string]json.RawMessage
		// A lenient look: the MAT_SEM decoder reads (and records)
		// rng_seed; this only drops it from the hashed wire form.
		if paramread.Probe(spec.Params, &params) == nil {
			delete(params, "rng_seed")
			if b, err := json.Marshal(params); err == nil {
				spec.Params = b
			}
		}
	}
	body, err := types.MarshalFinite(&spec)
	if err != nil {
		return nil
	}
	return body
}

// semDeriveSeed is a MAT_SEM bootstrap's seed at rng_seed 0:
// REG_RESAMPLE's derivation (regression.DeriveSeed) over the slot's
// wire form (spec) and the rows the fit reads — every sample's rows in
// fit order, each row in model order (k values) — so the same request
// over the same data draws the same replicates, worker and shard count
// aside (the slot is buffered: its rows keep record order).
func semDeriveSeed(spec []byte, samples []sem.BootstrapSample, k int) int64 {
	n := 0
	for _, s := range samples {
		n += len(s.Rows) / k
	}
	return regression.DeriveSeed(spec, n, func(word func(v float64, ok bool)) {
		for _, s := range samples {
			for _, v := range s.Rows {
				word(v, true)
			}
		}
	})
}

// semBootstrapScalars writes a bootstrap's replicate counts into sc
// (nil sc: scalars skipped; nil b: no bootstrap ran).
func semBootstrapScalars(sc map[string]float64, b *sem.Bootstrap) {
	if sc == nil || b == nil {
		return
	}
	keys := vectors.SEMBootstrapScalarKeys()
	sc[keys[0]], sc[keys[1]] = float64(b.Requested), float64(b.Succeeded)
}

// semDefinedWarnings is PULSE_MATRIX_DEFINED_NONFINITE when a := row
// is undefined at the estimate (its figures null), else nothing.
func semDefinedWarnings(name string, rows []sem.Estimate) []*types.ResponseWarning {
	var bad []string
	for _, e := range rows {
		if math.IsNaN(e.Value) {
			bad = append(bad, e.LVal)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	return []*types.ResponseWarning{matrixWarning(errors.PULSE_MATRIX_DEFINED_NONFINITE,
		"matrix "+name+": defined parameter(s) "+strings.Join(bad, ", ")+" are not finite at the estimate (a division by zero, or a log / sqrt outside its domain); their rows are null",
		map[string]any{"matrix": name, "defined": bad})}
}

// semJudgeable is judgeable, except that zeroOK admits a diagonal
// entry of exactly 0 (a zero-spread member).
func semJudgeable(s *linalg.Sym, zeroOK bool) bool {
	if !zeroOK {
		return judgeable(s)
	}
	for i := 0; i < s.N(); i++ {
		if d := s.At(i, i); d < 0 || math.IsNaN(d) || math.IsInf(d, 0) {
			return false
		}
	}
	return true
}

// semMeanColumns is the one column of MAT_SEM's implied_means /
// observed_means (a mean structure only): a member's mean.
var semMeanColumns = []string{"mean"}

// semScalarKeys are MAT_SEM's scalars, in manifest order.
var semScalarKeys = []string{
	"dof", "dof_baseline", "chi2", "chi2_p_value", "chi2_baseline",
	"cfi", "gfi", "agfi", "nfi", "tli", "rmsea", "aic", "bic", "loglik",
	"objective", "n", "n_params", "iterations", "converged",
}

// semParts fills the outputs from fit, implied (its Σ(θ̂)) and sample
// (the S it was fitted to — the data's, or the nearest substitute),
// both in member coordinates (all nil: an undefined input, every figure
// null; the estimate rows still list the specified parameters).
func (in *matrixFinalizeInput) semParts(out *matrixOutput, fit *sem.Fit, implied, sample func(r, c int) float64) {
	spec := in.Plan().SEM
	if implied == nil {
		out.Primary = in.Square(func(int, int) float64 { return math.NaN() })
	} else {
		out.Primary = in.Square(implied)
	}
	// The := rows (defined parameters) follow the parameter table.
	var rows []sem.Estimate
	if fit != nil {
		rows = append(fit.Estimates, fit.Defined...)
	} else {
		rows = append(sem.SpecifiedRows(spec.Model), sem.SpecifiedDefined(spec.Model)...)
	}
	if in.WantAuxiliary() {
		columns := vectors.SEMEstimateColumnsOf(spec)
		lavaan := spec.Lavaan()
		keys := make([]string, len(rows))
		values := make([][]float64, len(rows))
		for i, e := range rows {
			keys[i] = e.Key()
			if fit == nil {
				values[i] = nanVector(len(columns))
				continue
			}
			values[i] = []float64{e.Value, e.StdValue, e.SE, e.Z, e.P}
			if lavaan {
				values[i] = append(values[i], e.CILower, e.CIUpper)
			}
		}
		resid := func(int, int) float64 { return math.NaN() }
		if implied != nil {
			resid = func(r, c int) float64 { return sample(r, c) - implied(r, c) }
		}
		out.Auxiliary = map[string]*types.MatrixValues{
			"estimates": {
				Kind:       types.MatrixKindRectangular,
				Encoding:   types.MatrixEncodingFull,
				RowKeys:    keys,
				ColumnKeys: columns,
				Values:     values,
			},
			"residual_covariance": in.Square(resid),
		}
		if spec.HasMeans() && fit == nil {
			nan := func(int, int) float64 { return math.NaN() }
			out.Auxiliary["implied_means"] = in.Rect(semMeanColumns, nan)
			out.Auxiliary["observed_means"] = in.Rect(semMeanColumns, nan)
		}
		if in.Plan().Encoding == types.MatrixEncodingRecords {
			// A records row is self-describing: the path's parts and
			// the parameter's name / freedom ride beside its figures.
			fields := make([]map[string]any, len(rows))
			for i, e := range rows {
				fields[i] = map[string]any{"lval": e.LVal, "op": e.Op, "rval": e.RVal, "parameter": e.Param, "free": e.Free}
			}
			out.Auxiliary["estimates"].RecordFields = fields
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
		if spec.Bootstrap() {
			for _, k := range vectors.SEMBootstrapScalarKeys() {
				sc[k] = math.NaN()
			}
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

// semCaptureFit is the SEM fit a Compose SEM fit request's MAT_SEM slot
// binds (model.SEMFit), less its groups: the plan's model and options,
// the slot's member labels and columns, the weight basis and the fit's
// solver stop and statistics. nearestMin is the nearest-PD substitute's
// minimum eigenvalue (nil when the fit used the sample covariance).
func (m *matrixSlot) semCaptureFit(cm *linalg.CoMoment, converged bool, status string, iterations int, opt sem.Options, st sem.Stats, nearestMin *float64) *model.SEMFit {
	spec := m.plan.SEM
	info := string(opt.Information)
	if info == "" {
		info = string(sem.InformationDense)
	}
	sf := &model.SEMFit{
		Operator:       string(m.plan.Type),
		Model:          spec.Desc,
		Labels:         append([]string(nil), m.plan.FitLabels()...),
		Members:        append([]string(nil), m.cols...),
		Observed:       append([]string(nil), spec.Model.Observed...),
		Inner:          append([]string(nil), spec.Model.Inner...),
		Latents:        spec.Model.Latents(),
		Estimator:      "ml",
		Lavaan:         spec.Model.Lavaan(),
		MeanStructure:  spec.MeanStructure,
		Group:          spec.Group,
		GroupReference: spec.GroupReference,
		GroupEqual:     append([]string(nil), spec.GroupEqual...),
		GroupPartial:   append([]string(nil), spec.GroupPartial...),
		Solver:         string(opt.Solver),
		Information:    info,
		Basis:          vectorBasisOf(weighting.BasisOf(m.weight)),
		N:              cm.N(),
		SumWeights:     cm.W(),
		DoF:            st.DoF,
		NParams:        st.NParams,
		Converged:      converged,
		Status:         status,
		Iterations:     iterations,
	}
	if len(sf.GroupEqual) == 0 {
		sf.GroupEqual = nil
	}
	if len(sf.GroupPartial) == 0 {
		sf.GroupPartial = nil
	}
	if nearestMin != nil {
		sf.NearestPD, sf.MinEigenvalue = true, *nearestMin
	}
	return sf
}

// semCaptureGroup is one group of a captured SEM fit: its level, its
// fit rows' count and weight (cm) and its parameter matrices.
func semCaptureGroup(level string, cm *linalg.CoMoment, p sem.Params) model.SEMGroup {
	return model.SEMGroup{Level: level, N: cm.N(), SumWeights: cm.W(),
		Lambda: p.Lambda, Beta: p.Beta, Psi: p.Psi, Theta: p.Theta, Nu: p.Nu, Alpha: p.Alpha, SampleMean: p.SampleMean}
}
