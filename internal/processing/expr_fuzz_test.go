package processing

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/testutil/fuzzcontract"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// exprFuzzMaxInput caps one fuzzed expression: an expression is a
// request string, never a document.
const exprFuzzMaxInput = 4 << 10

// exprFuzzSchema is one field per value family an expression binds — a
// float64 slot, a u64 word, a date, a datetime, a dictionary-bearing
// categorical and set, and a decimal — plus a named vector over the
// float columns, so the fuzzer reaches every bind and builtin family.
func exprFuzzSchema(tb testing.TB) (*encoding.Schema, []vectors.Resolved, []*Record) {
	tb.Helper()
	cats := encoding.NewDictionary()
	sets := encoding.NewDictionary()
	for _, v := range []string{"north", "south", "east"} {
		if _, err := cats.Add(v); err != nil {
			tb.Fatal(err)
		}
		if _, err := sets.Add(v); err != nil {
			tb.Fatal(err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64, ByteOffset: 0},
		{Name: "y", Type: encoding.FieldTypeF64, ByteOffset: 8},
		{Name: "big", Type: encoding.FieldTypeU64, ByteOffset: 16},
		{Name: "d", Type: encoding.FieldTypeDate, ByteOffset: 24},
		{Name: "ts", Type: encoding.FieldTypeDateTime, ByteOffset: 28},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 36, Dictionary: cats},
		{Name: "tags", Type: encoding.FieldTypeSetU8, ByteOffset: 37, Dictionary: sets},
		{Name: "amt", Type: encoding.FieldTypeDecimal128, ByteOffset: 38, Precision: 18, Scale: 2},
	}}
	vecs, err := vectors.Resolve([]types.VectorSpec{{Name: "v", Fields: []string{"x", "y"}}}, schema)
	if err != nil {
		tb.Fatal(err)
	}
	full := NewRecordWithWide(schema, map[string]float64{"x": 1.5, "y": -2, "big": 7, "d": 19000, "ts": 1.6e9, "region": 1},
		nil, map[string]any{"tags": uint64(5), "amt": encoding.NewDecimal128FromInt(1234)})
	nulls := NewRecordWithNulls(schema, map[string]float64{"x": 3}, map[string]bool{"y": true, "region": true, "tags": true, "amt": true})
	return schema, vecs, []*Record{full, nulls}
}

// FuzzExprParse holds the expression compile path — the compile-once
// program FILTER_EXPRESSION and ATTR_FORMULA build from a request
// string (newExprProgram: vector and ref() static refusals, the
// calendar guard, the params fold, expr-lang's compile and the result
// refusal) — and the program's evaluation over a full and a null-input
// record, to the fuzz contract: no panic, a bounded run, and every
// error a registered *errors.CodedError.
func FuzzExprParse(f *testing.F) {
	for _, s := range []string{
		"x + y",
		"x > 1 && region == \"north\"",
		"region in [\"north\", \"east\"] ? 1 : 0",
		"\"north\" in tags",
		"len(tags) > 1",
		"weekday(d)",
		"month_index(d) - 1",
		"date_trunc(\"year\", d)",
		"weekday(to_day(ts))",
		"big > 3",
		"amt * 2",
		"abs(amt) > 1",
		"dot(v, v)",
		"norm(v)",
		"sum(v) / len(v)",
		"x ?? 0",
		"y == nil",
		"log(x) + sqrt(abs(y))",
		"let z = x * 2; z + y",
		"$env.x + $env[\"y\"]",
		"params.k + x",
		"ref(\"t\", \"k\")",
		"map(1..3, # * x)",
		"filter([1, 2, 3], # > x)",
		"x +",
		"region == ",
		"1 / 0",
		"undefined_name + 1",
		"",
	} {
		f.Add(s)
	}
	schema, vecs, records := exprFuzzSchema(f)
	opts := (*ExtensionRegistry)(nil).ExprOptions()
	contract := fuzzcontract.Contract{MaxInput: exprFuzzMaxInput}
	f.Fuzz(func(t *testing.T, src string) {
		if len(src) > exprFuzzMaxInput {
			src = src[:exprFuzzMaxInput] // the contract's cap, applied before the build too
		}
		for _, site := range []string{exprenv.SiteFormula, exprenv.SiteFilter} {
			var p *exprProgram
			contract.Run(t, []byte(src), func(in []byte) error {
				var err error
				p, err = newExprProgram(string(in), site, schema, vecs, nil, opts)
				return err
			})
			if p == nil {
				continue
			}
			// Each record is its own contract call, so a coded failure on
			// the full row never hides the null-input row's.
			for _, r := range records {
				contract.Run(t, []byte(src), func([]byte) error {
					_, _, err := p.runRecord(r)
					return err
				})
			}
		}
	})
}
