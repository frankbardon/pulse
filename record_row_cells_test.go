package pulse_test

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// everyTypeCohort writes a single-file cohort declaring every field
// type once, nullable, named after the type ("t_<type>"), each on its
// own byte(s). Row 0 holds a value in every column — date and datetime
// pre-1970 (signed); row 1 is null in every column but t_u8 (the row
// key, 1 and 2). withVectors=false leaves the two vector types out (the
// export oracle cannot write them yet).
func everyTypeCohort(t *testing.T, fs afero.Fs, path string, withVectors bool) *encoding.Schema {
	t.Helper()
	dict := func(prefix string, n int) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for i := 0; i < n; i++ {
			if _, err := d.Add(fmt.Sprintf("%s%d", prefix, i)); err != nil {
				t.Fatal(err)
			}
		}
		return d
	}
	var fields []encoding.Field
	var vectors []encoding.VectorField
	off := 0
	for ft := encoding.FieldType(0); ft.IsKnown(); ft++ {
		if ft.IsVector() {
			if !withVectors {
				continue
			}
			vectors = append(vectors, encoding.VectorField{FieldIndex: len(fields), Dim: 2})
		}
		f := encoding.Field{Name: "t_" + ft.String(), Type: ft, Nullable: ft != encoding.FieldTypeU8, ByteOffset: off}
		switch {
		case ft == encoding.FieldTypeDecimal128:
			f.Precision, f.Scale = 30, 4
		case ft.IsCategorical():
			f.Dictionary = dict("c", 5)
		case ft.IsSet():
			f.Dictionary = dict("m", int(ft.MaxSetEntries()))
		}
		if w := ft.ByteSize(); w > 0 {
			off += w
		} else {
			off++
		}
		fields = append(fields, f)
	}
	if want := map[bool]int{false: 20, true: 22}[withVectors]; len(fields) != want {
		t.Fatalf("walked %d field types, want %d", len(fields), want)
	}
	sch := &encoding.Schema{Fields: fields, Vectors: vectors}
	var buf bytes.Buffer
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(encx.WritePreamble(&buf, sch))
	for row := 0; row < 2; row++ {
		bm := make([]byte, sch.BitmapByteSize())
		for i := range sch.Fields {
			f := &sch.Fields[i]
			null := row == 1 && f.Type != encoding.FieldTypeU8
			if null {
				encoding.BitmapSetNull(bm, i)
			}
			var raw uint64
			switch f.Type {
			case encoding.FieldTypeU4:
				raw = 5
			case encoding.FieldTypeU8:
				raw = uint64(row + 1)
			case encoding.FieldTypeF32:
				raw = uint64(0xC0200000) // -2.5
			case encoding.FieldTypeF64:
				raw = 0x40934A0000000000 // 1234.5
			case encoding.FieldTypeDate:
				raw = uint64(uint32(0xFFFFFFFF)) // epoch day -1: 1969-12-31
			case encoding.FieldTypeDateTime:
				raw = ^uint64(0) // epoch second -1: 1969-12-31T23:59:59Z
			case encoding.FieldTypePackedBool:
				raw = 1
			case encoding.FieldTypeCategoricalU8, encoding.FieldTypeCategoricalU16, encoding.FieldTypeCategoricalU32:
				raw = 3
			case encoding.FieldTypeSetU8, encoding.FieldTypeSetU16, encoding.FieldTypeSetU32, encoding.FieldTypeSetU64:
				raw = 0b101
			default:
				raw = 200
			}
			if null {
				raw = 0
			}
			switch {
			case f.Type == encoding.FieldTypeU4:
				must(encoding.WriteNibble(&buf, false, uint8(raw)))
			case f.Type == encoding.FieldTypePackedBool:
				must(encoding.WriteBit(&buf, 0, raw == 1))
			case f.Type == encoding.FieldTypeDecimal128:
				d := encoding.NewDecimal128FromInt(-1234567) // -123.4567
				if null {
					d = encoding.ZeroDecimal128()
				}
				must(encoding.WriteDecimal128(&buf, d))
			case f.Type.IsWideSet():
				m := encoding.SetMaskFromUint64(0b101).WithBit(100)
				if null {
					m = encoding.SetMaskFromUint64(0)
				}
				must(encoding.WriteSetMask(&buf, f.Type, m))
			case f.Type.IsVector():
				v := []float64{-2.5, 0.125}
				if null {
					v = nil // the zeroed placeholder
				}
				must(encoding.WriteVector(&buf, f.Type, 2, v))
			default:
				must(encoding.WriteFieldValue(&buf, f.Type, raw))
			}
		}
		must(encoding.WriteBitmap(&buf, bm))
	}
	if err := afero.WriteFile(fs, path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return sch
}

