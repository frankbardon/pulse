package service

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// matrix_factor_test.go is MAT_FACTOR end to end: the R oracle
// (psych::fa minres, principal axis and maximum likelihood on the
// fixtures' correlations,
// then stats::varimax / stats::promax / GPArotation::oblimin;
// unweighted and both weight kinds), the identification, Heywood and
// not-converged guards, the Kaiser rule, the rotation-free shape and
// the refusals.

// Factor end-to-end tolerances: the kernel bounds of
// internal/processing/matrix_factor_test.go carried through the
// operator (whose correlation agrees with R's to ~1e-15).
const (
	factorPATol    = 1e-9
	factorExactTol = 1e-7
	factorMLTol    = 1e-8
	factorGPATol   = 1e-4
)

type factorRot struct {
	Loadings [][]float64 `json:"loadings"`
	Phi      [][]float64 `json:"phi"`
	Variance []float64   `json:"variance"`
}

type factorFit struct {
	Unrotated      [][]float64          `json:"unrotated"`
	Communalities  []float64            `json:"communalities"`
	Uniquenesses   []float64            `json:"uniquenesses"`
	Variance       []float64            `json:"variance"`
	RMSR           float64              `json:"rmsr"`
	EmpiricalChisq float64              `json:"empirical_chisq"`
	DF             float64              `json:"df"`
	EmpiricalP     *float64             `json:"empirical_p"`
	Rotations      map[string]factorRot `json:"rotations"`
	Iterations     *int                 `json:"iterations"`
	Converged      *bool                `json:"converged"`
	// Maximum likelihood only.
	Psi             []float64 `json:"psi"`
	LikelihoodChisq float64   `json:"likelihood_chisq"`
	LikelihoodP     *float64  `json:"likelihood_p"`
	TLI             *float64  `json:"tli"`
	RMSEA           *float64  `json:"rmsea"`
	RMSEALower      *float64  `json:"rmsea_lower"`
	RMSEAUpper      *float64  `json:"rmsea_upper"`
	BIC             *float64  `json:"bic"`
}

type factorOracle struct {
	Fixture       string     `json:"fixture"`
	Fields        []string   `json:"fields"`
	Weight        string     `json:"weight"`
	K             int        `json:"k"`
	NStar         float64    `json:"n_star"`
	KMO           float64    `json:"kmo"`
	BartlettChisq float64    `json:"bartlett_chisq"`
	BartlettDF    float64    `json:"bartlett_df"`
	BartlettP     float64    `json:"bartlett_p"`
	Minres        factorFit  `json:"minres"`
	MinresExact   *factorFit `json:"minres_exact"`
	PrincipalAxis factorFit  `json:"principal_axis"`
	MLExact       factorFit  `json:"ml_exact"`
}

func factorSpec(name string, fields []string, params map[string]any, weight string) types.MatrixSpec {
	s := pcaSpec(name, fields, params, weight)
	s.Type = types.MAT_FACTOR
	return s
}

func loadFactorOracle(t *testing.T) ([]factorOracle, string) {
	t.Helper()
	var golden struct {
		Cases []factorOracle `json:"cases"`
	}
	dir := loadMV(t, "mv_factor.json", &golden)
	if len(golden.Cases) == 0 {
		t.Fatal("oracle has no cases")
	}
	return golden.Cases, dir
}

func factorNear(t *testing.T, tol float64, where string, got, want float64) {
	t.Helper()
	if !(math.Abs(got-want) <= tol*(1+math.Abs(want))) {
		t.Errorf("%s = %.17g, R = %.17g", where, got, want)
	}
}

func factorNearRows(t *testing.T, tol float64, where string, got *types.MatrixValues, want [][]float64) {
	t.Helper()
	if got == nil || len(got.Values) != len(want) {
		t.Fatalf("%s: %+v, want %d rows", where, got, len(want))
	}
	for i := range want {
		if len(got.Values[i]) != len(want[i]) {
			t.Fatalf("%s[%d]: %d cols, want %d", where, i, len(got.Values[i]), len(want[i]))
		}
		for j := range want[i] {
			factorNear(t, tol, fmt.Sprintf("%s[%d][%d]", where, i, j), got.Values[i][j], want[i][j])
		}
	}
}

