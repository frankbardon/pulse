package processing

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// mardiaRow implements TEST_MARDIA: Mardia's (1970) test of
// multivariate normality on a VECTOR (the p members of the slot's
// Vector / Fields, resolved and stashed on the spec copy by
// buildRowTests). One sample; split_by is refused.
//
// With x̄ the mean, S the MAXIMUM-LIKELIHOOD covariance (M2/N) and
// D_ij = (x_i − x̄)ᵀ S⁻¹ (x_j − x̄):
//
//	b1p = (1/N²) Σ_i Σ_j D_ij³   (multivariate skewness)
//	b2p = (1/N)  Σ_i D_ii²       (multivariate kurtosis)
//
// skewness χ² = N·b1p/6 on p(p + 1)(p + 2)/6 df — times the
// small-sample factor k = (p + 1)(N + 1)(N + 3) / (N((N + 1)(p + 1) − 6))
// when N < 20 — and kurtosis z = (b2p − p(p + 2)) / √(8p(p + 2)/N),
// two-sided. These are MVN::mvn(mvnTest = "mardia")'s figures (cov =
// TRUE). params.statistic picks the headline; both ride Details.
//
// b1p is never formed from the n × n matrix D: with z_i = L⁻¹(x_i − x̄)
// (S = LLᵀ, the reference Cholesky), D_ij = z_i·z_j, so
// Σ_ij D_ij³ = Σ_abc (Σ_i z_ia z_ib z_ic)² — one symmetric third-moment
// tensor, O(n·p³) instead of O(n²·p).
//
// BUFFERED: every complete row is kept (n·p float64s) because each z_i
// needs the fitted x̄ and S, which only exist once the last row is in.
//
// Weights are frequency-only (weighting.ClassFrequencyOnly): a row of
// weight w counts as w identical rows, so N = Σw, x̄ and S are the
// Σw-weighted moments and b1p / b2p carry w_i·w_j / w_i — exactly the
// unweighted test on the expanded rows. A singular S (n ≤ p, a constant
// member, a linear dependency) warns PULSE_MATRIX_SINGULAR and leaves
// every figure null.
type mardiaRow struct {
	spec    *types.Test
	members []string
	// fill reads members (the shared member fill).
	fill      memberFill
	alpha     float64
	statistic string

	x   []float64
	cm  *linalg.CoMoment
	buf []float64 // complete rows, row-major n × p
	ws  []float64 // per-row weights, only when weighted
	// dropped counts filter-passing rows skipped because a member was
	// null: the listwise drop.
	dropped int64

	testWeight
}

func newMardiaRow(spec *types.Test, schema *encoding.Schema) (RowTest, error) {
	p := len(spec.Fields)
	if p < 1 {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			"TEST_MARDIA requires vector or fields with at least 1 member",
			map[string]any{"test": testLabel(spec), "p": p})
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
	statistic, verr := vectors.MardiaParams(testLabel(spec), spec)
	if verr != nil {
		return nil, verr
	}
	cm, err := linalg.NewCoMoment(p, linalg.Listwise)
	if err != nil {
		return nil, err
	}
	return &mardiaRow{
		spec:       spec,
		members:    append([]string(nil), spec.Fields...),
		fill:       memberFillOf(spec.Fields, schema),
		alpha:      alpha,
		statistic:  statistic,
		x:          make([]float64, p),
		cm:         cm,
		testWeight: newTestWeight(spec),
	}, nil
}

// UpdateRow keeps one complete record. A null member drops it
// (listwise) and counts it; a weighted test then skips a row whose
// weight is invalid or zero.
func (m *mardiaRow) UpdateRow(record *Record) error {
	if !m.fill.complete(record, m.x) {
		m.dropped++
		return nil
	}
	w, ok := m.rowWeight(record)
	if !ok {
		return nil
	}
	m.cm.Add(m.x, w)
	m.buf = append(m.buf, m.x...)
	if m.basis.Weighted() {
		m.ws = append(m.ws, w)
	}
	return nil
}

// mardiaMinN is the fewest complete rows (and the smallest N) the test
// accepts: below 3 the small-sample factor's denominator can vanish.
const mardiaMinN = 3

