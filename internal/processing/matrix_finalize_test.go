package processing

import (
	stderrors "errors"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// testBufferedType is a matrix type known only to these tests: its
// finalizer is installed per test, and its plan is marked buffered.
const testBufferedType types.MatrixType = "MAT_TEST_BUFFERED"

func finalizeSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64, ByteOffset: 0, Nullable: true},
		{Name: "y", Type: encoding.FieldTypeF64, ByteOffset: 8, Nullable: true},
		{Name: "w", Type: encoding.FieldTypeF64, ByteOffset: 16, Nullable: true},
	}}
}

// installFinalizer registers fin under typ for the test's duration.
func installFinalizer(t *testing.T, typ types.MatrixType, fin matrixFinalizer) {
	t.Helper()
	prev, had := matrixFinalizers[typ]
	matrixFinalizers[typ] = fin
	t.Cleanup(func() {
		if had {
			matrixFinalizers[typ] = prev
		} else {
			delete(matrixFinalizers, typ)
		}
	})
}

// testPlanSet resolves spec as a MAT_CORRELATION over x, y and retypes
// it to typ with the given streamability — the shape a buffered
// operator's resolved plan takes.
func testPlanSet(t *testing.T, spec types.MatrixSpec, typ types.MatrixType, streamable bool, compute ComputePlan) *matrixPlanSet {
	t.Helper()
	spec.Type = types.MAT_CORRELATION
	spec.Fields = []string{"x", "y"}
	req := &types.Request{Matrices: []types.MatrixSpec{spec}}
	plans, err := vectors.ResolveMatrices(req, finalizeSchema(), nil)
	if err != nil {
		t.Fatalf("ResolveMatrices: %v", err)
	}
	plans[0].Type = typ
	plans[0].Streamable, plans[0].Mergeable = streamable, streamable
	return &matrixPlanSet{plans: plans, weights: []*types.WeightSpec{spec.Weight.Spec()}, compute: compute}
}

type finalizeRow struct {
	x, y, w float64
	nullX   bool
	nullY   bool
}

func foldFinalizeRows(t *testing.T, s *matrixSlot, rows []finalizeRow) {
	t.Helper()
	schema := finalizeSchema()
	for i, r := range rows {
		nulls := map[string]bool{}
		if r.nullX {
			nulls["x"] = true
		}
		if r.nullY {
			nulls["y"] = true
		}
		rec := NewRecordWithNulls(schema, map[string]float64{"x": r.x, "y": r.y, "w": r.w}, nulls)
		rec.SetMergePosition(0, i)
		if err := s.UpdateRow(rec, ""); err != nil {
			t.Fatalf("UpdateRow: %v", err)
		}
	}
}

// mixedRows has a null x, a null y, an all-null row, a zero weight and
// invalid weights (negative, NaN).
func mixedRows() []finalizeRow {
	return []finalizeRow{
		{x: 1, y: 2, w: 1},
		{x: 2, y: 3, w: 2},
		{nullX: true, y: 9, w: 1},
		{x: 4, nullY: true, w: 1},
		{nullX: true, nullY: true, w: 1},
		{x: 5, y: 7, w: 0},
		{x: 6, y: 1, w: -1},
		{x: 7, y: 8, w: math.NaN()},
		{x: 8, y: 5, w: 3},
	}
}

// TestMatrixRows_AdmitsWhatCoMomentAdmits: a buffered slot keeps exactly
// the rows its co-moment counts — listwise the complete rows, pairwise
// the rows with any member present, invalid weights out, zero weights
// in — in fold order, with their weights and Σw / Σw²; Pair selects a
// cell's own rows.
func TestMatrixRows_AdmitsWhatCoMomentAdmits(t *testing.T) {
	wt := types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindProbability})
	cases := []struct {
		name      string
		params    string
		wantRows  [][]float64
		wantW     []float64
		wantPairX []float64
	}{
		{"listwise", "", [][]float64{{1, 2}, {2, 3}, {5, 7}, {8, 5}}, []float64{1, 2, 0, 3}, []float64{1, 2, 5, 8}},
		{"pairwise", `{"missing":"pairwise"}`,
			[][]float64{{1, 2}, {2, 3}, {math.NaN(), 9}, {4, math.NaN()}, {5, 7}, {8, 5}},
			[]float64{1, 2, 1, 1, 0, 3}, []float64{1, 2, 5, 8}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := types.MatrixSpec{Weight: wt}
			if c.params != "" {
				spec.Params = []byte(c.params)
			}
			slots, err := testPlanSet(t, spec, testBufferedType, false, FullComputePlan()).fresh()
			if err != nil {
				t.Fatalf("fresh: %v", err)
			}
			s := slots[0]
			if s.rows == nil {
				t.Fatal("a non-streamable plan must get a row buffer")
			}
			foldFinalizeRows(t, s, mixedRows())
			cm, err := s.state.Tree()
			if err != nil {
				t.Fatalf("Tree: %v", err)
			}
			if int64(s.rows.Len()) != cm.N() {
				t.Fatalf("rows kept %d, co-moment n %d", s.rows.Len(), cm.N())
			}
			if s.rows.Len() != len(c.wantRows) {
				t.Fatalf("rows kept %d, want %d", s.rows.Len(), len(c.wantRows))
			}
			sumW, sumWSq := 0.0, 0.0
			for r, want := range c.wantRows {
				got := s.rows.Row(r)
				for j := range want {
					if !(got[j] == want[j] || math.IsNaN(got[j]) && math.IsNaN(want[j])) {
						t.Errorf("row %d = %v, want %v", r, got, want)
					}
				}
				if s.rows.Weight(r) != c.wantW[r] {
					t.Errorf("weight %d = %v, want %v", r, s.rows.Weight(r), c.wantW[r])
				}
				sumW += c.wantW[r]
				sumWSq += c.wantW[r] * c.wantW[r]
			}
			if s.rows.SumW() != sumW || s.rows.SumWSq() != sumWSq || s.rows.SumW() != cm.W() {
				t.Errorf("Σw %v Σw² %v (co-moment W %v), want %v %v", s.rows.SumW(), s.rows.SumWSq(), cm.W(), sumW, sumWSq)
			}
			xs, _, _ := s.rows.Pair(0, 1)
			if !reflect.DeepEqual(xs, c.wantPairX) {
				t.Errorf("Pair(0, 1) x = %v, want %v", xs, c.wantPairX)
			}
		})
	}
}

