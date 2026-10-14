package processing

import (
	"fmt"
	"math"
	"testing"

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

type rotationOut struct {
	Loadings   [][]float64 `json:"loadings"`
	Rotation   [][]float64 `json:"rotation"`
	Phi        [][]float64 `json:"phi"`
	Variance   []float64   `json:"variance"`
	Order      []int       `json:"order"`
	Signs      []float64   `json:"signs"`
	Kappa      *float64    `json:"kappa"`
	Gamma      *float64    `json:"gamma"`
	Iterations *int        `json:"iterations"`
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
}

func rotNear(t *testing.T, where string, got, want float64) {
	t.Helper()
	if !(math.Abs(got-want) <= rotationOracleTol*(1+math.Abs(want))) {
		t.Errorf("%s = %.17g, R = %.17g", where, got, want)
	}
}

func rotNearRows(t *testing.T, where string, got, want [][]float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d rows, R %d", where, len(got), len(want))
	}
	for i := range want {
		if len(got[i]) != len(want[i]) {
			t.Fatalf("%s[%d]: %d cols, R %d", where, i, len(got[i]), len(want[i]))
		}
		for j := range want[i] {
			rotNear(t, fmt.Sprintf("%s[%d][%d]", where, i, j), got[i][j], want[i][j])
		}
	}
}

// TestRotation_MatchesROracle runs every case's unrotated loadings
// through each criterion — varimax, promax at κ = 4 and 2, oblimin at
// γ = 0 (quartimin) and 0.5 (biquartimin) — and compares the
// canonicalised loadings, rotation matrix, Φ and variance accounted to
// R within rotationOracleTol, the column order and signs exactly (the
// fixtures exercise reordered and flipped columns).
func TestRotation_MatchesROracle(t *testing.T) {
	doc := loadReference[rotationCase](t, "mv_rotation")
	if len(doc.Cases) == 0 {
		t.Fatal("oracle has no cases")
	}
	reordered, flipped := 0, 0
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
				rotNearRows(t, "loadings", got.Loadings, r.want.Loadings)
				rotNearRows(t, "rotation", got.Rotation, r.want.Rotation)
				rotNearRows(t, "phi", got.Phi, r.want.Phi)
				for i, w := range r.want.Variance {
					rotNear(t, fmt.Sprintf("variance[%d]", i), got.Variance[i], w)
				}
				if r.want.Iterations != nil && got.Iterations != *r.want.Iterations {
					t.Logf("GPA iterations %d, R %d (values still within tolerance)", got.Iterations, *r.want.Iterations)
				}
				if got.Oblique != (r.spec.Method != vectors.RotationVarimax) {
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
}

// TestRotation_Invariants checks, on every oracle input and criterion,
// the identities the result promises: L = A·Rotation, Φ =
// Rotation⁻¹·Rotation⁻ᵀ with a unit diagonal (the identity when
// orthogonal), variance = diag(Φ·LᵀL) descending, every column summing
// non-negative, and, for an orthogonal rotation, unchanged row
// communalities.
func TestRotation_Invariants(t *testing.T) {
	doc := loadReference[rotationCase](t, "mv_rotation")
	for _, c := range doc.Cases {
		for _, spec := range []rotationSpec{
			{Method: vectors.RotationVarimax},
			{Method: vectors.RotationPromax},
			{Method: vectors.RotationOblimin},
			{Method: vectors.RotationOblimin, Gamma: 0.5},
		} {
			t.Run(fmt.Sprintf("%s/%s/k%d/%s/%g", c.Fixture, c.Weight, c.K, spec.Method, spec.Gamma), func(t *testing.T) {
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
	a := [][]float64{{-0.8}, {-0.6}, {0.1}}
	for _, m := range []string{vectors.RotationVarimax, vectors.RotationPromax, vectors.RotationOblimin} {
		res, err := rotateLoadings(a, rotationSpec{Method: m})
		if err != nil {
			t.Fatal(err)
		}
		for i := range a {
			if res.Loadings[i][0] != a[i][0] {
				t.Errorf("%s: loadings %v, want %v", m, res.Loadings, a)
			}
		}
		if res.Rotation[0][0] != 1 || res.Phi[0][0] != 1 || res.Signs[0] != 1 || !res.Converged {
			t.Errorf("%s: %+v", m, res)
		}
		if want := 0.64 + 0.36 + 0.01; math.Abs(res.Variance[0]-want) > 1e-15 {
			t.Errorf("%s: variance %v, want %v", m, res.Variance, want)
		}
	}
}

// TestRotation_UnknownMethodIsInternal pins the programming-fault path.
func TestRotation_UnknownMethodIsInternal(t *testing.T) {
	for _, a := range [][][]float64{{{1, 0}, {0, 1}}, {{1}, {0.5}}} {
		if _, err := rotateLoadings(a, rotationSpec{Method: "quartimax"}); err == nil {
			t.Fatalf("unknown method accepted on %d columns", len(a[0]))
		}
	}
}

// TestSafeInverse_SingularFallsBackToPseudoInverse pins GPArotation's
// safe_inverse: an exactly singular matrix gets the SVD pseudo-inverse.
func TestSafeInverse_SingularFallsBackToPseudoInverse(t *testing.T) {
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