// Finalize emits the TestResult and resets the state.
func (m *mardiaRow) Finalize() (*types.TestResult, error) {
	p := len(m.members)
	defer func() {
		m.cm, _ = linalg.NewCoMoment(p, linalg.Listwise)
		m.buf, m.ws, m.dropped = nil, nil, 0
	}()
	n := m.cm.N()
	bigN := m.cm.W()
	if levelFloorN(m.basis, m.cm) < mardiaMinN || bigN < mardiaMinN {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_MARDIA requires n ≥ %d complete rows, got %d", mardiaMinN, n),
			map[string]any{"n": n, "min_required": mardiaMinN, "p": p})
	}
	df := float64(p * (p + 1) * (p + 2) / 6)
	res := &types.TestResult{
		Label:   testLabel(m.spec),
		Type:    types.TEST_MARDIA,
		Variant: m.statistic,
		Alpha:   m.alpha,
		Details: map[string]any{
			"members":          append([]string(nil), m.members...),
			"n":                n,
			"n_dropped":        m.dropped,
			"skewness":         math.NaN(),
			"skewness_chisq":   math.NaN(),
			"skewness_df":      df,
			"skewness_p_value": math.NaN(),
			"small_sample":     bigN < 20,
			"kurtosis":         math.NaN(),
			"kurtosis_z":       math.NaN(),
			"kurtosis_p_value": math.NaN(),
		},
	}
	if m.statistic == vectors.MardiaSkewness {
		res.DF = df
	}
	m.noteWeights(res.Details, []*linalg.CoMoment{m.cm}, false)
	res.Statistic, res.PValue = math.NaN(), math.NaN()

	s := m.cm.Cov(0)
	var l *linalg.Matrix
	if !rankDeficient(s.ToRows()) {
		l, _ = linalg.Cholesky(s)
	}
	if l == nil {
		res.Warnings = append(res.Warnings, string(errors.PULSE_MATRIX_SINGULAR)+
			": the covariance of "+fmt.Sprint(m.members)+" is singular (no more complete rows than members, a constant member, or a member that is a linear combination of others); Mardia's test is null")
		return res, nil
	}
	b1p, b2p := mardiaMoments(m.buf, m.ws, m.cm.Mean().Slice(), l, bigN)

	skew := bigN * b1p / 6
	if bigN < 20 {
		skew *= float64(p+1) * (bigN + 1) * (bigN + 3) / (bigN * (float64((bigN+1)*float64(p+1)) - 6))
	}
	pp2 := float64(p * (p + 2))
	z := (b2p - pp2) / math.Sqrt(8*pp2/bigN)
	pSkew := chiSquareSurvival(skew, df)
	pKurt := normalTwoSidedP(z)

	res.Details["skewness"] = b1p
	res.Details["skewness_chisq"] = skew
	res.Details["skewness_p_value"] = pSkew
	res.Details["kurtosis"] = b2p
	res.Details["kurtosis_z"] = z
	res.Details["kurtosis_p_value"] = pKurt
	if m.statistic == vectors.MardiaKurtosis {
		res.Statistic, res.PValue = z, pKurt
	} else {
		res.Statistic, res.PValue = skew, pSkew
	}
	res.RejectNull = res.PValue < m.alpha
	return res, nil
}

// mardiaMoments returns Mardia's b1p and b2p over the buffered rows
// (row-major n × p; ws nil = unit weights) given the mean, the lower
// Cholesky factor l of the ML covariance and N = Σw. Each row is
// whitened, z = l⁻¹(x − mean), by forward substitution; b2p sums
// w·|z|⁴ and b1p is Σ_abc (T_abc/N)² over the weighted third-moment
// tensor T_abc = Σ w·z_a z_b z_c, accumulated on a ≤ b ≤ c and counted
// by its multiplicity. Products are rounded before each addition
// (float64 casts) so the result does not depend on FMA contraction.
func mardiaMoments(buf, ws, mean []float64, l *linalg.Matrix, bigN float64) (b1p, b2p float64) {
	p := len(mean)
	type triple struct {
		a, b, c int
		mult    float64
	}
	var tri []triple
	for a := 0; a < p; a++ {
		for b := a; b < p; b++ {
			for c := b; c < p; c++ {
				mult := 6.0
				switch {
				case a == b && b == c:
					mult = 1
				case a == b || b == c:
					mult = 3
				}
				tri = append(tri, triple{a, b, c, mult})
			}
		}
	}
	tensor := make([]float64, len(tri))
	z := make([]float64, p)
	var sum4 float64
	for r := 0; r*p < len(buf); r++ {
		row := buf[r*p : (r+1)*p]
		for i := 0; i < p; i++ {
			v := row[i] - mean[i]
			for k := 0; k < i; k++ {
				v -= float64(l.At(i, k) * z[k])
			}
			z[i] = v / l.At(i, i)
		}
		w := 1.0
		if ws != nil {
			w = ws[r]
		}
		var d float64
		for i := 0; i < p; i++ {
			d += float64(z[i] * z[i])
		}
		sum4 += float64(w * float64(d*d))
		for t, tr := range tri {
			tensor[t] += float64(w * float64(float64(z[tr.a]*z[tr.b])*z[tr.c]))
		}
	}
	for t, tr := range tri {
		m3 := tensor[t] / bigN
		b1p += float64(tr.mult * float64(m3*m3))
	}
	return b1p, sum4 / bigN
}
