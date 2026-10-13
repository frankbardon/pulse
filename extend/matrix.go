package extend

import (
	"encoding/json"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// MatrixOperator is an extension MAT_* operator. It is FINALIZE-ONLY:
// the engine owns accumulation — the per-block co-moments, the fixed
// merge tree over 4096-row blocks, grouped buckets, missing-value modes
// and row weights — and hands the operator the merged state once per
// matrix result (once per group bucket on a grouped request). Because
// the operator never folds rows itself, its result inherits the
// matrix bit contract: serial, any DecodeWorkers and any ShardWorkers
// count give the same bits, provided Finalize is a pure function of its
// input.
//
// Finalize MUST be pure and re-entrant: grouped requests call it once
// per bucket, and the same instance may be finalized more than once. It
// must not retain the input or anything reachable from it past the
// call. An error refuses the matrix; a *errors.CodedError (for example
// PULSE_MATRIX_SINGULAR) is returned to the caller as is.
type MatrixOperator interface {
	Finalize(in *MatrixInput) (MatrixOutput, error)
}

// MatrixFactory builds a fresh MatrixOperator for one matrix slot of
// one request. It receives the slot's spec and the cohort schema; it
// should decode and validate spec.Params here so a bad parameter fails
// before any record is read.
type MatrixFactory func(spec *types.MatrixSpec, schema *encoding.Schema) (MatrixOperator, error)

// MatrixInput is what MatrixOperator.Finalize reads. It is valid only
// for the duration of the call (see the package doc's reuse contract).
type MatrixInput struct {
	// Name is the result name (types.MatrixResult.Name).
	Name string

	// CoMoment is the slot's merged co-moment state over Members, in
	// the missing-value mode the request chose (CoMoment.Mode). It is
	// READ-ONLY: never call Add or Merge on it — the engine may share it
	// with the slot's components floor. Read the figures through Cov,
	// Corr, Mean, N, W, NEff, PairN, PairW, PairW2, PairNEff and the raw
	// per-pair co-moment PairM2.
	CoMoment *linalg.CoMoment

	// Rows is the admitted row set, present only when the registration
	// declares Streamable: false (the buffered tier, which runs the
	// request buffered and serially); nil otherwise.
	Rows MatrixRows

	// Members are the resolved member columns, in axis order: the
	// CoMoment's variable i is Members[i].
	Members []string

	// Labels are the display labels per member when the vector declares
	// explicit labels; nil otherwise.
	Labels []string

	// Params is the spec's raw params object (nil when absent).
	Params json.RawMessage

	// WeightKind is the kind of the row weight folded into CoMoment
	// (and Rows), or "" when the slot is unweighted. A registration
	// that does not declare WeightAware always sees "".
	WeightKind types.WeightKind

	// Encoding is the Values layout the request asked for; Square
	// applies it.
	Encoding types.MatrixEncoding

	// WantAuxiliary, WantVectors and WantScalars report whether the
	// request renders that part of the result (a `return` exclusion
	// skips it). An operator should not compute a part it was told to
	// skip; one it returns anyway is dropped.
	WantAuxiliary, WantVectors, WantScalars bool
}

// Square renders a square symmetric matrix over Members in the input's
// Encoding (types.MatrixEncodingUpper keeps the upper triangle, row r
// starting at column r); at reads cell (r, c). Labels are carried when
// the input has them. The result owns fresh slices.
func (in *MatrixInput) Square(at func(r, c int) float64) *types.MatrixValues {
	p := len(in.Members)
	out := &types.MatrixValues{
		Kind:       types.MatrixKindSquareSymmetric,
		Encoding:   in.encoding(),
		RowKeys:    append([]string(nil), in.Members...),
		ColumnKeys: append([]string(nil), in.Members...),
		Labels:     append([]string(nil), in.Labels...),
		Values:     make([][]float64, p),
	}
	for r := 0; r < p; r++ {
		start := 0
		if out.Encoding == types.MatrixEncodingUpper {
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

// Rect renders a rectangular matrix (types.MatrixKindRectangular): one
// row per member, one column per given key; at reads cell (r, c). It is
// always written full — the upper encoding applies to a symmetric
// matrix only. The result owns fresh slices.
func (in *MatrixInput) Rect(columns []string, at func(r, c int) float64) *types.MatrixValues {
	out := &types.MatrixValues{
		Kind:       types.MatrixKindRectangular,
		Encoding:   types.MatrixEncodingFull,
		RowKeys:    append([]string(nil), in.Members...),
		ColumnKeys: append([]string(nil), columns...),
		Labels:     append([]string(nil), in.Labels...),
		Values:     make([][]float64, len(in.Members)),
	}
	for r := range in.Members {
		row := make([]float64, len(columns))
		for c := range columns {
			row[c] = at(r, c)
		}
		out.Values[r] = row
	}
	return out
}

func (in *MatrixInput) encoding() types.MatrixEncoding {
	if in.Encoding == "" {
		return types.MatrixEncodingFull
	}
	return in.Encoding
}

// MatrixRows is the read-only buffered row set of a Streamable: false
// matrix operator: the rows the co-moment admitted, in record order. It
// is valid only for the duration of Finalize; do not retain it or a
// slice it returns. Consumer-only: Pulse implements it and may add
// methods in a minor release.
type MatrixRows interface {
	// Len returns the number of rows.
	Len() int
	// Row returns row i's member values in Members order, 0 <= i <
	// Len(). A NaN is a missing member (possible only in pairwise mode,
	// where a row with at least one present member is admitted). Do not
	// mutate or retain the slice.
	Row(i int) []float64
	// Weight returns row i's valid weight, or 1 on an unweighted slot.
	Weight(i int) float64
}

// MatrixOutput is what MatrixOperator.Finalize returns: the parts of
// one types.MatrixResult. Every key the operator fills must be declared
// in its registration's OutputKeys (Auxiliary, Vectors and Scalars by
// map key) and every Components key in its ComponentSchema; pulse.New
// checks this against a synthetic co-moment. Nil maps are absent parts.
type MatrixOutput struct {
	// Primary is the operator's matrix. Required.
	Primary *types.MatrixValues
	// Auxiliary holds named companion matrices.
	Auxiliary map[string]*types.MatrixValues
	// Vectors holds per-member or per-pair summaries.
	Vectors map[string]any
	// Scalars holds whole-matrix figures; NaN is null on the wire.
	Scalars map[string]float64
	// Warnings are this matrix's data-quality diagnostics.
	Warnings []*types.ResponseWarning
	// Components are the operator's own Response.Components.Matrices
	// keys, merged after the engine's matrix floor ({n, n_null,
	// n_listwise_dropped}, the pairwise and the weighted floor keys,
	// which the operator never emits). Nil adds none.
	Components map[string]any
}
