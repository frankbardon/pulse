package processing

import (
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/expr-lang/expr"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/types"
)

// Compile-once FILTER_EXPRESSION / ATTR_FORMULA (E5-S5). The gates:
//
//   - TestExprValueType_MatchesAllValues — the build prototype declares
//     every field type exactly as AllValues surfaces it, on every decode
//     path, so type checking at build equals the old per-row check.
//   - TestRecord_ExprValueMatchesAllValues — the per-row subset env holds
//     exactly the values AllValues would.
//   - TestExprProgram_MatchesPerRowCompile — on every row whose inputs
//     are present, verdicts and formula values equal the old per-row
//     compile's, byte for byte.
//   - TestExprProgram_CompilesOncePerBuild — the compile count is fixed
//     per build, independent of the row count.
//   - TestExprFilter_NullSemantics / TestExprFormula_NullSemantics — the
//     documented nil binding.
//   - TestExprProgram_CompileErrorsAtBuild — same code and message, now
//     raised at build (deferred to the first row only for a name the
//     schema cannot declare).
//   - TestExprProgram_ConcurrentRuns — one program shared across
//     goroutines, lazy compiles included (run under -race).

func exprTestDict(prefix string, n int) *encoding.Dictionary {
	d := encoding.NewDictionary()
	for i := range n {
		_, _ = d.Add(fmt.Sprintf("%s%d", prefix, i))
	}
	return d
}

// exprAllTypesSchema carries one nullable field of every field type,
// bit-packed ones separated so each owns its byte, plus a dictionary-
// less categorical twin only a map-built record can carry.
func exprAllTypesSchema(t testing.TB) *encoding.Schema {
	t.Helper()
	var fields []encoding.Field
	for i := 0; ; i++ {
		ft := encoding.FieldType(i)
		if !ft.IsKnown() {
			break
		}
		f := encoding.Field{Name: "f_" + ft.String(), Type: ft, Nullable: true}
		switch {
		case ft.IsCategorical():
			f.Dictionary = exprTestDict("c", 3)
		case ft.IsSet():
			f.Dictionary = exprTestDict("s", int(min(ft.MaxSetEntries(), 130)))
		case ft == encoding.FieldTypeDecimal128:
			f.Precision, f.Scale = 38, 2
		}
		fields = append(fields, f)
	}
	return &encoding.Schema{Fields: fields}
}

// encodeExprRows writes n rows of s; row r nulls every field when
// r%3 == 2.
func encodeExprRows(t testing.TB, s *encoding.Schema, n int) []byte {
	t.Helper()
	var buf bytes.Buffer
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	for r := range n {
		for _, f := range s.Fields {
			v := uint64(r % 3)
			switch {
			case f.Type.IsBitPacked():
				buf.WriteByte(byte(v))
			case f.Type == encoding.FieldTypeDecimal128:
				must(encoding.WriteDecimal128(&buf, encoding.NewDecimal128FromInt(int64(r*125))))
			case f.Type.IsWideSet():
				must(encoding.WriteSetMask(&buf, f.Type, encoding.SetMaskFromUint64(v).WithBit(100)))
			case f.Type == encoding.FieldTypeF64:
				must(encoding.WriteFieldValue(&buf, f.Type, math.Float64bits(float64(r)+0.5)))
			case f.Type == encoding.FieldTypeF32:
				must(encoding.WriteFieldValue(&buf, f.Type, uint64(math.Float32bits(float32(r)+0.5))))
			default:
				must(encoding.WriteFieldValue(&buf, f.Type, v))
			}
		}
		bm := make([]byte, s.BitmapByteSize())
		if r%3 == 2 {
			for i := range s.Fields {
				encoding.BitmapSetNull(bm, i)
			}
		}
		buf.Write(bm)
	}
	return buf.Bytes()
}

