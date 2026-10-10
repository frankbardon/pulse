package service

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// matrix_pca_test.go is MAT_PCA end to end: the R oracle (eigen /
// prcomp, psych::KMO, psych::cortest.bartlett on N* — unweighted and
// both weight kinds), the retention rules, the determinism gate (sign,
// order and shape exact; values to tolerance) and the pairwise N* rule.

// pcaOracle is one mv_pca.json case.
type pcaOracle struct {
	Fixture       string      `json:"fixture"`
	Fields        []string    `json:"fields"`
	On            string      `json:"on"`
	Missing       string      `json:"missing"`
	Weight        string      `json:"weight"`
	NStar         float64     `json:"n_star"`
	Input         [][]float64 `json:"input"`
	Eigenvalues   []float64   `json:"eigenvalues"`
	Eigenvectors  [][]float64 `json:"eigenvectors"`
	Loadings      [][]float64 `json:"loadings"`
	Explained     []float64   `json:"explained_variance"`
	Cumulative    []float64   `json:"cumulative"`
	Kaiser        *int        `json:"kaiser_components"`
	Communalities []*float64  `json:"communalities_kaiser"`
	KMO           float64     `json:"kmo"`
	MSA           []float64   `json:"kmo_msa"`
	BartlettChisq float64     `json:"bartlett_chisq"`
	BartlettDF    float64     `json:"bartlett_df"`
	BartlettP     float64     `json:"bartlett_p"`
}

// pcaOracleTol pins every PCA figure against R: absolute + relative.
// The eigensolver is gonum-backed (LAPACK dsyev on the R side), so the
// values are same-machine stable and agree across architectures within
// the last ulps; 1e-9 holds with room on these well-separated spectra
// (sign, order and shape are compared exactly, not to tolerance).
const pcaOracleTol = 1e-9

func pcaSpec(name string, fields []string, params map[string]any, weight string) types.MatrixSpec {
	raw, _ := json.Marshal(params)
	s := types.MatrixSpec{Name: name, Type: types.MAT_PCA, Fields: fields, Params: raw}
	switch weight {
	case "frequency":
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w_freq", Kind: types.WeightKindFrequency})
	case "probability":
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w_prob", Kind: types.WeightKindProbability})
	}
	return s
}

func loadPCAOracle(t *testing.T) ([]pcaOracle, string) {
	t.Helper()
	var golden struct {
		Cases []pcaOracle `json:"cases"`
	}
	dir := loadMV(t, "mv_pca.json", &golden)
	if len(golden.Cases) == 0 {
		t.Fatal("oracle has no cases")
	}
	return golden.Cases, dir
}

func pcaNear(t *testing.T, where string, got, want float64) {
	t.Helper()
	if !(math.Abs(got-want) <= pcaOracleTol*(1+math.Abs(want))) {
		t.Errorf("%s = %.17g, R = %.17g", where, got, want)
	}
}

