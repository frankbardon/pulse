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

// ShardInfo is one shard inside a Pulse shard archive, surfaced by
// Inspect for archive-backed cohorts. Mirrors the public shape of
// service.ShardEntry without importing it — descriptor/ is header-only
// and must not depend on the execution layer.
//
// Filename is the basename of the shard inside the archive
// (e.g. "20190101.pulse"). RecordCount is the number of records carried
// by the shard, computed by peeking that shard's own header (the
// per-shard headers are authoritative; the canonical _schema.pulse
// aggregate is only a sanity check).
type ShardInfo struct {
	Filename    string `json:"filename"`
	RecordCount int64  `json:"record_count"`
}

// InspectResult holds the schema inspection output.
//
// Shards is populated when the inspected file is a Pulse shard archive
// (first four bytes match the zip magic PK\x03\x04). Entries are
// listed in zip central-directory order, which equals shard insertion
// order. Single-file cohorts leave Shards as an empty slice (never
// nil) so the JSON envelope emits "shards": [] rather than null.
//
// For archive-backed cohorts, FieldCount and Fields reflect the
// canonical schema carried in the reserved _schema.pulse entry, and
// RecordCount (when present in metadata) plus aggregate counting via
// per-shard headers populate the cumulative total — see ShardInfo.
type InspectResult struct {
	FieldCount  int             `json:"field_count"`
	Fields      []*InspectField `json:"fields"`
	Shards      []ShardInfo     `json:"shards"`
	RecordCount int64           `json:"record_count"`
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
}

// DictionaryInfo describes the categorical dictionary for a field.
type DictionaryInfo struct {
	TotalEntries int      `json:"total_entries"`
	Truncated    bool     `json:"truncated"`
	Values       []string `json:"values"`
}

// Inspect reads a .pulse file header and schema, returning structured
// field information. It never reads record data. This entry point
// handles single-file cohorts only; archive-backed cohorts route
// through InspectFromBytes which can magic-detect the zip container.
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

	result := &InspectResult{Shards: []ShardInfo{}}
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
		result.Fields[i] = renderInspectField(f, limit)
	}

	return env
}

// InspectFromBytes inspects either a single-file .pulse cohort or a
// Pulse shard archive. Detection is by the first four bytes: zip
// magic PK\x03\x04 → archive path; PULSE magic → single-file path.
// Other prefixes return the standard ENCODING_INVALID envelope from
// the single-file path.
//
// For archive-backed cohorts, the canonical schema and dictionaries
// come from the reserved _schema.pulse entry; Shards enumerates every
// non-reserved entry in central-directory order with per-shard
// RecordCount populated by peeking each shard's header; the
// envelope-level RecordCount is the cumulative sum across shards.
func InspectFromBytes(data []byte, opts *InspectOptions) *Envelope {
	if len(data) >= 4 && data[0] == 'P' && data[1] == 'K' && data[2] == 0x03 && data[3] == 0x04 {
		return inspectArchive(data, opts)
	}
	return Inspect(bytes.NewReader(data), opts)
}

// inspectArchive walks a Pulse shard archive: reads the canonical
// schema from _schema.pulse, enumerates non-reserved entries in
// central-directory order, and reports per-shard plus cumulative
// record counts. The result schema and dictionaries match the
// canonical entry (single source of truth); the truncated dictionary
// limit applies as for single-file cohorts.
func inspectArchive(data []byte, opts *InspectOptions) *Envelope {
	if opts == nil {
		opts = &InspectOptions{}
	}
	limit := opts.DictionaryLimit
	if limit <= 0 {
		limit = DefaultDictionaryLimit
	}
	if opts.FullDict {
		limit = 0
	}

	result := &InspectResult{Shards: []ShardInfo{}}
	env := NewEnvelope(result)

	reader := bytes.NewReader(data)
	arch, err := encoding.OpenArchive(reader, int64(len(data)))
	if err != nil {
		env.AddError(string(errors.PULSE_ARCHIVE_CORRUPT), "invalid pulse shard archive: "+err.Error(), nil)
		return env
	}

	rc, err := arch.Open(encoding.ReservedSchemaName)
	if err != nil {
		env.AddError(string(errors.PULSE_SHARD_MISSING),
			"archive missing reserved schema entry "+encoding.ReservedSchemaName+": "+err.Error(), nil)
		return env
	}
	doc, derr := encoding.ReadSchemaDoc(rc)
	_ = rc.Close()
	if derr != nil {
		env.AddError(string(errors.ENCODING_INVALID), "invalid schema doc: "+derr.Error(), nil)
		return env
	}
	if doc == nil || doc.Schema == nil {
		env.AddError(string(errors.ENCODING_INVALID), "schema doc has no schema", nil)
		return env
	}

	schema := doc.Schema
	result.FieldCount = len(schema.Fields)
	result.Fields = make([]*InspectField, len(schema.Fields))
	for i, f := range schema.Fields {
		result.Fields[i] = renderInspectField(f, limit)
	}

	// Enumerate shard entries (excluding the reserved schema) and peek
	// each one for the authoritative per-shard record count.
	var cumulative int64
	for _, entry := range arch.Entries() {
		if entry.Name == encoding.ReservedSchemaName {
			continue
		}
		count, perr := arch.PeekShardRecordCount(entry.Name)
		if perr != nil {
			env.AddError(string(errors.PULSE_SHARD_HEADER_INVALID),
				"peeking record count for shard "+entry.Name+": "+perr.Error(),
				map[string]any{"entry": entry.Name})
			return env
		}
		result.Shards = append(result.Shards, ShardInfo{
			Filename:    entry.Name,
			RecordCount: count,
		})
		cumulative += count
	}
	result.RecordCount = cumulative

	return env
}

// renderInspectField builds the InspectField record for a single
// encoding.Field, applying the dictionary truncation limit. Shared
// between the single-file path (in Inspect) and the archive path.
func renderInspectField(f encoding.Field, limit int) *InspectField {
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
	return field
}

// synthesizeDescription generates a fallback description for fields
// that have no stored description.
func synthesizeDescription(f encoding.Field) string {
	switch {
	case f.Type.IsCategorical():
		return "Categorical field: " + f.Name
	case f.Type.IsDecimal():
		return "Decimal field: " + f.Name
	}
	return "Numeric field: " + f.Name
}
