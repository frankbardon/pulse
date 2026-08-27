package spsstest

// ZSAV emission: the zlib-blocked data section SPSS 21+ writes into a `.zsav`.
//
// # Two layers, and the emitter says so
//
// The zlib blocks do NOT hold case data. They hold the bytecode command
// stream — the exact bytes [writeBytecodeStream] emits in the clear for a
// CompressionBytecode file — cut into fixed-size pieces and deflated one
// piece at a time. Building a ZSAV here is therefore: emit the stream into a
// buffer, cut, compress, then write an index describing what was cut where.
//
// That layering is why the same spec built as bytecode and as ZSAV must yield
// the same cohort: the inner bytes are literally identical, and only their
// packaging differs.
//
// # The index
//
// A 24-byte ZHEADER opens the data section (its own offset, the trailer's
// offset, the trailer's length), the compressed blocks follow, and the
// ZTRAILER closes the file: a 24-byte head (the bias negated, a reserved
// zero, the uncompressed block size, the block count) plus one 24-byte entry
// per block giving that block's offset and size in BOTH coordinate spaces —
// where it starts in the file, and where it would start if the file were not
// compressed.
//
// Every number is computed from what was actually emitted. Nothing here is
// declared and then produced: the entries are built from the byte counts the
// compressor returned, so the index cannot disagree with the blocks it
// describes. A fixture with a deliberately corrupt index is therefore
// something a test constructs by mutating output, not something this emitter
// can be asked for — which is the right way round, because a generator that
// could emit an inconsistent index would make an inconsistent index look
// legitimate.
//
// # The negated bias, recorded as the guess it is
//
// The ZTRAILER's leading int64 is written as the NEGATION of the header's
// bias field (bias 100 → -100), following the PSPP specification's note that
// this is what PSPP writes. It is a redundant copy of a float64 in an int64
// slot, and no reader needs it — the bytecode layer takes its bias from the
// header. It is emitted in the conventional form so a fixture looks ordinary
// to an outside reader, and Pulse's own reader deliberately does not check
// it; see the "What is deliberately NOT validated" note in io/spss/zsav.go.
//
// # Determinism
//
// The deflate level is pinned at [ZSAVCompressionLevel] rather than left at
// the standard library's DefaultCompression sentinel, whose meaning the
// toolchain is free to change. Blocks are compressed independently and in
// order, so the same spec always produces the same bytes.

import (
	"bytes"
	"compress/zlib"
	"fmt"
)

const (
	// zheaderSize is the fixed part at the head of a ZSAV data section.
	zheaderSize = 24

	// ztrailerHeadSize is the fixed part of the ZTRAILER, before its
	// per-block entries.
	ztrailerHeadSize = 24

	// ztrailerEntrySize is one block-index entry.
	ztrailerEntrySize = 24
)

// zsavBlock is one emitted block: what went in, and what came out.
type zsavBlock struct {
	uncompressedOfs  int64
	compressedOfs    int64
	uncompressedSize int32
	compressedSize   int32
	compressed       []byte
}

// writeZSAVData emits the whole ZSAV data section: header, blocks, trailer.
//
// It is called with e positioned at the first byte after the record type 999
// terminator, so e.buf.Len() is the data section's own offset — which is
// exactly the zheader_ofs the format wants recorded.
func writeZSAVData(e *enc, p plan) {
	inner := &enc{bo: e.bo}
	writeBytecodeStream(inner, p)
	if inner.err != nil {
		e.fail(inner.err)
		return
	}
	stream := inner.buf.Bytes()

	zheaderOfs := int64(e.buf.Len())
	blocks, err := compressZSAVBlocks(stream, p.zsavBlockSize(), zheaderOfs)
	if err != nil {
		e.fail(err)
		return
	}

	var compressedTotal int64
	for _, b := range blocks {
		compressedTotal += int64(b.compressedSize)
	}
	ztrailerOfs := zheaderOfs + zheaderSize + compressedTotal
	ztrailerLen := int64(ztrailerHeadSize + len(blocks)*ztrailerEntrySize)

	// ZHEADER.
	e.i64(zheaderOfs)
	e.i64(ztrailerOfs)
	e.i64(ztrailerLen)

	// The blocks, in index order, back to back.
	for _, b := range blocks {
		e.raw(b.compressed)
	}

	// ZTRAILER: the fixed head, then one entry per block.
	e.i64(int64(-p.bias())) // the negated bias — see the file comment
	e.i64(0)                // reserved
	e.i32(int32(p.zsavBlockSize()))
	e.i32(int32(len(blocks)))
	for _, b := range blocks {
		e.i64(b.uncompressedOfs)
		e.i64(b.compressedOfs)
		e.i32(b.uncompressedSize)
		e.i32(b.compressedSize)
	}
}

// compressZSAVBlocks cuts stream into blockSize pieces and deflates each one,
// filling in both offset series as it goes.
//
// The uncompressed offsets are measured from zheaderOfs, not from zero: the
// format defines them as the offsets the blocks WOULD have in an equivalent
// file that was not compressed, and in such a file the data section still
// starts where the dictionary left off.
func compressZSAVBlocks(stream []byte, blockSize int, zheaderOfs int64) ([]zsavBlock, error) {
	if blockSize <= 0 {
		return nil, fmt.Errorf("spsstest: ZSAV block size %d is not positive", blockSize)
	}

	uncompressedOfs := zheaderOfs
	compressedOfs := zheaderOfs + zheaderSize

	var blocks []zsavBlock
	for start := 0; start < len(stream); start += blockSize {
		end := start + blockSize
		if end > len(stream) {
			end = len(stream)
		}
		chunk := stream[start:end]

		var buf bytes.Buffer
		zw, err := zlib.NewWriterLevel(&buf, ZSAVCompressionLevel)
		if err != nil {
			return nil, fmt.Errorf("spsstest: opening a zlib writer at level %d: %w", ZSAVCompressionLevel, err)
		}
		if _, err := zw.Write(chunk); err != nil {
			return nil, fmt.Errorf("spsstest: compressing ZSAV block %d: %w", len(blocks)+1, err)
		}
		if err := zw.Close(); err != nil {
			return nil, fmt.Errorf("spsstest: closing ZSAV block %d: %w", len(blocks)+1, err)
		}

		blocks = append(blocks, zsavBlock{
			uncompressedOfs:  uncompressedOfs,
			compressedOfs:    compressedOfs,
			uncompressedSize: int32(len(chunk)),
			compressedSize:   int32(buf.Len()),
			compressed:       buf.Bytes(),
		})
		uncompressedOfs += int64(len(chunk))
		compressedOfs += int64(buf.Len())
	}
	return blocks, nil
}
