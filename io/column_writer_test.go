package io_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// An embedder's own typed writer gets the export's explicit column plan
// the built-in Excel / Arrow / Parquet writers use: implementing the
// public pio.ColumnAwareWriter is all it takes to type every header
// column from the field behind it, whatever projection, label augment
// and vector expansion did to the header.

// planNDJSON: an integer id, a float score, a categorical color (the
// augment target) and a 3-element vector v.
const planNDJSON = `{"id":1,"score":0.5,"color":"red","v":[1,2,3]}
{"id":2,"score":2.25,"color":"blue","v":[4,5,6]}
`

// planWriter records every call an export makes on it, in order, plus
// what each call carried.
type planWriter struct {
	calls  []string
	plan   []pio.ExportColumn
	schema *encoding.Schema
	lists  []pio.ListColumn
	header []string
}

func (w *planWriter) SetPulseSchema(s *encoding.Schema) {
	w.calls = append(w.calls, "SetPulseSchema")
	w.schema = s
}
func (w *planWriter) SetExportColumns(cols []pio.ExportColumn) {
	w.calls = append(w.calls, "SetExportColumns")
	w.plan = cols
}
func (w *planWriter) WriteHeader(cols []string) error {
	w.calls = append(w.calls, "WriteHeader")
	w.header = cols
	return nil
}
func (w *planWriter) WriteRow([]any) error { return nil }
func (w *planWriter) Close() error         { return nil }

var _ pio.ColumnAwareWriter = (*planWriter)(nil)
var _ pio.SchemaAwareWriter = (*planWriter)(nil)

// planListWriter is planWriter that also takes vectors as native lists.
type planListWriter struct{ planWriter }

func (w *planListWriter) SetListColumns(cols []pio.ListColumn) {
	w.calls = append(w.calls, "SetListColumns")
	w.lists = cols
}

var _ pio.ListAwareWriter = (*planListWriter)(nil)

// schemaOnlyWriter hides the plan: SchemaAwareWriter only.
type schemaOnlyWriter struct{ inner *planWriter }

func (s schemaOnlyWriter) SetPulseSchema(sc *encoding.Schema) { s.inner.SetPulseSchema(sc) }
func (s schemaOnlyWriter) WriteHeader(cols []string) error    { return s.inner.WriteHeader(cols) }
func (s schemaOnlyWriter) WriteRow(v []any) error             { return s.inner.WriteRow(v) }
func (s schemaOnlyWriter) Close() error                       { return s.inner.Close() }

// augmentColor is an external LabelResolver binding an AUGMENT on color.
type augmentColor struct{}

func (augmentColor) Has(field string) bool { return field == "color" }
func (augmentColor) Mode(field string) types.LabelMode {
	if field == "color" {
		return types.LabelModeAugment
	}
	return ""
}
func (augmentColor) Apply(field, raw string) (string, string, bool) {
	if field != "color" {
		return raw, "", false
	}
	return raw, map[string]string{"red": "Rouge", "blue": "Bleu"}[raw], true
}
func (augmentColor) FieldsWithAugment() []string  { return []string{"color"} }
func (augmentColor) Warnings() []pio.LabelWarning { return nil }

var _ pio.LabelResolver = augmentColor{}

// planCohort imports planNDJSON with v as a native vector and exports it
// to w projected to v, color and id (score dropped) with color's label
// augment.
func planExport(t *testing.T, w pio.Writer) {
	t.Helper()
	nd, err := pio.NewReaderFromBytes(pio.FormatNDJSON, []byte(planNDJSON), pio.ReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	fs := afero.NewMemMapFs()
	if _, err := importVectorTo(t, fs, nd); err != nil {
		t.Fatal(err)
	}
	job := pio.NewExportJob("out.pulse", w)
	job.FS = fs
	job.Includes = []string{"v", "color", "id"}
	job.LabelResolver = augmentColor{}
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("export: %v", err)
	}
}

// wantColumn is one plan entry's expected shape; typ is the typing
// field's type, or -1 for a text column (Field nil).
type wantColumn struct {
	kind    pio.ExportColumnKind
	source  string
	element int
	typ     int
}

const textColumn = -1

func checkPlan(t *testing.T, w *planWriter, want map[string]wantColumn, wantHeader []string) {
	t.Helper()
	if !reflect.DeepEqual(w.header, wantHeader) {
		t.Fatalf("header %v, want %v", w.header, wantHeader)
	}
	if len(w.plan) != len(w.header) {
		t.Fatalf("plan has %d columns, header %d", len(w.plan), len(w.header))
	}
	fields := pio.ColumnFields(w.plan, w.schema, w.header)
	for i, col := range w.plan {
		if col.Name != w.header[i] {
			t.Fatalf("plan[%d].Name = %q, header[%d] = %q", i, col.Name, i, w.header[i])
		}
		exp, ok := want[col.Name]
		if !ok {
			t.Fatalf("unexpected column %q", col.Name)
		}
		if col.Kind != exp.kind || col.Source != exp.source || col.Element != exp.element {
			t.Fatalf("column %q = kind %d source %q element %d, want kind %d source %q element %d",
				col.Name, col.Kind, col.Source, col.Element, exp.kind, exp.source, exp.element)
		}
		if exp.typ == textColumn {
			if col.Field != nil || fields[i] != nil {
				t.Fatalf("column %q is typed %v, want text", col.Name, col.Field.Type)
			}
			continue
		}
		if col.Field == nil || int(col.Field.Type) != exp.typ {
			t.Fatalf("column %q typed by %+v, want type %s", col.Name, col.Field, encoding.FieldType(exp.typ))
		}
		if fields[i] != col.Field {
			t.Fatalf("ColumnFields[%d] = %+v, want the plan's own field", i, fields[i])
		}
	}
}

