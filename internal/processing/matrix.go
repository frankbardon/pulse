package processing

import (
	"cmp"
	"math"
	"slices"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// matrix.go is the engine half of Request.Matrices: one matrixSlot per
// spec, the analogue of the per-aggregation onlineEntry. A slot folds
// every filter-passing row into per-merge-block linalg.CoMoment
// partials (BlockCoMoments) keyed by the row's ABSOLUTE record position,
// and finalizes through the one fixed merge tree — so the matrix is a
// function of the filtered rows alone, whatever path (streaming or
// buffered) or worker count delivered them. The slot is a BlockMerger;
// the parallel reducers hold a partition's slots as MatrixSlots and
// merge them by absorbing blocks.
//
// The members, their order and the decoded params come from
// internal/vectors.ResolveMatrices — the resolver the field-reference
// pass (runtime and predict) already ran, so a request reaching here
// resolves.

// matrixSlot is the running state of one Request.Matrices spec.
type matrixSlot struct {
	plan   vectors.Matrix
	weight *types.WeightSpec
	state  *BlockCoMoments
	x      []float64
	// dropped counts the filter-passing rows listwise deletion dropped
	// (a null member). An integer, summed across partitions at Merge,
	// so it is worker-invariant like the blocks. Always 0 under
	// pairwise.
	dropped int64
	// allNull counts the filter-passing rows whose every member is
	// null — the rows pairwise admits nowhere (its n_null). Summed at
	// Merge like dropped.
	allNull int64
	// compute is the run's plan: which result sub-parts and whether
	// the Components entry finalize renders (result).
	compute ComputePlan
}

var _ BlockMerger = (*matrixSlot)(nil)

// buildMatrixSlots resolves req.Matrices against the processor's
// schema and returns one fresh slot per spec (nil when there is none,
// or when the processor's plan accumulates no matrix). req is the
// STAMPED request: each spec's `weight` is its resolved weight. A type
// the instance hides is an unknown type.
func (p *Processor) buildMatrixSlots(req *types.Request) ([]*matrixSlot, error) {
	return buildMatrixSlotsFor(req, p.schema, p.exts, p.compute)
}

// buildMatrixSlotsFor is buildMatrixSlots over an explicit schema,
// registry and plan — shared by the Processor and the parallel
// reducers (BuildMatrixSlots).
func buildMatrixSlotsFor(req *types.Request, schema *encoding.Schema, exts *ExtensionRegistry, compute ComputePlan) ([]*matrixSlot, error) {
	set, err := resolveMatrixPlans(req, schema, exts, compute)
	if err != nil || set == nil {
		return nil, err
	}
	return set.fresh()
}

// matrixPlanSet is a request's resolved Request.Matrices specs: the
// plan and the stamped weight per spec, in spec order. Resolved once
// per run; fresh() mints zero-state slots from it (once for an
// ungrouped run, once per bucket for a grouped one).
type matrixPlanSet struct {
	plans   []vectors.Matrix
	weights []*types.WeightSpec
	compute ComputePlan
}

// resolveMatrixPlans resolves req.Matrices against schema (nil when
// there is no spec). req is the STAMPED request. A type the instance
// hides is an unknown type. A plan that accumulates no matrix (the
// `matrices` slot and components.matrices both excluded) resolves to
// nil too: no slot is minted, so no record is ever folded — the
// request-derived Request.Vectors warnings are raised elsewhere and
// stay.
func resolveMatrixPlans(req *types.Request, schema *encoding.Schema, exts *ExtensionRegistry, compute ComputePlan) (*matrixPlanSet, error) {
	if req == nil || len(req.Matrices) == 0 || !compute.AccumulatesMatrices() {
		return nil, nil
	}
	plans, verr := vectors.ResolveMatrices(req, schema, func(t types.MatrixType) bool {
		return t.Streamable() && !exts.isHidden(string(t))
	})
	if verr != nil {
		return nil, verr
	}
	set := &matrixPlanSet{plans: plans, weights: make([]*types.WeightSpec, len(plans)), compute: compute}
	for i := range plans {
		set.weights[i] = req.Matrices[i].Weight.Spec()
	}
	return set, nil
}

// fresh returns one zero-state slot per spec, in spec order, counting
// each as a matrix accumulator (WorkStats.MatrixAccumulators).
func (s *matrixPlanSet) fresh() ([]*matrixSlot, error) {
	slots := make([]*matrixSlot, len(s.plans))
	for i, plan := range s.plans {
		mode := linalg.Listwise
		if plan.Pairwise {
			mode = linalg.Pairwise
		}
		st, err := NewBlockCoMoments(len(plan.Members.Members), mode)
		if err != nil {
			return nil, err
		}
		slots[i] = &matrixSlot{
			plan:    plan,
			weight:  s.weights[i],
			state:   st,
			x:       make([]float64, len(plan.Members.Members)),
			compute: s.compute,
		}
		workMatrixAccumulators.Add(1)
	}
	return slots, nil
}

// UpdateRow folds one filter-passing record. A null member is NaN
// (missing: listwise skips the row and counts it dropped, pairwise
// skips it per pair); on a weighted slot an invalid row
// weight (null, NaN / ±Inf, negative, fractional under frequency) is
// handed to the co-moment as NaN, which counts it in n_weight_invalid
// and skips the row. A weight of 0 is valid: the row counts toward n
// and adds no mass. The field argument is ignored (BlockMerger shape).
func (m *matrixSlot) UpdateRow(r *Record, _ string) error {
	null, all := false, true
	for i, f := range m.plan.Members.Members {
		v, ok := r.NumericValue(f)
		if !ok {
			v = math.NaN()
		}
		isNaN := math.IsNaN(v)
		null = null || isNaN
		all = all && isNaN
		m.x[i] = v
	}
	if null && !m.plan.Pairwise {
		m.dropped++
	}
	if all && len(m.x) > 0 {
		m.allNull++
	}
	w := 1.0
	if m.weight != nil {
		wv, reason := readWeight(r, m.weight)
		if reason != weighting.Valid {
			wv = math.NaN()
		}
		w = wv
	}
	return m.state.Add(r, m.x, w)
}

// Finalize satisfies OnlineAggregator for the BlockMerger opt-in; a
// matrix slot has no scalar, its figure is result().
func (m *matrixSlot) Finalize() (float64, error) { return math.NaN(), nil }

// BlockMoments returns the slot's per-block state.
func (m *matrixSlot) BlockMoments() *BlockCoMoments { return m.state }

// result combines the blocks through the fixed tree and renders what
// the slot's plan computes: the operator's MatrixResult under
// MatricesSlot (zero otherwise) — its auxiliary / scalars / vectors
// sub-parts each only under their own flag — and its
// Response.Components.Matrices entry under Matrices (nil otherwise).
// A skipped sub-part is never built: absent on the wire, zero in Go.
func (m *matrixSlot) result() (types.MatrixResult, *types.MatrixComponents, error) {
	cm, err := m.state.Tree()
	if err != nil {
		return types.MatrixResult{}, nil, err
	}
	var res types.MatrixResult
	if m.compute.MatricesSlot {
		if res, err = m.render(cm); err != nil {
			return types.MatrixResult{}, nil, err
		}
	}
	if !m.compute.Matrices {
		return res, nil, nil
	}
	return res, m.components(cm), nil
}

// render is the slot's MatrixResult off the merged state cm.
func (m *matrixSlot) render(cm *linalg.CoMoment) (types.MatrixResult, error) {
	workMatrixResultBuilds.Add(1)
	fin, ok := matrixFinalizers[m.plan.Type]
	if !ok {
		return types.MatrixResult{}, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"matrix operator has no finalizer", map[string]any{"type": string(m.plan.Type)})
	}
	primary := fin(cm, m.plan)
	res := types.MatrixResult{
		Name:     m.plan.Name,
		Type:     m.plan.Type,
		Primary:  m.values(primary.At),
		Warnings: m.warnings(cm, primary),
	}
	if m.compute.MatrixScalars {
		workMatrixScalarBuilds.Add(1)
		res.Scalars = map[string]float64{"determinant": determinantSPD(primary)}
	}
	if m.plan.Pairwise && m.compute.MatrixAuxiliary {
		workMatrixAuxiliaryBuilds.Add(1)
		res.Auxiliary = map[string]*types.MatrixValues{
			"n": m.values(func(i, j int) float64 { return float64(cm.PairN(i, j)) }),
		}
	}
	if m.plan.TopPairs > 0 && m.compute.MatrixVectors {
		workMatrixVectorBuilds.Add(1)
		res.Vectors = map[string]any{"top_pairs": topPairs(primary, cm.PairN, m.plan.Members.Members, m.plan.TopPairs)}
	}
	return res, nil
}

