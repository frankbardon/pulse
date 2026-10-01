package service

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/processing"
	"github.com/spf13/afero"
)

// TestStreamingIterator_BufferedPositionalMatchesMapDecode pins the
// buffered (non-reuse) iterator's fresh positional records against the
// map decode they replaced: for every row and every schema field, each
// public accessor and AllValues agree with a record built from
// ReadRecordWithWide / ReadRecordWithWidePlan maps — full decode and a
// projected decode (whose records are sized to the retained set).
func TestStreamingIterator_BufferedPositionalMatchesMapDecode(t *testing.T) {
	const rows = 300
	schema, payload := buildWideCohort(t, 40, rows)
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatal(err)
	}
	buf.Write(payload)
	mem := afero.NewMemMapFs()
	if err := afero.WriteFile(mem, "/c.pulse", buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	// Retain a mix: categorical, date, decimal, set, bit-packed, nullable
	// tail — whatever the fixture put at these positions.
	retained := map[string]bool{}
	for i, f := range schema.Fields {
		if i%3 == 0 || f.Type == encoding.FieldTypeDecimal128 || f.Type.IsSet() || f.Nullable {
			retained[f.Name] = true
		}
	}
	keep := func(n string) bool { return retained[n] }

	for _, tc := range []struct {
		name string
		keep encx.FieldFilter
	}{
		{"full", nil},
		{"projected", keep},
	} {
		t.Run(tc.name, func(t *testing.T) {
			it := newStreamingIterator(mem, "/c.pulse", schema)
			defer it.Close()
			var plan *encx.DecodePlan
			if tc.keep != nil {
				it.SetProjection(tc.keep, len(retained))
				plan = it.plan
				if plan == nil {
					t.Fatal("SetProjection installed no plan")
				}
			}
			ref := encx.NewRecordReader(bytes.NewReader(payload), schema)
			n := 0
			for it.Next() {
				got := it.Record()
				values := map[string]float64{}
				nulls := map[string]bool{}
				wide := map[string]any{}
				var err error
				if plan != nil {
					err = ref.ReadRecordWithWidePlan(values, nulls, wide, tc.keep, plan)
				} else {
					err = ref.ReadRecordWithWide(values, nulls, wide)
				}
				if err != nil {
					t.Fatalf("row %d reference decode: %v", n, err)
				}
				want := processing.NewRecordWithWide(schema, values, nulls, wide)
				assertRecordsAgree(t, n, schema, got, want)
				n++
			}
			if err := it.Err(); err != nil {
				t.Fatal(err)
			}
			if n != rows {
				t.Fatalf("decoded %d rows, want %d", n, rows)
			}
		})
	}
}

func assertRecordsAgree(t *testing.T, row int, schema *encoding.Schema, got, want *processing.Record) {
	t.Helper()
	for _, f := range schema.Fields {
		n := f.Name
		gv, gok := got.NumericValue(n)
		wv, wok := want.NumericValue(n)
		if gv != wv || gok != wok {
			t.Fatalf("row %d NumericValue(%q) = (%v,%v), map decode (%v,%v)", row, n, gv, gok, wv, wok)
		}
		if got.IsNull(n) != want.IsNull(n) {
			t.Fatalf("row %d IsNull(%q) disagrees", row, n)
		}
		gw, gwok := got.WideValue(n)
		ww, wwok := want.WideValue(n)
		if !reflect.DeepEqual(gw, ww) || gwok != wwok {
			t.Fatalf("row %d WideValue(%q) = (%v,%v), map decode (%v,%v)", row, n, gw, gwok, ww, wwok)
		}
		gs, gsok := got.StringValue(n)
		ws, wsok := want.StringValue(n)
		if gs != ws || gsok != wsok {
			t.Fatalf("row %d StringValue(%q) disagrees", row, n)
		}
		gm, gmok := got.SetMaskValue(n)
		wm, wmok := want.SetMaskValue(n)
		if gm != wm || gmok != wmok {
			t.Fatalf("row %d SetMaskValue(%q) disagrees", row, n)
		}
	}
	if !reflect.DeepEqual(got.AllValues(), want.AllValues()) {
		t.Fatalf("row %d AllValues:\n got  %v\n want %v", row, got.AllValues(), want.AllValues())
	}
}