func factorKeys(k int) []string {
	out := make([]string, k)
	for c := range out {
		out[c] = fmt.Sprintf("F%d", c+1)
	}
	return out
}

// TestMatrixFactor_MatchesROracle runs MAT_FACTOR on every mv_factor.json
// case with each extraction and each rotation — none, varimax, promax
// (κ 4), oblimin (γ 0) and the further GPA criteria (quartimax,
// entropy, geomin / geomin_orthogonal at ε 0.01) — and checks the primary (unrotated or
// rotated loadings), auxiliary.unrotated, auxiliary.factor_correlation
// (oblique only), the communalities, uniquenesses and variance
// accounted, rmsr / empirical chi-square / df / p, KMO and Bartlett,
// and the fit's iterations / convergence (principal axis: psych's own
// count). Principal axis is compared with psych within factorPATol;
// minres with psych's exact optimum within factorExactTol; ml with the
// exact ML optimum within factorMLTol, plus its likelihood chi-square,
// p, TLI, RMSEA with its 90% interval and BIC (absent on the other
// extractions); a GPA criterion over
// minres or ml within GPA's stopping band, factorGPATol. The Heywood
// fixture's minres run is the subject of TestMatrixFactor_Guards.
func TestMatrixFactor_MatchesROracle(t *testing.T) {
	t.Parallel()

	cases, dir := loadFactorOracle(t)
	cfg := fixtureFS(t, dir)
	for _, c := range cases {
		for _, ext := range vectors.FactorExtractions() {
			want := c.PrincipalAxis
			tol := factorPATol
			switch ext {
			case vectors.FactorExtractionMinres:
				if c.MinresExact == nil {
					continue
				}
				want, tol = *c.MinresExact, factorExactTol
			case vectors.FactorExtractionML:
				want, tol = c.MLExact, factorMLTol
			}
			rotations := []string{vectors.RotationNone}
			if c.K >= 2 {
				rotations = vectors.FactorRotations()
			}
			for _, rot := range rotations {
				t.Run(fmt.Sprintf("%s/%s/k%d/%s/%s", c.Fixture, c.Weight, c.K, ext, rot), func(t *testing.T) {
					params := map[string]any{"factors": c.K, "extraction": ext, "rotation": rot}
					res := processMatrices(t, cfg, &types.Request{
						Cohort:   &types.Cohort{Filename: filepath.Join(dir, c.Fixture+".pulse")},
						Matrices: []types.MatrixSpec{factorSpec("fa", c.Fields, params, c.Weight)},
					}).Matrices[0]
					keys := factorKeys(c.K)
					if res.Primary.Kind != types.MatrixKindRectangular || !reflect.DeepEqual(res.Primary.RowKeys, c.Fields) || !reflect.DeepEqual(res.Primary.ColumnKeys, keys) {
						t.Fatalf("primary shape %+v", res.Primary)
					}
					rt := tol
					switch {
					case rot == vectors.RotationOblimin && ext != vectors.FactorExtractionPrincipalAxis:
						rt = factorGPATol
					case rot != vectors.RotationVarimax && rot != vectors.RotationPromax && rot != vectors.RotationOblimin:
						// The further GPA criteria: the stopping band on
						// every extraction (geomin amplifies rounding;
						// mv_rotation.json pins them step for step).
						rt = factorGPATol
					}
					variance := want.Variance
					if rot == vectors.RotationNone {
						factorNearRows(t, tol, "primary", res.Primary, want.Unrotated)
						if res.Auxiliary["unrotated"] != nil || res.Auxiliary["factor_correlation"] != nil {
							t.Error("unrotated run carries rotation outputs")
						}
					} else {
						w := want.Rotations[rot]
						factorNearRows(t, rt, "primary", res.Primary, w.Loadings)
						factorNearRows(t, tol, "unrotated", res.Auxiliary["unrotated"], want.Unrotated)
						fc := res.Auxiliary["factor_correlation"]
						if !vectors.RotationOblique(rot) {
							if fc != nil {
								t.Errorf("orthogonal %s carries factor_correlation", rot)
							}
						} else {
							if fc == nil || fc.Kind != types.MatrixKindSquareSymmetric || !reflect.DeepEqual(fc.RowKeys, keys) || !reflect.DeepEqual(fc.ColumnKeys, keys) {
								t.Fatalf("factor_correlation %+v", fc)
							}
							factorNearRows(t, rt, "factor_correlation", fc, w.Phi)
						}
						variance = w.Variance
					}
					for key, wv := range map[string][]float64{"communalities": want.Communalities, "uniquenesses": want.Uniquenesses, "variance_accounted": variance} {
						got := relFloats(t, res.Vectors[key])
						if len(got) != len(wv) {
							t.Fatalf("%s length %d, want %d", key, len(got), len(wv))
						}
						vt := tol
						if key == "variance_accounted" {
							vt = rt
						}
						for i := range wv {
							factorNear(t, vt, fmt.Sprintf("%s[%d]", key, i), got[i], wv[i])
						}
					}
					s := res.Scalars
					factorNear(t, tol, "rmsr", s["rmsr"], want.RMSR)
					factorNear(t, tol, "empirical_chisq", s["empirical_chisq"], want.EmpiricalChisq)
					if want.EmpiricalP == nil {
						if !math.IsNaN(s["empirical_p"]) {
							t.Errorf("empirical_p %v, want null (df %v)", s["empirical_p"], want.DF)
						}
					} else {
						factorNear(t, tol, "empirical_p", s["empirical_p"], *want.EmpiricalP)
					}
					factorNear(t, factorPATol, "kmo", s["kmo"], c.KMO)
					factorNear(t, factorPATol, "bartlett_chisq", s["bartlett_chisq"], c.BartlettChisq)
					factorNear(t, factorPATol, "bartlett_p", s["bartlett_p"], c.BartlettP)
					if s["df"] != want.DF || s["bartlett_df"] != c.BartlettDF || s["factors_retained"] != float64(c.K) {
						t.Errorf("df %v (R %v), bartlett_df %v (R %v), factors_retained %v", s["df"], want.DF, s["bartlett_df"], c.BartlettDF, s["factors_retained"])
					}
					if ext == vectors.FactorExtractionPrincipalAxis {
						conv := 0.0
						if *want.Converged {
							conv = 1
						}
						if s["iterations"] != float64(*want.Iterations) || s["converged"] != conv {
							t.Errorf("iterations %v converged %v, psych %d %v", s["iterations"], s["converged"], *want.Iterations, *want.Converged)
						}
						if (findWarning(res, errors.PULSE_MATRIX_NOT_CONVERGED) == nil) == !*want.Converged {
							t.Errorf("NOT_CONVERGED warning presence %v, psych converged %v", matWarningCodes(res), *want.Converged)
						}
					} else if s["converged"] != 1 {
						t.Errorf("%s converged %v", ext, s["converged"])
					}
					for _, key := range []string{"likelihood_chisq", "likelihood_p", "tli", "rmsea", "rmsea_lower", "rmsea_upper", "bic"} {
						if _, ok := s[key]; ok != (ext == vectors.FactorExtractionML) {
							t.Errorf("%s: scalar %s present %v", ext, key, ok)
						}
					}
					if ext == vectors.FactorExtractionML {
						factorNear(t, tol, "likelihood_chisq", s["likelihood_chisq"], want.LikelihoodChisq)
						for key, w := range map[string]*float64{"likelihood_p": want.LikelihoodP, "tli": want.TLI, "rmsea": want.RMSEA, "rmsea_lower": want.RMSEALower, "rmsea_upper": want.RMSEAUpper, "bic": want.BIC} {
							if w == nil {
								if !math.IsNaN(s[key]) {
									t.Errorf("%s %v, want null (df %v)", key, s[key], want.DF)
								}
								continue
							}
							factorNear(t, tol, key, s[key], *w)
						}
					}
					heywood := false
					for i, u := range want.Uniquenesses {
						heywood = heywood || u <= 0 || (want.Psi != nil && want.Psi[i] <= 0.005)
					}
					if (findWarning(res, errors.PULSE_MATRIX_HEYWOOD) != nil) != heywood {
						t.Errorf("HEYWOOD warning presence %v, oracle Heywood %v", matWarningCodes(res), heywood)
					}
				})
			}
		}
	}
}

