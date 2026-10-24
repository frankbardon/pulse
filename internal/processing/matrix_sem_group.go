package processing

import (
	"slices"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/sem"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
	"gonum.org/v1/gonum/mat"
)

// matrix_sem_group.go is MAT_SEM's joint multi-group fit
// (params.group): the slot keeps one BlockCoMoments per level of the
// group field beside its base state (every grouped row, once), so each
// group's mean and covariance come off the one fixed merge tree and the
// fit stays streamable and mergeable — partitions absorb level by level
// (the MAT_DISTANCE bucket pattern). A row whose group is null is a
// listwise drop: it reaches neither state.
//
// The levels are the group field's values that hold rows after the
// filters, in level order (a categorical's dictionary order, else
// ascending). Every group needs more rows than the model has observed
// variables and a positive-definite covariance; a group failing either,
// fewer than two groups, or a group_reference without rows fails the
// slot alone (PULSE_MATRIX_INSUFFICIENT_N / PULSE_MATRIX_SINGULAR). A
// frequency weight applies within each group: n_g = Σw over its rows.

// semGroupState is a grouped MAT_SEM slot's per-level state.
type semGroupState struct {
	field string
	// dict is the group field when categorical (its dictionary keys the
	// level labels and guards the index), nil otherwise.
	dict *encoding.Field
	// buckets holds one co-moment set per level code: the dictionary
	// index, else the integer value.
	buckets map[uint64]*BlockCoMoments
}

func newSEMGroupState(plan vectors.Matrix, schema *encoding.Schema) *semGroupState {
	st := &semGroupState{field: plan.SEM.Group, buckets: map[uint64]*BlockCoMoments{}}
	if schema != nil {
		if f := schema.Field(plan.SEM.Group); f != nil && f.Type.IsCategorical() {
			st.dict = f
		}
	}
	return st
}

// update folds one filter-passing record into the base state and its
// level's bucket; a null group is a listwise drop.
func (s *semGroupState) update(m *matrixSlot, r *Record) error {
	v, ok := r.NumericValue(s.field)
	if !ok {
		m.dropped++
		return nil
	}
	if s.dict != nil {
		if err := encx.CategoricalIndexFault(s.dict, v); err != nil {
			return err
		}
	}
	code := uint64(v)
	w, err := m.readRow(r)
	if err != nil {
		return err
	}
	if err := m.state.Add(r, m.x, w); err != nil {
		return err
	}
	b, ok := s.buckets[code]
	if !ok {
		if b, err = NewBlockCoMoments(len(m.cols), linalg.Listwise); err != nil {
			return err
		}
		s.buckets[code] = b
	}
	return b.Add(r, m.x, w)
}

// merge absorbs o's buckets (a level one side alone saw moves over
// whole).
func (s *semGroupState) merge(o *semGroupState) error {
	for k, b := range o.buckets {
		if mine, ok := s.buckets[k]; ok {
			if err := mine.Absorb(b); err != nil {
				return err
			}
			continue
		}
		s.buckets[k] = b
	}
	o.buckets = map[uint64]*BlockCoMoments{}
	return nil
}

// label is a level code's label: the dictionary entry (GROUP_CATEGORY's
// key), else the integer.
func (s *semGroupState) label(plan *vectors.SEMPlan, code uint64) string {
	if s.dict != nil && int(code) < len(plan.GroupLevels) {
		return plan.GroupLevels[code]
	}
	return strconv.FormatUint(code, 10)
}

// semGroupRefusal is a grouped fit's data-dependent refusal: the slot
// fails alone with code.
func semGroupRefusal(code errors.Code, name, reason, msg string, extra map[string]any) error {
	d := map[string]any{"matrix": name, "reason": reason}
	for k, v := range extra {
		d[k] = v
	}
	return errors.NewCodedErrorWithDetails(code, "matrix "+name+": "+msg, d)
}

