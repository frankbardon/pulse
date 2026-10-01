package spss

// ZSAV: zlib block compression, the encoding SPSS 21+ writes into a `.zsav`.
//
// # Two layers, not one
//
// This is the one thing to get right about ZSAV, and conflating the two
// layers is the classic misreading: the zlib blocks do NOT inflate to case
// data. They inflate to a BYTECODE COMMAND STREAM — the same stream a
// compression-flag-1 `.sav` carries in the clear, described in bytecode.go —
// and that stream is what produces elements.
//
//	file bytes ──▶ ZHEADER / ZTRAILER block index ──▶ N independent zlib
//	streams ──▶ concatenated command stream ──▶ decodeBytecodeStream ──▶
//	flat 8-byte elements ──▶ cases at the dictionary's stride
//
// So [decodeZSAV] inflates and then hands off; it never interprets an
// inflated byte itself. A reader that treated the inflated bytes as doubles
// would produce plausible numbers from every file, because command bytes ARE
// bytes and eight of them always make a double.
//
// Nothing in the header says which inner encoding the blocks carry: the
// compression flag has one value (2) for the whole scheme. Bytecode is what
// every writer of the format puts there and what PSPP and ReadStat both
// assume, so it is what this decodes — stated here because the alternative is
// a future reader assuming the flag distinguishes them.
//
// # The block index
//
// The data section opens with a 24-byte ZHEADER: the offset of the ZHEADER
// itself, the offset of the ZTRAILER, and the ZTRAILER's length. The ZTRAILER
// sits at the far end of the file, past every compressed block, and carries a
// 24-byte header (a redundant copy of the compression bias, a reserved zero,
// the uncompressed block size, and the block count) followed by one 24-byte
// entry per block:
//
//	int64 uncompressed_ofs   where this block would start if the file were not compressed
//	int64 compressed_ofs     where this block's zlib stream actually starts
//	int32 uncompressed_size  its inflated length
//	int32 compressed_size    its stored length
//
// Both offset series are absolute file offsets and both are cumulative: the
// first block starts at zheader_ofs uncompressed and zheader_ofs+24
// compressed, and every later block starts where its predecessor ended. That
// redundancy is the whole value of the index — it is checked rather than
// trusted ([zsavIndex.validateBlock]), because an entry that disagrees with its
// neighbours means the reader would inflate from an offset no writer ever
// wrote a stream at, and zlib would either fail or, worse, succeed on
// something.
//
// Each block is an INDEPENDENT zlib stream, complete with its own header and
// Adler-32 trailer. They are not chunks of one long stream, which is what
// makes a block-level integrity failure attributable to a block.
//
// # What is deliberately NOT validated
//
// The ZTRAILER's leading int64 bias is a redundant copy of the header's
// float64 bias field, written by PSPP as its NEGATION (bias 100 → -100). Two
// reasons it is read past rather than checked: it is lossily typed, so a file
// declaring a fractional bias cannot round-trip through it at all; and the
// bytecode layer takes its bias from the header field, which is the
// authoritative one. Rejecting a file over a redundant copy of a number we do
// not use would refuse data for a writer's convention. The reserved zero is
// skipped for the same reason — a reserved field that later gains a meaning
// must not break a reader that never needed it.
//
// # Diagnostics
//
// An index fault is PULSE_SPSS_ZSAV_INVALID and a damaged block is
// PULSE_SPSS_ZSAV_BLOCK_CORRUPT; both name the block, 1-based, in the message
// and under errors.DetailSPSSBlock. A fault the bytecode layer raises after
// inflation cannot name a file offset of its own — the position is inside a
// buffer that exists only in memory — so [zsavIndex.locator] reports the file
// offset of the block that HELD that byte, which is the nearest thing to it
// that exists on the user's disk.

import (
	"bytes"
	"compress/zlib"
	"io"

	"github.com/frankbardon/pulse/errors"
)

