// Package fingerprint_test is the cross-architecture fingerprint golden
// (U61 cross-arch-determinism, PRD FR-4): every figure Pulse promises
// is bit-identical on arm64 and amd64 is computed here and its float64
// bits are pinned in testdata/fingerprint.golden. The golden is
// regenerated on the arm64 dev host and CI runs it on linux/amd64, so a
// figure whose bits differ between the two architectures fails CI — the
// cross-arch tripwire.
//
// # Adding a figure
//
// A story that makes a figure deterministic adds ONE entry to the corpus:
//
//   - a direct call: one row in directFigures —
//     {"<pkg>.<Func>(<args>)", func() float64 { return pkg.Func(args) }};
//   - an end-to-end figure through the public library: one row in
//     requestFigures — a name, a cohort from fixtureCohorts (add the
//     in-repo .pulse file there if it is new), the request JSON and the
//     dotted paths of the figures in the response's --json shape, e.g.
//     "data.0.v" or "tests.0.p_value" (or append a path to an existing
//     row). A path ending in ".*" pins every numeric leaf under it, one
//     line each. Pin only figures the story made (or found) identical on
//     both architectures — check on amd64 (below) before committing.
//
// Names are unique; the golden is sorted by name, one line per figure:
// "<name> <hex bits> <%.17g decimal>", so a story's diff shows exactly
// which figures were added or moved. A NaN figure is refused (its sign
// bit differs by architecture); pick an input with a finite answer.
//
// # Regenerating
//
// Only with -update, never by hand (TestGoldensNotHandEdited checks the
// golden-hash footer), and on the arm64 dev host:
//
//	go test -count=1 ./internal/fingerprint -run TestFingerprint_Golden -update
//	go test -count=1 ./internal/fingerprint
//
// The golden must then also pass on linux/amd64 (CI runs it; locally:
// GOOS=linux GOARCH=amd64 go test -c -o fp.amd64 ./internal/fingerprint &&
// docker run --rm --platform linux/amd64 -v "$PWD":/w -w /w/internal/fingerprint golang /w/fp.amd64).
// A pinned figure that moves is a contract event: list it (old -> new
// bits) in the commit body and the migration table; more than the last
// few ulps is a stop-and-ask.
package fingerprint_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/statdist"
	"github.com/frankbardon/pulse/linalg/detmath"
	"github.com/frankbardon/pulse/types"
)

var update = flag.Bool("update", false, "regenerate testdata/fingerprint.golden (arm64 dev host only)")

const (
	goldenPath = "testdata/fingerprint.golden"
	hashMarker = "\n// golden-hash: "
)

// directFigure is one figure computed by a direct function call.
type directFigure struct {
	name string
	fn   func() float64
}

