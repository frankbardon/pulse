package detmath

import (
	"bytes"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// The FMA-contraction gates.
//
// Go permits `a + b*c` to be contracted into one fused multiply-add,
// even across statements; arm64, ppc64le, s390x, riscv64 and loong64 do
// it, amd64 does not. The only construct that forbids it is an explicit
// float64(...) conversion around the product, and nothing in the type
// system protects one: deleting it still compiles and still passes any
// tolerance-based test. Three gates hold the barriers, each catching
// what the others cannot:
//
//  1. TestDetmath_NoFusibleProducts — a source scan: every non-constant
//     float product is the direct operand of float64(...) (float32(...)
//     for a float32 product). Runs on every
//     architecture and names the line.
//  2. TestDetmath_NoFMAContraction — the compiler's own word: the
//     package is cross-compiled for arm64 with -d=fmahash=vy, which
//     prints one line per contraction it applied, and must print none.
//     Also runs on every host architecture (amd64 CI included).
//  3. TestDetmath_FusedTwinsDiverge — in the style of
//     internal/synth/moments_internal_test.go: `//go:noinline` twins
//     holding the stdlib's pre-barrier source verbatim, compared with
//     the pinned golden bits. On a contracting build the twin must
//     DISAGREE on some golden row (anti-vacuity: the golden table can
//     see a dropped barrier), while TestDetmath_GoldenBits holds the
//     production kernels to the barriered bits. On amd64 the twin is not
//     actually fused, so this gate can only detect on a contracting
//     architecture and logs which case it is in. For Expm1 / Erf /
//     Erfinv the contracted stdlib itself serves as the twin.

// parseKernel parses the package's non-test files.
func parseKernel(t *testing.T) (*token.FileSet, []*ast.File) {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Fatal("no non-test Go files found")
	}
	return fset, files
}

// TestDetmath_ImportBoundary keeps the kernels a stdlib-only leaf, so
// every consumer (the expr environment, FEAT_LOG, the synth row env) can
// import them without an import cycle or an engine dependency.
func TestDetmath_ImportBoundary(t *testing.T) {
	_, files := parseKernel(t)
	for _, f := range files {
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if first, _, _ := strings.Cut(path, "/"); strings.Contains(first, ".") {
				t.Errorf("imports %q: detmath may import only the standard library", path)
			}
		}
	}
}

// TestDetmath_NoFusibleProducts is gate 1: every non-constant float
// multiplication in the kernels, including a `*=` assignment, must be the
// direct operand of a same-width float64(...) / float32(...) conversion.
func TestDetmath_NoFusibleProducts(t *testing.T) {
	fset, files := parseKernel(t)
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}}
	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	if _, err := conf.Check("detmath", fset, files, info); err != nil {
		t.Fatal(err)
	}
	isFloat := func(e ast.Expr) bool {
		b, ok := info.Types[e].Type.Underlying().(*types.Basic)
		return ok && b.Info()&types.IsFloat != 0
	}
	barriered := map[ast.Expr]bool{}
	var products []*ast.BinaryExpr
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				// float64(p) barriers a float64 product, float32(p) a
				// float32 one (the ziggurat's wedge arithmetic); a
				// conversion to the OTHER width is a value change, not a
				// barrier, so it does not count.
				if id, ok := n.Fun.(*ast.Ident); ok && (id.Name == "float64" || id.Name == "float32") && len(n.Args) == 1 {
					arg := ast.Unparen(n.Args[0])
					if b, ok := info.Types[arg].Type.Underlying().(*types.Basic); ok && b.Name() == id.Name {
						barriered[arg] = true
					}
				}
			case *ast.AssignStmt:
				if n.Tok == token.MUL_ASSIGN && isFloat(n.Lhs[0]) {
					t.Errorf("%s: float `*=` cannot carry a float64() barrier; write x = float64(x * y)",
						fset.Position(n.Pos()))
				}
			case *ast.BinaryExpr:
				if n.Op != token.MUL || info.Types[n].Value != nil { // constants fold exactly
					return true
				}
				if isFloat(n) {
					products = append(products, n)
				}
			}
			return true
		})
	}
	if len(products) < 50 {
		t.Fatalf("found only %d float products: the scan is broken", len(products))
	}
	for _, p := range products {
		if !barriered[p] {
			t.Errorf("%s: float product without a same-width float64()/float32() barrier", fset.Position(p.Pos()))
		}
	}
}

