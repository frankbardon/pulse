package processing

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

func boolSchema() *encoding.Schema {
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "flag", Type: encoding.FieldTypePackedBool, Nullable: true},
			{Name: "score", Type: encoding.FieldTypeF64, Nullable: true},
		},
	}
}

func mixedSchema() *encoding.Schema {
	dict := encoding.NewDictionary()
	dict.Add("") // first slot intentionally empty so id=0 resolves to ""
	dict.Add("hello")
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "flag", Type: encoding.FieldTypePackedBool, Nullable: true},
			{Name: "score", Type: encoding.FieldTypeF64, Nullable: true},
			{Name: "notes", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict, Nullable: true},
		},
	}
}

func buildFilter(t *testing.T, ft types.FiltererType, field string, values []string, schema *encoding.Schema) (FilterFunc, error) {
	t.Helper()
	builder := filtererRegistry[ft]()
	return builder.Build(&types.Filterer{Type: ft, Field: field, Values: values}, schema)
}

// --- Strict mode ---

func TestFilterer_True_Strict_PackedBool(t *testing.T) {
	schema := boolSchema()
	fn, err := buildFilter(t, types.FILTER_TRUE, "flag", nil, schema)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	cases := []struct {
		name string
		val  float64
		null bool
		want bool
	}{
		{"true", 1, false, true},
		{"false", 0, false, false},
		{"null", 0, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := NewRecordWithNulls(schema, map[string]float64{"flag": c.val}, map[string]bool{"flag": c.null})
			got, err := fn(rec)
			if err != nil {
				t.Fatalf("filter: %v", err)
			}
			if got != c.want {
				t.Errorf("FILTER_TRUE strict flag=%v null=%v: got %v want %v", c.val, c.null, got, c.want)
			}
		})
	}
}

func TestFilterer_False_Strict_PackedBool(t *testing.T) {
	schema := boolSchema()
	fn, err := buildFilter(t, types.FILTER_FALSE, "flag", nil, schema)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	cases := []struct {
		name string
		val  float64
		null bool
		want bool
	}{
		{"true", 1, false, false},
		{"false", 0, false, true},
		{"null", 0, true, false}, // strict drops null in both directions
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := NewRecordWithNulls(schema, map[string]float64{"flag": c.val}, map[string]bool{"flag": c.null})
			got, err := fn(rec)
			if err != nil {
				t.Fatalf("filter: %v", err)
			}
			if got != c.want {
				t.Errorf("FILTER_FALSE strict flag=%v null=%v: got %v want %v", c.val, c.null, got, c.want)
			}
		})
	}
}

func TestFilterer_True_Strict_RejectsNonBool(t *testing.T) {
	schema := boolSchema()
	_, err := buildFilter(t, types.FILTER_TRUE, "score", nil, schema)
	if err == nil {
		t.Fatal("expected strict mode to reject non-packed_bool field")
	}
	if !strings.Contains(err.Error(), "packed_bool") {
		t.Errorf("error should mention packed_bool, got %q", err.Error())
	}
}

func TestFilterer_True_RequiresField(t *testing.T) {
	schema := boolSchema()
	_, err := buildFilter(t, types.FILTER_TRUE, "", nil, schema)
	if err == nil {
		t.Fatal("expected error when Field is empty")
	}
}

func TestFilterer_True_MissingFieldInSchema(t *testing.T) {
	schema := boolSchema()
	_, err := buildFilter(t, types.FILTER_TRUE, "ghost", nil, schema)
	if err == nil {
		t.Fatal("expected error when field not in schema")
	}
}

func TestFilterer_True_RejectsUnknownValuesToken(t *testing.T) {
	schema := boolSchema()
	_, err := buildFilter(t, types.FILTER_TRUE, "flag", []string{"loose"}, schema)
	if err == nil {
		t.Fatal("expected error for unknown values token")
	}
}

func TestFilterer_True_RejectsTooManyValues(t *testing.T) {
	schema := boolSchema()
	_, err := buildFilter(t, types.FILTER_TRUE, "flag", []string{"truthy", "extra"}, schema)
	if err == nil {
		t.Fatal("expected error for >1 values entry")
	}
}