// TestMatrixFactor_Guards: the coded paths.
//
//   - Not identified: 4 factors on attitude's 7 members ((7 − 4)² < 11)
//     nulls the loadings and fit figures with PULSE_MATRIX_NOT_IDENTIFIED
//     (factors 4, max_factors 3); KMO and Bartlett still compute.
//   - Heywood: the heywood fixture's one-factor minres fit puts h1's
//     uniqueness below 0 — reported as fitted, with PULSE_MATRIX_HEYWOOD
//     naming h1.
//   - Not converged: attitude under probability weights at k = 2 runs
//     principal axis to psych's 50-iteration cap: PULSE_MATRIX_NOT_CONVERGED
//     with solver fa_pa, converged 0.
//   - ML Heywood: the heywood fixture's one-factor ML fit pins a
//     uniqueness at the 0.005 floor — PULSE_MATRIX_HEYWOOD with the
//     floor message; df 0 nulls likelihood_p, tli, rmsea (and its
//     interval) and bic while the likelihood chi-square stays.
//   - Kaiser: the default count equals the eigenvalues above 1.
func TestMatrixFactor_Guards(t *testing.T) {
	t.Parallel()

	cases, dir := loadFactorOracle(t)
	cfg := fixtureFS(t, dir)
	att := cases[0].Fields
	run := func(fixture string, fields []string, params map[string]any, weight string) types.MatrixResult {
		return processMatrices(t, cfg, &types.Request{
			Cohort:   &types.Cohort{Filename: filepath.Join(dir, fixture+".pulse")},
			Matrices: []types.MatrixSpec{factorSpec("fa", fields, params, weight)},
		}).Matrices[0]
	}

	ni := run("attitude", att, map[string]any{"factors": 4}, "none")
	w := findWarning(ni, errors.PULSE_MATRIX_NOT_IDENTIFIED)
	if w == nil || w.Details["factors"] != 4 || w.Details["max_factors"] != 3 {
		t.Fatalf("not identified: warnings %v (%+v)", matWarningCodes(ni), w)
	}
	if len(ni.Primary.ColumnKeys) != 4 || !math.IsNaN(ni.Primary.Values[0][0]) || !math.IsNaN(ni.Scalars["rmsr"]) || !math.IsNaN(relFloats(t, ni.Vectors["communalities"])[0]) {
		t.Errorf("not identified: fit not null: %+v %v", ni.Primary, ni.Scalars)
	}
	if math.IsNaN(ni.Scalars["kmo"]) || math.IsNaN(ni.Scalars["bartlett_chisq"]) || ni.Scalars["df"] != -1 {
		t.Errorf("not identified: kmo %v bartlett %v df %v", ni.Scalars["kmo"], ni.Scalars["bartlett_chisq"], ni.Scalars["df"])
	}

	hw := run("heywood", []string{"h1", "h2", "h3"}, map[string]any{"factors": 1}, "none")
	w = findWarning(hw, errors.PULSE_MATRIX_HEYWOOD)
	if w == nil || !reflect.DeepEqual(w.Details["members"], []string{"h1"}) {
		t.Fatalf("heywood: warnings %v (%+v)", matWarningCodes(hw), w)
	}
	if u := relFloats(t, hw.Vectors["uniquenesses"]); !(u[0] <= 0) || math.IsNaN(hw.Primary.Values[0][0]) {
		t.Errorf("heywood: uniquenesses %v, loadings %v (want reported as fitted)", u, hw.Primary.Values)
	}

	nc := run("attitude", att, map[string]any{"factors": 2, "extraction": "principal_axis"}, "probability")
	w = findWarning(nc, errors.PULSE_MATRIX_NOT_CONVERGED)
	if w == nil || w.Details["solver"] != "fa_pa" || w.Details["max_iterations"] != 50 || nc.Scalars["converged"] != 0 || nc.Scalars["iterations"] != 50 {
		t.Fatalf("not converged: warnings %v (%+v), scalars %v", matWarningCodes(nc), w, nc.Scalars)
	}

	mlhw := run("heywood", []string{"h1", "h2", "h3"}, map[string]any{"factors": 1, "extraction": "ml"}, "none")
	w = findWarning(mlhw, errors.PULSE_MATRIX_HEYWOOD)
	if w == nil || len(w.Details["members"].([]string)) == 0 || !strings.Contains(w.Message, "0.005 floor") {
		t.Fatalf("ml heywood: warnings %v (%+v)", matWarningCodes(mlhw), w)
	}
	if s := mlhw.Scalars; math.IsNaN(s["likelihood_chisq"]) || !math.IsNaN(s["likelihood_p"]) || !math.IsNaN(s["tli"]) || !math.IsNaN(s["rmsea"]) || !math.IsNaN(s["rmsea_lower"]) || !math.IsNaN(s["rmsea_upper"]) || !math.IsNaN(s["bic"]) || s["df"] != 0 {
		t.Errorf("ml heywood (df 0): scalars %v", s)
	}

	pca := processMatrices(t, cfg, &types.Request{
		Cohort:   &types.Cohort{Filename: filepath.Join(dir, "attitude.pulse")},
		Matrices: []types.MatrixSpec{pcaSpec("pca", att, nil, "none"), factorSpec("fa", att, nil, "none")},
	}).Matrices
	if k := pca[0].Scalars["components_retained"]; pca[1].Scalars["factors_retained"] != k || len(pca[1].Primary.ColumnKeys) != int(k) {
		t.Errorf("kaiser: factors_retained %v (%v), PCA kaiser %v", pca[1].Scalars["factors_retained"], pca[1].Primary.ColumnKeys, k)
	}
}

