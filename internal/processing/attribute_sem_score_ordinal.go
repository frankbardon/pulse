package processing

import (
	"fmt"
	"maps"
	"slices"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/sem"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// attribute_sem_score_ordinal.go is ATTR_SEM_SCORE over an ordinal SEM
// fit (estimator dwls / wlsmv): lavaan's empirical Bayes modal scores
// (sem.NewEBMScorer) — what lavPredict computes for categorical
// indicators, and what method "regression" means there; "bartlett" is
// refused at decode and bind (vectors.SEMScoreOrdinalMethodRefusal).
//
// Inline, the fit is MAT_SEM's ordinal fit over the same rows: the
// PrePass buffers every fit-eligible row exactly as MAT_SEM's slot
// does (members in member order, a null member a listwise drop, the
// frequency weight passed through — an invalid one drops the row, a
// zero one keeps it — and, grouped, each level's rows, a null group a
// drop), Finalize codes each ordered member by its sorted distinct
// values over the (pooled) rows and fits sem.FitOrdinal /
// FitOrdinalGroups, the refusals MAT_SEM's (semOrdinalFault) as
// fit-scoped warnings. The coding — each ordered member's category
// values — travels with the fit (semScoreFit.cats), as a bound or saved
// ordinal fit's does (model.SEMFit.Categories), so a row is coded by
// the fit's categories, never its own cohort's: a value outside them
// has no category probability, and its row scores null, tallied into
// one PULSE_SEM_SCORE_LEVEL_UNSEEN warning per slot (details.variables,
// values per variable as rows). A null member scores null, as on an ML
// fit.

// semScoreOrdinalRows are an inline ordinal fit's buffered rows: every
// fit row (ungrouped) or each level's (grouped), in PrePass order.
type semScoreOrdinalRows struct {
	base   *matrixRows
	levels map[uint64]*matrixRows
}

func newSEMScoreOrdinalRows(p int, grouped bool) *semScoreOrdinalRows {
	o := &semScoreOrdinalRows{base: newMatrixRows(p, false)}
	if grouped {
		o.levels = map[uint64]*matrixRows{}
	}
	return o
}

// prePassOrdinal buffers one fit_on-admitted record (MAT_SEM's ordinal
// slot's row rule).
func (a *semScoreAttribute) prePassOrdinal(rec *Record) error {
	a.fill.fill(rec, a.x, nil)
	w := 1.0
	if a.weight != nil {
		v, reason := readWeight(rec, a.weight)
		w = v
		if reason != weighting.Valid {
			return nil // an invalid weight drops the row (matrixRows)
		}
	}
	if a.ord.levels == nil {
		a.ord.base.add(a.x, w)
		return nil
	}
	code, ok, level, err := a.groupCode(rec)
	if err != nil || !ok || !level {
		return err // a null group, or no level: a listwise drop
	}
	rows, ok := a.ord.levels[code]
	if !a.admitLevel(ok, len(a.ord.levels)) {
		return nil
	}
	if !ok {
		rows = newMatrixRows(len(a.members), false)
		a.ord.levels[code] = rows
	}
	rows.add(a.x, w)
	a.ord.base.add(a.x, w)
	return nil
}

// fitOrdinalModel fits MAT_SEM's ordinal model to the buffered rows:
// the closed fit with its coding, the caveats it owes (scored) and a
// refusal (every score null).
func (a *semScoreAttribute) fitOrdinalModel() (*semScoreFit, []*fitRefusal, *fitRefusal) {
	plan := a.cfg.Plan
	if plan.Grouped() {
		return a.fitOrdinalGroups()
	}
	rows := a.ord.base
	if rows.Len() < 2 || !(rows.SumW() > 0) {
		return nil, nil, &fitRefusal{code: errors.PULSE_MATRIX_INSUFFICIENT_N,
			msg:     fmt.Sprintf("the ordinal fit has %d complete rows; it needs 2 rows (and weight) at least, and every score is null", rows.Len()),
			details: map[string]any{"n": rows.Len(), "sum_weights": rows.SumW()}}
	}
	codes, levels := semOrdinalCoding([]*matrixRows{rows}, plan)
	fit, err := sem.FitOrdinal(plan.Model, semOrdinalInputOf(rows, plan, codes, levels), plan.Estimator, plan.Options)
	if err != nil {
		return nil, nil, ordinalFitRefusal(plan, err, nil, levels)
	}
	var notes []*fitRefusal
	if note := semConvergenceNote(fit.Converged, fit.Status, fit.Options.Solver, fit.Iterations); note != nil {
		notes = append(notes, note)
	}
	if ref := semIdentifiedRefusal(fit.Stats); ref != nil {
		return nil, notes, ref
	}
	params, _ := fit.Params()
	return &semScoreFit{models: []*sem.Model{plan.Model}, labels: []string{""}, codes: []uint64{0},
		params: []sem.Params{params}, cats: semOrdinalCategories(codes)}, notes, nil
}

// fitOrdinalGroups is fitOrdinalModel for a grouped plan: MAT_SEM's
// multi-group ordinal fit (finalizeSEMOrdinalGroups' refusals).
func (a *semScoreAttribute) fitOrdinalGroups() (*semScoreFit, []*fitRefusal, *fitRefusal) {
	plan := a.cfg.Plan
	codes := slices.Sorted(maps.Keys(a.ord.levels))
	labels := make([]string, len(codes))
	for i, c := range codes {
		labels[i] = a.levelLabel(c)
	}
	if len(codes) < 2 {
		return nil, nil, &fitRefusal{code: errors.PULSE_MATRIX_INSUFFICIENT_N,
			msg:     "params.group " + strconv.Quote(plan.Group) + " has fewer than two levels with fit rows; a multi-group fit needs two groups at least, and every score is null",
			details: map[string]any{"reason": "one_level", "group": plan.Group, "levels": labels}}
	}
	ref := 0
	if plan.GroupReference != "" {
		if ref = slices.Index(labels, plan.GroupReference); ref < 0 {
			return nil, nil, &fitRefusal{code: errors.PULSE_MATRIX_INSUFFICIENT_N,
				msg:     "params.group_reference " + strconv.Quote(plan.GroupReference) + " holds no fit rows; every score is null",
				details: map[string]any{"reason": "reference_absent", "group": plan.Group, "group_reference": plan.GroupReference, "levels": labels}}
		}
	}
	sets := make([]*matrixRows, len(codes))
	for g, c := range codes {
		rows := a.ord.levels[c]
		if rows.Len() < 2 || !(rows.SumW() > 0) {
			return nil, nil, &fitRefusal{code: errors.PULSE_MATRIX_INSUFFICIENT_N,
				msg:     fmt.Sprintf("group %q has %d fit rows; each group of an ordinal fit needs 2 rows (and weight) at least, and every score is null", labels[g], rows.Len()),
				details: map[string]any{"reason": "level_too_thin", "group": plan.Group, "level": labels[g], "n": rows.Len(), "sum_weights": rows.SumW()}}
		}
		sets[g] = rows
	}
	cmap, levels := semOrdinalCoding(sets, plan)
	groups := make([]sem.OrdinalGroup, len(codes))
	for g, c := range codes {
		level := g
		if plan.GroupLevels != nil {
			level = int(c)
		}
		groups[g] = sem.OrdinalGroup{Label: labels[g], In: semOrdinalInputOf(sets[g], plan, cmap, levels), Level: level, Reference: g == ref}
	}
	gf, err := sem.FitOrdinalGroups(plan.Model, groups, plan.Estimator, plan.Options)
	if err != nil {
		return nil, nil, ordinalFitRefusal(plan, err, labels, levels)
	}
	var notes []*fitRefusal
	if note := semConvergenceNote(gf.Converged, gf.Status, gf.Options.Solver, gf.Iterations); note != nil {
		notes = append(notes, note)
	}
	if ref := semIdentifiedRefusal(gf.Stats); ref != nil {
		return nil, notes, ref
	}
	out := &semScoreFit{labels: labels, codes: codes, cats: semOrdinalCategories(cmap)}
	for _, f := range gf.Groups {
		p, _ := f.Params()
		out.models = append(out.models, f.Model)
		out.params = append(out.params, p)
	}
	return out, notes, nil
}

// ordinalFitRefusal is a refused inline ordinal fit's fit-scoped
// refusal: MAT_SEM's code and details (semOrdinalFault), every score
// null.
func ordinalFitRefusal(plan *vectors.SEMPlan, err error, levels []string, cats []int) *fitRefusal {
	code, msg, details := semOrdinalFault(plan, err, levels, cats)
	return &fitRefusal{code: code, msg: msg + "; every score is null", details: details}
}

// semOrdinalCategories are semOrdinalCoding's codes as each ordered
// variable's category values ascending (nil for a numeric one).
func semOrdinalCategories(codes []map[float64]int) [][]float64 {
	out := make([][]float64, len(codes))
	for a, m := range codes {
		if m == nil {
			continue
		}
		out[a] = make([]float64, len(m))
		for x, c := range m {
			out[a][c] = x
		}
	}
	return out
}

// unseenCategoryWarning is the tally of rows an ordered value outside
// the fit's categories nulled (nil when none).
func (a *semScoreAttribute) unseenCategoryWarning() *types.ResponseWarning {
	if len(a.unseenCats) == 0 || a.fit == nil {
		return nil
	}
	ks := slices.Sorted(maps.Keys(a.unseenCats))
	obs := a.fit.models[0].Observed
	vars := make([]string, len(ks))
	rows := make([]int64, len(ks))
	cats := make([][]float64, len(ks))
	var total int64
	for i, k := range ks {
		vars[i], rows[i], cats[i] = obs[k], a.unseenCats[k], a.fit.cats[k]
		total += rows[i]
	}
	return &types.ResponseWarning{
		Code:    string(errors.PULSE_SEM_SCORE_LEVEL_UNSEEN),
		Message: a.label + ": " + strconv.FormatInt(total, 10) + " row(s) hold an ordered member value that is none of the ordinal fit's categories, so the row has no category probability to score; their scores are null",
		Details: map[string]any{"attribute": a.label, "type": string(types.ATTR_SEM_SCORE), "reason": "category_unseen",
			"variables": vars, "rows": rows, "categories": cats},
	}
}
