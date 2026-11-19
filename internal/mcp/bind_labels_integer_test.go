package mcp

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
)

// TestMCPSchemaBinding_LabelsIntegerCodedAugmentOnly: the bound labels
// slot names the cohort's categorical fields, then its integer-coded
// u4 / u8 / u16 / u32 ones (never a float or u64), and requires mode
// "augment" on the integer-coded ones — the response rule
// (PULSE_LABEL_REPLACE_NUMERIC). A cohort with no integer-coded field
// keeps the categorical-only fragment, no if / then.
func TestMCPSchemaBinding_LabelsIntegerCodedAugmentOnly(t *testing.T) {
	t.Parallel()

	dict := encoding.NewDictionary()
	dict.Add("US")
	snap := &descx.ExtensionsSnapshot{
		LabelTables: []descriptor.LabelTableMeta{{Name: "codes", HasRowsData: true}},
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "nibble", Type: encoding.FieldTypeU4},
		{Name: "country", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
		{Name: "code", Type: encoding.FieldTypeU8},
		{Name: "amount", Type: encoding.FieldTypeF64},
		{Name: "big", Type: encoding.FieldTypeU64},
		{Name: "wide", Type: encoding.FieldTypeU32},
	}}
	bound, err := BindWithExtensions(schema, snap)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{toolmeta.ToolProcess, toolmeta.ToolFacetSchema} {
		req := decodeBoundRequest(t, bound[tool])
		if got, want := enumOf(req, "labels", "field"), []string{"country", "nibble", "code", "wide"}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s labels field enum = %v, want %v", tool, got, want)
		}
		items := schemaAt(req, "properties.labels.items")
		ifEnum, _ := schemaAt(items, "if.properties.field")["enum"].([]any)
		if len(ifEnum) != 3 || ifEnum[0] != "nibble" || ifEnum[2] != "wide" {
			t.Errorf("%s labels if.field enum = %v, want the integer-coded fields", tool, ifEnum)
		}
		if c := schemaAt(items, "then.properties.mode")["const"]; c != "augment" {
			t.Errorf("%s labels then.mode const = %v, want augment", tool, c)
		}
	}

	only := &encoding.Schema{Fields: []encoding.Field{
		{Name: "country", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
		{Name: "amount", Type: encoding.FieldTypeF64},
	}}
	bound, err = BindWithExtensions(only, snap)
	if err != nil {
		t.Fatal(err)
	}
	items := schemaAt(decodeBoundRequest(t, bound[toolmeta.ToolProcess]), "properties.labels.items")
	if items == nil || items["if"] != nil || items["then"] != nil {
		t.Errorf("a categorical-only cohort's labels items = %v, want no if / then", items)
	}
}
