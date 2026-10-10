package processing

import (
	stderrors "errors"
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// nearPDCase is one mv_near_pd.json case (Matrix::nearPD(R, corr = TRUE,
// do2eigen = TRUE | FALSE) on the nonpsd_pairwise fixture's pairwise
// correlation, scripts/reference/gen_multivariate.R).
type nearPDCase struct {
	Fixture            string      `json:"fixture"`
	Do2Eigen           bool        `json:"do2eigen"`
	ConvTol            float64     `json:"conv_tol"`
	EigTol             float64     `json:"eig_tol"`
	PosdTol            float64     `json:"posd_tol"`
	MaxIt              int         `json:"maxit"`
	InputCorrelation   [][]float64 `json:"input_correlation"`
	Nearest            [][]float64 `json:"nearest"`
	NearestEigenvalues []float64   `json:"nearest_eigenvalues"`
	Frobenius          float64     `json:"frobenius_adjustment"`
	Iterations         int         `json:"iterations"`
	Converged          bool        `json:"converged"`
	InputCovariance    [][]float64 `json:"input_covariance"`
	RepairedCovariance [][]float64 `json:"repaired_covariance"`
}

// nearestOracleTol is the documented repair tolerance against
// Matrix::nearPD: 1e-9 absolute per cell (and on the Frobenius
// adjustment). The repair runs on gonum's eigensolver where nearPD runs
// LAPACK dsyevr; the two agree to a few ulps per projection, and 15
// projections keep the iterates within ~1e-12 of R's — 1e-9 leaves
// three orders of headroom for the cross-architecture eigen drift
// (.claude/reference/matrix-and-vectors.md, PSD guard and repair).
const nearestOracleTol = 1e-9

func symFrom(t *testing.T, rows [][]float64) *linalg.Sym {
	t.Helper()
	s, err := linalg.NewSymFromRows(rows)
	if err != nil {
		t.Fatalf("NewSymFromRows: %v", err)
	}
	return s
}

func assertSymNear(t *testing.T, label string, got *linalg.Sym, want [][]float64, tol float64) {
	t.Helper()
	for i := range want {
		for j := range want[i] {
			if d := math.Abs(got.At(i, j) - want[i][j]); !(d <= tol) {
				t.Errorf("%s [%d][%d] = %.17g, R = %.17g (|Δ| %.3g > %g)", label, i, j, got.At(i, j), want[i][j], d, tol)
			}
		}
	}
}

func nearPDCases(t *testing.T) (bare, full nearPDCase) {
	t.Helper()
	doc := loadReference[nearPDCase](t, "mv_near_pd")
	var haveBare, haveFull bool
	for _, c := range doc.Cases {
		if c.Do2Eigen {
			full, haveFull = c, true
		} else {
			bare, haveBare = c, true
		}
	}
	if !haveBare || !haveFull {
		t.Fatal("mv_near_pd lacks a do2eigen TRUE or FALSE case")
	}
	// The settings Pulse hard-codes are nearPD's defaults the oracle ran.
	for _, c := range []nearPDCase{bare, full} {
		if c.ConvTol != nearestConvTol || c.EigTol != nearestEigTol || c.PosdTol != nearestPosdTol || c.MaxIt != nearestMaxIter {
			t.Fatalf("oracle settings conv %g eig %g posd %g maxit %d differ from Pulse's", c.ConvTol, c.EigTol, c.PosdTol, c.MaxIt)
		}
	}
	return bare, full
}

// TestNearestCorrelation_MatchesNearPD pins the Higham repair to
// Matrix::nearPD(R, corr = TRUE): the bare alternating-projection fixed
// point (highamNearest) to do2eigen = FALSE — the iterate, its
// iteration count, convergence and eigenvalues (nearest_eigenvalues,
// not nearPD's own pre-projection r_eigenvalues) — and the full repair
// (nearestCorrelation, with the final eigenvalue floor) to
// do2eigen = TRUE, including the Frobenius adjustment.
func TestNearestCorrelation_MatchesNearPD(t *testing.T) {
	bare, full := nearPDCases(t)
	in := symFrom(t, bare.InputCorrelation)

	x, iters, conv, err := highamNearest(in)
	if err != nil {
		t.Fatalf("highamNearest: %v", err)
	}
	if iters != bare.Iterations || conv != bare.Converged {
		t.Errorf("higham: %d iterations converged=%v, nearPD %d converged=%v", iters, conv, bare.Iterations, bare.Converged)
	}
	assertSymNear(t, "higham (do2eigen FALSE)", x, bare.Nearest, nearestOracleTol)
	eig, err := linalg.SymEigen(x)
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range bare.NearestEigenvalues {
		if d := math.Abs(eig.Values.At(k) - want); !(d <= nearestOracleTol) {
			t.Errorf("higham eigenvalue %d = %.17g, R = %.17g", k, eig.Values.At(k), want)
		}
	}

	rep, err := nearestCorrelation(in)
	if err != nil {
		t.Fatalf("nearestCorrelation: %v", err)
	}
	if rep.Iterations != full.Iterations || rep.Converged != full.Converged {
		t.Errorf("repair: %d iterations converged=%v, nearPD %d converged=%v", rep.Iterations, rep.Converged, full.Iterations, full.Converged)
	}
	assertSymNear(t, "repair (do2eigen TRUE)", rep.X, full.Nearest, nearestOracleTol)
	if d := math.Abs(rep.Adjustment - full.Frobenius); !(d <= nearestOracleTol) {
		t.Errorf("Frobenius adjustment %.17g, nearPD normF %.17g", rep.Adjustment, full.Frobenius)
	}
	// Strictly positive definite: the reference Cholesky factors it, so
	// a decomposition can run on it.
	if _, err := linalg.Cholesky(rep.X); err != nil {
		t.Errorf("repaired matrix does not factor: %v", err)
	}
	for i := 0; i < rep.X.N(); i++ {
		if rep.X.At(i, i) != 1 {
			t.Errorf("repaired diagonal [%d] = %v, want exactly 1", i, rep.X.At(i, i))
		}
	}
	// The input was not PSD to begin with (the guard's own judgement).
	if _, _, bad := notPSD(in); !bad {
		t.Error("oracle input judged PSD")
	}
}

// partialGuardInput is a finalize input over a pairwise
// MAT_PARTIAL_CORRELATION plan with members x, y, z (PSDRisk true).
func partialGuardInput(repair string) *matrixFinalizeInput {
	cols := []string{"x", "y", "z"}
	plan := vectors.Matrix{Name: "m", Type: types.MAT_PARTIAL_CORRELATION, Pairwise: true,
		Members: vectors.Resolved{Name: "m", Members: cols, Labels: cols}, Repair: repair, Streamable: true, Mergeable: true}
	return &matrixFinalizeInput{slot: &matrixSlot{plan: plan, cols: cols}}
}

// TestGuardPSD_RefusesOrRepairs: the shared decomposition guard refuses
// a non-PSD input with the FATAL coded PULSE_MATRIX_NOT_PSD (its own
// code, not PROCESSING_INTERNAL) when no repair is requested, and under
// repair "nearest" returns the repair with the warning carrying the
// Frobenius adjustment. A COVARIANCE input is repaired via its
// (pairwise) correlation and scaled back: its variances stay exactly,
// and it matches nearPD's repaired_covariance (D·nearest·D).
func TestGuardPSD_RefusesOrRepairs(t *testing.T) {
	_, full := nearPDCases(t)
	cov := symFrom(t, full.InputCovariance)

	_, _, err := partialGuardInput("").guardPSD(cov, nil)
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_MATRIX_NOT_PSD {
		t.Fatalf("unrepaired guard error = %v, want PULSE_MATRIX_NOT_PSD", err)
	}
	if ce.Details["matrix"] != "m" || ce.Details["member"] != "z" || ce.Details["pivot"] != 2 {
		t.Errorf("refusal details = %v", ce.Details)
	}
	if opts, _ := ce.Details["repair_options"].([]string); len(opts) != 1 || opts[0] != vectors.RepairNearest {
		t.Errorf("refusal repair_options = %v", ce.Details["repair_options"])
	}

	corr := symFrom(t, full.InputCorrelation)
	for _, input := range []struct {
		name    string
		s, corr *linalg.Sym
		want    [][]float64
	}{
		{"covariance", cov, corr, full.RepairedCovariance},
		{"correlation", corr, nil, full.Nearest},
	} {
		t.Run(input.name, func(t *testing.T) {
			got, ws, err := partialGuardInput(vectors.RepairNearest).guardPSD(input.s, input.corr)
			if err != nil {
				t.Fatalf("repair: %v", err)
			}
			if len(ws) != 1 {
				t.Fatalf("converged repair warnings = %v, want the NOT_PSD warning alone", ws)
			}
			w := ws[0]
			assertSymNear(t, "repaired", got, input.want, nearestOracleTol*4)
			for i := 0; i < got.N(); i++ {
				if got.At(i, i) != input.s.At(i, i) {
					t.Errorf("diagonal [%d] = %v, input %v: the repair must keep it", i, got.At(i, i), input.s.At(i, i))
				}
			}
			if w == nil || w.Code != string(errors.PULSE_MATRIX_NOT_PSD) {
				t.Fatalf("repair warning = %v", w)
			}
			adj, _ := w.Details["frobenius_adjustment"].(float64)
			if d := math.Abs(adj - full.Frobenius); !(d <= nearestOracleTol) {
				t.Errorf("frobenius_adjustment %v, nearPD %v", adj, full.Frobenius)
			}
			if w.Details["repair"] != vectors.RepairNearest || w.Details["converged"] != true || w.Details["iterations"] != full.Iterations {
				t.Errorf("warning details = %v", w.Details)
			}
		})
	}

	// A PSD input, an unjudgeable one and a no-risk plan pass through
	// unchanged.
	pd := symFrom(t, [][]float64{{1, 0.5, 0.2}, {0.5, 1, 0.3}, {0.2, 0.3, 1}})
	if got, w, err := partialGuardInput("").guardPSD(pd, nil); err != nil || w != nil || got != pd {
		t.Errorf("PSD input: %v %v %v", got, w, err)
	}
	nan := symFrom(t, [][]float64{{1, math.NaN(), 0}, {math.NaN(), 1, 0}, {0, 0, 1}})
	if got, w, err := partialGuardInput("").guardPSD(nan, nil); err != nil || w != nil || got != nan {
		t.Errorf("undefined input: %v %v %v", got, w, err)
	}
	listwise := partialGuardInput("")
	listwise.slot.plan.Pairwise = false
	if got, w, err := listwise.guardPSD(cov, corr); err != nil || w != nil || got != cov {
		t.Errorf("listwise (no PSD risk) input: %v %v %v", got, w, err)
	}
}

// TestRepairWarnings_NotConvergedBesideNotPSD: a repair whose
// projection hit nearestMaxIter emits PULSE_MATRIX_NOT_CONVERGED beside
// the PULSE_MATRIX_NOT_PSD warning (which keeps converged false), with
// the cap and tolerance; a converged one emits NOT_PSD alone.
func TestRepairWarnings_NotConvergedBesideNotPSD(t *testing.T) {
	rep := nearestResult{Iterations: nearestMaxIter, Converged: false, Adjustment: 0.25}
	ws := repairWarnings("m", map[string]any{"matrix": "m"}, rep)
	if len(ws) != 2 || ws[0].Code != string(errors.PULSE_MATRIX_NOT_PSD) || ws[1].Code != string(errors.PULSE_MATRIX_NOT_CONVERGED) {
		t.Fatalf("non-converged repair warnings = %v", ws)
	}
	if ws[0].Details["converged"] != false || ws[0].Details["repair"] != vectors.RepairNearest {
		t.Errorf("NOT_PSD details = %v", ws[0].Details)
	}
	d := ws[1].Details
	if d["matrix"] != "m" || d["solver"] != "nearest_correlation" || d["iterations"] != nearestMaxIter ||
		d["max_iterations"] != nearestMaxIter || d["tolerance"] != nearestConvTol {
		t.Errorf("NOT_CONVERGED details = %v", d)
	}
	rep.Converged = true
	if ws := repairWarnings("m", map[string]any{}, rep); len(ws) != 1 || ws[0].Code != string(errors.PULSE_MATRIX_NOT_PSD) {
		t.Errorf("converged repair warnings = %v", ws)
	}
}

// TestPartialCorrelation_ControlsAreSchurComplement: the control-list
// form equals the textbook residual definition — the correlation of
// the conditional covariance R_OO − R_OC·R_CC⁻¹·R_CO — and the "all"
// form equals −P_ij / √(P_ii·P_jj).
func TestPartialCorrelation_ControlsAreSchurComplement(t *testing.T) {
	r := symFrom(t, [][]float64{
		{1, 0.6, 0.3, 0.4},
		{0.6, 1, 0.2, 0.5},
		{0.3, 0.2, 1, 0.1},
		{0.4, 0.5, 0.1, 1},
	})
	// Control for column 3 only: output {0, 1, 2}.
	got, err := partialCorrelation(r, []int{0, 1, 2}, false)
	if err != nil {
		t.Fatal(err)
	}
	cond := func(i, j int) float64 { return r.At(i, j) - r.At(i, 3)*r.At(j, 3)/r.At(3, 3) }
	for i := 0; i < 3; i++ {
		for j := i + 1; j < 3; j++ {
			want := cond(i, j) / math.Sqrt(cond(i, i)*cond(j, j))
			if d := math.Abs(got.At(i, j) - want); d > 1e-14 {
				t.Errorf("control list [%d][%d] = %v, Schur %v", i, j, got.At(i, j), want)
			}
		}
	}
	all, err := partialCorrelation(r, []int{0, 1, 2, 3}, true)
	if err != nil {
		t.Fatal(err)
	}
	prec, _ := linalg.InverseSPD(r)
	for i := 0; i < 4; i++ {
		if all.At(i, i) != 1 {
			t.Errorf("diagonal [%d] = %v", i, all.At(i, i))
		}
		for j := i + 1; j < 4; j++ {
			want := -prec.At(i, j) / math.Sqrt(prec.At(i, i)*prec.At(j, j))
			if d := math.Abs(all.At(i, j) - want); d > 1e-14 {
				t.Errorf("all [%d][%d] = %v, precision %v", i, j, all.At(i, j), want)
			}
		}
	}
	// Singular: column 2 = column 0 → the InverseSPD refusal.
	sing := symFrom(t, [][]float64{{1, 0.5, 1}, {0.5, 1, 0.5}, {1, 0.5, 1}})
	_, err = partialCorrelation(sing, []int{0, 1, 2}, true)
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_MATRIX_SINGULAR {
		t.Errorf("singular input error = %v, want PULSE_MATRIX_SINGULAR", err)
	}
}
