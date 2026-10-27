package exprenv

import (
	"reflect"
	"strings"
	"testing"

	"github.com/expr-lang/expr"
	exprtypes "github.com/expr-lang/expr/types"

	"github.com/frankbardon/pulse/encoding"
)

func prototypeTestSchema() *encoding.Schema {
	dict := encoding.NewDictionary()
	for _, s := range []string{"a", "b"} {
		_, _ = dict.Add(s)
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "day", Type: encoding.FieldTypeDate},
		{Name: "flag", Type: encoding.FieldTypePackedBool},
		{Name: "cat", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
		{Name: "tags", Type: encoding.FieldTypeSetU8, Dictionary: dict},
		{Name: "rawtags", Type: encoding.FieldTypeSetU8},
		{Name: "widetags", Type: encoding.FieldTypeSetU128},
		{Name: "amount", Type: encoding.FieldTypeDecimal128},
		{Name: "x", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict}, // duplicate: first wins
	}}
}

// TestPrototype_DeclaresSchemaTypes: every schema field is declared with
// the Go type the env binds it as (a decoded record's value through Bind); a duplicate keeps its
// first occurrence; a nil schema declares nothing.
func TestPrototype_DeclaresSchemaTypes(t *testing.T) {
	t.Parallel()

	decl := Prototype(prototypeTestSchema())
	want := map[string]reflect.Type{
		"x":        reflect.TypeOf(float64(0)),
		"day":      reflect.TypeOf(float64(0)),
		"flag":     reflect.TypeOf(float64(0)),
		"cat":      reflect.TypeOf(""),
		"tags":     reflect.TypeOf([]string(nil)),
		"rawtags":  reflect.TypeOf(uint64(0)),
		"widetags": reflect.TypeOf(encoding.SetMask{}),
		"amount":   reflect.TypeOf(Decimal{}),
	}
	if len(decl) != len(want) {
		t.Fatalf("Prototype declares %d names, want %d: %v", len(decl), len(want), decl)
	}
	for name, typ := range want {
		if got := decl[name]; got == nil || !got.Equal(exprtypes.TypeOf(reflect.Zero(typ).Interface())) {
			t.Errorf("%s declared %v, want %v", name, got, typ)
		}
	}
	if got := Prototype(nil); len(got) != 0 {
		t.Errorf("Prototype(nil) = %v, want empty", got)
	}
}

// TestPrototype_CompilesWithoutEngine: the schema prototype, the shared
// option hook and the embedder functions compile an expression with no
// record and no engine — and a type error or an unknown name fails at
// compile, as it does at the runtime's build.
func TestPrototype_CompilesWithoutEngine(t *testing.T) {
	t.Parallel()

	opts := append([]expr.Option{expr.Env(Prototype(prototypeTestSchema()))}, Options()...)
	opts = append(opts, ExtensionFunctions([]NamedFunc{
		{Name: "triple", Fn: func(v float64) float64 { return v * 3 }},
		{Name: "skipped", Fn: nil},
	})...)
	for _, src := range []string{
		`x % 2 + vsum([x, day]) + triple(flag)`,
		`has_any(tags, "a") && cat == "b"`,
		`jaccard(tags, ["a"]) > 0.5`,
		`popcount(tags) + len(cat)`,
	} {
		if _, err := expr.Compile(src, opts...); err != nil {
			t.Errorf("compile %q: %v", src, err)
		}
	}
	for _, tc := range []struct{ src, wantSub string }{
		{`cat - 1`, "invalid operation"},
		{`nosuch + 1`, "unknown name nosuch"},
		{`skipped(x)`, "unknown name skipped"},
		{`amount % 2`, "invalid operation"},
	} {
		_, err := expr.Compile(tc.src, opts...)
		if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
			t.Errorf("compile %q: %v, want an error containing %q", tc.src, err, tc.wantSub)
		}
	}
}