// TestMatrixFactor_UndefinedAndNotPSD: a constant member nulls every
// figure (k null columns for an integer k); a non-PSD pairwise input is
// refused with the fatal PULSE_MATRIX_NOT_PSD unless params.repair is
// "nearest", which factors the repaired matrix with the repair warning.
func TestMatrixFactor_UndefinedAndNotPSD(t *testing.T) {
	t.Parallel()

	rows := make([][5]float64, 20)
	for i := range rows {
		rows[i] = [5]float64{float64(i), float64(i * i % 7), 3, 1, 1}
	}
	mcfg := writeMatrixCohort(t, "const.pulse", rows)
	res := processMatrices(t, mcfg, &types.Request{
		Cohort:   &types.Cohort{Filename: "const.pulse"},
		Matrices: []types.MatrixSpec{factorSpec("fa", []string{"x1", "x2", "x3"}, map[string]any{"factors": 1, "rotation": "promax"}, "none")},
	}).Matrices[0]
	if len(res.Primary.ColumnKeys) != 1 || !math.IsNaN(res.Primary.Values[0][0]) || res.Auxiliary["factor_correlation"] == nil ||
		!math.IsNaN(res.Scalars["kmo"]) || !math.IsNaN(res.Scalars["rmsr"]) || res.Scalars["factors_retained"] != 1 {
		t.Errorf("undefined input: %+v %v", res.Primary, res.Scalars)
	}

	_, dir := loadFactorOracle(t)
	cfg := fixtureFS(t, dir)
	req := func(params string) *types.Request {
		return &types.Request{
			Cohort:   &types.Cohort{Filename: filepath.Join(dir, "nonpsd_pairwise.pulse")},
			Matrices: []types.MatrixSpec{{Name: "fa", Type: types.MAT_FACTOR, Fields: []string{"x", "y", "z"}, Params: json.RawMessage(params)}},
		}
	}
	_, err := processOrSlotErr(t.Context(), New(cfg), req(`{"missing": "pairwise", "factors": 1}`))
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_MATRIX_NOT_PSD {
		t.Fatalf("unrepaired: error %v, want PULSE_MATRIX_NOT_PSD", err)
	}
	rep := processMatrices(t, cfg, req(`{"missing": "pairwise", "factors": 1, "repair": "nearest"}`)).Matrices[0]
	if findWarning(rep, errors.PULSE_MATRIX_NOT_PSD) == nil || findWarning(rep, errors.PULSE_MATRIX_PAIRWISE_N_STAR) == nil || math.IsNaN(rep.Primary.Values[0][0]) {
		t.Errorf("repaired: warnings %v, primary %v", matWarningCodes(rep), rep.Primary.Values)
	}
}

