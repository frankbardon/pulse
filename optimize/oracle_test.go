package optimize_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/optimize"
)

// The R oracles (optimize/testdata/gen_optimize.R; `make
// reference-optimize`). CI never runs R: the JSON is committed with a
// golden-hash footer.
//
// Tolerance: 1e-6 relative on x, the cost / objective, the standard
// errors and the multipliers. The measured disagreement is ≤ 2e-8
// (least squares: MINPACK's ptol stop, which the script bounds at 1e-7
// of the polished stationary point) and ≤ 1e-14 (SLSQP), so 1e-6 is
// ≥ 50× the evidence — wide enough for amd64 / arm64 libm tails in
// these test callbacks, narrow enough that a wrong solution fails. A bound-held variable is compared exactly: both sides place it
// ON the bound.
const oracleRel = 1e-6

// bound is a JSON bound: a number, or "Inf" / "-Inf" for an open side.
type bound float64

func (b *bound) UnmarshalJSON(raw []byte) error {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		switch s {
		case "Inf":
			*b = bound(math.Inf(1))
			return nil
		case "-Inf":
			*b = bound(math.Inf(-1))
			return nil
		}
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return err
	}
	*b = bound(f)
	return nil
}

func bounds(bs []bound) []float64 {
	out := make([]float64, len(bs))
	for i, b := range bs {
		out[i] = float64(b)
	}
	return out
}

// loadOracle strips the golden-hash footer and decodes the document,
// failing when it records no R version or no package version.
func loadOracle(t *testing.T, name, pkg string, doc any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read %s: %v (regenerate with `make reference-optimize`)", name, err)
	}
	body, _ := splitGoldenHash(raw)
	if err := json.Unmarshal(body, doc); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	var meta struct {
		RVersion string            `json:"r_version"`
		Packages map[string]string `json:"packages"`
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(meta.RVersion, "R version ") || meta.Packages[pkg] == "" {
		t.Fatalf("%s records no R version or no %s version", name, pkg)
	}
}

func splitGoldenHash(content []byte) (body []byte, hash string) {
	const marker = "\n// golden-hash: "
	s := string(content)
	i := strings.LastIndex(s, marker)
	if i < 0 {
		return content, ""
	}
	return []byte(s[:i]), strings.TrimSpace(s[i+len(marker):])
}

func relNear(t *testing.T, what string, got, want float64) {
	t.Helper()
	scale := math.Max(math.Abs(got), math.Abs(want))
	if math.IsNaN(got) || math.Abs(got-want) > oracleRel*scale {
		t.Errorf("%s = %.17g, R %.17g (rel diff %.3g > %g)", what, got, want, math.Abs(got-want)/scale, oracleRel)
	}
}

// TestOptimize_OracleGoldensHashed: every committed oracle JSON carries
// a golden-hash footer matching its body, so a hand edit fails.
func TestOptimize_OracleGoldensHashed(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 2 {
		t.Fatalf("want the least_squares and minimize oracles under testdata, found %v", files)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		body, stored := splitGoldenHash(raw)
		if stored == "" {
			t.Errorf("%s: no golden-hash footer (regenerate with `make reference-optimize`)", f)
			continue
		}
		sum := sha256.Sum256(body)
		if hex.EncodeToString(sum[:]) != stored {
			t.Errorf("%s: hash mismatch (file may have been hand-edited; regenerate with `make reference-optimize`)", f)
		}
	}
}

type lsOracleCase struct {
	Name         string      `json:"name"`
	Model        string      `json:"model"`
	Params       []string    `json:"params"`
	Rows         [][]float64 `json:"rows"`
	Start        []float64   `json:"start"`
	Lower        []bound     `json:"lower"`
	Upper        []bound     `json:"upper"`
	X            []float64   `json:"x"`
	Cost         float64     `json:"cost"`
	Held         []string    `json:"held"`
	StdErrors    []float64   `json:"std_errors"`
	ClampedX     []float64   `json:"nlslm_clamped_x"`
	ClampedCost  float64     `json:"nlslm_clamped_cost"`
	residual     func([]float64) []float64
	jacobianFunc func([]float64) [][]float64
}