// finalizeSEMGroups is finalizeSEM for a grouped plan.
func finalizeSEMGroups(in *matrixFinalizeInput) (matrixOutput, error) {
	m := in.slot
	plan := in.Plan()
	spec := plan.SEM
	gs := m.semGroups
	out := matrixOutput{Warnings: m.warnings(in.CM, in.CM.Cov(0))}
	k := len(spec.Model.Observed)
	codes := make([]uint64, 0, len(gs.buckets))
	for c := range gs.buckets {
		codes = append(codes, c)
	}
	slices.Sort(codes)
	labels := make([]string, len(codes))
	for i, c := range codes {
		labels[i] = gs.label(spec, c)
	}
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
	moments := make([]sem.GroupMoments, len(codes))
	for g, c := range codes {
		cm, err := gs.buckets[c].Tree()
		if err != nil {
			return matrixOutput{}, err
		}
		if cm.N() <= int64(k) || !(cm.W() > float64(k)) {
			return matrixOutput{}, semGroupRefusal(errors.PULSE_MATRIX_INSUFFICIENT_N, plan.Name, "level_too_thin",
				"group "+strconv.Quote(labels[g])+" has "+strconv.FormatInt(cm.N(), 10)+" rows; each group needs more rows (and weight) than the model's "+strconv.Itoa(k)+" observed variables",
				map[string]any{"group": spec.Group, "level": labels[g], "n": cm.N(), "sum_weights": cm.W(), "observed": k})
		}
		cov, mv := cm.Cov(0), cm.Mean()
		s := mat.NewSymDense(k, nil)
		mean := make([]float64, k)
		for a := 0; a < k; a++ {
			mean[a] = mv.At(spec.Order[a])
			for b := a; b < k; b++ {
				s.SetSym(a, b, cov.At(spec.Order[a], spec.Order[b]))
			}
		}
		if !sem.PositiveDefinite(s) {
			return matrixOutput{}, semGroupRefusal(errors.PULSE_MATRIX_SINGULAR, plan.Name, "level_singular",
				"group "+strconv.Quote(labels[g])+"'s sample covariance is not positive definite; a multi-group fit has no nearest substitute",
				map[string]any{"group": spec.Group, "level": labels[g]})
		}
		// The level index — a categorical's dictionary index,
		// packed_bool's 0 / 1 — places the group in the per-group
		// modifiers (a level without rows leaves its position unused);
		// an integer field takes none (resolveSEMGroup).
		level := g
		if spec.GroupLevels != nil {
			level = int(c)
		}
		moments[g] = sem.GroupMoments{Label: labels[g], S: s, Raw: s, Mean: mean, N: cm.W(), Level: level, Reference: g == ref}
	}
	fit, err := sem.FitGroups(spec.Model, moments, spec.Options)
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
			"matrix "+plan.Name+": the model has more free parameters than the groups' covariances and means have distinct moments (negative degrees of freedom); its estimates are not identified",
			map[string]any{"matrix": plan.Name, "dof": fit.Stats.DoF, "n_params": fit.Stats.NParams}))
	}
	pos := make([]int, len(spec.Order))
	for a, i := range spec.Order {
		pos[i] = a
	}
	implied := func(g int) func(r, c int) float64 {
		return func(r, c int) float64 { return fit.Groups[g].Implied.At(pos[r], pos[c]) }
	}
	out.Primary = in.Square(implied(ref))
	if in.WantAuxiliary() {
		out.Auxiliary = semGroupAuxiliary(in, fit, labels, pos, implied)
	}
	if in.WantVectors() {
		var names, groups, ulabels []string
		var free []bool
		for g, gf := range fit.Groups {
			for _, e := range gf.Estimates {
				names, groups, ulabels = append(names, e.Param), append(groups, labels[g]), append(ulabels, e.Label)
				free = append(free, e.Free)
			}
		}
		out.Vectors = map[string]any{"parameter": names, "free": free, "group": groups, "label": ulabels}
	}
	if in.WantScalars() {
		st := fit.Stats
		conv := 0.0
		if fit.Converged {
			conv = 1
		}
		sc := map[string]float64{
			"dof": st.DoF, "dof_baseline": st.DoFBaseline, "chi2": st.Chi2, "chi2_p_value": st.Chi2P,
			"chi2_baseline": st.Chi2Baseline, "cfi": st.CFI, "gfi": st.GFI, "agfi": st.AGFI, "nfi": st.NFI,
			"tli": st.TLI, "rmsea": st.RMSEA, "aic": st.AIC, "bic": st.BIC, "loglik": st.LogLik,
			"objective": fit.Objective, "n": fit.N, "n_params": float64(st.NParams),
			"iterations": float64(fit.Iterations), "converged": conv,
		}
		for g, l := range labels {
			sc[vectors.SEMGroupChi2Key(l)] = fit.Chi2Group[g]
			sc[vectors.SEMGroupNKey(l)] = fit.Groups[g].N
		}
		out.Scalars = sc
	}
	return out, nil
}

// semGroupEstimateKey is a grouped fit's estimate row key: "[level] "
// before the ungrouped key.
func semGroupEstimateKey(level string, e sem.Estimate) string { return "[" + level + "] " + e.Key() }

// semGroupAuxiliary builds a grouped fit's auxiliaries: the estimates
// (every group's rows in level order, keys "[level] lval op rval",
// lavaan's columns) and per level its implied and residual covariance
// and implied and observed means (vectors.SEMGroupAuxiliaryKeys).
func semGroupAuxiliary(in *matrixFinalizeInput, fit *sem.GroupFit, labels []string, pos []int, implied func(int) func(r, c int) float64) map[string]*types.MatrixValues {
	spec := in.Plan().SEM
	columns := vectors.SEMEstimateColumnsOf(spec)
	var keys []string
	var values [][]float64
	var fields []map[string]any
	records := in.Plan().Encoding == types.MatrixEncodingRecords
	for g, gf := range fit.Groups {
		for _, e := range gf.Estimates {
			keys = append(keys, semGroupEstimateKey(labels[g], e))
			values = append(values, []float64{e.Value, e.StdValue, e.SE, e.Z, e.P, e.CILower, e.CIUpper})
			if records {
				fields = append(fields, map[string]any{"lval": e.LVal, "op": e.Op, "rval": e.RVal, "parameter": e.Param,
					"free": e.Free, "group": labels[g], "label": e.Label})
			}
		}
	}
	aux := map[string]*types.MatrixValues{
		"estimates": {
			Kind:         types.MatrixKindRectangular,
			Encoding:     types.MatrixEncodingFull,
			RowKeys:      keys,
			ColumnKeys:   columns,
			Values:       values,
			RecordFields: fields,
		},
	}
	for g, l := range labels {
		gf := fit.Groups[g]
		imp := implied(g)
		ks := vectors.SEMGroupAuxiliaryKeys(l)
		aux[ks[0]] = in.Square(imp)
		aux[ks[1]] = in.Square(func(r, c int) float64 { return gf.Sample.At(pos[r], pos[c]) - imp(r, c) })
		aux[ks[2]] = in.Rect(semMeanColumns, func(r, _ int) float64 { return gf.ImpliedMean[pos[r]] })
		aux[ks[3]] = in.Rect(semMeanColumns, func(r, _ int) float64 { return gf.SampleMean[pos[r]] })
	}
	return aux
}