func TestFilterer_True_StrictExplicit(t *testing.T) {
	schema := boolSchema()
	if _, err := buildFilter(t, types.FILTER_TRUE, "flag", []string{"strict"}, schema); err != nil {
		t.Fatalf("explicit strict should build: %v", err)
	}
}

// --- Truthy (opt-in JS coercion) ---

func TestFilterer_True_Truthy_Numeric(t *testing.T) {
	schema := boolSchema()
	fn, err := buildFilter(t, types.FILTER_TRUE, "score", []string{"truthy"}, schema)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	cases := []struct {
		name string
		val  float64
		null bool
		want bool // FILTER_TRUE keeps truthy
	}{
		{"positive", 3.5, false, true},
		{"negative", -1, false, true},
		{"zero", 0, false, false},
		{"null", 0, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := NewRecordWithNulls(schema, map[string]float64{"score": c.val}, map[string]bool{"score": c.null})
			got, err := fn(rec)
			if err != nil {
				t.Fatalf("filter: %v", err)
			}
			if got != c.want {
				t.Errorf("FILTER_TRUE truthy score=%v null=%v: got %v want %v", c.val, c.null, got, c.want)
			}
		})
	}
}

func TestFilterer_False_Truthy_Numeric_KeepsNullAndZero(t *testing.T) {
	schema := boolSchema()
	fn, err := buildFilter(t, types.FILTER_FALSE, "score", []string{"truthy"}, schema)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	cases := []struct {
		name string
		val  float64
		null bool
		want bool
	}{
		{"zero-kept", 0, false, true},
		{"null-kept", 0, true, true},
		{"positive-dropped", 5, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := NewRecordWithNulls(schema, map[string]float64{"score": c.val}, map[string]bool{"score": c.null})
			got, err := fn(rec)
			if err != nil {
				t.Fatalf("filter: %v", err)
			}
			if got != c.want {
				t.Errorf("FILTER_FALSE truthy score=%v null=%v: got %v want %v", c.val, c.null, got, c.want)
			}
		})
	}
}

func TestFilterer_True_Truthy_Categorical(t *testing.T) {
	schema := mixedSchema()
	fn, err := buildFilter(t, types.FILTER_TRUE, "notes", []string{"truthy"}, schema)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	// id 0 resolves to "" (falsy); id 1 resolves to "hello" (truthy).
	recEmpty := NewRecordWithNulls(schema, map[string]float64{"notes": 0}, map[string]bool{"notes": false})
	recText := NewRecordWithNulls(schema, map[string]float64{"notes": 1}, map[string]bool{"notes": false})
	recNull := NewRecordWithNulls(schema, map[string]float64{"notes": 0}, map[string]bool{"notes": true})

	for _, c := range []struct {
		name string
		rec  *Record
		want bool
	}{
		{"empty-string-dropped", recEmpty, false},
		{"non-empty-kept", recText, true},
		{"null-dropped", recNull, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := fn(c.rec)
			if err != nil {
				t.Fatalf("filter: %v", err)
			}
			if got != c.want {
				t.Errorf("FILTER_TRUE truthy notes: got %v want %v", got, c.want)
			}
		})
	}
}

func TestJSTruthy_TableMatchesJS(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want bool
	}{
		{"nil", nil, false},
		{"bool-true", true, true},
		{"bool-false", false, false},
		{"empty-string", "", false},
		{"non-empty-string", "x", true},
		{"zero-f64", 0.0, false},
		{"neg-zero-f64", negZeroF64(), false},
		{"positive-f64", 1.5, true},
		{"negative-f64", -1.5, true},
		{"nan-f64", nanF64(), false},
		{"zero-f32", float32(0), false},
		{"positive-f32", float32(2.5), true},
		{"decimal-zero", encoding.ZeroDecimal128(), false},
		{"decimal-non-zero", encoding.NewDecimal128FromInt(5), true},
		{"other-type-non-nil", struct{}{}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := jsTruthy(c.in); got != c.want {
				t.Errorf("jsTruthy(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func nanF64() float64 {
	zero := 0.0
	return zero / zero
}

func negZeroF64() float64 {
	one := 1.0
	return -1 / (one / 0)
}
