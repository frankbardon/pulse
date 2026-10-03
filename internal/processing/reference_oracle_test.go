package processing

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// Reference oracle for the shared distribution primitives.
//
// The goldens under testdata/reference/ are written by
// scripts/reference/gen_reference.R (`make reference`) — R is the
// oracle, CI never runs it, and every file carries a `// golden-hash:`
// footer checked by TestGoldensNotHandEdited below. Inputs and expected
// values are emitted with %.17g, so each case replays the exact doubles
// R evaluated.
//
// Tolerance: relative 1e-10 everywhere (oracleRelTol), with an absolute
// floor (oracleProbAbsFloor) that only matters at the representable
// edge of the far tail. Comparisons are relative, never bit-exact, so
// amd64 FMA contraction (vs arm64 local runs) cannot flip a case. A
// looser per-primitive tolerance is declared at its test with the
// justification next to it.

const (
	oracleRelTol = 1e-10
	// oracleProbAbsFloor is the absolute floor for probabilities: the
	// grids stop at 1e-300, so this floor never masks a tail value.
	oracleProbAbsFloor = 1e-310
	// oracleQuantileAbsFloor is the absolute floor for quantiles, which
	// can be exactly 0 (qnorm(0.5)); 1e-14 is a few ulps around 1.
	oracleQuantileAbsFloor = 1e-14
)

const referenceDir = "testdata/reference"

type referenceDoc[C any] struct {
	Generator string            `json:"generator"`
	RVersion  string            `json:"r_version"`
	Packages  map[string]string `json:"packages"`
	Primitive string            `json:"primitive"`
	RFunction string            `json:"r_function"`
	Cases     []C               `json:"cases"`
}

func loadReference[C any](t *testing.T, name string) referenceDoc[C] {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(referenceDir, name+".json"))
	if err != nil {
		t.Fatalf("read reference %s: %v (regenerate with `make reference`)", name, err)
	}
	body, _ := splitReferenceHash(raw)
	var doc referenceDoc[C]
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode reference %s: %v", name, err)
	}
	if doc.RVersion == "" || doc.Packages["jsonlite"] == "" || doc.Packages["stats"] == "" {
		t.Fatalf("reference %s does not record R / package versions", name)
	}
	if len(doc.Cases) == 0 {
		t.Fatalf("reference %s has no cases", name)
	}
	return doc
}

// oracleCheck tracks the worst relative error per primitive so a
// failure report shows the scale of the miss, not just the first case.
type oracleCheck struct {
	t         *testing.T
	name      string
	rel       float64
	absFloor  float64
	worst     float64
	worstCase string
	failures  int
}

func newOracleCheck(t *testing.T, name string, rel, absFloor float64) *oracleCheck {
	return &oracleCheck{t: t, name: name, rel: rel, absFloor: absFloor}
}

func (c *oracleCheck) check(got, want float64, format string, args ...any) {
	c.t.Helper()
	diff := math.Abs(got - want)
	relErr := diff / math.Max(math.Abs(want), math.SmallestNonzeroFloat64)
	if want != 0 && relErr > c.worst && !math.IsNaN(relErr) {
		c.worst = relErr
		c.worstCase = fmt.Sprintf(format, args...) + fmt.Sprintf(": got %.17g, want %.17g", got, want)
	}
	if math.IsNaN(got) || diff > math.Max(c.rel*math.Abs(want), c.absFloor) {
		c.failures++
		if c.failures <= 25 {
			c.t.Errorf("%s("+format+") = %.17g, want %.17g (rel err %.3g, tol %.0g)",
				append([]any{c.name}, append(args, got, want, relErr, c.rel)...)...)
		}
	}
}

func (c *oracleCheck) done(cases int) {
	c.t.Helper()
	if c.failures > 25 {
		c.t.Errorf("%s: %d more failures suppressed", c.name, c.failures-25)
	}
	c.t.Logf("%s: %d cases, worst relative error %.3g (%s)", c.name, cases, c.worst, c.worstCase)
}

func TestReferenceOracle_StudentTTwoSidedP(t *testing.T) {
	doc := loadReference[struct {
		T         float64 `json:"t"`
		DF        float64 `json:"df"`
		PTwoSided float64 `json:"p_two_sided"`
	}](t, "student_t_p")
	c := newOracleCheck(t, "studentTTwoSidedP", oracleRelTol, oracleProbAbsFloor)
	for _, k := range doc.Cases {
		c.check(studentTTwoSidedP(k.T, k.DF), k.PTwoSided, "t=%g, df=%g", k.T, k.DF)
		// Symmetric in t.
		c.check(studentTTwoSidedP(-k.T, k.DF), k.PTwoSided, "t=%g, df=%g", -k.T, k.DF)
	}
	c.done(len(doc.Cases))
}

