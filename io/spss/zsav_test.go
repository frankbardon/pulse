package spss

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/spsstest"
)

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// allThreeEncodings builds spec three times — uncompressed, bytecode and ZSAV
// — so a test can hold all three encodings of one logical file side by side.
//
// Building from ONE spec is the whole point. The three byte streams then
// differ only in how the data section says what it says, so any disagreement
// between the three reads is a decoder bug rather than a fixture difference.
func allThreeEncodings(t *testing.T, spec spsstest.Spec) (plain, packed, zipped []byte) {
	t.Helper()
	spec.Compression = spsstest.CompressionNone
	plain = build(t, spec)
	spec.Compression = spsstest.CompressionBytecode
	packed = build(t, spec)
	spec.Compression = spsstest.CompressionZSAV
	zipped = build(t, spec)
	return plain, packed, zipped
}

// zsavSpec asks for a spec as a ZSAV, optionally cutting the stream into
// small blocks so the index spans several of them.
func zsavSpec(spec spsstest.Spec, blockSize int) spsstest.Spec {
	spec.Compression = spsstest.CompressionZSAV
	spec.ZSAVBlockSize = blockSize
	return spec
}

// multiBlockSpec is a fixture large enough, at a deliberately small block
// size, to span many blocks. A one-block index exercises none of the
// cumulative-offset arithmetic, so this is the fixture the index tests use.
func multiBlockSpec() spsstest.Spec {
	spec := spsstest.Spec{
		Vars: []spsstest.Var{
			{Name: "I", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8}},
			{Name: "S", Width: 16},
		},
	}
	for i := 0; i < 120; i++ {
		spec.Cases = append(spec.Cases, []spsstest.Value{
			spsstest.Num(float64(i - 200)),
			spsstest.Text(strings.Repeat(string(rune('A'+i%26)), i%17)),
		})
	}
	return spec
}

// ---------------------------------------------------------------------------
// Index surgery
// ---------------------------------------------------------------------------

// zsavFields locates the ZSAV block index inside an emitted fixture, so a
// test can corrupt one field of it without rebuilding the file.
//
// It walks the ZHEADER by hand rather than calling readZSAVIndex: a test that
// located the bytes with the code under test would move with a bug in it.
type zsavFields struct {
	zheader int // file offset of the ZHEADER
	trailer int // file offset of the ZTRAILER
	blocks  int // the block count the trailer declares
}

func locateZSAV(t *testing.T, raw []byte) zsavFields {
	t.Helper()
	d := mustParse(t, raw)
	if d.header.compression != compressionZSAV {
		t.Fatalf("fixture declares compression %d, not ZSAV", d.header.compression)
	}
	f := zsavFields{zheader: d.dataOffset}
	f.trailer = int(binary.LittleEndian.Uint64(raw[f.zheader+8:]))
	if f.trailer < 0 || f.trailer+ztrailerHeadSize > len(raw) {
		t.Fatalf("the ZHEADER puts the trailer at %d, outside a %d-byte file", f.trailer, len(raw))
	}
	f.blocks = int(int32(binary.LittleEndian.Uint32(raw[f.trailer+20:])))
	return f
}

// entry returns the file offset of block i's index entry, 0-based.
func (f zsavFields) entry(i int) int {
	return f.trailer + ztrailerHeadSize + i*ztrailerEntrySize
}

func putI64(b []byte, off int, v int64) { binary.LittleEndian.PutUint64(b[off:], uint64(v)) }
func putI32(b []byte, off int, v int32) { binary.LittleEndian.PutUint32(b[off:], uint32(v)) }

func getI64(b []byte, off int) int64 { return int64(binary.LittleEndian.Uint64(b[off:])) }
func getI32(b []byte, off int) int32 { return int32(binary.LittleEndian.Uint32(b[off:])) }

