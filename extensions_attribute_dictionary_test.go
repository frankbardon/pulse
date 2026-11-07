package pulse

import (
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/prevstate"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// constCode emits v for every row (a row_local attribute).
type constCode struct{ v float64 }

func (c constCode) Row(extend.Record, string) (float64, error) { return c.v, nil }
func (c constCode) Compute(rows extend.Rows, _ string) ([]float64, error) {
	out := make([]float64, rows.Len())
	for i := range out {
		out[i] = c.v
	}
	return out, nil
}

// TestAdaptAttributeFactory_InheritedDictionary (U67 E3-S2): the
// adapter re-runs the field-reference walk's source rule before the
// factory (byte-identical refusal), and checks every value the built
// attribute emits — Row and Compute alike — against the source's
// dictionary: a code passes, a non-code is
// PULSE_EXTENSION_ATTRIBUTE_OUTPUT_MISMATCH, a NaN from a non-nullable
// value too.
func TestAdaptAttributeFactory_InheritedDictionary(t *testing.T) {
	t.Parallel()

	dict := encoding.NewDictionary()
	for _, l := range []string{"a", "b"} {
		if _, err := dict.Add(l); err != nil {
			t.Fatal(err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "c", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
		{Name: "x", Type: encoding.FieldTypeF64},
	}}
	const name types.AttributeType = "ATTR_T_CONST"
	build := func(v float64) processing.AttributeFactory {
		return adaptAttributeFactory(AttributeRegistration{Name: name, Mode: AttributeModeRowLocal,
			InheritsDictionary: &AttributeDictionarySource{},
			Factory: func(*types.Attribute, *encoding.Schema) (extend.AttributeComputer, error) {
				return constCode{v: v}, nil
			}})
	}

	bad := &types.Attribute{Type: name, Field: "x", Label: "k"}
	_, err := build(0)(bad, schema)
	_, want := prevstate.InheritedSource(bad, schema, prevstate.Inherits{name: ""})
	ce, ok := err.(*errors.CodedError)
	if !ok || want == nil || ce.Code != want.Code || ce.Message != want.Message || !reflect.DeepEqual(ce.Details, want.Details) {
		t.Fatalf("adapter refusal %v, want the walk's %v", err, want)
	}

	rec := processing.NewRecord(schema, map[string]float64{"c": 1})
	ok1 := &types.Attribute{Type: name, Field: "c", Label: "k"}
	for _, c := range []struct {
		v     float64
		fault bool
	}{{1, false}, {2, true}, {math.NaN(), true}} {
		comp, err := build(c.v)(ok1, schema)
		if err != nil {
			t.Fatal(err)
		}
		_, rowErr := comp.(processing.RowLocalAttribute).Row(rec, "c")
		_, allErr := comp.Compute([]*processing.Record{rec}, "c")
		for arm, e := range map[string]error{"Row": rowErr, "Compute": allErr} {
			ce, isCoded := e.(*errors.CodedError)
			if got := isCoded && ce.Code == errors.PULSE_EXTENSION_ATTRIBUTE_OUTPUT_MISMATCH; got != c.fault || (e != nil && !got) {
				t.Errorf("%s(%v): err %v, want fault %v", arm, c.v, e, c.fault)
			}
		}
	}
}
