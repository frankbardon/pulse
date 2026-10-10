package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// collinearityInput is a finalize input over a listwise uncentered
// MAT_COLLINEARITY plan on cols, every part rendered, with the
// unweighted rows folded into its co-moment.
func collinearityInput(t *testing.T, cols []string, rows [][]float64) *matrixFinalizeInput {
	t.Helper()
	cm, err := linalg.NewCoMoment(len(cols), linalg.Listwise)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		cm.Add(r, 1)
	}
	plan := vectors.Matrix{Name: "coll", Type: types.MAT_COLLINEARITY,
		Members: vectors.Resolved{Name: "coll", Members: cols, Labels: cols}, Streamable: true, Mergeable: true}
	compute := ComputePlan{MatrixAuxiliary: true, MatrixVectors: true, MatrixScalars: true}
	return &matrixFinalizeInput{CM: cm, slot: &matrixSlot{plan: plan, cols: cols, compute: compute}}
}

// TestMatrixCollinearity_UncenteredSingularNullsBelsley pins the
// uncentered numerically-singular path: a member whose mean dwarfs its
// spread (μ ≈ 2⁴⁰, sd ≈ 2, so sd² is below half an ulp of μ²) makes
// the scaled cross-product's intercept entry exactly μ/√fl(μ²) = 1, an
// eigenvalue of exactly 0 (the 2 × 2 [[1, 1], [1, 1]]: gonum's closed
// form, exact on every architecture). That nulls ONLY Belsley's
// figures — condition_indices, condition_number and every
// variance-decomposition cell — with a PULSE_MATRIX_SINGULAR WARNING
// (reason not_positive_definite, the outputs it nulled); R is
// invertible, so VIF, tolerance and max_vif stay defined and nothing is
// refused. The control (the same spread around a mean of 3) keeps every
// Belsley figure defined with no warning, so the path is not vacuous.
func TestMatrixCollinearity_UncenteredSingularNullsBelsley(t *testing.T) {
	for _, tc := range []struct {
		name     string
		base     float64
		singular bool
	}{
		{"intercept collinear beyond double precision", 1 << 40, true},
		{"control", 3, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := make([][]float64, 30)
			for i := range rows {
				rows[i] = []float64{tc.base + float64(i%7)}
			}
			out, err := finalizeCollinearity(collinearityInput(t, []string{"x"}, rows))
			if err != nil {
				t.Fatalf("finalize: %v", err)
			}
			vif, tol := out.Vectors["vif"].([]float64), out.Vectors["tolerance"].([]float64)
			if len(vif) != 1 || vif[0] != 1 || tol[0] != 1 || out.Scalars["max_vif"] != 1 {
				t.Errorf("vif %v, tolerance %v, max_vif %v: want defined 1", vif, tol, out.Scalars["max_vif"])
			}
			cond := out.Vectors["condition_indices"].([]float64)
			vd := out.Auxiliary["variance_decomposition"]
			if len(cond) != 2 || vd == nil || len(vd.Values) != 2 || vd.RowKeys[0] != collinearityIntercept {
				t.Fatalf("shape: condition_indices %v, variance_decomposition %+v", cond, vd)
			}
			var w *types.ResponseWarning
			for _, x := range out.Warnings {
				if x.Code == string(errors.PULSE_MATRIX_SINGULAR) {
					w = x
				}
			}
			if !tc.singular {
				if len(out.Warnings) != 0 {
					t.Errorf("control warnings %+v", out.Warnings)
				}
				if !(cond[0] == 1 && cond[1] > 1) || !(out.Scalars["condition_number"] == cond[1]) {
					t.Errorf("control condition_indices %v, condition_number %v", cond, out.Scalars["condition_number"])
				}
				for j, row := range vd.Values {
					for k, v := range row {
						if math.IsNaN(v) {
							t.Errorf("control pi[%d][%d] null", j, k)
						}
					}
				}
				return
			}
			for k, v := range cond {
				if !math.IsNaN(v) {
					t.Errorf("condition_indices[%d] = %v, want null", k, v)
				}
			}
			if !math.IsNaN(out.Scalars["condition_number"]) {
				t.Errorf("condition_number = %v, want null", out.Scalars["condition_number"])
			}
			for j, row := range vd.Values {
				for k, v := range row {
					if !math.IsNaN(v) {
						t.Errorf("pi[%d][%d] = %v, want null", j, k, v)
					}
				}
			}
			if len(out.Warnings) != 1 || w == nil {
				t.Fatalf("warnings %+v, want PULSE_MATRIX_SINGULAR alone", out.Warnings)
			}
			if w.Details["reason"] != "not_positive_definite" || w.Details["matrix"] != "coll" {
				t.Errorf("warning details %+v", w.Details)
			}
			outputs, _ := w.Details["outputs"].([]string)
			if len(outputs) != 3 || outputs[0] != "condition_indices" || outputs[1] != "condition_number" || outputs[2] != "variance_decomposition" {
				t.Errorf("warning outputs %v", w.Details["outputs"])
			}
		})
	}
}
