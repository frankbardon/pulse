//go:build heavy

package pulse

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"strconv"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestSEMShape_OneRequestMatchesSemopy: one request, six MAT_SEM slots
// (63 and 89 indicators over every row, and the 63 model over ids < 50,
// n = 47 < p = 67; each at the defaults and at ftol 1e-14), every slot
// in records encoding over a z-scored vector whose x01 is a formula
// attribute (the singular vector's x01s, null outside its rows, is
// labelled x01 so the same model text names it). The non-singular slots
// fit raw with no warning; the n < p slots fit semopy's cov_nearest
// substitute and warn PULSE_MATRIX_NEAREST_PD.
//
// Tolerances are this shape's, set explicitly (shapeDefaultTol,
// shapeTightTol). The 0/1 battery's objective is flat along a
// first-order factor's scale — its loadings move together against its
// construct path and its small latent variance; the expected
// information's smallest eigenvalue is ~11 against ~1e4 at full n
// (~0.7 against ~1.7e3 at n < p). Two consequences, measured: (1) the
// default ftol 1e-6 stop lies ~1e-3 (relative) short of the optimum
// along that direction, yet MAT_SEM reproduces semopy's stopping
// iterate — same iteration count, every figure within 4e-10 — so the
// default pin is 1e-8, not the 1e-4 near_* needs; (2) at ftol 1e-14
// the optimum is resolved along it only to ~sqrt(eps·F/h) ≈ 1e-7..1e-6
// (estimates up to 9e-7 apart, iteration counts differ by up to five),
// so the tight pin is 5e-6. semopy varies on its own at both levels:
// two runs of fit_semopy_shape.py (Python set order differs per
// process) differ by up to 3e-10 at the default and 3e-7 tight.
// internal/sem TestFit_ShapeMatchesSemopy pins the default fits from
// the rows' own S.
func TestSEMShape_OneRequestMatchesSemopy(t *testing.T) {
	p := shapeCohort(t)
	models := map[int]string{}
	for _, n := range []int{63, 89} {
		b, err := os.ReadFile(shapeTestdata + "shape" + strconv.Itoa(n) + "_model.txt")
		if err != nil {
			t.Fatal(err)
		}
		models[n] = string(b)
	}
	m63, m89 := shapeMembers(63), shapeMembers(89)
	sing := append([]string{"x01s"}, m63[1:]...)
	z := types.VectorStandardizeZScore
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "shape.pulse"},
		Attributes: []*types.Attribute{
			{Type: types.ATTR_FORMULA, Label: "x01", Expression: "1 - x01r"},
			{Type: types.ATTR_FORMULA, Label: "x01s", Expression: "id < 50 ? 1 - x01r : nil"},
		},
		Vectors: []types.VectorSpec{
			{Name: "v63", Fields: m63, Standardize: z},
			{Name: "v89", Fields: m89, Standardize: z},
			{Name: "v63s", Fields: sing, Labels: m63, Standardize: z},
		},
	}
	type slot struct {
		name, vector, ref string
		model             int
		tight             bool
	}
	var slots []slot
	for _, s := range []struct {
		vector, ref string
		model       int
	}{{"v63", "shape63", 63}, {"v89", "shape89", 89}, {"v63s", "shape63_singular", 63}} {
		slots = append(slots, slot{s.ref, s.vector, s.ref, s.model, false}, slot{s.ref + "_tight", s.vector, s.ref, s.model, true})
	}
	for _, s := range slots {
		params := map[string]any{"model": models[s.model]}
		if s.tight {
			params["ftol"], params["max_iter"] = 1e-14, 100000
		}
		raw, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		req.Matrices = append(req.Matrices, types.MatrixSpec{Name: s.name, Type: types.MAT_SEM, Vector: s.vector, Params: raw, Encoding: types.MatrixEncodingRecords})
	}
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Matrices) != len(slots) {
		t.Fatalf("%d matrices, want %d", len(resp.Matrices), len(slots))
	}
	for i, s := range slots {
		res := resp.Matrices[i]
		t.Run(s.name, func(t *testing.T) {
			kind, tol := "default", shapeDefaultTol
			if s.tight {
				kind, tol = "tight", shapeTightTol
			}
			rb, err := os.ReadFile(shapeTestdata + s.ref + "_semopy_" + kind + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var ref shapeSemopy
			if err := json.Unmarshal(rb, &ref); err != nil {
				t.Fatal(err)
			}
			if res.Name != s.name || res.Error != nil {
				t.Fatalf("%s: error %+v", res.Name, res.Error)
			}
			if res.Scalars["n"] != ref.N || res.Scalars["converged"] != 1 {
				t.Fatalf("n %v converged %v, semopy n %v", res.Scalars["n"], res.Scalars["converged"], ref.N)
			}
			if !s.tight && res.Scalars["iterations"] != ref.NIter {
				t.Errorf("iterations %v, semopy's default stop %v", res.Scalars["iterations"], ref.NIter)
			}
			// P3: only the n < p slots substitute, as semopy does.
			var codes []string
			for _, w := range res.Warnings {
				codes = append(codes, w.Code)
			}
			if ref.Nearest {
				if len(res.Warnings) != 1 || res.Warnings[0].Code != string(perr.PULSE_MATRIX_NEAREST_PD) {
					t.Fatalf("warnings %v, want PULSE_MATRIX_NEAREST_PD only", codes)
				}
				d := res.Warnings[0].Details
				if d["n"] != ref.N || d["p"] != ref.P {
					t.Errorf("nearest details %v, want n %v p %v", d, ref.N, ref.P)
				}
				if me, _ := d["min_eigenvalue"].(float64); math.Abs(me-ref.MinEig) > 1e-12 {
					t.Errorf("min_eigenvalue %v, semopy's input %v", d["min_eigenvalue"], ref.MinEig)
				}
			} else if len(res.Warnings) != 0 {
				t.Fatalf("warnings %v, want none (S factors)", codes)
			}
			checkShapeRecords(t, res, ref, tol)
		})
	}
}