// rezsav rebuilds a ZSAV fixture around a mutated command stream.
//
// It exists for the faults the generator will not produce: a stream cut
// mid-case, a command that contradicts the dictionary, a block index naming
// no blocks. internal/spsstest refuses to emit any of those on purpose — a
// generator that could emit an inconsistent file would make an inconsistent
// file look legitimate — so the tests that need one assemble it here.
//
// Only the packaging is rebuilt. The dictionary comes through untouched, and
// the inner stream is whatever mutate returns, so a test states exactly the
// one thing it changed.
func rezsav(t *testing.T, raw []byte, blockSize int, mutate func([]byte) []byte) []byte {
	t.Helper()
	d := mustParse(t, raw)
	idx, err := readZSAVIndex(d, raw)
	if err != nil {
		t.Fatalf("readZSAVIndex on a generated fixture: %v", err)
	}
	stream, err := idx.inflate(raw)
	if err != nil {
		t.Fatalf("inflate on a generated fixture: %v", err)
	}
	if mutate != nil {
		stream = mutate(stream)
	}

	var out bytes.Buffer
	out.Write(raw[:d.dataOffset])
	zheaderOfs := int64(d.dataOffset)

	type block struct {
		uncompressedOfs, compressedOfs   int64
		uncompressedSize, compressedSize int32
		payload                          []byte
	}
	var blocks []block
	uOfs, cOfs := zheaderOfs, zheaderOfs+zheaderSize
	for start := 0; start < len(stream); start += blockSize {
		end := start + blockSize
		if end > len(stream) {
			end = len(stream)
		}
		var buf bytes.Buffer
		zw, err := zlib.NewWriterLevel(&buf, spsstest.ZSAVCompressionLevel)
		if err != nil {
			t.Fatalf("zlib.NewWriterLevel: %v", err)
		}
		if _, err := zw.Write(stream[start:end]); err != nil {
			t.Fatalf("compressing: %v", err)
		}
		if err := zw.Close(); err != nil {
			t.Fatalf("closing: %v", err)
		}
		blocks = append(blocks, block{uOfs, cOfs, int32(end - start), int32(buf.Len()), buf.Bytes()})
		uOfs += int64(end - start)
		cOfs += int64(buf.Len())
	}

	ztrailerOfs := cOfs
	var hdr [24]byte
	putI64(hdr[:], 0, zheaderOfs)
	putI64(hdr[:], 8, ztrailerOfs)
	putI64(hdr[:], 16, int64(ztrailerHeadSize+len(blocks)*ztrailerEntrySize))
	out.Write(hdr[:])
	for _, b := range blocks {
		out.Write(b.payload)
	}

	var tr [24]byte
	putI64(tr[:], 0, int64(-d.header.bias))
	putI64(tr[:], 8, 0)
	putI32(tr[:], 16, int32(blockSize))
	putI32(tr[:], 20, int32(len(blocks)))
	out.Write(tr[:])
	for _, b := range blocks {
		var e [24]byte
		putI64(e[:], 0, b.uncompressedOfs)
		putI64(e[:], 8, b.compressedOfs)
		putI32(e[:], 16, b.uncompressedSize)
		putI32(e[:], 20, b.compressedSize)
		out.Write(e[:])
	}
	return out.Bytes()
}

// ---------------------------------------------------------------------------
// The central criterion
// ---------------------------------------------------------------------------

// TestZSAV_MatchesItsUncompressedTwin is this story's strongest test: a ZSAV
// fixture and the uncompressed fixture of the same spec must produce
// identical cohorts.
//
// It is strong because the two files are packaged INDEPENDENTLY — the
// generator deflates the command stream from the specification with no
// reference to this package — so the decoder is being checked against
// something other than itself, and the bytes on the wire are genuinely
// different (asserted here rather than assumed). Both halves of the cohort
// are compared: the rendered rows, and the authoritative schema, which is
// derived from a full scan of the data section and would drift if the
// inflation were wrong in a way the rows happened to hide.
//
// The bytecode twin is compared too. That is the layering check at the reader:
// ZSAV inflates TO a bytecode stream, so a ZSAV and a bytecode file built from
// one spec are the same stream in different wrappers, and a reader that
// treated the inflated bytes as anything else would disagree here first.
func TestZSAV_MatchesItsUncompressedTwin(t *testing.T) {
	cases := []struct {
		name      string
		spec      spsstest.Spec
		blockSize int
	}{
		{"the reference fixture", spsstest.ReferenceSpec(), 0},
		{"every command byte", everyCommandSpec(), 0},
		{"every command byte, many blocks", everyCommandSpec(), 16},
		{"a multi-block stream", multiBlockSpec(), 64},
		{"a stream cut one byte per block", spsstest.ReferenceSpec(), 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := tc.spec
			spec.ZSAVBlockSize = tc.blockSize
			plain, packed, zipped := allThreeEncodings(t, spec)

			if bytes.Equal(plain, zipped) || bytes.Equal(packed, zipped) {
				t.Fatal("the ZSAV fixture is byte-identical to one of its twins; it is not exercising the decoder")
			}

			want := readAll(t, NewReaderFromBytes(plain))
			assertRows(t, readAll(t, NewReaderFromBytes(zipped)), want)
			assertRows(t, readAll(t, NewReaderFromBytes(packed)), want)

			assertSchemasEqual(t,
				mustSchema(t, NewReaderFromBytes(zipped)),
				mustSchema(t, NewReaderFromBytes(plain)))
		})
	}
}

