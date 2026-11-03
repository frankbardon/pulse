package arrow

import (
	"bytes"
	"encoding/binary"
	stderrors "errors"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
)

// The Arrow IPC decoder sizes several allocations from lengths the file
// DECLARES rather than from the bytes it holds: the schema's field and
// child counts, the custom-metadata counts, and a compressed buffer's
// uncompressed length. A crafted file of a few bytes can declare
// billions, and the decoder allocates them before it reads a byte of
// the claimed content. preflightIPC walks the footer and every message
// first and refuses a declaration the source cannot honestly back, so
// what reaches ipc.NewFileReader allocates in proportion to the input.
//
// It checks only what bounds an allocation; everything else (offsets
// past the end, malformed tables) is left to the decoder, whose errors
// and panics the reader already codes.

// maxIPCNesting caps a schema's field nesting. A deeper tree would
// recurse the decoder (and the Pulse type mapping) towards a stack
// overflow, which no recover catches; Arrow C++ applies the same
// depth limit to IPC reads.
const maxIPCNesting = 64

// compressedClaimRatio is the uncompressed/compressed ratio under which
// a compressed buffer's declared size is trusted without decoding it:
// such a claim allocates at most this multiple of the source. A larger
// claim is verified by streaming the buffer through its codec, so an
// honest highly-compressible buffer still imports and a false one is
// refused.
const compressedClaimRatio = 16

// arrowMagic ends (and starts) every Arrow IPC file.
var arrowMagic = []byte("ARROW1")

// fb is a flatbuffer with bounds-checked reads: every accessor reports
// ok=false instead of indexing past the end.
type fb []byte

func (b fb) u32(at int) (uint32, bool) {
	if at < 0 || at > len(b)-4 {
		return 0, false
	}
	return binary.LittleEndian.Uint32(b[at:]), true
}

func (b fb) i64(at int) (int64, bool) {
	if at < 0 || at > len(b)-8 {
		return 0, false
	}
	return int64(binary.LittleEndian.Uint64(b[at:])), true
}

// fbTable is one flatbuffer table: its position and its vtable.
type fbTable struct {
	b       fb
	pos, vt int
	vtLen   int
}

// root returns the buffer's root table.
func (b fb) root() (fbTable, bool) {
	off, ok := b.u32(0)
	if !ok {
		return fbTable{}, false
	}
	return b.table(int(off))
}

// table reads the table at pos. Like the flatbuffers runtime it trusts
// the vtable's declared length and bounds only the entries it reads.
func (b fb) table(pos int) (fbTable, bool) {
	soff, ok := b.u32(pos)
	if !ok {
		return fbTable{}, false
	}
	vt := pos - int(int32(soff))
	if vt < 0 || vt > len(b)-2 {
		return fbTable{}, false
	}
	return fbTable{b: b, pos: pos, vt: vt, vtLen: int(binary.LittleEndian.Uint16(b[vt:]))}, true
}

// field returns the absolute position of slot's value, 0 when the slot
// is absent, or -1 when its vtable entry lies outside the buffer (the
// decoder would index past the end).
func (t fbTable) field(slot int) int {
	entry := 4 + 2*slot
	if entry >= t.vtLen {
		return 0
	}
	at := t.vt + entry
	if at > len(t.b)-2 {
		return -1
	}
	off := int(binary.LittleEndian.Uint16(t.b[at:]))
	if off == 0 {
		return 0
	}
	return t.pos + off
}

// ref follows the uoffset stored at slot; 0 when absent.
func (t fbTable) ref(slot int) (int, error) {
	at := t.field(slot)
	if at == 0 {
		return 0, nil
	}
	if at < 0 {
		return 0, errIPCBounds
	}
	off, ok := t.b.u32(at)
	if !ok {
		return 0, errIPCBounds
	}
	return at + int(off), nil // a target past the end fails its own read
}