// directFigures: covered kernels called directly.
var directFigures = []directFigure{
	// linalg/detmath (E1-S1: Erfc, Atanh, Tanh; the rest pre-existing).
	{"detmath.Erfc(0.3)", func() float64 { return detmath.Erfc(0.3) }},
	{"detmath.Erfc(1.5)", func() float64 { return detmath.Erfc(1.5) }},
	{"detmath.Erfc(4.2)", func() float64 { return detmath.Erfc(4.2) }},
	{"detmath.Erfc(-2.1)", func() float64 { return detmath.Erfc(-2.1) }},
	{"detmath.Erfc(9.5)", func() float64 { return detmath.Erfc(9.5) }},
	{"detmath.Atanh(0.1)", func() float64 { return detmath.Atanh(0.1) }},
	{"detmath.Atanh(0.62)", func() float64 { return detmath.Atanh(0.62) }},
	{"detmath.Atanh(-0.999)", func() float64 { return detmath.Atanh(-0.999) }},
	{"detmath.Tanh(0.2)", func() float64 { return detmath.Tanh(0.2) }},
	{"detmath.Tanh(1.7)", func() float64 { return detmath.Tanh(1.7) }},
	{"detmath.Tanh(-12.5)", func() float64 { return detmath.Tanh(-12.5) }},
	{"detmath.Exp(3.7)", func() float64 { return detmath.Exp(3.7) }},
	{"detmath.Log(0.37)", func() float64 { return detmath.Log(0.37) }},
	{"detmath.Log1p(1e-5)", func() float64 { return detmath.Log1p(1e-5) }},
	{"detmath.Expm1(-0.3)", func() float64 { return detmath.Expm1(-0.3) }},
	{"detmath.Pow(1.07,12.5)", func() float64 { return detmath.Pow(1.07, 12.5) }},
	{"detmath.Erf(0.8)", func() float64 { return detmath.Erf(0.8) }},
	{"detmath.Erfinv(0.95)", func() float64 { return detmath.Erfinv(0.95) }},
	{"detmath.Lgamma(7.3)", func() float64 { v, _ := detmath.Lgamma(7.3); return v }},
	{"detmath.Sin(2.2)", func() float64 { return detmath.Sin(2.2) }},
	{"detmath.Cos(2.2)", func() float64 { return detmath.Cos(2.2) }},
	{"detmath.Atan2(1.3,-0.4)", func() float64 { return detmath.Atan2(1.3, -0.4) }},

	// internal/statdist (E1-S2: chi-square, NormalQuantileDet; Student-t pre-existing).
	{"statdist.ChiSquareCDF(3.84,1)", func() float64 { return statdist.ChiSquareCDF(3.84, 1) }},
	{"statdist.ChiSquareCDF(12.6,7)", func() float64 { return statdist.ChiSquareCDF(12.6, 7) }},
	{"statdist.ChiSquareCDF(40,30)", func() float64 { return statdist.ChiSquareCDF(40, 30) }},
	{"statdist.ChiSquareSurvival(3.84,1)", func() float64 { return statdist.ChiSquareSurvival(3.84, 1) }},
	{"statdist.ChiSquareSurvival(25.3,12)", func() float64 { return statdist.ChiSquareSurvival(25.3, 12) }},
	{"statdist.ChiSquareSurvival(150,100)", func() float64 { return statdist.ChiSquareSurvival(150, 100) }},
	{"statdist.NormalQuantileDet(0.975)", func() float64 { return statdist.NormalQuantileDet(0.975) }},
	{"statdist.NormalQuantileDet(0.01)", func() float64 { return statdist.NormalQuantileDet(0.01) }},
	{"statdist.NormalQuantileDet(1e-9)", func() float64 { return statdist.NormalQuantileDet(1e-9) }},
	{"statdist.NormalQuantileDet(0.6)", func() float64 { return statdist.NormalQuantileDet(0.6) }},
	{"statdist.StudentTTwoSidedP(2.1,14)", func() float64 { return statdist.StudentTTwoSidedP(2.1, 14) }},
	{"statdist.StudentTQuantile(0.975,9)", func() float64 { return statdist.StudentTQuantile(0.975, 9) }},
}

// fixtureCohorts maps a cohort filename (as named in a request) to its
// in-repo .pulse file, relative to this package.
var fixtureCohorts = map[string]string{
	"mtcars.pulse": "../processing/testdata/reference/fixtures/mtcars.pulse",
}

// requestFigure is one Process request and the figures read from its
// response. Each path names one figure "<name>:<path>"; a path ending in
// ".*" pins every numeric leaf below it, "<name>:<path>.<leaf>".
type requestFigure struct {
	name    string   // figure-name prefix
	cohort  string   // a fixtureCohorts key
	request string   // the request JSON (its cohort is filled in)
	paths   []string // dotted paths into the response's --json shape
}

