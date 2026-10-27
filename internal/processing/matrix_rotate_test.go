package processing

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
)

// matrix_rotate_test.go pins the shared rotation engine against R
// (mv_rotation.json: stats::varimax, stats::promax, GPArotation::oblimin
// on PCA loadings of the reference fixtures, put through the
// sign-and-order rule) and its algebraic invariants.

// rotationOracleTol is the engine's documented agreement with R,
// absolute + relative per cell (matrix-and-vectors/matrix-decomposition.md, Rotation engine).
// Every criterion replays its R algorithm with R's constants, so it
// stops on the same iterate; what is left is rounding — the gonum SVD
// against LAPACK's in varimax, Gauss–Jordan against dgesv in oblimin,
// the normal equations against lm.fit's QR in promax — well inside 1e-9.
const rotationOracleTol = 1e-9

// rotationGPABandTol bounds a GPA run whose R trajectory amplifies
// rounding (the oracle's sensitivity above rotationSensitiveAbove): the
// two sides walk the same Barzilai–Borwein path — the criterion values
// agree to ~1e-15 for the first iterates — until a line-search decision
// near a tie goes the other way, after which each stops on its own
// iterate inside the 1e-5 gradient band. It is the factor tests' GPA
// band (factorGPATol); worst seen 1.1e-5 (geomin, attitude probability
// k = 3).
const rotationGPABandTol = 1e-4

// rotationSensitiveAbove: an oracle run whose own loadings move by more
// than this under a relative 1e-12 input change (the fixture's
// sensitivity) is compared within rotationGPABandTol, its iteration
// count only logged.
const rotationSensitiveAbove = 1e-9

type rotationOut struct {
	Loadings   [][]float64 `json:"loadings"`
	Rotation   [][]float64 `json:"rotation"`
	Phi        [][]float64 `json:"phi"`
	Variance   []float64   `json:"variance"`
	Order      []int       `json:"order"`
	Signs      []float64   `json:"signs"`
	Kappa      *float64    `json:"kappa"`
	Gamma      *float64    `json:"gamma"`
	Epsilon    *float64    `json:"epsilon"`
	Iterations *int        `json:"iterations"`
	// Sensitivity is R's own response to a relative 1e-12 input change
	// (gen_rotation.R; the further GPA criteria only).
	Sensitivity *float64 `json:"sensitivity"`
}

// sensitive reports an oracle run whose trajectory amplifies rounding.
func (o rotationOut) sensitive() bool {
	return o.Sensitivity != nil && *o.Sensitivity > rotationSensitiveAbove
}

// rotationCase is one mv_rotation.json case.
type rotationCase struct {
	Fixture   string        `json:"fixture"`
	Fields    []string      `json:"fields"`
	Weight    string        `json:"weight"`
	K         int           `json:"k"`
	Unrotated [][]float64   `json:"unrotated"`
	Varimax   rotationOut   `json:"varimax"`
	Promax    []rotationOut `json:"promax"`
	Oblimin   []rotationOut `json:"oblimin"`
	// GPForthVarimax is GPArotation::GPForth(A, method = "varimax",
	// normalize = TRUE): the orthogonal GPA driver's oracle.
	GPForthVarimax rotationOut `json:"gpforth_varimax"`
	// The further GPA criteria (E6-S2): GPArotation's quartimax,
	// entropy, geominQ (geomin) and geominT (geomin_orthogonal) at
	// epsilon 0.01 and 0.1.
	Quartimax        rotationOut   `json:"quartimax"`
	Entropy          rotationOut   `json:"entropy"`
	Geomin           []rotationOut `json:"geomin"`
	GeominOrthogonal []rotationOut `json:"geomin_orthogonal"`
}