func (t fbTable) sub(slot int) (fbTable, bool, error) {
	at, err := t.ref(slot)
	if err != nil || at == 0 {
		return fbTable{}, false, err
	}
	sub, ok := t.b.table(at)
	if !ok {
		return fbTable{}, false, errIPCBounds
	}
	return sub, true, nil
}

func (t fbTable) i64(slot int, def int64) (int64, error) {
	at := t.field(slot)
	if at == 0 {
		return def, nil
	}
	v, ok := t.b.i64(at)
	if at < 0 || !ok {
		return 0, errIPCBounds
	}
	return v, nil
}

func (t fbTable) u16(slot int, def uint16) (uint16, error) {
	at := t.field(slot)
	if at == 0 {
		return def, nil
	}
	if at < 0 || at > len(t.b)-2 {
		return 0, errIPCBounds
	}
	return binary.LittleEndian.Uint16(t.b[at:]), nil
}

func (t fbTable) u8(slot int, def byte) (byte, error) {
	at := t.field(slot)
	if at == 0 {
		return def, nil
	}
	if at < 0 || at >= len(t.b) {
		return 0, errIPCBounds
	}
	return t.b[at], nil
}

// vector returns the element start and declared count of the vector at
// slot, refusing a count whose elements could not fit in the buffer.
func (t fbTable) vector(slot, elemSize int, what string) (start, n int, err error) {
	at, err := t.ref(slot)
	if err != nil || at == 0 {
		return 0, 0, err
	}
	count, ok := t.b.u32(at)
	if !ok {
		return 0, 0, errIPCBounds
	}
	start = at + 4
	if uint64(count)*uint64(elemSize) > uint64(len(t.b)-start) {
		return 0, 0, fmt.Errorf("arrow IPC %s declares %d entries, more than its %d metadata bytes can hold", what, count, len(t.b))
	}
	return start, int(count), nil
}

// tables walks a vector of tables at slot, calling fn on each.
func (t fbTable) tables(slot int, what string, fn func(fbTable) error) error {
	start, n, err := t.vector(slot, 4, what)
	if err != nil {
		return err
	}
	for i := 0; i < n; i++ {
		at := start + 4*i
		off, _ := t.b.u32(at)
		sub, ok := t.b.table(at + int(off))
		if !ok {
			return errIPCBounds
		}
		if err := fn(sub); err != nil {
			return err
		}
	}
	return nil
}

var errIPCBounds = stderrors.New("arrow IPC metadata points outside its buffer")

// Flatbuffer slots of the Arrow IPC schema (format/File.fbs,
// format/Schema.fbs, format/Message.fbs).
const (
	footerSchema        = 1
	footerDictionaries  = 2
	footerRecordBatches = 3

	schemaFields         = 1
	schemaCustomMetadata = 2

	fieldChildren       = 5
	fieldCustomMetadata = 6

	messageHeaderType     = 1
	messageHeader         = 2
	messageCustomMetadata = 4

	headerDictionaryBatch = 2
	headerRecordBatch     = 3

	dictionaryBatchData = 1

	recordBatchNodes       = 1
	recordBatchBuffers     = 2
	recordBatchCompression = 3
	recordBatchVariadic    = 4

	bodyCompressionCodec = 0
	codecLZ4Frame        = 0
	codecZstd            = 1

	blockSize  = 24 // struct Block { offset: long; metaDataLength: int; bodyLength: long }
	nodeSize   = 16 // struct FieldNode { length: long; null_count: long }
	bufferSize = 16 // struct Buffer { offset: long; length: long }
)