// TestZSAV_NoWarningsOnACleanFile guards the case count in particular: the
// number of cases a ZSAV yields is what the header's declaration is compared
// against, so a decoder producing the right rows but the wrong count would
// warn on every clean file.
func TestZSAV_NoWarningsOnACleanFile(t *testing.T) {
	spec := zsavSpec(everyCommandSpec(), 16)
	r := NewReaderFromBytes(build(t, spec))
	if rows := readAll(t, r); len(rows) != 4 {
		t.Fatalf("read %d case(s), want 4", len(rows))
	}
	if w := r.Warnings(); len(w) != 0 {
		t.Errorf("a clean ZSAV file raised %d warning(s): %v", len(w), w)
	}
}

// TestZSAV_HonoursTheDeclaredBias proves the bias still comes from the header
// through the extra layer. ZSAV does not carry its own usable bias — the
// trailer's int64 copy is deliberately ignored — so a decoder that reached
// for it, or that hardcoded 100, would read every integer command offset by a
// constant.
func TestZSAV_HonoursTheDeclaredBias(t *testing.T) {
	for _, bias := range []float64{37, 50, 100} {
		spec := zsavSpec(everyCommandSpec(), 16)
		spec.CompressionBias = bias
		plainSpec := spec
		plainSpec.Compression = spsstest.CompressionNone

		assertRows(t,
			readAll(t, NewReaderFromBytes(build(t, spec))),
			readAll(t, NewReaderFromBytes(build(t, plainSpec))))
	}
}

// TestZSAV_ManyCases exercises inflation at a size where the output buffer has
// to grow across many blocks, and where a per-block off-by-one that a
// four-case fixture tolerates compounds into visible drift.
func TestZSAV_ManyCases(t *testing.T) {
	const n = 5000
	spec := spsstest.Spec{
		Vars: []spsstest.Var{
			{Name: "I", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8}},
			{Name: "S", Width: 8},
		},
	}
	for i := 0; i < n; i++ {
		spec.Cases = append(spec.Cases, []spsstest.Value{
			spsstest.Num(float64(i - 200)),
			spsstest.Text(strings.Repeat("A", i%9)),
		})
	}
	spec.ZSAVBlockSize = 1024
	plain, _, zipped := allThreeEncodings(t, spec)

	if f := locateZSAV(t, zipped); f.blocks < 4 {
		t.Fatalf("the fixture spans %d block(s); it exists to exercise a multi-block index", f.blocks)
	}
	assertRows(t, readAll(t, NewReaderFromBytes(zipped)), readAll(t, NewReaderFromBytes(plain)))
}

// TestZSAV_Reset re-reads one file twice. The inflation is memoised with the
// mapping, so the second pass must see the same cases and raise no second set
// of diagnostics.
func TestZSAV_Reset(t *testing.T) {
	r := NewReaderFromBytes(build(t, zsavSpec(everyCommandSpec(), 16)))
	first := readAll(t, r)
	if err := r.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	assertRows(t, readAll(t, r), first)
	if w := r.Warnings(); len(w) != 0 {
		t.Errorf("a second pass raised %d warning(s): %v", len(w), w)
	}
}

// TestZSAV_EmptyDataSection covers a file with no cases. The command stream
// is still non-empty — the end-of-file command and its block padding are part
// of the encoding — so there is a block to inflate and nothing in it.
func TestZSAV_EmptyDataSection(t *testing.T) {
	spec := spsstest.Spec{Vars: []spsstest.Var{{Name: "A"}}, Compression: spsstest.CompressionZSAV}
	rows := readAll(t, NewReaderFromBytes(build(t, spec)))
	if len(rows) != 0 {
		t.Errorf("read %d case(s) from a file with none: %q", len(rows), rows)
	}
}

// ---------------------------------------------------------------------------
// The block index
// ---------------------------------------------------------------------------

