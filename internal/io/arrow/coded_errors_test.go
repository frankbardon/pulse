package arrow

import (
	"bytes"
	"context"
	"encoding/binary"
	stderrors "errors"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/ipc"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/klauspost/compress/zstd"
	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/internal/io"
	"github.com/frankbardon/pulse/internal/testutil/fuzzcontract"
)

func requireCode(t *testing.T, err error, code errors.Code) *errors.CodedError {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
	return ce
}

func requireDecodePanic(t *testing.T, err error, format, value string) {
	t.Helper()
	ce := requireCode(t, err, errors.DATA_FILE)
	if ce.Details["format"] != format || ce.Details["panic"] != value {
		t.Fatalf("want details format %q panic %q, got %v", format, value, ce.Details)
	}
}

// A panic inside the third-party IPC decoder surfaces as DATA_FILE
// naming the format and the recovered value — from the reader and
// through the import an embedder runs — never as a crash.
func TestArrowReader_DecoderPanicIsCodedError(t *testing.T) {
	t.Parallel()
	panicking := func(*bytes.Reader, int64) (*ipc.FileReader, error) { panic("ipc decoder exploded") }

	r := NewReaderFromBytes(helperSimpleArrow(t))
	r.open = panicking
	_, err := r.ReadHeader()
	requireDecodePanic(t, err, "arrow", "ipc decoder exploded")

	src := NewReaderFromBytes(helperSimpleArrow(t))
	src.open = panicking
	job := pio.NewImportJob(src, "c.pulse")
	job.FS = afero.NewMemMapFs()
	_, err = job.Run(context.Background())
	requireDecodePanic(t, err, "arrow", "ipc decoder exploded")
}

// panicArray is an Arrow array whose cell reads panic, standing in for
// a decoded buffer the library mis-indexes.
type panicArray struct{ arrow.Array }

func (panicArray) IsNull(int) bool { panic("cell read out of range") }

// panicBatch hands out panicArray columns.
type panicBatch struct{ arrow.RecordBatch }

func (b panicBatch) Column(i int) arrow.Array { return panicArray{b.RecordBatch.Column(i)} }

// A panic reading a decoded cell is DATA_FILE too; a panic raised by
// the caller's own row callback is NOT the decoder's and propagates.
func TestArrowReader_CellPanicIsCodedError(t *testing.T) {
	t.Parallel()
	r := NewReaderFromBytes(helperSimpleArrow(t))
	defer r.Close()
	if _, err := r.ReadHeader(); err != nil {
		t.Fatal(err)
	}
	real := r.batches[0]
	r.batches[0] = panicBatch{real}
	err := r.ReadRows(context.Background(), func([]string) error { return nil })
	requireDecodePanic(t, err, "arrow", "cell read out of range")

	r.batches[0] = real
	defer func() {
		if got := recover(); got != "callback bug" {
			t.Fatalf("a callback panic must propagate unchanged, recovered %v", got)
		}
	}()
	_ = r.ReadRows(context.Background(), func([]string) error { panic("callback bug") })
	t.Fatal("ReadRows swallowed the callback panic")
}

// An unreadable path, a missing source and bytes that are not an Arrow
// IPC file are DATA_FILE, the library or fs error kept as Cause.
func TestArrowReader_OpenFaultsAreDataFile(t *testing.T) {
	t.Parallel()
	_, err := NewReader(afero.NewMemMapFs(), "missing.arrow").ReadHeader()
	ce := requireCode(t, err, errors.DATA_FILE)
	if !stderrors.Is(err, fs.ErrNotExist) || ce.Details["path"] != "missing.arrow" {
		t.Fatalf("want the fs cause and details.path, got %v / %v", err, ce.Details)
	}
	_, err = (&Reader{}).ReadHeader()
	requireCode(t, err, errors.DATA_FILE)

	good := helperSimpleArrow(t)
	for _, in := range [][]byte{[]byte("not arrow at all"), good[:len(good)/2]} {
		_, err := NewReaderFromBytes(in).InferPulseSchema()
		if ce := requireCode(t, err, errors.DATA_FILE); ce.Cause == nil {
			t.Fatalf("%q: want the decoder error kept as Cause", in)
		}
	}
}