// preflightIPC refuses an Arrow IPC file whose declared counts or
// compressed sizes the source cannot back. A layout it cannot follow is
// passed through for the decoder to refuse with its own error.
func preflightIPC(data []byte) error {
	const trailer = 10 // int32 footer length + "ARROW1"
	if len(data) < trailer+len(arrowMagic) || !bytes.Equal(data[len(data)-len(arrowMagic):], arrowMagic) {
		return nil
	}
	size := int64(binary.LittleEndian.Uint32(data[len(data)-trailer:]))
	end := int64(len(data)) - trailer
	if size <= 0 || size > end {
		return nil
	}
	footer := fb(data[end-size : end])
	root, ok := footer.root()
	if !ok {
		return nil
	}
	schema, ok, err := root.sub(footerSchema)
	if err != nil || !ok {
		return err
	}
	sc, err := checkSchema(schema)
	if err != nil {
		return err
	}
	for _, slot := range []int{footerDictionaries, footerRecordBatches} {
		start, n, err := root.vector(slot, blockSize, "footer block list")
		if err != nil {
			return err
		}
		for i := 0; i < n; i++ {
			at := start + blockSize*i
			off, _ := footer.i64(at)
			meta, _ := footer.u32(at + 8)
			body, _ := footer.i64(at + 16)
			if err := checkMessage(data, off, int64(int32(meta)), body, sc); err != nil {
				return fmt.Errorf("block %d: %w", i, err)
			}
		}
	}
	return nil
}

// ipcField is the slice of one schema field the decoder's buffer and
// node consumption depends on.
type ipcField struct {
	typ      byte  // flatbuffers Type union tag
	dense    bool  // a dense Union
	dictID   int64 // dictionary id; -1 when the field is not dictionary-encoded
	children []ipcField
}

// ipcSchema is the walked schema: its top-level fields, and every
// dictionary-encoded field by id (a dictionary batch carries that
// field's VALUES, typed by the field itself).
type ipcSchema struct {
	fields []ipcField
	dicts  map[int64]ipcField
	// known is false when a field's type could not be read; the
	// buffer-count check is then left to the decoder.
	known bool
}

// Flatbuffers Type union tags (format/Schema.fbs).
const (
	typeNull            = 1
	typeInt             = 2
	typeFloatingPoint   = 3
	typeBinary          = 4
	typeUtf8            = 5
	typeBool            = 6
	typeDecimal         = 7
	typeDate            = 8
	typeTime            = 9
	typeTimestamp       = 10
	typeInterval        = 11
	typeList            = 12
	typeStruct          = 13
	typeUnion           = 14
	typeFixedSizeBinary = 15
	typeFixedSizeList   = 16
	typeMap             = 17
	typeDuration        = 18
	typeLargeBinary     = 19
	typeLargeUtf8       = 20
	typeLargeList       = 21
	typeRunEndEncoded   = 22
	typeBinaryView      = 23
	typeUtf8View        = 24
	typeListView        = 25
	typeLargeListView   = 26

	fieldTypeType   = 2
	fieldType       = 3
	fieldDictionary = 4

	unionMode      = 0
	unionModeDense = 1

	dictionaryEncodingID = 0
	dictionaryBatchID    = 0

	messageVersion   = 0
	metadataVersion5 = 4
)

func checkSchema(schema fbTable) (*ipcSchema, error) {
	if _, _, err := schema.vector(schemaCustomMetadata, 4, "schema metadata"); err != nil {
		return nil, err
	}
	sc := &ipcSchema{dicts: map[int64]ipcField{}, known: true}
	err := schema.tables(schemaFields, "schema field list", func(f fbTable) error {
		fd, err := checkField(f, 1, sc)
		sc.fields = append(sc.fields, fd)
		return err
	})
	return sc, err
}

