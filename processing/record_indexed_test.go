package processing

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// nameKeyedOnly hides *Record's index-keyed methods so the reuse decoder
// must take the name-keyed shim. It is the reference arm the positional
// path is compared against.
type nameKeyedOnly struct{ r *Record }

func (n nameKeyedOnly) SetNumeric(name string, v float64) { n.r.SetNumeric(name, v) }
func (n nameKeyedOnly) SetNullField(name string)          { n.r.SetNullField(name) }
func (n nameKeyedOnly) SetWideField(name string, v any)   { n.r.SetWideField(name, v) }
func (n nameKeyedOnly) ClearForRow()                      { n.r.ClearForRow() }

func indexedRecordSchema() *encoding.Schema {
	setDict := func(n int) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for i := 0; i < n; i++ {
			_, _ = d.Add(fmt.Sprintf("opt-%d", i))
		}
		return d
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32},
		{Name: "lo", Type: encoding.FieldTypeU4, BitPosition: 0},
		{Name: "hi", Type: encoding.FieldTypeU4, BitPosition: 4},
		{Name: "flag", Type: encoding.FieldTypePackedBool, BitPosition: 0},
		{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 38, Scale: 2, Nullable: true},
		{Name: "rate", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "tags", Type: encoding.FieldTypeSetU8, Dictionary: setDict(8), Nullable: true},
		{Name: "wide", Type: encoding.FieldTypeSetU128, Dictionary: setDict(128)},
		{Name: "tail", Type: encoding.FieldTypeU16},
	}}
}

// encodeIndexedRows writes n synthetic rows for indexedRecordSchema with
// a rotating null pattern over the three nullable fields.
func encodeIndexedRows(t *testing.T, schema *encoding.Schema, n int) []byte {
	t.Helper()
	var buf bytes.Buffer
	for i := 0; i < n; i++ {
		must := func(err error) {
			if err != nil {
				t.Fatalf("row %d: %v", i, err)
			}
		}
		must(encoding.WriteFieldValue(&buf, encoding.FieldTypeU32, uint64(1000+i)))
		must(encoding.WriteNibble(&buf, false, uint8(i%16)))
		must(encoding.WriteNibble(&buf, true, uint8((i*7)%16)))
		must(encoding.WriteBit(&buf, 0, i%3 == 0))
		must(encoding.WriteDecimal128(&buf, encoding.NewDecimal128FromInt(int64(i*125-400))))
		must(encoding.WriteFieldValue(&buf, encoding.FieldTypeF64, math.Float64bits(float64(i)*1.5)))
		must(encoding.WriteFieldValue(&buf, encoding.FieldTypeSetU8, uint64(i*37)&0xFF))
		must(encoding.WriteSetMask(&buf, encoding.FieldTypeSetU128,
			encoding.SetMaskFromUint64(uint64(i)).WithBit(64+i%64)))
		must(encoding.WriteFieldValue(&buf, encoding.FieldTypeU16, uint64(i*11)))
		bm := make([]byte, schema.BitmapByteSize())
		for k, pos := range []int{4, 5, 6} {
			if (i>>k)&1 == 1 {
				encoding.BitmapSetNull(bm, pos)
			}
		}
		must(encoding.WriteBitmap(&buf, bm))
	}
	return buf.Bytes()
}

func snapshotRecord(r *Record) (map[string]float64, map[string]bool, map[string]any) {
	v := make(map[string]float64, len(r.values))
	for k, x := range r.values {
		v[k] = x
	}
	nl := make(map[string]bool, len(r.nulls))
	for k, x := range r.nulls {
		nl[k] = x
	}
	w := make(map[string]any, len(r.wide))
	for k, x := range r.wide {
		w[k] = x
	}
	return v, nl, w
}

// TestRecord_IndexedReusePathMatchesNameKeyed drives a reused *Record
// through the index-keyed decoder (it implements
// encoding.IndexedReusableRecord) and a name-keyed-only view of an
// identical Record through the shim, across both the full-stride and the
// plan path, and asserts the Record state is identical row by row —
// bit-packed nibbles/bools, decimal128, narrow and wide sets, and the
// trailing null bitmap included.
func TestRecord_IndexedReusePathMatchesNameKeyed(t *testing.T) {
	schema := indexedRecordSchema()
	const rows = 16
	raw := encodeIndexedRows(t, schema, rows)

	for _, tc := range []struct {
		name     string
		retained []string // nil ⇒ full-stride ReadRecordReused
	}{
		{name: "full"},
		{name: "plan-bitpacked-and-nullable", retained: []string{"hi", "amount", "tags"}},
		{name: "plan-tail-only", retained: []string{"tail"}},
		{name: "plan-wide-set", retained: []string{"flag", "wide", "rate"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var plan *encoding.DecodePlan
			var keep encoding.FieldFilter
			if tc.retained != nil {
				var err error
				if plan, err = schema.BuildDecodePlan(tc.retained); err != nil {
					t.Fatalf("BuildDecodePlan: %v", err)
				}
				set := make(map[string]bool, len(tc.retained))
				for _, n := range tc.retained {
					set[n] = true
				}
				keep = func(n string) bool { return set[n] }
			}

			idxRec := NewReusableRecord(schema)
			refRec := NewReusableRecord(schema)
			idxRR := encoding.NewRecordReader(bytes.NewReader(raw), schema)
			refRR := encoding.NewRecordReader(bytes.NewReader(raw), schema)
			read := func(rr *encoding.RecordReader, rec encoding.ReusableRecord) error {
				if plan == nil {
					return rr.ReadRecordReused(rec)
				}
				return rr.ReadRecordReusedWithPlan(rec, keep, plan)
			}
			for row := 0; ; row++ {
				errIdx := read(idxRR, idxRec)
				errRef := read(refRR, nameKeyedOnly{refRec})
				if errIdx != errRef {
					t.Fatalf("row %d: indexed err=%v, name-keyed err=%v", row, errIdx, errRef)
				}
				if errIdx == io.EOF {
					if row != rows {
						t.Fatalf("decoded %d rows, want %d", row, rows)
					}
					return
				}
				if errIdx != nil {
					t.Fatalf("row %d: %v", row, errIdx)
				}
				gv, gn, gw := snapshotRecord(idxRec)
				wv, wn, ww := snapshotRecord(refRec)
				if len(wv) == 0 {
					t.Fatalf("row %d: reference decoded nothing", row)
				}
				if !reflect.DeepEqual(gv, wv) || !reflect.DeepEqual(gn, wn) || !reflect.DeepEqual(gw, ww) {
					t.Fatalf("row %d mismatch:\n  indexed=%v %v %v\n  name-keyed=%v %v %v", row, gv, gn, gw, wv, wn, ww)
				}
			}
		})
	}
}