// gpaOracleRuns lists a case's GPA criteria beyond oblimin, each with
// the spec that replays it and the solver it reports.
func (c rotationCase) gpaOracleRuns() []struct {
	name, solver string
	spec         rotationSpec
	want         rotationOut
} {
	type run = struct {
		name, solver string
		spec         rotationSpec
		want         rotationOut
	}
	runs := []run{
		{"quartimax", "gpa_quartimax", rotationSpec{Method: vectors.RotationQuartimax}, c.Quartimax},
		{"entropy", "gpa_entropy", rotationSpec{Method: vectors.RotationEntropy}, c.Entropy},
	}
	for _, g := range c.Geomin {
		runs = append(runs, run{fmt.Sprintf("geomin_e%g", *g.Epsilon), "gpa_geomin", rotationSpec{Method: vectors.RotationGeomin, Epsilon: *g.Epsilon}, g})
	}
	for _, g := range c.GeominOrthogonal {
		runs = append(runs, run{fmt.Sprintf("geomin_orthogonal_e%g", *g.Epsilon), "gpa_geomin_orthogonal", rotationSpec{Method: vectors.RotationGeominOrthogonal, Epsilon: *g.Epsilon}, g})
	}
	return runs
}

func rotNear(t *testing.T, where string, got, want float64) {
	t.Helper()
	rotNearTol(t, rotationOracleTol, where, got, want)
}

func rotNearTol(t *testing.T, tol float64, where string, got, want float64) {
	t.Helper()
	if !(math.Abs(got-want) <= tol*(1+math.Abs(want))) {
		t.Errorf("%s = %.17g, R = %.17g", where, got, want)
	}
}

func rotNearRows(t *testing.T, where string, got, want [][]float64) {
	t.Helper()
	rotNearRowsTol(t, rotationOracleTol, where, got, want)
}

func rotNearRowsTol(t *testing.T, tol float64, where string, got, want [][]float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d rows, R %d", where, len(got), len(want))
	}
	for i := range want {
		if len(got[i]) != len(want[i]) {
			t.Fatalf("%s[%d]: %d cols, R %d", where, i, len(got[i]), len(want[i]))
		}
		for j := range want[i] {
			rotNearTol(t, tol, fmt.Sprintf("%s[%d][%d]", where, i, j), got[i][j], want[i][j])
		}
	}
}