// TestZSAV_IndexIsValidatedNotTrusted is the "corrupt block index is a coded
// error naming the block" criterion.
//
// Each case mutates ONE field of an otherwise-valid index. Every one must
// produce PULSE_SPSS_ZSAV_INVALID rather than a decode, because the index is
// the only thing that says where a block begins: inflating from an offset the
// writer never wrote a stream at either fails or, worse, succeeds on
// something. Where a block is implicated the message and the details must
// both name it, so a user is told which of a thousand blocks to look at.
func TestZSAV_IndexIsValidatedNotTrusted(t *testing.T) {
	cases := []struct {
		name string
		// mutate corrupts the index in place.
		mutate func(raw []byte, f zsavFields)
		// wantBlock is the 1-based block the diagnostic must name, 0
		// when the fault implicates no single block, and -1 for "the
		// last block", whose number depends on the fixture.
		wantBlock int
		wantMsg   string
	}{
		{
			"the header disowns its own offset",
			func(raw []byte, f zsavFields) { putI64(raw, f.zheader, int64(f.zheader+8)) },
			0, "states it sits at byte offset",
		},
		{
			"the trailer length does not fit whole entries",
			func(raw []byte, f zsavFields) { putI64(raw, f.zheader+16, 30) },
			0, "plus a whole number of",
		},
		{
			"the trailer sits before the first block could",
			func(raw []byte, f zsavFields) { putI64(raw, f.zheader+8, int64(f.zheader)) },
			0, "before the first block could start",
		},
		{
			"the trailer does not end at the end of the file",
			func(raw []byte, f zsavFields) { putI64(raw, f.zheader+8, int64(f.trailer-8)) },
			0, "but the file is",
		},
		{
			"the declared block count disagrees with the trailer length",
			func(raw []byte, f zsavFields) { putI32(raw, f.trailer+20, int32(f.blocks+1)) },
			0, "inconsistent with itself",
		},
		{
			"a non-positive block size",
			func(raw []byte, f zsavFields) { putI32(raw, f.trailer+16, 0) },
			0, "uncompressed block size",
		},
		{
			"the first block does not start where the header ends",
			func(raw []byte, f zsavFields) { putI64(raw, f.entry(0)+8, int64(f.zheader+zheaderSize+1)) },
			1, "declares compressed offset",
		},
		{
			"the first block's uncompressed offset is not the header's",
			func(raw []byte, f zsavFields) { putI64(raw, f.entry(0), int64(f.zheader+1)) },
			1, "declares uncompressed offset",
		},
		{
			"a later block leaves a gap in the compressed offsets",
			func(raw []byte, f zsavFields) { putI64(raw, f.entry(2)+8, getI64(raw, f.entry(2)+8)+1) },
			3, "declares compressed offset",
		},
		{
			"a later block leaves a gap in the uncompressed offsets",
			func(raw []byte, f zsavFields) { putI64(raw, f.entry(2), getI64(raw, f.entry(2))+1) },
			3, "declares uncompressed offset",
		},
		{
			"a block declares no uncompressed bytes",
			func(raw []byte, f zsavFields) { putI32(raw, f.entry(1)+16, 0) },
			2, "uncompressed size of 0",
		},
		{
			"a block declares no compressed bytes",
			func(raw []byte, f zsavFields) { putI32(raw, f.entry(1)+20, 0) },
			2, "compressed size of 0",
		},
		{
			"a block is wider than the declared block size",
			func(raw []byte, f zsavFields) { putI32(raw, f.trailer+16, getI32(raw, f.entry(0)+16)-1) },
			1, "over the",
		},
		{
			"a block claims more than DEFLATE can produce",
			func(raw []byte, f zsavFields) {
				putI32(raw, f.trailer+16, 1<<30)
				putI32(raw, f.entry(0)+16, 1<<28)
			},
			1, "worst case",
		},
		{
			"the last block stops short of the trailer",
			func(raw []byte, f zsavFields) {
				last := f.entry(f.blocks - 1)
				putI32(raw, last+20, getI32(raw, last+20)-1)
			},
			-1, "do not fill the compressed region",
		},
	}

	base := build(t, zsavSpec(multiBlockSpec(), 64))
	if f := locateZSAV(t, base); f.blocks < 4 {
		t.Fatalf("the fixture spans %d block(s); the index cases need at least 4", f.blocks)
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := append([]byte(nil), base...)
			tc.mutate(raw, locateZSAV(t, raw))

			err := NewReaderFromBytes(raw).ReadRows(context.Background(), func([]string) error {
				t.Error("a case was delivered from a corrupt block index")
				return nil
			})
			if err == nil {
				t.Fatal("a corrupt ZSAV block index read without error")
			}
			ce := codedError(t, err)
			if ce.Code != perr.PULSE_SPSS_ZSAV_INVALID {
				t.Fatalf("code = %s, want %s (message: %s)", ce.Code, perr.PULSE_SPSS_ZSAV_INVALID, ce.Message)
			}
			if !strings.Contains(ce.Message, tc.wantMsg) {
				t.Errorf("message = %q, want it to contain %q", ce.Message, tc.wantMsg)
			}
			assertDetails(t, ce, len(raw))
			want := tc.wantBlock
			if want < 0 {
				want = locateZSAV(t, raw).blocks
			}
			assertBlockNamed(t, ce, want)
		})
	}
}

