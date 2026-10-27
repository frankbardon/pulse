package detmath

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"go/ast"
	"go/parser"
	"go/token"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The ziggurat port's contract has three halves:
//
//  1. RNG consumption: NormFloat64(r) / ExpFloat64(r) pull from r the
//     same Uint64 / Float64 calls, in the same order and number, as
//     (*rand.Rand).NormFloat64 / ExpFloat64, so seeding and every later
//     draw's stream position are unchanged by the port.
//  2. Values: a draw is bit-identical to the stdlib's on the fast path
//     and the wedge (the wedge only DECIDES; its accepted value is the
//     fast-path product), and differs only in the tail, where the stdlib
//     calls the machine-specific math.Log and arm64 fuses `rn + x`.
//  3. Portability: TestDetmath_ZigguratGoldenBits pins exact bits for a
//     fixed seed, including tail draws, on every architecture.

// countingSource is a rand.Source that counts its Uint64 calls and
// serves a scripted prefix before falling through to a PCG.
type countingSource struct {
	script []uint64
	pcg    *rand.PCG
	n      int
}

func (c *countingSource) Uint64() uint64 {
	c.n++
	if len(c.script) > 0 {
		v := c.script[0]
		c.script = c.script[1:]
		return v
	}
	return c.pcg.Uint64()
}

func newCounting(seed uint64, script ...uint64) *countingSource {
	return &countingSource{script: script, pcg: rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)}
}

// f64Word is the Uint64 that makes (*rand.Rand).Float64 return f exactly
// (f must be a multiple of 2^-53 in [0, 1)).
func f64Word(f float64) uint64 { return uint64(f * (1 << 53)) }

// zigDraw names one variate, as the port and the stdlib method.
type zigDraw struct {
	name   string
	port   func(*rand.Rand) float64
	stdlib func(*rand.Rand) float64
}

var zigDraws = []zigDraw{
	{"NormFloat64", NormFloat64, (*rand.Rand).NormFloat64},
	{"ExpFloat64", ExpFloat64, (*rand.Rand).ExpFloat64},
}

// ulpDistance is |a - b| in units in the last place (a, b finite, same sign).
func ulpDistance(a, b float64) uint64 {
	ua, ub := math.Float64bits(math.Abs(a)), math.Float64bits(math.Abs(b))
	if ua > ub {
		return ua - ub
	}
	return ub - ua
}

const zigParityDraws = 1_000_000

// TestDetmath_ZigguratStreamParity: over a long seeded run the port and
// the stdlib stay at the same RNG position after EVERY draw, agree bit
// for bit on every draw that took one Uint64 (the fast path), and differ
// at most by a few ulps — only ever on a multi-pull draw.
func TestDetmath_ZigguratStreamParity(t *testing.T) {
	t.Parallel()

	for _, d := range zigDraws {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()

			ps, ss := newCounting(7), newCounting(7)
			pr, sr := rand.New(ps), rand.New(ss)
			var multi, tail, differ int
			var maxULP uint64
			for k := range zigParityDraws {
				p0, s0 := ps.n, ss.n
				got, want := d.port(pr), d.stdlib(sr)
				pulls := ps.n - p0
				if pulls != ss.n-s0 {
					t.Fatalf("draw %d: port pulled %d Uint64s, stdlib %d", k, pulls, ss.n-s0)
				}
				if pulls > 1 {
					multi++
				}
				// The tail is the only branch whose RESULT is computed
				// away from the fast-path product: |x| beyond the base
				// strip's edge.
				edge := zigNormR
				if d.name == "ExpFloat64" {
					edge = zigExpR
				}
				if math.Abs(want) >= edge {
					tail++
				}
				if math.Float64bits(got) == math.Float64bits(want) {
					continue
				}
				differ++
				if pulls == 1 || math.Abs(want) < edge {
					t.Fatalf("draw %d: %v (port) != %v (stdlib) off the tail branch (%d pulls)", k, got, want, pulls)
				}
				if u := ulpDistance(got, want); u > maxULP {
					maxULP = u
				}
			}
			// One last pull from each source proves the states agree.
			if a, b := pr.Uint64(), sr.Uint64(); a != b {
				t.Fatalf("after %d draws the sources diverged: %#x != %#x", zigParityDraws, a, b)
			}
			if tail == 0 || multi == 0 {
				t.Fatalf("%d draws reached the tail %d times and pulled more than once %d times: "+
					"the run is too short to exercise the port", zigParityDraws, tail, multi)
			}
			if maxULP > 4 {
				t.Errorf("a tail draw differs from the stdlib by %d ulps (more than math.Log's spread)", maxULP)
			}
			t.Logf("%d draws: %d multi-pull, %d tail, %d tail values differ from the stdlib (max %d ulp), "+
				"stream positions identical throughout", zigParityDraws, multi, tail, differ, maxULP)
		})
	}
}

