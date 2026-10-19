package mcp

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
	"github.com/frankbardon/pulse/types"
)

// everyTypeSchema carries one field of every registered field type
// (vectors included), named after its type.
func everyTypeSchema() *encoding.Schema {
	s := &encoding.Schema{}
	for b := 0; b < 256; b++ {
		ft := encoding.FieldType(b)
		if !ft.IsKnown() {
			continue
		}
		f := encoding.Field{Name: "f_" + ft.String(), Type: ft}
		if ft.IsCategorical() || ft.IsSet() {
			d := encoding.NewDictionary()
			d.Add("a")
			d.Add("b")
			f.Dictionary = d
		}
		s.Fields = append(s.Fields, f)
	}
	return s
}

// attributeSlots are the attribute item keys a field-type rule judges.
var attributeSlots = []fieldaccept.Slot{fieldaccept.SlotField, fieldaccept.SlotSubject, fieldaccept.SlotOrder}

// predictAcceptsAttributeField is the accepted-types table's verdict for
// a built-in attribute slot (what predict and the runtime refuse); a
// vector field is the field-reference walk's refusal. An untabled `field`
// keeps the historical numeric class; an untabled subject / order slot
// is not read, so any field binds.
func predictAcceptsAttributeField(op string, slot fieldaccept.Slot, ft encoding.FieldType) bool {
	if _, ok := fieldaccept.Lookup(op, slot); ok {
		return !ft.IsVector() && fieldaccept.Accepts(op, slot, ft)
	}
	if slot == fieldaccept.SlotField {
		return ft.IsNumericForAnalytics()
	}
	return true
}

// TestBind_AttributeFieldsFollowAcceptedTypes: for every built-in
// attribute type, every slot a type rule judges and every schema field,
// the bound pulse_process schema admits the field exactly when the
// accepted-types table (predict's and the runtime's rule) accepts it —
// a categorical state on ATTR_PREVIOUS_STATE / ATTR_CODE_IN, a date on
// ATTR_DATE_PART, a set on ATTR_SET_HAS, while ATTR_ZSCORE stays
// numeric-only. Checked with a real draft 2020-12 validator.
func TestBind_AttributeFieldsFollowAcceptedTypes(t *testing.T) {
	_, inst := instanceFor(t, nil, pulse.Extensions{})
	schema := everyTypeSchema()
	bound, err := BindForInstance(schema, inst)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{toolmeta.ToolProcess, toolmeta.ToolCompose, toolmeta.ToolProcessChain} {
		sch := compileBound(t, bound[tool])
		wrap := map[string]func(string) string{
			toolmeta.ToolProcess: func(b string) string { return `{"cohort": {"filename": "c.pulse"}, ` + b + `}` },
			toolmeta.ToolCompose: func(b string) string { return `{"requests": [{"cohort": {"filename": "c.pulse"}, ` + b + `}]}` },
			toolmeta.ToolProcessChain: func(b string) string {
				return `{"stages": [{"request": {"cohort": {"filename": "c.pulse"}, ` + b + `}}]}`
			},
		}[tool]
		for _, at := range types.AllAttributeTypes() {
			for _, slot := range attributeSlots {
				for _, f := range schema.Fields {
					item, _ := json.Marshal(map[string]any{"type": string(at), string(slot): f.Name})
					got := validates(t, sch, wrap(`"attributes": [`+string(item)+`]`)) == nil
					want := predictAcceptsAttributeField(string(at), slot, f.Type)
					if got != want {
						t.Errorf("%s: %s %s = %s (%s): bound admits %v, predict accepts %v", tool, at, slot, f.Name, f.Type, got, want)
					}
				}
			}
		}
	}
}

