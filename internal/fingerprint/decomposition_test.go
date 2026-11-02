package fingerprint_test

// The decomposition tolerance golden (U61 cross-arch-determinism, PRD
// FR-18; owner decision Q2 — split tolerance). Figures seeded by a gonum
// eigen / SVD / QR decomposition (linalg.SymEigen, linalg.SVD, the SEM
// fit's gonum mat calls) are NOT bit-identical across architectures:
// gonum runs SSE2 assembly kernels on amd64 and FMA-contracted pure Go
// on arm64, so their last ulps differ and iterative fits carry the
// difference further (measured arm64 vs linux/amd64 at authoring: at
// most 2.1e-13 pure relative, 4.1e-14 under the floor below). They are the
// accepted residual of U61, so this golden pins them at a documented
// tolerance instead of exact bits:
//
//	|got − want| ≤ decompRelTol · max(|want|, floor)
//
// decompRelTol is 1e-12 relative. floor is the row's absolute scale for
// near-zero figures: a loading, eigenvector component, correlation or
// p-value that sits near 0 has no meaningful relative error (its
// cross-arch spread is an absolute ~1e-16 · the scale of the matrix it
// came from), so each row declares the scale of its figures — 1 for
// correlation-scale outputs (loadings, eigenvectors, p-values, shares)
// and the data's own magnitude where that is larger. A figure larger
// than floor is held to pure relative 1e-12.
//
// Signs: linalg's canonicalize fixes each eigenvector's (and SVD
// pair's) sign — dominant component positive — so the golden compares
// the signs the engine reports, rotations included: a sign flip is a
// failure, not noise.
//
// The golden is generated on the arm64 dev host with -update (never by
// hand; TestGoldensNotHandEdited checks its footer) and CI's
// linux/amd64 run is the tripwire:
//
//	go test -count=1 ./internal/fingerprint -run TestDecomposition_Tolerance -update
//	go test -count=1 ./internal/fingerprint
//
// MAT_SEM is pinned only for a fit that runs to full convergence on
// both architectures: solver fisher, gradient below grad_tol, converged
// and the same iteration count (decomp.attitude.sem_path_fisher, 43
// iterations; measured spread ≤ 1.5e-14 relative). Deliberately NOT
// pinned — the documented residual (linalg.md "Not covered"), measured
// arm64 vs linux/amd64:
//   - default SLSQP fits (ftol 1e-6 early stop): the stop iteration
//     depends on the last ulps, spread up to ~7.9e-4 relative; even at
//     ftol 1e-15 a two-factor CFA (two_factor.pulse) spreads ~2.9e-8;
//   - Fisher fits that stop on the objective-decrease test rather than
//     grad_tol: converged = true on both arches, but a different
//     iteration count (two_factor CFA: 18 vs 20) and ~3.8e-9 relative
//     on the estimates; a two-factor attitude path model (same
//     iteration count) still spreads ~1.3e-12 on its z values;
//   - meanstructure fits on the default solver: ~1.3e-2 relative;
//   - bootstrap figures (resampled refits, near-zero estimates): ~1
//     relative on near-zero figures;
//   - multi-group fits on the default solver: ~8e-8; WLSMV: ~3.5e-9;
//   - solver fisher + information rank2: the `converged` flag and the
//     iteration count can flip between architectures.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const (
	decompGoldenPath = "testdata/decomposition.golden"
	// decompRelTol is the documented cross-arch tolerance (owner Q2).
	decompRelTol = 1e-12
)

// tolFigure is a requestFigure compared within tolerance; floor is the
// absolute scale below which the comparison stops being relative.
type tolFigure struct {
	requestFigure
	floor float64
}

// Row floors: unitScale for correlation-scale figures; attitudeCovScale
// for figures at the scale of attitude's raw covariance (entries
// 20–150).
const (
	unitScale        = 1.0
	attitudeCovScale = 100.0
)

