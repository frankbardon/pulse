package encoding

import (
	"bytes"
	"fmt"
	"strconv"
	"testing"
)

// buildDict constructs a dictionary with n unique entries.
func buildDict(n int) *Dictionary {
	d := NewDictionary()
	for i := range n {
		_, _ = d.Add("entry_" + strconv.Itoa(i))
	}
	return d
}

// serializedDict returns the on-wire bytes for a dictionary of n entries.
func serializedDict(n int) []byte {
	d := buildDict(n)
	var buf bytes.Buffer
	if _, err := d.WriteTo(&buf); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// BenchmarkDictionary_Resolve_HotPath documents that Dictionary.Resolve
// is already allocation-free per call: the dictionary caches its values
// at parse time as Go strings, and Resolve returns the cached pointer.
// This benchmark exists to refute the "zero-copy categorical" premise
// in .planning/improvement-12-mmap-and-arena-allocators.md, which
// assumed Pulse allocates a string per access.
//
// Any future change that adds per-call allocation in Resolve will show
// up here as allocs/op > 0 and block via reviewer eyeball (not a CI
// gate, but cheap to notice).
func BenchmarkDictionary_Resolve_HotPath(b *testing.B) {
	d := buildDict(16)
	b.ReportAllocs()
	var sink string
	for i := 0; b.Loop(); i++ {
		sink = d.Resolve(uint32(i & 15))
	}
	if sink == "" {
		b.Fatal("sink should never be empty for valid ids")
	}
}

// BenchmarkDictionary_ReadFrom guards the ReadFrom hot path. The shared-
// buffer rewrite drops one allocation per entry (was 2/entry, now 1/entry +
// one shared buffer). Don't regress.
func BenchmarkDictionary_ReadFrom(b *testing.B) {
	for _, n := range []int{5, 50, 500, 5000} {
		data := serializedDict(n)
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			for i := 0; i < b.N; i++ {
				d := NewDictionary()
				if _, err := d.ReadFrom(bytes.NewReader(data)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
