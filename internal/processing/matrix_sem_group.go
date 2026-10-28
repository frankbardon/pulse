package processing

import (
	"math"
	"slices"
	"strconv"
	"strings"

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
// ascending); a declared level (a categorical's dictionary entry,
// packed_bool's 0 / 1) without rows warns PULSE_MATRIX_GROUP_LEVEL_EMPTY,
// and a := reading its per-group label is null (DEFINED_NONFINITE,
// reason empty_group). Every group needs more rows than the model has observed
// variables and a positive-definite covariance; a group failing either,
// fewer than two groups, or a group_reference without rows fails the
// slot alone (PULSE_MATRIX_INSUFFICIENT_N / PULSE_MATRIX_SINGULAR). A
// frequency weight applies within each group: n_g = Σw over its rows.

// A DERIVED group (params.group naming a column the request computes —
// vectors.SEMPlan.GroupDerived) reads the record's computed value
// (Record.NumericValue, the one pass): with params.group_levels a value
// equal to a declared one is that level (code = its declared position,
// so the c(...) position), without them an integral value is its own
// level (ascending); NaN is null (a listwise drop, as a computed member
// reads it), and any other value — undeclared, or not integral — is a
// listwise drop counted once per slot in PULSE_MATRIX_GROUP_LEVEL_UNDECLARED.

// semGroupState is a grouped MAT_SEM slot's per-level state.
type semGroupState struct {
	field string
	// dict is the group field when categorical (its dictionary keys the
	// level labels and guards the index), nil otherwise.
	dict *encoding.Field
	// derived marks a group the request computes; declared maps each
	// params.group_levels value to its position (nil: the observed
	// integral values are the levels, coded by semObservedCode).
	derived  bool
	declared map[float64]uint64
	// undeclared counts the rows dropped as no level of a derived
	// group (merged like the slot's dropped count).
	undeclared int64
	// buckets holds one co-moment set per level code: the dictionary
	// index, else the integer value (a derived group: the declared
	// position, else semObservedCode).
	buckets map[uint64]*BlockCoMoments
	// rows holds each level's admitted rows (member order) on a
	// BUFFERED plan — a bootstrap, which redraws them within each level
	// — and is nil on a streaming one. Rows do not merge: a buffered
	// spec is never Mergeable.
	rows map[uint64]*matrixRows
}

func newSEMGroupState(plan vectors.Matrix, schema *encoding.Schema) *semGroupState {
	st := &semGroupState{field: plan.SEM.Group, buckets: map[uint64]*BlockCoMoments{}}
	if !plan.Streamable {
		st.rows = map[uint64]*matrixRows{}
	}
	if plan.SEM.GroupDerived {
		st.derived = true
		if plan.SEM.GroupDeclared != nil {
			st.declared = make(map[float64]uint64, len(plan.SEM.GroupDeclared))
			for i, l := range plan.SEM.GroupDeclared {
				st.declared[l.Value] = uint64(i)
			}
		}
		return st
	}
	if schema != nil {
		if f := schema.Field(plan.SEM.Group); f != nil && f.Type.IsCategorical() {
			st.dict = f
		}
	}
	return st
}

// semObservedCode is an undeclared derived group's level code for the
// integral value v: its int64 with the sign bit flipped, so codes sort
// as the values do (semObservedValue inverts it).
func semObservedCode(v float64) uint64 { return uint64(int64(v)) ^ (1 << 63) }

func semObservedValue(code uint64) int64 { return int64(code ^ (1 << 63)) }

// update folds one filter-passing record into the base state and its
// level's bucket; a null group is a listwise drop, and so is a derived
// group's value that is no level (counted undeclared).
func (s *semGroupState) update(m *matrixSlot, r *Record) error {
	v, ok := r.NumericValue(s.field)
	if !ok || (s.derived && math.IsNaN(v)) {
		m.dropped++
		return nil
	}
	if s.dict != nil {
		if err := encx.CategoricalIndexFault(s.dict, v); err != nil {
			return err
		}
	}
	code := uint64(v)
	if s.derived {
		var level bool
		if s.declared != nil {
			code, level = s.declared[v]
		} else if level = vectors.SEMGroupIntegral(v); level {
			code = semObservedCode(v)
		}
		if !level {
			m.dropped++
			s.undeclared++
			return nil
		}
	}
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
	if s.rows != nil {
		rb, ok := s.rows[code]
		if !ok {
			rb = newMatrixRows(len(m.cols), false)
			s.rows[code] = rb
		}
		rb.add(m.x, w)
	}
	return b.Add(r, m.x, w)
}

// buffered reports whether s keeps its levels' rows (a nil s: no).
func (s *semGroupState) buffered() bool { return s != nil && s.rows != nil }

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
	s.undeclared += o.undeclared
	o.undeclared = 0
	return nil
}

