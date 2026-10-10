package io

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
)

// SchemaFileField is one entry of an explicit schema file: the JSON
// array `import schema-template` emits and `--schema` reads. precision
// and scale apply to decimal128 only and are ignored for every other
// type. A schema file carries no layout — offsets and bit positions are
// a pure function of field order and types (encx.LayoutFields) — and no
// column index: ImportJob / ConvertJob match each field to its source
// column BY NAME (resolveExplicitSchema).
type SchemaFileField struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Nullable    bool   `json:"nullable"`
	Precision   uint8  `json:"precision,omitempty"`
	Scale       uint8  `json:"scale,omitempty"`
}

// ParseSchemaJSON decodes an explicit schema document (a JSON array of
// SchemaFileField) into an encoding.Schema. It is the one schema-file
// decoder every surface shares (CLI import / import predict / convert
// --schema, and any library or MCP caller that holds the document
// inline), so they cannot lay the same document out differently.
//
// Type names resolve through ParseSchemaFieldType (an unknown name is
// PULSE_IMPORT_SCHEMA_TYPE_UNKNOWN, never a silent fallback). The layout
// — ByteOffset and BitPosition — comes from the canonical
// encx.LayoutFields, so a document mixing bit-packed (u4, packed_bool)
// and byte-aligned fields yields exactly the schema inference builds for
// the same fields. CsvColumnIdx is the array position, a placeholder the
// by-name resolver overwrites before any row is read.
//
// A document that is not a JSON array of field objects, or that lists no
// field, is PULSE_IMPORT_SCHEMA_INVALID (details: reason "malformed" /
// "empty").
func ParseSchemaJSON(data []byte) (*encoding.Schema, error) {
	var defs []SchemaFileField
	if err := json.Unmarshal(data, &defs); err != nil {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_IMPORT_SCHEMA_INVALID,
			"parsing schema JSON: "+err.Error()+"; a schema is a JSON array of {name, type, nullable, description} objects",
			map[string]any{"reason": "malformed", "error": err.Error()})
	}
	return SchemaFromFields(defs)
}

// SchemaFromFields builds the encoding.Schema an explicit schema
// document's decoded entries describe — the step ParseSchemaJSON runs
// after decoding, exposed for callers that already hold the entries
// (MCP pulse_import's `schema` argument), so every surface resolves type
// names and lays fields out through this one routine. An empty list is
// PULSE_IMPORT_SCHEMA_INVALID (details: reason "empty"): no cohort can
// be written from it, and "no schema" is spelled by passing none.
func SchemaFromFields(defs []SchemaFileField) (*encoding.Schema, error) {
	if len(defs) == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_IMPORT_SCHEMA_INVALID,
			"explicit schema lists no fields; omit the schema to infer one",
			map[string]any{"reason": "empty"})
	}
	fields := make([]encoding.Field, len(defs))
	for i, d := range defs {
		ft, err := ParseSchemaFieldType(d.Name, d.Type)
		if err != nil {
			return nil, err
		}
		f := encoding.Field{
			Name:         d.Name,
			Type:         ft,
			Nullable:     d.Nullable,
			CsvColumnIdx: i,
			Description:  d.Description,
		}
		if ft.IsDecimal() {
			f.Precision = d.Precision
			f.Scale = d.Scale
		}
		fields[i] = f
	}
	encx.LayoutFields(fields)
	return &encoding.Schema{Fields: fields}, nil
}

// LoadSchemaFile reads an explicit schema document from path on fs and
// decodes it with ParseSchemaJSON.
func LoadSchemaFile(fs afero.Fs, path string) (*encoding.Schema, error) {
	data, err := afero.ReadFile(fs, path)
	if err != nil {
		return nil, fmt.Errorf("reading schema file: %w", err)
	}
	return ParseSchemaJSON(data)
}

// SchemaFileTemplate renders schema as an explicit schema document — the
// `import schema-template` output — in the SchemaFileField shape
// ParseSchemaJSON reads back, so a template round-trips through the same
// decoder. Descriptions are left empty for the author to fill in;
// decimal128 fields carry their precision and scale (omitted for every
// other type), without which the document could not be imported as
// written.
func SchemaFileTemplate(schema *encoding.Schema) []SchemaFileField {
	out := make([]SchemaFileField, len(schema.Fields))
	for i := range schema.Fields {
		f := &schema.Fields[i]
		out[i] = SchemaFileField{Name: f.Name, Type: f.Type.String(), Nullable: f.Nullable}
		if f.Type.IsDecimal() {
			out[i].Precision, out[i].Scale = f.Precision, f.Scale
		}
	}
	return out
}