// zigScripts force the rare branches: each Uint64 sequence opens a single
// draw. Normal: bits 32..38 pick strip i, the low 32 bits are j.
var zigScripts = []struct {
	name   string
	draw   string
	script []uint64
	pulls  int
}{
	// Base strip, j beyond kn[0]: the tail, rejected once (2y < x² for
	// x = -log(2^-20)/rn) and then accepted.
	{"norm tail positive", "NormFloat64", []uint64{0x7fffffff,
		f64Word(0x1p-20), f64Word(0.5), f64Word(0.5), f64Word(0.5)}, 5},
	{"norm tail negative", "NormFloat64", []uint64{0x80000001,
		f64Word(0.25), f64Word(0.75)}, 3},
	// Strip 5: j at the strip's outer edge with u near 1 rejects in the
	// wedge and falls to a fast-path draw (strip 2, j = 1); j = kn[5], the
	// wedge's inner edge, with u = 0 accepts.
	{"norm wedge reject", "NormFloat64", []uint64{5<<32 | 0x7fffffff, f64Word(1 - 0x1p-53), 2<<32 | 1}, 3},
	{"norm wedge accept", "NormFloat64", []uint64{5<<32 | uint64(zigNormK[5]), 0}, 2},
	// Exponential: bits 32..39 pick i, the low 32 bits are j.
	{"exp tail", "ExpFloat64", []uint64{0xffffffff, f64Word(0.375)}, 2},
	{"exp wedge reject", "ExpFloat64", []uint64{9<<32 | 0xffffffff, f64Word(1 - 0x1p-53), 2<<32 | 1}, 3},
	{"exp wedge accept", "ExpFloat64", []uint64{9<<32 | uint64(zigExpK[9]), 0}, 2},
}

func zigFor(name string) zigDraw {
	for _, d := range zigDraws {
		if d.name == name {
			return d
		}
	}
	panic(name)
}

// TestDetmath_ZigguratConsumption: on crafted sources that force the
// tail (with an inner rejection) and both wedge outcomes, the port pulls
// exactly the stdlib's Uint64s, and the source is in the same state
// afterwards.
func TestDetmath_ZigguratConsumption(t *testing.T) {
	t.Parallel()

	for _, sc := range zigScripts {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()

			d := zigFor(sc.draw)
			ps, ss := newCounting(3, sc.script...), newCounting(3, sc.script...)
			got, want := d.port(rand.New(ps)), d.stdlib(rand.New(ss))
			if ps.n != sc.pulls || ss.n != sc.pulls {
				t.Fatalf("pulls: port %d, stdlib %d, script expects %d", ps.n, ss.n, sc.pulls)
			}
			if ps.pcg.Uint64() != ss.pcg.Uint64() {
				t.Fatal("source states diverged after the draw")
			}
			if u := ulpDistance(got, want); u > 4 {
				t.Fatalf("%v (port) vs %v (stdlib): %d ulps", got, want, u)
			}
		})
	}
}

// zigGolden pins the port's exact bits. The first rows are the crafted
// branch draws of zigScripts (tail, wedge accept, the fast-path draw a
// wedge reject falls to); streamHash is an FNV-1a over the bits of
// zigParityDraws draws from seed 7 — 594 normal and 463
// exponential tail draws among them. Produced on darwin/arm64 with no
// contraction reported and checked identical on linux/amd64 (docker); CI
// re-runs it on linux/amd64.
var zigGolden = map[string]uint64{
	"norm tail positive": 0x400d26d5f680f581,
	"norm tail negative": 0xc00ec32fa594bf1a,
	"norm wedge reject":  0x3de7394920000000,
	"norm wedge accept":  0x3fde8e577894f65e,
	"exp tail":           0x40215b1bd538a83d,
	"exp wedge reject":   0x3dbad6b240000000,
	"exp wedge accept":   0x3fd03bf045387d55,
	"NormFloat64 stream": 0x58cb104576fe7beb,
	"ExpFloat64 stream":  0x6314fb4888e5935e,
}

func zigStreamHash(d zigDraw, n int) uint64 {
	r := rand.New(newCounting(7))
	h := fnv.New64a()
	var b [8]byte
	for range n {
		binary.LittleEndian.PutUint64(b[:], math.Float64bits(d.port(r)))
		h.Write(b[:])
	}
	return h.Sum64()
}

