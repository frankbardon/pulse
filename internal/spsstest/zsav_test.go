package spsstest

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"io"
	"math"
	"testing"
)

// decodedIndex is a ZSAV block index read back out of an emitted fixture by
// an INDEPENDENT parse — a plain little-endian walk written from the
// specification, not a call into the emitter that produced the bytes. Every
// assertion in this file is made against these numbers rather than against
// the plan the emitter worked from, so an emitter that computed a consistent
// but wrong index would still be caught.
type decodedIndex struct {
	zheaderOfs  int64
	ztrailerOfs int64
	ztrailerLen int64
	bias        int64
	zero        int64
	blockSize   int32
	nBlocks     int32
	entries     []decodedEntry
}

type decodedEntry struct {
	uncompressedOfs  int64
	compressedOfs    int64
	uncompressedSize int32
	compressedSize   int32
}

// readIndex parses the ZHEADER at the start of the data section and the
// ZTRAILER it points at.
func readIndex(t *testing.T, raw []byte) decodedIndex {
	t.Helper()
	dataOfs := len(raw) - len(dataSection(t, raw))
	if len(raw)-dataOfs < zheaderSize {
		t.Fatalf("the data section is %d byte(s), too short for a %d-byte ZSAV header", len(raw)-dataOfs, zheaderSize)
	}
	u64 := func(off int) int64 { return int64(binary.LittleEndian.Uint64(raw[off : off+8])) }
	i32 := func(off int) int32 { return int32(binary.LittleEndian.Uint32(raw[off : off+4])) }

	idx := decodedIndex{
		zheaderOfs:  u64(dataOfs),
		ztrailerOfs: u64(dataOfs + 8),
		ztrailerLen: u64(dataOfs + 16),
	}
	if idx.ztrailerOfs < 0 || idx.ztrailerOfs+idx.ztrailerLen > int64(len(raw)) {
		t.Fatalf("the trailer at %d for %d byte(s) does not fit a %d-byte file", idx.ztrailerOfs, idx.ztrailerLen, len(raw))
	}
	tr := int(idx.ztrailerOfs)
	idx.bias = u64(tr)
	idx.zero = u64(tr + 8)
	idx.blockSize = i32(tr + 16)
	idx.nBlocks = i32(tr + 20)
	for i := int32(0); i < idx.nBlocks; i++ {
		e := tr + ztrailerHeadSize + int(i)*ztrailerEntrySize
		idx.entries = append(idx.entries, decodedEntry{
			uncompressedOfs:  u64(e),
			compressedOfs:    u64(e + 8),
			uncompressedSize: i32(e + 16),
			compressedSize:   i32(e + 20),
		})
	}
	return idx
}

// inflateBlocks decompresses every block the index names and concatenates the
// results — the command stream a reader has to reconstruct.
func inflateBlocks(t *testing.T, raw []byte, idx decodedIndex) []byte {
	t.Helper()
	var out []byte
	for i, e := range idx.entries {
		src := raw[e.compressedOfs : e.compressedOfs+int64(e.compressedSize)]
		zr, err := zlib.NewReader(bytes.NewReader(src))
		if err != nil {
			t.Fatalf("block %d does not open as a zlib stream: %v", i+1, err)
		}
		got, err := io.ReadAll(zr)
		if err != nil {
			t.Fatalf("block %d failed to inflate: %v", i+1, err)
		}
		if err := zr.Close(); err != nil {
			t.Fatalf("block %d failed its zlib checksum: %v", i+1, err)
		}
		if int32(len(got)) != e.uncompressedSize {
			t.Errorf("block %d inflated to %d byte(s), but its entry declares %d", i+1, len(got), e.uncompressedSize)
		}
		out = append(out, got...)
	}
	return out
}

// zsavSpec is the reference fixture asked for as a ZSAV.
func zsavSpec() Spec {
	spec := ReferenceSpec()
	spec.Compression = CompressionZSAV
	return spec
}