// requestFigures: end-to-end figures through pulse.Process. Only figures
// that already match on arm64 and amd64 are pinned; a figure a later
// story makes deterministic is added by that story (append its path).
var requestFigures = []requestFigure{
	// Binned reproducible sums (internal/reprosum): bit-exact by construction.
	{"process.mtcars.sum_mpg", "mtcars.pulse",
		`{"aggregations":[{"type":"AGG_SUM","field":"mpg","label":"v"}]}`,
		[]string{"data.0.v"}},
	{"process.mtcars.avg_wt", "mtcars.pulse",
		`{"aggregations":[{"type":"AGG_AVERAGE","field":"wt","label":"v"}]}`,
		[]string{"data.0.v"}},
	{"process.mtcars.avg_qsec_by_cyl", "mtcars.pulse",
		`{"groupers":[{"type":"GROUP_CATEGORY","field":"cyl","label":"g"}],"aggregations":[{"type":"AGG_AVERAGE","field":"qsec","label":"v"}]}`,
		[]string{"data.*"}},
	{"process.mtcars.stddev_mpg", "mtcars.pulse",
		`{"aggregations":[{"type":"AGG_STDDEV","field":"mpg","label":"v"}]}`,
		[]string{"data.0.v"}},
	// TEST_T one-sample on the Student-t chain (statdist, FMA-gated).
	{"process.mtcars.ttest_mpg_mu20", "mtcars.pulse",
		`{"tests":[{"type":"TEST_T","field":"mpg","params":{"mu":20},"label":"t"}]}`,
		[]string{"tests.0.statistic", "tests.0.p_value", "tests.0.details.ci_low", "tests.0.details.ci_high", "tests.0.details.effect_size.cohens_d"}},
	// TEST_PEARSON_R r and p (its Fisher-z CI is not yet covered: E2-S3).
	{"process.mtcars.pearson_mpg_wt", "mtcars.pulse",
		`{"tests":[{"type":"TEST_PEARSON_R","field":"mpg","field2":"wt","label":"t"}]}`,
		[]string{"tests.0.statistic", "tests.0.p_value"}},
	// MAT_CORRELATION primary on the FMA-free CoMoment path (its
	// determinant runs a gonum decomposition: not pinned).
	{"process.mtcars.corr_mpg_wt_hp", "mtcars.pulse",
		`{"vectors":[{"name":"v","fields":["mpg","wt","hp"]}],"matrices":[{"type":"MAT_CORRELATION","vector":"v","name":"m"}]}`,
		[]string{"matrices.0.primary.values.*"}},
}

// TestFingerprint_Golden computes the corpus and compares it, line by
// line, with testdata/fingerprint.golden (or rewrites it under -update).
func TestFingerprint_Golden(t *testing.T) {
	t.Parallel()

	figs := map[string]float64{}
	add := func(name string, v float64) {
		t.Helper()
		if _, dup := figs[name]; dup {
			t.Fatalf("duplicate figure name %q", name)
		}
		if math.IsNaN(v) {
			t.Fatalf("figure %q is NaN: its sign bit is architecture-dependent; pick an input with a finite answer", name)
		}
		figs[name] = v
	}
	for _, f := range directFigures {
		add(f.name, f.fn())
	}
	for name, v := range requestFigureValues(t) {
		add(name, v)
	}

	got := render(figs)
	if *update {
		sum := sha256.Sum256(got)
		out := append(append([]byte{}, got...), []byte(hashMarker+hex.EncodeToString(sum[:])+"\n")...)
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, out, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}

	raw, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("reading %s: %v (regenerate with -update)", goldenPath, err)
	}
	want, _ := splitHash(raw)
	if diff := diffLines(parseGolden(t, want), figs); diff != "" {
		t.Errorf("fingerprint drift (a pinned figure changed bits, or the corpus changed without -update):\n%s", diff)
	}
}

