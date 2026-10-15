package processing

import (
	"fmt"
	"math"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// boxMRow implements TEST_BOX_M, Box's M test that the covariance
// matrix of a VECTOR (the p members of the slot's Vector / Fields,
// resolved and stashed on the spec copy by buildRowTests) is equal
// across the k ≥ 2 levels of a categorical SplitBy — the equal-
// covariance assumption TEST_MANOVA and the pooled TEST_HOTELLING_T2
// rest on. A streaming row test: one listwise linalg.CoMoment per
// level, the same fold as TEST_MANOVA.
//
// At Finalize, levels sorted, each level g has ν_g = N*_g − 1 and
// S_g = M2*_g/ν_g (levelScaledM2: the level's own c_g = N*_g/Σw_g), and
// the pooled S_p = Σ M2*_g / Σ ν_g. Then (biotools::boxM, term for
// term):
//
//   - M = Σν·ln|S_p| − Σ ν_g ln|S_g| (log-determinants from the
//     reference Cholesky);
//   - A1 = (2p² + 3p − 1)/(6(p + 1)(k − 1))·(Σ 1/ν_g − 1/Σν);
//   - χ² = M(1 − A1) on p(p + 1)/2·(k − 1) df — the headline.
//
// Box's F approximation rides Details: A2 = (p − 1)(p + 2)/(6(k − 1))·
// (Σ 1/ν_g² − 1/(Σν)²), df1 = the χ² df; when A2 > A1²,
// df2 = (df1 + 2)/(A2 − A1²) and F = M/b with b = df1/(1 − A1 − df1/df2);
// when A2 < A1², df2 = (df1 + 2)/(A1² − A2), b = df2/(1 − A1 + 2/df2)
// and F = df2·M/(df1(b − M)) (SPSS's form); A2 = A1² leaves F null.
//
// Weights follow the one rule (frequency weights equal the rep()
// expansion). A singular per-level or pooled covariance (a level with
// n ≤ p rows, a member constant within a level, a linear dependency)
// is not an error: the result carries a PULSE_MATRIX_SINGULAR warning
// and null figures.
type boxMRow struct {
	spec    *types.Test
	members []string
	// fill reads members (the shared member fill).
	fill    memberFill
	splitBy string
	alpha   float64

	x      []float64
	groups map[string]*linalg.CoMoment
	order  []string
	// dropped counts filter-passing rows (split key present) skipped
	// because a member was null: the listwise drop.
	dropped int64

	testWeight
}

func newBoxMRow(spec *types.Test, schema *encoding.Schema) (RowTest, error) {
	p := len(spec.Fields)
	if p == 0 {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_BOX_M requires vector or fields (the members whose covariance it compares)")
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
	if err := vectors.LevelVectorParams(testLabel(spec), spec); err != nil {
		return nil, err
	}
	if schema != nil {
		if cerr := fieldaccept.Check(string(spec.Type), fieldaccept.SlotSplitBy, spec.SplitBy, schema); cerr != nil {
			return nil, cerr
		}
	}
	return &boxMRow{
		spec:       spec,
		members:    append([]string(nil), spec.Fields...),
		fill:       memberFillOf(spec.Fields, schema),
		splitBy:    spec.SplitBy,
		alpha:      alpha,
		x:          make([]float64, p),
		groups:     make(map[string]*linalg.CoMoment),
		testWeight: newTestWeight(spec),
	}, nil
}

// UpdateRow folds one record. A null split key skips the row; a null
// member drops it (listwise) and counts it; a weighted test then skips
// a row whose weight is invalid or zero.
func (m *boxMRow) UpdateRow(record *Record) error {
	key, ok := record.StringValue(m.splitBy)
	if !ok {
		return nil
	}
	if !m.fill.complete(record, m.x) {
		m.dropped++
		return nil
	}
	w, ok := m.rowWeight(record)
	if !ok {
		return nil
	}
	c, exists := m.groups[key]
	if !exists {
		var err error
		c, err = linalg.NewCoMoment(len(m.members), linalg.Listwise)
		if err != nil {
			return err
		}
		m.groups[key] = c
		m.order = append(m.order, key)
	}
	c.Add(m.x, w)
	return nil
}

// logDetSPD is ln|s| for a symmetric positive-definite s — 2·Σ ln L_ii
// over the reference Cholesky factor — or ok=false when s is singular
// (rankDeficient, the one decision) or the factorisation fails.
func logDetSPD(s [][]float64) (float64, bool) {
	if rankDeficient(s) {
		return 0, false
	}
	sym, err := linalg.NewSymFromRows(s)
	if err != nil {
		return 0, false
	}
	l, err := linalg.Cholesky(sym)
	if err != nil {
		return 0, false
	}
	acc := 0.0
	for i := 0; i < len(s); i++ {
		acc += math.Log(l.At(i, i))
	}
	return 2 * acc, true
}

// boxMF is Box's F approximation to M (see boxMRow): F, df2 and its p,
// all NaN when A2 = A1².
func boxMF(m, a1, a2, df1 float64) (f, df2, p float64) {
	d := a2 - a1*a1
	switch {
	case d > 0:
		df2 = (df1 + 2) / d
		b := df1 / (1 - a1 - df1/df2)
		f = m / b
	case d < 0:
		df2 = (df1 + 2) / -d
		b := df2 / (1 - a1 + 2/df2)
		f = df2 * m / (df1 * (b - m))
	default:
		return math.NaN(), math.NaN(), math.NaN()
	}
	return f, df2, manovaP(f, df1, df2)
}

// Finalize emits the TestResult and resets the state.
func (m *boxMRow) Finalize() (*types.TestResult, error) {
	defer func() {
		m.groups = make(map[string]*linalg.CoMoment)
		m.order = nil
		m.dropped = 0
	}()
	p := len(m.members)
	keys := append([]string(nil), m.order...)
	sort.Strings(keys)
	k := len(keys)
	if k < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_SPLIT_GROUPS_LT_2,
			fmt.Sprintf("TEST_BOX_M needs split_by %q to have at least 2 levels, got %d", m.splitBy, k),
			map[string]any{"groups": keys, "split_by": m.splitBy, "min_required": 2})
	}
	levels := make([]*linalg.CoMoment, k)
	ns := make([]int64, k)
	nu := make([]float64, k)
	for i, key := range keys {
		levels[i] = m.groups[key]
		ns[i] = levels[i].N()
		nu[i] = levelNStar(m.basis, levels[i]) - 1
	}
	for i := range levels {
		if ns[i] < 2 || !(nu[i] > 0) {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
				fmt.Sprintf("TEST_BOX_M needs n ≥ 2 complete rows (and N* > 1) in every level; level %q has n = %d, N* = %.4g", keys[i], ns[i], nu[i]+1),
				map[string]any{"n": ns, "min_required": 2, "p": p, "group": keys[i], "groups": keys})
		}
	}
	df1 := float64(p*(p+1)/2) * float64(k-1)
	res := &types.TestResult{
		Label: testLabel(m.spec),
		Type:  types.TEST_BOX_M,
		DF:    df1,
		Alpha: m.alpha,
		Details: map[string]any{
			"members":   append([]string(nil), m.members...),
			"groups":    keys,
			"n":         ns,
			"n_dropped": m.dropped,
			"df1":       df1,
		},
	}
	m.noteWeights(res.Details, levels, true)
	for i, c := range levels {
		m.checkNEff(m.spec, true, keys[i], c.NEff(), p+1)
	}

	sumNu, inv, inv2 := 0.0, 0.0, 0.0
	pooled := make([][]float64, p)
	for i := range pooled {
		pooled[i] = make([]float64, p)
	}
	logDets := make([]float64, k)
	singular := false
	for g, c := range levels {
		m2 := levelScaledM2(m.basis, c)
		s := make([][]float64, p)
		for i := range s {
			s[i] = make([]float64, p)
			for j := range s[i] {
				pooled[i][j] += m2[i][j]
				s[i][j] = m2[i][j] / nu[g]
			}
		}
		ld, ok := logDetSPD(s)
		if !ok {
			singular = true
			logDets[g] = math.NaN()
		} else {
			logDets[g] = ld
		}
		sumNu += nu[g]
		inv += 1 / nu[g]
		inv2 += 1 / (nu[g] * nu[g])
	}
	for i := range pooled {
		for j := range pooled[i] {
			pooled[i][j] /= sumNu
		}
	}
	ldPooled, ok := logDetSPD(pooled)
	if !ok {
		singular = true
		ldPooled = math.NaN()
	}
	res.Details["log_det"] = logDets
	res.Details["log_det_pooled"] = ldPooled
	if singular {
		res.Statistic, res.PValue = math.NaN(), math.NaN()
		for _, key := range []string{"m", "f", "df2", "f_p_value"} {
			res.Details[key] = math.NaN()
		}
		res.Warnings = append(res.Warnings, string(errors.PULSE_MATRIX_SINGULAR)+
			": a per-level or pooled covariance of "+fmt.Sprint(m.members)+" is singular (a level with no more rows than members, a member constant within a level, or a linear dependency); Box's M is undefined")
		return res, nil
	}
	bm := float64(sumNu * ldPooled)
	for g := range levels {
		bm -= float64(nu[g] * logDets[g])
	}
	pf, qf := float64(p), float64(k-1)
	a1 := (2*pf*pf + 3*pf - 1) / (6 * (pf + 1) * qf) * (inv - 1/sumNu)
	a2 := (pf - 1) * (pf + 2) / (6 * qf) * (inv2 - 1/(sumNu*sumNu))
	chi := bm * (1 - a1)
	f, df2, fp := boxMF(bm, a1, a2, df1)
	res.Statistic = chi
	res.PValue = chiSquareSurvival(chi, df1)
	res.RejectNull = res.PValue < m.alpha
	res.Details["m"] = bm
	res.Details["f"] = f
	res.Details["df2"] = df2
	res.Details["f_p_value"] = fp
	return res, nil
}