const (
	// zheaderSize is the fixed 24-byte ZHEADER at the head of a ZSAV data
	// section: zheader_ofs, ztrailer_ofs, ztrailer_len.
	zheaderSize = 24

	// ztrailerHeadSize is the fixed part of the ZTRAILER, before its
	// per-block entries: bias, zero, block_size, n_blocks.
	ztrailerHeadSize = 24

	// ztrailerEntrySize is one block-index entry.
	ztrailerEntrySize = 24

	// zsavConventionalBlockSize is the uncompressed block size every
	// writer of the format uses. It is NOT required: block_size is
	// carried per file, so the declared value is what a reader honours.
	// The constant exists to document the convention, not to enforce it.
	zsavConventionalBlockSize = 0x3ff000

	// maxDeflateExpansion bounds how much larger an inflated block may be
	// than the bytes it came from. DEFLATE's worst case is 1032:1 — a
	// 258-byte back-reference in as few as two bits — so an entry
	// claiming more than this from its own compressed size is claiming
	// something no encoder could have produced.
	//
	// It is a guard on ALLOCATION, not a correctness rule: without it a
	// hand-written index could ask for gigabytes of buffer from a
	// kilobyte file, and the reader would exhaust memory before zlib got
	// the chance to disagree.
	maxDeflateExpansion = 1032
)

// zsavBlock is one entry of the block index.
type zsavBlock struct {
	uncompressedOfs  int64
	compressedOfs    int64
	uncompressedSize int32
	compressedSize   int32
}

// zsavIndex is the whole decoded ZHEADER + ZTRAILER pair.
type zsavIndex struct {
	zheaderOfs  int64
	ztrailerOfs int64
	ztrailerLen int64
	blockSize   int32
	blocks      []zsavBlock

	// totalUncompressed is the sum of the blocks' uncompressed sizes: the
	// length of the command stream inflation will produce. It is int64
	// because it is a sum of file-declared numbers and must be checked
	// against the platform's int before it becomes a length.
	totalUncompressed int64
}

// decodeZSAV expands a ZSAV data section into the flat case bytes the rest of
// the package reads.
//
// The two layers are explicit here and nowhere else: read the block index,
// inflate the blocks into one command stream, then hand that stream to the
// bytecode decoder. Each step's failures are its own — a broken index cannot
// be reported as a broken stream, and vice versa.
func decodeZSAV(d *dictionary, data []byte, plan *dataPlan) ([]byte, int, error) {
	idx, err := readZSAVIndex(d, data)
	if err != nil {
		return nil, 0, err
	}
	stream, err := idx.inflate(data)
	if err != nil {
		return nil, 0, err
	}
	return decodeBytecodeStream(d, plan, stream, idx.locator())
}

