package sem

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"gonum.org/v1/gonum/mat"
)

// wls_group.go is the multi-group ordinal fit: lavaan's DWLS / WLSMV
// with group = (delta parameterization). Every group's copy of the model
// is specified as the ML multi-group fit's (groupModels: per-group
// modifiers, the reference group, group_equal / group_partial) and
// carries its own two-step statistics and Γ over the SHARED category
// coding (every group's Levels the pooled data's, so a category one
// group lacks is that group's ReasonEmptyCategory refusal, as lavaan's
// "some categories are empty in group g").
//
// lavaan's ordinal identification (lav_pt_flat), outside the reference
// group, under group_equal thresholds: each ordered variable's
// intercept is freed (invariance.go) and so is its scale factor Δ
// (`x ~*~ x`) unless it is binary (one threshold); the thresholds of a
// variable are one value per row across groups (group_partial "x|tk"
// frees one). Everywhere else Δ = 1. WLS.est is (τ − ν − (ΛCα))·Δ for a
// threshold and Δ_i·Δ_j·σ_ij for a covariance (lav_lisrel_th /
// lav_lisrel_sigma), so the τ − μ term reads the group's implied latent
// response mean.
//
// The groups' discrepancies combine as lavaan's
//
//	fmin = Σ_g (n_g/N) · ½ (n_g − 1)/n_g · F_g,
//
// so T = 2N·fmin = Σ_g (n_g − 1) F_g (Chi2Group the terms). The robust
// vcov is lavaan's robust.sem over the joint free vector:
// E⁻¹ (Σ_g (n_g/N) Δ_gᵀV_gΓ_gV_gΔ_g) E⁻¹ / (N − G), E the observed
// information of fmin (Richardson on its gradient); WLSMV's traces are
// lav_test_ug_trace_stream's over the expected E = Σ_g (n_g/N) Δ_gᵀV_gΔ_g.
// The baseline is every group's independence model.

// OrdinalGroup is one group's rows for FitOrdinalGroups: the
// listwise-complete rows over the model's Observed order (every group's
// Kinds and Levels the same — the pooled coding), plus the group's
// place in the fit (GroupMoments' Label, Level and Reference).
type OrdinalGroup struct {
	Label     string
	In        TwoStepInput
	Level     int
	Reference bool
}

// jointWLS is the multi-group DWLS problem: one wlsState per group,
// c_g = (n_g − 1)/N, and idx[g][k], the joint index of group g's free
// entry k (model parameters, thresholds, scale factors).
type jointWLS struct {
	parts []*wlsState
	c     []float64
	idx   [][]int
	nFree int
	xg    [][]float64
}

func (j *jointWLS) split(x []float64) {
	for g, ix := range j.idx {
		for k, t := range ix {
			j.xg[g][k] = x[t]
		}
	}
}

// objective is Σ_g c_g F_g = 2·fmin.
func (j *jointWLS) objective(x []float64) float64 {
	j.split(x)
	f := 0.0
	for g, ws := range j.parts {
		v := ws.objective(j.xg[g])
		if math.IsInf(v, 1) {
			return v
		}
		f += j.c[g] * v
	}
	return f
}

func (j *jointWLS) derivatives(x []float64, needH bool) (float64, []float64, *mat.SymDense, error) {
	j.split(x)
	f := 0.0
	grad := make([]float64, j.nFree)
	var h *mat.SymDense
	if needH {
		h = mat.NewSymDense(j.nFree, nil)
	}
	for g, ws := range j.parts {
		fg, gg, hg, err := ws.derivatives(j.xg[g], needH)
		if err != nil {
			return math.Inf(1), nil, nil, fmt.Errorf("group %d: %w", g+1, err)
		}
		c, ix := j.c[g], j.idx[g]
		f += c * fg
		for k, t := range ix {
			grad[t] += c * gg[k]
		}
		if !needH {
			continue
		}
		addJoint(h, hg, ix, c)
	}
	return f, grad, h, nil
}

// addJoint adds c·hg (a group's symmetric matrix over its free entries)
// into h over the joint indices ix.
func addJoint(h, hg *mat.SymDense, ix []int, c float64) {
	for a, ta := range ix {
		for b := a; b < len(ix); b++ {
			tb := ix[b]
			v := c * hg.At(a, b)
			if ta == tb && a != b {
				v *= 2
			}
			h.SetSym(ta, tb, h.At(ta, tb)+v)
		}
	}
}

