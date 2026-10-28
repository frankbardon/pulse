package processing

import (
	stderrors "errors"
	"math"
	"slices"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
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
	codes, levels := semOrdinalCoding([]*matrixRows{rows}, spec)
	input := semOrdinalInputOf(rows, spec, codes, levels)
	fit, err := sem.FitOrdinal(spec.Model, input, spec.Estimator, spec.Options)
	if err != nil {
		return matrixOutput{}, semOrdinalError(plan.Name, spec, err, nil, levels)
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
	if m.capture != nil {
		// A Compose SEM fit request's battery: bind the point fit, its
		// coding and thresholds (EBM scores, ATTR_SEM_SCORE).
		sf := m.semCaptureFit(in.CM, fit.Converged, fit.Status, fit.Iterations, fit.Options, fit.Stats, nil)
		sf.N, sf.SumWeights = int64(rows.Len()), rows.SumW()
		semCaptureOrdinal(sf, spec, codes)
		params, _ := fit.Params()
		sf.Groups = []model.SEMGroup{semCaptureOrdinalGroup("", rows, params, spec)}
		m.capture.OfferSEM(sf)
	}
	var boot *sem.Bootstrap
	if spec.Bootstrap() {
		// Every replicate refits the two-step statistics from its own
		// rows at this fit's coding (the seed reads the raw rows).
		rows := sem.BootstrapRows(fit)
		samples := []sem.BootstrapSample{{Rows: semCodedRows(input)}}
		raw := []sem.BootstrapSample{{Rows: semModelRows(in.Rows, spec.Order)}}
		if boot, err = semBootstrapWith(in, samples, raw, rows, semBootstrapOrdinal(spec, input)); err != nil {
			return matrixOutput{}, err
		}
		copy(fit.Estimates, rows)
		copy(fit.Defined, rows[len(fit.Estimates):])
	}
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
	semBootstrapScalars(out.Scalars, boot)
	return out, nil
}

// semOrdinalCoding codes each ordered member by its sorted distinct
// values over every row set (the POOLED rows of a grouped fit: every
// group shares the categories, so one a group lacks is its
// empty_category refusal): codes[a] maps a value to its code (nil for
// a numeric member), levels[a] the category count, in the model's
// observed order.
func semOrdinalCoding(sets []*matrixRows, spec *vectors.SEMPlan) ([]map[float64]int, []int) {
	obs := spec.Model.Observed
	codes := make([]map[float64]int, len(obs))
	levels := make([]int, len(obs))
	for a, v := range obs {
		if !slices.Contains(spec.Ordered, v) {
			continue
		}
		var vals []float64
		for _, rows := range sets {
			for r := 0; r < rows.Len(); r++ {
				vals = append(vals, rows.Row(r)[spec.Order[a]])
			}
		}
		slices.Sort(vals)
		vals = slices.Compact(vals)
		codes[a] = make(map[float64]int, len(vals))
		for c, x := range vals {
			codes[a][x] = c
		}
		levels[a] = len(vals)
	}
	return codes, levels
}

// semOrdinalInputOf is the two-step input of rows: columns in the
// model's observed order (SEMPlan.Order), each ordered column coded by
// codes (semOrdinalCoding).
func semOrdinalInputOf(rows *matrixRows, spec *vectors.SEMPlan, codes []map[float64]int, levels []int) sem.TwoStepInput {
	obs := spec.Model.Observed
	k := len(obs)
	in := sem.TwoStepInput{Kinds: make([]sem.OrdinalKind, k), Levels: append([]int(nil), levels...),
		Rows: make([][]float64, rows.Len()), Weights: make([]float64, rows.Len())}
	for a := range obs {
		in.Kinds[a] = sem.KindNumeric
		if codes[a] != nil {
			in.Kinds[a] = sem.KindOrdered
		}
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

// semCaptureOrdinal records an ordinal fit's ordered variables
// (Observed order) and each one's category values ascending (the codes
// of semOrdinalCoding) on a captured SEM fit.
func semCaptureOrdinal(sf *model.SEMFit, spec *vectors.SEMPlan, codes []map[float64]int) {
	for a, v := range spec.Model.Observed {
		if codes[a] == nil {
			continue
		}
		cats := make([]float64, len(codes[a]))
		for x, c := range codes[a] {
			cats[c] = x
		}
		sf.Ordered = append(sf.Ordered, v)
		sf.Categories = append(sf.Categories, cats)
	}
}

// semCaptureOrdinalGroup is one group of a captured ordinal SEM fit: its
// level, its rows' count and weight, its parameter matrices and its
// thresholds per ordered variable.
func semCaptureOrdinalGroup(level string, rows *matrixRows, p sem.Params, spec *vectors.SEMPlan) model.SEMGroup {
	g := model.SEMGroup{Level: level, N: int64(rows.Len()), SumWeights: rows.SumW(),
		Lambda: p.Lambda, Beta: p.Beta, Psi: p.Psi, Theta: p.Theta, Nu: p.Nu, Alpha: p.Alpha, SampleMean: p.SampleMean}
	for a, v := range spec.Model.Observed {
		if spec.Model.IsOrdered(v) {
			g.Thresholds = append(g.Thresholds, append([]float64(nil), p.Thresholds[a]...))
		}
	}
	return g
}

// semCodedRows is a two-step input's rows, row-major (the bootstrap's
// sample).
func semCodedRows(in sem.TwoStepInput) []float64 {
	out := make([]float64, 0, len(in.Rows)*len(in.Kinds))
	for _, r := range in.Rows {
		out = append(out, r...)
	}
	return out
}

// semBootstrapOrdinal is an ordinal plan's bootstrap refit: its
// estimator at in's coding.
func semBootstrapOrdinal(spec *vectors.SEMPlan, in sem.TwoStepInput) *sem.BootstrapOrdinal {
	return &sem.BootstrapOrdinal{Estimator: spec.Estimator, Kinds: in.Kinds, Levels: in.Levels}
}

// semOrdinalError maps a refused ordinal fit to the slot's coded error
// (cats: each observed variable's category count, semOrdinalCoding):
// a two-step refusal by its reason (details.reason; a grouped fit's
// details.level is the group's label, levels the labels in fit order),
// a group_partial threshold the data do not hold
// PULSE_MATRIX_INSUFFICIENT_N (reason partial_threshold_absent),
// anything else PULSE_MATRIX_SINGULAR (the fit is undefined on this
// data).
func semOrdinalError(name string, spec *vectors.SEMPlan, err error, levels []string, cats []int) error {
	code, msg, details := semOrdinalFault(spec, err, levels, cats)
	details["matrix"] = name
	return errors.NewCodedErrorWithDetails(code, "matrix "+name+": "+msg, details)
}

// semOrdinalFault is semOrdinalError's code, message (unprefixed) and
// details (no slot key) — shared with ATTR_SEM_SCORE's inline ordinal
// fit, whose refusals are fit-scoped warnings.
func semOrdinalFault(spec *vectors.SEMPlan, err error, levels []string, cats []int) (errors.Code, string, map[string]any) {
	details := map[string]any{"estimator": string(spec.Estimator), "error": err.Error()}
	var ge *sem.GroupSpecError
	if stderrors.As(err, &ge) {
		details["reason"], details["param"], details["value"] = "partial_threshold_absent", ge.Param, ge.Value
		return errors.PULSE_MATRIX_INSUFFICIENT_N, "params." + ge.Error(), details
	}
	var oe *sem.OrdinalError
	if !stderrors.As(err, &oe) {
		return errors.PULSE_MATRIX_SINGULAR, "the ordinal SEM fit is undefined: " + err.Error(), details
	}
	details["reason"] = oe.Reason
	if oe.Var >= 0 && oe.Var < len(spec.Model.Observed) {
		details["variable"] = spec.Model.Observed[oe.Var]
	}
	msg := oe.Error()
	if oe.Group >= 0 && oe.Group < len(levels) {
		details["group"], details["level"] = spec.Group, levels[oe.Group]
		msg += " (group " + spec.Group + " = " + levels[oe.Group] + ")"
	}
	code := errors.PULSE_MATRIX_SINGULAR
	switch oe.Reason {
	case sem.ReasonEmptyCategory, sem.ReasonNoData:
		code = errors.PULSE_MATRIX_INSUFFICIENT_N
	case sem.ReasonInvalidInput:
		code = errors.PROCESSING_INTERNAL
	case sem.ReasonTooManyCategories:
		code = errors.PULSE_MATRIX_TOO_MANY_CATEGORIES
		details["limit"] = sem.MaxOrderedCategories
		if oe.Var >= 0 && oe.Var < len(cats) {
			details["categories"] = cats[oe.Var] // the member's distinct values
		}
		msg += "; an ordered member is a rating scale — fit a continuous measure by the ML estimator, or recode it into at most " +
			strconv.Itoa(sem.MaxOrderedCategories) + " categories"
	}
	return code, "the ordinal SEM fit is undefined: " + msg, details
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

// finalizeSEMOrdinalGroups is finalizeSEMOrdinal for a grouped plan
// (params.group): lavaan's multi-group DWLS / WLSMV (sem.FitOrdinalGroups)
// over each level's buffered rows (semGroupState.rows), the levels
// finalizeSEMGroups' (level order, group_reference, one_level /
// reference_absent refusals). Each ordered member is coded over the
// POOLED rows, so a category one level lacks fails the slot alone with
// PULSE_MATRIX_INSUFFICIENT_N (reason empty_category, details.level). A
// level with fewer than 2 rows (or no weight) is level_too_thin. The
// outputs are finalizeSEMGroups' (per-level tables carrying the `x | tk`
// and `x ~*~ x` rows), plus semOrdinalScalars; params.resample redraws
// each level's rows within the level at the pooled coding.
func finalizeSEMOrdinalGroups(in *matrixFinalizeInput) (matrixOutput, error) {
	m := in.slot
	plan := in.Plan()
	spec := plan.SEM
	gs := m.semGroups
	out := matrixOutput{Warnings: m.warnings(in.CM, in.CM.Cov(0))}
	codes := make([]uint64, 0, len(gs.rows))
	for c := range gs.rows {
		codes = append(codes, c)
	}
	slices.Sort(codes)
	labels := make([]string, len(codes))
	for i, c := range codes {
		labels[i] = gs.label(spec, c)
	}
	emptyLabels, emptyLevels := semEmptyLevels(spec.GroupLevels, func(c uint64) bool { _, ok := gs.rows[c]; return ok })
	out.Warnings = append(out.Warnings, semGroupLevelWarnings(plan.Name, spec.Group, emptyLabels, gs.undeclared)...)
	if len(codes) < 2 {
		return matrixOutput{}, semGroupRefusal(errors.PULSE_MATRIX_INSUFFICIENT_N, plan.Name, "one_level",
			"params.group "+strconv.Quote(spec.Group)+" has fewer than two levels with rows; a multi-group fit needs two groups at least",
			map[string]any{"group": spec.Group, "levels": labels})
	}
	ref := 0
	if spec.GroupReference != "" {
		ref = slices.Index(labels, spec.GroupReference)
		if ref < 0 {
			return matrixOutput{}, semGroupRefusal(errors.PULSE_MATRIX_INSUFFICIENT_N, plan.Name, "reference_absent",
				"params.group_reference "+strconv.Quote(spec.GroupReference)+" holds no rows",
				map[string]any{"group": spec.Group, "group_reference": spec.GroupReference, "levels": labels})
		}
	}
	sets := make([]*matrixRows, len(codes))
	for g, c := range codes {
		rows := gs.rows[c]
		if rows.Len() < 2 || !(rows.SumW() > 0) {
			return matrixOutput{}, semGroupRefusal(errors.PULSE_MATRIX_INSUFFICIENT_N, plan.Name, "level_too_thin",
				"group "+strconv.Quote(labels[g])+" has "+strconv.Itoa(rows.Len())+" rows; each group of an ordinal fit needs 2 rows (and weight) at least",
				map[string]any{"group": spec.Group, "level": labels[g], "n": rows.Len(), "sum_weights": rows.SumW()})
		}
		sets[g] = rows
	}
	cmap, levels := semOrdinalCoding(sets, spec)
	groups := make([]sem.OrdinalGroup, len(codes))
	for g, c := range codes {
		level := g
		if spec.GroupLevels != nil {
			level = int(c)
		}
		groups[g] = sem.OrdinalGroup{Label: labels[g], In: semOrdinalInputOf(sets[g], spec, cmap, levels), Level: level, Reference: g == ref}
	}
	fit, err := sem.FitOrdinalGroups(spec.Model, groups, spec.Estimator, spec.Options)
	if err != nil {
		return matrixOutput{}, semOrdinalError(plan.Name, spec, err, labels, levels)
	}
	if !fit.Converged {
		out.Warnings = append(out.Warnings, matrixWarning(errors.PULSE_MATRIX_NOT_CONVERGED,
			"matrix "+plan.Name+": "+fit.Status+" after the solver's iterations; the estimates are its last iterate",
			map[string]any{"matrix": plan.Name, "solver": "sem_" + string(fit.Options.Solver), "status": fit.Status, "iterations": fit.Iterations}))
	}
	if fit.Stats.DoF < 0 {
		out.Warnings = append(out.Warnings, matrixWarning(errors.PULSE_MATRIX_NOT_IDENTIFIED,
			"matrix "+plan.Name+": the model has more free parameters than the groups' two-step statistics have moments (negative degrees of freedom); its estimates are not identified",
			map[string]any{"matrix": plan.Name, "dof": fit.Stats.DoF, "n_params": fit.Stats.NParams}))
	}
	fitted := make([]int, len(groups))
	for g := range groups {
		fitted[g] = groups[g].Level
	}
	out.Warnings = append(out.Warnings, semGroupDefinedWarnings(plan.Name, spec.Group, spec.Model, emptyLabels, emptyLevels, fitted, fit.Defined)...)
	if m.capture != nil {
		// A Compose SEM fit request's battery: bind the point fit, each
		// group's own parameters and thresholds at the pooled coding.
		sf := m.semCaptureFit(in.CM, fit.Converged, fit.Status, fit.Iterations, fit.Options, fit.Stats, nil)
		sf.N, sf.SumWeights = 0, 0
		semCaptureOrdinal(sf, spec, cmap)
		for g := range codes {
			params, _ := fit.Groups[g].Params()
			sf.Groups = append(sf.Groups, semCaptureOrdinalGroup(labels[g], sets[g], params, spec))
			sf.N += int64(sets[g].Len())
			sf.SumWeights += sets[g].SumW()
		}
		m.capture.OfferSEM(sf)
	}
	var boot *sem.Bootstrap
	if spec.Bootstrap() {
		samples := make([]sem.BootstrapSample, len(codes))
		raw := make([]sem.BootstrapSample, len(codes))
		for g, gr := range groups {
			samples[g] = sem.BootstrapSample{Rows: semCodedRows(gr.In), Label: gr.Label, Level: gr.Level, Reference: gr.Reference}
			raw[g] = sem.BootstrapSample{Rows: semModelRows(sets[g], spec.Order)}
		}
		if boot, err = semGroupBootstrap(in, fit, samples, raw, semBootstrapOrdinal(spec, groups[ref].In)); err != nil {
			return matrixOutput{}, err
		}
	}
	semGroupOutputs(in, &out, fit, labels, ref, boot)
	semOrdinalScalars(out.Scalars, spec, fit.Stats.Scaled)
	return out, nil
}