// TestZSAV_TruncatedTrailer covers the index running past the end of the
// file, which is a transfer that was cut rather than a writer that was wrong
// — so it is PULSE_SPSS_DATA_TRUNCATED, the code the rest of the package uses
// for exactly that.
func TestZSAV_TruncatedTrailer(t *testing.T) {
	raw := build(t, zsavSpec(spsstest.ReferenceSpec(), 0))
	f := locateZSAV(t, raw)
	putI64(raw, f.zheader+16, getI64(raw, f.zheader+16)+ztrailerEntrySize)

	err := NewReaderFromBytes(raw).ReadRows(context.Background(), func([]string) error { return nil })
	ce := codedError(t, err)
	if ce.Code != perr.PULSE_SPSS_DATA_TRUNCATED {
		t.Fatalf("code = %s, want %s (message: %s)", ce.Code, perr.PULSE_SPSS_DATA_TRUNCATED, ce.Message)
	}
	assertDetails(t, ce, len(raw))
}

// TestZSAV_HeaderTruncated covers a file that ends inside the 24-byte ZHEADER
// itself — before there is any index to validate.
func TestZSAV_HeaderTruncated(t *testing.T) {
	full := build(t, zsavSpec(spsstest.ReferenceSpec(), 0))
	f := locateZSAV(t, full)
	for cut := 0; cut < zheaderSize; cut++ {
		raw := append([]byte(nil), full[:f.zheader+cut]...)
		err := NewReaderFromBytes(raw).ReadRows(context.Background(), func([]string) error { return nil })
		if err == nil {
			t.Fatalf("cut %d: a file ending inside the ZSAV header read without error", cut)
		}
		ce := codedError(t, err)
		if ce.Code != perr.PULSE_SPSS_DATA_TRUNCATED {
			t.Errorf("cut %d: code = %s, want %s", cut, ce.Code, perr.PULSE_SPSS_DATA_TRUNCATED)
		}
		assertDetails(t, ce, len(raw))
	}
}

// TestZSAV_NoBlocksIsNoCases covers an index that names no blocks at all.
// It is well-formed — there is simply nothing between the header and the
// trailer — and it must read as an empty cohort rather than as a fault, since
// refusing it would be inventing a rule the format does not state.
func TestZSAV_NoBlocksIsNoCases(t *testing.T) {
	raw := rezsav(t, build(t, zsavSpec(spsstest.ReferenceSpec(), 0)), 64,
		func([]byte) []byte { return nil })

	r := NewReaderFromBytes(raw)
	if f := locateZSAV(t, raw); f.blocks != 0 {
		t.Fatalf("the rebuilt fixture has %d block(s), want none", f.blocks)
	}
	rows := readAll(t, r)
	if len(rows) != 0 {
		t.Errorf("read %d case(s) from a blockless index: %q", len(rows), rows)
	}
	// The header still declares two cases, so the count mismatch is a
	// warning — not silence, and not a hard failure.
	if len(r.Warnings()) != 1 {
		t.Errorf("a blockless index raised %d warning(s), want the case-count mismatch", len(r.Warnings()))
	}
}

// ---------------------------------------------------------------------------
// Damaged blocks
// ---------------------------------------------------------------------------