// The schema footer of this fuzzer-found file declares 922,746,880
// fields in 128 bytes; the IPC decoder allocated them all (tens of GB,
// ten seconds of page clearing) before failing. It is refused up front.
func TestArrowReader_DeclaredFieldCountIsRefused(t *testing.T) {
	// serial: measures process-wide heap allocation (runtime.MemStats),
	// which a parallel sibling's allocations would be charged to.
	in := fuzzcontract.CorpusEntry(t, filepath.Join("testdata", "fuzz", "FuzzArrowImport", "schema-field-count-claim"))
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := NewReaderFromBytes(in).ReadHeader()
	runtime.ReadMemStats(&after)
	ce := requireCode(t, err, errors.DATA_FILE)
	if !strings.Contains(ce.Message, "schema field list declares 922746880 entries") {
		t.Fatalf("want the declared-count refusal, got %v", err)
	}
	if got := after.TotalAlloc - before.TotalAlloc; got > 64<<20 {
		t.Fatalf("refusal allocated %d bytes", got)
	}
}

// firstCompressedClaim returns the offset, in an IPC file, of the first
// non-empty buffer of its first record batch — where a compressed
// body stores the buffer's declared uncompressed length.
func firstCompressedClaim(t *testing.T, data []byte) int {
	t.Helper()
	end := len(data) - 10
	size := int(binary.LittleEndian.Uint32(data[end:]))
	footer, _ := fb(data[end-size : end]).root()
	start, n, err := footer.vector(footerRecordBatches, blockSize, "blocks")
	if err != nil || n == 0 {
		t.Fatalf("no record batch block: %v", err)
	}
	off, _ := footer.b.i64(start)
	meta, _ := footer.b.u32(start + 8)
	msg, _ := fb(data[int(off)+8 : int(off)+int(meta)]).root()
	batch, _, _ := msg.sub(messageHeader)
	bstart, bn, _ := batch.vector(recordBatchBuffers, bufferSize, "buffers")
	for i := 0; i < bn; i++ {
		boff, _ := batch.b.i64(bstart + bufferSize*i)
		if blen, _ := batch.b.i64(bstart + bufferSize*i + 8); blen > 0 {
			return int(off) + int(meta) + int(boff)
		}
	}
	t.Fatal("no non-empty buffer")
	return 0
}

// A compressed buffer may not claim an uncompressed length its codec
// does not decode to: the decoder allocates the claim before reading
// the payload. A claim far beyond the payload is decoded and checked;
// an honest highly-compressible buffer still reads.
func TestArrowReader_CompressedLengthClaimIsChecked(t *testing.T) {
	t.Parallel()
	sc := arrow.NewSchema([]arrow.Field{{Name: "v", Type: arrow.PrimitiveTypes.Int8}}, nil)
	for name, opt := range map[string]ipc.Option{"lz4": ipc.WithLZ4(), "zstd": ipc.WithZstd()} {
		zeros := arrowFuzzFile(t, sc, 1, func(b *array.RecordBuilder) {
			b.Field(0).(*array.Int8Builder).AppendValues(make([]int8, 200_000), nil)
		}, opt)
		if _, err := NewReaderFromBytes(zeros).ReadHeader(); err != nil {
			t.Fatalf("%s: an honest 200 KB buffer of zeros must read: %v", name, err)
		}
		at := firstCompressedClaim(t, zeros)
		for _, claim := range []int64{1 << 30, 200_001, -7} {
			patched := bytes.Clone(zeros)
			binary.LittleEndian.PutUint64(patched[at:], uint64(claim))
			_, err := NewReaderFromBytes(patched).ReadHeader()
			ce := requireCode(t, err, errors.DATA_FILE)
			if !strings.Contains(ce.Message, "compressed buffer 1: declares") {
				t.Fatalf("%s claim %d: want the compressed-claim refusal, got %v", name, claim, err)
			}
		}
	}
}