// TestMatrixPCA_MatchesROracle pins every mv_pca.json case —
// attitude and mtcars on the correlation and the covariance, likert
// ties, the weighted fixture and the listwise nulls fixture; unweighted,
// frequency (the rep() expansion: Σw = N*) and probability weights
// (Kish n_eff = N*) — plus the pairwise nulls case, whose input is
// cor(use = "pairwise.complete.obs") (the MAT_CORRELATION matrix Pulse
// analyses) and whose Bartlett N* is the smallest pair N, named by
// PULSE_MATRIX_PAIRWISE_N_STAR. Every component is kept (an integer
// k = p), so the full eigen-decomposition is compared: eigenvalues, the
// eigenvectors' and loadings' SIGN, ORDER and SHAPE exactly (the
// SymEigen convention the oracle applies too) and their values to
// tolerance, explained / cumulative shares, KMO, per-member MSA and
// Bartlett's chi-square, df and p. The kaiser default is checked
// against the oracle's kaiser_components and communalities.
func TestMatrixPCA_MatchesROracle(t *testing.T) {
	cases, dir := loadPCAOracle(t)
	cfg := fixtureFS(t, dir)
	pairwise := 0
	for _, c := range cases {
		p := len(c.Fields)
		name := fmt.Sprintf("%s/%s/%s/p%d/%s", c.Fixture, c.On, c.Missing, p, c.Weight)
		if c.Missing == vectors.MissingPairwise {
			pairwise++
		}
		t.Run(name, func(t *testing.T) {
			params := map[string]any{"on": c.On, "components": p, "missing": c.Missing}
			specs := []types.MatrixSpec{pcaSpec("all", c.Fields, params, c.Weight)}
			if c.On == vectors.PCAOnCorrelation {
				specs = append(specs, pcaSpec("kaiser", c.Fields, map[string]any{"missing": c.Missing}, c.Weight))
			}
			resp := processMatrices(t, cfg, &types.Request{
				Cohort:   &types.Cohort{Filename: filepath.Join(dir, c.Fixture+".pulse")},
				Matrices: specs,
			})
			res := resp.Matrices[0]
			wantCols := make([]string, p)
			for k := range wantCols {
				wantCols[k] = fmt.Sprintf("PC%d", k+1)
			}
			for key, mv := range map[string]*types.MatrixValues{"primary": res.Primary, "eigenvectors": res.Auxiliary["eigenvectors"]} {
				if mv == nil || mv.Kind != types.MatrixKindRectangular || mv.Encoding != types.MatrixEncodingFull ||
					!reflect.DeepEqual(mv.RowKeys, c.Fields) || !reflect.DeepEqual(mv.ColumnKeys, wantCols) || len(mv.Values) != p {
					t.Fatalf("%s shape: %+v", key, mv)
				}
			}
			for i := 0; i < p; i++ {
				for k := 0; k < p; k++ {
					for key, pair := range map[string][2]float64{
						"eigenvectors": {res.Auxiliary["eigenvectors"].Values[i][k], c.Eigenvectors[i][k]},
						"loadings":     {res.Primary.Values[i][k], c.Loadings[i][k]},
					} {
						got, want := pair[0], pair[1]
						if math.Abs(want) > 1e-6 && math.Signbit(got) != math.Signbit(want) {
							t.Errorf("%s[%d][%d] sign: %v, R %v", key, i, k, got, want)
						}
						pcaNear(t, fmt.Sprintf("%s[%d][%d]", key, i, k), got, want)
					}
				}
			}
			for key, want := range map[string][]float64{
				"eigenvalues": c.Eigenvalues, "explained_variance": c.Explained, "cumulative": c.Cumulative, "kmo_msa": c.MSA,
			} {
				got := relFloats(t, res.Vectors[key])
				if len(got) != len(want) {
					t.Fatalf("%s length %d, want %d", key, len(got), len(want))
				}
				for i := range want {
					pcaNear(t, fmt.Sprintf("%s[%d]", key, i), got[i], want[i])
				}
			}
			pcaNear(t, "kmo", res.Scalars["kmo"], c.KMO)
			pcaNear(t, "bartlett_chisq", res.Scalars["bartlett_chisq"], c.BartlettChisq)
			pcaNear(t, "bartlett_p", res.Scalars["bartlett_p"], c.BartlettP)
			if res.Scalars["bartlett_df"] != c.BartlettDF || res.Scalars["components_retained"] != float64(p) {
				t.Errorf("df %v (R %v), retained %v", res.Scalars["bartlett_df"], c.BartlettDF, res.Scalars["components_retained"])
			}
			if c.Missing == vectors.MissingPairwise {
				// Bartlett reads the smallest pair N, and says so.
				w := findWarning(res, errors.PULSE_MATRIX_PAIRWISE_N_STAR)
				if len(res.Warnings) != 1 || w == nil || w.Details["n_star"] != c.NStar {
					t.Errorf("warnings %v (%+v), want PULSE_MATRIX_PAIRWISE_N_STAR alone with n_star %v", matWarningCodes(res), w, c.NStar)
				}
				if res.Auxiliary["n"] == nil {
					t.Error("pairwise auxiliary.n missing")
				}
			} else if len(res.Warnings) != 0 {
				t.Errorf("warnings %v", matWarningCodes(res))
			}
			if c.On != vectors.PCAOnCorrelation {
				return
			}
			ks := resp.Matrices[1]
			k := *c.Kaiser
			if ks.Scalars["components_retained"] != float64(k) || len(ks.Primary.ColumnKeys) != k || len(ks.Primary.Values[0]) != k {
				t.Fatalf("kaiser kept %v (%v), R %d", ks.Scalars["components_retained"], ks.Primary.ColumnKeys, k)
			}
			comm := relFloats(t, ks.Vectors["communalities"])
			for i, w := range c.Communalities {
				pcaNear(t, fmt.Sprintf("communalities[%d]", i), comm[i], *w)
			}
		})
	}
	if pairwise == 0 {
		t.Fatal("oracle has no pairwise case")
	}
}

