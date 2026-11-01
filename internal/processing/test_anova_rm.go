package processing

import (
	"fmt"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/types"
)

// anovaRMRow implements TEST_ANOVA_RM as a buffered row test:
// one-way repeated-measures ANOVA over a (SubjectField × SplitBy)
// design.
//
// Algorithm — balanced design only (one observation per subject per
// condition):
//  1. Buffer the wide subject × condition table, one value per cell.
//  2. Drop subjects with any missing condition (warn).
//  3. Compute grand mean μ, per-condition mean μ_j, per-subject mean μ_i.
//  4. Sum of squares decomposition:
//     SS_total   = Σ (x_ij − μ)²
//     SS_between = k · Σ (μ_i − μ)²            (between-subjects)
//     SS_cond    = n · Σ (μ_j − μ)²            (treatment)
//     SS_error   = SS_total − SS_between − SS_cond
//  5. df_cond = k − 1, df_error = (n−1)(k−1)
//     F = (SS_cond / df_cond) / (SS_error / df_error)
//     p = fSurvival(F, df_cond, df_error)
//
// Sphericity correction (Greenhouse-Geisser / Huynh-Feldt) is out of
// scope for v1 — documented in the depth ANOVA plan.
type anovaRMRow struct {
	spec      *types.Test
	schema    *encoding.Schema
	valueCol  string
	condition string
	subject   string
	alpha     float64

	// cells maps subject → (condition → value). Drop-subject semantics
	// applies when any condition is missing.
	cells          map[string]map[string]float64
	subjectOrder   []string
	conditionOrder []string
	conditionSeen  map[string]struct{}
}

func newAnovaRMRow(spec *types.Test, schema *encoding.Schema) (RowTest, error) {
	if cerr := fieldaccept.RequiredSlotsRefusal(spec.Type, false, fieldaccept.TestSlotsOf(spec)); cerr != nil {
		return nil, cerr
	}
	alpha := spec.Alpha
	if alpha == 0 {
		alpha = 0.05
	}
	if alpha <= 0 || alpha >= 1 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INVALID_ALPHA,
			fmt.Sprintf("alpha %g not in (0, 1)", spec.Alpha),
			map[string]any{"alpha": spec.Alpha})
	}
	if schema != nil {
		if cerr := testFieldTypeRefusal(spec, schema); cerr != nil {
			return nil, cerr
		}
		if cerr := fieldaccept.Check(string(spec.Type), fieldaccept.SlotSplitBy, spec.SplitBy, schema); cerr != nil {
			return nil, cerr
		}
		if cerr := fieldaccept.Check(string(spec.Type), fieldaccept.SlotSubject, spec.SubjectField, schema); cerr != nil {
			return nil, cerr
		}
	}
	return &anovaRMRow{
		spec:          spec,
		schema:        schema,
		valueCol:      spec.Field,
		condition:     spec.SplitBy,
		subject:       spec.SubjectField,
		alpha:         alpha,
		cells:         make(map[string]map[string]float64),
		conditionSeen: make(map[string]struct{}),
	}, nil
}

func (a *anovaRMRow) UpdateRow(record *Record) error {
	v, ok := record.NumericValue(a.valueCol)
	if !ok {
		return nil
	}
	cond, ok, err := record.LevelValueChecked(a.condition)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	subj, ok, err := record.LevelValueChecked(a.subject)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if _, seen := a.conditionSeen[cond]; !seen {
		a.conditionSeen[cond] = struct{}{}
		a.conditionOrder = append(a.conditionOrder, cond)
	}
	row, exists := a.cells[subj]
	if !exists {
		row = make(map[string]float64)
		a.cells[subj] = row
		a.subjectOrder = append(a.subjectOrder, subj)
	}
	row[cond] = v
	return nil
}

func (a *anovaRMRow) Finalize() (*types.TestResult, error) {
	defer a.reset()
	conditions := append([]string(nil), a.conditionOrder...)
	sort.Strings(conditions)
	k := len(conditions)
	if k < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_SPLIT_GROUPS_LT_2,
			fmt.Sprintf("TEST_ANOVA_RM requires ≥ 2 conditions, got %d", k),
			map[string]any{"conditions": conditions, "min_required": 2})
	}
	// Filter to complete subjects.
	subjects := make([]string, 0, len(a.subjectOrder))
	dropped := 0
	for _, subj := range a.subjectOrder {
		row := a.cells[subj]
		complete := true
		for _, cond := range conditions {
			if _, ok := row[cond]; !ok {
				complete = false
				break
			}
		}
		if complete {
			subjects = append(subjects, subj)
		} else {
			dropped++
		}
	}
	n := len(subjects)
	if n < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_ANOVA_RM requires ≥ 2 complete subjects, got %d", n),
			map[string]any{"complete_subjects": n, "incomplete": dropped})
	}
	// Compute means.
	grand := 0.0
	subjectMeans := make([]float64, n)
	conditionMeans := make([]float64, k)
	for i, subj := range subjects {
		row := a.cells[subj]
		var rowSum float64
		for j, cond := range conditions {
			v := row[cond]
			grand += v
			rowSum += v
			conditionMeans[j] += v
		}
		subjectMeans[i] = rowSum / float64(k)
	}
	grand /= float64(n * k)
	for j := range conditionMeans {
		conditionMeans[j] /= float64(n)
	}
	// SS decomposition.
	var ssTotal, ssBetween, ssCond float64
	for i, subj := range subjects {
		row := a.cells[subj]
		for _, cond := range conditions {
			d := row[cond] - grand
			ssTotal += float64(d * d)
		}
		ds := subjectMeans[i] - grand
		ssBetween += float64(ds * ds)
	}
	ssBetween = float64(ssBetween * float64(k))
	for _, cm := range conditionMeans {
		dc := cm - grand
		ssCond += float64(dc * dc)
	}
	ssCond = float64(ssCond * float64(n))
	ssError := ssTotal - ssBetween - ssCond
	dfCond := float64(k - 1)
	dfError := float64((n - 1) * (k - 1))
	if dfError <= 0 || ssError <= 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			"TEST_ANOVA_RM: zero error degrees of freedom or degenerate SS_error",
			map[string]any{"n": n, "k": k, "ss_error": ssError})
	}
	msCond := ssCond / dfCond
	msError := ssError / dfError
	F := msCond / msError
	p := fSurvival(F, dfCond, dfError)
	res := &types.TestResult{
		Label:      testLabel(a.spec),
		Type:       types.TEST_ANOVA_RM,
		Variant:    "balanced",
		Statistic:  F,
		DF:         dfCond,
		PValue:     p,
		Alpha:      a.alpha,
		RejectNull: p < a.alpha,
		Details: map[string]any{
			"conditions":          conditions,
			"complete_subjects":   n,
			"dropped_subjects":    dropped,
			"condition_means":     conditionMeans,
			"grand_mean":          grand,
			"ss_total":            ssTotal,
			"ss_between_subjects": ssBetween,
			"ss_treatment":        ssCond,
			"ss_error":            ssError,
			"df_treatment":        dfCond,
			"df_error":            dfError,
		},
	}
	setEffectSize(res.Details, "partial_eta_squared", partialEtaSquared(ssCond, ssError))
	if dropped > 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf("%s: %d subject(s) had missing conditions and were dropped",
			errors.PULSE_TEST_SUBJECT_MISSING, dropped))
	}
	return res, nil
}

func (a *anovaRMRow) reset() {
	a.cells = make(map[string]map[string]float64)
	a.subjectOrder = nil
	a.conditionOrder = nil
	a.conditionSeen = make(map[string]struct{})
}