// readZSAVIndex reads and validates the ZHEADER and the ZTRAILER it points at.
//
// Every field is checked against the file it claims to describe before a
// single byte is inflated. That ordering matters: zlib is perfectly happy to
// inflate whatever sits at a wrong offset if it happens to look like a
// stream, so the offsets have to be proven first.
func readZSAVIndex(d *dictionary, data []byte) (*zsavIndex, error) {
	bo := d.byteOrder
	off := d.dataOffset
	if off < 0 || off > len(data) || len(data)-off < zheaderSize {
		return nil, dataError(errors.PULSE_SPSS_DATA_TRUNCATED, len(data),
			"the file ends before the %d-byte ZSAV header at byte offset %d; %d byte(s) remain",
			zheaderSize, off, len(data)-off)
	}

	idx := &zsavIndex{
		zheaderOfs:  int64(bo.Uint64(data[off : off+8])),
		ztrailerOfs: int64(bo.Uint64(data[off+8 : off+16])),
		ztrailerLen: int64(bo.Uint64(data[off+16 : off+24])),
	}

	// The ZHEADER states its own position. It is the cheapest possible
	// check that this index belongs to this file at this offset, and it
	// is the first thing to fail on a file whose dictionary was rewritten
	// without its data section being rebuilt.
	if idx.zheaderOfs != int64(off) {
		return nil, dataError(errors.PULSE_SPSS_ZSAV_INVALID, off,
			"the ZSAV header states it sits at byte offset %d, but the data section begins at %d; the block index does not describe this file",
			idx.zheaderOfs, off)
	}
	if idx.ztrailerLen < ztrailerHeadSize || (idx.ztrailerLen-ztrailerHeadSize)%ztrailerEntrySize != 0 {
		return nil, dataError(errors.PULSE_SPSS_ZSAV_INVALID, off+16,
			"the ZSAV header declares a %d-byte trailer; a trailer is %d bytes plus a whole number of %d-byte block entries",
			idx.ztrailerLen, ztrailerHeadSize, ztrailerEntrySize)
	}
	entriesFromLen := (idx.ztrailerLen - ztrailerHeadSize) / ztrailerEntrySize

	firstBlockOfs := idx.zheaderOfs + zheaderSize
	if idx.ztrailerOfs < firstBlockOfs {
		return nil, dataError(errors.PULSE_SPSS_ZSAV_INVALID, off+8,
			"the ZSAV header puts the trailer at byte offset %d, before the first block could start at %d",
			idx.ztrailerOfs, firstBlockOfs)
	}
	// The specification has the trailer end exactly at the end of the
	// file. Anything else means the reader and the writer disagree about
	// which bytes are the index, so it is refused rather than tolerated.
	if end := idx.ztrailerOfs + idx.ztrailerLen; end != int64(len(data)) {
		code := errors.PULSE_SPSS_ZSAV_INVALID
		if end > int64(len(data)) {
			code = errors.PULSE_SPSS_DATA_TRUNCATED
		}
		return nil, dataError(code, off+8,
			"the ZSAV trailer runs from byte offset %d for %d byte(s), ending at %d, but the file is %d byte(s) long",
			idx.ztrailerOfs, idx.ztrailerLen, end, len(data))
	}

	// The ZTRAILER's fixed part. The bias and the reserved zero are read
	// past deliberately — see the file comment.
	t := int(idx.ztrailerOfs)
	idx.blockSize = int32(bo.Uint32(data[t+16 : t+20]))
	nBlocks := int32(bo.Uint32(data[t+20 : t+24]))

	if int64(nBlocks) != entriesFromLen {
		return nil, dataError(errors.PULSE_SPSS_ZSAV_INVALID, t+20,
			"the ZSAV trailer declares %d block(s) but its declared length of %d byte(s) has room for %d; the block index is inconsistent with itself",
			nBlocks, idx.ztrailerLen, entriesFromLen)
	}
	if nBlocks > 0 && idx.blockSize <= 0 {
		return nil, dataError(errors.PULSE_SPSS_ZSAV_INVALID, t+16,
			"the ZSAV trailer declares an uncompressed block size of %d; it must be positive (writers use %d)",
			idx.blockSize, zsavConventionalBlockSize)
	}

	idx.blocks = make([]zsavBlock, nBlocks)
	for i := range idx.blocks {
		e := t + ztrailerHeadSize + i*ztrailerEntrySize
		idx.blocks[i] = zsavBlock{
			uncompressedOfs:  int64(bo.Uint64(data[e : e+8])),
			compressedOfs:    int64(bo.Uint64(data[e+8 : e+16])),
			uncompressedSize: int32(bo.Uint32(data[e+16 : e+20])),
			compressedSize:   int32(bo.Uint32(data[e+20 : e+24])),
		}
		if err := idx.validateBlock(i, e); err != nil {
			return nil, err
		}
		idx.totalUncompressed += int64(idx.blocks[i].uncompressedSize)
	}
	// The command stream becomes one Go slice, so its length has to fit
	// the platform's int. On 64-bit this is unreachable; on a 32-bit
	// build a large file could exceed it, and a silent wrap would turn
	// into a negative make() and a panic.
	if int64(int(idx.totalUncompressed)) != idx.totalUncompressed {
		return nil, dataError(errors.PULSE_SPSS_ZSAV_INVALID, t,
			"the ZSAV block index declares %d uncompressed byte(s) in total, more than this platform can address",
			idx.totalUncompressed)
	}

	// The blocks must fill the compressed region exactly. A gap would be
	// bytes no entry accounts for; an overlap would be bytes two entries
	// both claim.
	if n := len(idx.blocks); n > 0 {
		last := idx.blocks[n-1]
		if end := last.compressedOfs + int64(last.compressedSize); end != idx.ztrailerOfs {
			return nil, zsavBlockError(errors.PULSE_SPSS_ZSAV_INVALID, n, int(last.compressedOfs), n,
				"ends at byte offset %d, but the trailer begins at %d; the blocks do not fill the compressed region",
				end, idx.ztrailerOfs)
		}
	} else if idx.ztrailerOfs != firstBlockOfs {
		return nil, dataError(errors.PULSE_SPSS_ZSAV_INVALID, off+8,
			"the ZSAV trailer declares no blocks, but %d byte(s) sit between the header and the trailer",
			idx.ztrailerOfs-firstBlockOfs)
	}
	return idx, nil
}

