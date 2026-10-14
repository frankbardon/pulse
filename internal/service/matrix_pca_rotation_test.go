package service

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
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

// pcaRotationGPABandTol is a GPA run the oracle marks sensitive (R's own
// loadings move by more than 1e-9 under a relative 1e-12 input change):
// the GPA stopping band, processing's rotationGPABandTol.
const pcaRotationGPABandTol = 1e-4

// pcaRotationOut is one orthogonal criterion's R result.
type pcaRotationOut struct {
	Loadings [][]float64 `json:"loadings"`
	Variance []float64   `json:"variance"`
	Epsilon  *float64    `json:"epsilon"`
	// Sensitivity is R's own response to a relative 1e-12 input change
	// (gen_rotation.R): above 1e-9 the run is compared within the GPA
	// band (processing's rotationGPABandTol), not pcaRotationTol.
	Sensitivity *float64 `json:"sensitivity"`
}

type pcaRotationCase struct {
	Fixture   string         `json:"fixture"`
	Fields    []string       `json:"fields"`
	Weight    string         `json:"weight"`
	K         int            `json:"k"`
	Unrotated [][]float64    `json:"unrotated"`
	Varimax   pcaRotationOut `json:"varimax"`
	// The orthogonal GPA criteria MAT_PCA accepts (E6-S2).
	Quartimax        pcaRotationOut   `json:"quartimax"`
	Entropy          pcaRotationOut   `json:"entropy"`
	GeominOrthogonal []pcaRotationOut `json:"geomin_orthogonal"`
}

// pcaRotationRun is one MAT_PCA rotation params set and its R result.
type pcaRotationRun struct {
	name   string
	params map[string]any
	want   pcaRotationOut
}

// orthogonalRuns lists every vectors.PCARotations criterion the case
// records: varimax, quartimax, entropy and geomin_orthogonal at each
// recorded epsilon. A PCARotations value the oracle lacks fails the
// caller (each accepted criterion needs its oracle).
func (c pcaRotationCase) orthogonalRuns() []pcaRotationRun {
	runs := []pcaRotationRun{
		{vectors.RotationVarimax, map[string]any{"rotation": vectors.RotationVarimax}, c.Varimax},
		{vectors.RotationQuartimax, map[string]any{"rotation": vectors.RotationQuartimax}, c.Quartimax},
		{vectors.RotationEntropy, map[string]any{"rotation": vectors.RotationEntropy}, c.Entropy},
	}
	for _, g := range c.GeominOrthogonal {
		runs = append(runs, pcaRotationRun{fmt.Sprintf("%s_e%g", vectors.RotationGeominOrthogonal, *g.Epsilon),
			map[string]any{"rotation": vectors.RotationGeominOrthogonal, "epsilon": *g.Epsilon}, g})
	}
	return runs
}