// TestZSAV_BlockPayloadCorrupt covers damage to the compressed bytes
// themselves, with a coherent index around them. That is a different fault
// from a broken index — the offsets were right and the bytes at them are
// damaged — and it carries its own code so a user can tell "this file was
// built wrong" from "this file arrived wrong".
func TestZSAV_BlockPayloadCorrupt(t *testing.T) {
	base := build(t, zsavSpec(multiBlockSpec(), 64))
	f := locateZSAV(t, base)
	if f.blocks < 4 {
		t.Fatalf("the fixture spans %d block(s); this test needs at least 4", f.blocks)
	}

	cases := []struct {
		name  string
		block int // 1-based
	}{
		{"the first block", 1},
		{"a middle block", 3},
		{"the last block", f.blocks},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := append([]byte(nil), base...)
			e := f.entry(tc.block - 1)
			start := getI64(raw, e+8)
			size := getI32(raw, e+20)
			// Flip every byte of the block's payload except the
			// two-byte zlib header, so the stream still OPENS and
			// the failure is an inflate or a checksum rather than
			// a rejected header. Both are this code.
			for i := int64(2); i < int64(size); i++ {
				raw[start+i] ^= 0xFF
			}

			err := NewReaderFromBytes(raw).ReadRows(context.Background(), func([]string) error { return nil })
			if err == nil {
				t.Fatal("a damaged zlib block read without error")
			}
			ce := codedError(t, err)
			if ce.Code != perr.PULSE_SPSS_ZSAV_BLOCK_CORRUPT {
				t.Fatalf("code = %s, want %s (message: %s)", ce.Code, perr.PULSE_SPSS_ZSAV_BLOCK_CORRUPT, ce.Message)
			}
			assertDetails(t, ce, len(raw))
			assertBlockNamed(t, ce, tc.block)
		})
	}
}

// TestZSAV_BlockNotAZlibStream covers a block whose first bytes are not a
// zlib header at all — the shape a wrong compressed offset produces.
func TestZSAV_BlockNotAZlibStream(t *testing.T) {
	raw := build(t, zsavSpec(spsstest.ReferenceSpec(), 0))
	f := locateZSAV(t, raw)
	start := getI64(raw, f.entry(0)+8)
	raw[start] = 0x00
	raw[start+1] = 0x00

	err := NewReaderFromBytes(raw).ReadRows(context.Background(), func([]string) error { return nil })
	ce := codedError(t, err)
	if ce.Code != perr.PULSE_SPSS_ZSAV_BLOCK_CORRUPT {
		t.Fatalf("code = %s, want %s (message: %s)", ce.Code, perr.PULSE_SPSS_ZSAV_BLOCK_CORRUPT, ce.Message)
	}
	if !strings.Contains(ce.Message, "zlib stream") {
		t.Errorf("message = %q, want it to say the block does not open as a zlib stream", ce.Message)
	}
	assertBlockNamed(t, ce, 1)
}

// TestZSAV_BlockSizeDisagreesWithItsPayload covers an entry whose declared
// uncompressed size is not what its block actually inflates to.
//
// Both directions are fatal and for the same reason: the blocks concatenate
// into one command stream, so a block that yields the wrong number of bytes
// shifts every element after it onto the wrong variable. The long case is the
// one worth having — it is invisible unless the reader deliberately reads
// past the length it was promised.
func TestZSAV_BlockSizeDisagreesWithItsPayload(t *testing.T) {
	cases := []struct {
		name    string
		delta   int32
		wantMsg string
	}{
		{"the entry declares fewer bytes than the block holds", -1, "inflates to more than"},
		{"the entry declares more bytes than the block holds", +1, "inflated to"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := build(t, zsavSpec(spsstest.ReferenceSpec(), 0))
			f := locateZSAV(t, raw)
			putI32(raw, f.entry(0)+16, getI32(raw, f.entry(0)+16)+tc.delta)

			err := NewReaderFromBytes(raw).ReadRows(context.Background(), func([]string) error { return nil })
			if err == nil {
				t.Fatal("a block that disagrees with its entry read without error")
			}
			ce := codedError(t, err)
			if ce.Code != perr.PULSE_SPSS_ZSAV_BLOCK_CORRUPT {
				t.Fatalf("code = %s, want %s (message: %s)", ce.Code, perr.PULSE_SPSS_ZSAV_BLOCK_CORRUPT, ce.Message)
			}
			if !strings.Contains(ce.Message, tc.wantMsg) {
				t.Errorf("message = %q, want it to contain %q", ce.Message, tc.wantMsg)
			}
			assertBlockNamed(t, ce, 1)
		})
	}
}

// ---------------------------------------------------------------------------
// The inner layer
// ---------------------------------------------------------------------------