// A schema nested deeper than the IPC depth limit is refused before the
// decoder recurses into it.
func TestArrowReader_DeepNestingIsRefused(t *testing.T) {
	t.Parallel()
	var dt arrow.DataType = arrow.PrimitiveTypes.Int8
	for i := 0; i < maxIPCNesting+1; i++ {
		dt = arrow.StructOf(arrow.Field{Name: "c", Type: dt, Nullable: true})
	}
	deep := arrowFuzzFile(t, arrow.NewSchema([]arrow.Field{{Name: "s", Type: dt, Nullable: true}}, nil), 0, func(*array.RecordBuilder) {})
	_, err := NewReaderFromBytes(deep).ReadHeader()
	ce := requireCode(t, err, errors.DATA_FILE)
	if !strings.Contains(ce.Message, "deeper than 64 levels") {
		t.Fatalf("want the nesting refusal, got %v", err)
	}
}

// A batch of zero-width (Null-typed) columns can declare any row count
// in a few bytes; past maxUnbackedRows per file, a count no decoded
// byte backs is refused rather than iterated. Rows a buffer backs —
// however compressed the source — are never refused (the 200 K-row
// compressed batches of TestArrowReader_CompressedLengthClaimIsChecked).
func TestArrowReader_UnbackedRowCountIsRefused(t *testing.T) {
	t.Parallel()
	sc := arrow.NewSchema([]arrow.Field{{Name: "n", Type: arrow.Null, Nullable: true}}, nil)
	claim := arrowFuzzFile(t, sc, 1, func(b *array.RecordBuilder) {
		b.Field(0).(*array.NullBuilder).AppendEmptyValues(1 << 20)
	})
	r := NewReaderFromBytes(claim)
	_, err := r.ReadHeader()
	ce := requireCode(t, err, errors.DATA_FILE)
	if !strings.Contains(ce.Message, "declares 1048576 rows; 1048576 rows or nested elements are more than its decoded buffers back") {
		t.Fatalf("want the row-count refusal, got %v", err)
	}
	if r.batches != nil || r.fileReader != nil || r.arrowSc != nil {
		t.Fatal("a failed init must release what it had decoded")
	}
	backed := arrowFuzzFile(t, sc, 1, func(b *array.RecordBuilder) {
		b.Field(0).(*array.NullBuilder).AppendEmptyValues(10)
	})
	if _, err := NewReaderFromBytes(backed).ReadHeader(); err != nil {
		t.Fatalf("a small Null-typed batch must read: %v", err)
	}

	// One level down: a list<null> whose two rows hold 2^20 items no
	// buffer backs (the list's own offsets back only its two rows).
	lsc := arrow.NewSchema([]arrow.Field{{Name: "l", Type: arrow.ListOf(arrow.Null), Nullable: true}}, nil)
	list := func(items int) []byte {
		return arrowFuzzFile(t, lsc, 1, func(b *array.RecordBuilder) {
			lb := b.Field(0).(*array.ListBuilder)
			for i := 0; i < 2; i++ {
				lb.Append(true)
				lb.ValueBuilder().(*array.NullBuilder).AppendEmptyValues(items / 2)
			}
		})
	}
	_, err = NewReaderFromBytes(list(1 << 20)).ReadHeader()
	ce = requireCode(t, err, errors.DATA_FILE)
	if !strings.Contains(ce.Message, "declares 2 rows; 1048576 rows or nested elements are more than its decoded buffers back") {
		t.Fatalf("want the nested-element refusal, got %v", err)
	}
	if _, err := NewReaderFromBytes(list(10)).ReadHeader(); err != nil {
		t.Fatalf("a small list<null> batch must read: %v", err)
	}
}

