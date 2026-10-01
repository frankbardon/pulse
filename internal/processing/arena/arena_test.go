package arena

import (
	"math/rand/v2"
	"testing"
)

func TestArena_AllocBytes_Independent(t *testing.T) {
	a := New(0)
	x := a.AllocBytes(4)
	y := a.AllocBytes(4)
	if len(x) != 4 || len(y) != 4 {
		t.Fatalf("lens: x=%d y=%d", len(x), len(y))
	}
	for i := range x {
		x[i] = byte(i + 1)
	}
	for i := range y {
		y[i] = byte(0xF0 | i)
	}
	for i := range x {
		if x[i] != byte(i+1) {
			t.Fatalf("x corrupted at %d: got %x", i, x[i])
		}
	}
	for i := range y {
		if y[i] != byte(0xF0|i) {
			t.Fatalf("y corrupted at %d: got %x", i, y[i])
		}
	}
}

func TestArena_AllocF64s_StoresValues(t *testing.T) {
	a := New(0)
	xs := a.AllocF64s(5)
	for i := range xs {
		xs[i] = float64(i) * 1.5
	}
	for i, v := range xs {
		want := float64(i) * 1.5
		if v != want {
			t.Fatalf("xs[%d] = %v, want %v", i, v, want)
		}
	}
}

func TestArena_Reset_OverwritesPriorAllocs(t *testing.T) {
	a := New(0)
	first := a.AllocBytes(8)
	for i := range first {
		first[i] = 0xAA
	}
	if a.Len() != 8 {
		t.Fatalf("Len after alloc: got %d want 8", a.Len())
	}
	a.Reset()
	if a.Len() != 0 {
		t.Fatalf("Len after Reset: got %d want 0", a.Len())
	}
	// Second allocation at the same offset must be zero-initialized,
	// not the 0xAA bytes from the prior cycle.
	second := a.AllocBytes(8)
	for i, b := range second {
		if b != 0 {
			t.Fatalf("second[%d] = %x, want 0 (arena did not zero on reuse)", i, b)
		}
	}
}

func TestArena_Grow_PreservesAlreadyAllocatedData(t *testing.T) {
	a := New(8)
	first := a.AllocBytes(8)
	for i := range first {
		first[i] = byte(i + 10)
	}
	// Force a grow by allocating beyond current capacity.
	second := a.AllocBytes(100)
	_ = second
	// IMPORTANT: after grow, `first` points to the OLD backing buffer.
	// It must still read back the original values (Go grow doesn't
	// invalidate prior slice headers).
	for i, b := range first {
		if b != byte(i+10) {
			t.Fatalf("first[%d] = %x after grow, want %x", i, b, byte(i+10))
		}
	}
}

func TestArena_F64Alignment(t *testing.T) {
	a := New(0)
	_ = a.AllocBytes(3) // unaligned offset
	xs := a.AllocF64s(2)
	if len(xs) != 2 {
		t.Fatalf("len: %d", len(xs))
	}
	xs[0] = -1.25
	xs[1] = 7
	if xs[0] != -1.25 || xs[1] != 7 {
		t.Fatalf("read back: %v %v", xs[0], xs[1])
	}
}

func TestArena_ZeroSizedAlloc(t *testing.T) {
	a := New(0)
	if got := a.AllocBytes(0); got != nil {
		t.Fatalf("AllocBytes(0) = %v, want nil", got)
	}
	if got := a.AllocF64s(0); got != nil {
		t.Fatalf("AllocF64s(0) = %v, want nil", got)
	}
}

// FuzzArena_AllocResetIntegrity exercises random sequences of allocs and
// resets. After each batch, every slice handed out within that batch
// must round-trip the value the test wrote.
func FuzzArena_AllocResetIntegrity(f *testing.F) {
	f.Add(uint64(1), uint8(5), uint16(64))
	f.Add(uint64(42), uint8(20), uint16(8))
	f.Fuzz(func(t *testing.T, seed uint64, batches uint8, maxAlloc uint16) {
		if batches == 0 {
			batches = 1
		}
		if maxAlloc == 0 {
			maxAlloc = 4
		}
		rng := rand.New(rand.NewPCG(seed, seed^0xdeadbeef))
		a := New(0)
		for b := byte(0); b < batches; b++ {
			batchAllocs := 1 + rng.IntN(8)
			records := make([][]byte, 0, batchAllocs)
			fillBytes := make([]byte, 0, batchAllocs)
			for range batchAllocs {
				n := 1 + rng.IntN(int(maxAlloc))
				buf := a.AllocBytes(n)
				if len(buf) != n {
					t.Fatalf("alloc len: got %d want %d", len(buf), n)
				}
				fill := byte(rng.UintN(256))
				for j := range buf {
					buf[j] = fill
				}
				records = append(records, buf)
				fillBytes = append(fillBytes, fill)
			}
			// Verify each buffer still holds its fill byte before reset.
			for i, buf := range records {
				for j, got := range buf {
					if got != fillBytes[i] {
						t.Fatalf("batch=%d alloc=%d byte=%d: got %x want %x", b, i, j, got, fillBytes[i])
					}
				}
			}
			a.Reset()
		}
	})
}