// validateBlock checks entry i against its predecessor and against the file.
// entryOfs is the entry's own byte offset, so a diagnostic points at the
// bytes that are wrong rather than at the block they describe.
func (idx *zsavIndex) validateBlock(i, entryOfs int) error {
	b := idx.blocks[i]
	num := i + 1 // 1-based, matching the message and DetailSPSSBlock

	wantUncompressed := idx.zheaderOfs
	wantCompressed := idx.zheaderOfs + zheaderSize
	if i > 0 {
		prev := idx.blocks[i-1]
		wantUncompressed = prev.uncompressedOfs + int64(prev.uncompressedSize)
		wantCompressed = prev.compressedOfs + int64(prev.compressedSize)
	}
	if b.uncompressedOfs != wantUncompressed {
		return zsavBlockError(errors.PULSE_SPSS_ZSAV_INVALID, num, entryOfs, len(idx.blocks),
			"declares uncompressed offset %d, but the block before it ends at %d; the uncompressed offsets must run on without a gap",
			b.uncompressedOfs, wantUncompressed)
	}
	if b.compressedOfs != wantCompressed {
		return zsavBlockError(errors.PULSE_SPSS_ZSAV_INVALID, num, entryOfs+8, len(idx.blocks),
			"declares compressed offset %d, but the block before it ends at %d; the compressed offsets must run on without a gap",
			b.compressedOfs, wantCompressed)
	}
	if b.uncompressedSize <= 0 {
		return zsavBlockError(errors.PULSE_SPSS_ZSAV_INVALID, num, entryOfs+16, len(idx.blocks),
			"declares an uncompressed size of %d; every block holds at least one byte",
			b.uncompressedSize)
	}
	if b.uncompressedSize > idx.blockSize {
		return zsavBlockError(errors.PULSE_SPSS_ZSAV_INVALID, num, entryOfs+16, len(idx.blocks),
			"declares an uncompressed size of %d, over the %d-byte block size the trailer declares",
			b.uncompressedSize, idx.blockSize)
	}
	if b.compressedSize <= 0 {
		return zsavBlockError(errors.PULSE_SPSS_ZSAV_INVALID, num, entryOfs+20, len(idx.blocks),
			"declares a compressed size of %d; every block holds at least one byte",
			b.compressedSize)
	}
	if end := b.compressedOfs + int64(b.compressedSize); end > idx.ztrailerOfs {
		return zsavBlockError(errors.PULSE_SPSS_ZSAV_INVALID, num, entryOfs+20, len(idx.blocks),
			"runs from byte offset %d for %d compressed byte(s), ending at %d, past the trailer at %d",
			b.compressedOfs, b.compressedSize, end, idx.ztrailerOfs)
	}
	if int64(b.uncompressedSize) > int64(b.compressedSize)*maxDeflateExpansion {
		return zsavBlockError(errors.PULSE_SPSS_ZSAV_INVALID, num, entryOfs+16, len(idx.blocks),
			"claims %d byte(s) inflated from %d compressed, over DEFLATE's %d:1 worst case; no encoder could have written that",
			b.uncompressedSize, b.compressedSize, maxDeflateExpansion)
	}
	return nil
}