// decompositionFigures: end-to-end figures seeded by a gonum
// decomposition. The paths address the Response --json shape.
var decompositionFigures = []tolFigure{
	// MAT_PCA (linalg.SymEigen of the correlation / covariance).
	{requestFigure{"decomp.mtcars.pca_corr", "mtcars.pulse",
		`{"vectors":[{"name":"v","fields":["mpg","disp","hp","drat","wt","qsec"]}],"matrices":[{"type":"MAT_PCA","vector":"v","name":"m"}]}`,
		[]string{"matrices.0.primary.*", "matrices.0.auxiliary.*", "matrices.0.vectors.*", "matrices.0.scalars.*"}}, unitScale},
	{requestFigure{"decomp.mtcars.pca_cov", "mtcars.pulse",
		`{"vectors":[{"name":"v","fields":["mpg","wt","qsec","drat"]}],"matrices":[{"type":"MAT_PCA","vector":"v","name":"m","params":{"on":"covariance","components":2}}]}`,
		[]string{"matrices.0.primary.*", "matrices.0.auxiliary.*", "matrices.0.vectors.*", "matrices.0.scalars.*"}}, unitScale},
	{requestFigure{"decomp.mtcars.pca_varimax", "mtcars.pulse",
		`{"vectors":[{"name":"v","fields":["mpg","disp","hp","drat","wt","qsec"]}],"matrices":[{"type":"MAT_PCA","vector":"v","name":"m","params":{"components":2,"rotation":"varimax"}}]}`,
		[]string{"matrices.0.primary.*", "matrices.0.vectors.*"}}, unitScale},
	{requestFigure{"decomp.mtcars.pca_quartimax", "mtcars.pulse",
		`{"vectors":[{"name":"v","fields":["mpg","disp","hp","drat","wt","qsec"]}],"matrices":[{"type":"MAT_PCA","vector":"v","name":"m","params":{"components":2,"rotation":"quartimax"}}]}`,
		[]string{"matrices.0.primary.*", "matrices.0.vectors.*"}}, unitScale},
	{requestFigure{"decomp.mtcars.pca_geomin_orthogonal", "mtcars.pulse",
		`{"vectors":[{"name":"v","fields":["mpg","disp","hp","drat","wt","qsec"]}],"matrices":[{"type":"MAT_PCA","vector":"v","name":"m","params":{"components":2,"rotation":"geomin_orthogonal"}}]}`,
		[]string{"matrices.0.primary.*", "matrices.0.vectors.*"}}, unitScale},
	{requestFigure{"decomp.mtcars.pca_entropy", "mtcars.pulse",
		`{"vectors":[{"name":"v","fields":["mpg","disp","hp","drat","wt","qsec"]}],"matrices":[{"type":"MAT_PCA","vector":"v","name":"m","params":{"components":2,"rotation":"entropy"}}]}`,
		[]string{"matrices.0.primary.*", "matrices.0.vectors.*"}}, unitScale},

	// MAT_FACTOR (SymEigen seed, then the ML / minres iterations and the
	// GPA / promax rotations).
	{requestFigure{"decomp.attitude.factor_ml_promax", "attitude.pulse",
		`{"vectors":[{"name":"v","fields":["rating","complaints","privileges","learning","raises","critical","advance"]}],"matrices":[{"type":"MAT_FACTOR","vector":"v","name":"m","params":{"factors":2,"extraction":"ml","rotation":"promax"}}]}`,
		[]string{"matrices.0.primary.*", "matrices.0.auxiliary.*", "matrices.0.vectors.*", "matrices.0.scalars.*"}}, unitScale},
	{requestFigure{"decomp.attitude.factor_minres_geomin", "attitude.pulse",
		`{"vectors":[{"name":"v","fields":["rating","complaints","privileges","learning","raises","critical","advance"]}],"matrices":[{"type":"MAT_FACTOR","vector":"v","name":"m","params":{"factors":2,"rotation":"geomin"}}]}`,
		[]string{"matrices.0.primary.*", "matrices.0.auxiliary.*", "matrices.0.vectors.*", "matrices.0.scalars.*"}}, unitScale},
	{requestFigure{"decomp.attitude.factor_minres_entropy", "attitude.pulse",
		`{"vectors":[{"name":"v","fields":["rating","complaints","privileges","learning","raises","critical","advance"]}],"matrices":[{"type":"MAT_FACTOR","vector":"v","name":"m","params":{"factors":2,"rotation":"entropy"}}]}`,
		[]string{"matrices.0.primary.*", "matrices.0.vectors.*", "matrices.0.scalars.*"}}, unitScale},
	{requestFigure{"decomp.two_factor.factor_minres_varimax", "two_factor.pulse",
		`{"vectors":[{"name":"v","fields":["v1","v2","v3","v4","v5","v6"]}],"matrices":[{"type":"MAT_FACTOR","vector":"v","name":"m","params":{"factors":2,"rotation":"varimax"}}]}`,
		[]string{"matrices.0.primary.*", "matrices.0.vectors.*", "matrices.0.scalars.*"}}, unitScale},
	{requestFigure{"decomp.two_factor.factor_minres_quartimax", "two_factor.pulse",
		`{"vectors":[{"name":"v","fields":["v1","v2","v3","v4","v5","v6"]}],"matrices":[{"type":"MAT_FACTOR","vector":"v","name":"m","params":{"factors":2,"rotation":"quartimax"}}]}`,
		[]string{"matrices.0.primary.*"}}, unitScale},
	{requestFigure{"decomp.two_factor.factor_minres_geomin_orthogonal", "two_factor.pulse",
		`{"vectors":[{"name":"v","fields":["v1","v2","v3","v4","v5","v6"]}],"matrices":[{"type":"MAT_FACTOR","vector":"v","name":"m","params":{"factors":2,"rotation":"geomin_orthogonal"}}]}`,
		[]string{"matrices.0.primary.*"}}, unitScale},
	{requestFigure{"decomp.two_factor.factor_pa_oblimin", "two_factor.pulse",
		`{"vectors":[{"name":"v","fields":["v1","v2","v3","v4","v5","v6"]}],"matrices":[{"type":"MAT_FACTOR","vector":"v","name":"m","params":{"factors":2,"extraction":"principal_axis","rotation":"oblimin"}}]}`,
		[]string{"matrices.0.primary.*", "matrices.0.auxiliary.*", "matrices.0.vectors.*"}}, unitScale},
	{requestFigure{"decomp.two_factor.factor_ml_none", "two_factor.pulse",
		`{"vectors":[{"name":"v","fields":["v1","v2","v3","v4","v5","v6"]}],"matrices":[{"type":"MAT_FACTOR","vector":"v","name":"m","params":{"factors":2,"extraction":"ml"}}]}`,
		[]string{"matrices.0.primary.*", "matrices.0.vectors.*", "matrices.0.scalars.*"}}, unitScale},

	// TEST_MANOVA (SymEigen of E⁻¹H) and its weighted twin.
	{requestFigure{"decomp.mtcars_groups.manova_by_cyl", "mtcars_groups.pulse",
		`{"tests":[{"type":"TEST_MANOVA","fields":["mpg","wt","qsec"],"split_by":"cyl","label":"t"}]}`,
		[]string{"tests.0.statistic", "tests.0.p_value", "tests.0.details.*"}}, unitScale},
	{requestFigure{"decomp.mtcars_groups.manova_by_gear_wprob", "mtcars_groups.pulse",
		`{"tests":[{"type":"TEST_MANOVA","fields":["mpg","disp","drat"],"split_by":"gear","label":"t","weight":{"field":"w_prob","kind":"probability"}}]}`,
		[]string{"tests.0.statistic", "tests.0.p_value", "tests.0.details.*"}}, unitScale},

	// MAT_COLLINEARITY (SymEigen condition indices / variance
	// proportions). (MAT_CORRELATION's determinant is not here: it runs
	// the FMA-free reference Cholesky, so it is bit-pinned in
	// fingerprint.golden.)
	{requestFigure{"decomp.mtcars.collinearity", "mtcars.pulse",
		`{"matrices":[{"type":"MAT_COLLINEARITY","name":"m","fields":["disp","hp","wt","drat"]}]}`,
		[]string{"matrices.0.*"}}, unitScale},
	{requestFigure{"decomp.mtcars.collinearity_centered", "mtcars.pulse",
		`{"matrices":[{"type":"MAT_COLLINEARITY","name":"m","fields":["disp","hp","wt","drat"],"params":{"center":true}}]}`,
		[]string{"matrices.0.*"}}, unitScale},

	// ATTR_PC_SCORE / ATTR_FACTOR_SCORE (SymEigen-fitted scorers), read
	// back through spread aggregates (their means are ~0 by design).
	{requestFigure{"decomp.mtcars_groups.pc_factor_scores", "mtcars_groups.pulse",
		`{"attributes":[{"type":"ATTR_PC_SCORE","label":"pc1","fields":["mpg","disp","hp","wt"],"params":{"component":1}},{"type":"ATTR_PC_SCORE","label":"pc2v","fields":["mpg","disp","hp","wt"],"params":{"component":2,"components":2,"rotation":"varimax"}},{"type":"ATTR_FACTOR_SCORE","label":"f1","fields":["mpg","disp","hp","wt","qsec","drat"],"params":{"factor":1,"factors":1}},{"type":"ATTR_FACTOR_SCORE","label":"f1b","fields":["mpg","disp","hp","wt","qsec","drat"],"params":{"factor":1,"factors":1,"extraction":"ml","method":"bartlett"}}],"groups":[{"type":"GROUP_CATEGORY","field":"cyl"}],"aggregations":[{"type":"AGG_AVERAGE","field":"pc1","label":"m_pc1"},{"type":"AGG_STDDEV","field":"pc2v","label":"sd_pc2v"},{"type":"AGG_AVERAGE","field":"f1","label":"m_f1"},{"type":"AGG_MAX","field":"f1b","label":"max_f1b"}]}`,
		[]string{"data.0.m_pc1", "data.1.m_pc1", "data.2.m_pc1", "data.0.sd_pc2v", "data.1.sd_pc2v", "data.0.m_f1", "data.2.m_f1", "data.0.max_f1b", "data.1.max_f1b"}}, unitScale},

	// OVERLAY_CORRESPONDENCE (linalg.SVD of the standardised residuals).
	{requestFigure{"decomp.mtcars_groups.correspondence_cyl_gear", "mtcars_groups.pulse",
		`{"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"cyl"}],"columns":[{"type":"GROUP_CATEGORY","field":"gear"}],"cell":{"type":"AGG_COUNT","field":"mpg","label":"n"}},"overlays":[{"name":"map","kind":"OVERLAY_CORRESPONDENCE","scope":"matrix","params":{"dimensions":2,"normalization":"symmetric"}}]}`,
		[]string{"overlays.0.*"}}, unitScale},

	// MAT_SEM: only a fit that reaches grad_tol on both architectures
	// (solver fisher, converged, the same iteration count) is pinned —
	// see the file comment for the residual left out. Its
	// residual_covariance (S − Σ̂, a cancellation at the data's own
	// scale: attitude's covariance entries are O(100)) is floored at
	// that scale; every other figure is held at floor 1.
	{requestFigure{"decomp.attitude.sem_path_fisher", "attitude.pulse",
		`{"vectors":[{"name":"v","fields":["rating","complaints","privileges","learning","raises"]}],"matrices":[{"type":"MAT_SEM","vector":"v","name":"m","params":{"model":"f =~ complaints + privileges + learning + raises\nrating ~ f","solver":"fisher","grad_tol":1e-12}}]}`,
		[]string{"matrices.0.primary.*", "matrices.0.auxiliary.estimates.*", "matrices.0.scalars.*"}}, unitScale},
	{requestFigure{"decomp.attitude.sem_path_fisher_residuals", "attitude.pulse",
		`{"vectors":[{"name":"v","fields":["rating","complaints","privileges","learning","raises"]}],"matrices":[{"type":"MAT_SEM","vector":"v","name":"m","params":{"model":"f =~ complaints + privileges + learning + raises\nrating ~ f","solver":"fisher","grad_tol":1e-12}}]}`,
		[]string{"matrices.0.auxiliary.residual_covariance.*"}}, attitudeCovScale},
}

