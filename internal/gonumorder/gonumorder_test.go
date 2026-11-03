package gonumorder

import (
	"go/parser"
	"go/token"
	"math"
	"math/rand/v2"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// The routines here are exercised against gonum through their callers'
// parity tests (linalg TestSPD_MatchesGonumOrder for the SPD path,
// internal/sem TestSEM_MatOpsMatchGonum for Dgemm / Dsymm / Dgemv /
// Dsymv / the LU inverse / the Cholesky LogDet), which replay the gonum
// mat methods themselves. This file holds what only this package can
// state: its import boundary and the amd64 math.Log port.

// TestGonumOrderImportBoundary keeps gonumorder a leaf over the
// standard library: linalg imports it (TestLinalgImportBoundary), so
// anything wider would put the public linalg package one import away
// from the engine or a cycle.
func TestGonumOrderImportBoundary(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	seen := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		seen++
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if first, _, _ := strings.Cut(path, "/"); strings.Contains(first, ".") {
				t.Errorf("%s imports %q: gonumorder may import only the standard library", name, path)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no non-test Go files found")
	}
}

// logInputs are LogAMD64's corpus: the special cases, the f1 = √2/2
// boundary (where the pure-Go math.Log takes the other branch),
// subnormals, and uniform draws over many binades.
func logInputs() []float64 {
	in := []float64{0, math.Copysign(0, -1), -1, math.Inf(1), math.Inf(-1), math.NaN(), 1, 2, 0.5,
		0x1p-1074, 0x1p-1060, 3 * 0x1p-1070, math.SmallestNonzeroFloat64 * 7, math.MaxFloat64,
		7.07106781186547524401e-01, math.Ldexp(7.07106781186547524401e-01, 5), math.Ldexp(7.07106781186547524401e-01, -9),
		math.Nextafter(7.07106781186547524401e-01, 1), math.Nextafter(7.07106781186547524401e-01, 0)}
	rng := rand.New(rand.NewPCG(599, 78))
	for i := 0; i < 4000; i++ {
		in = append(in, math.Ldexp(0.5+float64(rng.Float64()*0.5), rng.IntN(200)-100))
	}
	return in
}

// TestGonumOrder_LogMatchesAMD64 holds LogAMD64 to the standard
// library's math.Log: on amd64 (whose math.Log is the assembly LogAMD64
// ports) bit for bit, NaN included; elsewhere — where math.Log is the
// FMA-contracted pure Go — within an ulp on normal inputs.
func TestGonumOrder_LogMatchesAMD64(t *testing.T) {
	t.Parallel()

	exact := runtime.GOARCH == "amd64"
	for _, x := range logInputs() {
		got, want := LogAMD64(x), math.Log(x)
		if exact {
			if math.Float64bits(got) != math.Float64bits(want) {
				t.Errorf("LogAMD64(%v) = %v (%#016x), math.Log %v (%#016x)", x, got, math.Float64bits(got), want, math.Float64bits(want))
			}
			continue
		}
		if x < 0x1p-1022 || math.IsNaN(x) || math.IsInf(x, 0) {
			continue // amd64's mask-based split differs from the pure Go off the normal range
		}
		if d := math.Abs(got - want); d > 2*math.Abs(math.Nextafter(want, 0)-want) && d > 0 {
			t.Errorf("LogAMD64(%v) = %v, math.Log %v", x, got, want)
		}
	}
}

// logDigestPin is the XOR-fold of LogAMD64 over logInputs (NaN
// canonical), generated on darwin/arm64 and checked on linux/amd64.
const logDigestPin = 0x4309309dbf493907

// TestGonumOrder_LogBitPinned: LogAMD64 gives the same bits on every
// architecture.
func TestGonumOrder_LogBitPinned(t *testing.T) {
	t.Parallel()

	var fold, mix uint64
	for _, x := range logInputs() {
		bits := math.Float64bits(LogAMD64(x))
		if math.IsNaN(LogAMD64(x)) {
			bits = 0x7ff8000000000001
		}
		mix = mix*0x100000001b3 ^ bits
		fold ^= mix
	}
	if fold != logDigestPin {
		t.Fatalf("LogAMD64 corpus fold %#016x, pinned %#016x (GOARCH=%s)", fold, uint64(logDigestPin), runtime.GOARCH)
	}
}
