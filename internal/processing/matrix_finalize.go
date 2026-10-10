package processing

import (
	stderrors "errors"
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// matrix_finalize.go is the finalizer contract every built-in MAT_*
// operator implements (matrixFinalizers), the row buffer a BUFFERED
// matrix slot keeps, and the PULSE_MATRIX_SINGULAR annotation an
// operator applies when it refuses a matrix.

// matrixFinalizer turns a slot's finalize input into the operator's
// outputs. It may build any of the MatrixResult parts — a primary
// matrix (required), named auxiliary matrices, vectors, scalars and
// warnings — and it may refuse the matrix with a coded error (for
// example PULSE_MATRIX_SINGULAR through singularMatrixError), which the
// run returns as is. A finalizer reads the plan's compute flags
// (WantAuxiliary / WantVectors / WantScalars) and never builds a part
// the run skips; render drops one it built anyway.
type matrixFinalizer func(in *matrixFinalizeInput) (matrixOutput, error)

// matrixOutput is what a finalizer returns: MatrixResult's parts. Nil
// maps are absent parts.
type matrixOutput struct {
	Primary   *types.MatrixValues
	Auxiliary map[string]*types.MatrixValues
	Vectors   map[string]any
	Scalars   map[string]float64
	Warnings  []*types.ResponseWarning
}

// matrixFinalizeInput is what a finalizer reads. CM is the slot's
// merged co-moment state — every slot keeps one, so the floor counts
// (N, PairN, W) and the co-moment warnings are available to every
// operator. Rows is the admitted row set of a BUFFERED slot (a plan
// that is not Streamable) and nil on a co-moment slot.
type matrixFinalizeInput struct {
	CM   *linalg.CoMoment
	Rows *matrixRows
	slot *matrixSlot
}

// Plan returns the slot's resolved spec.
func (in *matrixFinalizeInput) Plan() vectors.Matrix { return in.slot.plan }

// Members returns the slot's axis members in order.
func (in *matrixFinalizeInput) Members() []string { return in.slot.plan.Members.Members }

// WantAuxiliary, WantVectors and WantScalars report whether the run's
// plan renders that part (a `return` exclusion skips it).
func (in *matrixFinalizeInput) WantAuxiliary() bool { return in.slot.compute.MatrixAuxiliary }
func (in *matrixFinalizeInput) WantVectors() bool   { return in.slot.compute.MatrixVectors }
func (in *matrixFinalizeInput) WantScalars() bool   { return in.slot.compute.MatrixScalars }

// Square renders a square symmetric matrix over the members in the
// slot's encoding; at reads cell (r, c).
func (in *matrixFinalizeInput) Square(at func(r, c int) float64) *types.MatrixValues {
	return in.slot.values(at)
}

// coMomentOutput is the U16 output set over a co-moment primary: the
// primary in the slot's encoding, the co-moment data-quality warnings,
// scalars.determinant (reference Cholesky), auxiliary.n under pairwise
// and vectors.top_pairs under params.summary.top_pairs — each part only
// when the run's plan renders it. MAT_COVARIANCE and MAT_CORRELATION
// (Pearson) are exactly this.
func (in *matrixFinalizeInput) coMomentOutput(primary *linalg.Sym) matrixOutput {
	m, cm := in.slot, in.CM
	out := matrixOutput{
		Primary:  in.Square(primary.At),
		Warnings: m.warnings(cm, primary),
	}
	if in.WantScalars() {
		out.Scalars = map[string]float64{"determinant": determinantSPD(primary)}
	}
	if m.plan.Pairwise && in.WantAuxiliary() {
		out.Auxiliary = map[string]*types.MatrixValues{
			"n": in.Square(func(i, j int) float64 { return float64(cm.PairN(i, j)) }),
		}
	}
	if m.plan.TopPairs > 0 && in.WantVectors() {
		out.Vectors = map[string]any{"top_pairs": topPairs(primary, cm.PairN, m.plan.Members.Members, m.plan.TopPairs)}
	}
	return out
}

// matrixRows is a buffered matrix slot's row store — the rankSample
// pattern (test_rank.go) widened to p columns: every row the slot's
// co-moment admits, in fold order (record order on the buffered path),
// with its weight, plus the running Σw and Σw². Admission mirrors
// linalg.CoMoment.Add exactly, so the buffer and the co-moment count
// the same rows: listwise keeps a row whose members are all present,
// pairwise one with at least one member present (a missing member is
// NaN in the row); either way an invalid weight (NaN, ±Inf, negative)
// is not kept, and a weight of 0 is (it counts toward n and adds no
// mass).
type matrixRows struct {
	p        int
	pairwise bool
	// values is row-major: row r is values[r*p : (r+1)*p].
	values       []float64
	weights      []float64
	sumW, sumWSq float64
}

func newMatrixRows(p int, pairwise bool) *matrixRows {
	return &matrixRows{p: p, pairwise: pairwise}
}

// add keeps the row x (copied) with weight w when the co-moment would
// admit it.
func (b *matrixRows) add(x []float64, w float64) {
	present, complete := false, true
	for _, v := range x {
		if math.IsNaN(v) {
			complete = false
		} else {
			present = true
		}
	}
	if (b.pairwise && !present) || (!b.pairwise && !complete) {
		return
	}
	if math.IsNaN(w) || math.IsInf(w, 0) || w < 0 {
		return
	}
	b.values = append(b.values, x...)
	b.weights = append(b.weights, w)
	b.sumW += w
	b.sumWSq += float64(w * w)
}

// Len is the number of kept rows.
func (b *matrixRows) Len() int { return len(b.weights) }

// Row returns kept row r (a view; do not modify).
func (b *matrixRows) Row(r int) []float64 { return b.values[r*b.p : (r+1)*b.p] }

// Weight returns kept row r's weight (1 on an unweighted slot).
func (b *matrixRows) Weight(r int) float64 { return b.weights[r] }

// SumW and SumWSq are Σw and Σw² over the kept rows.
func (b *matrixRows) SumW() float64   { return b.sumW }
func (b *matrixRows) SumWSq() float64 { return b.sumWSq }

// Pair returns members i and j over the kept rows where BOTH are
// present, in row order, with each row's weight — a pairwise cell's own
// row set (under listwise every kept row).
func (b *matrixRows) Pair(i, j int) (xs, ys, ws []float64) {
	for r := 0; r < b.Len(); r++ {
		row := b.Row(r)
		if math.IsNaN(row[i]) || math.IsNaN(row[j]) {
			continue
		}
		xs = append(xs, row[i])
		ys = append(ys, row[j])
		ws = append(ws, b.weights[r])
	}
	return xs, ys, ws
}

// singularMatrixError annotates a linalg PULSE_MATRIX_SINGULAR raised
// while finalizing a matrix over members: it names the matrix and,
// where linalg identified the dependent axis indices
// (details "dependent_indices"), adds "dependent_fields" — those
// members' field names in axis order. Any other error is returned
// unchanged; the original error is never mutated.
func singularMatrixError(err error, matrix string, members []string) error {
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_MATRIX_SINGULAR {
		return err
	}
	details := make(map[string]any, len(ce.Details)+2)
	for k, v := range ce.Details {
		details[k] = v
	}
	details["matrix"] = matrix
	if idx, ok := ce.Details["dependent_indices"].([]int); ok {
		fields := make([]string, 0, len(idx))
		for _, i := range idx {
			if i >= 0 && i < len(members) {
				fields = append(fields, members[i])
			}
		}
		details["dependent_fields"] = fields
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_MATRIX_SINGULAR,
		"matrix "+matrix+": "+ce.Message, details)
}