// inflate decompresses every block into one contiguous command stream.
//
// The output is materialised whole for the same reason the bytecode expansion
// is (see decodeBytecode): the schema mapping walks every case regardless, so
// a lazy block reader would buy nothing and would make the case geometry
// conditional on the compression flag.
func (idx *zsavIndex) inflate(data []byte) ([]byte, error) {
	out := make([]byte, 0, int(idx.totalUncompressed))
	total := len(idx.blocks)
	for i, b := range idx.blocks {
		num := i + 1
		src := data[b.compressedOfs : b.compressedOfs+int64(b.compressedSize)]

		zr, err := zlib.NewReader(bytes.NewReader(src))
		if err != nil {
			return nil, zsavBlockError(errors.PULSE_SPSS_ZSAV_BLOCK_CORRUPT, num, int(b.compressedOfs), total,
				"does not open as a zlib stream: %v", err)
		}
		buf := make([]byte, b.uncompressedSize)
		n, err := io.ReadFull(zr, buf)
		switch {
		case err == nil, err == io.EOF, err == io.ErrUnexpectedEOF:
			// A stream that ended early is reported by length below,
			// which says something more useful than "unexpected EOF".
		default:
			zr.Close()
			return nil, zsavBlockError(errors.PULSE_SPSS_ZSAV_BLOCK_CORRUPT, num, int(b.compressedOfs), total,
				"failed to inflate after %d of its declared %d byte(s): %v", n, b.uncompressedSize, err)
		}
		if int32(n) != b.uncompressedSize {
			zr.Close()
			return nil, zsavBlockError(errors.PULSE_SPSS_ZSAV_BLOCK_CORRUPT, num, int(b.compressedOfs), total,
				"inflated to %d byte(s) but its index entry declares %d; a short block shifts every later value onto the wrong variable",
				n, b.uncompressedSize)
		}

		// Reading one byte past the declared length does two jobs. It
		// catches a block that inflates to MORE than it declared — as
		// fatal as a short one, and invisible otherwise — and it drives
		// the stream to its end, which is where compress/zlib verifies
		// the Adler-32 checksum. Without this probe a block whose
		// payload is damaged but whose first N bytes still inflate
		// would pass unnoticed.
		var probe [1]byte
		extra, err := io.ReadFull(zr, probe[:])
		zr.Close()
		if extra > 0 {
			return nil, zsavBlockError(errors.PULSE_SPSS_ZSAV_BLOCK_CORRUPT, num, int(b.compressedOfs), total,
				"inflates to more than the %d byte(s) its index entry declares; a long block shifts every later value onto the wrong variable",
				b.uncompressedSize)
		}
		if err != io.EOF {
			return nil, zsavBlockError(errors.PULSE_SPSS_ZSAV_BLOCK_CORRUPT, num, int(b.compressedOfs), total,
				"did not end cleanly at its declared %d byte(s): %v", b.uncompressedSize, err)
		}
		out = append(out, buf...)
	}
	return out, nil
}

// locator maps a position in the inflated command stream onto a byte offset
// in the file.
//
// There is no exact answer: the stream is a buffer that exists only in
// memory, and the byte at index i was produced by decompression rather than
// read from disk. The file offset of the block that produced it is the
// closest true statement available, and it is what a user needs anyway —
// "block 3, which starts at 0x1a40" is actionable, an offset into a buffer
// they cannot see is not.
func (idx *zsavIndex) locator() streamLocator {
	base := idx.zheaderOfs
	return func(i int) int {
		for _, b := range idx.blocks {
			start := int(b.uncompressedOfs - base)
			if i < start+int(b.uncompressedSize) {
				return int(b.compressedOfs)
			}
		}
		// Past the end of the stream — the "ended mid-case" report.
		// The trailer offset is where the compressed data stops, which
		// is the honest end-of-data position in the file.
		return int(idx.ztrailerOfs)
	}
}

// zsavBlockError builds a ZSAV diagnostic that names a block.
//
// num is 1-based and lands both in the message and in Details under
// errors.DetailSPSSBlock, so a caller reading the details finds the same
// number a human reading the message does.
func zsavBlockError(code errors.Code, num, off, total int, format string, args ...any) *errors.CodedError {
	ce := dataError(code, off, "ZSAV block %d of %d "+format, append([]any{num, total}, args...)...)
	ce.Details[errors.DetailSPSSBlock] = num
	return ce
}