func checkField(f fbTable, depth int, sc *ipcSchema) (ipcField, error) {
	fd := ipcField{dictID: -1}
	if depth > maxIPCNesting {
		return fd, fmt.Errorf("arrow IPC schema nests fields deeper than %d levels", maxIPCNesting)
	}
	if _, _, err := f.vector(fieldCustomMetadata, 4, "field metadata"); err != nil {
		return fd, err
	}
	if err := f.tables(fieldChildren, "field child list", func(c fbTable) error {
		child, err := checkField(c, depth+1, sc)
		fd.children = append(fd.children, child)
		return err
	}); err != nil {
		return fd, err
	}
	typ, err := f.u8(fieldTypeType, 0)
	if err != nil || typ == 0 || typ > typeLargeListView {
		sc.known = false
		return fd, nil
	}
	fd.typ = typ
	if typ == typeUnion {
		if t, ok, err := f.sub(fieldType); err != nil || !ok {
			sc.known = false
		} else if mode, err := t.u16(unionMode, 0); err != nil {
			sc.known = false
		} else {
			fd.dense = mode == unionModeDense
		}
	}
	if d, ok, err := f.sub(fieldDictionary); err != nil {
		sc.known = false
	} else if ok {
		id, err := d.i64(dictionaryEncodingID, 0)
		if err != nil {
			sc.known = false
		} else {
			fd.dictID = id
			sc.dicts[id] = fd
		}
	}
	return fd, nil
}

// batchNeeds is what decoding one batch consumes: field nodes,
// buffers, and variadic buffer counts (one per view column).
type batchNeeds struct{ nodes, buffers, views int }

// needs adds what decoding one column of type f consumes, following
// the IPC array loader: one field node per array, a validity slot for
// every type that has one (always consumed, even when the null count
// lets the loader skip it), then the type's own buffers. A
// dictionary-encoded column is its index array; asValues reads the
// field as the dictionary batch carrying its values instead.
func (n *batchNeeds) add(f ipcField, v5, asValues bool) {
	n.nodes++
	if f.dictID >= 0 && !asValues {
		n.buffers += 2 // validity + indices
		return
	}
	validity := f.typ != typeNull
	if v5 && (f.typ == typeUnion || f.typ == typeRunEndEncoded) {
		validity = false
	}
	if validity {
		n.buffers++
	}
	switch f.typ {
	case typeInt, typeFloatingPoint, typeBool, typeDecimal, typeDate, typeTime,
		typeTimestamp, typeInterval, typeDuration, typeFixedSizeBinary,
		typeList, typeLargeList, typeMap:
		n.buffers++
	case typeBinary, typeUtf8, typeLargeBinary, typeLargeUtf8, typeListView, typeLargeListView:
		n.buffers += 2
	case typeBinaryView, typeUtf8View:
		n.buffers++ // + its variadic data buffers, counted by the caller
		n.views++
	case typeUnion:
		n.buffers++
		if f.dense {
			n.buffers++
		}
	}
	for _, c := range f.children {
		n.add(c, v5, false)
	}
}

// checkMessage checks one footer block's message: its custom-metadata
// count and, for a (dictionary) record batch, its node / buffer counts
// and every compressed buffer's declared size.
func checkMessage(data []byte, off, meta, body int64, sc *ipcSchema) error {
	n := int64(len(data))
	if off < 0 || meta < 8 || body < 0 || off > n || meta > n-off || body > n-off-meta {
		return nil // the decoder refuses the block itself
	}
	prefix := int64(4)
	if binary.LittleEndian.Uint32(data[off:]) == 0xFFFFFFFF {
		prefix = 8
	}
	msg := fb(data[off+prefix : off+meta])
	root, ok := msg.root()
	if !ok {
		return nil
	}
	if _, _, err := root.vector(messageCustomMetadata, 4, "message metadata"); err != nil {
		return err
	}
	kind, err := root.u8(messageHeaderType, 0)
	if err != nil {
		return err
	}
	version, err := root.u16(messageVersion, 0)
	if err != nil {
		return err
	}
	v5 := version >= metadataVersion5
	header, ok, err := root.sub(messageHeader)
	if err != nil || !ok {
		return err
	}
	var need *batchNeeds
	switch kind {
	case headerDictionaryBatch:
		id, err := header.i64(dictionaryBatchID, 0)
		if err != nil {
			return err
		}
		batch, ok, err := header.sub(dictionaryBatchData)
		if err != nil || !ok {
			return err
		}
		header = batch
		if f, ok := sc.dicts[id]; ok && sc.known {
			need = &batchNeeds{}
			need.add(f, v5, true)
		}
	case headerRecordBatch:
		if sc.known {
			need = &batchNeeds{}
			for _, f := range sc.fields {
				need.add(f, v5, false)
			}
		}
	default:
		return nil
	}
	return checkRecordBatch(header, data[off+meta:off+meta+body], need)
}

