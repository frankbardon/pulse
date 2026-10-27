package exprenv

import (
	"reflect"
	"testing"

	"github.com/expr-lang/expr"
	exprtypes "github.com/expr-lang/expr/types"

	"github.com/frankbardon/pulse/encoding"
)

// derivedTestView is prototypeTestSchema plus two virtual columns a
// dictionary-inheritance view declares (prevstate.KeySchema's shape): a
// categorical one on cat's dictionary and a packed_bool one.
func derivedTestView(base *encoding.Schema) *encoding.Schema {
	view := *base
	view.Fields = append(append([]encoding.Field(nil), base.Fields...),
		encoding.Field{Name: "prev", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Dictionary: base.Field("cat").Dictionary},
		encoding.Field{Name: "prev_flag", Type: encoding.FieldTypePackedBool, Nullable: true},
	)
	return &view
}

// TestDerivedFields_OnlyTheViewsAdditions: DerivedFields keys the
// fields the view declares beyond the schema; nothing (nil) for a nil
// view or the schema itself — a request without a derived dictionary
// column binds exactly as before.
func TestDerivedFields_OnlyTheViewsAdditions(t *testing.T) {
	t.Parallel()

	base := prototypeTestSchema()
	view := derivedTestView(base)
	got := DerivedFields(base, view)
	if len(got) != 2 || got["prev"] != view.Field("prev") || got["prev_flag"] != view.Field("prev_flag") {
		t.Fatalf("DerivedFields = %v, want prev + prev_flag", got)
	}
	for name, v := range map[string]*encoding.Schema{"nil view": nil, "same schema": base} {
		if got := DerivedFields(base, v); got != nil {
			t.Errorf("%s: %v, want nil", name, got)
		}
	}
}

// TestDerivedDecl_TypedAsTheSourceField: a derived column the view
// declares is typed as its field binds (a dictionary categorical →
// string, a packed_bool → float64); any other derived name stays
// DerivedType.
func TestDerivedDecl_TypedAsTheSourceField(t *testing.T) {
	t.Parallel()

	view := derivedTestView(prototypeTestSchema())
	for name, want := range map[string]exprtypes.Type{
		"prev":      exprtypes.TypeOf(""),
		"prev_flag": DerivedType,
		"feat_out":  DerivedType,
	} {
		if got := DerivedDecl(view, name); !got.Equal(want) || got.String() != want.String() {
			t.Errorf("DerivedDecl(%s) = %v, want %v", name, got, want)
		}
	}
	if got := DerivedDecl(nil, "prev"); got.String() != DerivedType.String() {
		t.Errorf("nil view: %v", got)
	}
}

// TestBind_LiftsADictionaryCode: a derived categorical's value rides
// the record as its dictionary code; Bind lifts it to the label, as
// AllValues resolves a schema categorical, so `prev == "b"` reads like
// `cat == "b"`. A label already resolved, and a packed_bool flag, pass
// through.
func TestBind_LiftsADictionaryCode(t *testing.T) {
	t.Parallel()

	view := derivedTestView(prototypeTestSchema())
	prev := view.Field("prev")
	if got := Bind(prev, float64(1)); got != "b" {
		t.Fatalf("Bind(prev, 1) = %#v, want \"b\"", got)
	}
	if got := Bind(prev, "a"); got != "a" {
		t.Errorf("Bind(prev, label) = %#v", got)
	}
	if got := Bind(view.Field("prev_flag"), float64(1)); got != float64(1) {
		t.Errorf("Bind(packed_bool) = %#v", got)
	}
	decl := Prototype(view)
	prog, err := expr.Compile(`prev == "b"`, expr.Env(decl))
	if err != nil {
		t.Fatal(err)
	}
	out, err := expr.Run(prog, map[string]any{"prev": Bind(prev, float64(1))})
	if err != nil || !reflect.DeepEqual(out, true) {
		t.Errorf("prev == \"b\" on code 1: %v, %v", out, err)
	}
}