// TestMatrixPCA_VarimaxMatchesROracle runs MAT_PCA with components k
// and every orthogonal rotation — varimax, quartimax, entropy and
// geomin_orthogonal at ε 0.01 / 0.1 (vectors.PCARotations) — on every
// mv_rotation.json fixture (unweighted, frequency and probability
// weights) beside the same spec unrotated: primary = R's rotated
// loadings after the sign-and-order rule (GPA criteria: GPArotation's
// wrappers),
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
	nearTol := func(t *testing.T, tol float64, where string, got, want float64) {
		t.Helper()
		if !(math.Abs(got-want) <= tol*(1+math.Abs(want))) {
			t.Errorf("%s = %.17g, R = %.17g", where, got, want)
		}
	}
	covered := map[string]bool{}
	for _, c := range golden.Cases {
		for _, run := range c.orthogonalRuns() {
			covered[run.params["rotation"].(string)] = true
			t.Run(fmt.Sprintf("%s/%s/k%d/%s", c.Fixture, c.Weight, c.K, run.name), func(t *testing.T) {
				if run.want.Loadings == nil {
					t.Fatalf("oracle has no %s", run.name)
				}
				tol := pcaRotationTol
				if run.want.Sensitivity != nil && *run.want.Sensitivity > 1e-9 {
					tol = pcaRotationGPABandTol
				}
				params := map[string]any{"components": c.K}
				for k, v := range run.params {
					params[k] = v
				}
				resp := processMatrices(t, cfg, &types.Request{
					Cohort: &types.Cohort{Filename: filepath.Join(dir, c.Fixture+".pulse")},
					Matrices: []types.MatrixSpec{
						pcaSpec("rotated", c.Fields, params, c.Weight),
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
						nearTol(t, pcaRotationTol, fmt.Sprintf("unrotated[%d][%d]", i, k), un.Values[i][k], c.Unrotated[i][k])
						nearTol(t, tol, fmt.Sprintf("%s[%d][%d]", run.name, i, k), rot.Primary.Values[i][k], run.want.Loadings[i][k])
					}
				}
				rv := relFloats(t, rot.Vectors["rotated_variance"])
				if len(rv) != c.K {
					t.Fatalf("rotated_variance length %d, want %d", len(rv), c.K)
				}
				for k, w := range run.want.Variance {
					nearTol(t, tol, fmt.Sprintf("rotated_variance[%d]", k), rv[k], w)
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
				if rot.Auxiliary["factor_correlation"] != nil {
					t.Error("an orthogonal rotation carries factor_correlation")
				}
			})
		}
	}
	for _, r := range vectors.PCARotations()[1:] {
		if !covered[r] {
			t.Errorf("PCARotations value %q has no oracle run", r)
		}
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

// TestRotation_PredictMatchesRuntime is the predict ≡ runtime gate for
// the rotation knob. An OBLIQUE criterion on MAT_PCA or ATTR_PC_SCORE
// is refused by predict and by the run with the same code, reason
// (rotation_oblique_unsupported), message and details; every criterion
// on MAT_FACTOR (and every orthogonal one on MAT_PCA) predicts valid,
// runs, and its auxiliary keys are exactly the ones predict's
// vectors.Matrix.AuxiliaryKeys advertises — factor_correlation iff the
// criterion is oblique (vectors.RotationOblique). MAT_FACTOR's two
// factors on three members are not identified, so this run covers the
// null-fit branch; TestMatrixFactor_MatchesROracle covers the fitted
// one per criterion.
func TestRotation_PredictMatchesRuntime(t *testing.T) {
	cfg := writeMatrixCohort(t, "r.pulse", matrixRows(200))
	data, err := afero.ReadFile(cfg.Fs(), "r.pulse")
	if err != nil {
		t.Fatal(err)
	}
	members := []string{"x1", "x2", "x3"}
	predict := func(req *types.Request) *descriptor.Envelope {
		return descx.Predict(bytes.NewReader(data), req, &descx.PredictOptions{})
	}
	refused := 0
	for _, rot := range vectors.ObliqueRotations() {
		for _, req := range []*types.Request{
			{Matrices: []types.MatrixSpec{{Name: "pca", Type: types.MAT_PCA, Fields: members,
				Params: json.RawMessage(fmt.Sprintf(`{"components": 2, "rotation": %q}`, rot))}}},
			{Attributes: []*types.Attribute{{Type: types.ATTR_PC_SCORE, Label: "pc", Fields: members,
				Params: json.RawMessage(fmt.Sprintf(`{"component": 1, "components": 2, "rotation": %q}`, rot))}}},
		} {
			req.Cohort = &types.Cohort{Filename: "r.pulse"}
			env := predict(req)
			_, runErr := New(cfg).Process(context.Background(), req)
			var ce *errors.CodedError
			if !stderrors.As(runErr, &ce) || ce.Details["reason"] != "rotation_oblique_unsupported" {
				t.Fatalf("%s: runtime error %v, want rotation_oblique_unsupported", rot, runErr)
			}
			if len(env.Errors) != 1 || env.Errors[0].Code != string(ce.Code) || env.Errors[0].Message != ce.Message ||
				!reflect.DeepEqual(env.Errors[0].Details, ce.Details) {
				t.Errorf("%s: predict %+v, runtime %s %s %v", rot, env.Errors, ce.Code, ce.Message, ce.Details)
			}
			refused++
		}
	}
	if refused == 0 {
		t.Fatal("no oblique criterion exercised")
	}
	for _, c := range []struct {
		typ       types.MatrixType
		rotations []string
		params    string
	}{
		{types.MAT_FACTOR, vectors.FactorRotations()[1:], `{"factors": 2, "rotation": %q}`},
		{types.MAT_PCA, vectors.PCARotations()[1:], `{"components": 2, "rotation": %q}`},
	} {
		for _, rot := range c.rotations {
			label := string(c.typ) + "/" + rot
			req := &types.Request{
				Cohort:   &types.Cohort{Filename: "r.pulse"},
				Matrices: []types.MatrixSpec{{Name: "m", Type: c.typ, Fields: members, Params: json.RawMessage(fmt.Sprintf(c.params, rot))}},
			}
			env := predict(req)
			if len(env.Errors) != 0 {
				t.Fatalf("%s: predict errors %v", label, env.Errors)
			}
			resp, err := New(cfg).Process(context.Background(), req)
			if err != nil {
				t.Fatalf("%s: %v", label, err)
			}
			var got []string
			for k := range resp.Matrices[0].Auxiliary {
				got = append(got, k)
			}
			// The keys predict advertises (matrixAuxiliaryNodes reads
			// AuxiliaryKeys on the resolved spec).
			want, known := vectors.Matrix{Type: c.typ, Rotation: rot}.AuxiliaryKeys()
			if !known {
				t.Fatalf("%s: AuxiliaryKeys does not model the operator", label)
			}
			if slices.Contains(want, "factor_correlation") != vectors.RotationOblique(rot) {
				t.Errorf("%s: AuxiliaryKeys %v disagrees with RotationOblique", label, want)
			}
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("%s: runtime auxiliary %v, want %v", label, got, want)
			}
		}
	}
}

// TestRotation_EpsilonReachesTheEngine: params.epsilon moves the geomin
// fit on MAT_FACTOR (geomin, geomin_orthogonal) and MAT_PCA
// (geomin_orthogonal) — the knob is threaded to the criterion, not
// decoded and dropped — while an explicit 0.01 is the default bit for
// bit.
func TestRotation_EpsilonReachesTheEngine(t *testing.T) {
	cases, dir := loadFactorOracle(t)
	cfg := fixtureFS(t, dir)
	c := cases[2] // attitude k = 3
	run := func(typ types.MatrixType, params map[string]any) [][]float64 {
		raw, _ := json.Marshal(params)
		return processMatrices(t, cfg, &types.Request{
			Cohort:   &types.Cohort{Filename: filepath.Join(dir, c.Fixture+".pulse")},
			Matrices: []types.MatrixSpec{{Name: "m", Type: typ, Fields: c.Fields, Params: raw}},
		}).Matrices[0].Primary.Values
	}
	for _, s := range []struct {
		typ   types.MatrixType
		count string
		rot   string
	}{
		{types.MAT_FACTOR, "factors", vectors.RotationGeomin},
		{types.MAT_FACTOR, "factors", vectors.RotationGeominOrthogonal},
		{types.MAT_PCA, "components", vectors.RotationGeominOrthogonal},
	} {
		base := map[string]any{s.count: 3, "rotation": s.rot}
		def := run(s.typ, base)
		explicit := run(s.typ, map[string]any{s.count: 3, "rotation": s.rot, "epsilon": vectors.GeominDefaultEpsilon})
		wide := run(s.typ, map[string]any{s.count: 3, "rotation": s.rot, "epsilon": 0.5})
		if !reflect.DeepEqual(def, explicit) {
			t.Errorf("%s %s: epsilon 0.01 differs from the default", s.typ, s.rot)
		}
		if reflect.DeepEqual(def, wide) {
			t.Errorf("%s %s: epsilon 0.5 left the loadings unchanged", s.typ, s.rot)
		}
	}
}
