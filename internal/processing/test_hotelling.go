package processing

import (
	"fmt"
	"math"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// hotellingRow implements TEST_HOTELLING_T2 as a streaming row test on
// a mean VECTOR: the p members of the slot's Vector / Fields, which
// buildRowTests resolves and stashes on the spec copy the factory
// receives (spec.Fields are then the literal member names).
//
// Each filter-passing row with every member present (listwise) folds
// into one linalg.CoMoment per SplitBy level, in first-seen order:
//
//   - One-sample (no SplitBy): T² = N*·(x̄ − μ)ᵀ S⁻¹ (x̄ − μ) against
//     params.mu (length p, default zeros); F = (N* − p)/(p(N* − 1))·T²
//     on (p, N* − p).
//   - Paired (Fields2, same length p, no SplitBy): each row folds the
//     difference vector Fields − Fields2 (listwise across both sets),
//     then the one-sample form runs against params.mu.
//   - Two-sample (SplitBy, exactly 2 levels, sorted): pooled
//     S = (M2*₁ + M2*₂)/(N*₁ + N*₂ − 2), T² = N*₁N*₂/(N*₁ + N*₂)·dᵀS⁻¹d
//     with d = x̄₁ − x̄₂; exact F = (N − p − 1)/(p(N − 2))·T² on
//     (p, N − p − 1), N = N*₁ + N*₂.
//   - Two-sample, params.variance "unequal": Krishnamoorthy–Yu (2004)
//     modified Nel–Van der Merwe. Vᵢ = Sᵢ/N*ᵢ, S̃ = V₁ + V₂,
//     T² = dᵀS̃⁻¹d, ν = (p + p²)/Σᵢ (tr[(VᵢS̃⁻¹)²] + tr[VᵢS̃⁻¹]²)/(N*ᵢ − 1),
//     F = (ν − p + 1)/(νp)·T² on (p, ν − p + 1).
//
// Weights follow the one rule (weighting.Basis): every formula is the
// frequency formula on w*, so N* is Σw (unweighted: n; frequency: Σw)
// or Kish n_eff (probability), M2* = c·M2 with c = N*/Σw, and the
// per-level covariance is M2*/(N* − 1). A singular (pooled) covariance
// is not an error: the result carries a PULSE_MATRIX_SINGULAR warning
// and a null statistic and p-value.
type hotellingRow struct {
	spec     *types.Test
	members  []string
	members2 []string // paired form: the second set (difference = members − members2)
	splitBy  string
	one      bool // one-sample or paired: a single "" level
	paired   bool
	unequal  bool // two-sample params.variance "unequal"
	alpha    float64
	mu       []float64

	x      []float64
	groups map[string]*linalg.CoMoment
	order  []string
	// dropped counts filter-passing rows (split key present) skipped
	// because a member was null: the listwise drop.
	dropped int64

	testWeight
}

func newHotellingRow(spec *types.Test, schema *encoding.Schema) (RowTest, error) {
	p := len(spec.Fields)
	if p == 0 {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_HOTELLING_T2 requires vector or fields (the members of the mean vector)")
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
	if spec.SplitBy != "" && schema != nil {
		if f := schema.Field(spec.SplitBy); f != nil && !f.Type.IsCategorical() {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				fmt.Sprintf("TEST_HOTELLING_T2 split_by %q must be categorical, got %s", spec.SplitBy, f.Type.String()),
				map[string]any{"split_by": spec.SplitBy, "type": f.Type.String()})
		}
	}
	paired := len(spec.Fields2) > 0
	if paired && len(spec.Fields2) != p {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("TEST_HOTELLING_T2 pairs %d members with %d; both sets need the same count", p, len(spec.Fields2)),
			map[string]any{"test": testLabel(spec), "reason": "vector2_length_mismatch", "p": p, "p2": len(spec.Fields2)})
	}
	if paired && spec.SplitBy != "" {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			"TEST_HOTELLING_T2 sets fields2 (the paired form) and split_by; drop one",
			map[string]any{"test": testLabel(spec), "reason": "paired_with_split_by", "split_by": spec.SplitBy})
	}
	cfg, cerr := vectors.HotellingParams(testLabel(spec), spec.Params, p, vectors.HotellingFormOf(spec))
	if cerr != nil {
		return nil, cerr
	}
	h := &hotellingRow{
		spec:       spec,
		members:    append([]string(nil), spec.Fields...),
		splitBy:    spec.SplitBy,
		one:        spec.SplitBy == "",
		paired:     paired,
		unequal:    cfg.Variance == vectors.HotellingVarianceUnequal,
		alpha:      alpha,
		mu:         cfg.Mu,
		x:          make([]float64, p),
		groups:     make(map[string]*linalg.CoMoment),
		testWeight: newTestWeight(spec),
	}
	if paired {
		h.members2 = append([]string(nil), spec.Fields2...)
	}
	return h, nil
}