// jointDelta maps a group's Δ (rows its statistics, columns its free
// entries) onto the joint free vector: a shared parameter's columns sum.
func jointDelta(d [][]float64, ix []int, n int) [][]float64 {
	out := make([][]float64, len(d))
	for r, row := range d {
		out[r] = make([]float64, n)
		for k, t := range ix {
			out[r][t] += row[k]
		}
	}
	return out
}

// FitOrdinalGroups fits model (NewGroupModel with ModelOptions.Ordered)
// by DWLS or WLSMV jointly to every group's rows (at least two groups).
// A refused input is the group's two-step *OrdinalError (its Group the
// group's position in groups), a group_partial threshold row the data
// do not hold a *GroupSpecError.
func FitOrdinalGroups(model *Model, groups []OrdinalGroup, est Estimator, opt Options) (*GroupFit, error) {
	return fitOrdinalGroups(model, groups, est, opt, false)
}

func fitOrdinalGroups(model *Model, gs []OrdinalGroup, est Estimator, opt Options, pointOnly bool) (*GroupFit, error) {
	if len(gs) < 2 {
		return nil, errors.New("a multi-group fit needs at least two gs")
	}
	if !model.MeanStructure || !model.lavaan || !model.opt.grouped {
		return nil, errors.New("a multi-group model is specified by NewGroupModel")
	}
	opt, err := ordinalOptions(model, est, opt)
	if err != nil {
		return nil, err
	}
	ref, levels := 0, make([]int, len(gs))
	seenRef, seenLevel := false, map[int]bool{}
	for g, gr := range gs {
		levels[g] = g
		if model.arity > 0 {
			if gr.Level < 0 || gr.Level >= model.arity || seenLevel[gr.Level] {
				return nil, fmt.Errorf("group %q: level %d is not a distinct position of the per-group modifiers (%d values)", gr.Label, gr.Level, model.arity)
			}
			levels[g], seenLevel[gr.Level] = gr.Level, true
		}
		if gr.Reference {
			if seenRef {
				return nil, errors.New("more than one reference group")
			}
			ref, seenRef = g, true
		}
		if len(gr.In.Levels) != len(gs[0].In.Levels) || len(gr.In.Kinds) != len(gs[0].In.Kinds) {
			return nil, fmt.Errorf("group %q: its columns differ from the first group's", gr.Label)
		}
		for v := range gr.In.Levels {
			if gr.In.Kinds[v] != gs[0].In.Kinds[v] || (gr.In.Kinds[v] == KindOrdered && gr.In.Levels[v] != gs[0].In.Levels[v]) {
				return nil, fmt.Errorf("group %q: column %d's coding differs from the first group's; every group shares the pooled categories", gr.Label, v)
			}
		}
	}
	models, err := model.groupModels(levels, ref)
	if err != nil {
		return nil, err
	}
	if err := checkPartialThresholds(model, gs[0].In); err != nil {
		return nil, err
	}
	j := &jointWLS{c: make([]float64, len(gs)), idx: make([][]int, len(gs)), xg: make([][]float64, len(gs))}
	parts := make([]*wlsPart, len(gs))
	total := 0.0
	for g, gr := range gs {
		p, err := newWLSPart(models[g], gr.In, g)
		if err != nil {
			return nil, err
		}
		if g != ref && model.equal.has(GroupEqualThresholds) {
			for v, kind := range p.stats.Kinds {
				if kind == KindOrdered && p.stats.Levels[v] > 2 {
					p.ws.sc = append(p.ws.sc, v)
					p.x0 = append(p.x0, 1)
				}
			}
		}
		parts[g] = p
		j.parts = append(j.parts, p.ws)
		total += p.stats.N
	}
	shared := map[string]int{}
	var x0 []float64
	for g, p := range parts {
		ws := p.ws
		ix := make([]int, len(p.x0))
		join := func(k int, key string) {
			if key != "" {
				if t, ok := shared[key]; ok {
					ix[k] = t
					return
				}
				shared[key] = j.nFree
			}
			ix[k] = j.nFree
			x0 = append(x0, p.x0[k])
			j.nFree++
		}
		for k, par := range ws.st.free {
			join(k, ws.st.m.classKey(par))
		}
		for t, tr := range ws.th {
			join(ws.nModel+t, model.thresholdClassKey(model.Observed[tr.v], tr.k))
		}
		for t := range ws.sc {
			join(ws.nModel+len(ws.th)+t, "")
		}
		j.idx[g] = ix
		j.xg[g] = make([]float64, len(ix))
		j.c[g] = (p.stats.N - 1) / total
	}
	lower := make([]float64, j.nFree)
	for k := range lower {
		lower[k] = math.Inf(-1)
	}
	x, f, iters, conv, err := fisherMinimize(j, lower, x0, opt)
	if err != nil {
		return nil, err
	}
	status := "Fisher scoring stopped: gradient below grad_tol"
	if !conv {
		status = "Fisher scoring stopped before grad_tol"
	}
	j.split(x)
	gf := &GroupFit{N: total, Objective: f, Iterations: iters, Converged: conv, Status: status, Options: opt, theta: x}
	ests := make([][]float64, len(gs))
	deltas := make([][][]float64, len(gs))
	for g, ws := range j.parts {
		xg := append([]float64(nil), j.xg[g]...)
		e, _, _, _ := ws.est(xg)
		d, err := ws.delta(xg)
		if err != nil {
			return nil, fmt.Errorf("group %d: %w", g+1, err)
		}
		ests[g], deltas[g] = e, d
	}
	var vcov *mat.SymDense
	se := nanSlice(j.nFree)
	if !pointOnly {
		if vcov, err = j.robustVCov(x, parts, deltas, total); err != nil {
			return nil, err
		}
		for i := range se {
			se[i] = math.Sqrt(vcov.At(i, i))
		}
	}
	crit := opt.waldCrit()
	for g, ws := range j.parts {
		st, p := ws.st, parts[g]
		xg := append([]float64(nil), j.xg[g]...)
		sg, mm, c, err := ws.setDeltaResiduals(xg)
		if err != nil {
			return nil, fmt.Errorf("group %d: %w", g+1, err)
		}
		seg := make([]float64, len(xg))
		for k, t := range j.idx[g] {
			seg[k] = se[t]
		}
		n := p.stats.N
		fg := (n - 1) / n * ws.objective(xg)
		fit := &Fit{Model: st.m, N: n, Objective: fg, Iterations: iters, Converged: conv, Status: status,
			Options: opt, Sample: p.s, Implied: ws.scaledImplied(xg, sg), theta: xg, SampleMean: p.mean, ImpliedMean: st.mu(mm)}
		fit.params = st.snapshot(p.mean)
		// std_estimate reads Σ itself: an ordered variable's implied
		// variance 1/Δ² (lavaan's std.all under the delta
		// parameterization).
		fit.Estimates = append(st.estimates(xg[:ws.nModel], seg[:ws.nModel], sg, c, crit), ws.thresholdRows(xg, seg, sg, crit)...)
		fit.Estimates = append(fit.Estimates, ws.scaleRows(xg, seg, crit)...)
		if !pointOnly {
			fit.Ordinal = &OrdinalFit{Estimator: est, Stats: p.stats, Gamma: p.gamma, Obs: ws.obs, Est: ests[g], Weight: ws.w,
				Delta: deltas[g], FreeKeys: ws.freeKeys(), Joint: append([]int(nil), j.idx[g]...)}
		}
		gf.Groups = append(gf.Groups, fit)
		gf.Labels = append(gf.Labels, gs[g].Label)
		gf.Chi2Group = append(gf.Chi2Group, (n-1)*ws.objective(xg))
	}
	if len(model.defined) > 0 {
		labels := map[string]labelValue{}
		for g, ws := range j.parts {
			ix := j.idx[g]
			ws.st.addLabels(labels, gf.Groups[g].Estimates, func(k int) int { return ix[k] })
		}
		v := vcov
		if v == nil {
			v = mat.NewSymDense(j.nFree, nil)
		}
		gf.Defined = evalDefined(model.defined, labels, x, v, crit)
	}
	if pointOnly {
		return gf, nil
	}
	gf.VCov = vcov
	gf.Stats = j.stats(gf, parts, deltas, ests, est, total)
	return gf, nil
}

