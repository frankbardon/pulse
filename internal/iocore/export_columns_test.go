package iocore

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

func columnFieldNames(fields []*encoding.Field) []string {
	out := make([]string, len(fields))
	for i, f := range fields {
		if f == nil {
			out[i] = "<text>"
			continue
		}
		out[i] = f.Name + ":" + f.Type.String()
	}
	return out
}

// ColumnFields never types a column by its position in the schema: a
// projected or augmented header is typed by NAME without a plan, and by
// the plan itself when one matches the header.
func TestColumnFields(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "qty", Type: encoding.FieldTypeU32},
		{Name: "amount", Type: encoding.FieldTypeDecimal128, Scale: 2},
		{Name: "color", Type: encoding.FieldTypeCategoricalU8},
	}}
	elem := encoding.Field{Name: "v_1", Type: encoding.FieldTypeF32, Nullable: true}

	cases := []struct {
		name   string
		cols   []ExportColumn
		header []string
		want   []string
	}{
		{
			name:   "no plan, projected and augmented header typed by name",
			header: []string{"amount", "color", "color_label"},
			want:   []string{"amount:decimal128", "color:categorical_u8", "<text>"},
		},
		{
			name: "plan decides, including a text-typed source field",
			cols: []ExportColumn{
				{Name: "amount", Field: &schema.Fields[1]},
				{Name: "qty"},
				{Name: "v_1", Kind: ExportColumnElement, Field: &elem},
			},
			header: []string{"amount", "qty", "v_1"},
			want:   []string{"amount:decimal128", "<text>", "v_1:f32"},
		},
		{
			name:   "plan not matching the header falls back to names",
			cols:   []ExportColumn{{Name: "qty"}},
			header: []string{"color", "qty"},
			want:   []string{"color:categorical_u8", "qty:u32"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := columnFieldNames(ColumnFields(tc.cols, schema, tc.header))
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("column %d: got %s, want %s", i, got[i], tc.want[i])
				}
			}
		})
	}
	if got := ColumnFields(nil, nil, []string{"a"}); got[0] != nil {
		t.Errorf("no plan and no schema must be text, got %v", got[0])
	}
}
