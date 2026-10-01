package processing

import (
	"bytes"
	"fmt"
	"io"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
)

// Wide-value decode cost on the positional reuse path, per wide kind.
//
// Each sub-benchmark decodes the same synthetic rows through
// ReadRecordReused into one reused *Record and reports allocs/row. The
// "u32" arm is the no-wide baseline; every other arm adds exactly one
// wide-capable column, so its allocs/row minus the baseline is what that
// kind costs per row. Masks are chosen at or above 256 so a narrow rung
// cannot hide behind the runtime's small-integer interface cache.

func wideBenchSchema(ft encoding.FieldType) *encoding.Schema {
	f := encoding.Field{Name: "w", Type: ft}
	switch {
	case ft == encoding.FieldTypeDecimal128:
		f.Precision, f.Scale = 18, 2
	case ft.IsSet():
		d := encoding.NewDictionary()
		for i := 0; i < int(ft.MaxSetEntries()); i++ {
			_, _ = d.Add(fmt.Sprintf("m%d", i))
		}
		f.Dictionary = d
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32},
		f,
	}}
}

func wideBenchRows(b *testing.B, schema *encoding.Schema, n int) []byte {
	b.Helper()
	var buf bytes.Buffer
	ft := schema.Fields[1].Type
	for i := 0; i < n; i++ {
		var err error
		if err = encoding.WriteFieldValue(&buf, encoding.FieldTypeU32, uint64(i)); err != nil {
			b.Fatal(err)
		}
		switch {
		case ft == encoding.FieldTypeU32:
			err = encoding.WriteFieldValue(&buf, ft, uint64(i))
		case ft == encoding.FieldTypeDecimal128:
			err = encoding.WriteDecimal128(&buf, encoding.NewDecimal128FromInt(int64(i*125+1)))
		case ft.IsWideSet():
			err = encoding.WriteSetMask(&buf, ft, encoding.SetMaskFromUint64(uint64(256+i)).WithBit(100))
		default:
			err = encoding.WriteFieldValue(&buf, ft, uint64(256+i%200))
		}
		if err != nil {
			b.Fatal(err)
		}
	}
	return buf.Bytes()
}

func BenchmarkRecordReuse_WideKinds(b *testing.B) {
	const rows = 4096
	for _, ft := range []encoding.FieldType{
		encoding.FieldTypeU32,
		encoding.FieldTypeDecimal128,
		encoding.FieldTypeSetU16,
		encoding.FieldTypeSetU64,
		encoding.FieldTypeSetU128,
		encoding.FieldTypeSetU256,
	} {
		schema := wideBenchSchema(ft)
		raw := wideBenchRows(b, schema, rows)
		b.Run(ft.String(), func(b *testing.B) {
			b.ReportAllocs()
			rec := NewReusableRecord(schema)
			for b.Loop() {
				rr := encx.NewRecordReader(bytes.NewReader(raw), schema)
				for {
					if err := rr.ReadRecordReused(rec); err == io.EOF {
						break
					} else if err != nil {
						b.Fatal(err)
					}
					if _, ok := rec.SetMaskValue("w"); !ok && ft.IsSet() {
						b.Fatal("set value lost")
					}
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*rows), "ns/row")
		})
	}
}