// model builds the case's residual and Jacobian (plain math: the oracle
// comparison is within tolerance, not bit-exact).
func (c *lsOracleCase) model(t *testing.T) {
	t.Helper()
	m := len(c.Rows)
	r := make([]float64, m)
	jac := make([][]float64, m)
	for i := range jac {
		jac[i] = make([]float64, len(c.Params))
	}
	switch c.Model {
	case "hill": // b0 + beta·t^s/(t^s + h^s)
		c.residual = func(x []float64) []float64 {
			b0, beta, h, s := x[0], x[1], x[2], x[3]
			for i, row := range c.Rows {
				ts := math.Pow(row[0], s)
				r[i] = b0 + beta*ts/(ts+math.Pow(h, s)) - row[1]
			}
			return r
		}
		c.jacobianFunc = func(x []float64) [][]float64 {
			beta, h, s := x[1], x[2], x[3]
			for i, row := range c.Rows {
				ts, hs := math.Pow(row[0], s), math.Pow(h, s)
				d := ts + hs
				jac[i][0] = 1
				jac[i][1] = ts / d
				jac[i][2] = -beta * ts * s * math.Pow(h, s-1) / (d * d)
				jac[i][3] = beta * ts * hs * (math.Log(row[0]) - math.Log(h)) / (d * d)
			}
			return jac
		}
	case "exp_decay": // a·exp(−k·t) + c
		c.residual = func(x []float64) []float64 {
			for i, row := range c.Rows {
				r[i] = x[0]*math.Exp(-x[1]*row[0]) + x[2] - row[1]
			}
			return r
		}
		c.jacobianFunc = func(x []float64) [][]float64 {
			for i, row := range c.Rows {
				e := math.Exp(-x[1] * row[0])
				jac[i][0], jac[i][1], jac[i][2] = e, -x[0]*row[0]*e, 1
			}
			return jac
		}
	default:
		t.Fatalf("%s: unknown model %q", c.Name, c.Model)
	}
}