// TestZSAV_MagicIsFL3 pins the one dictionary-level difference a ZSAV makes.
// The format marks a zlib-compressed file with "$FL3" rather than "$FL2", and
// R's foreign — which does not implement ZSAV — refuses such a file on the
// magic alone, so getting it wrong would produce a file that outside readers
// classify as something else entirely.
func TestZSAV_MagicIsFL3(t *testing.T) {
	cases := []struct {
		compression Compression
		want        string
	}{
		{CompressionNone, "$FL2"},
		{CompressionBytecode, "$FL2"},
		{CompressionZSAV, "$FL3"},
	}
	for _, tc := range cases {
		t.Run(tc.compression.String(), func(t *testing.T) {
			spec := ReferenceSpec()
			spec.Compression = tc.compression
			raw, err := Build(spec)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if got := string(raw[:4]); got != tc.want {
				t.Errorf("header magic = %q, want %q", got, tc.want)
			}
			if got := int32(binary.LittleEndian.Uint32(raw[offCompressionField:])); got != int32(tc.compression) {
				t.Errorf("compression field = %d, want %d", got, int32(tc.compression))
			}
		})
	}
}

// TestZSAV_InnerStreamIsTheBytecodeStream is the two-layer criterion at the
// generator, and the reason the emitter shares writeBytecodeStream rather
// than growing a second one.
//
// The blocks do NOT hold case data. They hold the bytecode command stream —
// byte for byte the same stream a CompressionBytecode build of the same spec
// puts in the clear. A ZSAV that inflated to raw 8-byte elements would read
// as plausible numbers under any decoder, which is exactly the failure this
// pins shut.
func TestZSAV_InnerStreamIsTheBytecodeStream(t *testing.T) {
	spec := ReferenceSpec()
	spec.Compression = CompressionBytecode
	packed, err := Build(spec)
	if err != nil {
		t.Fatalf("Build(bytecode): %v", err)
	}
	wantStream := dataSection(t, packed)

	raw, err := Build(zsavSpec())
	if err != nil {
		t.Fatalf("Build(zsav): %v", err)
	}
	got := inflateBlocks(t, raw, readIndex(t, raw))

	if !bytes.Equal(got, wantStream) {
		t.Errorf("the inflated ZSAV stream =\n% X\nwant the bytecode data section\n% X", got, wantStream)
	}
}

// TestZSAV_IndexDescribesTheFile checks every invariant the block index
// asserts about itself. The redundancy is the point of the structure: the
// entries state each block's position twice, in two coordinate spaces, and a
// reader validates them precisely because a writer could get them wrong.
func TestZSAV_IndexDescribesTheFile(t *testing.T) {
	specs := []struct {
		name string
		spec Spec
	}{
		{"one block", zsavSpec()},
		{"many blocks", multiBlockSpec()},
	}
	for _, tc := range specs {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := Build(tc.spec)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			idx := readIndex(t, raw)
			dataOfs := int64(len(raw) - len(dataSection(t, raw)))

			if idx.zheaderOfs != dataOfs {
				t.Errorf("zheader_ofs = %d, want the data section's own offset %d", idx.zheaderOfs, dataOfs)
			}
			if want := int64(ztrailerHeadSize + len(idx.entries)*ztrailerEntrySize); idx.ztrailerLen != want {
				t.Errorf("ztrailer_len = %d, want %d for %d block(s)", idx.ztrailerLen, want, len(idx.entries))
			}
			if got := idx.ztrailerOfs + idx.ztrailerLen; got != int64(len(raw)) {
				t.Errorf("ztrailer_ofs + ztrailer_len = %d, want the file size %d", got, len(raw))
			}
			if idx.zero != 0 {
				t.Errorf("the trailer's reserved field = %d, want 0", idx.zero)
			}
			if len(idx.entries) == 0 {
				t.Fatal("the index names no blocks; every fixture has at least the end-of-file command")
			}

			// The two offset series are cumulative and start at
			// stated places. Checking both is what proves the
			// emitter is not simply repeating one of them.
			wantUncompressed := idx.zheaderOfs
			wantCompressed := idx.zheaderOfs + zheaderSize
			for i, e := range idx.entries {
				if e.uncompressedOfs != wantUncompressed {
					t.Errorf("block %d uncompressed_ofs = %d, want %d", i+1, e.uncompressedOfs, wantUncompressed)
				}
				if e.compressedOfs != wantCompressed {
					t.Errorf("block %d compressed_ofs = %d, want %d", i+1, e.compressedOfs, wantCompressed)
				}
				if e.uncompressedSize <= 0 || e.uncompressedSize > idx.blockSize {
					t.Errorf("block %d uncompressed_size = %d, outside 1..%d", i+1, e.uncompressedSize, idx.blockSize)
				}
				if e.compressedSize <= 0 {
					t.Errorf("block %d compressed_size = %d, want a positive size", i+1, e.compressedSize)
				}
				wantUncompressed += int64(e.uncompressedSize)
				wantCompressed += int64(e.compressedSize)
			}
			if wantCompressed != idx.ztrailerOfs {
				t.Errorf("the blocks end at %d but the trailer begins at %d; they do not fill the compressed region", wantCompressed, idx.ztrailerOfs)
			}

			// Every block except possibly the last is full.
			for i, e := range idx.entries[:len(idx.entries)-1] {
				if e.uncompressedSize != idx.blockSize {
					t.Errorf("block %d is %d uncompressed byte(s), but only the last block may be short of the %d-byte block size", i+1, e.uncompressedSize, idx.blockSize)
				}
			}
		})
	}
}