// UpdateRow folds one record. A null split key skips the row; a null
// member drops it (listwise) and counts it; a weighted test then skips
// a row whose weight is invalid or zero.
func (h *hotellingRow) UpdateRow(record *Record) error {
	key := ""
	if !h.one {
		s, ok := record.StringValue(h.splitBy)
		if !ok {
			return nil
		}
		key = s
	}
	for i, f := range h.members {
		v, ok := record.NumericValue(f)
		if !ok || math.IsNaN(v) {
			h.dropped++
			return nil
		}
		h.x[i] = v
	}
	for i, f := range h.members2 {
		v, ok := record.NumericValue(f)
		if !ok || math.IsNaN(v) {
			h.dropped++
			return nil
		}
		h.x[i] = float64(h.x[i] - v)
	}
	w, ok := h.rowWeight(record)
	if !ok {
		return nil
	}
	c, exists := h.groups[key]
	if !exists {
		var err error
		c, err = linalg.NewCoMoment(len(h.members), linalg.Listwise)
		if err != nil {
			return err
		}
		h.groups[key] = c
		h.order = append(h.order, key)
	}
	c.Add(h.x, w)
	return nil
}

// Finalize emits the TestResult and resets the state.
func (h *hotellingRow) Finalize() (*types.TestResult, error) {
	defer func() {
		h.groups = make(map[string]*linalg.CoMoment)
		h.order = nil
		h.dropped = 0
	}()
	if h.one {
		return h.finalizeOneSample()
	}
	if h.unequal {
		return h.finalizeTwoSampleUnequal()
	}
	return h.finalizeTwoSample()
}

// nStar is a level's N* under the test's basis.
func (h *hotellingRow) nStar(c *linalg.CoMoment) float64 {
	if h.basis == weighting.Probability {
		return c.NEff()
	}
	return c.W()
}

// scaledM2 is a level's co-moment matrix on w*: M2* = c·M2, read as
// Cov(0)·N* under probability (Cov(0) = M2/Σw) and Cov(0)·Σw otherwise.
func (h *hotellingRow) scaledM2(c *linalg.CoMoment) [][]float64 {
	cov := c.Cov(0)
	scale := c.W()
	if h.basis == weighting.Probability {
		scale = c.NEff()
	}
	p := len(h.members)
	out := make([][]float64, p)
	for i := range out {
		out[i] = make([]float64, p)
		for j := range out[i] {
			out[i][j] = float64(cov.At(i, j) * scale)
		}
	}
	return out
}

// quadForm returns dᵀ S⁻¹ d for S = m/den, or ok=false when S is
// singular. The DECISION reads the rank of S's correlation form under
// the one rank tolerance (linalg.RankTolerance over gonum SymEigen —
// portable decisions), so a member that is a linear combination of
// others is singular even when rounding leaves a tiny positive pivot;
// a zero or non-finite variance is singular outright. The VALUE is the
// FMA-free reference Cholesky solve, so its bits are architecture-free.
func quadForm(m [][]float64, den float64, d []float64) (float64, bool) {
	p := len(d)
	s := make([][]float64, p)
	for i := range s {
		s[i] = make([]float64, p)
		for j := range s[i] {
			s[i][j] = m[i][j] / den
		}
	}
	sd := make([]float64, p)
	for i := range sd {
		v := s[i][i]
		if !(v > 0) || math.IsInf(v, 0) {
			return 0, false
		}
		sd[i] = math.Sqrt(v)
	}
	r := make([][]float64, p)
	for i := range r {
		r[i] = make([]float64, p)
		for j := range r[i] {
			r[i][j] = s[i][j] / (sd[i] * sd[j])
		}
	}
	rs, err := linalg.NewSymFromRows(r)
	if err != nil {
		return 0, false
	}
	eig, err := linalg.SymEigen(rs)
	if err != nil {
		return 0, false
	}
	if p > 0 && eig.Values.At(p-1) <= linalg.RankTolerance(p, p, eig.Values.At(0)) {
		return 0, false
	}
	sym, err := linalg.NewSymFromRows(s)
	if err != nil {
		return 0, false
	}
	y, err := linalg.SolveSPD(sym, linalg.NewVec(append([]float64(nil), d...)))
	if err != nil {
		return 0, false
	}
	q := 0.0
	for i, di := range d {
		q += float64(di * y.At(i))
	}
	return q, true
}

