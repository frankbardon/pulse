package spsstest

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

// byteOrderSpec is a fixture with at least one of every byte-ordered field
// shape the emitter writes: header int32s and a float64 bias, a record type 2
// packed format word, a record type 3 value slot, a record type 4 index, a
// record 7/3 int32 payload, a record 7/16 int64 payload, and case data.
func byteOrderSpec(bo ByteOrder, c Compression) Spec {
	mi := DefaultMachineIntegerInfoFor(bo)
	n := int64(2)
	return Spec{
		ByteOrder:   bo,
		Compression: c,
		Vars: []Var{
			{Name: "ID"},
			{Name: "SEX", Label: "Sex"},
			{Name: "NAME", Width: 10},
		},
		ValueLabels: []ValueLabelSet{{
			Vars:   []string{"SEX"},
			Labels: []ValueLabel{{Value: Num(1), Label: "Male"}},
		}},
		Cases: [][]Value{
			{Num(1), Num(1), Text("ALICE")},
			{Num(1e300), SysMis(), Text("BOB")},
		},
		MachineIntegerInfo: &mi,
		CaseCount64:        &n,
	}
}

// TestBuild_BigEndianHeaderFields reads the header back at the offsets the
// hand-verified walkthrough fixes, byte for byte, and asserts each one is
// most-significant-byte first.
//
// Reading the fields back with binary.BigEndian rather than with the parser
// is the point: the parser is the code under test elsewhere, and letting it
// verify the emitter would make one shared misreading invisible.
func TestBuild_BigEndianHeaderFields(t *testing.T) {
	b, err := Build(byteOrderSpec(BigEndian, CompressionNone))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if got := string(b[0:4]); got != "$FL2" {
		t.Errorf("magic = %q, want $FL2; the magic is text and is not byte-ordered", got)
	}
	for _, tc := range []struct {
		name string
		off  int
		want int32
	}{
		{"layout_code", 0x40, 2},
		{"nominal_case_size", 0x44, 4},
		{"compression", 0x48, 0},
		{"weight_index", 0x4C, 0},
		{"ncases", 0x50, 2},
	} {
		if got := int32(binary.BigEndian.Uint32(b[tc.off:])); got != tc.want {
			t.Errorf("%s big-endian = %d, want %d (bytes %x)", tc.name, got, tc.want, b[tc.off:tc.off+4])
		}
		// The same bytes read the other way must NOT be the value: that
		// is what proves the field moved rather than happening to be
		// palindromic.
		if got := int32(binary.LittleEndian.Uint32(b[tc.off:])); got == tc.want && tc.want != 0 {
			t.Errorf("%s reads as %d in BOTH orders; the field was not byte-swapped", tc.name, tc.want)
		}
	}
	if got := math.Float64frombits(binary.BigEndian.Uint64(b[0x54:])); got != 100 {
		t.Errorf("bias big-endian = %v, want 100 (bytes %x)", got, b[0x54:0x5C])
	}

	// Every fixed-width text field is untouched by the byte order, which
	// is what makes a big-endian and a little-endian fixture comparable
	// at all.
	if got := string(b[0x5C:0x65]); got != DefaultCreationDate {
		t.Errorf("creation_date = %q, want %q", got, DefaultCreationDate)
	}
}

// TestBuild_ByteOrderIsDeterministic keeps the emitter's central promise on
// the new axis: same spec, same bytes, every time.
func TestBuild_ByteOrderIsDeterministic(t *testing.T) {
	for _, c := range []Compression{CompressionNone, CompressionBytecode, CompressionZSAV} {
		for _, bo := range []ByteOrder{LittleEndian, BigEndian} {
			first, err := Build(byteOrderSpec(bo, c))
			if err != nil {
				t.Fatalf("Build(%v, %v): %v", bo, c, err)
			}
			for i := 0; i < 3; i++ {
				again, err := Build(byteOrderSpec(bo, c))
				if err != nil {
					t.Fatalf("Build(%v, %v) rebuild: %v", bo, c, err)
				}
				if !bytes.Equal(first, again) {
					t.Fatalf("Build(%v, %v) is not deterministic across calls", bo, c)
				}
			}
		}
	}
}

// TestBuild_ByteOrderChangesEverySection is the anti-vacuity check. A
// generator that byte-swapped the header and left the extension payloads or
// the data section alone would produce a file that parses and decodes to
// wrong numbers, so the two orders must differ in each section separately —
// not merely somewhere.
func TestBuild_ByteOrderChangesEverySection(t *testing.T) {
	for _, c := range []Compression{CompressionNone, CompressionBytecode, CompressionZSAV} {
		le, err := Build(byteOrderSpec(LittleEndian, c))
		if err != nil {
			t.Fatalf("Build(little, %v): %v", c, err)
		}
		be, err := Build(byteOrderSpec(BigEndian, c))
		if err != nil {
			t.Fatalf("Build(big, %v): %v", c, err)
		}
		if len(le) != len(be) {
			// ZSAV block sizes can differ once the bytes differ, which
			// is legitimate; only the fixed sections are compared
			// below, so a length change is not itself a fault.
			t.Logf("%v: lengths differ (%d vs %d)", c, len(le), len(be))
		}

		// The header, where layout_code lives.
		if bytes.Equal(le[0x40:0x54], be[0x40:0x54]) {
			t.Errorf("%v: the header's int32 block is identical in both orders", c)
		}
		// The record type 2 block, where the packed format words live.
		hdr := 176
		if bytes.Equal(le[hdr:hdr+32], be[hdr:hdr+32]) {
			t.Errorf("%v: the first record type 2 is identical in both orders", c)
		}
		// The data section, found via the terminator each file carries.
		leData := dataStart(t, le, binary.LittleEndian)
		beData := dataStart(t, be, binary.BigEndian)
		if bytes.Equal(le[leData:], be[beData:]) {
			t.Errorf("%v: the data section is identical in both orders", c)
		}
	}
}