// requestFigureValues runs every requestFigures entry through one Pulse
// instance over an in-memory copy of the fixture cohorts.
func requestFigureValues(t *testing.T) map[string]float64 {
	t.Helper()
	mem := afero.NewMemMapFs()
	for name, src := range fixtureCohorts {
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("fixture %s: %v", src, err)
		}
		if err := afero.WriteFile(mem, name, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, err := pulse.New(pulse.Options{FS: mem})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	out := map[string]float64{}
	for _, rf := range requestFigures {
		if _, ok := fixtureCohorts[rf.cohort]; !ok {
			t.Fatalf("%s: cohort %q is not in fixtureCohorts", rf.name, rf.cohort)
		}
		var req types.Request
		if err := json.Unmarshal([]byte(rf.request), &req); err != nil {
			t.Fatalf("%s: decoding request: %v", rf.name, err)
		}
		req.Cohort = &types.Cohort{Filename: rf.cohort}
		resp, err := p.Process(context.Background(), &req)
		if err != nil {
			t.Fatalf("%s: Process: %v", rf.name, err)
		}
		wire, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("%s: marshal response: %v", rf.name, err)
		}
		dec := json.NewDecoder(bytes.NewReader(wire))
		dec.UseNumber()
		var doc any
		if err := dec.Decode(&doc); err != nil {
			t.Fatalf("%s: decode response: %v", rf.name, err)
		}
		if len(rf.paths) == 0 {
			t.Fatalf("%s: no paths", rf.name)
		}
		for _, path := range rf.paths {
			leaves := extract(t, rf.name+":"+strings.TrimSuffix(path, ".*"), doc, path)
			if len(leaves) == 0 {
				t.Fatalf("%s: path %q matched no numeric figure in %s", rf.name, path, wire)
			}
			for k, v := range leaves {
				if _, dup := out[k]; dup {
					t.Fatalf("duplicate figure name %q", k)
				}
				out[k] = v
			}
		}
	}
	return out
}

// extract walks a dotted path; a trailing ".*" collects every numeric
// leaf below the node, named name + "." + its sub-path.
func extract(t *testing.T, name string, doc any, path string) map[string]float64 {
	t.Helper()
	segs := strings.Split(path, ".")
	wild := segs[len(segs)-1] == "*"
	if wild {
		segs = segs[:len(segs)-1]
	}
	node := doc
	for _, s := range segs {
		switch n := node.(type) {
		case map[string]any:
			v, ok := n[s]
			if !ok {
				t.Fatalf("%s: path %q: no key %q", name, path, s)
			}
			node = v
		case []any:
			i, err := strconv.Atoi(s)
			if err != nil || i < 0 || i >= len(n) {
				t.Fatalf("%s: path %q: bad index %q (len %d)", name, path, s, len(n))
			}
			node = n[i]
		default:
			t.Fatalf("%s: path %q: cannot descend into %T at %q", name, path, node, s)
		}
	}
	out := map[string]float64{}
	if !wild {
		num, ok := node.(json.Number)
		if !ok {
			t.Fatalf("%s: path %q is %T (%v), not a number", name, path, node, node)
		}
		out[name] = mustFloat(t, name, num)
		return out
	}
	var walk func(prefix string, n any)
	walk = func(prefix string, n any) {
		switch v := n.(type) {
		case map[string]any:
			for k, c := range v {
				walk(prefix+"."+k, c)
			}
		case []any:
			for i, c := range v {
				walk(prefix+"."+strconv.Itoa(i), c)
			}
		case json.Number:
			out[prefix] = mustFloat(t, prefix, v)
		}
	}
	walk(name, node)
	return out
}

func mustFloat(t *testing.T, name string, n json.Number) float64 {
	t.Helper()
	// Go's encoder writes the shortest decimal that round-trips, so
	// ParseFloat recovers the exact bits.
	f, err := strconv.ParseFloat(n.String(), 64)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return f
}

// render writes one line per figure, sorted by name.
func render(figs map[string]float64) []byte {
	names := make([]string, 0, len(figs))
	for n := range figs {
		names = append(names, n)
	}
	sort.Strings(names)
	var b bytes.Buffer
	b.WriteString("# Cross-architecture fingerprint (internal/fingerprint). Regenerate with -update on arm64; never hand-edit.\n")
	b.WriteString("# <figure> <float64 bits> <decimal>\n")
	for _, n := range names {
		v := figs[n]
		fmt.Fprintf(&b, "%s 0x%016x %s\n", n, math.Float64bits(v), strconv.FormatFloat(v, 'g', 17, 64))
	}
	return b.Bytes()
}