// TestZSAV_InnerStreamFaultsAreBytecodeFaults is the layering assertion at the
// reader. The blocks inflate to a bytecode command stream, so a stream that
// contradicts the dictionary or stops mid-case must raise the SAME codes it
// would in an uncompressed `.sav` — not a ZSAV code. Reporting a desynchronised
// command as a compression fault would send a user to look at the zlib layer,
// which is intact.
func TestZSAV_InnerStreamFaultsAreBytecodeFaults(t *testing.T) {
	base := build(t, zsavSpec(spsstest.ReferenceSpec(), 8))

	t.Run("a command that contradicts the dictionary", func(t *testing.T) {
		raw := rezsav(t, base, 8, func(s []byte) []byte {
			out := append([]byte(nil), s...)
			out[0] = cmdSpaces // element 1 of a case is numeric
			return out
		})
		err := NewReaderFromBytes(raw).ReadRows(context.Background(), func([]string) error { return nil })
		ce := codedError(t, err)
		if ce.Code != perr.PULSE_SPSS_COMPRESSION_INVALID {
			t.Fatalf("code = %s, want %s (message: %s)", ce.Code, perr.PULSE_SPSS_COMPRESSION_INVALID, ce.Message)
		}
		assertDetails(t, ce, len(raw))
	})

	t.Run("a stream that ends mid-case", func(t *testing.T) {
		raw := rezsav(t, base, 8, func(s []byte) []byte {
			// Keep the first three commands of the first case and
			// drop everything after them, including the end-of-file
			// command. The reference fixture is four elements per
			// case, so the stream stops inside one.
			out := append([]byte(nil), s[:commandBlockSize]...)
			for i := 3; i < commandBlockSize; i++ {
				out[i] = cmdPad
			}
			return out
		})
		err := NewReaderFromBytes(raw).ReadRows(context.Background(), func([]string) error { return nil })
		ce := codedError(t, err)
		if ce.Code != perr.PULSE_SPSS_DATA_TRUNCATED {
			t.Fatalf("code = %s, want %s (message: %s)", ce.Code, perr.PULSE_SPSS_DATA_TRUNCATED, ce.Message)
		}
		assertDetails(t, ce, len(raw))
	})
}

// TestZSAV_LocatorNamesTheBlockThatHeldTheByte pins how an inner-stream
// position is reported.
//
// There is no exact answer: the command stream is a buffer that exists only in
// memory, and a byte in it was produced by decompression rather than read from
// disk. The file offset of the block that produced it is the closest true
// statement available, and it is what a user can act on. A position past the
// end of the stream reports where the compressed data stops.
func TestZSAV_LocatorNamesTheBlockThatHeldTheByte(t *testing.T) {
	raw := build(t, zsavSpec(multiBlockSpec(), 64))
	d := mustParse(t, raw)
	idx, err := readZSAVIndex(d, raw)
	if err != nil {
		t.Fatalf("readZSAVIndex: %v", err)
	}
	if len(idx.blocks) < 3 {
		t.Fatalf("the fixture spans %d block(s); this test needs at least 3", len(idx.blocks))
	}

	loc := idx.locator()
	inner := 0
	for i, b := range idx.blocks {
		for _, at := range []int{inner, inner + int(b.uncompressedSize) - 1} {
			if got := loc(at); got != int(b.compressedOfs) {
				t.Errorf("locator(%d) = %d, want block %d's compressed offset %d", at, got, i+1, b.compressedOfs)
			}
		}
		inner += int(b.uncompressedSize)
	}
	if got := loc(inner); got != int(idx.ztrailerOfs) {
		t.Errorf("locator past the end = %d, want the trailer offset %d", got, idx.ztrailerOfs)
	}
	if got := loc(inner + 1_000_000); got != int(idx.ztrailerOfs) {
		t.Errorf("locator far past the end = %d, want the trailer offset %d", got, idx.ztrailerOfs)
	}
}

// ---------------------------------------------------------------------------
// Never a panic
// ---------------------------------------------------------------------------

// TestZSAV_TruncatedAnywhere sweeps every cut position across the whole ZSAV
// data section. A `.zsav` is the encoding most likely to arrive half-copied —
// it is the compact one, so it is the one people move — and every prefix of it
// must be a coded error rather than a panic or a plausible cohort.
func TestZSAV_TruncatedAnywhere(t *testing.T) {
	full := build(t, zsavSpec(multiBlockSpec(), 64))
	start := mustParse(t, full).dataOffset

	for cut := start; cut < len(full); cut++ {
		raw := full[:cut]
		err := NewReaderFromBytes(raw).ReadRows(context.Background(), func([]string) error { return nil })
		if err == nil {
			t.Fatalf("cut at %d of %d read without error", cut, len(full))
		}
		ce := codedError(t, err)
		switch ce.Code {
		case perr.PULSE_SPSS_DATA_TRUNCATED, perr.PULSE_SPSS_ZSAV_INVALID,
			perr.PULSE_SPSS_ZSAV_BLOCK_CORRUPT, perr.PULSE_SPSS_COMPRESSION_INVALID:
		default:
			t.Fatalf("cut at %d: code = %s, want a truncation or ZSAV code (message: %s)", cut, ce.Code, ce.Message)
		}
		assertDetails(t, ce, len(raw))
	}
}