// TestRotation_MatchesROracle runs every case's unrotated loadings
// through each criterion — varimax, promax at κ = 4 and 2, oblimin at
// γ = 0 (quartimin) and 0.5 (biquartimin) — and compares the
// canonicalised loadings, rotation matrix, Φ and variance accounted to
// R within rotationOracleTol, the column order and signs exactly (the
// fixtures exercise reordered and flipped columns). The further GPA
// criteria (quartimax, entropy, geomin at ε = 0.01 / 0.1,
// geomin_orthogonal likewise) run the same comparison, plus R's
// iteration count exactly and their solver name — except a run the
// oracle marks sensitive (its own result moves by more than 1e-9 under a
// 1e-12 input change), compared within rotationGPABandTol with the order
// and signs still exact. At least half the GPA runs must be insensitive,
// so the last-digit comparison is the rule, not the exception.
func TestRotation_MatchesROracle(t *testing.T) {
	t.Parallel()

	doc := loadReference[rotationCase](t, "mv_rotation")
	if len(doc.Cases) == 0 {
		t.Fatal("oracle has no cases")
	}
	reordered, flipped, gpaRuns, gpaSensitive := 0, 0, 0, 0
	for _, c := range doc.Cases {
		type run struct {
			name string
			spec rotationSpec
			want rotationOut
		}
		runs := []run{{"varimax", rotationSpec{Method: vectors.RotationVarimax}, c.Varimax}}
		for _, pm := range c.Promax {
			runs = append(runs, run{fmt.Sprintf("promax_k%g", *pm.Kappa), rotationSpec{Method: vectors.RotationPromax, Kappa: *pm.Kappa}, pm})
		}
		for _, ob := range c.Oblimin {
			runs = append(runs, run{fmt.Sprintf("oblimin_g%g", *ob.Gamma), rotationSpec{Method: vectors.RotationOblimin, Gamma: *ob.Gamma}, ob})
		}
		gpaSolver := map[string]string{}
		for _, g := range c.gpaOracleRuns() {
			if g.want.Loadings == nil {
				t.Fatalf("%s/%s/k%d: oracle has no %s", c.Fixture, c.Weight, c.K, g.name)
			}
			runs = append(runs, run{g.name, g.spec, g.want})
			gpaSolver[g.name] = g.solver
			gpaRuns++
			if g.want.sensitive() {
				gpaSensitive++
			}
		}
		for _, r := range runs {
			t.Run(fmt.Sprintf("%s/%s/k%d/%s", c.Fixture, c.Weight, c.K, r.name), func(t *testing.T) {
				got, err := rotateLoadings(c.Unrotated, r.spec)
				if err != nil {
					t.Fatal(err)
				}
				if !got.Converged {
					t.Errorf("not converged after %d iterations", got.Iterations)
				}
				if fmt.Sprint(got.Order) != fmt.Sprint(r.want.Order) || fmt.Sprint(got.Signs) != fmt.Sprint(r.want.Signs) {
					t.Fatalf("order %v signs %v, R %v %v", got.Order, got.Signs, r.want.Order, r.want.Signs)
				}
				tol := rotationOracleTol
				if r.want.sensitive() {
					tol = rotationGPABandTol
				}
				rotNearRowsTol(t, tol, "loadings", got.Loadings, r.want.Loadings)
				rotNearRowsTol(t, tol, "rotation", got.Rotation, r.want.Rotation)
				rotNearRowsTol(t, tol, "phi", got.Phi, r.want.Phi)
				for i, w := range r.want.Variance {
					rotNearTol(t, tol, fmt.Sprintf("variance[%d]", i), got.Variance[i], w)
				}
				if solver, ok := gpaSolver[r.name]; ok {
					if r.want.Sensitivity == nil {
						t.Fatal("oracle run has no sensitivity")
					}
					if r.want.sensitive() {
						t.Logf("sensitive run (R moves %.3g under a 1e-12 input change): GPA iterations %d, R %d", *r.want.Sensitivity, got.Iterations, *r.want.Iterations)
					} else if got.Iterations != *r.want.Iterations {
						t.Errorf("GPA iterations %d, R %d", got.Iterations, *r.want.Iterations)
					}
					if got.Solver != solver {
						t.Errorf("solver %q, want %q", got.Solver, solver)
					}
				} else if r.want.Iterations != nil && got.Iterations != *r.want.Iterations {
					t.Logf("GPA iterations %d, R %d (values still within tolerance)", got.Iterations, *r.want.Iterations)
				}
				if got.Oblique != vectors.RotationOblique(r.spec.Method) {
					t.Errorf("oblique = %v", got.Oblique)
				}
			})
			for c, o := range r.want.Order {
				if o != c {
					reordered++
					break
				}
			}
			for _, s := range r.want.Signs {
				if s < 0 {
					flipped++
					break
				}
			}
		}
	}
	if reordered == 0 || flipped == 0 {
		t.Fatalf("oracle exercises %d reorders and %d flips; both rule halves need a case", reordered, flipped)
	}
	if gpaRuns == 0 || 2*gpaSensitive > gpaRuns {
		t.Fatalf("%d of %d further GPA runs are sensitive; the last-digit comparison must be the rule", gpaSensitive, gpaRuns)
	}
}

