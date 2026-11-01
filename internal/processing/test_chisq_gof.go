package processing

import (
	"fmt"
	"math"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// chiSqParams is TEST_CHISQ's params object. Expected (label →
// proportion) selects the goodness-of-fit form; absent (or null), the
// test is the independence form, unchanged.
type chiSqParams struct {
	Expected map[string]float64 `json:"expected"`
}

// decodeChiSqParams reads TEST_CHISQ's params through paramread.
func decodeChiSqParams(spec *types.Test) (chiSqParams, error) {
	var params chiSqParams
	if err := paramread.DecodeOptional(paramread.Test, string(types.TEST_CHISQ), spec.Params, &params); err != nil {
		return params, errors.NewCodedError(errors.PROCESSING_CONFIG, "TEST_CHISQ params: "+err.Error())
	}
	return params, nil
}

// chiSqGOFRow is TEST_CHISQ's goodness-of-fit form (params.expected):
// Pearson's χ² of a one-way table — the rows field's levels — against
// the expected proportions, df = k − 1 for the k labels expected lists.
// A listed label the data never shows counts as observed 0; an observed
// level expected does not list is refused at Finalize
// (PULSE_TEST_EXPECTED_LABEL_MISSING) — the proportions are never
// renormalised. Labels are reported sorted byte-wise.
//
// Weighted exactly as the independence form: the table holds Σw per
// level; frequency reads it as is, probability scales it to the
// table's Kish n_eff (T*_i = n_eff·Σw_i/Σw) against n_eff·p_i. With
// p_i the same shares, it is the arithmetic of OVERLAY_CHISQ_VS_POP on
// the same counts (R's chisq.test(x, p = p)).
type chiSqGOFRow struct {
	spec      *types.Test
	rowsField string
	alpha     float64

	labels []string  // sorted expected labels
	props  []float64 // their proportions
	index  map[string]int

	counts       []int64
	cellW        []float64
	n            int64
	sumW, sumWSq float64
	missing      map[string]bool
	testWeight
}

func newChiSqGOFRow(spec *types.Test, schema *encoding.Schema, expected map[string]float64) (RowTest, error) {
	if spec.Rows == "" {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			"TEST_CHISQ goodness of fit (params.expected) requires rows",
			map[string]any{"rows": spec.Rows})
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
	if cerr := fieldaccept.ExpectedRefusal(string(spec.Type), spec.Rows, spec.Cols, expected, schema); cerr != nil {
		return nil, cerr
	}
	if cerr := contingencyAxisRefusal(spec, schema); cerr != nil {
		return nil, cerr
	}
	labels := fieldaccept.SortedLabels(expected)
	g := &chiSqGOFRow{
		spec:       spec,
		rowsField:  spec.Rows,
		alpha:      alpha,
		labels:     labels,
		props:      make([]float64, len(labels)),
		index:      make(map[string]int, len(labels)),
		testWeight: newTestWeight(spec),
	}
	for i, l := range labels {
		g.props[i] = expected[l]
		g.index[l] = i
	}
	g.reset()
	return g, nil
}

func (g *chiSqGOFRow) UpdateRow(record *Record) error {
	level, ok, err := record.LevelValueChecked(g.rowsField)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	w, ok := g.rowWeight(record)
	if !ok {
		return nil
	}
	i, known := g.index[level]
	if !known {
		g.missing[level] = true
		return nil
	}
	g.counts[i]++
	g.cellW[i] += w
	g.n++
	g.sumW += w
	g.sumWSq += float64(w * w)
	return nil
}

func (g *chiSqGOFRow) Finalize() (*types.TestResult, error) {
	defer g.reset()
	if len(g.missing) > 0 {
		missing := make([]string, 0, len(g.missing))
		for l := range g.missing {
			missing = append(missing, l)
		}
		sort.Strings(missing)
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_EXPECTED_LABEL_MISSING,
			fmt.Sprintf("TEST_CHISQ goodness of fit observed rows level(s) %q that params.expected does not list; give every level its expected proportion (they are never renormalised)", missing),
			map[string]any{
				"type":            string(types.TEST_CHISQ),
				"rows":            g.rowsField,
				"labels":          missing,
				"expected_labels": append([]string(nil), g.labels...),
			})
	}
	k := len(g.labels)
	if g.n == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_CONTINGENCY_DEGENERATE,
			fmt.Sprintf("TEST_CHISQ goodness of fit needs at least one observed row; got N=0 over %d categories", k),
			map[string]any{"categories": k, "n": g.n})
	}
	// The table the statistic reads: Σw per level, scaled by
	// c = N*/Σw under probability only (unweighted and frequency read
	// the exact Σw, no multiply).
	scale := g.basis.Scale(g.sumW, g.sumWSq)
	N := g.basis.NStar(g.sumW, g.sumWSq)
	scaled := func(x float64) float64 {
		if g.basis == weighting.Probability {
			return scale * x
		}
		return x
	}
	expectedCounts := make([]float64, k)
	var stat float64
	expectedMin := math.Inf(1)
	lowExpectedCells := 0
	for i := range k {
		expected := float64(N * g.props[i])
		if expected < expectedMin {
			expectedMin = expected
		}
		if expected < 5 {
			lowExpectedCells++
		}
		diff := scaled(g.cellW[i]) - expected
		stat += diff * diff / expected
		// Reported on the observed table's own scale (Σw, unscaled).
		expectedCounts[i] = g.sumW * g.props[i]
	}
	df := float64(k - 1)
	p := chiSquareSurvival(stat, df)
	var warnings []string
	if lowExpectedCells > 0 {
		warnings = append(warnings, fmt.Sprintf("%s: %d categor(ies) have expected count < 5; χ² approximation may be unreliable",
			errors.PULSE_TEST_EXPECTED_COUNT_TOO_LOW, lowExpectedCells))
	}
	details := map[string]any{
		"labels":               append([]string(nil), g.labels...),
		"observed":             append([]int64(nil), g.counts...),
		"expected":             expectedCounts,
		"expected_proportions": append([]float64(nil), g.props...),
		"n":                    g.n,
		"expected_min":         expectedMin,
	}
	if g.basis.Weighted() {
		details["observed"] = append([]float64(nil), g.cellW...)
	}
	g.noteScalar(details, g.sumW, g.sumWSq)
	return &types.TestResult{
		Label:      testLabel(g.spec),
		Type:       types.TEST_CHISQ,
		Variant:    "goodness_of_fit",
		Statistic:  stat,
		DF:         df,
		PValue:     p,
		Alpha:      g.alpha,
		RejectNull: p < g.alpha,
		Details:    details,
		Warnings:   warnings,
	}, nil
}

func (g *chiSqGOFRow) reset() {
	g.counts = make([]int64, len(g.labels))
	g.cellW = make([]float64, len(g.labels))
	g.n = 0
	g.sumW, g.sumWSq = 0, 0
	g.missing = map[string]bool{}
}