// TestColumnAwareWriter_ExternalWriterGetsPlan: a projected,
// `_label`-augmented, vector-expanded export hands an external writer a
// plan whose Field types each column by name — never by the schema's
// field at the same position — before WriteHeader.
func TestColumnAwareWriter_ExternalWriterGetsPlan(t *testing.T) {
	t.Parallel()

	w := &planWriter{}
	planExport(t, w)

	if want := []string{"SetPulseSchema", "SetExportColumns", "WriteHeader"}; !reflect.DeepEqual(w.calls, want) {
		t.Fatalf("calls %v, want %v", w.calls, want)
	}
	f64 := int(encoding.FieldTypeF64)
	checkPlan(t, w, map[string]wantColumn{
		"id":          {pio.ExportColumnField, "id", -1, int(encoding.FieldTypeU4)},
		"color":       {pio.ExportColumnField, "color", -1, int(encoding.FieldTypeCategoricalU8)},
		"color_label": {pio.ExportColumnLabel, "color", -1, textColumn},
		"v_1":         {pio.ExportColumnElement, "v", 0, f64},
		"v_2":         {pio.ExportColumnElement, "v", 1, f64},
		"v_3":         {pio.ExportColumnElement, "v", 2, f64},
	}, []string{"id", "color", "color_label", "v_1", "v_2", "v_3"})
	for _, col := range w.plan {
		if col.Kind == pio.ExportColumnElement && !col.Field.Nullable {
			t.Fatalf("element column %q is not nullable; a NaN element is a null cell", col.Name)
		}
	}

	// The header is not the schema: lining it up by position would
	// type some column with another field — the bug the plan exists to
	// stop. Keep the fixture one where that is true.
	shifted := false
	for i, name := range w.header {
		shifted = shifted || i >= len(w.schema.Fields) || w.schema.Fields[i].Name != name
	}
	if !shifted {
		t.Fatal("header lines up with the schema by position; the fixture no longer separates position from name")
	}
}

// TestColumnAwareWriter_ListAwareGetsListColumn: a writer that also
// takes native lists sees the vector as ONE ExportColumnList typed by the
// vector field, and SetListColumns after SetExportColumns.
func TestColumnAwareWriter_ListAwareGetsListColumn(t *testing.T) {
	t.Parallel()

	w := &planListWriter{}
	planExport(t, w)

	if want := []string{"SetPulseSchema", "SetExportColumns", "SetListColumns", "WriteHeader"}; !reflect.DeepEqual(w.calls, want) {
		t.Fatalf("calls %v, want %v", w.calls, want)
	}
	checkPlan(t, &w.planWriter, map[string]wantColumn{
		"id":          {pio.ExportColumnField, "id", -1, int(encoding.FieldTypeU4)},
		"color":       {pio.ExportColumnField, "color", -1, int(encoding.FieldTypeCategoricalU8)},
		"color_label": {pio.ExportColumnLabel, "color", -1, textColumn},
		"v":           {pio.ExportColumnList, "v", -1, int(encoding.FieldTypeVecF64)},
	}, []string{"id", "color", "color_label", "v"})
	if len(w.lists) != 1 || w.lists[0].Index != 3 {
		t.Fatalf("list columns %+v, want v at index 3", w.lists)
	}
}

// TestColumnFields_ByNameWithoutPlan: a writer without the plan still
// types by NAME through ColumnFields — the documented fallback — so the
// label column is text and each element column its element field.
func TestColumnFields_ByNameWithoutPlan(t *testing.T) {
	t.Parallel()

	w := &planWriter{}
	planExport(t, schemaOnlyWriter{w})
	if w.plan != nil {
		t.Fatalf("a writer without ColumnAwareWriter received a plan: %+v", w.plan)
	}
	fields := pio.ColumnFields(nil, w.schema, w.header)
	for i, name := range w.header {
		f := fields[i]
		switch {
		case name == "color_label":
			if f != nil {
				t.Fatalf("color_label typed %s, want text", f.Type)
			}
		case f == nil || f.Name != name:
			t.Fatalf("column %q typed by %+v, want the schema field of that name", name, f)
		}
	}
}

// TestColumnAwareWriter_ConvertNeverCalls: a convert's columns are
// source text, so ConvertJob never hands a plan.
func TestColumnAwareWriter_ConvertNeverCalls(t *testing.T) {
	t.Parallel()

	nd, err := pio.NewReaderFromBytes(pio.FormatNDJSON, []byte(planNDJSON), pio.ReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	w := &planWriter{}
	if _, err := (&pio.ConvertJob{Source: nd, Target: w, SampleRows: 1000}).Run(context.Background()); err != nil {
		t.Fatalf("convert: %v", err)
	}
	if w.plan != nil {
		t.Fatalf("ConvertJob handed a plan: %+v", w.plan)
	}
}
