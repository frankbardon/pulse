package service

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// matrix_pca_rotation_test.go is MAT_PCA params.rotation end to end:
// varimax against stats::varimax on the operator's own loadings
// (mv_rotation.json), the unrotated parts unchanged bit for bit, and the
// rotation-free output byte-identical to the operator before the knob.

// pcaRotationTol is the varimax agreement with R through the whole
// operator: the PCA loadings' own 1e-9 bound (pcaOracleTol) carried
// through a rotation that agrees with R to ~1e-15 on fixed input
// (TestRotation_MatchesROracle, processing).
const pcaRotationTol = 1e-9

type pcaRotationCase struct {
	Fixture   string      `json:"fixture"`
	Fields    []string    `json:"fields"`
	Weight    string      `json:"weight"`
	K         int         `json:"k"`
	Unrotated [][]float64 `json:"unrotated"`
	Varimax   struct {
		Loadings [][]float64 `json:"loadings"`
		Variance []float64   `json:"variance"`
	} `json:"varimax"`
}

// TestMatrixPCA_VarimaxMatchesROracle runs MAT_PCA with components k
// and rotation "varimax" on every mv_rotation.json fixture (unweighted,
// frequency and probability weights) beside the same spec unrotated:
// primary = R's varimax loadings after the sign-and-order rule,
// auxiliary.unrotated = R's PCA loadings AND bit-identical to the
// unrotated run's primary, vectors.rotated_variance = R's variance
// accounted, and every unrotated part (eigenvectors, eigenvalues,
// shares, communalities, KMO, Bartlett) bit-identical to the unrotated
// run.
func TestMatrixPCA_VarimaxMatchesROracle(t *testing.T) {
	var golden struct {
		Cases []pcaRotationCase `json:"cases"`
	}
	dir := loadMV(t, "mv_rotation.json", &golden)
	if len(golden.Cases) == 0 {
		t.Fatal("oracle has no cases")
	}
	cfg := fixtureFS(t, dir)
	near := func(t *testing.T, where string, got, want float64) {
		t.Helper()
		if !(math.Abs(got-want) <= pcaRotationTol*(1+math.Abs(want))) {
			t.Errorf("%s = %.17g, R = %.17g", where, got, want)
		}
	}
	for _, c := range golden.Cases {
		t.Run(fmt.Sprintf("%s/%s/k%d", c.Fixture, c.Weight, c.K), func(t *testing.T) {
			resp := processMatrices(t, cfg, &types.Request{
				Cohort: &types.Cohort{Filename: filepath.Join(dir, c.Fixture+".pulse")},
				Matrices: []types.MatrixSpec{
					pcaSpec("rotated", c.Fields, map[string]any{"components": c.K, "rotation": vectors.RotationVarimax}, c.Weight),
					pcaSpec("plain", c.Fields, map[string]any{"components": c.K}, c.Weight),
				},
			})
			rot, plain := resp.Matrices[0], resp.Matrices[1]
			un := rot.Auxiliary["unrotated"]
			if un == nil {
				t.Fatal("auxiliary.unrotated missing")
			}
			if !reflect.DeepEqual(un.Values, plain.Primary.Values) || !reflect.DeepEqual(un.ColumnKeys, plain.Primary.ColumnKeys) {
				t.Error("auxiliary.unrotated differs from the unrotated primary")
			}
			if rot.Primary.Kind != types.MatrixKindRectangular || !reflect.DeepEqual(rot.Primary.ColumnKeys, plain.Primary.ColumnKeys) ||
				!reflect.DeepEqual(rot.Primary.RowKeys, c.Fields) {
				t.Fatalf("primary shape %+v", rot.Primary)
			}
			for i := range c.Fields {
				for k := 0; k < c.K; k++ {
					near(t, fmt.Sprintf("unrotated[%d][%d]", i, k), un.Values[i][k], c.Unrotated[i][k])
					near(t, fmt.Sprintf("varimax[%d][%d]", i, k), rot.Primary.Values[i][k], c.Varimax.Loadings[i][k])
				}
			}
			rv := relFloats(t, rot.Vectors["rotated_variance"])
			if len(rv) != c.K {
				t.Fatalf("rotated_variance length %d, want %d", len(rv), c.K)
			}
			for k, w := range c.Varimax.Variance {
				near(t, fmt.Sprintf("rotated_variance[%d]", k), rv[k], w)
			}
			if !reflect.DeepEqual(rot.Auxiliary["eigenvectors"], plain.Auxiliary["eigenvectors"]) {
				t.Error("eigenvectors moved under rotation")
			}
			for _, key := range []string{"eigenvalues", "explained_variance", "cumulative", "communalities", "kmo_msa"} {
				if !reflect.DeepEqual(rot.Vectors[key], plain.Vectors[key]) {
					t.Errorf("vectors.%s moved under rotation", key)
				}
			}
			if !reflect.DeepEqual(rot.Scalars, plain.Scalars) || len(rot.Warnings) != len(plain.Warnings) {
				t.Errorf("scalars / warnings moved: %v vs %v", rot.Scalars, plain.Scalars)
			}
			if _, ok := plain.Vectors["rotated_variance"]; ok || plain.Auxiliary["unrotated"] != nil {
				t.Error("unrotated spec carries rotation outputs")
			}
		})
	}
}