// captureWriter is an export target that keeps every row.
type captureWriter struct {
	cols []string
	rows [][]any
}

func (w *captureWriter) WriteHeader(c []string) error { w.cols = c; return nil }
func (w *captureWriter) WriteRow(v []any) error {
	w.rows = append(w.rows, append([]any(nil), v...))
	return nil
}
func (w *captureWriter) Close() error { return nil }

// TestRecordRowCells_EveryFieldTypeIsJSONNative: every path that hands
// record values to a caller as a row — record rows of a windowed
// Process (Response.Data), Sample and Lookup — marshals every field
// type to a JSON-native cell. decimal128 renders as the decimal string
// at the column scale, exactly as export renders it and as a grouped
// decimal aggregate already did; before, the raw Decimal128 (unexported
// fields) marshalled as {} on all three paths. The other families were
// already JSON-native and are pinned unchanged: numbers (date /
// datetime as signed epoch days / seconds — pre-1970 negative, never
// the unsigned on-wire word; packed_bool 0/1), categorical as the
// export label, sets as the export's label list, vectors as the
// element array. A null cell is absent.
func TestRecordRowCells_EveryFieldTypeIsJSONNative(t *testing.T) {
	fs := afero.NewMemMapFs()
	sch := everyTypeCohort(t, fs, "all.pulse", true)
	everyTypeCohort(t, fs, "noVec.pulse", false)
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// Export's text per column, row 0 — the oracle for the non-numeric
	// families.
	cw := &captureWriter{}
	if _, err := p.Export(ctx, &pio.ExportJob{Source: "noVec.pulse", Target: cw}); err != nil {
		t.Fatal(err)
	}
	export := map[string]any{}
	for i, c := range cw.cols {
		export[c] = cw.rows[0][i]
	}

	want := map[string]string{
		"t_u4": "5", "t_u8": "1", "t_u16": "200", "t_u32": "200", "t_u64": "200",
		"t_f32": "-2.5", "t_f64": "1234.5",
		"t_date": "-1", "t_datetime": "-1", "t_packed_bool": "1",
		"t_categorical_u8": `"c3"`, "t_categorical_u16": `"c3"`, "t_categorical_u32": `"c3"`,
		"t_decimal128": `"-123.4567"`,
		"t_set_u8":     `["m0","m2"]`, "t_set_u16": `["m0","m2"]`, "t_set_u32": `["m0","m2"]`, "t_set_u64": `["m0","m2"]`,
		"t_set_u128": `["m0","m2","m100"]`, "t_set_u256": `["m0","m2","m100"]`,
		"t_vec_f32": `[-2.5,0.125]`, "t_vec_f64": `[-2.5,0.125]`,
	}
	if len(want) != len(sch.Fields) {
		t.Fatalf("want covers %d types, schema has %d", len(want), len(sch.Fields))
	}
	// The JSON string families must match export's text.
	for _, name := range []string{"t_decimal128", "t_categorical_u8", "t_categorical_u16", "t_categorical_u32"} {
		if got := fmt.Sprintf("%q", export[name]); got != want[name] {
			t.Fatalf("%s: export text %s, want %s — the oracle disagrees", name, got, want[name])
		}
	}
	if export["t_date"] != "1969-12-31" || export["t_datetime"] != "1969-12-31T23:59:59Z" ||
		export["t_set_u128"] != "m0|m2|m100" || export["t_packed_bool"] != "true" {
		t.Fatalf("export oracle: date %v datetime %v set_u128 %v packed_bool %v", export["t_date"],
			export["t_datetime"], export["t_set_u128"], export["t_packed_bool"])
	}

	paths := map[string]func() []map[string]any{
		"process record rows": func() []map[string]any {
			// Record rows carry the full record under the default
			// projection (#201d).
			resp, err := p.Process(ctx, &types.Request{Cohort: &types.Cohort{Filename: "all.pulse"},
				Windows: []*types.Window{{Type: types.WIN_ROW_NUMBER, Label: "rn", OrderBy: []types.OrderKey{{Field: "t_u8"}}}}})
			if err != nil {
				t.Fatal(err)
			}
			return resp.Data
		},
		"sample": func() []map[string]any {
			rows, err := p.Sample(ctx, "all.pulse", 2)
			if err != nil {
				t.Fatal(err)
			}
			return rows
		},
		"lookup": func() []map[string]any {
			if _, err := p.BuildIndex(ctx, "all.pulse", []string{"t_u8"}); err != nil {
				t.Fatal(err)
			}
			var out []map[string]any
			for _, v := range []string{"1", "2"} {
				res, err := p.Lookup(ctx, &types.LookupRequest{Cohort: &types.Cohort{Filename: "all.pulse"}, Field: "t_u8", Value: v})
				if err != nil {
					t.Fatal(err)
				}
				out = append(out, res.Rows...)
			}
			return out
		},
	}
	for name, rowsOf := range paths {
		t.Run(name, func(t *testing.T) {
			rows := rowsOf()
			if len(rows) != 2 {
				t.Fatalf("%d rows, want 2", len(rows))
			}
			b, err := json.Marshal(rows)
			if err != nil {
				t.Fatal(err)
			}
			var decoded []map[string]json.RawMessage
			if err := json.Unmarshal(b, &decoded); err != nil {
				t.Fatal(err)
			}
			for col, w := range want {
				if got := string(decoded[0][col]); got != w {
					t.Errorf("row 0 %s = %s, want %s", col, got, w)
				}
				if col == "t_u8" {
					continue
				}
				if got, present := decoded[1][col]; present {
					t.Errorf("row 1 %s = %s, want absent (null)", col, got)
				}
			}
			// The Go value an embedder reads is the same decimal string.
			if got, ok := rows[0]["t_decimal128"].(string); !ok || got != "-123.4567" {
				t.Errorf("Go cell t_decimal128 = %#v, want the string -123.4567", rows[0]["t_decimal128"])
			}
		})
	}
}

