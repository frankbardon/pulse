package processing

import (
	"fmt"
	"math"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// mannWhitneyRow implements TEST_MANN_WHITNEY_U as a buffered row test:
// nonparametric two-sample comparison of a numeric Field across two
// groups defined by SplitBy.
//
// Algorithm: buffer raw values per group, then mid-rank the combined
// set. Statistic:
//
//	R_A = Σ rank_i over group A
//	U_A = R_A − n_A(n_A+1)/2
//	U   = min(U_A, U_B)
//
// Two-sided p-value via the normal approximation with tie correction:
//
//	μ_U = n_A n_B / 2
//	σ²_U = (n_A n_B / 12) · ( (N+1) − Σ(t³−t) / (N(N−1)) )
//	z    = (U_A − μ_U) / σ_U   (continuity-corrected by ½)
//	p    = 2 · (1 − Φ(|z|))
type mannWhitneyRow struct {
	spec    *types.Test
	schema  *encoding.Schema
	field   string
	splitBy string
	alpha   float64

	values map[string][]float64
	order  []string
}

func newMannWhitneyRow(spec *types.Test, schema *encoding.Schema) (RowTest, error) {
	if spec.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_MANN_WHITNEY_U requires field")
	}
	if spec.SplitBy == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_MANN_WHITNEY_U requires split_by")
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
		if f := schema.Field(spec.Field); f != nil && (f.Type.IsCategorical()) {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_FIELD_NOT_NUMERIC,
				fmt.Sprintf("TEST_MANN_WHITNEY_U field %q has non-numeric type %s", spec.Field, f.Type.String()),
				map[string]any{"field": spec.Field, "field_type": f.Type.String()})
		}
		if f := schema.Field(spec.SplitBy); f != nil && !f.Type.IsCategorical() {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				fmt.Sprintf("TEST_MANN_WHITNEY_U split_by %q must be categorical, got %s", spec.SplitBy, f.Type.String()),
				map[string]any{"split_by": spec.SplitBy, "field_type": f.Type.String()})
		}
	}
	return &mannWhitneyRow{
		spec:    spec,
		schema:  schema,
		field:   spec.Field,
		splitBy: spec.SplitBy,
		alpha:   alpha,
		values:  make(map[string][]float64),
	}, nil
}

func (m *mannWhitneyRow) UpdateRow(record *Record) error {
	v, ok := record.NumericValue(m.field)
	if !ok {
		return nil
	}
	key, ok := record.StringValue(m.splitBy)
	if !ok {
		return nil
	}
	if _, exists := m.values[key]; !exists {
		m.order = append(m.order, key)
	}
	m.values[key] = append(m.values[key], v)
	return nil
}

func (m *mannWhitneyRow) Finalize() (*types.TestResult, error) {
	defer m.reset()
	keys := append([]string(nil), m.order...)
	sort.Strings(keys)
	if len(keys) < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_SPLIT_GROUPS_LT_2,
			fmt.Sprintf("TEST_MANN_WHITNEY_U requires 2 split groups, got %d", len(keys)),
			map[string]any{"groups": keys, "min_required": 2})
	}
	if len(keys) > 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_SPLIT_GROUPS_LT_2,
			fmt.Sprintf("TEST_MANN_WHITNEY_U sees %d groups; filter to two via FILTER_INCLUDE/FILTER_EXCLUDE", len(keys)),
			map[string]any{"groups": keys, "max_allowed": 2})
	}
	a := m.values[keys[0]]
	b := m.values[keys[1]]
	nA, nB := len(a), len(b)
	if nA < 2 || nB < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_MANN_WHITNEY_U requires n ≥ 2 per group, got %d / %d", nA, nB),
			map[string]any{"n": []int{nA, nB}, "min_required": 2})
	}
	combined := make([]float64, 0, nA+nB)
	combined = append(combined, a...)
	combined = append(combined, b...)
	ranks, ties := midRanks(combined)
	var rA float64
	for i := range nA {
		rA += ranks[i]
	}
	N := float64(nA + nB)
	nAf := float64(nA)
	nBf := float64(nB)
	uA := rA - nAf*(nAf+1)/2
	uB := nAf*nBf - uA
	uMin := math.Min(uA, uB)
	muU := nAf * nBf / 2
	tc := tieCorrection(ties)
	varU := (nAf * nBf / 12.0) * ((N + 1) - tc/(N*(N-1)))
	var z, p float64
	switch {
	case varU <= 0:
		// Degenerate (all ties); set z and p conservatively.
		z = 0
		p = 1
	default:
		// Continuity correction of ±0.5 toward μ.
		diff := uA - muU
		switch {
		case diff > 0:
			diff -= 0.5
			if diff < 0 {
				diff = 0
			}
		case diff < 0:
			diff += 0.5
			if diff > 0 {
				diff = 0
			}
		}
		z = diff / math.Sqrt(varU)
		p = 2 * (1 - standardNormalCDF(math.Abs(z)))
	}
	res := &types.TestResult{
		Label:      testLabel(m.spec),
		Type:       types.TEST_MANN_WHITNEY_U,
		Variant:    "asymptotic",
		Statistic:  uMin,
		PValue:     p,
		Alpha:      m.alpha,
		RejectNull: p < m.alpha,
		Details: map[string]any{
			"groups": keys,
			"n":      []int{nA, nB},
			"u_a":    uA,
			"u_b":    uB,
			"u_min":  uMin,
			"r_a":    rA,
			"r_b":    nAf*(N+1) - rA + nBf*(N+1) - (N*(N+1) - rA - (nAf*(N+1) - rA)), // compactness; see r_a/N
			"mu_u":   muU,
			"var_u":  varU,
			"z":      z,
		},
	}
	// The r_b expression above collapses to rB = N(N+1)/2 − rA; rewrite
	// directly for clarity.
	res.Details["r_b"] = N*(N+1)/2 - rA
	if tiesDominate(ties, nA+nB) {
		res.Warnings = append(res.Warnings, string(errors.PULSE_TEST_TIES_DOMINATE)+
			": ≥ 50% of values are tied; the asymptotic p-value is unreliable")
	}
	return res, nil
}

func (m *mannWhitneyRow) reset() {
	m.values = make(map[string][]float64)
	m.order = nil
}