// A zstd frame header names the window the decoder allocates before it
// reads a block; a one-byte edit there cost 320 MB per buffer. A frame
// asking for more than its buffer's content could need is refused, as
// is a declared content size beyond the buffer's claim.
func TestArrowCheckZstdFrames(t *testing.T) {
	t.Parallel()
	content := bytes.Repeat([]byte("pulse"), 1000)
	// stream encodes content as a multi-segment frame (a Window
	// Descriptor at byte 5) and, for window > 0, rewrites that
	// descriptor to name a 2^window-byte window.
	stream := func(window int) []byte {
		var out bytes.Buffer
		enc, err := zstd.NewWriter(&out, zstd.WithEncoderConcurrency(1), zstd.WithSingleSegment(false))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := enc.Write(content); err != nil {
			t.Fatal(err)
		}
		if err := enc.Close(); err != nil {
			t.Fatal(err)
		}
		frame := out.Bytes()
		if frame[4]&0x20 != 0 {
			t.Fatal("want a multi-segment frame")
		}
		if window > 0 {
			frame[5] = byte(window-10) << 3
		}
		return frame
	}
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	oneShot := enc.EncodeAll(content, nil) // single segment, content size declared
	n := int64(len(content))

	for name, tc := range map[string]struct {
		frames  []byte
		claim   int64
		refused string
	}{
		"encoder window":              {stream(0), n, ""},
		"window at the floor":         {stream(23), n, ""},
		"one-shot, size declared":     {oneShot, n, ""},
		"two frames":                  {append(bytes.Clone(oneShot), oneShot...), 2 * n, ""},
		"window past the floor":       {stream(26), n, "window"},
		"declared size past claim":    {oneShot, n - 1, "content bytes"},
		"second frame past the floor": {append(bytes.Clone(oneShot), stream(26)...), 2 * n, "window"},
	} {
		err := checkZstdFrames(tc.frames, tc.claim)
		switch {
		case tc.refused == "" && err != nil:
			t.Fatalf("%s: refused an honest frame: %v", name, err)
		case tc.refused != "" && (err == nil || !strings.Contains(err.Error(), tc.refused)):
			t.Fatalf("%s: want a %q refusal, got %v", name, tc.refused, err)
		}
	}
}

// An embedded "overlays" column that is not the writer's LIST<STRUCT>
// of seven strings, or whose JSON does not decode, is a malformed
// source: DATA_FILE, never an uncoded error or a failed type assertion.
func TestArrowReadOverlays_MalformedColumnIsDataFile(t *testing.T) {
	t.Parallel()
	overlayFile := func(dt arrow.DataType, build func(b array.Builder)) []byte {
		return arrowFuzzFile(t, arrow.NewSchema([]arrow.Field{
			{Name: "v", Type: arrow.PrimitiveTypes.Int8},
			{Name: OverlaysFieldName, Type: dt, Nullable: true},
		}, nil), 1, func(b *array.RecordBuilder) {
			b.Field(0).(*array.Int8Builder).Append(1)
			build(b.Field(1))
		})
	}
	notList := overlayFile(arrow.BinaryTypes.String, func(b array.Builder) { b.(*array.StringBuilder).Append("x") })
	wrongStruct := overlayFile(arrow.ListOf(arrow.StructOf(arrow.Field{Name: "a", Type: arrow.PrimitiveTypes.Int8})), func(b array.Builder) {
		lb := b.(*array.ListBuilder)
		lb.Append(true)
		sb := lb.ValueBuilder().(*array.StructBuilder)
		sb.Append(true)
		sb.FieldBuilder(0).(*array.Int8Builder).Append(1)
	})
	badJSON := overlayFile(arrow.ListOf(OverlaysFieldType()), func(b array.Builder) {
		lb := b.(*array.ListBuilder)
		lb.Append(true)
		sb := lb.ValueBuilder().(*array.StructBuilder)
		sb.Append(true)
		for f, v := range []string{"n", "k", "s", "{not json", "shape", "{}"} {
			sb.FieldBuilder(f).(*array.StringBuilder).Append(v)
		}
		sb.FieldBuilder(6).(*array.StringBuilder).AppendNull()
	})
	for name, in := range map[string][]byte{"not a list": notList, "wrong struct": wrongStruct, "bad JSON": badJSON} {
		_, err := NewReaderFromBytes(in).ReadOverlays()
		ce := requireCode(t, err, errors.DATA_FILE)
		if _, ok := ce.Details["panic"]; ok {
			t.Fatalf("%s: refused by the panic guard, want a checked refusal: %v", name, err)
		}
	}
}

