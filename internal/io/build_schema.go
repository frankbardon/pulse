package io

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
)

// descriptionQualityFindings returns one PULSE_FIELD_DESCRIPTION_LOW_QUALITY
// finding per field whose description fails
// encx.IsLowQualityDescription (the rule predict applies), in field
// order. Under strict the first finding is returned as the error.
func descriptionQualityFindings(schema *encoding.Schema, strict bool) ([]*errors.CodedError, error) {
	var out []*errors.CodedError
	for i := range schema.Fields {
		f := &schema.Fields[i]
		if !encx.IsLowQualityDescription(f.Description) {
			continue
		}
		finding := errors.NewCodedErrorWithDetails(
			errors.PULSE_FIELD_DESCRIPTION_LOW_QUALITY,
			"field "+f.Name+" has a low-quality description",
			map[string]any{"field": f.Name, "description": f.Description})
		if strict {
			return nil, finding
		}
		out = append(out, finding)
	}
	return out, nil
}

// checkSchemaShape refuses a schema the builder cannot write as a
// readable cohort: no fields, more fields than the u16 field count
// holds, an empty or duplicate field name (every name-keyed read
// would resolve the wrong field), an unknown type, a decimal128
// precision outside 1..38 or a scale past its precision, a pre-seeded
// dictionary on a type that has none or longer than its rung holds,
// and parent groups (declared through the builder options, never in
// the schema). Every refusal is SERVICE_VALIDATION naming the field.
func checkSchemaShape(schema *encoding.Schema) error {
	invalid := func(msg string, details map[string]any) error {
		return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION, msg, details)
	}
	if schema == nil || len(schema.Fields) == 0 {
		return invalid("cohort schema has no fields", nil)
	}
	if len(schema.Fields) > math.MaxUint16 {
		return invalid("cohort schema has more fields than a .pulse schema block holds",
			map[string]any{"fields": len(schema.Fields), "max": math.MaxUint16})
	}
	if len(schema.Groups) > 0 {
		return invalid("schema carries parent groups; declare groups through the builder options, not the schema",
			map[string]any{"groups": len(schema.Groups)})
	}
	seen := make(map[string]int, len(schema.Fields))
	for i := range schema.Fields {
		f := &schema.Fields[i]
		if f.Name == "" {
			return invalid("field name is empty", map[string]any{"field_index": i})
		}
		if prev, dup := seen[f.Name]; dup {
			return invalid(fmt.Sprintf("duplicate field name %q", f.Name),
				map[string]any{"field": f.Name, "field_index": i, "first_index": prev})
		}
		seen[f.Name] = i
		if !f.Type.IsKnown() {
			return invalid(fmt.Sprintf("field %q has an unknown type", f.Name),
				map[string]any{"field": f.Name, "type_byte": int(f.Type)})
		}
		if f.Type.IsDecimal() && (f.Precision < 1 || f.Precision > encoding.MaxDecimalPrecision || f.Scale > f.Precision) {
			return invalid(fmt.Sprintf("field %q: decimal128 needs precision 1..%d and scale <= precision", f.Name, encoding.MaxDecimalPrecision),
				map[string]any{"field": f.Name, "precision": f.Precision, "scale": f.Scale})
		}
		if f.Dictionary != nil && f.Dictionary.Count() > 0 {
			if !f.Type.HasDictionary() {
				return invalid(fmt.Sprintf("field %q: type %s carries no dictionary", f.Name, f.Type),
					map[string]any{"field": f.Name, "type": f.Type.String()})
			}
			if limit := dictionaryCeiling(f.Type); uint64(f.Dictionary.Count()) > uint64(limit) {
				return invalid(fmt.Sprintf("field %q: pre-seeded dictionary has %d entries, %s holds %d", f.Name, f.Dictionary.Count(), f.Type, limit),
					map[string]any{"field": f.Name, "entries": f.Dictionary.Count(), "max_entries": limit})
			}
		}
	}
	return nil
}

// dictionaryCeiling is the most dictionary entries a dictionary-bearing
// rung addresses.
func dictionaryCeiling(ft encoding.FieldType) uint32 {
	if ft.IsSet() {
		return ft.MaxSetEntries()
	}
	return ft.MaxCategoricalEntries()
}

// prepareBuildSchema validates a caller-authored builder schema and
// returns the schema the builder writes: a fresh copy whose layout —
// ByteOffset, BitPosition and CsvColumnIdx — is RECOMPUTED from field
// order, types and nullability exactly as import lays a schema out
// (bit-packed fields one whole byte each, bit 0), so caller-supplied
// layout values are ignored. Each dictionary-bearing field gets its own
// dictionary seeded with the caller's pre-seeded entries in order (the
// caller's dictionary is never mutated); first-seen labels grow it
// later. The description checks are the ones import shares
// (checkSchemaDescriptions); quality findings are returned as warnings
// or, under strict, as the error.
func prepareBuildSchema(in *encoding.Schema, strict bool) (*encoding.Schema, []*errors.CodedError, error) {
	if err := checkSchemaShape(in); err != nil {
		return nil, nil, err
	}
	if err := checkSchemaDescriptions(in); err != nil {
		return nil, nil, err
	}
	warns, err := descriptionQualityFindings(in, strict)
	if err != nil {
		return nil, nil, err
	}
	out := &encoding.Schema{Fields: make([]encoding.Field, len(in.Fields))}
	for i := range in.Fields {
		src := &in.Fields[i]
		f := encoding.Field{
			Name:         src.Name,
			Type:         src.Type,
			Nullable:     src.Nullable,
			CsvColumnIdx: i,
			Description:  src.Description,
		}
		if f.Type.IsDecimal() {
			f.Precision, f.Scale = src.Precision, src.Scale
		}
		if f.Type.HasDictionary() {
			f.Dictionary = encoding.NewDictionary()
			if src.Dictionary != nil {
				for _, v := range src.Dictionary.Values() {
					if _, err := f.Dictionary.Add(v); err != nil {
						return nil, nil, err
					}
				}
			}
		}
		out.Fields[i] = f
	}
	relayoutOffsets(out)
	return out, warns, nil
}
