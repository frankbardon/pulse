// Package arena provides a bump-allocator backed by a single contiguous
// []byte. The arena hands out []float64 and []byte slices that share that
// backing buffer; resetting the arena invalidates every slice it ever
// returned.
//
// Use cases inside Pulse:
//
//   - Streaming iterator scratch: each row needs small typed buffers
//     (8-byte field-decode scratch, transient wide-value slots). Bumping
//     into a shared buffer avoids the per-row map/slice make().
//   - Batch processing: when the row count is known up front, one Grow
//     call up front + AllocF64s per aggregator working buffer collapses
//     N independent makes into one.
//
// Lifetime contract: anything returned from AllocF64s/AllocBytes lives
// until the next Reset(). After Reset(), every previously-returned slice
// header is still valid Go memory but its bytes will be overwritten by
// subsequent allocations. Callers MUST NOT retain slices across Reset.
//
// Concurrency: Arena is not safe for concurrent use. Callers that fan
// allocations across goroutines must hold one Arena per goroutine.
package arena

import "unsafe"

// Arena is a bump allocator backed by a single byte buffer.
type Arena struct {
	buf []byte
	off int
}

// New creates an Arena with the given initial capacity in bytes. Capacity
// grows on demand when Alloc requests exceed available space; pre-sizing
// to the expected high-water mark avoids the grow path.
func New(initialBytes int) *Arena {
	if initialBytes < 0 {
		initialBytes = 0
	}
	return &Arena{buf: make([]byte, 0, initialBytes)}
}

// Reset returns the offset to zero. All slices previously returned by
// Alloc* remain valid Go memory but their contents are no longer owned
// by the caller — subsequent allocations will overwrite them.
func (a *Arena) Reset() {
	a.off = 0
}

// Len returns the number of bytes currently allocated since the last
// Reset.
func (a *Arena) Len() int { return a.off }

// Cap returns the arena's current capacity in bytes.
func (a *Arena) Cap() int { return cap(a.buf) }

// AllocBytes returns a []byte of length n drawn from the arena. The
// returned slice is zeroed (Go-init semantics) and aliased to the arena
// backing buffer.
func (a *Arena) AllocBytes(n int) []byte {
	if n <= 0 {
		return nil
	}
	a.grow(n)
	out := a.buf[a.off : a.off+n : a.off+n]
	a.off += n
	// Re-zero the region: previous Reset cycle may have left data behind.
	for i := range out {
		out[i] = 0
	}
	return out
}

// AllocF64s returns a []float64 of length n drawn from the arena. The
// underlying memory is 8-byte aligned because Alloc places f64 regions
// at offsets that are multiples of 8.
func (a *Arena) AllocF64s(n int) []float64 {
	if n <= 0 {
		return nil
	}
	// Align off to 8 bytes for safe float64 access on architectures
	// that require it (arm, sparc, some old armv7). amd64/arm64 tolerate
	// unaligned, but the cost of aligning is one ALU op per Alloc.
	if pad := a.off & 7; pad != 0 {
		a.off += 8 - pad
	}
	bytes := n * 8
	a.grow(bytes)
	region := a.buf[a.off : a.off+bytes]
	a.off += bytes
	// Zero the region.
	for i := range region {
		region[i] = 0
	}
	// Convert []byte to []float64 view.
	return unsafe.Slice((*float64)(unsafe.Pointer(&region[0])), n)
}

// grow ensures at least n bytes are available past off. Doubles capacity
// each grow to amortize.
func (a *Arena) grow(n int) {
	need := a.off + n
	if need <= cap(a.buf) {
		// Extend len in place; backing array unchanged.
		if need > len(a.buf) {
			a.buf = a.buf[:need]
		}
		return
	}
	// Reallocate. Double capacity until it fits.
	newCap := max(cap(a.buf)*2, 64)
	for newCap < need {
		newCap *= 2
	}
	next := make([]byte, need, newCap)
	copy(next, a.buf[:a.off])
	a.buf = next
}