func TestReferenceOracle_StudentTInverseTwoSided(t *testing.T) {
	doc := loadReference[struct {
		Alpha float64 `json:"alpha"`
		DF    float64 `json:"df"`
		Q     float64 `json:"q"`
	}](t, "student_t_inverse_two_sided")
	c := newOracleCheck(t, "studentTInverseTwoSided", oracleRelTol, oracleQuantileAbsFloor)
	for _, k := range doc.Cases {
		c.check(studentTInverseTwoSided(k.Alpha, k.DF), k.Q, "alpha=%g, df=%g", k.Alpha, k.DF)
	}
	c.done(len(doc.Cases))
}

func TestReferenceOracle_ChiSquareSurvival(t *testing.T) {
	doc := loadReference[struct {
		X  float64 `json:"x"`
		DF float64 `json:"df"`
		P  float64 `json:"p"`
	}](t, "chi_square_survival")
	c := newOracleCheck(t, "chiSquareSurvival", oracleRelTol, oracleProbAbsFloor)
	for _, k := range doc.Cases {
		c.check(chiSquareSurvival(k.X, k.DF), k.P, "x=%g, df=%g", k.X, k.DF)
	}
	c.done(len(doc.Cases))
}

func TestReferenceOracle_FSurvival(t *testing.T) {
	doc := loadReference[struct {
		F   float64 `json:"f"`
		DF1 float64 `json:"df1"`
		DF2 float64 `json:"df2"`
		P   float64 `json:"p"`
	}](t, "f_survival")
	c := newOracleCheck(t, "fSurvival", oracleRelTol, oracleProbAbsFloor)
	for _, k := range doc.Cases {
		c.check(fSurvival(k.F, k.DF1, k.DF2), k.P, "f=%g, df1=%g, df2=%g", k.F, k.DF1, k.DF2)
	}
	c.done(len(doc.Cases))
}

func TestReferenceOracle_StandardNormalCDF(t *testing.T) {
	doc := loadReference[struct {
		Z   float64 `json:"z"`
		CDF float64 `json:"cdf"`
	}](t, "normal_cdf")
	c := newOracleCheck(t, "standardNormalCDF", oracleRelTol, oracleProbAbsFloor)
	for _, k := range doc.Cases {
		c.check(standardNormalCDF(k.Z), k.CDF, "z=%g", k.Z)
	}
	c.done(len(doc.Cases))
}

func TestReferenceOracle_StandardNormalPPF(t *testing.T) {
	doc := loadReference[struct {
		P float64 `json:"p"`
		Q float64 `json:"q"`
	}](t, "normal_ppf")
	c := newOracleCheck(t, "standardNormalPPF", oracleRelTol, oracleQuantileAbsFloor)
	for _, k := range doc.Cases {
		c.check(standardNormalPPF(k.P), k.Q, "p=%g", k.P)
	}
	c.done(len(doc.Cases))
}

func TestReferenceOracle_KolmogorovSurvival(t *testing.T) {
	doc := loadReference[struct {
		Lambda float64 `json:"lambda"`
		P      float64 `json:"p"`
	}](t, "kolmogorov_survival")
	c := newOracleCheck(t, "kolmogorovSurvival", oracleRelTol, oracleProbAbsFloor)
	for _, k := range doc.Cases {
		c.check(kolmogorovSurvival(k.Lambda), k.P, "lambda=%g", k.Lambda)
	}
	c.done(len(doc.Cases))
}

// parallelEval evaluates fn over n cases on GOMAXPROCS workers. The
// studentized-range primitives are nested quadratures (milliseconds per
// call), so the oracle evaluates them concurrently and checks serially.
func parallelEval(n int, fn func(i int) float64) []float64 {
	out := make([]float64, n)
	var wg sync.WaitGroup
	next := make(chan int)
	for range runtime.GOMAXPROCS(0) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				out[i] = fn(i)
			}
		}()
	}
	for i := range n {
		next <- i
	}
	close(next)
	wg.Wait()
	return out
}