// singular marks res's figures undefined and attaches the warning.
func (h *hotellingRow) singular(res *types.TestResult, what string) {
	res.Statistic = math.NaN()
	res.PValue = math.NaN()
	res.RejectNull = false
	res.Details["f"] = math.NaN()
	res.Warnings = append(res.Warnings, string(errors.PULSE_MATRIX_SINGULAR)+
		": the "+what+" covariance of "+fmt.Sprint(h.members)+" is singular (a member is constant or a linear combination of others); T² is undefined")
}

func (h *hotellingRow) finalizeOneSample() (*types.TestResult, error) {
	p := len(h.members)
	c, ok := h.groups[""]
	var n int64
	if ok {
		n = c.N()
	}
	form := "one_sample"
	if h.paired {
		form = "paired"
	}
	if n < int64(p)+1 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_HOTELLING_T2 %s on %d members requires n ≥ %d complete rows, got %d", form, p, p+1, n),
			map[string]any{"n": n, "min_required": p + 1, "p": p})
	}
	ns := h.nStar(c)
	if !(ns > float64(p)) {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_HOTELLING_T2 %s: effective sample size %.4g does not exceed p = %d", form, ns, p),
			map[string]any{"n": n, "n_star": ns, "p": p})
	}
	mean := c.Mean().Slice()
	d := make([]float64, p)
	for i := range d {
		d[i] = mean[i] - h.mu[i]
	}
	df2 := ns - float64(p)
	res := &types.TestResult{
		Label:   testLabel(h.spec),
		Type:    types.TEST_HOTELLING_T2,
		Variant: form,
		DF:      float64(p),
		Alpha:   h.alpha,
		Details: map[string]any{
			"members":   append([]string(nil), h.members...),
			"n":         n,
			"n_dropped": h.dropped,
			"mu":        append([]float64(nil), h.mu...),
			"df1":       float64(p),
			"df2":       df2,
		},
	}
	if h.paired {
		// The mean of the row-wise differences members − members2.
		res.Details["members2"] = append([]string(nil), h.members2...)
		res.Details["mean_diff"] = mean
	} else {
		res.Details["mean"] = mean
	}
	h.noteWeights(res.Details, []*linalg.CoMoment{c}, false)
	h.checkNEff(h.spec, false, "", c.NEff(), p+1)
	q, ok := quadForm(h.scaledM2(c), ns-1, d)
	if !ok {
		h.singular(res, "sample")
		return res, nil
	}
	t2 := float64(ns * q)
	f := (ns - float64(p)) / (float64(p) * (ns - 1)) * t2
	res.Statistic = t2
	res.Details["f"] = f
	res.PValue = fSurvival(f, float64(p), df2)
	res.RejectNull = res.PValue < h.alpha
	return res, nil
}

// twoLevels returns the split's two levels, sorted, or
// PULSE_TEST_SPLIT_GROUPS_LT_2 when there are not exactly two.
func (h *hotellingRow) twoLevels() ([]string, error) {
	keys := append([]string(nil), h.order...)
	sort.Strings(keys)
	if len(keys) != 2 {
		details := map[string]any{"groups": keys, "split_by": h.splitBy}
		msg := fmt.Sprintf("TEST_HOTELLING_T2 two-sample needs split_by %q to have exactly 2 levels, got %d", h.splitBy, len(keys))
		if len(keys) < 2 {
			details["min_required"] = 2
		} else {
			details["max_allowed"] = 2
		}
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_SPLIT_GROUPS_LT_2, msg, details)
	}
	return keys, nil
}

