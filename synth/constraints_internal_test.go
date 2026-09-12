package synth

import (
	"bytes"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// representativeFieldSpecs gives one minimal, valid FieldSpec per type
// name fieldTypeFromName accepts. The sentinel table below is driven off
// this map rather than off a hardcoded "expected Go type" column: the
// expectation is whatever the field's OWN sampler puts in the row.
func representativeFieldSpecs() map[string]FieldSpec {
	num := func(t string) FieldSpec {
		return FieldSpec{Name: "x", Type: t, Distribution: DistUniform,
			Params: map[string]any{"min": 0.0, "max": 10.0}}
	}
	cat := func(t string) FieldSpec {
		return FieldSpec{Name: "x", Type: t, Distribution: DistWeightedCategorical,
			Params: map[string]any{"values": []any{"a", "b"}}}
	}
	set := func(t string) FieldSpec {
		return FieldSpec{Name: "x", Type: t, Distribution: DistSetBernoulli,
			Params: map[string]any{
				"options":     []any{"a", "b"},
				"frequencies": []any{0.5, 0.5},
			}}
	}
	return map[string]FieldSpec{
		"u4":  num("u4"),
		"u8":  num("u8"),
		"u16": num("u16"),
		"u32": num("u32"),
		"u64": num("u64"),
		"f32": {Name: "x", Type: "f32", Distribution: DistNormal,
			Params: map[string]any{"mean": 0.0, "std": 1.0}},
		"f64": {Name: "x", Type: "f64", Distribution: DistNormal,
			Params: map[string]any{"mean": 0.0, "std": 1.0}},
		"date": {Name: "x", Type: "date", Distribution: DistUniformDate,
			Params: map[string]any{"start": "2024-01-01", "end": "2024-12-31"}},
		"packed_bool": {Name: "x", Type: "packed_bool", Distribution: DistBernoulli,
			Params: map[string]any{"p": 0.5}},
		"categorical_u8":  cat("categorical_u8"),
		"categorical_u16": cat("categorical_u16"),
		"categorical_u32": cat("categorical_u32"),
		"decimal128": {Name: "x", Type: "decimal128", Distribution: DistNormal,
			Scale: 2, Params: map[string]any{"mean": 10.0, "std": 1.0}},
		"set_u8":  set("set_u8"),
		"set_u16": set("set_u16"),
		"set_u32": set("set_u32"),
		"set_u64": set("set_u64"),
	}
}

// undeclarableFieldTypes names the encoding field types a Spec cannot
// declare today, with the reason. datetime has no fieldTypeFromName case
// (see skills/synthetic-data.md) so no spec reaches the writer with it.
var undeclarableFieldTypes = map[string]string{
	"datetime": "fieldTypeFromName has no case for it",
}

// declarableTypeNames walks the encoding.FieldType enum by value so a
// type added to encoding shows up here without anyone editing a list.
func declarableTypeNames(t *testing.T) []string {
	t.Helper()
	var out []string
	for i := 0; i < 64; i++ {
		name := encoding.FieldType(i).String()
		if strings.HasPrefix(name, "unknown(") {
			continue
		}
		if _, ok := fieldTypeFromName(name); !ok {
			if _, known := undeclarableFieldTypes[name]; !known {
				t.Fatalf("field type %q is neither declarable nor listed in "+
					"undeclarableFieldTypes; if it just became declarable add a "+
					"representative FieldSpec", name)
			}
			continue
		}
		out = append(out, name)
	}
	return out
}

// TestSentinelFor_MatchesDrawnRowValueForEveryDeclarableType is the gate
// on issue #258 generalized: for every field type a Spec can declare,
// the expr environment's Go type must equal the Go type the field's own
// sampler writes into the row. The expectation is DRAWN, never asserted
// from a second hand-written table, so a new field type cannot acquire a
// wrong env type silently.
func TestSentinelFor_MatchesDrawnRowValueForEveryDeclarableType(t *testing.T) {
	specs := representativeFieldSpecs()
	names := declarableTypeNames(t)
	if len(names) < 17 {
		t.Fatalf("expected at least 17 declarable field types, got %d: %v", len(names), names)
	}
	for _, name := range names {
		fs, ok := specs[name]
		if !ok {
			t.Fatalf("declarable field type %q has no representative FieldSpec; add one", name)
		}
		t.Run(name, func(t *testing.T) {
			_, wfs, err := buildSchema(&Spec{RowCount: 1, Fields: []FieldSpec{fs}})
			if err != nil {
				t.Fatalf("buildSchema(%s): %v", name, err)
			}
			drawn, _ := wfs[0].sampler.next(rand.New(rand.NewPCG(1, 2)))
			got := reflect.TypeOf(sentinelFor(name))
			want := reflect.TypeOf(drawn)
			if got != want {
				t.Fatalf("sentinelFor(%q) = %v, but the sampler draws %v — "+
					"a constraint touching this field fails at run time with "+
					"\"invalid operation\"", name, got, want)
			}
		})
	}
}

// TestSentinelFor_DeadNullableArmsAreGone pins that the removed
// nullable_* type names are unreachable: fieldTypeFromName cannot build
// them, so no spec naming one gets past buildSchema, and sentinelFor
// must not be carrying a special case for them. nullable_bool is the one
// that mattered — it returned a Go bool.
func TestSentinelFor_DeadNullableArmsAreGone(t *testing.T) {
	for _, name := range []string{
		"nullable_bool", "nullable_u4", "nullable_u8", "nullable_u16", "nullable_decimal128",
	} {
		if _, ok := fieldTypeFromName(name); ok {
			t.Fatalf("%q became declarable; sentinelFor needs a real mapping", name)
		}
		if got := sentinelFor(name); reflect.TypeOf(got) != reflect.TypeOf(float64(0)) {
			t.Fatalf("sentinelFor(%q) = %T, want float64 fallback", name, got)
		}
	}
}

// TestConstraints_PackedBoolFiltersRows is the direct #258 regression:
// before the fix both cases failed with
// PROCESSING_RUNTIME "invalid operation: bool(float64)".
func TestConstraints_PackedBoolFiltersRows(t *testing.T) {
	cases := []struct {
		name string
		expr string
		want float64
	}{
		{"true", "flag == 1", 1},
		{"false", "flag == 0", 0},
		{"boolean-and", "flag == 1 && n >= 0", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := &Spec{
				RowCount: 120,
				Fields: []FieldSpec{
					{Name: "flag", Type: "packed_bool", Distribution: DistBernoulli,
						Params: map[string]any{"p": 0.5}},
					{Name: "n", Type: "u8", Distribution: DistUniform,
						Params: map[string]any{"min": 0.0, "max": 10.0}},
				},
				Constraints:      []ConstraintSpec{{Expr: tc.expr}},
				MaxRejectionRate: 0.9,
			}
			data, res, err := SynthBytes(spec, Options{Seed: 7})
			if err != nil {
				t.Fatalf("SynthBytes: %v", err)
			}
			if res.RowsGenerated != 120 {
				t.Fatalf("rows generated = %d, want 120", res.RowsGenerated)
			}
			vals, _ := decodeConstraintTestField(t, data, "flag")
			if len(vals) != 120 {
				t.Fatalf("decoded %d rows, want 120", len(vals))
			}
			for i, v := range vals {
				if v != tc.want {
					t.Fatalf("row %d flag = %v, want %v (constraint %q not enforced)",
						i, v, tc.want, tc.expr)
				}
			}
		})
	}
}