// exprDecodePaths decodes raw through every record-producing path the
// engine uses: the reuse decoder (full stride and plan), a fresh
// buffered record per row under a projected plan, and the map decoder
// into NewRecordWithWide. fn sees each record.
func exprDecodePaths(t *testing.T, s *encoding.Schema, raw []byte, fn func(path string, r *Record)) {
	t.Helper()
	names := make([]string, len(s.Fields))
	for i := range s.Fields {
		names[i] = s.Fields[i].Name
	}
	plan, err := encx.BuildDecodePlan(s, names)
	if err != nil {
		t.Fatal(err)
	}
	keepAll := encx.FieldFilter(func(string) bool { return true })
	loop := func(path string, read func(rr *encx.RecordReader) (*Record, error)) {
		rr := encx.NewRecordReader(bytes.NewReader(raw), s)
		for {
			rec, err := read(rr)
			if err == io.EOF {
				return
			}
			if err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			fn(path, rec)
		}
	}
	reused := NewReusableRecord(s)
	loop("reuse-full", func(rr *encx.RecordReader) (*Record, error) {
		return reused, rr.ReadRecordReused(reused)
	})
	reusedPlan := NewReusableRecord(s)
	loop("reuse-plan", func(rr *encx.RecordReader) (*Record, error) {
		return reusedPlan, rr.ReadRecordReusedWithPlan(reusedPlan, keepAll, plan)
	})
	binding := BindRecords(s, keepAll)
	loop("buffered-plan", func(rr *encx.RecordReader) (*Record, error) {
		rec := binding.NewRecord()
		return rec, rr.ReadRecordReusedWithPlan(rec, keepAll, plan)
	})
	loop("map-decode", func(rr *encx.RecordReader) (*Record, error) {
		vals, nulls, wide := map[string]float64{}, map[string]bool{}, map[string]any{}
		if err := rr.ReadRecordWithWide(vals, nulls, wide); err != nil {
			return nil, err
		}
		return NewRecordWithWide(s, vals, nulls, wide), nil
	})
}

func TestExprValueType_MatchesAllValues(t *testing.T) {
	s := exprAllTypesSchema(t)
	raw := encodeExprRows(t, s, 6)
	seen := map[string]map[string]bool{} // path → field → present on some row
	exprDecodePaths(t, s, raw, func(path string, r *Record) {
		if seen[path] == nil {
			seen[path] = map[string]bool{}
		}
		env := r.AllValues()
		for i := range s.Fields {
			f := &s.Fields[i]
			v, ok := env[f.Name]
			if !ok {
				continue
			}
			seen[path][f.Name] = true
			if got, want := reflect.TypeOf(v), exprValueType(f); got != want {
				t.Errorf("%s: %s (%s) surfaces %v, prototype declares %v", path, f.Name, f.Type, got, want)
			}
		}
	})
	for path, fields := range seen {
		if len(fields) != len(s.Fields) {
			t.Errorf("%s: only %d of %d fields ever present", path, len(fields), len(s.Fields))
		}
	}
	if len(seen) != 4 {
		t.Fatalf("decode paths exercised: %d, want 4", len(seen))
	}

	// A dictionary-less categorical (a synthesized schema) is a float.
	bare := &encoding.Schema{Fields: []encoding.Field{{Name: "k", Type: encoding.FieldTypeCategoricalU8}}}
	r := NewRecord(bare, map[string]float64{"k": 2})
	if got, want := reflect.TypeOf(r.AllValues()["k"]), exprValueType(&bare.Fields[0]); got != want {
		t.Fatalf("dictionary-less categorical: AllValues %v, prototype %v", got, want)
	}
}