// TestMatrixSlot_CoMomentPlanHasNoRowBuffer: a streamable (co-moment)
// plan keeps no rows — U16 state is unchanged.
func TestMatrixSlot_CoMomentPlanHasNoRowBuffer(t *testing.T) {
	slots, err := testPlanSet(t, types.MatrixSpec{}, types.MAT_CORRELATION, true, FullComputePlan()).fresh()
	if err != nil {
		t.Fatalf("fresh: %v", err)
	}
	if slots[0].rows != nil {
		t.Fatal("a streamable plan must not buffer rows")
	}
}

// TestMatrixFinalizer_MultiOutput: a finalizer returns primary,
// auxiliary, vectors, scalars and warnings, reads a buffered slot's
// rows, and every part reaches the MatrixResult; a part the run's plan
// skips is dropped even when the finalizer built it.
func TestMatrixFinalizer_MultiOutput(t *testing.T) {
	var seen int
	installFinalizer(t, testBufferedType, func(in *matrixFinalizeInput) (matrixOutput, error) {
		if in.Rows == nil {
			t.Fatal("buffered finalizer got no rows")
		}
		seen = in.Rows.Len()
		return matrixOutput{
			Primary:   in.Square(func(r, c int) float64 { return float64(in.Rows.Len()) }),
			Auxiliary: map[string]*types.MatrixValues{"a": in.Square(func(r, c int) float64 { return float64(r + c) })},
			Vectors:   map[string]any{"v": []float64{1, 2}},
			Scalars:   map[string]float64{"s": 42},
			Warnings:  []*types.ResponseWarning{{Code: string(errors.PULSE_MATRIX_ZERO_VARIANCE), Message: "m"}},
		}, nil
	})
	full := FullComputePlan()
	noScalars := full
	noScalars.MatrixScalars, noScalars.MatrixVectors = false, false
	for _, c := range []struct {
		name    string
		compute ComputePlan
		scalars bool
	}{{"full", full, true}, {"scalars and vectors excluded", noScalars, false}} {
		t.Run(c.name, func(t *testing.T) {
			slots, err := testPlanSet(t, types.MatrixSpec{}, testBufferedType, false, c.compute).fresh()
			if err != nil {
				t.Fatalf("fresh: %v", err)
			}
			foldFinalizeRows(t, slots[0], mixedRows())
			res, _, err := slots[0].result()
			if err != nil {
				t.Fatalf("result: %v", err)
			}
			if seen != 6 || res.Primary == nil || res.Primary.Values[0][0] != 6 { // unweighted: the 6 complete rows
				t.Fatalf("primary = %+v (rows seen %d)", res.Primary, seen)
			}
			if res.Auxiliary["a"] == nil || res.Auxiliary["a"].Values[1][1] != 2 {
				t.Errorf("auxiliary = %+v", res.Auxiliary)
			}
			if len(res.Warnings) != 1 {
				t.Errorf("warnings = %+v", res.Warnings)
			}
			if c.scalars != (res.Scalars != nil) || c.scalars != (res.Vectors != nil) {
				t.Errorf("scalars %v vectors %v, want present %v", res.Scalars, res.Vectors, c.scalars)
			}
			if c.scalars && res.Scalars["s"] != 42 {
				t.Errorf("scalars = %v", res.Scalars)
			}
		})
	}
}