// TestRotation_Invariants checks, on every oracle input and criterion,
// the identities the result promises: L = A·Rotation, Φ =
// Rotation⁻¹·Rotation⁻ᵀ with a unit diagonal (the identity when
// orthogonal), variance = diag(Φ·LᵀL) descending, every column summing
// non-negative, and, for an orthogonal rotation, unchanged row
// communalities.
func TestRotation_Invariants(t *testing.T) {
	t.Parallel()

	doc := loadReference[rotationCase](t, "mv_rotation")
	for _, c := range doc.Cases {
		for _, spec := range []rotationSpec{
			{Method: vectors.RotationVarimax},
			{Method: vectors.RotationPromax},
			{Method: vectors.RotationOblimin},
			{Method: vectors.RotationOblimin, Gamma: 0.5},
			{Method: vectors.RotationQuartimax},
			{Method: vectors.RotationEntropy},
			{Method: vectors.RotationGeomin},
			{Method: vectors.RotationGeomin, Epsilon: 0.1},
			{Method: vectors.RotationGeominOrthogonal},
		} {
			t.Run(fmt.Sprintf("%s/%s/k%d/%s/%g/%g", c.Fixture, c.Weight, c.K, spec.Method, spec.Gamma, spec.Epsilon), func(t *testing.T) {
				a := c.Unrotated
				res, err := rotateLoadings(a, spec)
				if err != nil {
					t.Fatal(err)
				}
				const tol = 1e-10
				near := func(where string, got, want float64) {
					if math.Abs(got-want) > tol*(1+math.Abs(want)) {
						t.Errorf("%s = %v, want %v", where, got, want)
					}
				}
				prod := mulRows(a, res.Rotation)
				for i := range prod {
					for j := range prod[i] {
						near(fmt.Sprintf("A·T[%d][%d]", i, j), prod[i][j], res.Loadings[i][j])
					}
				}
				inv := safeInverse(res.Rotation)
				phi := mulRowsNT(inv, inv)
				k := len(res.Phi)
				for i := 0; i < k; i++ {
					near(fmt.Sprintf("phi[%d][%d]", i, i), res.Phi[i][i], 1)
					for j := 0; j < k; j++ {
						near(fmt.Sprintf("T⁻¹T⁻ᵀ[%d][%d]", i, j), phi[i][j], res.Phi[i][j])
						if !res.Oblique && i != j && res.Phi[i][j] != 0 {
							t.Errorf("orthogonal phi[%d][%d] = %v", i, j, res.Phi[i][j])
						}
					}
				}
				v := rotationVariance(res.Loadings, res.Phi)
				for c := range v {
					near(fmt.Sprintf("variance[%d]", c), res.Variance[c], v[c])
					if c > 0 && res.Variance[c] > res.Variance[c-1] {
						t.Errorf("variance not descending: %v", res.Variance)
					}
					sum := 0.0
					for i := range res.Loadings {
						sum += res.Loadings[i][c]
					}
					if sum < 0 {
						t.Errorf("column %d sums %v < 0", c, sum)
					}
				}
				if !res.Oblique {
					for i := range a {
						h0, h1 := 0.0, 0.0
						for j := range a[i] {
							h0 += a[i][j] * a[i][j]
							h1 += res.Loadings[i][j] * res.Loadings[i][j]
						}
						near(fmt.Sprintf("communality[%d]", i), h1, h0)
					}
				}
			})
		}
	}
}

// TestRotation_SingleColumnUnchanged pins the k < 2 rule: a single
// column is returned as is (R's varimax / promax return it, GPArotation
// refuses), Rotation = Φ = I, variance its sum of squares, no flip even
// when it sums negative.
func TestRotation_SingleColumnUnchanged(t *testing.T) {
	t.Parallel()

	a := [][]float64{{-0.8}, {-0.6}, {0.1}}
	for _, m := range vectors.FactorRotations()[1:] {
		res, err := rotateLoadings(a, rotationSpec{Method: m})
		if err != nil {
			t.Fatal(err)
		}
		for i := range a {
			if res.Loadings[i][0] != a[i][0] {
				t.Errorf("%s: loadings %v, want %v", m, res.Loadings, a)
			}
		}
		if res.Rotation[0][0] != 1 || res.Phi[0][0] != 1 || res.Signs[0] != 1 || !res.Converged || res.Oblique != vectors.RotationOblique(m) {
			t.Errorf("%s: %+v", m, res)
		}
		if want := 0.64 + 0.36 + 0.01; math.Abs(res.Variance[0]-want) > 1e-15 {
			t.Errorf("%s: variance %v, want %v", m, res.Variance, want)
		}
	}
}

// TestRotation_UnknownMethodIsInternal pins the programming-fault path:
// a name outside vectors.FactorRotations, and "none" (the decoder maps
// it to no rotation; the engine never sees it).
func TestRotation_UnknownMethodIsInternal(t *testing.T) {
	t.Parallel()

	for _, a := range [][][]float64{{{1, 0}, {0, 1}}, {{1}, {0.5}}} {
		for _, m := range []string{"bentler", vectors.RotationNone, ""} {
			if _, err := rotateLoadings(a, rotationSpec{Method: m}); err == nil {
				t.Fatalf("unknown method %q accepted on %d columns", m, len(a[0]))
			}
		}
	}
}