// TestZSAV_DoesNotPanicOnArbitraryBytes is the blunt no-panic guard, at the
// bar E3-S1 set for the bytecode path: the whole data section is replaced with
// every one of the 256 possible byte fills, and each must produce a cohort or
// a coded error.
func TestZSAV_DoesNotPanicOnArbitraryBytes(t *testing.T) {
	base := build(t, zsavSpec(multiBlockSpec(), 64))
	start := mustParse(t, base).dataOffset

	for fill := 0; fill < 256; fill++ {
		raw := append([]byte(nil), base...)
		for i := start; i < len(raw); i++ {
			raw[i] = byte(fill)
		}
		err := NewReaderFromBytes(raw).ReadRows(context.Background(), func([]string) error { return nil })
		if err == nil {
			continue
		}
		ce := codedError(t, err)
		assertDetails(t, ce, len(raw))
	}
}

// TestZSAV_IndexFieldFuzz walks every int64 and int32 field of the block index
// and replaces it with values chosen to break arithmetic: zero, negative,
// and the extremes of both widths. Nothing here may panic, and nothing may
// read as a cohort with the wrong number of cases.
func TestZSAV_IndexFieldFuzz(t *testing.T) {
	base := build(t, zsavSpec(multiBlockSpec(), 64))
	f := locateZSAV(t, base)

	wide := []int64{0, -1, 1, 1 << 40, -(1 << 40), 1<<63 - 1, -1 << 63}
	narrow := []int32{0, -1, 1, 1<<31 - 1, -1 << 31, 1 << 20}

	var offsets64, offsets32 []int
	offsets64 = append(offsets64, f.zheader, f.zheader+8, f.zheader+16, f.trailer, f.trailer+8)
	offsets32 = append(offsets32, f.trailer+16, f.trailer+20)
	for i := 0; i < f.blocks; i++ {
		offsets64 = append(offsets64, f.entry(i), f.entry(i)+8)
		offsets32 = append(offsets32, f.entry(i)+16, f.entry(i)+20)
	}

	want := len(readAll(t, NewReaderFromBytes(base)))
	check := func(raw []byte) {
		t.Helper()
		r := NewReaderFromBytes(raw)
		var n int
		err := r.ReadRows(context.Background(), func([]string) error { n++; return nil })
		if err != nil {
			assertDetails(t, codedError(t, err), len(raw))
			return
		}
		if n != want {
			t.Fatalf("a mutated index read %d case(s) without error, want %d", n, want)
		}
	}

	for _, off := range offsets64 {
		for _, v := range wide {
			raw := append([]byte(nil), base...)
			putI64(raw, off, v)
			check(raw)
		}
	}
	for _, off := range offsets32 {
		for _, v := range narrow {
			raw := append([]byte(nil), base...)
			putI32(raw, off, v)
			check(raw)
		}
	}
}

// ---------------------------------------------------------------------------
// Shared assertions
// ---------------------------------------------------------------------------

// assertBlockNamed checks that a ZSAV diagnostic names the block it is about,
// in both the message and the details. want is 1-based; 0 means the fault
// implicates no single block and neither must claim one.
func assertBlockNamed(t *testing.T, ce *perr.CodedError, want int) {
	t.Helper()
	got, present := ce.Details[perr.DetailSPSSBlock]
	if want == 0 {
		if present {
			t.Errorf("Details[%q] = %v, but this fault implicates no single block", perr.DetailSPSSBlock, got)
		}
		return
	}
	n, ok := got.(int)
	if !ok {
		t.Fatalf("Details[%q] = %v (%T), want an int", perr.DetailSPSSBlock, got, got)
	}
	if n != want {
		t.Errorf("Details[%q] = %d, want block %d", perr.DetailSPSSBlock, n, want)
	}
	if !strings.Contains(ce.Message, "ZSAV block ") {
		t.Errorf("message = %q, want it to name the ZSAV block", ce.Message)
	}
}
