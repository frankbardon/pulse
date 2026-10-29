package io

import (
	"fmt"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
)

// This file holds the schema-level checks of an explicit (caller-
// authored) schema that ImportJob and the cohort builder share.
// ImportJob runs checkSchemaDescriptions on a user-supplied
// ImportJob.Schema before the row pass; the builder runs it too, plus
// its own structural and quality checks (build_schema.go).

// checkSchemaDescriptions refuses a field description past
// encoding.MaxDescriptionBytes with PULSE_IMPORT_DESCRIPTION_TOO_LONG —
// the code the schema writer raises — before any row is read, so a long
// description fails fast instead of after a whole pass. The rule is
// encx.DescriptionLengthRefusal, which a materialize predict shares.
func checkSchemaDescriptions(schema *encoding.Schema) error {
	if ce := encx.DescriptionLengthRefusal(schema.Fields); ce != nil {
		return ce
	}
	return nil
}

// resolveExplicitSchema matches a caller-authored schema to the source's
// columns BY NAME and returns a COPY whose CsvColumnIdx values point at
// the header positions those names occupy. It is the one resolver every
// explicit-schema path runs — ImportJob.Run, ImportJob.Predict (and
// through it the measured predict in import_detect.go) and
// ConvertJob.Run / Predict — so the verbs cannot disagree about which
// column a field reads.
//
// Before this resolver an explicit schema mapped by POSITION: field i
// read column i whatever either was called, so a schema written in a
// different order than the source silently mislabelled same-typed
// columns. Matching is exact (case and whitespace included) and every
// mismatch is a coded refusal raised before a row is read:
//
//   - PULSE_IMPORT_SCHEMA_DUPLICATE — a field name declared twice, or a
//     header name repeated (when the schema names it, or extra columns
//     are not ignored);
//   - PULSE_IMPORT_SCHEMA_FIELD_MISSING — a field with no column;
//   - PULSE_IMPORT_SCHEMA_COLUMN_UNMAPPED — a column with no field,
//     unless ignoreExtra, which drops such columns instead.
//
// A keyed source (LateKeyRefuser: NDJSON, a JSON array) is armed to
// refuse a key first seen after the first object with
// PULSE_IMPORT_SCHEMA_KEY_LATE, under the same ignoreExtra opt-out.
//
// A source with no header at all (an empty file) has no rows to
// mislabel; the schema is returned as a copy, unresolved.
//
// The copy is laid out canonically (encx.LayoutFields): caller-supplied
// ByteOffset / BitPosition are ignored.
//
// The caller's schema is never mutated: the row pass assigns
// dictionaries and (on predict) nullability on the copy.
func resolveExplicitSchema(source Reader, schema *encoding.Schema, ignoreExtra bool) (*encoding.Schema, error) {
	fieldIdx := make(map[string]int, len(schema.Fields))
	fields := make([]string, len(schema.Fields))
	for i := range schema.Fields {
		name := schema.Fields[i].Name
		if _, dup := fieldIdx[name]; dup {
			return nil, errors.NewCodedErrorWithDetails(
				errors.PULSE_IMPORT_SCHEMA_DUPLICATE,
				fmt.Sprintf("explicit schema declares field %q more than once", name),
				map[string]any{"field": name, "side": "schema"},
			)
		}
		fieldIdx[name] = i
		fields[i] = name
	}

	header, err := source.ReadHeader()
	if err != nil {
		return nil, err
	}

	out := *schema
	out.Fields = append([]encoding.Field(nil), schema.Fields...)
	// Caller-supplied ByteOffset / BitPosition are ignored: the record
	// writer and the decoder place every field by a width-derived cursor,
	// so the stored values must be that same layout. A caller-authored
	// offset (an embedder schema that never set one is all zeros) would
	// otherwise be written into the schema block, refused by shard
	// cohesion against an inferred twin, and — on BitPosition — read
	// back from a bit the writer never set.
	encx.LayoutFields(&out)
	if len(header) == 0 {
		return &out, nil
	}

	columns := append([]string(nil), header...)
	colIdx := make(map[string]int, len(header))
	for i, name := range header {
		if _, dup := colIdx[name]; dup {
			if _, named := fieldIdx[name]; named || !ignoreExtra {
				return nil, errors.NewCodedErrorWithDetails(
					errors.PULSE_IMPORT_SCHEMA_DUPLICATE,
					fmt.Sprintf("source header repeats column %q; by-name schema matching needs unique column names", name),
					map[string]any{"column": name, "side": "header", "columns": columns},
				)
			}
			continue
		}
		colIdx[name] = i
	}

	for i := range out.Fields {
		name := out.Fields[i].Name
		idx, ok := colIdx[name]
		if !ok {
			return nil, errors.NewCodedErrorWithDetails(
				errors.PULSE_IMPORT_SCHEMA_FIELD_MISSING,
				fmt.Sprintf("explicit schema field %q has no source column of that name", name),
				map[string]any{"field": name, "columns": columns},
			)
		}
		out.Fields[i].CsvColumnIdx = idx
	}

	if !ignoreExtra {
		for _, name := range header {
			if _, ok := fieldIdx[name]; !ok {
				return nil, errors.NewCodedErrorWithDetails(
					errors.PULSE_IMPORT_SCHEMA_COLUMN_UNMAPPED,
					fmt.Sprintf("source column %q is not named by the explicit schema (pass --ignore-extra to drop it)", name),
					map[string]any{"column": name, "fields": fields},
				)
			}
		}
	}

	if lk, ok := source.(LateKeyRefuser); ok {
		lk.RefuseLateKeys(!ignoreExtra)
	}
	return &out, nil
}

// ParseSchemaFieldType resolves the type name an explicit schema file
// declares for field. The name must be a registered field type
// (encoding.ParseFieldType, exact spelling); anything else is
// PULSE_IMPORT_SCHEMA_TYPE_UNKNOWN naming the field, the type as written
// and the valid set. There is no fallback — an unknown name used to
// become f64 silently, so a typo stored the column in the wrong type.
func ParseSchemaFieldType(field, name string) (encoding.FieldType, error) {
	if ft, ok := encoding.ParseFieldType(name); ok {
		return ft, nil
	}
	valid := RegisteredFieldTypeNames()
	return 0, errors.NewCodedErrorWithDetails(
		errors.PULSE_IMPORT_SCHEMA_TYPE_UNKNOWN,
		fmt.Sprintf("schema field %q declares unknown type %q; valid types: %s", field, name, strings.Join(valid, ", ")),
		map[string]any{"field": field, "type": name, "valid": valid},
	)
}

// RegisteredFieldTypeNames lists every registered field type's name in
// type-byte order, walked off the encoding registry (FieldType.IsKnown)
// so a new type joins the list without an edit here.
func RegisteredFieldTypeNames() []string {
	var names []string
	for ft := encoding.FieldType(0); ft.IsKnown(); ft++ {
		names = append(names, ft.String())
	}
	return names
}
