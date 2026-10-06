package processing

import (
	"math"

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
}

var _ BlockMerger = (*matrixSlot)(nil)

// buildMatrixSlots resolves req.Matrices against the processor's
// schema and returns one fresh slot per spec (nil when there is none).
// req is the STAMPED request: each spec's `weight` is its resolved
// weight. A type the instance hides is an unknown type.
func (p *Processor) buildMatrixSlots(req *types.Request) ([]*matrixSlot, error) {
	return buildMatrixSlotsFor(req, p.schema, p.exts)
}

// buildMatrixSlotsFor is buildMatrixSlots over an explicit schema and
// registry — shared by the Processor and the parallel reducers
// (BuildMatrixSlots).
func buildMatrixSlotsFor(req *types.Request, schema *encoding.Schema, exts *ExtensionRegistry) ([]*matrixSlot, error) {
	if req == nil || len(req.Matrices) == 0 {
		return nil, nil
	}
	plans, verr := vectors.ResolveMatrices(req, schema, func(t types.MatrixType) bool {
		return t.Streamable() && !exts.isHidden(string(t))
	})
	if verr != nil {
		return nil, verr
	}
	slots := make([]*matrixSlot, len(plans))
	for i, plan := range plans {
		st, err := NewBlockCoMoments(len(plan.Members.Members), linalg.Listwise)
		if err != nil {
			return nil, err
		}
		slots[i] = &matrixSlot{
			plan:   plan,
			weight: req.Matrices[i].Weight.Spec(),
			state:  st,
			x:      make([]float64, len(plan.Members.Members)),
		}
	}
	return slots, nil
}

// UpdateRow folds one filter-passing record. A null member is NaN
// (missing: listwise skips the row); on a weighted slot an invalid row
// weight (null, NaN / ±Inf, negative, fractional under frequency) is
// handed to the co-moment as NaN, which counts it in n_weight_invalid
// and skips the row. A weight of 0 is valid: the row counts toward n
// and adds no mass. The field argument is ignored (BlockMerger shape).
func (m *matrixSlot) UpdateRow(r *Record, _ string) error {
	for i, f := range m.plan.Members.Members {
		v, ok := r.NumericValue(f)
		if !ok {
			v = math.NaN()
		}
		m.x[i] = v
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

// result combines the blocks through the fixed tree and renders the
// operator's MatrixResult.
func (m *matrixSlot) result() (types.MatrixResult, error) {
	cm, err := m.state.Tree()
	if err != nil {
		return types.MatrixResult{}, err
	}
	fin, ok := matrixFinalizers[m.plan.Type]
	if !ok {
		return types.MatrixResult{}, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"matrix operator has no finalizer", map[string]any{"type": string(m.plan.Type)})
	}
	primary := fin(cm, m.plan)
	return types.MatrixResult{
		Name:    m.plan.Name,
		Type:    m.plan.Type,
		Primary: m.values(primary),
		Scalars: map[string]float64{"determinant": determinantSPD(primary)},
	}, nil
}

// matrixFinalizer turns a slot's merged co-moment state into the
// operator's primary matrix.
type matrixFinalizer func(cm *linalg.CoMoment, plan vectors.Matrix) *linalg.Sym

// matrixFinalizers is the built-in MAT_* registry: one finalizer per
// types.AllMatrixTypes() entry (there is no extension MAT_* category).
var matrixFinalizers = map[types.MatrixType]matrixFinalizer{
	types.MAT_COVARIANCE: finalizeCovariance,
}

// finalizeCovariance is MAT_COVARIANCE: M2 / (W − ddof), NaN where
// W − ddof ≤ 0 or a member has no mass.
func finalizeCovariance(cm *linalg.CoMoment, plan vectors.Matrix) *linalg.Sym {
	return cm.Cov(plan.DDOF)
}

// values renders a square symmetric matrix over the slot's members in
// its encoding: full rows, or the upper triangle (row r from column r).
func (m *matrixSlot) values(s *linalg.Sym) *types.MatrixValues {
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
			row = append(row, s.At(r, c))
		}
		out.Values[r] = row
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

// finalizeMatrixSlots renders every slot's result in spec order; nil
// when there is none.
func finalizeMatrixSlots(slots []*matrixSlot) ([]types.MatrixResult, error) {
	if len(slots) == 0 {
		return nil, nil
	}
	out := make([]types.MatrixResult, 0, len(slots))
	for _, s := range slots {
		res, err := s.result()
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	return out, nil
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
// there is none). req must be the STAMPED request
// (StampWeightsWith), so each spec's weight is resolved, exactly as
// the Processor builds its slots.
func BuildMatrixSlots(req *types.Request, schema *encoding.Schema, exts *ExtensionRegistry) (*MatrixSlots, error) {
	slots, err := buildMatrixSlotsFor(req, schema, exts)
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
	if len(m.slots) != len(o.slots) {
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"matrix merge: partitions differ in slot count",
			map[string]any{"slots": len(m.slots), "other_slots": len(o.slots)})
	}
	for i, s := range m.slots {
		if err := MergeBlockMerger(s, o.slots[i]); err != nil {
			return err
		}
	}
	return nil
}

// Finalize renders every slot's result in spec order (nil when there
// is none).
func (m *MatrixSlots) Finalize() ([]types.MatrixResult, error) {
	if m == nil {
		return nil, nil
	}
	return finalizeMatrixSlots(m.slots)
}