// components renders the slot's Response.Components.Matrices entry off
// the merged state cm and the integer row tallies — every input is
// worker-invariant, so the entry is too.
func (m *matrixSlot) components(cm *linalg.CoMoment) *types.MatrixComponents {
	workMatrixComponentBuilds.Add(1)
	c := &types.MatrixComponents{
		Name:             m.plan.Name,
		Type:             m.plan.Type,
		N:                int(cm.N()),
		NListwiseDropped: int(m.dropped),
	}
	if m.plan.Pairwise {
		c.NNull = int(m.allNull)
		p := len(m.plan.Members.Members)
		if p > 0 {
			lo, hi := cm.PairN(0, 0), cm.PairN(0, 0)
			for i := 0; i < p; i++ {
				for j := i; j < p; j++ {
					n := cm.PairN(i, j)
					lo, hi = min(lo, n), max(hi, n)
				}
			}
			minN, maxN := int(lo), int(hi)
			c.MinPairN, c.MaxPairN = &minN, &maxN
		}
	} else {
		c.NNull = int(m.dropped)
	}
	if m.weight != nil {
		sw, inv := cm.W(), int(cm.NWeightInvalid())
		c.SumWeights, c.NWeightInvalid = &sw, &inv
		if m.weight.EffectiveKind() == types.WeightKindProbability {
			neff := cm.NEff()
			c.NEff = &neff
		}
	}
	if m.plan.Type == types.MAT_COVARIANCE {
		c.Operator = map[string]any{"ddof": m.plan.DDOF}
	}
	return c
}