// label is a level code's label: the dictionary entry (GROUP_CATEGORY's
// key) or a declared derived level's label, else the integer.
func (s *semGroupState) label(plan *vectors.SEMPlan, code uint64) string {
	if (s.dict != nil || s.declared != nil) && int(code) < len(plan.GroupLevels) {
		return plan.GroupLevels[code]
	}
	if s.derived {
		return strconv.FormatInt(semObservedValue(code), 10)
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
	emptyLabels, emptyLevels := semEmptyLevels(spec.GroupLevels, func(c uint64) bool { _, ok := gs.buckets[c]; return ok })
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
	moments := make([]sem.GroupMoments, len(codes))
	levelCMs := make([]*linalg.CoMoment, len(codes))
	for g, c := range codes {
		cm, err := gs.buckets[c].Tree()
		if err != nil {
			return matrixOutput{}, err
		}
		levelCMs[g] = cm
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
	fitted := make([]int, len(moments))
	for g := range moments {
		fitted[g] = moments[g].Level
	}
	out.Warnings = append(out.Warnings, semGroupDefinedWarnings(plan.Name, spec.Group, spec.Model, emptyLabels, emptyLevels, fitted, fit.Defined)...)
	if m.capture != nil {
		// A Compose SEM fit request's battery: bind the point fit, each
		// group's own parameters.
		sf := m.semCaptureFit(in.CM, fit.Converged, fit.Status, fit.Iterations, fit.Options, fit.Stats, nil)
		for g := range codes {
			params, _ := fit.Groups[g].Params()
			if params.SampleMean == nil {
				params.SampleMean = append([]float64(nil), moments[g].Mean...)
			}
			sf.Groups = append(sf.Groups, semCaptureGroup(labels[g], levelCMs[g], params))
		}
		m.capture.OfferSEM(sf)
	}
	var boot *sem.Bootstrap
	if spec.Bootstrap() {
		// Each level's rows are redrawn within the level, so every
		// replicate keeps each n_g.
		samples := make([]sem.BootstrapSample, len(codes))
		for g, c := range codes {
			samples[g] = sem.BootstrapSample{Rows: semModelRows(gs.rows[c], spec.Order),
				Label: moments[g].Label, Level: moments[g].Level, Reference: moments[g].Reference}
		}
		if boot, err = semGroupBootstrap(in, fit, samples, samples, nil); err != nil {
			return matrixOutput{}, err
		}
	}
	semGroupOutputs(in, &out, fit, labels, ref, boot)
	return out, nil
}

// semGroupBootstrap bootstraps a grouped fit (semBootstrapWith over its
// table rows, BootstrapGroupRows) and writes the figures back into
// every group's estimates and the := rows.
func semGroupBootstrap(in *matrixFinalizeInput, fit *sem.GroupFit, samples, seedSamples []sem.BootstrapSample, ord *sem.BootstrapOrdinal) (*sem.Bootstrap, error) {
	rows := sem.BootstrapGroupRows(fit)
	boot, err := semBootstrapWith(in, samples, seedSamples, rows, ord)
	if err != nil {
		return nil, err
	}
	at := 0
	for _, gf := range fit.Groups {
		at += copy(gf.Estimates, rows[at:])
	}
	copy(fit.Defined, rows[at:])
	return boot, nil
}

// semGroupOutputs writes a grouped fit's parts into out: the primary
// (the reference level's implied covariance), the auxiliaries
// (semGroupAuxiliary), the vectors (parameter, free, group, label) and
// the scalars (the joint statistics, per-level chi2_group_ / n_group_,
// the bootstrap's counts).
func semGroupOutputs(in *matrixFinalizeInput, out *matrixOutput, fit *sem.GroupFit, labels []string, ref int, boot *sem.Bootstrap) {
	spec := in.Plan().SEM
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
		// vectors.group is each row's level, null on a := row (a
		// defined parameter belongs to no group).
		var names, ulabels []string
		var groups []any
		var free []bool
		for g, gf := range fit.Groups {
			for _, e := range gf.Estimates {
				names, groups, ulabels = append(names, e.Param), append(groups, labels[g]), append(ulabels, e.Label)
				free = append(free, e.Free)
			}
		}
		for _, e := range fit.Defined {
			names, groups, ulabels = append(names, e.Param), append(groups, nil), append(ulabels, e.Label)
			free = append(free, false)
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
		semBootstrapScalars(sc, boot)
		out.Scalars = sc
	}
}

// semGroupEstimateKey is a grouped fit's estimate row key: "[level] "
// before the ungrouped key.
func semGroupEstimateKey(level string, e sem.Estimate) string { return "[" + level + "] " + e.Key() }

// semGroupAuxiliary builds a grouped fit's auxiliaries: the estimates
// (every group's rows in level order, keys "[level] lval op rval",
// lavaan's columns, then the := rows unprefixed) and per level its implied and residual covariance
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
	// The := rows close the table, unprefixed: a defined parameter reads
	// the joint parameters, so it belongs to no group (group null).
	for _, e := range fit.Defined {
		keys = append(keys, e.Key())
		values = append(values, []float64{e.Value, e.StdValue, e.SE, e.Z, e.P, e.CILower, e.CIUpper})
		if records {
			fields = append(fields, map[string]any{"lval": e.LVal, "op": e.Op, "rval": e.RVal, "parameter": e.Param,
				"free": false, "group": nil, "label": e.Label})
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

// semEmptyLevels lists the declared levels (level code i ↔ declared[i])
// for which has reports no rows: their labels and codes, in level
// order. A group field without declared levels (an integer field, whose
// levels are its observed values) has none.
func semEmptyLevels(declared []string, has func(code uint64) bool) (labels []string, levels []int) {
	for i, l := range declared {
		if !has(uint64(i)) {
			labels, levels = append(labels, l), append(levels, i)
		}
	}
	return labels, levels
}

// semGroupLevelWarnings are a grouped slot's level warnings, in order:
// PULSE_MATRIX_GROUP_LEVEL_EMPTY over its empty declared levels, then
// PULSE_MATRIX_GROUP_LEVEL_UNDECLARED over the rows a derived group
// dropped as no level (undeclared, the count); each at most once.
func semGroupLevelWarnings(name, field string, empty []string, undeclared int64) []*types.ResponseWarning {
	var out []*types.ResponseWarning
	if w := semGroupLevelEmpty(name, field, empty); w != nil {
		out = append(out, w)
	}
	if undeclared > 0 {
		out = append(out, matrixWarning(errors.PULSE_MATRIX_GROUP_LEVEL_UNDECLARED,
			"matrix "+name+": "+strconv.FormatInt(undeclared, 10)+" row(s) of computed group "+strconv.Quote(field)+" hold a value that is no level of the fit (not a declared group_levels value, or not an integer); they are dropped listwise",
			map[string]any{"matrix": name, "group_field": field, "count": undeclared}))
	}
	return out
}

// semGroupLevelEmpty is PULSE_MATRIX_GROUP_LEVEL_EMPTY over a grouped
// slot's empty levels (nil when there are none): one per slot.
func semGroupLevelEmpty(name, field string, levels []string) *types.ResponseWarning {
	if len(levels) == 0 {
		return nil
	}
	return matrixWarning(errors.PULSE_MATRIX_GROUP_LEVEL_EMPTY,
		"matrix "+name+": level(s) "+strings.Join(levels, ", ")+" of group field "+strconv.Quote(field)+" hold no rows after the filters; they are no group of the fit (no estimate rows, no chi2_group_ / n_group_ scalars)",
		map[string]any{"matrix": name, "group_field": field, "levels": slices.Clone(levels)})
}

// semGroupDefinedWarnings is a grouped fit's := warnings: a label only
// an empty level names (emptyLabels / emptyLevels from semEmptyLevels;
// fitted the levels of the fitted groups) is absent from the fit, so
// each such label a null := row reads warns PULSE_MATRIX_DEFINED_NONFINITE
// with reason empty_group, in level then label order; every other null
// row warns semDefinedWarnings' plain one.
func semGroupDefinedWarnings(name, field string, model *sem.Model, emptyLabels []string, emptyLevels, fitted []int, rows []sem.Estimate) []*types.ResponseWarning {
	present := map[string]bool{}
	for _, l := range fitted {
		for _, lab := range model.LevelLabels(l) {
			present[lab] = true
		}
	}
	reads := model.DefinedReads()
	explained := map[string]bool{}
	var out []*types.ResponseWarning
	seen := map[string]bool{}
	for i, lv := range emptyLevels {
		for _, lab := range model.LevelLabels(lv) {
			if present[lab] || seen[lab] {
				continue
			}
			seen[lab] = true
			var defined []string
			for _, e := range rows {
				if math.IsNaN(e.Value) && slices.Contains(reads[e.LVal], lab) {
					defined = append(defined, e.LVal)
					explained[e.LVal] = true
				}
			}
			if len(defined) == 0 {
				continue
			}
			out = append(out, matrixWarning(errors.PULSE_MATRIX_DEFINED_NONFINITE,
				"matrix "+name+": defined parameter(s) "+strings.Join(defined, ", ")+" read label "+lab+" of level "+strconv.Quote(emptyLabels[i])+" of group field "+strconv.Quote(field)+", which holds no rows after the filters; their rows are null",
				map[string]any{"matrix": name, "reason": "empty_group", "label": lab, "level": emptyLabels[i], "group_field": field, "defined": defined}))
		}
	}
	var rest []sem.Estimate
	for _, e := range rows {
		if !explained[e.LVal] {
			rest = append(rest, e)
		}
	}
	return append(out, semDefinedWarnings(name, rest)...)
}