func (h *hotellingRow) finalizeTwoSample() (*types.TestResult, error) {
	p := len(h.members)
	keys, err := h.twoLevels()
	if err != nil {
		return nil, err
	}
	a, b := h.groups[keys[0]], h.groups[keys[1]]
	na, nb := h.nStar(a), h.nStar(b)
	nTot := na + nb
	if n := a.N() + b.N(); n < int64(p)+2 || !(nTot-float64(p)-1 > 0) {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_HOTELLING_T2 two-sample on %d members requires n₁ + n₂ ≥ %d complete rows (and N* > p + 1), got %d", p, p+2, n),
			map[string]any{"n": []int64{a.N(), b.N()}, "min_required": p + 2, "p": p})
	}
	ma, mb := a.Mean().Slice(), b.Mean().Slice()
	d := make([]float64, p)
	for i := range d {
		d[i] = ma[i] - mb[i]
	}
	df2 := nTot - float64(p) - 1
	res := &types.TestResult{
		Label:   testLabel(h.spec),
		Type:    types.TEST_HOTELLING_T2,
		Variant: "two_sample_pooled",
		DF:      float64(p),
		Alpha:   h.alpha,
		Details: map[string]any{
			"members":   append([]string(nil), h.members...),
			"groups":    keys,
			"n":         []int64{a.N(), b.N()},
			"n_dropped": h.dropped,
			"means":     [][]float64{ma, mb},
			"mean_diff": d,
			"df1":       float64(p),
			"df2":       df2,
		},
	}
	h.noteWeights(res.Details, []*linalg.CoMoment{a, b}, true)
	h.checkNEff(h.spec, false, "", a.NEff()+b.NEff(), p+2)
	ma2, mb2 := h.scaledM2(a), h.scaledM2(b)
	pooled := make([][]float64, p)
	for i := range pooled {
		pooled[i] = make([]float64, p)
		for j := range pooled[i] {
			pooled[i][j] = ma2[i][j] + mb2[i][j]
		}
	}
	q, ok := quadForm(pooled, nTot-2, d)
	if !ok {
		h.singular(res, "pooled")
		return res, nil
	}
	t2 := float64(na*nb) / nTot * q
	f := (nTot - float64(p) - 1) / (float64(p) * (nTot - 2)) * t2
	res.Statistic = t2
	res.Details["f"] = f
	res.PValue = fSurvival(f, float64(p), df2)
	res.RejectNull = res.PValue < h.alpha
	return res, nil
}