// TestConstraints_BarePackedBoolRefusedAtCompileTime documents the one
// visible consequence of typing packed_bool as the float64 the row
// carries: a bare `flag` is no longer a boolean expression, so the
// authoring form is `flag == 1`. That is strictly better than what it
// replaced — before the fix `flag` compiled and then failed on EVERY row
// with PROCESSING_RUNTIME "invalid operation: bool(float64)".
func TestConstraints_BarePackedBoolRefusedAtCompileTime(t *testing.T) {
	spec := &Spec{
		RowCount: 10,
		Fields: []FieldSpec{
			{Name: "flag", Type: "packed_bool", Distribution: DistBernoulli,
				Params: map[string]any{"p": 0.5}},
		},
		Constraints:      []ConstraintSpec{{Expr: "flag"}},
		MaxRejectionRate: 0.9,
	}
	_, _, err := SynthBytes(spec, Options{Seed: 1})
	if err == nil {
		t.Fatal("expected a compile-time refusal for a bare packed_bool constraint")
	}
	if !strings.Contains(err.Error(), "compiling constraint") {
		t.Fatalf("expected a compile-time refusal, got %v", err)
	}
	if strings.Contains(err.Error(), "PROCESSING_RUNTIME") {
		t.Fatalf("regressed to the #258 per-row runtime failure: %v", err)
	}
}