// TestZSAV_TrailerBiasIsTheNegatedHeaderBias records a GUESS, explicitly.
//
// The specification says PSPP writes this int64 as the negation of the
// header's float64 bias field, and this emitter follows that. Nothing was
// found that requires a reader to check it, Pulse's own reader deliberately
// does not (it is a lossily-typed copy of a number the bytecode layer takes
// from the header instead), and ReadStat accepted the fixtures either way as
// far as this could be observed. The test exists so the convention is stated
// rather than accidental: if a real-world file is ever found writing it
// positive, this is the assertion to revisit.
func TestZSAV_TrailerBiasIsTheNegatedHeaderBias(t *testing.T) {
	cases := []float64{0, 37, 50}
	for _, bias := range cases {
		spec := zsavSpec()
		spec.CompressionBias = bias
		raw, err := Build(spec)
		if err != nil {
			t.Fatalf("Build(bias %v): %v", bias, err)
		}
		want := CompressionBias
		if bias != 0 {
			want = bias
		}
		if got := readIndex(t, raw).bias; got != int64(-want) {
			t.Errorf("bias %v: trailer bias = %d, want %d", bias, got, int64(-want))
		}
		// The header keeps the authoritative float64 either way.
		if got := binary.LittleEndian.Uint64(raw[offBiasField : offBiasField+8]); got != math.Float64bits(want) {
			t.Errorf("bias %v: the header's own bias field moved", bias)
		}
	}
}

// multiBlockSpec forces an index with several blocks by shrinking the block
// size. The conventional 0x3ff000 would put every fixture in this package in
// one block, and a one-block index exercises none of the cumulative-offset
// arithmetic a reader has to get right.
func multiBlockSpec() Spec {
	spec := Spec{
		Vars: []Var{
			{Name: "I", Print: Format{Type: FormatF, Width: 8}},
			{Name: "S", Width: 16},
		},
		Compression:   CompressionZSAV,
		ZSAVBlockSize: 24,
	}
	for i := 0; i < 40; i++ {
		spec.Cases = append(spec.Cases, []Value{Num(float64(i - 120)), Text(string(rune('A' + i%26)))})
	}
	return spec
}

// TestZSAV_BlockSizeDefaultsToTheConventionalValue keeps the default fixture
// as ordinary as possible. ZSAVBlockSize exists for tests; a fixture handed to
// an outside reader should look like what a real writer produces.
func TestZSAV_BlockSizeDefaultsToTheConventionalValue(t *testing.T) {
	raw, err := Build(zsavSpec())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	idx := readIndex(t, raw)
	if idx.blockSize != ZSAVBlockSize {
		t.Errorf("block_size = %d, want the conventional %d", idx.blockSize, ZSAVBlockSize)
	}
	if idx.nBlocks != 1 {
		t.Errorf("the reference fixture spans %d block(s); at the conventional block size it fits in one", idx.nBlocks)
	}
}