// TestMatrixFactor_Refusals: the params rules — every refusal
// SERVICE_VALIDATION bad_params, so a misspelt or misplaced knob never
// silently takes its default.
func TestMatrixFactor_Refusals(t *testing.T) {
	t.Parallel()

	cases, dir := loadFactorOracle(t)
	cfg := fixtureFS(t, dir)
	for _, params := range []string{
		`{"factors": 0}`,
		`{"factors": 8}`,
		`{"factors": "all"}`,
		`{"extraction": "mle"}`,
		`{"rotation": "bentler"}`,
		`{"kappa": 3}`,
		`{"rotation": "varimax", "epsilon": 0.01}`,
		`{"rotation": "geomin", "epsilon": 0}`,
		`{"rotation": "oblimin", "kappa": 3}`,
		`{"rotation": "promax", "gamma": 0.5}`,
		`{"components": 2}`,
		`{"repair": "clip"}`,
	} {
		_, err := New(cfg).Process(t.Context(), &types.Request{
			Cohort:   &types.Cohort{Filename: filepath.Join(dir, "attitude.pulse")},
			Matrices: []types.MatrixSpec{{Name: "fa", Type: types.MAT_FACTOR, Fields: cases[0].Fields, Params: json.RawMessage(params)}},
		})
		want := errors.SERVICE_VALIDATION
		if strings.HasPrefix(params, `{"components"`) { // not a key of this operator: the params allowlist
			want = errors.PULSE_REQUEST_UNKNOWN_FIELD
		}
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != want || (want == errors.SERVICE_VALIDATION && ce.Details["reason"] != "bad_params") {
			t.Errorf("%s: error %v, want %s", params, err, want)
		}
	}
}

