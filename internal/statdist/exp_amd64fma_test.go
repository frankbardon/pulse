package statdist

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"math/rand/v2"
	"runtime"
	"testing"

	"golang.org/x/sys/cpu"
)

// expSweep is the deterministic input sweep behind
// TestStatdist_ExpAMD64FMA: uniform draws (bit-identical on every
// arch; the scale products are barriered) over the whole finite range,
// the IRLS working range and a tiny neighbourhood of 0, plus the edges
// (overflow — 709.78 is +Inf, as on amd64 — the denormal two-step,
// underflow, ±Inf, NaN).
func expSweep() []float64 {
	r := rand.New(rand.NewPCG(3, 4))
	var xs []float64
	for range 20000 {
		xs = append(xs,
			float64((r.Float64()-0.5)*1500),
			float64((r.Float64()-0.5)*40),
			float64((r.Float64()-0.5)*1e-6))
	}
	return append(xs, expEdges...)
}

var expEdges = []float64{
	0, math.Copysign(0, -1), 1, -1, 0.5, -0.5, 709.78, 709.782712893384, 709.7827128933841,
	-708.3, -708.4, -720, -744, -745.1, -745.2, -1e300, 1e300,
	5e-324, -5e-324, math.Inf(1), math.Inf(-1),
}

// TestStatdist_ExpAMD64FMA pins ExpAMD64FMA's bits on every arch (a
// SHA-256 over the sweep, pinned on darwin/arm64 and identical on
// linux/amd64) plus the edge values one by one, and — on an amd64 CPU
// whose math.Exp takes the AVX+FMA path (math.useFMA's own condition) —
// checks it equals math.Exp bit for bit, the property it exists for.
func TestStatdist_ExpAMD64FMA(t *testing.T) {
	t.Parallel()

	edges := []uint64{
		0x3ff0000000000000, 0x3ff0000000000000, 0x4005bf0a8b145769, 0x3fd78b56362cef38,
		0x3ffa61298e1e069c, 0x3fe368b2fc6f960a, 0x7ff0000000000000, 0x7ff0000000000000,
		0x7ff0000000000000, 0x00119e98b83de7a4, 0x000ff15b469edf89, 0x0000000993b4dc95,
		0x0000000000000002, 0x0000000000000001, 0x0000000000000000, 0x0000000000000000,
		0x7ff0000000000000, 0x3ff0000000000000, 0x3ff0000000000000, 0x7ff0000000000000,
		0x0000000000000000,
	}
	if len(edges) != len(expEdges) {
		t.Fatalf("edges table has %d bits for %d inputs", len(edges), len(expEdges))
	}
	for i, x := range expEdges {
		if got := math.Float64bits(ExpAMD64FMA(x)); got != edges[i] {
			t.Errorf("ExpAMD64FMA(%v) = %#016x, want %#016x", x, got, edges[i])
		}
	}
	if !math.IsNaN(ExpAMD64FMA(math.NaN())) {
		t.Error("ExpAMD64FMA(NaN) is not NaN")
	}

	h := sha256.New()
	var buf [8]byte
	xs := expSweep()
	for _, x := range xs {
		binary.LittleEndian.PutUint64(buf[:], math.Float64bits(ExpAMD64FMA(x)))
		h.Write(buf[:])
	}
	const want = "6e66aefe1fc8e6b542a28f8ad8561ccef1c1665a64129adea199df6e2e2ca695"
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		t.Errorf("ExpAMD64FMA sweep digest = %s, want %s", got, want)
	}

	if runtime.GOARCH != "amd64" || !(cpu.X86.HasAVX && cpu.X86.HasFMA) {
		return
	}
	for _, x := range xs {
		if a, b := math.Exp(x), ExpAMD64FMA(x); math.Float64bits(a) != math.Float64bits(b) {
			t.Errorf("amd64 FMA math.Exp(%v) = %#016x, ExpAMD64FMA = %#016x", x, math.Float64bits(a), math.Float64bits(b))
		}
	}
}
