package synth

import (
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/detmath"
)

// The standard normal and exponential variates every normal, lognormal,
// exponential, mixture, copula, model, residual and vector draw consumes
// come from internal/detmath's ziggurat port, never from math/rand/v2's
// (*rand.Rand).NormFloat64 / ExpFloat64, whose rare tail branch calls the
// machine-specific math.Log and (on arm64) fuses `rn + x`. The port pulls
// the RNG exactly as the stdlib does, so the stream is unchanged; only a
// tail draw's last bits are, and they are now the same on every machine.

// tailSource opens every draw on the ziggurat's tail: the first Uint64 is
// word (base strip, |j| beyond kn[0] / ke[0]), the rest come from a PCG.
type tailSource struct {
	word   uint64
	opened bool
	pcg    *rand.PCG
}

func (s *tailSource) Uint64() uint64 {
	if !s.opened {
		s.opened = true
		return s.word
	}
	return s.pcg.Uint64()
}

func tailRand(k int, word uint64) *rand.Rand {
	return rand.New(&tailSource{word: word, pcg: rand.NewPCG(uint64(k), 99)})
}

// zigTailDraws forced tail draws per variate: on arm64 the stdlib's
// tail disagrees with the port on about 2% of normal and 0.07% of
// exponential tail draws, so a site reverted to the stdlib fails here
// locally (the falsification shape of TestSynthGeneration_DrawsRunOnDetmath;
// on amd64, where the stdlib tail is uncontracted, a revert may pass, and
// TestSynthZiggurat_NoStdlibVariates catches it on every architecture).
const zigTailDraws = 50_000

// TestSynthGeneration_VariatesRunOnZigguratPort: the normal and the
// exponential sampler's every draw is the port's variate, bit for bit,
// on draws forced onto the tail.
func TestSynthGeneration_VariatesRunOnZigguratPort(t *testing.T) {
	cases := []struct {
		name   string
		fs     FieldSpec
		words  [2]uint64
		port   func(*rand.Rand) float64
		stdlib func(*rand.Rand) float64
	}{
		{
			name: "normal",
			fs: FieldSpec{Name: "x", Type: "f64", Distribution: DistNormal,
				Params: map[string]any{"mean": 50.0, "std": 10.0}},
			words: [2]uint64{0x7fffffff, 0x80000001},
			port:  func(r *rand.Rand) float64 { return 50 + float64(detmath.NormFloat64(r)*10) },
			stdlib: func(r *rand.Rand) float64 {
				return 50 + float64(r.NormFloat64()*10)
			},
		},
		{
			name: "exponential",
			fs: FieldSpec{Name: "x", Type: "f64", Distribution: DistExponential,
				Params: map[string]any{"lambda": 0.5}},
			words:  [2]uint64{0xffffffff, 0xffffffff},
			port:   func(r *rand.Rand) float64 { return detmath.ExpFloat64(r) / 0.5 },
			stdlib: func(r *rand.Rand) float64 { return r.ExpFloat64() / 0.5 },
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := mustSampler(t, c.fs)
			differ := 0
			for k := range zigTailDraws {
				w := c.words[k%2]
				got, _ := s.next(tailRand(k, w))
				want := c.port(tailRand(k, w))
				if !sameBits(got.(float64), want) {
					t.Fatalf("tail draw %d: %s = %x, want the port's %x", k, c.name,
						math.Float64bits(got.(float64)), math.Float64bits(want))
				}
				if !sameBits(want, c.stdlib(tailRand(k, w))) {
					differ++
				}
			}
			t.Logf("%s: the stdlib variate differs from the port on %d of %d forced tail draws", c.name, differ, zigTailDraws)
		})
	}
}

// TestSynthZiggurat_NoStdlibVariates bans `.NormFloat64(` / `.ExpFloat64(`
// on anything but the detmath package in non-test synth code, so the
// machine-sensitive stdlib variates cannot come back at a new site.
func TestSynthZiggurat_NoStdlibVariates(t *testing.T) {
	checked := 0
	for _, dir := range []string{".", filepath.Join("..", "..", "synth")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(dir, name)
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			checked++
			ast.Inspect(f, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok || (sel.Sel.Name != "NormFloat64" && sel.Sel.Name != "ExpFloat64") {
					return true
				}
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "detmath" {
					return true
				}
				t.Errorf("%s: %s is math/rand/v2's machine-sensitive variate; call detmath.%s(rng)",
					fset.Position(sel.Pos()), sel.Sel.Name, sel.Sel.Name)
				return true
			})
		}
	}
	if checked < 20 {
		t.Fatalf("scanned only %d files: the walk is broken", checked)
	}
}