// TestMatrixPCA_SpectrumKernelMatchesOracle feeds every oracle case's
// own input matrix (the pairwise one included) through linalg.SymEigen
// and checks the eigen-decomposition against R exactly in sign, order
// and shape: the convention the operator inherits.
func TestMatrixPCA_SpectrumKernelMatchesOracle(t *testing.T) {
	cases, _ := loadPCAOracle(t)
	for n, c := range cases {
		s, err := linalg.NewSymFromRows(c.Input)
		if err != nil {
			t.Fatal(err)
		}
		eig, err := linalg.SymEigen(s)
		if err != nil {
			t.Fatal(err)
		}
		for k, want := range c.Eigenvalues {
			pcaNear(t, fmt.Sprintf("case %d λ[%d]", n, k), eig.Values.At(k), want)
			for i := range c.Input {
				got := eig.Vectors.At(i, k)
				if w := c.Eigenvectors[i][k]; math.Abs(w) > 1e-6 && math.Signbit(got) != math.Signbit(w) {
					t.Errorf("case %d v[%d][%d] sign %v, R %v", n, i, k, got, w)
				}
				pcaNear(t, fmt.Sprintf("case %d v[%d][%d]", n, i, k), got, c.Eigenvectors[i][k])
			}
		}
	}
}

// TestMatrixPCA_RetentionRules: kaiser (λ > 1), an integer k and
// {"variance": share} (the fewest leading components reaching the
// share) on the attitude correlation, whose eigenvalues are 3.716,
// 1.141, 0.847, … (cumulative 0.531, 0.694, 0.815, …).
func TestMatrixPCA_RetentionRules(t *testing.T) {
	cases, dir := loadPCAOracle(t)
	cfg := fixtureFS(t, dir)
	c := cases[0]
	if c.Fixture != "attitude" || c.On != vectors.PCAOnCorrelation || c.Weight != "none" {
		t.Fatalf("case 0 is %s/%s/%s", c.Fixture, c.On, c.Weight)
	}
	for _, rc := range []struct {
		components any
		want       int
	}{
		{nil, 2},
		{"kaiser", 2},
		{3, 3},
		{map[string]any{"variance": 0.5}, 1},
		{map[string]any{"variance": 0.7}, 3},
		{map[string]any{"variance": 1}, len(c.Fields)},
	} {
		params := map[string]any{}
		if rc.components != nil {
			params["components"] = rc.components
		}
		resp := processMatrices(t, cfg, &types.Request{
			Cohort:   &types.Cohort{Filename: filepath.Join(dir, "attitude.pulse")},
			Matrices: []types.MatrixSpec{pcaSpec("pca", c.Fields, params, "none")},
		})
		res := resp.Matrices[0]
		if res.Scalars["components_retained"] != float64(rc.want) || len(res.Primary.ColumnKeys) != rc.want ||
			len(res.Auxiliary["eigenvectors"].ColumnKeys) != rc.want {
			t.Errorf("components %v: kept %v (%v), want %d", rc.components, res.Scalars["components_retained"], res.Primary.ColumnKeys, rc.want)
		}
		if got := relFloats(t, res.Vectors["eigenvalues"]); len(got) != len(c.Fields) {
			t.Errorf("components %v: %d eigenvalues, want all %d", rc.components, len(got), len(c.Fields))
		}
	}
}

// TestMatrixPCA_Refusals: the params rules — every refusal
// SERVICE_VALIDATION bad_params.
func TestMatrixPCA_Refusals(t *testing.T) {
	cases, dir := loadPCAOracle(t)
	cfg := fixtureFS(t, dir)
	fields := cases[0].Fields
	for _, params := range []string{
		`{"on": "covariance"}`,
		`{"on": "covariance", "components": "kaiser"}`,
		`{"on": "spearman"}`,
		`{"components": 0}`,
		`{"components": 2.5}`,
		`{"components": 8}`,
		`{"components": "all"}`,
		`{"components": {"variance": 0}}`,
		`{"components": {"variance": 1.2}}`,
		`{"components": {"share": 0.5}}`,
		`{"repair": "clip"}`,
		`{"control": "all"}`,
	} {
		_, err := New(cfg).Process(t.Context(), &types.Request{
			Cohort:   &types.Cohort{Filename: filepath.Join(dir, "attitude.pulse")},
			Matrices: []types.MatrixSpec{{Name: "pca", Type: types.MAT_PCA, Fields: fields, Params: json.RawMessage(params)}},
		})
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.SERVICE_VALIDATION || ce.Details["reason"] != "bad_params" {
			t.Errorf("%s: error %v, want SERVICE_VALIDATION bad_params", params, err)
		}
	}
}