// matrixFinalizer turns a slot's merged co-moment state into the
// operator's primary matrix.
type matrixFinalizer func(cm *linalg.CoMoment, plan vectors.Matrix) *linalg.Sym

// matrixFinalizers is the built-in MAT_* registry: one finalizer per
// types.AllMatrixTypes() entry (there is no extension MAT_* category).
var matrixFinalizers = map[types.MatrixType]matrixFinalizer{
	types.MAT_COVARIANCE:  finalizeCovariance,
	types.MAT_CORRELATION: finalizeCorrelation,
}

// finalizeCovariance is MAT_COVARIANCE: M2 / (W − ddof), NaN where
// W − ddof ≤ 0 or a member has no mass.
func finalizeCovariance(cm *linalg.CoMoment, plan vectors.Matrix) *linalg.Sym {
	return cm.Cov(plan.DDOF)
}

// finalizeCorrelation is MAT_CORRELATION (Pearson): C_ij / √(M2_ii·M2_jj)
// clamped to [−1, 1] — CoMoment.Corr, whose one-root form is
// TEST_PEARSON_R's arithmetic. A member with zero spread (constant, a
// single massed row, no rows) has no defined correlation: its whole
// row and column, diagonal included, are NaN.
func finalizeCorrelation(cm *linalg.CoMoment, _ vectors.Matrix) *linalg.Sym {
	return cm.Corr()
}

// values renders a square symmetric matrix over the slot's members in
// its encoding: full rows, or the upper triangle (row r from column r).
// at reads cell (r, c).
func (m *matrixSlot) values(at func(r, c int) float64) *types.MatrixValues {
	members := m.plan.Members.Members
	p := len(members)
	out := &types.MatrixValues{
		Kind:       types.MatrixKindSquareSymmetric,
		Encoding:   m.plan.Encoding,
		RowKeys:    append([]string(nil), members...),
		ColumnKeys: append([]string(nil), members...),
		Values:     make([][]float64, p),
	}
	if m.plan.ExplicitLabels {
		out.Labels = append([]string(nil), m.plan.Members.Labels...)
	}
	for r := 0; r < p; r++ {
		start := 0
		if m.plan.Encoding == types.MatrixEncodingUpper {
			start = r
		}
		row := make([]float64, 0, p-start)
		for c := start; c < p; c++ {
			row = append(row, at(r, c))
		}
		out.Values[r] = row
	}
	return out
}

// topPairs is MAT_CORRELATION's params.summary.top_pairs: the k
// off-diagonal pairs of r with the largest |r|. Candidates are taken in
// (row, col) axis order over the strict upper triangle and stably
// sorted by |r| descending, so equal |r| keep axis order — the ranking
// is a function of the (worker-invariant) matrix bits alone. A pair
// whose r is undefined (NaN) is not a candidate; k past the candidate
// count lists them all. pairN gives each pair's n (CoMoment.PairN: its
// own N under pairwise, the listwise N otherwise).
func topPairs(r *linalg.Sym, pairN func(i, j int) int64, members []string, k int) []types.MatrixPair {
	p := len(members)
	out := make([]types.MatrixPair, 0, p*(p-1)/2)
	for i := 0; i < p; i++ {
		for j := i + 1; j < p; j++ {
			v := r.At(i, j)
			if math.IsNaN(v) {
				continue
			}
			out = append(out, types.MatrixPair{Row: members[i], Col: members[j], R: v, N: int(pairN(i, j))})
		}
	}
	slices.SortStableFunc(out, func(a, b types.MatrixPair) int {
		return cmp.Compare(math.Abs(b.R), math.Abs(a.R))
	})
	if k < len(out) {
		out = out[:k:k]
	}
	return out
}

