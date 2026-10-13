package iocore

import "github.com/frankbardon/pulse/encoding"

// ExportColumnKind classifies one output column of an export by what it
// carries.
type ExportColumnKind uint8

const (
	// ExportColumnField is a source field's own cell.
	ExportColumnField ExportColumnKind = iota
	// ExportColumnLabel is a `<field>_label` augment sibling: label text.
	ExportColumnLabel
	// ExportColumnElement is one element of a vector EXPANDED into scalar
	// columns: a nullable f32 / f64 cell.
	ExportColumnElement
	// ExportColumnList is a vector carried as ONE native list cell (a
	// ListAwareWriter target).
	ExportColumnList
)

// ExportColumn describes one column of an export's header: its name, what
// it carries, the source field it comes from and the field that TYPES it.
//
// The export's header is not the source schema's field list. Projection
// (ExportJob.Includes) drops fields, a label augment inserts `_label`
// siblings, and vector expansion turns one field into Dim columns — so a
// column's position says nothing about its source field. A typed writer
// that read its column types off the schema by position typed a column
// with another field's type, silently whenever the value happened to
// parse. ExportColumn is the explicit mapping that replaces that guess;
// ExportJob builds it once and it drives both the written header and
// ExportJob.Predict's PredictReport.Columns.
type ExportColumn struct {
	// Name is the header name.
	Name string
	// Kind is what the column carries.
	Kind ExportColumnKind
	// Source is the name of the source field the column comes from (the
	// augmented field for a label sibling, the vector for an element).
	Source string
	// Element is the 0-based vector element of an ExportColumnElement,
	// -1 for every other kind.
	Element int
	// Field is the field that types the column: the source field itself
	// for ExportColumnField / ExportColumnList, the nullable scalar f32 /
	// f64 element field (named Name) for ExportColumnElement, and nil —
	// a text column — for ExportColumnLabel and for a field whose cells a
	// label REPLACE binding rewrites to text.
	Field *encoding.Field
}

// ColumnAwareWriter is an optional extension of Writer for typed targets
// that want the export's explicit column plan. ExportJob.Run calls
// SetExportColumns once, before WriteHeader, with one entry per header
// column in header order; the writer types column i from cols[i].Field
// rather than from the source schema's i-th field. A CohortWriter target
// is never called (it lays out its own columns), nor is any writer under
// ConvertJob (its columns are source text).
type ColumnAwareWriter interface {
	Writer
	SetExportColumns(cols []ExportColumn)
}

// ColumnFields resolves, for each header column, the field that types it
// — nil for a text column. With a column plan whose names match header
// one for one, the plan decides. Without one (a caller that hands the
// writer only SetPulseSchema), each header column is typed by the schema
// field of the SAME NAME, never by position; a column with no such field
// is text. A nil schema and no plan yields all text.
func ColumnFields(cols []ExportColumn, schema *encoding.Schema, header []string) []*encoding.Field {
	out := make([]*encoding.Field, len(header))
	if planMatches(cols, header) {
		for i := range cols {
			out[i] = cols[i].Field
		}
		return out
	}
	if schema == nil {
		return out
	}
	byName := make(map[string]int, len(schema.Fields))
	for i, f := range schema.Fields {
		byName[f.Name] = i
	}
	for i, name := range header {
		if at, ok := byName[name]; ok {
			out[i] = &schema.Fields[at]
		}
	}
	return out
}

func planMatches(cols []ExportColumn, header []string) bool {
	if cols == nil || len(cols) != len(header) {
		return false
	}
	for i := range cols {
		if cols[i].Name != header[i] {
			return false
		}
	}
	return true
}