// TestRecordRowCells_DecimalWindowCopyRendersAndOrders: a window
// order_by and a Request.Sort over a decimal column order by value, and
// the record row still renders the decimal string — the cell keeps its
// value and scale until the row is final. A value window over the
// decimal itself (WIN_LAG) is refused by the accepted-types table
// (internal/fieldaccept, TODO #199), so it cannot copy the cell.
func TestRecordRowCells_DecimalWindowCopyRendersAndOrders(t *testing.T) {
	fs := afero.NewMemMapFs()
	orderabilityCohort(t, fs, "dec.pulse") // id 1..5, amt 10.50 -3.25 2.50 100.00 null
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Process(context.Background(), &types.Request{Cohort: &types.Cohort{Filename: "dec.pulse"},
		Windows: []*types.Window{{Type: types.WIN_LAG, Field: "amt", Label: "prev", OrderBy: []types.OrderKey{{Field: "amt"}}}}})
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_FIELD_TYPE_UNSUPPORTED {
		t.Fatalf("WIN_LAG over decimal128: %v, want PULSE_FIELD_TYPE_UNSUPPORTED", err)
	}
	resp, err := p.Process(context.Background(), &types.Request{Cohort: &types.Cohort{Filename: "dec.pulse"},
		Windows: []*types.Window{{Type: types.WIN_ROW_NUMBER, Label: "prev", OrderBy: []types.OrderKey{{Field: "amt"}}}},
		Sort:    []types.OrderKey{{Field: "amt", Desc: true}}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(resp.Data)
	var rows []map[string]any
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rows {
		got = append(got, fmt.Sprintf("%v/%v", r["amt"], r["prev"]))
	}
	want := "[100.00/4 10.50/3 2.50/2 -3.25/1 <nil>/5]"
	if fmt.Sprint(got) != want {
		t.Errorf("amt/prev = %v, want %s", got, want)
	}
}