// TestMatrixFactor_MLGuards: the maximum-likelihood-only paths.
//
//   - Singular: a member that is the sum of two others leaves the
//     correlation singular, so ML (which needs it positive definite)
//     withholds the fit — null loadings, vectors and ML scalars — and
//     PULSE_MATRIX_SINGULAR names them; minres still fits it.
//   - Insufficient: probability weights with Kish n_eff 3 clear
//     Bartlett's 1 + (2p + 5)/6 but not the likelihood's extra 2k/3, so
//     the likelihood figures are null with PULSE_MATRIX_INSUFFICIENT_N
//     scope "likelihood" while Bartlett's test and the loadings compute.
func TestMatrixFactor_MLGuards(t *testing.T) {
	t.Parallel()

	rows := make([][5]float64, 12)
	for i := range rows {
		a, b := float64(i), float64(i*i%7)
		rows[i] = [5]float64{a, b, a + b, 1, 1}
	}
	cfg := writeMatrixCohort(t, "dep.pulse", rows)
	fit := func(ext string) types.MatrixResult {
		return processMatrices(t, cfg, &types.Request{
			Cohort:   &types.Cohort{Filename: "dep.pulse"},
			Matrices: []types.MatrixSpec{factorSpec("fa", []string{"x1", "x2", "x3"}, map[string]any{"factors": 1, "extraction": ext}, "none")},
		}).Matrices[0]
	}
	sing := fit("ml")
	w := findWarning(sing, errors.PULSE_MATRIX_SINGULAR)
	if w == nil || !reflect.DeepEqual(w.Details["outputs"], []string{"kmo", "bartlett_chisq", "bartlett_p", "loadings", "communalities", "uniquenesses", "variance_accounted", "likelihood_chisq", "likelihood_p", "tli", "rmsea", "rmsea_lower", "rmsea_upper", "bic"}) {
		t.Fatalf("singular ml: warnings %v (%+v)", matWarningCodes(sing), w)
	}
	if !math.IsNaN(sing.Primary.Values[0][0]) || !math.IsNaN(sing.Scalars["likelihood_chisq"]) || !math.IsNaN(sing.Scalars["tli"]) || !math.IsNaN(relFloats(t, sing.Vectors["communalities"])[0]) {
		t.Errorf("singular ml: fit not null: %v %v", sing.Primary.Values, sing.Scalars)
	}
	if mr := fit("minres"); math.IsNaN(mr.Primary.Values[0][0]) {
		t.Errorf("singular minres: loadings %v, want fitted", mr.Primary.Values)
	}

	small := [][5]float64{{1, 2, 1, 1, 1}, {2, 1, 3, 1, 1}, {4, 5, 2, 1, 1}, {3, 3, 5, 1, 3}}
	scfg := writeMatrixCohort(t, "small.pulse", small)
	spec := factorSpec("fa", []string{"x1", "x2", "x3"}, map[string]any{"factors": 1, "extraction": "ml"}, "none")
	spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "p", Kind: types.WeightKindProbability})
	ins := processMatrices(t, scfg, &types.Request{Cohort: &types.Cohort{Filename: "small.pulse"}, Matrices: []types.MatrixSpec{spec}}).Matrices[0]
	w = findWarning(ins, errors.PULSE_MATRIX_INSUFFICIENT_N)
	if w == nil || w.Details["scope"] != "likelihood" || w.Details["min_n_star"] != 3.5 {
		t.Fatalf("insufficient: warnings %v (%+v)", matWarningCodes(ins), w)
	}
	for _, key := range []string{"likelihood_chisq", "likelihood_p", "tli", "rmsea", "rmsea_lower", "rmsea_upper", "bic"} {
		if !math.IsNaN(ins.Scalars[key]) {
			t.Errorf("insufficient: %s %v, want null", key, ins.Scalars[key])
		}
	}
	if math.IsNaN(ins.Scalars["bartlett_chisq"]) || math.IsNaN(ins.Primary.Values[0][0]) {
		t.Errorf("insufficient: bartlett %v loadings %v, want computed", ins.Scalars["bartlett_chisq"], ins.Primary.Values)
	}
}