// TestConstraints_IsnullSeesTheRowsActualNullState asserts both
// directions in one test because each is trivially satisfiable by
// abandoning the other: an isnull that always returned false would make
// the "only nulls" case infeasible, and one that always returned true
// would make the "no nulls" case infeasible.
func TestConstraints_IsnullSeesTheRowsActualNullState(t *testing.T) {
	cases := []struct {
		name     string
		expr     string
		nullRate float64
		wantNull bool
	}{
		{"bare-identifier-not-null", "!isnull(x)", 0.3, false},
		{"bare-identifier-null", "isnull(x)", 0.7, true},
		{"string-literal-not-null", `!isnull("x")`, 0.3, false},
		{"string-literal-null", `isnull("x")`, 0.7, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := &Spec{
				RowCount: 100,
				Fields: []FieldSpec{
					{Name: "x", Type: "f64", Distribution: DistNormal, Nullable: true,
						NullRate: tc.nullRate, Params: map[string]any{"mean": 5.0, "std": 1.0}},
				},
				Constraints:      []ConstraintSpec{{Expr: tc.expr}},
				MaxRejectionRate: 0.95,
			}
			data, _, err := SynthBytes(spec, Options{Seed: 11})
			if err != nil {
				t.Fatalf("SynthBytes: %v", err)
			}
			_, nulls := decodeConstraintTestField(t, data, "x")
			if len(nulls) != 100 {
				t.Fatalf("decoded %d rows, want 100", len(nulls))
			}
			for i, isNull := range nulls {
				if isNull != tc.wantNull {
					t.Fatalf("row %d null = %v, want %v (constraint %q not enforced)",
						i, isNull, tc.wantNull, tc.expr)
				}
			}
		})
	}
}

// TestConstraints_IsnullOnPackedBoolCompiles pairs the two halves of this
// story: the null predicate must be reachable on the field type whose env
// mapping was broken, since that is what the rule layer's `when` clauses
// will test.
func TestConstraints_IsnullOnPackedBoolCompiles(t *testing.T) {
	spec := &Spec{
		RowCount: 60,
		Fields: []FieldSpec{
			{Name: "flag", Type: "packed_bool", Distribution: DistBernoulli,
				Nullable: true, NullRate: 0.4, Params: map[string]any{"p": 0.5}},
		},
		Constraints:      []ConstraintSpec{{Expr: "isnull(flag) || flag == 1"}},
		MaxRejectionRate: 0.95,
	}
	data, _, err := SynthBytes(spec, Options{Seed: 3})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	vals, nulls := decodeConstraintTestField(t, data, "flag")
	sawNull, sawSet := false, false
	for i := range vals {
		if nulls[i] {
			sawNull = true
			continue
		}
		if vals[i] != 1 {
			t.Fatalf("row %d flag = %v, want 1 for a non-null row", i, vals[i])
		}
		sawSet = true
	}
	if !sawNull || !sawSet {
		t.Fatalf("expected both null and set rows; sawNull=%v sawSet=%v", sawNull, sawSet)
	}
}

