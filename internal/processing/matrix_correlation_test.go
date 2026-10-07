package processing

import (
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// TestMatrixCorrelation_ZeroWeightRowsCountTowardN: under a weight with
// zero-weight rows, MAT_CORRELATION's co-moment counts every
// zero-weight row toward its n (CoMoment semantics) while
// TEST_PEARSON_R skips it, so mat.n == pearson.n + zero_weight_rows;
// both add no mass for those rows, so r agrees to the parity
// tolerance. Frequency and probability weights alike.
func TestMatrixCorrelation_ZeroWeightRowsCountTowardN(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64, ByteOffset: 0},
		{Name: "y", Type: encoding.FieldTypeF64, ByteOffset: 8},
		{Name: "w", Type: encoding.FieldTypeF64, ByteOffset: 16},
	}}
	for _, kind := range []types.WeightKind{types.WeightKindFrequency, types.WeightKindProbability} {
		t.Run(string(kind), func(t *testing.T) {
			wt := types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: kind})
			req := &types.Request{Matrices: []types.MatrixSpec{{Type: types.MAT_CORRELATION, Fields: []string{"x", "y"}, Weight: wt}}}
			slots, err := buildMatrixSlotsFor(req, schema, nil, FullComputePlan())
			if err != nil || len(slots) != 1 {
				t.Fatalf("buildMatrixSlotsFor: %d slots, %v", len(slots), err)
			}
			pearson, err := newPearsonRRow(&types.Test{Type: types.TEST_PEARSON_R, Field: "x", Field2: "y", Weight: wt}, schema)
			if err != nil {
				t.Fatalf("newPearsonRRow: %v", err)
			}
			const rows = 300
			zero := 0
			for i := 0; i < rows; i++ {
				x := float64(i%17) + 0.25*float64(i%5)
				y := 2*x - float64(i%7)
				w := float64(1 + i%3)
				if kind == types.WeightKindProbability {
					w = 0.5 + 0.1*float64(i%4)
				}
				if i%9 == 4 {
					w, zero = 0, zero+1
				}
				rec := NewRecordWithNulls(schema, map[string]float64{"x": x, "y": y, "w": w}, nil)
				rec.SetMergePosition(0, i)
				if err := slots[0].UpdateRow(rec, ""); err != nil {
					t.Fatalf("matrix UpdateRow: %v", err)
				}
				if err := pearson.UpdateRow(rec); err != nil {
					t.Fatalf("pearson UpdateRow: %v", err)
				}
			}
			cm, err := slots[0].state.Tree()
			if err != nil {
				t.Fatalf("Tree: %v", err)
			}
			res, err := pearson.Finalize()
			if err != nil {
				t.Fatalf("pearson Finalize: %v", err)
			}
			pn, ok := res.Details["n"].(int64)
			if !ok {
				t.Fatalf("pearson details.n = %T", res.Details["n"])
			}
			if zero == 0 {
				t.Fatal("fixture has no zero-weight rows")
			}
			if got, want := cm.N(), pn+int64(zero); got != want {
				t.Errorf("mat.n = %d, want pearson.n %d + zero-weight rows %d = %d", got, pn, zero, want)
			}
			mres, _, err := slots[0].result()
			if err != nil {
				t.Fatalf("result: %v", err)
			}
			r, ref := mres.Primary.Values[0][1], res.Statistic
			if !(math.Abs(r-ref) <= 1e-12+1e-12*math.Abs(ref)) {
				t.Errorf("r = %s, TEST_PEARSON_R = %s", fmt.Sprint(r), fmt.Sprint(ref))
			}
		})
	}
}