// dataStart walks the record tags to the terminator, returning the offset of
// the first data byte. It is an independent mini-walk so the test does not
// depend on the reader.
func dataStart(t *testing.T, b []byte, bo binary.ByteOrder) int {
	t.Helper()
	for off := 176; off+8 <= len(b); {
		rt := int32(bo.Uint32(b[off:]))
		switch rt {
		case 999:
			return off + 8
		case 2:
			hasLabel := bo.Uint32(b[off+8:]) == 1
			nMissing := int32(bo.Uint32(b[off+12:]))
			if nMissing < 0 {
				nMissing = -nMissing
			}
			off += 32
			if hasLabel {
				n := int(bo.Uint32(b[off:]))
				off += 4 + (n+3)/4*4
			}
			off += int(nMissing) * 8
		case 3:
			n := int(bo.Uint32(b[off+4:]))
			off += 8
			for i := 0; i < n; i++ {
				ln := int(b[off+8])
				off += 8 + (ln+1+7)/8*8
			}
		case 4:
			n := int(bo.Uint32(b[off+4:]))
			off += 8 + n*4
		case 6:
			n := int(bo.Uint32(b[off+4:]))
			off += 8 + n*80
		case 7:
			size := int(bo.Uint32(b[off+8:]))
			count := int(bo.Uint32(b[off+12:]))
			off += 16 + size*count
		default:
			t.Fatalf("unexpected record type %d at offset %d", rt, off)
		}
	}
	t.Fatal("no record type 999 terminator")
	return 0
}

// TestDefaultMachineIntegerInfoFor pins the helper that keeps a big-endian
// fixture from declaring little-endian in its own record 7/3 — the exact
// self-contradiction the reader's cross-check rejects, and one an author
// reaching for "the ordinary defaults" would not have meant.
func TestDefaultMachineIntegerInfoFor(t *testing.T) {
	if got := DefaultMachineIntegerInfoFor(LittleEndian).Endianness; got != EndiannessLittle {
		t.Errorf("little-endian Endianness = %d, want %d", got, EndiannessLittle)
	}
	if got := DefaultMachineIntegerInfoFor(BigEndian).Endianness; got != EndiannessBig {
		t.Errorf("big-endian Endianness = %d, want %d", got, EndiannessBig)
	}
	if DefaultMachineIntegerInfo() != DefaultMachineIntegerInfoFor(LittleEndian) {
		t.Error("DefaultMachineIntegerInfo diverged from its little-endian form")
	}
}

// TestBuild_EndiannessFieldIsEmittedVerbatim pins the one deliberate
// non-reconciliation in the emitter. Build must NOT correct a 7/3 endianness
// field that contradicts Spec.ByteOrder, because a fixture that carries that
// contradiction is the only way to test that a reader notices it.
func TestBuild_EndiannessFieldIsEmittedVerbatim(t *testing.T) {
	spec := byteOrderSpec(BigEndian, CompressionNone)
	mi := *spec.MachineIntegerInfo
	mi.Endianness = EndiannessLittle // a lie, on purpose
	spec.MachineIntegerInfo = &mi

	b, err := Build(spec)
	if err != nil {
		t.Fatalf("Build refused a self-contradicting spec; the contradiction is the fixture: %v", err)
	}
	// The 7/3 payload is eight int32s; endianness is the seventh, so it
	// sits 24 bytes in. Find the record by its tag rather than by a fixed
	// offset, so the test does not move when a record ahead of it does.
	want := make([]byte, 4)
	binary.BigEndian.PutUint32(want, uint32(EndiannessLittle))
	needle := tagBytes(binary.BigEndian, 7, SubtypeMachineInteger, 4, 8)
	at := bytes.Index(b, needle)
	if at < 0 {
		t.Fatal("no record 7/3 in the emitted bytes")
	}
	got := b[at+16+24 : at+16+28]
	if !bytes.Equal(got, want) {
		t.Errorf("record 7/3 endianness field = %x, want %x emitted verbatim", got, want)
	}
}

// tagBytes renders a record type 7 header (rec_type, subtype, size, count).
func tagBytes(bo binary.ByteOrder, rt, subtype, size, count int32) []byte {
	out := make([]byte, 16)
	bo.PutUint32(out[0:], uint32(rt))
	bo.PutUint32(out[4:], uint32(subtype))
	bo.PutUint32(out[8:], uint32(size))
	bo.PutUint32(out[12:], uint32(count))
	return out
}
