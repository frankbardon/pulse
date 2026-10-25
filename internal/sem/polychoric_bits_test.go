package sem

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The cross-architecture gates of the two-step ordinal numerics. The
// pinned bits below hold on amd64 and arm64 alike BY CONSTRUCTION: the
// four files are FMA-free (TestPolychoric_NoFusibleProducts, and the
// compiler's own report, TestPolychoric_NoFMAContraction) and call only
// detmath's portable kernels and the correctly rounded math.Sqrt. They
// were produced on darwin/arm64 and checked identical on linux/amd64
// (docker); CI re-runs them on linux/amd64.
//
// Never regenerate them for a refactor — the pin exists to stay green
// across one. A DELIBERATE numerical change prints the new values on
// failure (go test ./internal/sem -run TestPolychoric_GoldenBits -v);
// paste them in and say in the commit why the bits moved.

// twoStepFiles are the FMA-free sources.
var twoStepFiles = []string{"normal.go", "bvn.go", "ordinal.go", "ordinal_scores.go", "ordinal_gamma.go"}

// TestPolychoric_GoldenBits pins scalar results of Φ, Φ⁻¹ and Φ₂ (every
// BVN branch: the 6-, 12- and 20-point Sheppard rules, the |ρ| ≥ 0.925
// expansion either sign, ρ = ±1) and a SHA-256 of every figure of the
// fixtures' two-step statistics and casewise scores.
func TestPolychoric_GoldenBits(t *testing.T) {
	scalars := []struct {
		name string
		got  float64
		want uint64
	}{
		{"Φ(-7.3)", stdNormCDF(-7.3), 0x3d443ff667f464a8},
		{"Φ(1.7)", stdNormCDF(1.7), 0x3fee92eb735d786f},
		{"Φ⁻¹(1e-20)", stdNormQuantile(1e-20), 0xc022865170b43a4b},
		{"Φ⁻¹(0.003)", stdNormQuantile(0.003), 0xc005fb74ce9859bc},
		{"Φ⁻¹(0.3)", stdNormQuantile(0.3), 0xbfe0c7e39582c5fa},
		{"Φ₂(0.3,-1.1;0.2)", BivariateNormalCDF(0.3, -1.1, 0.2), 0x3fb990d26130bca3},
		{"Φ₂(-1.5,0.7;0.6)", BivariateNormalCDF(-1.5, 0.7, 0.6), 0x3fb0e57a0bf1c2d6},
		{"Φ₂(1.2,0.4;0.9)", BivariateNormalCDF(1.2, 0.4, 0.9), 0x3fe4e987493cad78},
		{"Φ₂(-0.5,-0.5;0.95)", BivariateNormalCDF(-0.5, -0.5, 0.95), 0x3fd0e515e71ebdde},
		{"Φ₂(2,-1;-0.97)", BivariateNormalCDF(2, -1, -0.97), 0x3fc1655818e4c09d},
		{"Φ₂(0.1,0.2;0.9999)", BivariateNormalCDF(0.1, 0.2, 0.9999), 0x3fe146450752686b},
		{"Φ₂(-3,2.5;-1)", BivariateNormalCDF(-3, 2.5, -1), 0},
		{"Φ₂(0.4,-0.2;1)", BivariateNormalCDF(0.4, -0.2, 1), 0x3fdaed68af0463c7},
	}
	for _, c := range scalars {
		if b := math.Float64bits(c.got); b != c.want {
			t.Errorf("%s = %.17g (0x%016x), pinned 0x%016x", c.name, c.got, b, c.want)
		}
	}
	digests := map[string]string{
		"brand_twostep":    "0612aabc4bbacd5e04704e0b21887725441c25538d58404ee5166b93f01c98fb",
		"brand_twostep_mg": "cfced81c515428702d7d1653eea58a983e09309a1f44c9cb855986889aa267f1",
		"poly_extreme":     "cc62f64be9b5518dc1ad737ebcbc6a75e78c44b9bfbfa1ff270ecd856248f9e1",
		"poly_binary":      "ccd7789450b76b34d85e41b30cb6d72454efd46b0e3e836b71406f325608ba3a",
		"poly_weighted":    "e3dc3a6eea9a62accdefd85bf9de997d85eeb1a2fea20c5c520f30729acb2160",
	}
	for _, fx := range twoStepFixtures {
		want, ok := digests[fx.ref]
		if !ok {
			continue
		}
		_, in, ss := fitTwoStepFixture(t, fx.ref, fx.data)
		if got := twoStepDigest(t, in, ss); got != want {
			t.Errorf("%s: two-step digest %s, pinned %s", fx.ref, got, want)
		}
	}
}