func TestRecord_ExprValueMatchesAllValues(t *testing.T) {
	s := exprAllTypesSchema(t)
	raw := encodeExprRows(t, s, 6)
	probe := func(path string, r *Record, extra ...string) {
		t.Helper()
		names := append([]string{"nope"}, extra...)
		for i := range s.Fields {
			names = append(names, s.Fields[i].Name)
		}
		var got []any
		var gotOK []bool
		for _, n := range names { // before AllValues builds its cache
			v, ok := r.exprValue(n)
			got, gotOK = append(got, v), append(gotOK, ok)
		}
		all := r.AllValues()
		for i, n := range names {
			want, ok := all[n]
			if ok != gotOK[i] || !reflect.DeepEqual(want, got[i]) {
				t.Errorf("%s: exprValue(%q) = %#v,%v; AllValues has %#v,%v", path, n, got[i], gotOK[i], want, ok)
			}
		}
	}
	exprDecodePaths(t, s, raw, func(path string, r *Record) {
		probe(path, r)
	})

	// Overflow sides: an injected label, a boxed wide value on a slot
	// that cannot hold it typed, a null overflow name, a schema field
	// outside a projection.
	r := NewReusableRecord(s)
	rr := encx.NewRecordReader(bytes.NewReader(raw), s)
	if err := rr.ReadRecordReused(r); err != nil {
		t.Fatal(err)
	}
	r.injectValue("label", 4.5)
	r.SetWide("f_set_u8", encoding.SetMaskFromUint64(3)) // SetMask on a narrow rung: boxed
	r.SetWide("f_decimal128", 1.25)                      // wrong dynamic type: boxed
	r.Set("ov", 1)
	r.SetNull("ov")
	probe("overflow", r, "label", "ov")

	proj := BindRecords(s, func(n string) bool { return n == "f_f64" })
	pr := proj.NewRecord()
	pr.Set("f_f64", 2.5)
	pr.Set("f_categorical_u8", 1) // outside the projection: overflow, resolved
	probe("projected", pr)
}

// perRowCompile is the pre-E5-S5 evaluation, the parity oracle: compile
// against this row's AllValues, then run.
func perRowCompile(src string, env map[string]any, exts *ExtensionRegistry) (any, error) {
	opts := append([]expr.Option{expr.Env(env)}, exts.ExprOptions()...)
	prog, err := expr.Compile(src, opts...)
	if err != nil {
		return nil, err
	}
	return expr.Run(prog, env)
}

// exprParitySchema is a compact schema for the semantic tests.
func exprParitySchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32},
		{Name: "x", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "y", Type: encoding.FieldTypeU16},
		{Name: "cat", Type: encoding.FieldTypeCategoricalU8, Dictionary: exprTestDict("c", 3), Nullable: true},
		{Name: "tags", Type: encoding.FieldTypeSetU8, Dictionary: exprTestDict("t", 4), Nullable: true},
		{Name: "amt", Type: encoding.FieldTypeDecimal128, Precision: 38, Scale: 2, Nullable: true},
		{Name: "d", Type: encoding.FieldTypeDate},
	}}
}

// exprParityRecords builds n records; x, cat, tags and amt are null on
// rows where i%4 == 3 when withNulls.
func exprParityRecords(s *encoding.Schema, n int, withNulls bool) []*Record {
	out := make([]*Record, n)
	for i := range n {
		vals := map[string]float64{
			"id": float64(i), "x": float64(i%17) + 0.5, "y": float64(i % 5),
			"cat": float64(i % 3), "d": float64(18262 + i%40),
		}
		wide := map[string]any{
			"tags": uint64(i % 16),
			"amt":  encoding.NewDecimal128FromInt(int64(i*37 - 200)),
		}
		nulls := map[string]bool{}
		if withNulls && i%4 == 3 {
			for _, n := range []string{"x", "cat", "tags", "amt"} {
				nulls[n] = true
				delete(vals, n)
				delete(wide, n)
			}
		}
		out[i] = NewRecordWithWide(s, vals, nulls, wide)
	}
	return out
}

var exprParityFilters = []string{
	"x > 8",
	"x > 8 && y < 3",
	"x == 4.5 || cat == 'c1'",
	"cat != 'c2'",
	"cat in ['c0', 'c2']",
	"not (y in [1, 3])",
	"'t1' in tags",
	"has_all(tags, ['t2']) || has_any(tags, ['t0', 't3'])",
	"popcount(tags) >= 2",
	"len(tags) == 0",
	"x * 2 + y > 17",
	"(x > 5 ? 'hi' : 'lo') == 'hi'",
	"id / 7 > 3",
	"lookup('boost', cat) > 1",
	"double(y) > 4",
	"let z = x + y; z > 10",
	"cat startsWith 'c' && upper(cat) == 'C1'",
	"d >= 18280",
	"x in 1..8",
	"$env.y > 2",
}