// checkPartialThresholds refuses a group_partial threshold row "x|tk"
// the data hold no k-th threshold of (Levels − 1 thresholds).
func checkPartialThresholds(m *Model, in TwoStepInput) error {
	for row := range m.partial {
		if !m.thresholdRowKey(row) {
			continue
		}
		v, t, _ := cutThreshold(row)
		i := m.obsIndex[v]
		if i >= len(in.Levels) || t > in.Levels[i]-1 {
			return &GroupSpecError{Param: "group_partial", Value: row,
				Msg: fmt.Sprintf("group_partial %q names threshold %d of %s, whose data hold %d categories (%d thresholds)", row, t, v, in.Levels[i], in.Levels[i]-1)}
		}
	}
	return nil
}

// cutThreshold splits a threshold row "x|tk" (thresholdRowKey) into x
// and k.
func cutThreshold(row string) (string, int, bool) {
	v, t, ok := strings.Cut(row, OpThreshold)
	if !ok || len(t) < 2 {
		return "", 0, false
	}
	k, err := strconv.Atoi(t[1:])
	return v, k, err == nil
}

// scaleRows are a grouped fit's `x ~*~ x` rows, one per ordered
// variable: its Δ — free (an SE, z, p and interval) or fixed at 1;
// std_estimate 1 (lavaan's std.all of a scale factor).
func (ws *wlsState) scaleRows(x, se []float64, crit float64) []Estimate {
	m := ws.st.m
	at := ws.nModel + len(ws.th)
	free := map[int]int{}
	for t, v := range ws.sc {
		free[v] = at + t
	}
	var out []Estimate
	n := 0
	for v, kind := range ws.kinds {
		if kind != KindOrdered {
			continue
		}
		n++
		e := Estimate{LVal: m.Observed[v], Op: OpScale, RVal: m.Observed[v], Param: fmt.Sprintf("_d%d", n),
			Value: 1, StdValue: 1, SE: math.NaN(), Z: math.NaN(), P: math.NaN(), CILower: math.NaN(), CIUpper: math.NaN()}
		if k, ok := free[v]; ok {
			e.Free, e.Value, e.SE = true, x[k], se[k]
			e.Z = e.Value / e.SE
			e.P = 2 * (1 - normCDF(math.Abs(e.Z)))
			e.CILower, e.CIUpper = e.Value-crit*e.SE, e.Value+crit*e.SE
		}
		out = append(out, e)
	}
	return out
}