// twoStepDigest hashes every threshold, moment and correlation, then the
// casewise score vector of every row (and every pair score of the first
// 25 rows), as raw float bits.
func twoStepDigest(t *testing.T, in TwoStepInput, ss []*TwoStepStats) string {
	t.Helper()
	h := sha256.New()
	put := func(v float64) {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], math.Float64bits(v))
		h.Write(b[:])
	}
	for _, s := range ss {
		for _, th := range s.Thresholds {
			for _, v := range th {
				put(v)
			}
		}
		for v := range s.Mean {
			put(s.Mean[v])
			put(s.Var[v])
		}
		for _, p := range s.Pairs {
			put(p.Rho)
		}
	}
	s := ss[0]
	sc := make([]float64, s.Layout().Size)
	for r, row := range in.Rows {
		if len(ss) > 1 {
			break // grouped: the statistics alone
		}
		if err := s.Scores(row, sc); err != nil {
			t.Fatal(err)
		}
		for _, v := range sc {
			put(v)
		}
		if r < 25 {
			for k := range s.Pairs {
				ps := s.PairScores(k, row)
				put(ps.Rho)
				for _, v := range ps.ThI {
					put(v)
				}
				for _, v := range ps.ThJ {
					put(v)
				}
				if !math.IsNaN(ps.VarI) {
					put(ps.VarI)
				}
				if !math.IsNaN(ps.VarJ) {
					put(ps.VarJ)
				}
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TestPolychoric_NoFusibleProducts is a source scan: every non-constant
// float product in the two-step files is the direct operand of a
// float64(...) conversion, the only construct the Go specification
// forbids the compiler to contract into a fused multiply-add.
func TestPolychoric_NoFusibleProducts(t *testing.T) {
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
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}}
	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	if _, err := conf.Check("sem", fset, files, info); err != nil {
		t.Fatal(err)
	}
	scanned := map[string]int{}
	for _, f := range files {
		name := filepath.Base(fset.Position(f.Pos()).Filename)
		if !slices.Contains(twoStepFiles, name) {
			continue
		}
		barriered := map[ast.Expr]bool{}
		var products []*ast.BinaryExpr
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				if id, ok := n.Fun.(*ast.Ident); ok && id.Name == "float64" && len(n.Args) == 1 {
					barriered[ast.Unparen(n.Args[0])] = true
				}
			case *ast.AssignStmt:
				if n.Tok == token.MUL_ASSIGN {
					if b, ok := info.Types[n.Lhs[0]].Type.Underlying().(*types.Basic); ok && b.Info()&types.IsFloat != 0 {
						t.Errorf("%s: float `*=` cannot carry a float64() barrier", fset.Position(n.Pos()))
					}
				}
			case *ast.BinaryExpr:
				if n.Op != token.MUL || info.Types[n].Value != nil {
					return true
				}
				if b, ok := info.Types[n].Type.Underlying().(*types.Basic); ok && b.Info()&types.IsFloat != 0 {
					products = append(products, n)
				}
			}
			return true
		})
		scanned[name] = len(products)
		for _, p := range products {
			if !barriered[p] {
				t.Errorf("%s: float product without a float64() barrier", fset.Position(p.Pos()))
			}
		}
	}
	for _, name := range twoStepFiles {
		if scanned[name] < 10 {
			t.Errorf("%s: only %d float products found — the scan is broken or the file moved", name, scanned[name])
		}
	}
}

// TestPolychoric_NoFMAContraction asks the compiler: internal/sem
// cross-compiled for arm64 with -d=fmahash=vy prints one line per
// multiply-add it contracted, and none may name a two-step file. A
// probe first proves the flag still reports a contraction.
func TestPolychoric_NoFMAContraction(t *testing.T) {
	build := func(dir, importPath string) string {
		cmd := exec.Command("go", "build", "-o", os.DevNull, "-gcflags="+importPath+"=-d=fmahash=vy", ".")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0", "GOFLAGS=")
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Run(); err != nil {
			t.Fatalf("cross-compile for arm64: %v\n%s", err, out.String())
		}
		return out.String()
	}
	probe := t.TempDir()
	if err := os.WriteFile(filepath.Join(probe, "go.mod"), []byte("module fmaprobe\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(probe, "p.go"), []byte("package fmaprobe\n\nfunc F(a, b, c float64) float64 { return a + b*c }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(build(probe, "fmaprobe"), "fmahash triggered") {
		t.Fatal("the -d=fmahash=vy probe reported no contraction for `a + b*c`: the flag changed and this gate is vacuous")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for l := range strings.SplitSeq(build(wd, "github.com/frankbardon/pulse/internal/sem"), "\n") {
		if !strings.Contains(l, "fmahash triggered") {
			continue
		}
		for _, f := range twoStepFiles {
			if strings.Contains(l, "/"+f+":") || strings.HasPrefix(l, f+":") || strings.Contains(l, " "+f+":") {
				t.Errorf("the arm64 compiler contracted a multiply-add in %s: %s", f, l)
			}
		}
	}
}