// fmahashBuild cross-compiles dir's package for GOARCH=arm64 with the
// compiler's contraction report on, and returns the report lines.
func fmahashBuild(t *testing.T, dir, importPath string) []string {
	t.Helper()
	cmd := exec.Command("go", "build", "-o", os.DevNull,
		"-gcflags="+importPath+"=-d=fmahash=vy", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0", "GOFLAGS=")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("cross-compile for arm64 failed: %v\n%s", err, out.String())
	}
	var lines []string
	for l := range strings.SplitSeq(out.String(), "\n") {
		if strings.Contains(l, "fmahash triggered") {
			lines = append(lines, l)
		}
	}
	return lines
}

// TestDetmath_NoFMAContraction is gate 2. It first proves the probe can
// see a contraction (a scratch module holding `a + b*c` must report
// one — otherwise a renamed compiler flag would make this gate silently
// vacuous), then requires the detmath build to report none.
func TestDetmath_NoFMAContraction(t *testing.T) {
	probe := t.TempDir()
	if err := os.WriteFile(filepath.Join(probe, "go.mod"), []byte("module fmaprobe\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := "package fmaprobe\n\nfunc F(a, b, c float64) float64 { return a + b*c }\n"
	if err := os.WriteFile(filepath.Join(probe, "p.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := fmahashBuild(t, probe, "fmaprobe"); len(got) == 0 {
		t.Fatal("the -d=fmahash=vy probe reported no contraction for `a + b*c` on arm64: " +
			"the compiler flag changed and this gate has gone vacuous")
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got := fmahashBuild(t, wd, "github.com/frankbardon/pulse/internal/detmath"); len(got) > 0 {
		t.Errorf("the arm64 compiler contracted %d multiply-adds in detmath; wrap each product in "+
			"float64(...):\n%s", len(got), strings.Join(got, "\n"))
	}
}

// contractionObservable reports whether this build contracts a
// multiply-add at all.
func contractionObservable() bool {
	// a*b = 1 + 2^-29 + 2^-60 exactly; rounding drops the 2^-60 term,
	// which a fused c + a*b keeps.
	a := 1 + 0x1p-30
	b, c := a, -1.0
	return fusedAdd(a, b, c) != barrieredAdd(a, b, c)
}

//go:noinline
func fusedAdd(a, b, c float64) float64 { return c + a*b }

//go:noinline
func barrieredAdd(a, b, c float64) float64 { return c + float64(a*b) }

// fusedExp is src/math/exp.go's pure-Go exp + expmulti, verbatim
// (pre-barrier).
//
//go:noinline
func fusedExp(x float64) float64 {
	const (
		Ln2Hi     = 6.93147180369123816490e-01
		Ln2Lo     = 1.90821492927058770002e-10
		Log2e     = 1.44269504088896338700e+00
		Overflow  = 7.09782712893383973096e+02
		Underflow = -7.45133219101941108420e+02
		NearZero  = 1.0 / (1 << 28)
		P1        = 1.66666666666666657415e-01
		P2        = -2.77777777770155933842e-03
		P3        = 6.61375632143793436117e-05
		P4        = -1.65339022054652515390e-06
		P5        = 4.13813679705723846039e-08
	)
	switch {
	case math.IsNaN(x):
		return x
	case x > Overflow:
		return math.Inf(1)
	case x < Underflow:
		return 0
	case -NearZero < x && x < NearZero:
		return 1 + x
	}
	var k int
	switch {
	case x < 0:
		k = int(Log2e*x - 0.5)
	case x > 0:
		k = int(Log2e*x + 0.5)
	}
	hi := x - float64(k)*Ln2Hi
	lo := float64(k) * Ln2Lo
	r := hi - lo
	t := r * r
	c := r - t*(P1+t*(P2+t*(P3+t*(P4+t*P5))))
	y := 1 - ((lo - (r*c)/(2-c)) - hi)
	return math.Ldexp(y, k)
}

// fusedLog is src/math/log.go's pure-Go log, verbatim (pre-barrier).
//
//go:noinline
func fusedLog(x float64) float64 {
	const (
		Ln2Hi = 6.93147180369123816490e-01
		Ln2Lo = 1.90821492927058770002e-10
		L1    = 6.666666666666735130e-01
		L2    = 3.999999999940941908e-01
		L3    = 2.857142874366239149e-01
		L4    = 2.222219843214978396e-01
		L5    = 1.818357216161805012e-01
		L6    = 1.531383769920937332e-01
		L7    = 1.479819860511658591e-01
	)
	switch {
	case math.IsNaN(x) || math.IsInf(x, 1):
		return x
	case x < 0:
		return math.NaN()
	case x == 0:
		return math.Inf(-1)
	}
	f1, ki := math.Frexp(x)
	if f1 < math.Sqrt2/2 {
		f1 *= 2
		ki--
	}
	f := f1 - 1
	k := float64(ki)
	s := f / (2 + f)
	s2 := s * s
	s4 := s2 * s2
	t1 := s2 * (L1 + s4*(L3+s4*(L5+s4*L7)))
	t2 := s4 * (L2 + s4*(L4+s4*L6))
	R := t1 + t2
	hfsq := 0.5 * f * f
	return k*Ln2Hi - ((hfsq - (s*(hfsq+R) + k*Ln2Lo)) - f)
}

// fusedSin is src/math/sin.go's pure-Go sin, verbatim (pre-barrier),
// over the barriered trigReduce (integer arithmetic; not under test).
//
//go:noinline
func fusedSin(x float64) float64 {
	const (
		PI4A = 7.85398125648498535156e-1
		PI4B = 3.77489470793079817668e-8
		PI4C = 2.69515142907905952645e-15
	)
	switch {
	case x == 0 || math.IsNaN(x):
		return x
	case math.IsInf(x, 0):
		return math.NaN()
	}
	sign := false
	if x < 0 {
		x = -x
		sign = true
	}
	var j uint64
	var y, z float64
	if x >= reduceThreshold {
		j, z = trigReduce(x)
	} else {
		j = uint64(x * (4 / math.Pi))
		y = float64(j)
		if j&1 == 1 {
			j++
			y++
		}
		j &= 7
		z = ((x - y*PI4A) - y*PI4B) - y*PI4C
	}
	if j > 3 {
		sign = !sign
		j -= 4
	}
	zz := z * z
	if j == 1 || j == 2 {
		y = 1.0 - 0.5*zz + zz*zz*((((((_cos[0]*zz)+_cos[1])*zz+_cos[2])*zz+_cos[3])*zz+_cos[4])*zz+_cos[5])
	} else {
		y = z + z*zz*((((((_sin[0]*zz)+_sin[1])*zz+_sin[2])*zz+_sin[3])*zz+_sin[4])*zz+_sin[5])
	}
	if sign {
		y = -y
	}
	return y
}

// TestDetmath_FusedTwinsDiverge is gate 3 (see the file comment).
func TestDetmath_FusedTwinsDiverge(t *testing.T) {
	obs := contractionObservable()
	t.Logf("GOARCH=%s contracts c+a*b into an FMA: %v", runtime.GOARCH, obs)
	if !obs {
		t.Log("this gate cannot detect a dropped barrier on this architecture; " +
			"TestDetmath_NoFusibleProducts and TestDetmath_NoFMAContraction still do")
	}
	twins := map[string]func(float64) float64{"Exp": fusedExp, "Log": fusedLog, "Sin": fusedSin}
	for name, twin := range twins {
		diverged := 0
		for _, c := range goldenCases {
			if c.fn != name || c.isNaN {
				continue
			}
			if math.Float64bits(twin(c.x)) != c.want {
				diverged++
			}
		}
		if obs && diverged == 0 {
			t.Errorf("%s: the stdlib's unbarriered source reproduces every golden row on a contracting "+
				"build, so the golden table can no longer see a dropped barrier — add an input "+
				"where fusion moves the last bit", name)
		}
		if !obs && diverged != 0 {
			t.Errorf("%s: the unbarriered twin moved %d golden rows on a NON-contracting build; "+
				"the twin is no longer the verbatim port", name, diverged)
		}
		t.Logf("%s: unbarriered twin differs from the pinned bits on %d rows", name, diverged)
	}
	// Expm1 / Erf / Erfinv / Asin need no hand-copied twin: off s390x (the
	// one GOARCH with assembly for them) math.Expm1 / Erf / Erfinv / Asin ARE the
	// unbarriered pure-Go sources, so on a contracting build the stdlib
	// itself is the fused twin. On amd64 it is not fused, and math.Erf
	// also calls the FMA-path exp assembly, so this half runs only where
	// it can detect.
	if !obs || runtime.GOARCH == "s390x" {
		return
	}
	for name, twin := range map[string]func(float64) float64{
		"Expm1": math.Expm1, "Erf": math.Erf, "Erfinv": math.Erfinv, "Asin": math.Asin,
	} {
		diverged := 0
		for _, c := range goldenCases {
			if c.fn == name && !c.isNaN && math.Float64bits(twin(c.x)) != c.want {
				diverged++
			}
		}
		if diverged == 0 {
			t.Errorf("%s: the contracted stdlib reproduces every golden row, so the golden table can "+
				"no longer see a dropped barrier — add an input where fusion moves the last bit", name)
		}
		t.Logf("%s: contracted math.%s differs from the pinned bits on %d rows", name, name, diverged)
	}
}