// parseGolden reads the golden body into name -> bits.
func parseGolden(t *testing.T, body []byte) map[string]uint64 {
	t.Helper()
	out := map[string]uint64{}
	for _, line := range strings.Split(string(body), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 3 {
			t.Fatalf("malformed golden line %q", line)
		}
		bits, err := strconv.ParseUint(strings.TrimPrefix(f[1], "0x"), 16, 64)
		if err != nil {
			t.Fatalf("malformed golden bits in %q: %v", line, err)
		}
		out[f[0]] = bits
	}
	return out
}

// diffLines names every figure added, removed or moved (with its ulp
// distance); empty means identical.
func diffLines(want map[string]uint64, got map[string]float64) string {
	var lines []string
	for n, wb := range want {
		g, ok := got[n]
		if !ok {
			lines = append(lines, fmt.Sprintf("  removed  %s (golden 0x%016x)", n, wb))
			continue
		}
		gb := math.Float64bits(g)
		if gb != wb {
			w := math.Float64frombits(wb)
			lines = append(lines, fmt.Sprintf("  moved    %s: golden 0x%016x (%.17g) -> got 0x%016x (%.17g), %d ulp",
				n, wb, w, gb, g, ulps(wb, gb)))
		}
	}
	for n, g := range got {
		if _, ok := want[n]; !ok {
			lines = append(lines, fmt.Sprintf("  added    %s = 0x%016x (%.17g)", n, math.Float64bits(g), g))
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// ulps is the distance between two finite float64s in units in the last
// place (sign-magnitude mapped onto a monotone integer line).
func ulps(a, b uint64) uint64 {
	ord := func(u uint64) int64 {
		if u>>63 == 1 {
			return -int64(u &^ (1 << 63))
		}
		return int64(u)
	}
	d := ord(a) - ord(b)
	if d < 0 {
		d = -d
	}
	return uint64(d)
}

func splitHash(content []byte) (body []byte, hash string) {
	s := string(content)
	idx := strings.LastIndex(s, hashMarker)
	if idx < 0 {
		return content, ""
	}
	return []byte(s[:idx]), strings.TrimSpace(s[idx+len(hashMarker):])
}

// TestGoldensNotHandEdited verifies every golden under testdata/ ends
// with a valid `// golden-hash:` footer. Mirrors the descriptor gate
// (listed by name in CLAUDE.md); regenerate with -update, never by hand.
func TestGoldensNotHandEdited(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatalf("reading testdata: %v", err)
	}
	seen := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		seen++
		content, err := os.ReadFile(filepath.Join("testdata", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		body, stored := splitHash(content)
		if stored == "" {
			t.Errorf("%s: no golden-hash found (file may have been hand-edited)", e.Name())
			continue
		}
		sum := sha256.Sum256(body)
		if hex.EncodeToString(sum[:]) != stored {
			t.Errorf("%s: hash mismatch (file may have been hand-edited; regenerate with -update)", e.Name())
		}
	}
	if seen == 0 {
		t.Fatal("no goldens under testdata/")
	}
}

// TestFingerprint_DiffNamesMovedFigure: the comparator reports a
// one-bit move with its ulp distance, and an added and removed figure.
func TestFingerprint_DiffNamesMovedFigure(t *testing.T) {
	t.Parallel()

	v := 0.1
	want := map[string]uint64{"a": math.Float64bits(v), "gone": 1}
	got := map[string]float64{"a": math.Nextafter(v, 1), "new": 2}
	d := diffLines(want, got)
	for _, s := range []string{"moved    a:", "1 ulp", "removed  gone", "added    new"} {
		if !strings.Contains(d, s) {
			t.Errorf("diff missing %q:\n%s", s, d)
		}
	}
	if diffLines(map[string]uint64{"a": math.Float64bits(v)}, map[string]float64{"a": v}) != "" {
		t.Error("identical corpora reported a diff")
	}
	if ulps(math.Float64bits(math.Copysign(0, -1)), math.Float64bits(math.SmallestNonzeroFloat64)) != 1 {
		t.Error("ulps across signed zero")
	}
}