// TestRotation_GeominEpsilonDefault: an unset ε is GPArotation's
// delta = 0.01 (vectors.GeominDefaultEpsilon), bit for bit, and a
// different ε moves the fit.
func TestRotation_GeominEpsilonDefault(t *testing.T) {
	t.Parallel()

	doc := loadReference[rotationCase](t, "mv_rotation")
	a := doc.Cases[0].Unrotated
	for _, m := range []string{vectors.RotationGeomin, vectors.RotationGeominOrthogonal} {
		def, err := rotateLoadings(a, rotationSpec{Method: m})
		if err != nil {
			t.Fatal(err)
		}
		set, err := rotateLoadings(a, rotationSpec{Method: m, Epsilon: vectors.GeominDefaultEpsilon})
		if err != nil {
			t.Fatal(err)
		}
		sameBitRotation(t, def, set)
		other, err := rotateLoadings(a, rotationSpec{Method: m, Epsilon: 0.1})
		if err != nil {
			t.Fatal(err)
		}
		if gpaSameBits(other.Loadings[0][0], def.Loadings[0][0]) {
			t.Errorf("%s: epsilon 0.1 left loadings[0][0] at %v", m, def.Loadings[0][0])
		}
	}
}

// TestSafeInverse_SingularFallsBackToPseudoInverse pins GPArotation's
// safe_inverse: an exactly singular matrix gets the SVD pseudo-inverse.
func TestSafeInverse_SingularFallsBackToPseudoInverse(t *testing.T) {
	t.Parallel()

	m := [][]float64{{1, 2}, {2, 4}}
	inv := safeInverse(m)
	// pinv of the rank-1 [[1,2],[2,4]] is m / 25.
	for i := range m {
		for j := range m[i] {
			if want := m[i][j] / 25; math.Abs(inv[i][j]-want) > 1e-14 {
				t.Fatalf("pinv = %v, want m/25", inv)
			}
		}
	}
	got := safeInverse([][]float64{{0, 2}, {3, 0}})
	if got[0][1] != 1.0/3 || got[1][0] != 0.5 || got[0][0] != 0 || got[1][1] != 0 {
		t.Fatalf("LU inverse = %v", got)
	}
}

// TestRotationCapNote_NamesSolver: a fitted score attribute's
// iteration-cap caveat is PULSE_MATRIX_NOT_CONVERGED with the rotation's
// solver, run and caps in details — every criterion's solver name, as
// MAT_PCA / MAT_FACTOR report it — and nothing when the rotation
// converged. Both ATTR_PC_SCORE and ATTR_FACTOR_SCORE raise it.
func TestRotationCapNote_NamesSolver(t *testing.T) {
	t.Parallel()

	doc := loadReference[rotationCase](t, "mv_rotation")
	a := doc.Cases[0].Unrotated
	for _, m := range vectors.FactorRotations()[1:] {
		rot, err := rotateLoadings(a, rotationSpec{Method: m})
		if err != nil {
			t.Fatal(err)
		}
		if note := rotationCapNote(m, rot); note != nil {
			t.Errorf("%s: converged rotation noted: %+v", m, note)
		}
		want := map[string]string{
			vectors.RotationVarimax: "varimax", vectors.RotationPromax: "varimax", vectors.RotationOblimin: "gpa_oblimin",
			vectors.RotationQuartimax: "gpa_quartimax", vectors.RotationEntropy: "gpa_entropy",
			vectors.RotationGeomin: "gpa_geomin", vectors.RotationGeominOrthogonal: "gpa_geomin_orthogonal",
		}[m]
		if want == "" || rot.Solver != want {
			t.Errorf("%s: solver %q, want %q", m, rot.Solver, want)
		}
		rot.Converged, rot.Iterations = false, rot.MaxIterations
		note := rotationCapNote(m, rot)
		if note == nil || note.code != errors.PULSE_MATRIX_NOT_CONVERGED || note.details["solver"] != want ||
			note.details["max_iterations"] != rot.MaxIterations || !strings.Contains(note.msg, want) {
			t.Errorf("%s: capped note %+v", m, note)
		}
	}
}
