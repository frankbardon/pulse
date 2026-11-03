package parquet

import (
	"bytes"
	"context"
	stderrors "errors"
	"io/fs"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/parquet/file"
	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/internal/io"
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