// TestMatrixFactor_ConfLevelWidensRMSEAInterval: params.conf_level
// reaches the ml RMSEA interval — on every oracle case whose 90%
// interval has an interior lower bound, 0.95 contains it on both sides
// and 0.5 sits inside it, while rmsea and the other fit figures do not
// move.
func TestMatrixFactor_ConfLevelWidensRMSEAInterval(t *testing.T) {
	t.Parallel()

	cases, dir := loadFactorOracle(t)
	cfg := fixtureFS(t, dir)
	seen := 0
	for _, c := range cases {
		if c.MLExact.RMSEALower == nil || *c.MLExact.RMSEALower <= 0 || c.MLExact.RMSEAUpper == nil {
			continue
		}
		seen++
		run := func(conf any) map[string]float64 {
			params := map[string]any{"factors": c.K, "extraction": "ml"}
			if conf != nil {
				params["conf_level"] = conf
			}
			return processMatrices(t, cfg, &types.Request{
				Cohort:   &types.Cohort{Filename: filepath.Join(dir, c.Fixture+".pulse")},
				Matrices: []types.MatrixSpec{factorSpec("fa", c.Fields, params, c.Weight)},
			}).Matrices[0].Scalars
		}
		def, wide, narrow := run(nil), run(0.95), run(0.5)
		factorNear(t, factorMLTol, "default rmsea_lower", def["rmsea_lower"], *c.MLExact.RMSEALower)
		factorNear(t, factorMLTol, "default rmsea_upper", def["rmsea_upper"], *c.MLExact.RMSEAUpper)
		if !(wide["rmsea_lower"] < def["rmsea_lower"] && wide["rmsea_upper"] > def["rmsea_upper"]) {
			t.Errorf("%s k%d: 95%% [%v, %v] does not contain 90%% [%v, %v]", c.Fixture, c.K, wide["rmsea_lower"], wide["rmsea_upper"], def["rmsea_lower"], def["rmsea_upper"])
		}
		if !(narrow["rmsea_lower"] > def["rmsea_lower"] && narrow["rmsea_upper"] < def["rmsea_upper"]) {
			t.Errorf("%s k%d: 50%% [%v, %v] not inside 90%% [%v, %v]", c.Fixture, c.K, narrow["rmsea_lower"], narrow["rmsea_upper"], def["rmsea_lower"], def["rmsea_upper"])
		}
		for _, key := range []string{"rmsea", "tli", "bic", "likelihood_chisq"} {
			if wide[key] != def[key] || narrow[key] != def[key] {
				t.Errorf("%s moved with conf_level: %v / %v / %v", key, def[key], wide[key], narrow[key])
			}
		}
	}
	if seen == 0 {
		t.Fatal("no oracle case has an interior RMSEA lower bound")
	}
}