// Each committed claim seed is refused with its own check's message —
// through the import an embedder runs, not just the reader.
func TestArrowImport_CommittedClaimSeedsAreRefused(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]string{
		"schema-field-count-claim": "schema field list declares",
		"zstd-window-claim":        "zstd frame declares a 67108864-byte window",
		"lz4-length-claim":         "declares 1099511627776 uncompressed bytes",
		"null-row-count-claim":     "more than its decoded buffers back",
		// Found by FuzzArrowImport: the batch lists no buffers, the
		// decoder read two anyway from the bytes after the list and
		// allocated the 1 TiB "claim" it found there.
		"buffer-list-shorter-than-schema": "lists 1 field nodes and 0 buffers, its schema reads 1 and 2",
		// Found by the fuzz.yml workflow: a footer dictionary block whose
		// message header type byte is not DictionaryBatch. The decoder
		// reads it as a dictionary batch anyway and allocated the 4 GiB
		// compressed-buffer claim the preflight had skipped.
		"dictionary-block-any-header-type": "declares 4294967296 uncompressed bytes but does not decode",
		// Found by the fuzz.yml workflow: a list<null> column whose Null
		// child declares 7.2e16 items; formatting the rows walked them.
		"nested-null-child-claim": "rows or nested elements are more than its decoded buffers back",
	} {
		in := fuzzcontract.CorpusEntry(t, filepath.Join("testdata", "fuzz", "FuzzArrowImport", name))
		job := pio.NewImportJob(NewReaderFromBytes(in), "c.pulse")
		job.FS = afero.NewMemMapFs()
		_, err := job.Run(context.Background())
		if ce := requireCode(t, err, errors.DATA_FILE); !strings.Contains(ce.Message, want) {
			t.Fatalf("%s: want %q, got %v", name, want, err)
		}
	}
}

