package descriptor

import (
	"bytes"
	"io"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// DefaultDictionaryLimit is the default max entries shown for categorical dictionaries.
const DefaultDictionaryLimit = 100

// InspectOptions controls inspect behavior.
type InspectOptions struct {
	// FullDict disables dictionary truncation when true.
	FullDict bool
	// DictionaryLimit overrides the default truncation limit.
	// Zero means use DefaultDictionaryLimit.
	DictionaryLimit int
}

// InspectResult holds the schema inspection output.
type InspectResult struct {
	FieldCount int             `json:"field_count"`
	Fields     []*InspectField `json:"fields"`
}

// InspectField describes a single field in the inspect output.
type InspectField struct {
	Name              string          `json:"name"`
	Type              string          `json:"type"`
	ByteOffset        int             `json:"byte_offset"`
	BitPosition       int             `json:"bit_position"`
	Description       string          `json:"description"`
	DescriptionSource string          `json:"description_source"`
	Categorical       bool            `json:"categorical"`
	Dictionary        *DictionaryInfo `json:"dictionary,omitempty"`
	// Precision is the decimal128 precision (1-38). Present only for
	// decimal128 / nullable_decimal128 fields.
	Precision *uint8 `json:"precision,omitempty"`
	// Scale is the decimal128 scale (0-precision). Present only for
	// decimal128 / nullable_decimal128 fields.
	Scale *uint8 `json:"scale,omitempty"`
	// H3Resolution is the native cell resolution (0-15). Present only
	// for h3_cell fields where the import recorded a resolution.
	H3Resolution *uint8 `json:"h3_resolution,omitempty"`
}

// DictionaryInfo describes the categorical dictionary for a field.
type DictionaryInfo struct {
	TotalEntries int      `json:"total_entries"`
	Truncated    bool     `json:"truncated"`
	Values       []string `json:"values"`
}

// Inspect reads a .pulse file header and schema, returning structured
// field information. It never reads record data.
func Inspect(fileData io.ReadSeeker, opts *InspectOptions) *Envelope {
	if opts == nil {
		opts = &InspectOptions{}
	}
	limit := opts.DictionaryLimit
	if limit <= 0 {
		limit = DefaultDictionaryLimit
	}
	if opts.FullDict {
		limit = 0 // no truncation
	}

	result := &InspectResult{}
	env := NewEnvelope(result)

	// Read header.
	if err := encoding.ReadHeader(fileData); err != nil {
		env.AddError(string(errors.ENCODING_INVALID), "invalid pulse file header: "+err.Error(), nil)
		return env
	}

	// Read schema.
	schema, err := encoding.ReadSchema(fileData)
	if err != nil {
		env.AddError(string(errors.ENCODING_INVALID), "invalid pulse schema: "+err.Error(), nil)
		return env
	}

	result.FieldCount = len(schema.Fields)
	result.Fields = make([]*InspectField, len(schema.Fields))

	for i, f := range schema.Fields {
		desc := f.Description
		descSource := "schema"
		if desc == "" {
			desc = synthesizeDescription(f)
			descSource = "synthesized"
		}

		field := &InspectField{
			Name:              f.Name,
			Type:              f.Type.String(),
			ByteOffset:        f.ByteOffset,
			BitPosition:       f.BitPosition,
			Description:       desc,
			DescriptionSource: descSource,
			Categorical:       f.Type.IsCategorical(),
		}

		if f.Type.IsDecimal() {
			p := f.Precision
			s := f.Scale
			field.Precision = &p
			field.Scale = &s
		}
		if f.Type == encoding.FieldTypeH3Cell && f.H3Resolution != 0xFF {
			res := f.H3Resolution
			field.H3Resolution = &res
		}

		if f.Type.IsCategorical() && f.Dictionary != nil {
			values := f.Dictionary.Values()
			dictInfo := &DictionaryInfo{
				TotalEntries: len(values),
				Truncated:    false,
				Values:       values,
			}

			if limit > 0 && len(values) > limit {
				dictInfo.Truncated = true
				dictInfo.Values = values[:limit]
			}

			field.Dictionary = dictInfo
		}

		result.Fields[i] = field
	}

	return env
}

// InspectFromBytes is a convenience wrapper that creates a reader from bytes.
func InspectFromBytes(data []byte, opts *InspectOptions) *Envelope {
	return Inspect(bytes.NewReader(data), opts)
}

// synthesizeDescription generates a fallback description for fields
// that have no stored description.
func synthesizeDescription(f encoding.Field) string {
	switch {
	case f.Type.IsCategorical():
		return "Categorical field: " + f.Name
	case f.Type.IsDecimal():
		return "Decimal field: " + f.Name
	case f.Type == encoding.FieldTypePointF64:
		return "Geo point field: " + f.Name
	case f.Type == encoding.FieldTypeH3Cell:
		return "H3 cell field: " + f.Name
	}
	return "Numeric field: " + f.Name
}