// finalizeTwoSampleUnequal is the two-sample form without a shared
// covariance: Krishnamoorthy & Yu (2004), the modified Nel–Van der Merwe
// test. Per level Sᵢ = M2*ᵢ/(N*ᵢ − 1) and Vᵢ = Sᵢ/N*ᵢ; S̃ = V₁ + V₂;
// T² = dᵀS̃⁻¹d; ν = (p + p²)/Σᵢ (tr[(VᵢS̃⁻¹)²] + tr[VᵢS̃⁻¹]²)/(N*ᵢ − 1);
// F = (ν − p + 1)/(νp)·T² on (p, ν − p + 1). Each level needs n > p
// complete rows (and N* > p), which bounds ν ≥ min(N*ᵢ − 1) ≥ p, so
// df2 ≥ 1. At p = 1 it is Welch's t-test (T² = t², ν = Welch df).
func (h *hotellingRow) finalizeTwoSampleUnequal() (*types.TestResult, error) {
	p := len(h.members)
	keys, err := h.twoLevels()
	if err != nil {
		return nil, err
	}
	a, b := h.groups[keys[0]], h.groups[keys[1]]
	levels := []*linalg.CoMoment{a, b}
	ns := []float64{h.nStar(a), h.nStar(b)}
	for i, c := range levels {
		if c.N() < int64(p)+1 || !(ns[i] > float64(p)) {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
				fmt.Sprintf("TEST_HOTELLING_T2 two-sample unequal on %d members requires n > p (and N* > p) in each level; level %q has n = %d, N* = %.4g", p, keys[i], c.N(), ns[i]),
				map[string]any{"n": []int64{a.N(), b.N()}, "n_star": ns, "min_required": p + 1, "p": p, "group": keys[i]})
		}
	}
	ma, mb := a.Mean().Slice(), b.Mean().Slice()
	d := make([]float64, p)
	for i := range d {
		d[i] = ma[i] - mb[i]
	}
	res := &types.TestResult{
		Label:   testLabel(h.spec),
		Type:    types.TEST_HOTELLING_T2,
		Variant: "two_sample_unequal",
		DF:      float64(p),
		Alpha:   h.alpha,
		Details: map[string]any{
			"members":   append([]string(nil), h.members...),
			"groups":    keys,
			"n":         []int64{a.N(), b.N()},
			"n_dropped": h.dropped,
			"means":     [][]float64{ma, mb},
			"mean_diff": d,
			"df1":       float64(p),
			"df2":       math.NaN(),
		},
	}
	h.noteWeights(res.Details, levels, true)
	h.checkNEff(h.spec, false, "", a.NEff()+b.NEff(), p+2)
	// Vᵢ = M2*ᵢ/((N*ᵢ − 1)·N*ᵢ), the covariance of level i's mean.
	v := make([][][]float64, 2)
	sum := make([][]float64, p)
	for i := range sum {
		sum[i] = make([]float64, p)
	}
	for l, c := range levels {
		m2 := h.scaledM2(c)
		den := float64((ns[l] - 1) * ns[l])
		v[l] = make([][]float64, p)
		for i := range v[l] {
			v[l][i] = make([]float64, p)
			for j := range v[l][i] {
				v[l][i][j] = m2[i][j] / den
				sum[i][j] += v[l][i][j]
			}
		}
	}
	t2, ok := quadForm(sum, 1, d)
	var inv *linalg.Sym
	if ok {
		s, err := linalg.NewSymFromRows(sum)
		if err == nil {
			inv, err = linalg.InverseSPD(s)
		}
		ok = err == nil
	}
	if !ok {
		h.singular(res, "summed (V₁ + V₂)")
		return res, nil
	}
	// denom = Σᵢ (tr[(VᵢS̃⁻¹)²] + tr[VᵢS̃⁻¹]²)/(N*ᵢ − 1).
	denom := 0.0
	for l := range levels {
		am := make([][]float64, p) // Aᵢ = Vᵢ S̃⁻¹
		tr := 0.0
		for i := 0; i < p; i++ {
			am[i] = make([]float64, p)
			for j := 0; j < p; j++ {
				acc := 0.0
				for k := 0; k < p; k++ {
					acc += float64(v[l][i][k] * inv.At(k, j))
				}
				am[i][j] = acc
			}
			tr += am[i][i]
		}
		tr2 := 0.0
		for i := 0; i < p; i++ {
			for j := 0; j < p; j++ {
				tr2 += float64(am[i][j] * am[j][i])
			}
		}
		denom += (tr2 + float64(tr*tr)) / (ns[l] - 1)
	}
	nu := float64(p+p*p) / denom
	df2 := nu - float64(p) + 1
	f := df2 / (nu * float64(p)) * t2
	res.Statistic = t2
	res.Details["df2"] = df2
	res.Details["f"] = f
	res.PValue = fSurvival(f, float64(p), df2)
	res.RejectNull = res.PValue < h.alpha
	return res, nil
}

// noteWeights writes sum_weights (and n_eff under kind probability)
// beside `n`: scalars for the one-sample form, per-level arrays for
// the two-sample one; no-op unweighted.
func (h *hotellingRow) noteWeights(details map[string]any, levels []*linalg.CoMoment, split bool) {
	if !h.basis.Weighted() {
		return
	}
	sums := make([]float64, len(levels))
	neffs := make([]float64, len(levels))
	for i, c := range levels {
		sums[i], neffs[i] = c.W(), c.NEff()
	}
	if !split {
		details["sum_weights"] = sums[0]
		if h.basis == weighting.Probability {
			details["n_eff"] = neffs[0]
		}
		return
	}
	details["sum_weights"] = sums
	if h.basis == weighting.Probability {
		details["n_eff"] = neffs
	}
}
