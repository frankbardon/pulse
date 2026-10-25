package sem

import (
	"errors"
	"fmt"
	"math"

	"gonum.org/v1/gonum/mat"
)

// group.go is the joint multi-group ML fit (lavaan's group =): one
// model description specified once per group (its per-group modifiers
// c(...) read at the group's level), every parameter free per group
// (configural) except a labelled one, whose value is shared wherever
// the label appears — across groups too, as lavaan reads a label — and
// the group_equal classes (invariance.go). The groups' discrepancies
// combine as lavaan's
//
//	F(θ) = Σ_g (n_g / N) F_g(θ_g),
//
// so the joint chi-square is Σ_g n_g F_g and the expected information
// Σ_g (n_g/2) H_g. A multi-group fit always follows lavaan's
// conventions (Model.Lavaan) with a mean structure.

// GroupMoments are one group's sample moments: S (adjusted, as
// FitMoments' s, or the data's), the data's covariance Raw, the sample
// mean and the row count, all over the model's Observed order. Level
// is the group's position in the per-group modifiers c(...) (its level
// index: a group without rows leaves its position unused); Reference
// marks the group whose latent means stay fixed at 0 and whose marker
// loadings stay fixed (lavaan's first group; none marked: the first).
type GroupMoments struct {
	Label     string
	S, Raw    *mat.SymDense
	Mean      []float64
	N         float64
	Level     int
	Reference bool
}

// GroupFit is a fitted multi-group model.
type GroupFit struct {
	// Labels are the groups' labels, in fit order.
	Labels []string
	// Groups are the per-group fits, index-aligned with Labels: each
	// one's Model, N (n_g), Objective (F_g), sample and implied moments
	// and Estimates (standard errors from the joint information). Each
	// group's Stats are zero; the joint figures are GroupFit.Stats.
	Groups []*Fit
	// N is the total row count Σ n_g.
	N float64
	// Objective is the minimised joint discrepancy Σ (n_g/N) F_g (twice
	// lavaan's fmin).
	Objective  float64
	Iterations int
	Converged  bool
	Status     string
	Options    Options
	// Stats are the joint statistics (lavaan's, over every group).
	Stats Stats
	// Chi2Group are the per-group chi-squares n_g F_g, summing to
	// Stats.Chi2.
	Chi2Group []float64
	// Defined are the := rows over the joint free vector and its
	// covariance (a label resolves to its group's parameter; a shared
	// one is one parameter), in description order; nil without a :=
	// line.
	Defined []Estimate
	// VCov is an ordinal fit's robust covariance of the joint free
	// vector (each group's OrdinalFit.Joint indexes it), and TraceUG /
	// TraceUG2 WLSMV's tr(UΓ) and tr((UΓ)²); nil and 0 on an ML fit.
	VCov              *mat.SymDense
	TraceUG, TraceUG2 float64
	theta             []float64
}

// NewGroupModel specifies desc for a multi-group fit: the model every
// group's copy follows (the first group's per-group modifiers), with a
// mean structure and lavaan conventions whatever opt says, and opt's
// group_equal / group_partial validated (a *GroupSpecError).
func NewGroupModel(desc string, opt ModelOptions) (*Model, error) {
	opt.MeanStructure, opt.grouped, opt.level = true, true, 0
	m, err := newModel(desc, false, opt)
	if err != nil {
		return nil, err
	}
	if err := m.specifyEquality(opt); err != nil {
		return nil, err
	}
	return m, nil
}

// jointState is the multi-group problem: one state per group, the
// groups' weights n_g / N, and idx[g][k], the joint index of group g's
// free parameter k (a labelled parameter shares one index across
// groups).
type jointState struct {
	parts []*state
	w     []float64
	idx   [][]int
	nFree int
	lower []float64
	xg    [][]float64
}

func (j *jointState) split(x []float64) {
	for g, ix := range j.idx {
		for k, t := range ix {
			j.xg[g][k] = x[t]
		}
	}
}

func (j *jointState) objective(x []float64) float64 {
	j.split(x)
	f := 0.0
	for g, st := range j.parts {
		v := st.objective(j.xg[g])
		if math.IsInf(v, 1) {
			return v
		}
		f += j.w[g] * v
	}
	return f
}