// TestZSAV_BlockSizeOverrideCutsTheStream is the capability the override
// exists for: several blocks, each full except the last.
func TestZSAV_BlockSizeOverrideCutsTheStream(t *testing.T) {
	spec := multiBlockSpec()
	raw, err := Build(spec)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	idx := readIndex(t, raw)
	if idx.blockSize != int32(spec.ZSAVBlockSize) {
		t.Errorf("block_size = %d, want the declared %d", idx.blockSize, spec.ZSAVBlockSize)
	}
	if idx.nBlocks < 3 {
		t.Fatalf("the fixture spans %d block(s); it exists to exercise a multi-block index", idx.nBlocks)
	}

	// The concatenation still has to be the whole bytecode stream.
	bytecodeSpec := spec
	bytecodeSpec.Compression = CompressionBytecode
	packed, err := Build(bytecodeSpec)
	if err != nil {
		t.Fatalf("Build(bytecode): %v", err)
	}
	if got, want := inflateBlocks(t, raw, idx), dataSection(t, packed); !bytes.Equal(got, want) {
		t.Errorf("the inflated stream is %d byte(s) and the bytecode data section %d; cutting into blocks changed the stream", len(got), len(want))
	}
}

// TestZSAV_Deterministic holds the package's central promise across the new
// code path. Compression is the one place a Go version bump could move the
// bytes, which is why the deflate level is pinned rather than left at the
// standard library's DefaultCompression sentinel.
func TestZSAV_Deterministic(t *testing.T) {
	for _, spec := range []Spec{zsavSpec(), multiBlockSpec()} {
		a, err := Build(spec)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		b, err := Build(spec)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		if !bytes.Equal(a, b) {
			t.Error("two builds of one ZSAV spec differ; the fixture is not byte-stable")
		}
	}
	if ZSAVCompressionLevel < zlib.BestSpeed || ZSAVCompressionLevel > zlib.BestCompression {
		t.Errorf("ZSAVCompressionLevel = %d, outside %d..%d; it must be an explicit level, never the DefaultCompression sentinel",
			ZSAVCompressionLevel, zlib.BestSpeed, zlib.BestCompression)
	}
}

// TestZSAV_DiffersFromItsTwinsOnTheWire is what makes a ZSAV fixture worth
// anything as a test input: the same logical cases must reach a reader as
// genuinely different bytes from both other encodings.
func TestZSAV_DiffersFromItsTwinsOnTheWire(t *testing.T) {
	spec := ReferenceSpec()
	plain, err := Build(spec)
	if err != nil {
		t.Fatalf("Build(uncompressed): %v", err)
	}
	spec.Compression = CompressionBytecode
	packed, err := Build(spec)
	if err != nil {
		t.Fatalf("Build(bytecode): %v", err)
	}
	spec.Compression = CompressionZSAV
	zipped, err := Build(spec)
	if err != nil {
		t.Fatalf("Build(zsav): %v", err)
	}

	z := dataSection(t, zipped)
	if bytes.Equal(z, dataSection(t, plain)) {
		t.Error("the ZSAV and uncompressed data sections are byte-identical")
	}
	if bytes.Equal(z, dataSection(t, packed)) {
		t.Error("the ZSAV and bytecode data sections are byte-identical; the blocks are not compressed")
	}
}

// TestZSAV_RejectsANegativeBlockSize keeps the emitter's refuse-rather-than-
// coerce discipline. A zero means the default; a negative is a mistake, and
// silently substituting the default would hide it.
func TestZSAV_RejectsANegativeBlockSize(t *testing.T) {
	spec := zsavSpec()
	spec.ZSAVBlockSize = -1
	if _, err := Build(spec); err == nil {
		t.Fatal("a negative ZSAV block size built without error")
	}
}

// TestZSAV_EmptyDataSection covers a file with no cases. The stream is still
// non-empty — the end-of-file command and its block padding are part of the
// encoding — so the index still names one block.
func TestZSAV_EmptyDataSection(t *testing.T) {
	spec := Spec{Vars: []Var{{Name: "A"}}, Compression: CompressionZSAV}
	raw, err := Build(spec)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	idx := readIndex(t, raw)
	if idx.nBlocks != 1 {
		t.Fatalf("n_blocks = %d, want 1", idx.nBlocks)
	}
	if got := inflateBlocks(t, raw, idx); len(got) != cmdBlockSize {
		t.Errorf("the inflated stream is %d byte(s), want the %d-byte end-of-file block", len(got), cmdBlockSize)
	}
}
