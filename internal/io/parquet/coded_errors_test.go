package parquet

import (
	"bytes"
	"context"
	stderrors "errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/parquet/compress"
	"github.com/apache/arrow-go/v18/parquet/file"
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

func requireDecodePanic(t *testing.T, err error, value string) {
	t.Helper()
	ce := requireCode(t, err, errors.DATA_FILE)
	if ce.Details["format"] != "parquet" || ce.Details["panic"] != value {
		t.Fatalf("want details format parquet panic %q, got %v", value, ce.Details)
	}
}

// A panic inside the third-party Parquet decoder surfaces as DATA_FILE
// naming the format and the recovered value — from the reader and
// through the import an embedder runs — never as a crash.
func TestParquetReader_DecoderPanicIsCodedError(t *testing.T) {
	t.Parallel()
	panicking := func(*bytes.Reader) (*file.Reader, error) { panic("thrift footer exploded") }

	r := NewReaderFromBytes(helperSimpleParquet(t))
	r.open = panicking
	_, err := r.ReadHeader()
	requireDecodePanic(t, err, "thrift footer exploded")

	src := NewReaderFromBytes(helperSimpleParquet(t))
	src.open = panicking
	job := pio.NewImportJob(src, "c.pulse")
	job.FS = afero.NewMemMapFs()
	_, err = job.Run(context.Background())
	requireDecodePanic(t, err, "thrift footer exploded")
}

// panicArray is an Arrow array whose cell reads panic, standing in for
// a decoded buffer the library mis-indexes.
type panicArray struct{ arrow.Array }

func (panicArray) IsNull(int) bool { panic("cell read out of range") }

// A panic reading a decoded cell is DATA_FILE too; a panic raised by
// the caller's own row callback is NOT the decoder's and propagates.
func TestParquetReader_CellPanicIsCodedError(t *testing.T) {
	t.Parallel()
	r := NewReaderFromBytes(helperSimpleParquet(t))
	defer r.Close()
	if _, err := r.ReadHeader(); err != nil {
		t.Fatal(err)
	}
	real := r.tbl
	cols := make([]arrow.Column, real.NumCols())
	for c := range cols {
		src := real.Column(c)
		chunks := make([]arrow.Array, 0, len(src.Data().Chunks()))
		for _, ch := range src.Data().Chunks() {
			chunks = append(chunks, panicArray{ch})
		}
		cols[c] = *arrow.NewColumn(src.Field(), arrow.NewChunked(src.DataType(), chunks))
	}
	r.tbl = array.NewTable(real.Schema(), cols, real.NumRows())
	err := r.ReadRows(context.Background(), func([]string) error { return nil })
	requireDecodePanic(t, err, "cell read out of range")

	r.tbl = real
	defer func() {
		if got := recover(); got != "callback bug" {
			t.Fatalf("a callback panic must propagate unchanged, recovered %v", got)
		}
	}()
	_ = r.ReadRows(context.Background(), func([]string) error { panic("callback bug") })
	t.Fatal("ReadRows swallowed the callback panic")
}

// An unreadable path, a missing source and bytes that are not a
// Parquet file are DATA_FILE, the library or fs error kept as Cause.
func TestParquetReader_OpenFaultsAreDataFile(t *testing.T) {
	t.Parallel()
	_, err := NewReader(afero.NewMemMapFs(), "missing.parquet").ReadHeader()
	ce := requireCode(t, err, errors.DATA_FILE)
	if !stderrors.Is(err, fs.ErrNotExist) || ce.Details["path"] != "missing.parquet" {
		t.Fatalf("want the fs cause and details.path, got %v / %v", err, ce.Details)
	}
	_, err = (&Reader{}).ReadHeader()
	requireCode(t, err, errors.DATA_FILE)

	good := helperSimpleParquet(t)
	for _, in := range [][]byte{[]byte("not parquet at all"), good[:len(good)/2]} {
		_, err := NewReaderFromBytes(in).InferPulseSchema()
		if ce := requireCode(t, err, errors.DATA_FILE); ce.Cause == nil {
			t.Fatalf("%q: want the decoder error kept as Cause", in)
		}
	}
}

// Found by the fuzz.yml workflow: a row group declaring 2,281,701,312
// rows over column chunks of 3 values each. The decoder reserved memory
// for the declared rows (26 GB) before reading a page; checkRowGroups
// refuses the count its own chunks cannot back, through the import an
// embedder runs.
func TestParquetImport_RowCountClaimIsRefused(t *testing.T) {
	t.Parallel()
	in := fuzzcontract.CorpusEntry(t, filepath.Join("testdata", "fuzz", "FuzzParquetImport", "row-group-row-count-claim"))
	job := pio.NewImportJob(NewReaderFromBytes(in), "c.pulse")
	job.FS = afero.NewMemMapFs()
	_, err := job.Run(context.Background())
	want := "row group 0 declares 2281701312 rows, but its column 0 holds only 3 values"
	if ce := requireCode(t, err, errors.DATA_FILE); !strings.Contains(ce.Message, want) {
		t.Fatalf("want %q, got %v", want, err)
	}
	// An honest file passes: every chunk holds at least its group's rows.
	if _, err := NewReaderFromBytes(helperSimpleParquet(t)).InferPulseSchema(); err != nil {
		t.Fatalf("honest file refused: %v", err)
	}
}

// Found by FuzzParquetImport: a snappy page whose stream header claims
// 3 GB. The registered SNAPPY codec is the guarded one, which refuses
// the claim before decoding (through the import an embedder runs) and
// still decodes an honest block.
func TestParquetImport_SnappyLengthClaimIsRefused(t *testing.T) {
	t.Parallel()
	in := fuzzcontract.CorpusEntry(t, filepath.Join("testdata", "fuzz", "FuzzParquetImport", "snappy-decoded-length-claim"))
	job := pio.NewImportJob(NewReaderFromBytes(in), "c.pulse")
	job.FS = afero.NewMemMapFs()
	_, err := job.Run(context.Background())
	if ce := requireCode(t, err, errors.DATA_FILE); !strings.Contains(ce.Message, "snappy block declares") {
		t.Fatalf("want the snappy claim refusal, got %v", err)
	}
	codec, err := compress.GetCodec(compress.Codecs.Snappy)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := codec.(boundedSnappy); !ok {
		t.Fatalf("registered SNAPPY codec is %T, want boundedSnappy", codec)
	}
	src := bytes.Repeat([]byte("pulse "), 100)
	enc := codec.Encode(nil, src)
	got, err := compress.Decode(codec, make([]byte, len(src)), enc)
	if err != nil || !bytes.Equal(got, src) {
		t.Fatalf("honest block: %v", err)
	}
	if _, err := compress.Decode(codec, make([]byte, len(src)-1), enc); err == nil {
		t.Fatal("a block longer than its page was decoded")
	}
	// A caller outside the page reader that leaves the sizing to the
	// codec gets the stock decode.
	if got, err := compress.Decode(codec, nil, enc); err != nil || !bytes.Equal(got, src) {
		t.Fatalf("empty dst: %v", err)
	}
}