func (j *jointState) derivatives(x []float64, needH bool) (float64, []float64, *mat.SymDense, error) {
	j.split(x)
	f := 0.0
	grad := make([]float64, j.nFree)
	var h *mat.SymDense
	if needH {
		h = mat.NewSymDense(j.nFree, nil)
	}
	for g, st := range j.parts {
		fg, gg, hg, err := st.derivatives(j.xg[g], needH)
		if err != nil {
			return math.Inf(1), nil, nil, fmt.Errorf("group %d: %w", g+1, err)
		}
		w, ix := j.w[g], j.idx[g]
		f += w * fg
		for k, t := range ix {
			grad[t] += w * gg[k]
		}
		if !needH {
			continue
		}
		for a, ta := range ix {
			for b := a; b < len(ix); b++ {
				tb := ix[b]
				v := w * hg.At(a, b)
				if ta == tb && a != b {
					v *= 2
				}
				h.SetSym(ta, tb, h.At(ta, tb)+v)
			}
		}
	}
	return f, grad, h, nil
}

func (j *jointState) gradient(x []float64) []float64 {
	_, g, _, err := j.derivatives(x, false)
	if err != nil {
		g = make([]float64, len(x))
		for i := range g {
			g[i] = math.Inf(1)
		}
	}
	return g
}