// TestMatrixPCA_RotationNoneByteIdentical: rotation absent and
// rotation "none" render the same JSON bytes, with no rotation key; a
// kaiser run that keeps one component rotates nothing (k < 2) yet still
// reports unrotated + rotated_variance; the undefined-input branch
// nulls the rotation parts too.
func TestMatrixPCA_RotationNoneByteIdentical(t *testing.T) {
	cases, dir := loadPCAOracle(t)
	cfg := fixtureFS(t, dir)
	c := cases[0]
	req := func(params map[string]any) *types.Request {
		return &types.Request{
			Cohort:   &types.Cohort{Filename: filepath.Join(dir, c.Fixture+".pulse")},
			Matrices: []types.MatrixSpec{pcaSpec("pca", c.Fields, params, c.Weight)},
		}
	}
	absent, err := json.Marshal(processMatrices(t, cfg, req(map[string]any{"components": 3})).Matrices)
	if err != nil {
		t.Fatal(err)
	}
	none, err := json.Marshal(processMatrices(t, cfg, req(map[string]any{"components": 3, "rotation": "none"})).Matrices)
	if err != nil {
		t.Fatal(err)
	}
	if string(absent) != string(none) {
		t.Errorf("rotation none differs from absent:\n%s\n%s", absent, none)
	}
	var probe []map[string]any
	if err := json.Unmarshal(absent, &probe); err != nil {
		t.Fatal(err)
	}
	if aux, _ := probe[0]["auxiliary"].(map[string]any); aux["unrotated"] != nil {
		t.Error("unrotated output carries auxiliary.unrotated")
	}
	if vec, _ := probe[0]["vectors"].(map[string]any); vec["rotated_variance"] != nil {
		t.Error("unrotated output carries vectors.rotated_variance")
	}

	one := processMatrices(t, cfg, req(map[string]any{"components": 1, "rotation": "varimax"})).Matrices[0]
	if !reflect.DeepEqual(one.Primary.Values, one.Auxiliary["unrotated"].Values) {
		t.Error("a single component was rotated")
	}
	if rv := relFloats(t, one.Vectors["rotated_variance"]); len(rv) != 1 || math.Abs(rv[0]-relFloats(t, one.Vectors["eigenvalues"])[0]) > 1e-12 {
		t.Errorf("single-component rotated_variance %v, want λ₁", rv)
	}

	// A constant member leaves the input undefined: k columns of nulls,
	// rotation parts included.
	rows := make([][5]float64, 20)
	for i := range rows {
		rows[i] = [5]float64{float64(i), float64(i * i % 7), 3, 1, 1}
	}
	mcfg := writeMatrixCohort(t, "const.pulse", rows)
	res := processMatrices(t, mcfg, &types.Request{
		Cohort:   &types.Cohort{Filename: "const.pulse"},
		Matrices: []types.MatrixSpec{pcaSpec("pca", []string{"x1", "x2", "x3"}, map[string]any{"components": 2, "rotation": "varimax"}, "none")},
	}).Matrices[0]
	un := res.Auxiliary["unrotated"]
	if un == nil || len(un.ColumnKeys) != 2 || !math.IsNaN(un.Values[0][0]) || !math.IsNaN(res.Primary.Values[0][1]) {
		t.Errorf("undefined input: unrotated %+v primary %+v", un, res.Primary)
	}
	if rv := relFloats(t, res.Vectors["rotated_variance"]); len(rv) != 2 || !math.IsNaN(rv[0]) {
		t.Errorf("undefined input: rotated_variance %v", rv)
	}
}