// robustVCov is lavaan's robust.sem vcov of the joint free vector:
// E⁻¹ M E⁻¹ / (N − G), E the observed information of fmin and
// M = Σ_g (n_g/N) Δ_gᵀV_gΓ_gV_gΔ_g (Δ_g on the joint columns).
func (j *jointWLS) robustVCov(x []float64, parts []*wlsPart, deltas [][][]float64, total float64) (*mat.SymDense, error) {
	hobs, err := richardsonInformation(x, func(at []float64) ([]float64, error) {
		_, g, _, err := j.derivatives(at, false)
		if err != nil {
			return nil, err
		}
		for i := range g {
			g[i] /= 2
		}
		return g, nil
	})
	j.split(x)
	if err != nil {
		return nil, err
	}
	_, bread := invertInformation(hobs, true)
	k := j.nFree
	meat := mat.NewSymDense(k, nil)
	for g, p := range parts {
		dj := jointDelta(deltas[g], j.idx[g], k)
		m := sandwichMeat(dj, p.ws.w, p.nacov)
		fg := p.stats.N / total
		for a := 0; a < k; a++ {
			for b := a; b < k; b++ {
				meat.SetSym(a, b, meat.At(a, b)+fg*m.At(a, b))
			}
		}
	}
	var t1, t2 mat.Dense
	t1.Mul(bread, meat)
	t2.Mul(&t1, bread)
	div := total - float64(len(parts))
	vcov := mat.NewSymDense(k, nil)
	for a := 0; a < k; a++ {
		for b := a; b < k; b++ {
			vcov.SetSym(a, b, (t2.At(a, b)+t2.At(b, a))/2/div)
		}
	}
	return vcov, nil
}