// FitGroups fits model (NewGroupModel) jointly to every group's moments
// (at least two groups): the joint ML fit under lavaan's conventions —
// Fisher scoring to the optimum by default, standard errors from the
// joint expected information, lavaan's multiple-group statistics
// (ComputeStats is not needed).
func FitGroups(model *Model, groups []GroupMoments, opt Options) (*GroupFit, error) {
	if len(groups) < 2 {
		return nil, errors.New("a multi-group fit needs at least two groups")
	}
	if !model.MeanStructure || !model.lavaan || !model.opt.grouped {
		return nil, errors.New("a multi-group model is specified by NewGroupModel")
	}
	ref, levels := 0, make([]int, len(groups))
	seenRef, seenLevel := false, map[int]bool{}
	for g, gm := range groups {
		levels[g] = g
		if model.arity > 0 {
			if gm.Level < 0 || gm.Level >= model.arity || seenLevel[gm.Level] {
				return nil, fmt.Errorf("group %q: level %d is not a distinct position of the per-group modifiers (%d values)", gm.Label, gm.Level, model.arity)
			}
			levels[g], seenLevel[gm.Level] = gm.Level, true
		}
		if gm.Reference {
			if seenRef {
				return nil, errors.New("more than one reference group")
			}
			ref, seenRef = g, true
		}
	}
	models, err := model.groupModels(levels, ref)
	if err != nil {
		return nil, err
	}
	if err := opt.Validate(); err != nil {
		return nil, err
	}
	if opt.Solver == "" {
		opt.Solver = SolverFisher
	}
	opt = opt.withDefaults()
	p := len(model.Observed)
	j := &jointState{w: make([]float64, len(groups)), idx: make([][]int, len(groups)), xg: make([][]float64, len(groups))}
	total := 0.0
	for _, gm := range groups {
		total += gm.N
	}
	shared := map[string]int{}
	var lower []float64
	for g, gm := range groups {
		if r, _ := gm.S.Dims(); r != p || len(gm.Mean) != p {
			return nil, fmt.Errorf("group %q: moments over %d variables, the model has %d", gm.Label, r, p)
		}
		st := newState(models[g], gm.S)
		st.raw = gm.Raw
		st.info = opt.Information
		st.ybar = append([]float64(nil), gm.Mean...)
		_, ld, ok := invLogDet(gm.S)
		if !ok {
			return nil, fmt.Errorf("group %q: sample covariance matrix is not positive definite", gm.Label)
		}
		st.sInvLogDet = ld
		j.parts = append(j.parts, st)
		j.w[g] = gm.N / total
		ix := make([]int, len(st.free))
		for k, par := range st.free {
			if key := st.m.classKey(par); key != "" {
				if t, ok := shared[key]; ok {
					ix[k] = t
					continue
				}
				shared[key] = j.nFree
			}
			ix[k] = j.nFree
			lower = append(lower, par.lower)
			j.nFree++
		}
		j.idx[g] = ix
		j.xg[g] = make([]float64, len(st.free))
	}
	j.lower = lower
	x0 := make([]float64, j.nFree)
	seen := make([]bool, j.nFree)
	for g, st := range j.parts {
		xs := st.startValues()
		for k, t := range j.idx[g] {
			if !seen[t] {
				x0[t], seen[t] = xs[k], true
			}
		}
	}
	var (
		x      []float64
		f      float64
		iters  int
		conv   bool
		status string
	)
	switch opt.Solver {
	case SolverFisher:
		var err error
		x, f, iters, conv, err = fisherMinimize(j, lower, x0, opt)
		if err != nil {
			return nil, err
		}
		status = "Fisher scoring stopped: gradient below grad_tol"
		if !conv {
			status = "Fisher scoring stopped before grad_tol"
		}
	default:
		xl := make([]float64, j.nFree)
		xu := make([]float64, j.nFree)
		for k, lo := range lower {
			xl[k], xu[k] = math.NaN(), math.NaN()
			if !math.IsInf(lo, -1) {
				xl[k] = lo
			}
		}
		res := slsqpMinimize(j.objective, j.gradient, x0, xl, xu, opt.Ftol, opt.MaxIter)
		x, f, iters, conv, status = res.x, res.fun, res.nit, res.mode == slsqpSuccess, slsqpMessages[res.mode]
	}
	se, vcov, err := j.standardErrors(x, groups, len(model.defined) > 0)
	if err != nil {
		return nil, err
	}
	gf := &GroupFit{N: total, Objective: f, Iterations: iters, Converged: conv, Status: status, Options: opt, theta: x}
	j.split(x)
	parts := make([]lavaanPart, len(groups))
	for g, st := range j.parts {
		xg := j.xg[g]
		st.update(xg)
		sg, mm, c, err := st.sigma()
		if err != nil {
			return nil, err
		}
		seg := make([]float64, len(xg))
		for k, t := range j.idx[g] {
			seg[k] = se[t]
		}
		gm := groups[g]
		fit := &Fit{Model: st.m, N: gm.N, Objective: st.objective(xg), Iterations: iters, Converged: conv,
			Status: status, Options: opt, Sample: gm.S, Implied: sg, theta: append([]float64(nil), xg...),
			SampleMean: st.ybar, ImpliedMean: st.mu(mm)}
		fit.Estimates = st.estimates(xg, seg, sg, c, opt.waldCrit())
		fit.params = st.snapshot(st.ybar)
		gf.Groups = append(gf.Groups, fit)
		gf.Labels = append(gf.Labels, gm.Label)
		gf.Chi2Group = append(gf.Chi2Group, gm.N*fit.Objective)
		if parts[g], err = lavaanGroupPart(st.m, gm.S, sg, fit.SampleMean, fit.ImpliedMean, gm.N); err != nil {
			return nil, err
		}
	}
	gf.Stats = lavaanCombine(parts, gf.Chi2Group, j.nFree)
	if len(model.defined) > 0 {
		// A label reads its first group's parameter (group order): a
		// shared label is one joint index wherever it is read.
		labels := map[string]labelValue{}
		for g, st := range j.parts {
			ix := j.idx[g]
			st.addLabels(labels, gf.Groups[g].Estimates, func(k int) int { return ix[k] })
		}
		gf.Defined = evalDefined(model.defined, labels, x, vcov, opt.waldCrit())
	}
	return gf, nil
}

// standardErrors inverts the joint expected information
// Σ_g (n_g/2)·H_g (lavaan's, square roots taken as they are); full
// returns the inverse too (nil otherwise).
func (j *jointState) standardErrors(x []float64, groups []GroupMoments, full bool) ([]float64, *mat.SymDense, error) {
	_, _, h, err := j.derivatives(x, true)
	if err != nil {
		return nil, nil, err
	}
	// h is Σ w_g H_g with w_g = n_g / N, so the information is (N/2)·h.
	total := 0.0
	for _, gm := range groups {
		total += gm.N
	}
	k := j.nFree
	fim := mat.NewSymDense(k, nil)
	for a := 0; a < k; a++ {
		for b := a; b < k; b++ {
			fim.SetSym(a, b, total/2*h.At(a, b))
		}
	}
	diag, vcov := invertInformation(fim, full)
	se := make([]float64, k)
	for i, v := range diag {
		se[i] = math.Sqrt(v)
	}
	return se, vcov, nil
}