var exprParityFormulas = []string{
	"x * 2",
	"x + y / 3",
	"cat == 'c1' ? x : y",
	"popcount(tags) + y",
	"x > 8",
	"lookup('boost', cat) * x",
	"double(y) + 0.25",
	"id / 5",
	"let z = x * x; z - y",
}

func exprParityExts() *ExtensionRegistry {
	return &ExtensionRegistry{
		ExprFunctions: []ExprFunction{{Name: "double", Fn: func(v float64) float64 { return v * 2 }}},
		LookupTables: map[string]LookupTable{
			"boost": {Rows: map[string]float64{"c0": 0.5, "c1": 1.5, "c2": 2.5}},
		},
	}
}

func TestExprProgram_MatchesPerRowCompile(t *testing.T) {
	s := exprParitySchema()
	exts := exprParityExts()
	for _, src := range exprParityFilters {
		// Fresh records per expression: a record whose AllValues map is
		// already built short-circuits the per-row subset env.
		recs := exprParityRecords(s, 200, true)
		fns, err := BuildFilters([]*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: src}}, s, exts)
		if err != nil {
			t.Fatalf("%q: build: %v", src, err)
		}
		checked := 0
		for i, r := range recs {
			// The compiled path first: the oracle builds the record's
			// AllValues cache, which the compiled path would then reuse.
			got, gerr := fns[0](r)
			want, werr := perRowCompile(src, r.AllValues(), exts)
			if i%4 == 3 && werr != nil {
				continue // a null row: the old path failed; semantics below
			}
			if werr != nil || gerr != nil {
				t.Fatalf("%q row %d: per-row err %v, compiled err %v", src, i, werr, gerr)
			}
			if got != want.(bool) {
				t.Fatalf("%q row %d: compiled %v, per-row %v", src, i, got, want)
			}
			checked++
		}
		if checked < 150 {
			t.Fatalf("%q: only %d rows compared", src, checked)
		}
	}
	for _, src := range exprParityFormulas {
		attr := &types.Attribute{Type: types.ATTR_FORMULA, Expression: src, Label: "v"}
		comp, err := newFormulaAttribute(attr, s)
		if err != nil {
			t.Fatal(err)
		}
		if err := bindAttribute(comp, exts); err != nil {
			t.Fatalf("%q: bind: %v", src, err)
		}
		fa := comp.(*formulaAttribute)
		for i, r := range exprParityRecords(s, 200, true) {
			if i%4 == 3 {
				continue
			}
			got, gerr := fa.Row(r, "") // before the oracle builds AllValues
			want, werr := perRowCompile(src, r.AllValues(), exts)
			if werr != nil || gerr != nil {
				t.Fatalf("%q row %d: per-row err %v, compiled err %v", src, i, werr, gerr)
			}
			var wf float64
			switch w := want.(type) {
			case float64:
				wf = w
			case int:
				wf = float64(w)
			case bool:
				if w {
					wf = 1
				}
			default:
				t.Fatalf("%q: oracle returned %T", src, want)
			}
			if math.Float64bits(got) != math.Float64bits(wf) {
				t.Fatalf("%q row %d: compiled %v, per-row %v", src, i, got, wf)
			}
		}
	}
}

