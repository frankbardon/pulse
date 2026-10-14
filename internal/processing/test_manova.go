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

// manovaRow implements TEST_MANOVA, the one-way MANOVA of a mean
// VECTOR (the p members of the slot's Vector / Fields, resolved and
// stashed on the spec copy by buildRowTests) across the k ≥ 2 levels of
// a categorical SplitBy, as a streaming row test.
//
// Each filter-passing row with every member present (listwise) folds
// into one linalg.CoMoment per level. At Finalize, levels sorted:
//
//   - E (within SSCP) = Σ_g M2*_g, each level's co-moment matrix on w*
//     (levelScaledM2: the level's OWN c_g = N*_g/Σw_g, TEST_ANOVA_F's
//     rule, so frequency weights equal the rep() expansion).
//   - H (between SSCP) = Σ_g N*_g (m_g − m̄)(m_g − m̄)ᵀ with the grand
//     mean m̄ = Σ_g N*_g m_g / N*, N* = Σ_g N*_g. Unweighted and under
//     frequency weights E + H is the merged (total) SSCP.
//   - q = k − 1 (hypothesis df), ν = N* − k (residual df), and the
//     four statistics read the eigenvalues λ of E⁻¹H with R's
//     summary.manova approximations: Pillai V = Σ λ/(1+λ), Wilks
//     Λ = Π 1/(1+λ) (Rao's F), Hotelling–Lawley U = Σ λ, Roy θ = max λ
//     (an UPPER BOUND on F, so its p is a lower bound). A statistic's
//     p is null when its F is negative or a df is not positive.
//
// Statistic / DF / PValue are Pillai's (V, df1, p) — the most robust
// of the four to unequal covariances. A singular E (decided by the one
// rank tolerance on its correlation form) is not an error: the result
// carries a PULSE_MATRIX_SINGULAR warning and null figures.
type manovaRow struct {
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

func newManovaRow(spec *types.Test, schema *encoding.Schema) (RowTest, error) {
	p := len(spec.Fields)
	if p == 0 {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_MANOVA requires vector or fields (the members of the mean vector)")
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
	return &manovaRow{
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
func (m *manovaRow) UpdateRow(record *Record) error {
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

// manovaStatistic is one of the four statistics with its F
// approximation.
type manovaStatistic struct {
	stat, f, df1, df2, p float64
}

func (s manovaStatistic) details() map[string]any {
	return map[string]any{"statistic": s.stat, "f": s.f, "df1": s.df1, "df2": s.df2, "p_value": s.p}
}

// manovaP is R's summary.manova p rule: the F upper tail when F ≥ 0
// and both df are positive, NaN otherwise.
func manovaP(f, df1, df2 float64) float64 {
	if !(f >= 0) || !(df1 > 0) || !(df2 > 0) {
		return math.NaN()
	}
	return fSurvival(f, df1, df2)
}

// manovaStatistics returns Pillai, Wilks, Hotelling–Lawley and Roy for
// the eigenvalues eig (all p of E⁻¹H), hypothesis df q and residual df
// dfr — R's stats:::Pillai / Wilks / HL / Roy, term for term.
func manovaStatistics(eig []float64, q, dfr float64) (pillai, wilks, hl, roy manovaStatistic) {
	p := float64(len(eig))
	s := math.Min(p, q)
	n := 0.5 * (dfr - p - 1)
	mm := 0.5 * (math.Abs(p-q) - 1)

	v, prod, u, mx := 0.0, 1.0, 0.0, 0.0
	for _, l := range eig {
		v += l / (1 + l)
		prod *= 1 / (1 + l)
		u += l
		mx = math.Max(mx, l)
	}

	t1 := 2*mm + s + 1
	t2 := 2*n + s + 1
	pillai = manovaStatistic{stat: v, f: (t2 / t1 * v) / (s - v), df1: s * t1, df2: s * t2}

	w1 := dfr - 0.5*(p-q+1)
	w2 := (p*q - 2) / 4
	w3 := p*p + q*q - 5
	if w3 > 0 {
		w3 = math.Sqrt((p*q*p*q - 4) / w3)
	} else {
		w3 = 1
	}
	wdf2 := w1*w3 - 2*w2
	wilks = manovaStatistic{stat: prod, f: ((math.Pow(prod, -1/w3) - 1) * wdf2) / p / q, df1: p * q, df2: wdf2}

	h2 := 2 * (s*n + 1)
	hl = manovaStatistic{stat: u, f: (h2 * u) / s / s / t1, df1: s * t1, df2: h2}

	r1 := math.Max(p, q)
	r2 := dfr - r1 + q
	roy = manovaStatistic{stat: mx, f: (r2 * mx) / r1, df1: r1, df2: r2}

	for _, st := range []*manovaStatistic{&pillai, &wilks, &hl, &roy} {
		st.p = manovaP(st.f, st.df1, st.df2)
	}
	return pillai, wilks, hl, roy
}

// manovaEigen returns the eigenvalues of E⁻¹H, descending, negatives
// (rounding) clamped to 0, or ok=false when E is singular. Both are
// scaled by D = diag(E)^-½ first (the eigenvalues are invariant), then
// the symmetric form L⁻¹H′L⁻ᵀ of E′ = LLᵀ (the FMA-free reference
// Cholesky) is decomposed.
func manovaEigen(e, h [][]float64) ([]float64, bool) {
	p := len(e)
	if rankDeficient(e) {
		return nil, false
	}
	d := make([]float64, p)
	for i := range d {
		d[i] = 1 / math.Sqrt(e[i][i])
	}
	es := make([][]float64, p)
	hs := make([][]float64, p)
	for i := range es {
		es[i] = make([]float64, p)
		hs[i] = make([]float64, p)
		for j := range es[i] {
			es[i][j] = float64(e[i][j]*d[i]) * d[j]
			hs[i][j] = float64(h[i][j]*d[i]) * d[j]
		}
	}
	sym, err := linalg.NewSymFromRows(es)
	if err != nil {
		return nil, false
	}
	L, err := linalg.Cholesky(sym)
	if err != nil {
		return nil, false
	}
	// forward solves L·y = b for b; column c of Y = L⁻¹H′.
	forward := func(b []float64) []float64 {
		y := make([]float64, p)
		for i := 0; i < p; i++ {
			acc := b[i]
			for k := 0; k < i; k++ {
				acc -= float64(L.At(i, k) * y[k])
			}
			y[i] = acc / L.At(i, i)
		}
		return y
	}
	y := make([][]float64, p) // y[c] = column c of L⁻¹H′
	for c := 0; c < p; c++ {
		col := make([]float64, p)
		for i := range col {
			col[i] = hs[i][c]
		}
		y[c] = forward(col)
	}
	// A = L⁻¹·(L⁻¹H′)ᵀ: row r of (L⁻¹H′) is (y[0][r], …, y[p-1][r]),
	// i.e. column r of (L⁻¹H′)ᵀ is y[·][r] read across c.
	a := make([][]float64, p)
	for i := range a {
		a[i] = make([]float64, p)
	}
	for r := 0; r < p; r++ {
		col := make([]float64, p)
		for c := 0; c < p; c++ {
			col[c] = y[c][r]
		}
		z := forward(col)
		for i := 0; i < p; i++ {
			a[i][r] = z[i]
		}
	}
	for i := 0; i < p; i++ {
		for j := 0; j < i; j++ {
			avg := 0.5 * (a[i][j] + a[j][i])
			a[i][j], a[j][i] = avg, avg
		}
	}
	as, err := linalg.NewSymFromRows(a)
	if err != nil {
		return nil, false
	}
	eig, err := linalg.SymEigen(as)
	if err != nil {
		return nil, false
	}
	out := eig.Values.Slice()
	for i, l := range out {
		if l < 0 {
			out[i] = 0
		}
	}
	return out, true
}

// Finalize emits the TestResult and resets the state.
func (m *manovaRow) Finalize() (*types.TestResult, error) {
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
			fmt.Sprintf("TEST_MANOVA needs split_by %q to have at least 2 levels, got %d", m.splitBy, k),
			map[string]any{"groups": keys, "split_by": m.splitBy, "min_required": 2})
	}
	levels := make([]*linalg.CoMoment, k)
	ns := make([]int64, k)
	var n int64
	nStar := 0.0
	for i, key := range keys {
		levels[i] = m.groups[key]
		ns[i] = levels[i].N()
		n += ns[i]
		nStar += levelNStar(m.basis, levels[i])
	}
	dfr := nStar - float64(k)
	if n < int64(p+k) || !(dfr > 0) {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_MANOVA on %d members and %d levels requires n ≥ p + k = %d complete rows (and N* > k), got %d", p, k, p+k, n),
			map[string]any{"n": ns, "n_star": nStar, "min_required": p + k, "p": p, "groups": keys})
	}
	q := float64(k - 1)
	means := make([][]float64, k)
	grand := make([]float64, p)
	e := make([][]float64, p)
	h := make([][]float64, p)
	for i := range e {
		e[i] = make([]float64, p)
		h[i] = make([]float64, p)
	}
	for g, c := range levels {
		means[g] = c.Mean().Slice()
		w := levelNStar(m.basis, c)
		for i := range grand {
			grand[i] += float64(w * means[g][i])
		}
		m2 := levelScaledM2(m.basis, c)
		for i := range e {
			for j := range e[i] {
				e[i][j] += m2[i][j]
			}
		}
	}
	for i := range grand {
		grand[i] /= nStar
	}
	for g, c := range levels {
		w := levelNStar(m.basis, c)
		dev := make([]float64, p)
		for i := range dev {
			dev[i] = means[g][i] - grand[i]
		}
		for i := range h {
			for j := range h[i] {
				h[i][j] += float64(float64(w*dev[i]) * dev[j])
			}
		}
	}
	res := &types.TestResult{
		Label:   testLabel(m.spec),
		Type:    types.TEST_MANOVA,
		Variant: "one_way",
		Alpha:   m.alpha,
		Details: map[string]any{
			"members":       append([]string(nil), m.members...),
			"groups":        keys,
			"n":             ns,
			"n_dropped":     m.dropped,
			"means":         means,
			"df_hypothesis": q,
			"df_residual":   dfr,
		},
	}
	m.noteWeights(res.Details, levels, true)
	nEff := 0.0
	for _, c := range levels {
		nEff += c.NEff()
	}
	m.checkNEff(m.spec, false, "", nEff, p+k)
	eig, ok := manovaEigen(e, h)
	if !ok {
		nan := manovaStatistic{stat: math.NaN(), f: math.NaN(), df1: math.NaN(), df2: math.NaN(), p: math.NaN()}
		res.Statistic, res.PValue, res.DF = math.NaN(), math.NaN(), math.NaN()
		res.Details["f"], res.Details["df1"], res.Details["df2"] = math.NaN(), math.NaN(), math.NaN()
		for _, key := range []string{"pillai", "wilks", "hotelling_lawley", "roy"} {
			res.Details[key] = nan.details()
		}
		res.Details["roy"].(map[string]any)["upper_bound"] = true
		res.Warnings = append(res.Warnings, string(errors.PULSE_MATRIX_SINGULAR)+
			": the within-group SSCP of "+fmt.Sprint(m.members)+" is singular (a member is constant within groups or a linear combination of others); the MANOVA statistics are undefined")
		return res, nil
	}
	pillai, wilks, hl, roy := manovaStatistics(eig, q, dfr)
	res.Statistic = pillai.stat
	res.DF = pillai.df1
	res.PValue = pillai.p
	res.RejectNull = res.PValue < m.alpha
	res.Details["f"] = pillai.f
	res.Details["df1"] = pillai.df1
	res.Details["df2"] = pillai.df2
	res.Details["eigenvalues"] = eig
	res.Details["pillai"] = pillai.details()
	res.Details["wilks"] = wilks.details()
	res.Details["hotelling_lawley"] = hl.details()
	rd := roy.details()
	rd["upper_bound"] = true
	res.Details["roy"] = rd
	return res, nil
}