// TestDecomposition_Tolerance computes decompositionFigures and compares
// each with testdata/decomposition.golden within decompRelTol (or
// rewrites it under -update).
func TestDecomposition_Tolerance(t *testing.T) {
	t.Parallel()

	rows := make([]requestFigure, len(decompositionFigures))
	floors := map[string]float64{}
	for i, f := range decompositionFigures {
		rows[i] = f.requestFigure
		floors[f.name] = f.floor
	}
	got := requestFigureValues(t, rows, nil)
	for name, v := range got {
		if math.IsNaN(v) {
			t.Fatalf("figure %q is NaN: pick an input with a finite answer", name)
		}
	}

	body := renderDecimal(got)
	if *update {
		sum := sha256.Sum256(body)
		out := append(append([]byte{}, body...), []byte(hashMarker+hex.EncodeToString(sum[:])+"\n")...)
		if err := os.WriteFile(decompGoldenPath, out, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	raw, err := os.ReadFile(decompGoldenPath)
	if err != nil {
		t.Fatalf("reading %s: %v (regenerate with -update)", decompGoldenPath, err)
	}
	want, _ := splitHash(raw)
	floorOf := func(name string) float64 {
		row, _, _ := strings.Cut(name, ":")
		f, ok := floors[row]
		if !ok {
			t.Fatalf("figure %q belongs to no row", name)
		}
		return f
	}
	diff, worst := diffTolerance(parseDecimal(t, want), got, floorOf)
	t.Logf("worst relative distance %.3g (%s)", worst.rel, worst.name)
	if diff != "" {
		t.Errorf("decomposition drift beyond %g relative (or the corpus changed without -update):\n%s", decompRelTol, diff)
	}
}

// renderDecimal writes one "<name> <%.17g>" line per figure, sorted.
func renderDecimal(figs map[string]float64) []byte {
	names := make([]string, 0, len(figs))
	for n := range figs {
		names = append(names, n)
	}
	sort.Strings(names)
	var b bytes.Buffer
	b.WriteString("# Decomposition tolerance golden (internal/fingerprint). Compared at 1e-12 relative; regenerate with -update on arm64; never hand-edit.\n")
	b.WriteString("# <figure> <decimal>\n")
	for _, n := range names {
		fmt.Fprintf(&b, "%s %s\n", n, strconv.FormatFloat(figs[n], 'g', 17, 64))
	}
	return b.Bytes()
}

func parseDecimal(t *testing.T, body []byte) map[string]float64 {
	t.Helper()
	out := map[string]float64{}
	for _, line := range strings.Split(string(body), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 {
			t.Fatalf("malformed golden line %q", line)
		}
		v, err := strconv.ParseFloat(f[1], 64)
		if err != nil {
			t.Fatalf("malformed golden value in %q: %v", line, err)
		}
		out[f[0]] = v
	}
	return out
}

type worstFigure struct {
	name string
	rel  float64
}

// withinTolerance reports |got − want| ≤ decompRelTol · max(|want|, floor)
// and the distance relative to that scale.
func withinTolerance(want, got, floor float64) (bool, float64) {
	scale := math.Max(math.Abs(want), floor)
	d := math.Abs(got - want)
	if scale == 0 {
		return d == 0, d
	}
	rel := d / scale
	return rel <= decompRelTol, rel
}

// diffTolerance names every figure added, removed or moved beyond the
// tolerance; it also returns the worst scaled distance seen.
func diffTolerance(want, got map[string]float64, floorOf func(string) float64) (string, worstFigure) {
	var lines []string
	var worst worstFigure
	for n, w := range want {
		g, ok := got[n]
		if !ok {
			lines = append(lines, fmt.Sprintf("  removed  %s (golden %.17g)", n, w))
			continue
		}
		okTol, rel := withinTolerance(w, g, floorOf(n))
		if rel > worst.rel {
			worst = worstFigure{n, rel}
		}
		if !okTol {
			lines = append(lines, fmt.Sprintf("  moved    %s: golden %.17g -> got %.17g (scaled distance %.3g > %g)", n, w, g, rel, decompRelTol))
		}
	}
	for n, g := range got {
		if _, ok := want[n]; !ok {
			lines = append(lines, fmt.Sprintf("  added    %s = %.17g", n, g))
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n"), worst
}

// TestDecomposition_ToleranceBoundary: the comparator passes a move
// within 1e-12 relative, fails one beyond it, and applies the floor to
// near-zero figures.
func TestDecomposition_ToleranceBoundary(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		want, got   float64
		floor       float64
		inTolerance bool
	}{
		{"identical", 0.731, 0.731, 1, true},
		{"within relative", 123.4, 123.4 * (1 + 5e-13), 1, true},
		{"beyond relative", 123.4, 123.4 * (1 + 2e-12), 1, false},
		{"near zero within floor", 3e-17, -4e-14, 1, true},
		{"near zero beyond floor", 3e-17, 3e-12, 1, false},
		{"no floor is pure relative", 1e-8, 1e-8 + 1e-19, 0, false},
		{"exact zero, no floor", 0, 0, 0, true},
	}
	for _, c := range cases {
		if ok, _ := withinTolerance(c.want, c.got, c.floor); ok != c.inTolerance {
			t.Errorf("%s: withinTolerance(%g, %g, floor %g) = %v, want %v", c.name, c.want, c.got, c.floor, ok, c.inTolerance)
		}
	}

	want := map[string]float64{"r:a": 2, "r:gone": 1}
	got := map[string]float64{"r:a": 2 * (1 + 1e-11), "r:new": 3}
	d, worst := diffTolerance(want, got, func(string) float64 { return 1 })
	for _, s := range []string{"moved    r:a", "removed  r:gone", "added    r:new"} {
		if !strings.Contains(d, s) {
			t.Errorf("diff missing %q:\n%s", s, d)
		}
	}
	if worst.name != "r:a" {
		t.Errorf("worst = %+v, want r:a", worst)
	}
}