func TestExprProgram_CompilesOncePerBuild(t *testing.T) {
	s := exprParitySchema()
	exts := exprParityExts()
	filter := []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "x > 8 && cat != 'c1'"}}
	count := func(rows int, withNulls bool) int64 {
		recs := exprParityRecords(s, rows, withNulls)
		before := ExprCompileCount()
		fns, err := BuildFilters(filter, s, exts)
		if err != nil {
			t.Fatal(err)
		}
		if got := ExprCompileCount() - before; got != 1 {
			t.Fatalf("build compiled %d times, want 1 (before any row)", got)
		}
		for _, r := range recs {
			if _, err := fns[0](r); err != nil {
				t.Fatal(err)
			}
		}
		return ExprCompileCount() - before
	}
	for _, tc := range []struct {
		nulls bool
		want  int64
	}{{false, 1}, {true, 2}} { // null rows add ONE untyped compile
		for _, rows := range []int{8, 3000} {
			if got := count(rows, tc.nulls); got != tc.want {
				t.Errorf("filter, %d rows, nulls=%v: %d compiles, want %d", rows, tc.nulls, got, tc.want)
			}
		}
	}

	// End to end through the Processor: buffered and streaming arms,
	// a formula over a schema field and one over a feature output
	// (deferred to the first row), each compiling once whatever the
	// row count.
	for _, tc := range []struct {
		name string
		req  *types.Request
	}{
		{"buffered-filter+formula", &types.Request{
			Filterers:    filter,
			Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "x * 2 + y", Label: "v"}},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "v"}},
		}},
		{"streaming-filter+formula", &types.Request{
			Filterers:    filter,
			Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "x * 2 + y", Label: "v"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "v"}},
		}},
		{"deferred-feature-name", &types.Request{
			Features:     []*types.Feature{{Type: types.FEAT_LOG, Field: "y", Label: "ly"}},
			Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "ly >= 0"}},
			Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "ly + 1", Label: "v"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "v"}},
		}},
	} {
		per := map[int]int64{}
		for _, rows := range []int{10, 2000} {
			recs := exprParityRecords(s, rows, false)
			before := ExprCompileCount()
			if _, err := NewProcessorWithExtensions(s, exts).Process(context.Background(), tc.req, NewSliceIterator(recs)); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			per[rows] = ExprCompileCount() - before
		}
		if per[10] != per[2000] || per[10] == 0 || per[10] > 2 {
			t.Errorf("%s: compiles %v by row count; want the same small count", tc.name, per)
		}
	}
}

func TestExprFilter_NullSemantics(t *testing.T) {
	s := exprParitySchema()
	// Row 0: x=2.5 cat=c1 tags={t0}; row 1: every nullable field null.
	recs := []*Record{
		NewRecordWithWide(s, map[string]float64{"id": 0, "x": 2.5, "y": 1, "cat": 1, "d": 18262},
			nil, map[string]any{"tags": uint64(1), "amt": encoding.NewDecimal128FromInt(5)}),
		NewRecordWithWide(s, map[string]float64{"id": 1, "y": 1, "d": 18262},
			map[string]bool{"x": true, "cat": true, "tags": true, "amt": true}, nil),
	}
	for _, tc := range []struct {
		src        string
		keep, null bool // verdict on the full row, on the null row
	}{
		{"x > 1", true, false},             // ordering on nil: unknown → dropped
		{"not (x > 1)", false, false},      // still unknown, still dropped
		{"x > 1 || y == 1", true, false},   // the whole predicate is unknown
		{"x == nil", false, true},          // nil tests
		{"x != nil", true, false},          //
		{"x != nil && x > 1", true, false}, // guarded
		{"(x ?? 0) > 1", true, false},      // coalesced
		{"(x ?? 5) > 1", true, true},       //
		{"x == 3", false, false},           // equality with nil is false
		{"x != 3", true, true},             // and inequality true...
		{"cat != 'c0'", true, true},        // ...for a categorical too
		{"cat == 'c1'", true, false},       //
		{"cat == nil", false, true},        //
		{"cat in ['c1']", true, false},     //
		{"'t0' in tags", true, false},      // membership in a nil set
		{"len(tags) > 0", true, false},     // len(nil) errors → unknown
		{"amt != nil", true, false},        //
		{"x + 1 > 0", true, false},         // arithmetic on nil → unknown
	} {
		fns, err := BuildFilters([]*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: tc.src}}, s, nil)
		if err != nil {
			t.Fatalf("%q: %v", tc.src, err)
		}
		for i, want := range []bool{tc.keep, tc.null} {
			got, err := fns[0](recs[i])
			if err != nil {
				t.Fatalf("%q row %d: unexpected error %v", tc.src, i, err)
			}
			if got != want {
				t.Errorf("%q row %d: got %v, want %v", tc.src, i, got, want)
			}
		}
	}

	// A genuine evaluation error on a row with every input present is
	// still an error, not a dropped row.
	fns, err := BuildFilters([]*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "x > 1 ? 'yes' : false"}}, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fns[0](recs[0]); !errors.HasCode(err, errors.PROCESSING_RUNTIME) {
		t.Fatalf("non-bool on a full row: err %v, want PROCESSING_RUNTIME", err)
	}
}