// TestBind_AttributeFieldsUnderForEach: a per-type constraint keeps the
// for_each placeholder form — an ATTR_PREVIOUS_STATE item carrying
// for_each may spell field / subject_field / order_field as placeholders,
// the same item without for_each may not, and a literal field the type
// refuses is refused either way.
func TestBind_AttributeFieldsUnderForEach(t *testing.T) {
	_, inst := instanceFor(t, nil, pulse.Extensions{})
	bound, err := BindForInstance(makeBindSchema(), inst)
	if err != nil {
		t.Fatal(err)
	}
	sch := compileBound(t, bound[toolmeta.ToolProcess])
	const fe = `"for_each": {"axes": [{"name": "ch", "values": ["a"]}]}`
	cases := []struct {
		name string
		item string
		ok   bool
	}{
		{"placeholders under for_each", `{"type": "ATTR_PREVIOUS_STATE", "field": "{{ch}}", "subject_field": "{{ch}}_id", "order_field": "{{ch}}_on", ` + fe + `}`, true},
		{"placeholders without for_each", `{"type": "ATTR_PREVIOUS_STATE", "field": "{{ch}}", "subject_field": "score", "order_field": "observed_on"}`, false},
		{"refused literal under for_each", `{"type": "ATTR_PREVIOUS_STATE", "field": "score", ` + fe + `}`, false},
		{"refused order under for_each", `{"type": "ATTR_PREVIOUS_STATE", "field": "category", "order_field": "score", ` + fe + `}`, false},
		{"accepted literals", `{"type": "ATTR_PREVIOUS_STATE", "field": "category", "subject_field": "score", "order_field": "observed_on"}`, true},
	}
	for _, c := range cases {
		err := validates(t, sch, `{"cohort": {"filename": "c.pulse"}, "attributes": [`+c.item+`]}`)
		if c.ok && err != nil {
			t.Errorf("%s: rejected, want accepted: %v", c.name, err)
		}
		if !c.ok && err == nil {
			t.Errorf("%s: accepted, want rejected", c.name)
		}
	}
}

// zeroAttribute is a row-local extension attribute writing 0.
type zeroAttribute struct{}

func (zeroAttribute) Compute(rows extend.Rows, _ string) ([]float64, error) {
	return make([]float64, rows.Len()), nil
}

func (zeroAttribute) Row(extend.Record, string) (float64, error) { return 0, nil }

// TestBind_ExtensionAttributeFieldFollowsAccepts: an extension attribute
// declaring Accepts binds `field` to exactly the schema fields of those
// types; one declaring none keeps the numeric class.
func TestBind_ExtensionAttributeFieldFollowsAccepts(t *testing.T) {
	stub := func(*types.Attribute, *encoding.Schema) (extend.AttributeComputer, error) {
		return zeroAttribute{}, nil
	}
	const cats, plain = "ATTR_TESTNS_CATS", "ATTR_TESTNS_PLAIN"
	ext := pulse.Extensions{Attributes: []pulse.AttributeRegistration{
		{Name: cats, Mode: pulse.AttributeModeRowLocal, Factory: stub,
			Accepts: []encoding.FieldType{encoding.FieldTypeCategoricalU8, encoding.FieldTypeDate}},
		{Name: plain, Mode: pulse.AttributeModeRowLocal, Factory: stub},
	}}
	_, inst := instanceFor(t, nil, ext)
	schema := everyTypeSchema()
	bound, err := BindForInstance(schema, inst)
	if err != nil {
		t.Fatal(err)
	}
	sch := compileBound(t, bound[toolmeta.ToolProcess])
	for _, f := range schema.Fields {
		for _, c := range []struct {
			op   string
			want bool
		}{
			{cats, f.Type == encoding.FieldTypeCategoricalU8 || f.Type == encoding.FieldTypeDate},
			{plain, f.Type.IsNumericForAnalytics()},
		} {
			item, _ := json.Marshal(map[string]any{"type": c.op, "field": f.Name})
			got := validates(t, sch, `{"cohort": {"filename": "c.pulse"}, "attributes": [`+string(item)+`]}`) == nil
			if got != c.want {
				t.Errorf("%s field = %s (%s): bound admits %v, want %v", c.op, f.Name, f.Type, got, c.want)
			}
		}
	}
}