type tukeySurvivalCase struct {
	Q       float64 `json:"q"`
	K       float64 `json:"k"`
	DF      float64 `json:"df"`
	P       float64 `json:"p"`
	RPtukey float64 `json:"r_ptukey_upper"`
}

type tukeyInverseCase struct {
	Alpha   float64 `json:"alpha"`
	K       float64 `json:"k"`
	DF      float64 `json:"df"`
	Q       float64 `json:"q"`
	RQtukey float64 `json:"r_qtukey"`
}

// The studentized-range oracle is a nested stats::integrate() of the
// cancellation-free survival (see gen_reference.R), not ptukey(): R's
// ptukey() is accurate to only ~1e-8 and forms the tail as 1 − CDF.
func TestReferenceOracle_StudentizedRangeSurvival(t *testing.T) {
	doc := loadReference[tukeySurvivalCase](t, "studentized_range_survival")
	got := parallelEval(len(doc.Cases), func(i int) float64 {
		k := doc.Cases[i]
		return studentizedRangeSurvival(k.Q, int(k.K), k.DF)
	})
	c := newOracleCheck(t, "studentizedRangeSurvival", oracleRelTol, oracleProbAbsFloor)
	for i, k := range doc.Cases {
		c.check(got[i], k.P, "q=%g, k=%g, df=%g", k.Q, k.K, k.DF)
	}
	c.done(len(doc.Cases))
}

func TestReferenceOracle_StudentizedRangeInverse(t *testing.T) {
	doc := loadReference[tukeyInverseCase](t, "studentized_range_inverse")
	got := parallelEval(len(doc.Cases), func(i int) float64 {
		k := doc.Cases[i]
		return studentizedRangeInverse(k.Alpha, int(k.K), k.DF)
	})
	c := newOracleCheck(t, "studentizedRangeInverse", oracleRelTol, oracleQuantileAbsFloor)
	for i, k := range doc.Cases {
		c.check(got[i], k.Q, "alpha=%g, k=%g, df=%g", k.Alpha, k.K, k.DF)
	}
	c.done(len(doc.Cases))
}

// TestReferenceOracle_StudentizedRangeMatchesRBuiltins cross-checks
// the quadrature oracle itself against R's ptukey()/qtukey(), an
// independent algorithm (Copenhaver & Holland), so a defect shared by
// gen_reference.R and the Go port cannot pass unseen.
//
// Looser tolerances, justified: ptukey() is accurate to ~1e-8 relative
// in its CDF for df ≥ 10 and returns the upper tail as 1 − CDF, so its
// relative tail error grows like 1e-8·(1−p)/p — the check covers
// p ≥ 1e-4 at relative 1e-5. qtukey() is documented accurate to ~4
// decimal places: relative 1e-3. Below df = 10 both lose accuracy (at
// df = 2, k = 2 ptukey is 2e-5 off a closed form — see
// TestStudentizedRange_TwoSampleIsStudentT), so the cross-check skips
// them; the k = 2 closed form covers small df instead.
func TestReferenceOracle_StudentizedRangeMatchesRBuiltins(t *testing.T) {
	surv := loadReference[tukeySurvivalCase](t, "studentized_range_survival")
	checked := 0
	for _, k := range surv.Cases {
		if k.P < 1e-4 || k.DF < 10 {
			continue
		}
		checked++
		if math.Abs(k.P-k.RPtukey) > 1e-5*k.P {
			t.Errorf("q=%g k=%g df=%g: oracle %.17g vs ptukey %.17g", k.Q, k.K, k.DF, k.P, k.RPtukey)
		}
	}
	inv := loadReference[tukeyInverseCase](t, "studentized_range_inverse")
	for _, k := range inv.Cases {
		if k.DF < 10 {
			continue
		}
		checked++
		if math.Abs(k.Q-k.RQtukey) > 1e-3*k.Q {
			t.Errorf("alpha=%g k=%g df=%g: oracle %.17g vs qtukey %.17g", k.Alpha, k.K, k.DF, k.Q, k.RQtukey)
		}
	}
	if checked < 150 {
		t.Fatalf("only %d cases cross-checked against ptukey/qtukey", checked)
	}
}