func TestExprFormula_NullSemantics(t *testing.T) {
	s := exprParitySchema()
	full := NewRecordWithWide(s, map[string]float64{"id": 0, "x": 2.5, "y": 1, "cat": 1, "d": 18262}, nil, nil)
	null := NewRecordWithWide(s, map[string]float64{"id": 1, "y": 1, "d": 18262}, map[string]bool{"x": true, "cat": true}, nil)
	row := func(src string, r *Record) (float64, error) {
		comp, err := newFormulaAttribute(&types.Attribute{Type: types.ATTR_FORMULA, Expression: src, Label: "v"}, s)
		if err != nil {
			t.Fatal(err)
		}
		if err := bindAttribute(comp, nil); err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		return comp.(*formulaAttribute).Row(r, "")
	}
	for _, tc := range []struct {
		src        string
		full, null float64
	}{
		{"(x ?? 0) * 2", 5, 0},
		{"x == nil ? -1 : x", 2.5, -1},
		{"cat == nil ? 7 : y", 1, 7},
		{"cat != 'c0'", 1, 1},
	} {
		for i, c := range []struct {
			r    *Record
			want float64
		}{{full, tc.full}, {null, tc.null}} {
			got, err := row(tc.src, c.r)
			if err != nil || got != c.want {
				t.Errorf("%q row %d: got %v, %v; want %v", tc.src, i, got, err, c.want)
			}
		}
	}
	// Unguarded arithmetic on a null has no number to give: it raises.
	_, err := row("x * 2", null)
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_RUNTIME || ce.Message != "evaluating formula expression: x * 2" {
		t.Fatalf("unguarded null: err %v, want PROCESSING_RUNTIME evaluating formula expression", err)
	}
}

func TestExprProgram_CompileErrorsAtBuild(t *testing.T) {
	s := exprParitySchema()
	wantErr := func(t *testing.T, err error, msg string) {
		t.Helper()
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_RUNTIME || ce.Message != msg {
			t.Fatalf("err %v, want PROCESSING_RUNTIME %q", err, msg)
		}
	}
	// Syntax and type errors: at build, before any row, the historical
	// message. The old per-row compile raised the same one on row one.
	for _, src := range []string{"x >", "cat > 5", "nosuch > 1 +"} {
		_, err := BuildFilters([]*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: src}}, s, nil)
		wantErr(t, err, "compiling filter expression: "+src)
		_, perRow := perRowCompile(src, exprParityRecords(s, 1, false)[0].AllValues(), nil)
		if perRow == nil || !strings.Contains(err.(*errors.CodedError).Cause.Error(), strings.SplitN(perRow.Error(), "\n", 2)[0]) {
			t.Fatalf("%q: build cause %v, per-row cause %v", src, err.(*errors.CodedError).Cause, perRow)
		}
	}
	// A name the schema cannot declare may be a feature output: the
	// build defers, and the first row raises exactly as before.
	fns, err := BuildFilters([]*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "nosuch > 1"}}, s, nil)
	if err != nil {
		t.Fatalf("deferred build: %v", err)
	}
	_, err = fns[0](exprParityRecords(s, 1, false)[0])
	wantErr(t, err, "compiling filter expression: nosuch > 1")

	// ATTR_FORMULA: the Processor binds at build — zero records still
	// surface the compile error.
	req := &types.Request{
		Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "x *", Label: "v"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "v"}},
	}
	_, err = NewProcessor(s).Process(context.Background(), req, NewSliceIterator(nil))
	wantErr(t, err, "compiling formula expression: x *")
}

func TestExprProgram_ConcurrentRuns(t *testing.T) {
	s := exprParitySchema()
	recs := exprParityRecords(s, 400, true)
	for _, r := range recs {
		r.injectValue("extra", 1) // a non-schema name: the compile defers
	}
	for _, src := range []string{"x > 8 && cat != 'c1'", "extra > 0 && x > 3"} {
		prog, err := newExprProgram(src, "filter", s, nil)
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for w := range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := w; i < len(recs); i += 8 {
					_, _, _ = prog.runRecord(recs[i])
				}
			}()
		}
		wg.Wait()
	}
}