// determinantSPD is det(s) through the reference Cholesky (FMA-free,
// arch-independent): the product of the squared pivots, NaN when s is
// not positive definite or carries an undefined cell.
func determinantSPD(s *linalg.Sym) float64 {
	l, err := linalg.Cholesky(s)
	if err != nil {
		return math.NaN()
	}
	det := 1.0
	for i := 0; i < s.N(); i++ {
		d := l.At(i, i)
		det = float64(det*d) * d
	}
	return det
}

// finalizeMatrixSlots renders, in spec order, every slot's result when
// the plan computes the matrices slot, and each one's
// Response.Components.Matrices entry when it computes
// components.matrices; nil when there is no slot (or that half is
// skipped).
func finalizeMatrixSlots(slots []*matrixSlot) ([]types.MatrixResult, []types.MatrixComponents, error) {
	if len(slots) == 0 {
		return nil, nil, nil
	}
	var out []types.MatrixResult
	var comps []types.MatrixComponents
	for _, s := range slots {
		res, c, err := s.result()
		if err != nil {
			return nil, nil, err
		}
		if s.compute.MatricesSlot {
			out = append(out, res)
		}
		if c != nil {
			comps = append(comps, *c)
		}
	}
	return out, comps, nil
}

// attachMatrixComponents appends the matrix entries onto
// resp.Components.Matrices, allocating the shell only when there is
// one — the attachAggregationComponents pattern. Callers gate it on the
// components opt-out.
func attachMatrixComponents(resp *types.Response, comps []types.MatrixComponents) {
	if len(comps) == 0 {
		return
	}
	if resp.Components == nil {
		resp.Components = &types.ResponseComponents{}
	}
	resp.Components.Matrices = append(resp.Components.Matrices, comps...)
}

// AttachMatrixComponents is attachMatrixComponents for the parallel
// reducers.
func AttachMatrixComponents(resp *types.Response, comps []types.MatrixComponents) {
	attachMatrixComponents(resp, comps)
}

// foldMatrixRecords folds a buffered record set into every slot in
// record order.
func foldMatrixRecords(slots []*matrixSlot, records []*Record) error {
	for _, s := range slots {
		for _, r := range records {
			if err := s.UpdateRow(r, ""); err != nil {
				return err
			}
		}
	}
	return nil
}

// MatrixSlots is the parallel reducers' handle on a request's
// Request.Matrices state: one partition's slots (a decode segment's or
// a shard's). Each partition folds its own rows; partitions combine by
// Merge, which moves the other partition's per-block co-moments in
// (BlockCoMoments.Absorb) — never a partial-against-partial combine —
// so the finalized matrices are the one fixed merge tree over the same
// blocks the serial path builds, bit for bit, whatever the worker
// count or the order partitions merge in. A nil *MatrixSlots is a
// request without matrices: every method is a no-op.
type MatrixSlots struct {
	slots []*matrixSlot
}

// BuildMatrixSlots returns fresh slot state for req.Matrices (nil when
// there is none, or when compute accumulates no matrix). req must be
// the STAMPED request (StampWeightsWith), so each spec's weight is
// resolved, exactly as the Processor builds its slots; compute is the
// run's plan (the Processor's own).
func BuildMatrixSlots(req *types.Request, schema *encoding.Schema, exts *ExtensionRegistry, compute ComputePlan) (*MatrixSlots, error) {
	slots, err := buildMatrixSlotsFor(req, schema, exts, compute)
	if err != nil || len(slots) == 0 {
		return nil, err
	}
	return &MatrixSlots{slots: slots}, nil
}

// UpdateRow folds one filter-passing, merge-position-stamped record
// into every slot.
func (m *MatrixSlots) UpdateRow(r *Record) error {
	if m == nil {
		return nil
	}
	for _, s := range m.slots {
		if err := s.UpdateRow(r, ""); err != nil {
			return err
		}
	}
	return nil
}

// Merge absorbs o's per-block state slot by slot; o must not be used
// afterwards. A slot-count mismatch is PROCESSING_INTERNAL.
func (m *MatrixSlots) Merge(o *MatrixSlots) error {
	if m == nil || o == nil {
		if (m == nil) != (o == nil) {
			return errors.NewCodedError(errors.PROCESSING_INTERNAL,
				"matrix merge: one partition carries matrix state and the other none")
		}
		return nil
	}
	return mergeSlotSets(m.slots, o.slots)
}

// Finalize renders every slot's result and Response.Components.Matrices
// entry in spec order, each as the plan the slots were built under
// computes it (finalizeMatrixSlots).
func (m *MatrixSlots) Finalize() ([]types.MatrixResult, []types.MatrixComponents, error) {
	if m == nil {
		return nil, nil, nil
	}
	return finalizeMatrixSlots(m.slots)
}