// stats are a multi-group ordinal fit's statistics: T = Σ (n_g − 1)F_g,
// the independence baseline summed over the groups, lavaan's indices
// with the group count, GFI the n-weighted mean of the groups' (AGFI
// over every group's statistics) and, under WLSMV, the scaled-shifted
// test from lav_test_ug_trace_stream's traces.
func (j *jointWLS) stats(gf *GroupFit, parts []*wlsPart, deltas [][][]float64, ests [][]float64, est Estimator, total float64) Stats {
	G := len(parts)
	st := Stats{NParams: j.nFree, LogLik: math.NaN(), AIC: math.NaN(), BIC: math.NaN(), Chi2P: math.NaN()}
	size, pairs := 0, 0
	gfi := 0.0
	for g, p := range parts {
		ws := p.ws
		np := len(p.stats.Pairs)
		cor := len(ws.obs) - np
		size += len(ws.obs)
		pairs += np
		st.Chi2 += gf.Chi2Group[g]
		fb := 0.0
		for k := cor; k < len(ws.obs); k++ {
			fb += ws.w[k] * ws.obs[k] * ws.obs[k]
		}
		st.Chi2Baseline += (p.stats.N - 1) * fb
		t1, t2 := 0.0, 0.0
		for k, o := range ws.obs {
			dd := o - ests[g][k]
			t1 += dd * dd * ws.w[k]
			t2 += o * o * ws.w[k]
		}
		gfi += p.stats.N * (1 - t1/t2)
	}
	st.DoF = float64(size - j.nFree)
	st.DoFBaseline = float64(pairs)
	st.CFI, st.TLI, st.RMSEA = lavaanIndices(st.Chi2, st.DoF, st.Chi2Baseline, st.DoFBaseline, total, G)
	st.NFI = lavaanNFI(st.Chi2, st.DoF, st.Chi2Baseline, st.DoFBaseline)
	st.GFI = gfi / total
	st.AGFI = 1
	if st.DoF > 0 {
		st.AGFI = 1 - float64(size)/st.DoF*(1-st.GFI)
	}
	if est != EstimatorWLSMV {
		return st
	}
	k := j.nFree
	// The expected information Σ_g f_g Δ_gᵀV_gΔ_g and its inverse.
	info := mat.NewSymDense(k, nil)
	djs := make([][][]float64, G)
	for g, p := range parts {
		djs[g] = jointDelta(deltas[g], j.idx[g], k)
		e := weightedGram(djs[g], p.ws.w, p.stats.N/total)
		for a := 0; a < k; a++ {
			for b := a; b < k; b++ {
				info.SetSym(a, b, info.At(a, b)+e.At(a, b))
			}
		}
	}
	_, einv := invertInformation(info, true)
	tr, t1 := 0.0, 0.0
	mt2 := mat.NewDense(k, k, nil)
	mc := mat.NewDense(k, k, nil)
	bTr, bTr2 := 0.0, 0.0
	for g, p := range parts {
		w, gam := p.ws.w, p.nacov
		size := len(w)
		fg := p.stats.N / total
		wd := mat.NewDense(size, k, nil)
		for r := 0; r < size; r++ {
			for c := 0; c < k; c++ {
				wd.Set(r, c, w[r]*djs[g][r][c])
			}
		}
		gm := mat.NewDense(size, size, nil)
		for a := range gam {
			for b := range gam[a] {
				gm.Set(a, b, gam[a][b])
			}
		}
		var gwd, bg mat.Dense
		gwd.Mul(gm, wd)
		bg.Mul(wd.T(), &gwd)
		trv := 0.0
		for a := 0; a < size; a++ {
			trv += w[a] * gam[a][a]
			for b := 0; b < size; b++ {
				t1 += w[a] * gam[a][b] * w[b] * gam[b][a]
			}
		}
		tr += trv - fg*mat.Sum(elementMul(einv, &bg))
		vgwd := mat.NewDense(size, k, nil)
		for r := 0; r < size; r++ {
			for c := 0; c < k; c++ {
				vgwd.Set(r, c, w[r]*gwd.At(r, c))
			}
		}
		var t2g mat.Dense
		t2g.Mul(gwd.T(), vgwd)
		t2g.Scale(fg, &t2g)
		mt2.Add(mt2, &t2g)
		bg.Scale(fg, &bg)
		mc.Add(mc, &bg)
		// The baseline's U is V on the group's covariances.
		cor := size - len(p.stats.Pairs)
		for a := cor; a < size; a++ {
			bTr += w[a] * gam[a][a]
			for b := cor; b < size; b++ {
				bTr2 += w[a] * gam[a][b] * w[b] * gam[b][a]
			}
		}
	}
	var ec mat.Dense
	ec.Mul(einv, mc)
	tr2 := t1 - 2*mat.Sum(elementMul(einv, mt2))
	for a := 0; a < k; a++ {
		for b := 0; b < k; b++ {
			tr2 += ec.At(a, b) * ec.At(b, a)
		}
	}
	gf.TraceUG, gf.TraceUG2 = tr, tr2
	sc := scaledShifted(st.Chi2, st.DoF, tr, tr2)
	bsc := scaledShifted(st.Chi2Baseline, st.DoFBaseline, bTr, bTr2)
	st.Scaled = &ScaledStats{
		Chi2: sc.stat, ScalingFactor: sc.scaling, ShiftParameter: sc.shift,
		Chi2Baseline: bsc.stat, BaselineScalingFactor: bsc.scaling,
	}
	st.Scaled.Chi2P = 1 - chiCDF(sc.stat, st.DoF)
	st.Scaled.CFI, st.Scaled.TLI, st.Scaled.RMSEA = lavaanIndices(sc.stat, st.DoF, bsc.stat, st.DoFBaseline, total, G)
	return st
}

// elementMul is the elementwise product of two k × k matrices.
func elementMul(a mat.Matrix, b mat.Matrix) *mat.Dense {
	var out mat.Dense
	out.MulElem(a, b)
	return &out
}