// TestDetmath_ZigguratGoldenBits holds the port to exact bits on every
// architecture, tail draws included.
func TestDetmath_ZigguratGoldenBits(t *testing.T) {
	t.Parallel()

	for _, sc := range zigScripts {
		got := math.Float64bits(zigFor(sc.draw).port(rand.New(newCounting(3, sc.script...))))
		if got != zigGolden[sc.name] {
			t.Errorf("%s: got %#016x, want %#016x", sc.name, got, zigGolden[sc.name])
		}
	}
	for _, d := range zigDraws {
		if got := zigStreamHash(d, zigParityDraws); got != zigGolden[d.name+" stream"] {
			t.Errorf("%s stream: got %#016x, want %#016x", d.name, got, zigGolden[d.name+" stream"])
		}
	}
}

// zigTailSweep is how many forced tail draws TestDetmath_ZigguratTailSweep
// takes per variate: enough that math.Log disagrees with the port's Log
// on some of them on arm64 (about 1 in 500 inputs) and on amd64 (its
// assembly), so routing the tail back to math.Log moves the pinned hash.
const zigTailSweep = 50_000

// TestDetmath_ZigguratTailSweep forces the tail on every draw — the
// opening Uint64 picks the base strip with |j| beyond kn[0] (ke[0]), and
// draw k's Float64s come from seed k — and pins the exact bits. Each draw
// still agrees with the stdlib's to within math.Log's spread and pulls
// what it pulls.
func TestDetmath_ZigguratTailSweep(t *testing.T) {
	t.Parallel()

	want := map[string]uint64{"NormFloat64": 0xae34d5afd185526e, "ExpFloat64": 0x2ae47dd37cf9bcb1}
	for _, d := range zigDraws {
		h := fnv.New64a()
		var b [8]byte
		differ := 0
		for k := range zigTailSweep {
			word := uint64(0x7fffffff)
			if k%2 == 1 {
				word = 0x80000001
			}
			if d.name == "ExpFloat64" {
				word = 0xffffffff
			}
			ps, ss := newCounting(uint64(k), word), newCounting(uint64(k), word)
			got, ref := d.port(rand.New(ps)), d.stdlib(rand.New(ss))
			if ps.n != ss.n {
				t.Fatalf("%s tail draw %d: port pulled %d, stdlib %d", d.name, k, ps.n, ss.n)
			}
			if math.Float64bits(got) != math.Float64bits(ref) {
				differ++
				if u := ulpDistance(got, ref); u > 4 {
					t.Fatalf("%s tail draw %d: %v vs stdlib %v (%d ulps)", d.name, k, got, ref, u)
				}
			}
			binary.LittleEndian.PutUint64(b[:], math.Float64bits(got))
			h.Write(b[:])
		}
		if got := h.Sum64(); got != want[d.name] {
			t.Errorf("%s tail sweep: got %#016x, want %#016x", d.name, got, want[d.name])
		}
		t.Logf("%s: %d of %d forced tail draws differ from the stdlib", d.name, differ, zigTailSweep)
	}
}

// TestDetmath_ZigguratCallsPortableKernels: ziggurat.go imports no
// "math", so its tail and wedge can only reach this package's Log / Exp.
// The wedge's Exp feeds a float32 comparison, where a one-ulp float64
// difference almost never survives the rounding, so no value sweep can
// see it reverted to math.Exp; this source check can.
func TestDetmath_ZigguratCallsPortableKernels(t *testing.T) {
	t.Parallel()

	f, err := parser.ParseFile(token.NewFileSet(), "ziggurat.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range f.Imports {
		if path, _ := strconv.Unquote(imp.Path.Value); path == "math" {
			t.Error(`ziggurat.go imports "math": its Log / Exp calls must be this package's portable kernels`)
		}
	}
}

// zigTablesSHA256 pins the six tables' bytes, so an edit to any of the
// ~1,150 values fails here even where GOROOT's source is unreadable.
const zigTablesSHA256 = "168df72b9f81a1d6804cacfd654148e12c278a163b7c871c106bb96ed8b0a839"

