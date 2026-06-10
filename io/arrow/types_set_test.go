package arrow

import (
	"bytes"
	"context"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/ipc"
	"github.com/apache/arrow-go/v18/arrow/memory"

	"github.com/frankbardon/pulse/encoding"
)

func TestArrow_TypeToPulseListUTF8(t *testing.T) {
	listType := arrow.ListOf(arrow.BinaryTypes.String)
	if got := TypeToPulse(listType); got != encoding.FieldTypeSetU8 {
		t.Errorf("TypeToPulse(LIST<UTF8>) = %s, want set_u8 (provisional)", got)
	}
}

func TestArrow_TypeFromPulseSetU8(t *testing.T) {
	dt := TypeFromPulse(encoding.FieldTypeSetU8)
	if dt.ID() != arrow.LIST {
		t.Errorf("TypeFromPulse(set_u8) = %v, want LIST", dt)
	}
}

func TestArrow_FormatValueListUTF8JoinsWithPipe(t *testing.T) {
	alloc := memory.NewGoAllocator()
	sc := arrow.NewSchema([]arrow.Field{
		{Name: "issuers", Type: arrow.ListOf(arrow.BinaryTypes.String)},
	}, nil)
	bldr := array.NewRecordBuilder(alloc, sc)
	defer bldr.Release()

	lb := bldr.Field(0).(*array.ListBuilder)
	sb := lb.ValueBuilder().(*array.StringBuilder)
	// row 0: [VISA, MC]
	lb.Append(true)
	sb.AppendValues([]string{"VISA", "MC"}, nil)
	// row 1: []
	lb.Append(true)
	// row 2: [AMEX]
	lb.Append(true)
	sb.AppendValues([]string{"AMEX"}, nil)

	rec := bldr.NewRecordBatch()
	defer rec.Release()

	col := rec.Column(0).(*array.List)
	cases := []struct {
		idx  int
		want string
	}{
		{0, "VISA|MC"},
		{1, ""},
		{2, "AMEX"},
	}
	for _, c := range cases {
		got := FormatValue(col, c.idx)
		if got != c.want {
			t.Errorf("FormatValue[%d] = %q, want %q", c.idx, got, c.want)
		}
	}
}

// TestArrow_InferPulseSchemaListUTF8 builds an Arrow IPC stream with a
// LIST<UTF8> column, lets the reader's InferPulseSchema run, and
// asserts the column lands as set_*.
func TestArrow_InferPulseSchemaListUTF8(t *testing.T) {
	alloc := memory.NewGoAllocator()
	sc := arrow.NewSchema([]arrow.Field{
		{Name: "issuers", Type: arrow.ListOf(arrow.BinaryTypes.String)},
	}, nil)
	bldr := array.NewRecordBuilder(alloc, sc)
	defer bldr.Release()
	lb := bldr.Field(0).(*array.ListBuilder)
	sb := lb.ValueBuilder().(*array.StringBuilder)
	for i := 0; i < 10; i++ {
		lb.Append(true)
		sb.AppendValues([]string{"VISA", "MC"}, nil)
	}
	rec := bldr.NewRecordBatch()
	defer rec.Release()

	var buf bytes.Buffer
	fw, err := ipc.NewFileWriter(&buf, ipc.WithSchema(sc))
	if err != nil {
		t.Fatalf("NewFileWriter: %v", err)
	}
	if err := fw.Write(rec); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := fw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r := NewReaderFromBytes(buf.Bytes())
	defer r.Close()
	schema, err := r.InferPulseSchema()
	if err != nil {
		t.Fatalf("InferPulseSchema: %v", err)
	}
	got := schema.Field("issuers")
	if got == nil {
		t.Fatal("issuers field missing")
	}
	if !got.Type.IsSet() {
		t.Errorf("issuers.Type = %s, want set_*", got.Type)
	}
	// Drain row content through ReadRows to ensure the FormatValue
	// LIST path is exercised end-to-end and yields the pipe-joined
	// form the importer expects.
	if _, err := r.ReadHeader(); err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	var rows [][]string
	err = r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	if len(rows) == 0 || rows[0][0] != "VISA|MC" {
		t.Errorf("rows[0] = %v, want [VISA|MC]", rows)
	}
}