// TestConstraints_IsnullRejectsUnknownField pins that a typo'd field name
// fails loudly instead of answering false forever.
func TestConstraints_IsnullRejectsUnknownField(t *testing.T) {
	t.Run("string-literal", func(t *testing.T) {
		spec := &Spec{
			RowCount: 5,
			Fields: []FieldSpec{
				{Name: "x", Type: "f64", Distribution: DistNormal,
					Params: map[string]any{"mean": 0.0, "std": 1.0}},
			},
			Constraints: []ConstraintSpec{{Expr: `isnull("nope")`}},
		}
		if _, _, err := SynthBytes(spec, Options{Seed: 1}); err == nil ||
			!strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("expected an unknown-field failure, got %v", err)
		}
	})
	t.Run("bare-identifier", func(t *testing.T) {
		spec := &Spec{
			RowCount: 5,
			Fields: []FieldSpec{
				{Name: "x", Type: "f64", Distribution: DistNormal,
					Params: map[string]any{"mean": 0.0, "std": 1.0}},
			},
			// nope is not a declared field, so the patcher leaves it alone
			// and expr's own unknown-name check reports it at compile time.
			Constraints: []ConstraintSpec{{Expr: "isnull(nope)"}},
		}
		if _, _, err := SynthBytes(spec, Options{Seed: 1}); err == nil {
			t.Fatal("expected a compile failure for an undeclared identifier")
		}
	})
}

// TestConstraints_NonBooleanFieldsUnchanged keeps the pre-existing
// categorical / numeric env shapes honest.
func TestConstraints_NonBooleanFieldsUnchanged(t *testing.T) {
	spec := &Spec{
		RowCount: 80,
		Fields: []FieldSpec{
			{Name: "region", Type: "categorical_u8", Distribution: DistWeightedCategorical,
				Params: map[string]any{"values": []any{"east", "west"}}},
			{Name: "amount", Type: "f64", Distribution: DistUniform,
				Params: map[string]any{"min": 0.0, "max": 100.0}},
		},
		Constraints: []ConstraintSpec{
			{Expr: `region == "east"`},
			{Expr: "amount >= 0"},
		},
		MaxRejectionRate: 0.9,
	}
	data, _, err := SynthBytes(spec, Options{Seed: 9})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	r := bytes.NewReader(data)
	if err := encoding.ReadHeader(r); err != nil {
		t.Fatalf("read header: %v", err)
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	f := schema.Field("region")
	if f == nil || f.Dictionary == nil {
		t.Fatal("region is not categorical")
	}
	rr := encoding.NewRecordReader(r, schema)
	values := make(map[string]float64)
	nulls := make(map[string]bool)
	rows := 0
	for {
		if err := rr.ReadRecord(values, nulls); err != nil {
			break
		}
		rows++
		if got := f.Dictionary.Resolve(uint32(values["region"])); got != "east" {
			t.Fatalf("row %d region = %q, want east", rows, got)
		}
		if values["amount"] < 0 {
			t.Fatalf("row %d amount = %v, want >= 0", rows, values["amount"])
		}
	}
	if rows != 80 {
		t.Fatalf("decoded %d rows, want 80", rows)
	}
}

// decodeConstraintTestField returns one field's values and null flags per
// record. The cohort-readback helpers in synth/synth_test.go are
// package synth_test and therefore invisible here.
func decodeConstraintTestField(t *testing.T, data []byte, name string) ([]float64, []bool) {
	t.Helper()
	r := bytes.NewReader(data)
	if err := encoding.ReadHeader(r); err != nil {
		t.Fatalf("read header: %v", err)
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	rr := encoding.NewRecordReader(r, schema)
	var vals []float64
	var isNull []bool
	values := make(map[string]float64)
	nulls := make(map[string]bool)
	for {
		if err := rr.ReadRecord(values, nulls); err != nil {
			break
		}
		vals = append(vals, values[name])
		isNull = append(isNull, nulls[name])
	}
	return vals, isNull
}