// TestOptimize_LeastSquaresMatchesNLSLM pins optimize.LeastSquares, from
// R's start under R's bounds and the default schedule, to minpack.lm's
// fit: x, the cost ½Σr², and (interior case) the Gauss–Newton standard
// errors √diag(σ²(JᵀJ)⁻¹) formed from the returned JTJ. Measured
// agreement on arm64: ≤ 1.8e-8 relative on x and the standard errors
// (MINPACK's own ptol stop, which the script bounds at 1e-7), ≤ 4e-15
// on the cost.
//
// In the bound-binding case the held parameter must sit EXACTLY on its
// bound (Active = AtLower), and Pulse's cost must undercut nlsLM's own
// clamped run, which stalls short of the bound-constrained optimum —
// the script's oracle there is nlsLM on the reduced problem.
func TestOptimize_LeastSquaresMatchesNLSLM(t *testing.T) {
	var doc struct {
		Cases []*lsOracleCase `json:"cases"`
	}
	loadOracle(t, "least_squares.json", "minpack.lm", &doc)
	if len(doc.Cases) < 2 {
		t.Fatalf("want an interior and a bound-binding case, got %d", len(doc.Cases))
	}
	sawHeld, sawSE := false, false
	for _, c := range doc.Cases {
		t.Run(c.Name, func(t *testing.T) {
			c.model(t)
			n := len(c.Params)
			res, err := optimize.LeastSquares(c.residual, c.jacobianFunc, c.Start, bounds(c.Lower), bounds(c.Upper), optimize.LeastSquaresOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if !res.Status.Converged() {
				t.Fatalf("status %v", res.Status)
			}
			held := map[string]bool{}
			for _, h := range c.Held {
				held[h] = true
			}
			for j, p := range c.Params {
				if held[p] {
					if res.X[j] != c.X[j] || res.Active[j] != optimize.AtLower {
						t.Errorf("%s = %.17g active %v, want exactly on its lower bound %g", p, res.X[j], res.Active[j], c.X[j])
					}
					continue
				}
				if res.Active[j] != optimize.NotActive {
					t.Errorf("%s active %v, want free", p, res.Active[j])
				}
				relNear(t, p, res.X[j], c.X[j])
			}
			relNear(t, "cost", res.Cost, c.Cost)

			if len(c.Held) > 0 {
				sawHeld = true
				if !(res.Cost < c.ClampedCost) {
					t.Errorf("cost %.17g does not undercut nlsLM's clamped run %.17g", res.Cost, c.ClampedCost)
				}
			}
			if len(c.StdErrors) > 0 {
				sawSE = true
				inv, err := linalg.InverseSPD(res.JTJ)
				if err != nil {
					t.Fatal(err)
				}
				s2 := 2 * res.Cost / float64(len(c.Rows)-n)
				for j, p := range c.Params {
					relNear(t, "se("+p+")", math.Sqrt(s2*inv.At(j, j)), c.StdErrors[j])
				}
			}
		})
	}
	if !sawHeld || !sawSE {
		t.Fatalf("oracle lost a case kind: bound-binding %v, standard errors %v", sawHeld, sawSE)
	}
}

type minOracleCase struct {
	Name        string    `json:"name"`
	X0          []float64 `json:"x0"`
	Lower       []bound   `json:"lower"`
	Upper       []bound   `json:"upper"`
	X           []float64 `json:"x"`
	F           float64   `json:"f"`
	Multipliers []float64 `json:"multipliers"`
	ActiveLower []int     `json:"active_lower"`
	ActiveUpper []int     `json:"active_upper"`
}

// The minimize.json problem, as gen_optimize.R states it:
//
//	min  Σ(x − c)² + exp(0.5(x₁ − x₂)),  c = (1, 2, 1.5, −1)
//	s.t. x₁ + x₂ + x₃ + x₄ − 2.5 = 0
//	     2.5 − x₂² − x₃² ≥ 0
//	     0 ≤ x ≤ 3
var minOracleCenter = []float64{1, 2, 1.5, -1}

func minOracleProblem() (func([]float64) float64, func([]float64) []float64, []optimize.Constraint) {
	f := func(x []float64) float64 {
		s := math.Exp(0.5 * (x[0] - x[1]))
		for i, v := range x {
			d := v - minOracleCenter[i]
			s += d * d
		}
		return s
	}
	grad := func(x []float64) []float64 {
		e := 0.5 * math.Exp(0.5*(x[0]-x[1]))
		g := make([]float64, 4)
		for i, v := range x {
			g[i] = 2 * (v - minOracleCenter[i])
		}
		g[0] += e
		g[1] -= e
		return g
	}
	cons := []optimize.Constraint{
		{
			Type: optimize.Eq,
			Fn:   func(x []float64) float64 { return x[0] + x[1] + x[2] + x[3] - 2.5 },
			Grad: func([]float64) []float64 { return []float64{1, 1, 1, 1} },
		},
		{
			Type: optimize.Ineq,
			Fn:   func(x []float64) float64 { return 2.5 - x[1]*x[1] - x[2]*x[2] },
			Grad: func(x []float64) []float64 { return []float64{0, -2 * x[1], -2 * x[2], 0} },
		},
	}
	return f, grad, cons
}

// TestOptimize_MinimizeMatchesNLoptrSLSQP pins optimize.Minimize to
// nloptr's NLOPT_LD_SLSQP on a strictly convex problem whose unique
// optimum has the equality, the inequality and the x₄ ≥ 0 bound all
// binding: x, f and both multipliers (nloptr reports none; the stored
// ones are the R KKT solve at its solution). Acc is tightened to 1e-12
// to compare solutions, not stopping points: at the default 1e-6 Pulse
// (like SciPy) stops ~4e-6 relative short in x. Measured agreement on
// arm64 at 1e-12: ≤ 7e-15 relative on every figure.
func TestOptimize_MinimizeMatchesNLoptrSLSQP(t *testing.T) {
	var doc struct {
		Cases []minOracleCase `json:"cases"`
	}
	loadOracle(t, "minimize.json", "nloptr", &doc)
	if len(doc.Cases) == 0 {
		t.Fatal("no minimize oracle cases")
	}
	c := doc.Cases[0]
	f, grad, cons := minOracleProblem()
	res, err := optimize.Minimize(f, grad, c.X0, optimize.MinimizeOptions{
		Lower: bounds(c.Lower), Upper: bounds(c.Upper), Constraints: cons, Acc: 1e-12,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != optimize.StatusSuccess {
		t.Fatalf("status %v", res.Status)
	}
	if res.MaxViolation > 1e-10 {
		t.Errorf("max violation %g", res.MaxViolation)
	}
	want := make([]optimize.ActiveBound, len(c.X))
	for _, i := range c.ActiveLower {
		want[i] = optimize.AtLower
	}
	for _, i := range c.ActiveUpper {
		want[i] = optimize.AtUpper
	}
	for i := range c.X {
		if res.Active[i] != want[i] {
			t.Errorf("x%d active %v, want %v", i+1, res.Active[i], want[i])
		}
		if want[i] != optimize.NotActive {
			// On the bound: both sides place it there (R to ~1e-15).
			if math.Abs(res.X[i]-c.X[i]) > 1e-12 {
				t.Errorf("x%d = %.17g, want on its bound %g", i+1, res.X[i], c.X[i])
			}
			continue
		}
		relNear(t, "x"+string(rune('1'+i)), res.X[i], c.X[i])
	}
	relNear(t, "f", res.F, c.F)
	if len(res.Multipliers) != len(c.Multipliers) {
		t.Fatalf("multipliers %v, want %d", res.Multipliers, len(c.Multipliers))
	}
	for i, m := range c.Multipliers {
		relNear(t, "multiplier "+cons[i].Type.String(), res.Multipliers[i], m)
	}
}
