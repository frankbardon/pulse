package pulse_test

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// orderabilityCohort writes a hand-built cohort carrying the types a
// CSV import cannot infer: a nullable decimal128 (scale 2) and a
// set_u8. Rows: id 1..5, amt 10.50, -3.25, 2.50, 100.00, null.
func orderabilityCohort(t *testing.T, fs afero.Fs, path string) {
	t.Helper()
	tags := encoding.NewDictionary()
	for _, l := range []string{"x", "y"} {
		if _, err := tags.Add(l); err != nil {
			t.Fatal(err)
		}
	}
	sch := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU8},
		{Name: "amt", Type: encoding.FieldTypeDecimal128, Nullable: true, Precision: 18, Scale: 2, ByteOffset: 1, CsvColumnIdx: 1},
		{Name: "tags", Type: encoding.FieldTypeSetU8, Dictionary: tags, ByteOffset: 17, CsvColumnIdx: 2},
	}}
	var buf []byte
	w := &byteSink{b: &buf}
	if err := encoding.WriteHeader(w); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(w, sch); err != nil {
		t.Fatal(err)
	}
	for i, m := range []int64{1050, -325, 250, 10000, 0} {
		rec := make([]byte, sch.RecordByteSize())
		rec[0] = byte(i + 1)
		if i == 4 {
			encoding.BitmapSetNull(rec[len(rec)-sch.BitmapByteSize():], 1)
		} else {
			e := encoding.EncodeDecimal128(encoding.NewDecimal128FromInt(m))
			copy(rec[1:], e[:])
		}
		rec[17] = byte(1 << (i % 2))
		buf = append(buf, rec...)
	}
	if err := afero.WriteFile(fs, path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestWindowOrderBy_OrderabilityPredictMatchesRuntime: one orderability
// rule (internal/descriptor.IsOrderableType, applied by the shared
// FieldRefRefusals walk) decides a window order_by key on both sides.
// Every type the window comparator orders correctly is accepted by
// predict AND ordered by the runtime — datetime by signed epoch seconds
// (pre-1970 before 1970), packed_bool false before true, decimal128 by
// value at the column's scale (before, every decimal compared equal and
// the rows kept input order); nulls last. A set_* key, which the
// comparator cannot order, is refused by both with one code, message
// and details (before, predict refused and the runtime answered
// arbitrary row numbers).
func TestWindowOrderBy_OrderabilityPredictMatchesRuntime(t *testing.T) {
	fs := afero.NewMemMapFs()
	csv := "id,ts,flag\n1,2024-01-01T00:00:00Z,true\n2,1969-12-31T23:59:59Z,false\n3,1950-06-01T12:00:00Z,true\n4,1970-01-01T00:00:00Z,false\n"
	if err := afero.WriteFile(fs, "o.csv", []byte(csv), 0o644); err != nil {
		t.Fatal(err)
	}
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	res, err := p.ImportFile(ctx, pulse.ImportSpec{SourcePath: "o.csv",
		ColumnTypeOverrides: map[string]string{"id": "u8", "flag": "packed_bool"}})
	if err != nil {
		t.Fatal(err)
	}
	ins, err := p.Inspect(ctx, res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(ins.Fields[1].Type, " ", ins.Fields[2].Type); got != "datetime packed_bool" {
		t.Fatalf("imported types = %s, want datetime packed_bool — the test would prove nothing", got)
	}
	orderabilityCohort(t, fs, "dec.pulse")

	req := func(cohort, field string) *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: cohort},
			Windows: []*types.Window{{Type: types.WIN_ROW_NUMBER, Label: "rn", OrderBy: []types.OrderKey{{Field: field}}}},
			Sort:    []types.OrderKey{{Field: "id"}}} // retains id under projection
	}
	ordered := map[string]struct {
		cohort, field string
		want          map[float64]float64 // id -> row number
	}{
		"datetime pre-1970": {res.Path, "ts", map[float64]float64{3: 1, 2: 2, 4: 3, 1: 4}},
		"packed_bool":       {res.Path, "flag", map[float64]float64{2: 1, 4: 2, 1: 3, 3: 4}},
		"decimal128":        {"dec.pulse", "amt", map[float64]float64{2: 1, 3: 2, 1: 3, 4: 4, 5: 5}},
	}
	for name, c := range ordered {
		t.Run(name, func(t *testing.T) {
			resp, err := p.Process(ctx, req(c.cohort, c.field))
			if err != nil {
				t.Fatalf("runtime: %v", err)
			}
			got := map[float64]float64{}
			for _, r := range resp.Data {
				id, _ := toF64(r["id"])
				rn, _ := toF64(r["rn"])
				got[id] = rn
			}
			if fmt.Sprint(got) != fmt.Sprint(c.want) {
				t.Fatalf("id -> row number = %v, want %v", got, c.want)
			}
			if env := predictEnvelope(t, p, fs, c.cohort, req(c.cohort, c.field)); len(env.Errors) != 0 {
				t.Fatalf("predict refused what the runtime orders: %+v", env.Errors)
			}
		})
	}

	t.Run("set refused on both sides", func(t *testing.T) {
		_, err := p.Process(ctx, req("dec.pulse", "tags"))
		ce := requireCode(t, err, errors.PULSE_WINDOW_INVALID)
		env := predictEnvelope(t, p, fs, "dec.pulse", req("dec.pulse", "tags"))
		if len(env.Errors) != 1 {
			t.Fatalf("predict errors = %+v, want exactly the orderability refusal", env.Errors)
		}
		e := env.Errors[0]
		if e.Code != string(ce.Code) || e.Message != ce.Message || fmt.Sprint(e.Details) != fmt.Sprint(ce.Details) {
			t.Fatalf("predict %s %q %v != runtime %s %q %v", e.Code, e.Message, e.Details, ce.Code, ce.Message, ce.Details)
		}
	})
}

func toF64(v any) (float64, bool) {
	f, err := strconv.ParseFloat(fmt.Sprint(v), 64)
	return f, err == nil
}