// TestMatrixPCA_NotPSDGuard: a non-PSD pairwise correlation is refused
// with the fatal PULSE_MATRIX_NOT_PSD; params.repair "nearest"
// decomposes the repaired matrix with the repair warning, and KMO /
// Bartlett read the repaired correlation.
func TestMatrixPCA_NotPSDGuard(t *testing.T) {
	_, dir := loadPCAOracle(t)
	cfg := fixtureFS(t, dir)
	fields := []string{"x", "y", "z"}
	req := func(params string) *types.Request {
		return &types.Request{
			Cohort:   &types.Cohort{Filename: filepath.Join(dir, "nonpsd_pairwise.pulse")},
			Matrices: []types.MatrixSpec{{Name: "pca", Type: types.MAT_PCA, Fields: fields, Params: json.RawMessage(params)}},
		}
	}
	_, err := New(cfg).Process(t.Context(), req(`{"missing": "pairwise", "components": 3}`))
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_MATRIX_NOT_PSD {
		t.Fatalf("unrepaired: error %v, want PULSE_MATRIX_NOT_PSD", err)
	}
	resp := processMatrices(t, cfg, req(`{"missing": "pairwise", "components": 3, "repair": "nearest"}`))
	res := resp.Matrices[0]
	if findWarning(res, errors.PULSE_MATRIX_NOT_PSD) == nil {
		t.Fatalf("repaired: warnings %v, want PULSE_MATRIX_NOT_PSD", matWarningCodes(res))
	}
	lam := relFloats(t, res.Vectors["eigenvalues"])
	for k, v := range lam {
		if !(v > 0) {
			t.Errorf("repaired λ[%d] = %v, want positive", k, v)
		}
	}
	if k := res.Scalars["kmo"]; math.IsNaN(k) || math.IsNaN(res.Scalars["bartlett_chisq"]) {
		t.Errorf("repaired: kmo %v bartlett %v, want defined", k, res.Scalars["bartlett_chisq"])
	}
}

// TestMatrixPCA_SingularNullsKMO: a member that is the sum of two others
// leaves the components defined (a zero eigenvalue) while KMO, the MSA
// and Bartlett are null with a PULSE_MATRIX_SINGULAR warning naming the
// dependency.
func TestMatrixPCA_SingularNullsKMO(t *testing.T) {
	rows := make([][5]float64, 40)
	for i := range rows {
		a, b := float64(i%7)+0.3*float64(i%3), float64((i*5)%11)
		rows[i] = [5]float64{a, b, a + b, 1, 1}
	}
	cfg := writeMatrixCohort(t, "sing.pulse", rows)
	resp := processMatrices(t, cfg, &types.Request{
		Cohort:   &types.Cohort{Filename: "sing.pulse"},
		Matrices: []types.MatrixSpec{pcaSpec("pca", []string{"x1", "x2", "x3"}, map[string]any{"components": 3}, "none")},
	})
	res := resp.Matrices[0]
	w := findWarning(res, errors.PULSE_MATRIX_SINGULAR)
	if w == nil || !reflect.DeepEqual(w.Details["dependent_fields"], []string{"x1", "x2", "x3"}) {
		t.Fatalf("warnings %v (%+v), want PULSE_MATRIX_SINGULAR over x1, x2, x3", matWarningCodes(res), w)
	}
	for _, key := range []string{"kmo", "bartlett_chisq", "bartlett_p"} {
		if !math.IsNaN(res.Scalars[key]) {
			t.Errorf("%s = %v, want null", key, res.Scalars[key])
		}
	}
	lam := relFloats(t, res.Vectors["eigenvalues"])
	if math.IsNaN(lam[0]) || math.Abs(lam[2]) > 1e-9 {
		t.Errorf("eigenvalues %v, want defined with a zero last", lam)
	}
}
