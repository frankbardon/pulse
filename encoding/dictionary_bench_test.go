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