// checkRecordBatch checks one (dictionary) record batch. The decoder
// indexes the node, buffer and variadic-count lists by what the schema
// consumes, never by their declared length — an index past the end
// reads whatever bytes follow as a buffer and allocates the length it
// finds there — so a list shorter than need is refused here.
func checkRecordBatch(batch fbTable, body []byte, need *batchNeeds) error {
	_, nodes, err := batch.vector(recordBatchNodes, nodeSize, "record batch node list")
	if err != nil {
		return err
	}
	vstart, views, err := batch.vector(recordBatchVariadic, 8, "record batch variadic count list")
	if err != nil {
		return err
	}
	start, n, err := batch.vector(recordBatchBuffers, bufferSize, "record batch buffer list")
	if err != nil {
		return err
	}
	if need != nil {
		buffers := int64(need.buffers)
		if views < need.views {
			return fmt.Errorf("arrow IPC record batch lists %d variadic buffer counts, its schema reads %d", views, need.views)
		}
		for i := 0; i < need.views; i++ {
			c, _ := batch.b.i64(vstart + 8*i)
			if c < 0 || c > int64(n) {
				return fmt.Errorf("arrow IPC record batch declares %d variadic buffers for a view column, it lists %d buffers", c, n)
			}
			buffers += c
		}
		if nodes < need.nodes || int64(n) < buffers {
			return fmt.Errorf("arrow IPC record batch lists %d field nodes and %d buffers, its schema reads %d and %d", nodes, n, need.nodes, buffers)
		}
	}
	compression, ok, err := batch.sub(recordBatchCompression)
	if err != nil || !ok {
		return err
	}
	codec, err := compression.u8(bodyCompressionCodec, codecLZ4Frame)
	if err != nil {
		return err
	}
	for i := 0; i < n; i++ {
		at := start + bufferSize*i
		boff, _ := batch.b.i64(at)
		blen, _ := batch.b.i64(at + 8)
		if blen == 0 {
			continue
		}
		if boff < 0 || blen < 8 || boff > int64(len(body)) || blen > int64(len(body))-boff {
			return fmt.Errorf("arrow IPC compressed buffer %d (offset %d, length %d) lies outside its %d-byte body", i, boff, blen, len(body))
		}
		buf := body[boff : boff+blen]
		claim := int64(binary.LittleEndian.Uint64(buf))
		if claim == -1 {
			continue // stored uncompressed
		}
		if codec == codecZstd {
			if err := checkZstdFrames(buf[8:], claim); err != nil {
				return fmt.Errorf("arrow IPC compressed buffer %d: %w", i, err)
			}
		}
		if err := checkCompressedClaim(codec, buf[8:], claim); err != nil {
			return fmt.Errorf("arrow IPC compressed buffer %d: %w", i, err)
		}
	}
	return nil
}

