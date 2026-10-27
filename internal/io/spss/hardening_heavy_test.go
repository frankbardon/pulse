//go:build heavy

package spss

import (
	"testing"

	"github.com/frankbardon/pulse/internal/spsstest"
)

// TestCorruption_DictionarySweep is the dictionary's counterpart to E3-S1's
// data-section byte-fill fuzz and E3-S2's ZSAV cut sweep: every byte of the
// dictionary, overwritten with every one of a set of fills that between them
// cover the tag, length and sentinel values the walk branches on.
//
// The invariant is the total one — never a panic, and any failure is a coded
// error from a known family carrying an in-range offset. It is deliberately
// NOT "this mutation must fail": most single-byte edits land in a label or a
// name and produce a file that is different but perfectly readable, and a
// test demanding failure would be asserting that corruption is always
// detectable, which is false for a format with no checksums.
func TestCorruption_DictionarySweep(t *testing.T) {
	t.Parallel()

	for _, mode := range []struct {
		name string
		c    spsstest.Compression
	}{
		{"uncompressed", spsstest.CompressionNone},
		{"bytecode", spsstest.CompressionBytecode},
		{"zsav", spsstest.CompressionZSAV},
	} {
		t.Run(mode.name, func(t *testing.T) {
			t.Parallel()

			raw := build(t, endianTwinSpec(spsstest.LittleEndian, mode.c))
			dictEnd := mustParse(t, raw).dataOffset

			// All 256 fills, matching the bar E3-S1 set over the data
			// section. Choosing a "meaningful" subset would beg the
			// question — the values that matter are the ones the walk
			// branches on, and the branches are what is under test.
			for off := 0; off < dictEnd; off++ {
				for fill := 0; fill < 256; fill++ {
					if raw[off] == byte(fill) {
						continue
					}
					b := clone(raw)
					b[off] = byte(fill)
					assertNoPanicCodedOrFine(t, b, off, byte(fill))
				}
			}
		})
	}
}