// TestMatrixFinalizer_CodedErrorPropagates: a finalizer's coded refusal
// reaches the caller with its own code and the annotated singular
// details — rank, condition_number and the dependent members by name.
func TestMatrixFinalizer_CodedErrorPropagates(t *testing.T) {
	installFinalizer(t, testBufferedType, func(in *matrixFinalizeInput) (matrixOutput, error) {
		s, _ := linalg.NewSymFromRows([][]float64{{1, 1}, {1, 1}})
		_, err := linalg.Cholesky(s)
		return matrixOutput{}, singularMatrixError(err, in.Plan().Name, in.Members())
	})
	slots, err := testPlanSet(t, types.MatrixSpec{}, testBufferedType, false, FullComputePlan()).fresh()
	if err != nil {
		t.Fatalf("fresh: %v", err)
	}
	foldFinalizeRows(t, slots[0], mixedRows())
	_, _, err = slots[0].result()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_MATRIX_SINGULAR {
		t.Fatalf("want PULSE_MATRIX_SINGULAR, got %v", err)
	}
	if !reflect.DeepEqual(ce.Details["dependent_fields"], []string{"x", "y"}) {
		t.Errorf("dependent_fields = %v", ce.Details["dependent_fields"])
	}
	if ce.Details["rank"] != 1 || ce.Details["matrix"] != slots[0].plan.Name {
		t.Errorf("details = %v", ce.Details)
	}
	if _, ok := ce.Details["pivot"]; !ok {
		t.Errorf("linalg details lost: %v", ce.Details)
	}
}

// TestSingularMatrixError_PassesOthersThrough: only a
// PULSE_MATRIX_SINGULAR is annotated; dependent_fields appears only
// where linalg identified the indices.
func TestSingularMatrixError_PassesOthersThrough(t *testing.T) {
	other := errors.NewCodedError(errors.PULSE_MATRIX_SHAPE_MISMATCH, "shape")
	if got := singularMatrixError(other, "m", []string{"a"}); got != other {
		t.Errorf("non-singular error rewritten: %v", got)
	}
	plain := errors.NewCodedErrorWithDetails(errors.PULSE_MATRIX_SINGULAR, "s", map[string]any{"reason": "non_finite"})
	var ce *errors.CodedError
	if !stderrors.As(singularMatrixError(plain, "m", []string{"a"}), &ce) {
		t.Fatal("not coded")
	}
	if _, ok := ce.Details["dependent_fields"]; ok {
		t.Errorf("dependent_fields without dependent_indices: %v", ce.Details)
	}
	if _, ok := plain.Details["matrix"]; ok {
		t.Error("the original error was mutated")
	}
}

// TestMatrixFinalizer_NilPrimaryIsInternal: a finalizer without a
// primary is a programming fault, PROCESSING_INTERNAL.
func TestMatrixFinalizer_NilPrimaryIsInternal(t *testing.T) {
	installFinalizer(t, testBufferedType, func(*matrixFinalizeInput) (matrixOutput, error) {
		return matrixOutput{}, nil
	})
	slots, err := testPlanSet(t, types.MatrixSpec{}, testBufferedType, false, FullComputePlan()).fresh()
	if err != nil {
		t.Fatalf("fresh: %v", err)
	}
	_, _, err = slots[0].result()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_INTERNAL {
		t.Fatalf("want PROCESSING_INTERNAL, got %v", err)
	}
}

// TestMatrixSlots_BufferedMergeRefused: buffered rows do not merge, so a
// merge reaching a buffered slot is a routing fault, never a silent
// loss of one partition's rows.
func TestMatrixSlots_BufferedMergeRefused(t *testing.T) {
	set := testPlanSet(t, types.MatrixSpec{}, testBufferedType, false, FullComputePlan())
	a, err := set.fresh()
	if err != nil {
		t.Fatalf("fresh: %v", err)
	}
	b, _ := set.fresh()
	err = (&MatrixSlots{slots: a}).Merge(&MatrixSlots{slots: b})
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_INTERNAL {
		t.Fatalf("want PROCESSING_INTERNAL, got %v", err)
	}
}

// TestCanStream_BufferedMatrixSpecRoutesBuffered: a spec whose params
// make it buffered (a rank method) routes the request — and so
// ProcessStream, which runs Process — onto the buffered path, and the
// merge gate keeps it serial; the Pearson spec beside it streams.
func TestCanStream_BufferedMatrixSpecRoutesBuffered(t *testing.T) {
	schema := finalizeSchema()
	pearson := types.MatrixSpec{Type: types.MAT_CORRELATION, Fields: []string{"x", "y"}}
	rank := pearson
	rank.Params = []byte(`{"method":"spearman"}`)
	if !CanStreamRequest(&types.Request{Matrices: []types.MatrixSpec{pearson}}, schema) {
		t.Error("a Pearson matrix must stream")
	}
	if CanStreamRequest(&types.Request{Matrices: []types.MatrixSpec{pearson, rank}}, schema) {
		t.Error("a rank-method matrix must route buffered")
	}
	if !CanMergeRequest(&types.Request{Matrices: []types.MatrixSpec{pearson}}, schema) {
		t.Error("a Pearson matrix must merge")
	}
	if CanMergeRequest(&types.Request{Matrices: []types.MatrixSpec{rank}}, schema) {
		t.Error("a rank-method matrix must not merge")
	}
}