// checkCompressedClaim refuses a compressed buffer whose declared
// uncompressed length is not what its codec actually decodes to. A
// claim within compressedClaimRatio of the compressed length is
// trusted (it allocates in proportion to the source); a larger one is
// decoded, without materialising it, and must match exactly.
func checkCompressedClaim(codec byte, payload []byte, claim int64) error {
	if claim < 0 {
		return fmt.Errorf("declares a negative uncompressed length %d", claim)
	}
	if claim <= compressedClaimRatio*int64(len(payload))+4096 {
		return nil
	}
	var r io.Reader
	switch codec {
	case codecLZ4Frame:
		r = lz4.NewReader(bytes.NewReader(payload))
	case codecZstd:
		dec, err := zstd.NewReader(bytes.NewReader(payload), zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true))
		if err != nil {
			return err
		}
		defer dec.Close()
		r = dec
	default:
		return nil // the decoder refuses the codec itself
	}
	got, err := io.Copy(io.Discard, io.LimitReader(r, claim+1))
	if err != nil {
		return fmt.Errorf("declares %d uncompressed bytes but does not decode: %w", claim, err)
	}
	if got != claim {
		return fmt.Errorf("declares %d uncompressed bytes but decodes to %d", claim, got)
	}
	return nil
}

// zstdWindowFloor is the decoder window any zstd frame may declare
// whatever its content: the zstd format's own recommended decoder
// minimum, and the window Pulse's encoder library uses by default.
const zstdWindowFloor = 8 << 20

// checkZstdFrames walks every frame of a zstd payload and refuses one
// whose header asks the decoder for more memory than the buffer's
// claimed content could need: a window beyond max(claim,
// zstdWindowFloor), or a declared content size other than the claim.
// The decoder allocates the window a frame header names before it
// decodes a block, so a one-byte edit there costs hundreds of
// megabytes per buffer.
func checkZstdFrames(p []byte, claim int64) error {
	const (
		frameMagic     = 0xFD2FB528
		skippableMagic = 0x184D2A50
	)
	for len(p) >= 4 {
		magic := binary.LittleEndian.Uint32(p)
		if magic&0xFFFFFFF0 == skippableMagic {
			if len(p) < 8 {
				return nil
			}
			skip := int64(binary.LittleEndian.Uint32(p[4:]))
			if skip > int64(len(p)-8) {
				return nil
			}
			p = p[8+skip:]
			continue
		}
		if magic != frameMagic || len(p) < 5 {
			return nil // the decoder refuses it
		}
		fhd := p[4]
		single := fhd&0x20 != 0
		at := 5
		var window int64
		if !single {
			if at >= len(p) {
				return nil
			}
			wd := p[at]
			at++
			base := int64(1) << (10 + int(wd>>3))
			window = base + base/8*int64(wd&7)
		}
		at += [4]int{0, 1, 2, 4}[fhd&3] // dictionary id
		fcsLen := [4]int{0, 2, 4, 8}[fhd>>6]
		if fhd>>6 == 0 && single {
			fcsLen = 1
		}
		if at+fcsLen > len(p) {
			return nil
		}
		if fcsLen > 0 {
			var fcs uint64
			for i := fcsLen - 1; i >= 0; i-- {
				fcs = fcs<<8 | uint64(p[at+i])
			}
			if fcsLen == 2 {
				fcs += 256
			}
			if fcs > uint64(claim) {
				return fmt.Errorf("zstd frame declares %d content bytes, more than the buffer's %d", fcs, claim)
			}
			if single {
				window = int64(fcs)
			}
		}
		at += fcsLen
		if window > max(claim, zstdWindowFloor) {
			return fmt.Errorf("zstd frame declares a %d-byte window for a %d-byte buffer", window, claim)
		}
		// Skip the frame's blocks to reach the next frame.
		for {
			if at+3 > len(p) {
				return nil
			}
			bh := uint32(p[at]) | uint32(p[at+1])<<8 | uint32(p[at+2])<<16
			at += 3
			size := int(bh >> 3)
			if (bh>>1)&3 == 1 { // RLE: one stored byte
				size = 1
			}
			if size > len(p)-at {
				return nil
			}
			at += size
			if bh&1 != 0 {
				break
			}
		}
		if fhd&0x04 != 0 {
			at += 4
		}
		if at > len(p) {
			return nil
		}
		p = p[at:]
	}
	return nil
}