// TestStudentizedRange_TwoSampleIsStudentT pins the k = 2 case against
// an exact identity at every df: with two groups Q = √2·|T|, T ~ t(ν),
// so P(Q > q) = P(|T| > q/√2). Checked for both the Go port and the R
// quadrature oracle, against the R-verified Student-t primitive.
func TestStudentizedRange_TwoSampleIsStudentT(t *testing.T) {
	doc := loadReference[tukeySurvivalCase](t, "studentized_range_survival")
	n := 0
	for _, k := range doc.Cases {
		if k.K != 2 {
			continue
		}
		n++
		want := studentTTwoSidedP(k.Q/math.Sqrt2, k.DF)
		if got := studentizedRangeSurvival(k.Q, 2, k.DF); math.Abs(got-want) > oracleRelTol*want {
			t.Errorf("studentizedRangeSurvival(q=%g, 2, df=%g) = %.17g, want P(|T|>q/√2) = %.17g", k.Q, k.DF, got, want)
		}
		if math.Abs(k.P-want) > oracleRelTol*want {
			t.Errorf("R oracle (q=%g, k=2, df=%g) = %.17g, want P(|T|>q/√2) = %.17g", k.Q, k.DF, k.P, want)
		}
	}
	if n == 0 {
		t.Fatal("no k = 2 cases in the studentized-range golden")
	}
}

// TestNormalRangeSurvival_TwoSampleClosedForm pins the inner integral
// against the closed form for k = 2: the range of two iid N(0, 1) is
// |Z₁ − Z₂| ~ |N(0, 2)|, so P(R > t) = 2·Φ(−t/√2) = erfc(t/2).
func TestNormalRangeSurvival_TwoSampleClosedForm(t *testing.T) {
	for _, tt := range []float64{1e-3, 0.1, 0.5, 1, 2, 3.5, 5, 8, 12, 20, 30, 50} {
		want := math.Erfc(tt / 2)
		got := normalRangeSurvival(tt, 2)
		if math.Abs(got-want) > oracleRelTol*want {
			t.Errorf("normalRangeSurvival(%g, 2) = %.17g, want erfc(t/2) = %.17g (rel err %.3g)",
				tt, got, want, math.Abs(got-want)/want)
		}
	}
}

// TestGK15_ExactOnPolynomials checks the Gauss–Kronrod tables: the
// 15-point Kronrod rule is exact through degree 22 and the embedded
// 7-point Gauss rule through degree 13, so a mistyped node or weight
// shows up as a non-zero error on some monomial.
func TestGK15_ExactOnPolynomials(t *testing.T) {
	for deg := 0; deg <= 22; deg++ {
		f := func(x float64) float64 { return math.Pow(x, float64(deg)) }
		want := 0.0
		if deg%2 == 0 {
			want = 2 / float64(deg+1)
		}
		kron, est := gk15(f, -1, 1)
		if math.Abs(kron-want) > 1e-15 {
			t.Errorf("Kronrod ∫x^%d over [-1,1] = %.17g, want %.17g", deg, kron, want)
		}
		if deg <= 13 && est > 1e-15 {
			t.Errorf("Gauss/Kronrod disagree on x^%d (error estimate %.3g); Gauss rule should be exact", deg, est)
		}
	}
}

// TestGoldensNotHandEdited verifies every R reference golden under
// testdata/reference/ ends with a valid `// golden-hash:` footer whose
// hash matches the body. Mirrors the regression and descriptor gates;
// regenerate with `make reference`, never by hand.
func TestGoldensNotHandEdited(t *testing.T) {
	entries, err := os.ReadDir(referenceDir)
	if err != nil {
		t.Fatalf("reading %s: %v", referenceDir, err)
	}
	seen := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		seen++
		content, err := os.ReadFile(filepath.Join(referenceDir, entry.Name()))
		if err != nil {
			t.Errorf("reading %s: %v", entry.Name(), err)
			continue
		}
		body, stored := splitReferenceHash(content)
		if stored == "" {
			t.Errorf("%s: no golden-hash found (file may have been hand-edited)", entry.Name())
			continue
		}
		sum := sha256.Sum256(body)
		if hex.EncodeToString(sum[:]) != stored {
			t.Errorf("%s: hash mismatch (file may have been hand-edited; regenerate with `make reference`)", entry.Name())
		}
	}
	if seen == 0 {
		t.Fatalf("no reference goldens under %s", referenceDir)
	}
}

func splitReferenceHash(content []byte) (body []byte, hash string) {
	const marker = "\n// golden-hash: "
	s := string(content)
	idx := strings.LastIndex(s, marker)
	if idx < 0 {
		return content, ""
	}
	return []byte(s[:idx]), strings.TrimSpace(s[idx+len(marker):])
}