// The preflight's node / buffer accounting must match what the Arrow
// writer emits for every layout family, or it would refuse honest
// files: views (variadic buffers), unions both modes, run-end encoded,
// maps, list views, nested lists and structs, fixed-size binary and
// dictionaries, compressed and not.
func TestArrowPreflight_HonestLayoutsPass(t *testing.T) {
	t.Parallel()
	mem := memory.NewGoAllocator()
	str := func(vs ...string) arrow.Array {
		b := array.NewStringBuilder(mem)
		defer b.Release()
		b.AppendValues(vs, nil)
		return b.NewArray()
	}
	i32 := func(vs ...int32) arrow.Array {
		b := array.NewInt32Builder(mem)
		defer b.Release()
		b.AppendValues(vs, nil)
		return b.NewArray()
	}
	view := array.NewStringViewBuilder(mem)
	view.AppendValues([]string{"short", "a value longer than twelve bytes"}, []bool{true, false})
	ty := array.NewInt8Builder(mem)
	ty.AppendValues([]int8{0, 1}, nil)
	off := array.NewInt32Builder(mem)
	off.AppendValues([]int32{0, 0}, nil)
	tys, offs := ty.NewArray(), off.NewArray()
	dense, err := array.NewDenseUnionFromArrays(tys, offs, []arrow.Array{i32(7), str("x")})
	if err != nil {
		t.Fatal(err)
	}
	sparse, err := array.NewSparseUnionFromArrays(tys, []arrow.Array{i32(1, 2), str("p", "q")})
	if err != nil {
		t.Fatal(err)
	}
	ree := array.NewRunEndEncodedArray(i32(2, 5), str("a", "b"), 5, 0)
	cols := map[string]arrow.Array{
		"view":   view.NewArray(),
		"dense":  dense,
		"sparse": sparse,
		"ree":    ree,
	}
	for name, js := range map[string]struct {
		dt  arrow.DataType
		doc string
	}{
		"map":       {arrow.MapOf(arrow.BinaryTypes.String, arrow.PrimitiveTypes.Int32), `[[{"key":"a","value":1}], null]`},
		"list view": {arrow.ListViewOf(arrow.PrimitiveTypes.Int32), `[[1,2], null, []]`},
		"fsb":       {&arrow.FixedSizeBinaryType{ByteWidth: 2}, `["AAA=", null]`},
		"large":     {arrow.BinaryTypes.LargeString, `["x", null]`},
		"struct":    {arrow.StructOf(arrow.Field{Name: "l", Type: arrow.ListOf(arrow.BinaryTypes.String), Nullable: true}), `[{"l":["a"]}, null]`},
		"dict":      {&arrow.DictionaryType{IndexType: arrow.PrimitiveTypes.Int16, ValueType: arrow.BinaryTypes.String}, `["a", "b", null, "a"]`},
	} {
		col, _, err := array.FromJSON(mem, js.dt, strings.NewReader(js.doc))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		cols[name] = col
	}
	for name, col := range cols {
		for codec, opts := range map[string][]ipc.Option{"plain": nil, "lz4": {ipc.WithLZ4()}, "zstd": {ipc.WithZstd()}} {
			sc := arrow.NewSchema([]arrow.Field{{Name: name, Type: col.DataType(), Nullable: true}}, nil)
			var buf bytes.Buffer
			fw, err := ipc.NewFileWriter(&buf, append([]ipc.Option{ipc.WithSchema(sc)}, opts...)...)
			if err != nil {
				t.Fatal(err)
			}
			rec := array.NewRecordBatch(sc, []arrow.Array{col}, int64(col.Len()))
			if err := fw.Write(rec); err != nil {
				t.Fatalf("%s/%s: %v", name, codec, err)
			}
			if err := fw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := preflightIPC(buf.Bytes()); err != nil {
				t.Fatalf("%s/%s: an honest file was refused: %v", name, codec, err)
			}
		}
	}
}

// A view column's variadic count says how many more buffers the
// decoder reads; a count past the buffer list is refused.
func TestArrowPreflight_VariadicCountPastBufferList(t *testing.T) {
	t.Parallel()
	sc := arrow.NewSchema([]arrow.Field{{Name: "v", Type: arrow.BinaryTypes.StringView, Nullable: true}}, nil)
	data := arrowFuzzFile(t, sc, 1, func(b *array.RecordBuilder) {
		b.Field(0).(*array.StringViewBuilder).AppendValues([]string{"a value longer than twelve bytes"}, nil)
	})
	if err := preflightIPC(data); err != nil {
		t.Fatal(err)
	}
	end := len(data) - 10
	size := int(binary.LittleEndian.Uint32(data[end:]))
	footer, _ := fb(data[end-size : end]).root()
	start, _, _ := footer.vector(footerRecordBatches, blockSize, "blocks")
	off, _ := footer.b.i64(start)
	meta, _ := footer.b.u32(start + 8)
	base := int(off) + 8
	msg, _ := fb(data[base : int(off)+int(meta)]).root()
	batch, _, _ := msg.sub(messageHeader)
	vstart, views, err := batch.vector(recordBatchVariadic, 8, "variadic")
	if err != nil || views != 1 {
		t.Fatalf("want one variadic count, got %d (%v)", views, err)
	}
	binary.LittleEndian.PutUint64(data[base+vstart:], 2)
	err = preflightIPC(data)
	if err == nil || !strings.Contains(err.Error(), "lists 1 field nodes and 3 buffers, its schema reads 1 and 4") {
		t.Fatalf("want the buffer-list refusal, got %v", err)
	}
}