func zigTablesDigest() string {
	h := sha256.New()
	for _, k := range [][]uint32{zigNormK[:], zigExpK[:]} {
		_ = binary.Write(h, binary.LittleEndian, k)
	}
	for _, f := range [][]float32{zigNormW[:], zigNormF[:], zigExpW[:], zigExpF[:]} {
		_ = binary.Write(h, binary.LittleEndian, f)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// stdlibTable reads one composite-literal table from GOROOT's
// src/math/rand/v2/<file> as uint64 words (float32 values as their bits).
func stdlibTable(t *testing.T, file, name string) []uint64 {
	t.Helper()
	out, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatalf("go env GOROOT: %v", err)
	}
	path := filepath.Join(strings.TrimSpace(string(out)), "src", "math", "rand", "v2", file)
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, decl := range f.Decls {
		g, ok := decl.(*ast.GenDecl)
		if !ok || g.Tok != token.VAR {
			continue
		}
		for _, spec := range g.Specs {
			vs := spec.(*ast.ValueSpec)
			if vs.Names[0].Name != name {
				continue
			}
			lit := vs.Values[0].(*ast.CompositeLit)
			isFloat := lit.Type.(*ast.ArrayType).Elt.(*ast.Ident).Name == "float32"
			var words []uint64
			for _, e := range lit.Elts {
				s := e.(*ast.BasicLit).Value
				if isFloat {
					v, err := strconv.ParseFloat(s, 32)
					if err != nil {
						t.Fatalf("%s[%d] = %s: %v", name, len(words), s, err)
					}
					words = append(words, uint64(math.Float32bits(float32(v))))
					continue
				}
				v, err := strconv.ParseUint(s, 0, 32)
				if err != nil {
					t.Fatalf("%s[%d] = %s: %v", name, len(words), s, err)
				}
				words = append(words, v)
			}
			return words
		}
	}
	t.Fatalf("%s: no var %s", path, name)
	return nil
}

// TestDetmath_ZigguratTablesMatchStdlib: the copied tables equal the
// stdlib's, value for value, re-read from the toolchain's own source, and
// their bytes match the pinned digest.
func TestDetmath_ZigguratTablesMatchStdlib(t *testing.T) {
	t.Parallel()

	u32 := func(a []uint32) []uint64 {
		w := make([]uint64, len(a))
		for i, v := range a {
			w[i] = uint64(v)
		}
		return w
	}
	f32 := func(a []float32) []uint64 {
		w := make([]uint64, len(a))
		for i, v := range a {
			w[i] = uint64(math.Float32bits(v))
		}
		return w
	}
	for _, c := range []struct {
		file, name string
		ours       []uint64
	}{
		{"normal.go", "kn", u32(zigNormK[:])}, {"normal.go", "wn", f32(zigNormW[:])},
		{"normal.go", "fn", f32(zigNormF[:])}, {"exp.go", "ke", u32(zigExpK[:])},
		{"exp.go", "we", f32(zigExpW[:])}, {"exp.go", "fe", f32(zigExpF[:])},
	} {
		theirs := stdlibTable(t, c.file, c.name)
		if len(theirs) != len(c.ours) {
			t.Errorf("%s: %d entries, stdlib has %d", c.name, len(c.ours), len(theirs))
			continue
		}
		for i := range theirs {
			if theirs[i] != c.ours[i] {
				t.Errorf("%s[%d]: %#x, stdlib %#x", c.name, i, c.ours[i], theirs[i])
			}
		}
	}
	if got := zigTablesDigest(); got != zigTablesSHA256 {
		t.Errorf("table digest %s, pinned %s", got, zigTablesSHA256)
	}
}

// TestDetmath_ZigguratStdlibFusesTail is the anti-vacuity half of the
// parity test, in the style of TestDetmath_FusedTwinsDiverge: on a
// contracting build the stdlib's tail (`rn + x`, fused) must leave the
// port's barriered bits on some draw, or the parity run could no longer
// see a dropped barrier.
func TestDetmath_ZigguratStdlibFusesTail(t *testing.T) {
	t.Parallel()

	differ := 0
	pr, sr := rand.New(newCounting(7)), rand.New(newCounting(7))
	for range zigParityDraws {
		if math.Float64bits(NormFloat64(pr)) != math.Float64bits(sr.NormFloat64()) {
			differ++
		}
	}
	t.Logf("contracting build: %v; %d of %d normal draws differ from the stdlib", contractionObservable(), differ, zigParityDraws)
	if contractionObservable() && differ == 0 {
		t.Error("the fused stdlib tail reproduces every port draw on a contracting build: " +
			"the parity run can no longer see a dropped tail barrier")
	}
}

func BenchmarkNormFloat64(b *testing.B) {
	r := rand.New(rand.NewPCG(1, 2))
	b.Run("detmath", func(b *testing.B) {
		var s float64
		for b.Loop() {
			s += NormFloat64(r)
		}
		sink = s
	})
	b.Run("stdlib", func(b *testing.B) {
		var s float64
		for b.Loop() {
			s += r.NormFloat64()
		}
		sink = s
	})
}

func BenchmarkExpFloat64(b *testing.B) {
	r := rand.New(rand.NewPCG(1, 2))
	b.Run("detmath", func(b *testing.B) {
		var s float64
		for b.Loop() {
			s += ExpFloat64(r)
		}
		sink = s
	})
	b.Run("stdlib", func(b *testing.B) {
		var s float64
		for b.Loop() {
			s += r.ExpFloat64()
		}
		sink = s
	})
}
