package descriptor

import (
	"bytes"
	stderrors "errors"
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
//
// RecordCount is populated on BOTH paths: the cumulative per-shard sum
// for an archive, and (payload_bytes / Schema.RecordByteSize) for a
// single-file cohort — derived from the file length, never by reading a
// record. A real 0 means an empty cohort. When the payload length is
// not a whole multiple of the record stride (a truncated tail) the
// count is the floor and the envelope carries an ENCODING_INVALID
// warning naming the leftover bytes.
//
// Layout and Groups are present only for a cohort written in a format
// newer than 0x01 (parent groups, constant elision). A 0x01 cohort
// omits both keys, so its output is byte-identical to the pre-0x02
// shape. Every figure in them is derived from the header, the schema
// block and the file length — the same header-only facts RecordCount
// uses — never from a record.
type InspectResult struct {
	FieldCount  int             `json:"field_count"`
	Fields      []*InspectField `json:"fields"`
	Shards      []ShardInfo     `json:"shards"`
	RecordCount int64           `json:"record_count"`
	// Layout describes the physical row of a 0x02 cohort. Omitted for 0x01.
	Layout *InspectLayout `json:"layout,omitempty"`
	// Groups reports each parent group in file order. Omitted when the
	// schema declares none.
	Groups []*InspectGroup `json:"groups,omitempty"`
}

// InspectLayout is the on-wire row shape of a cohort whose format
// version is newer than 0x01. PhysicalRecordStride is what each record
// occupies on disk (group indices + row fields + the narrowed null
// bitmap); LogicalRecordStride is the row an ungrouped twin would carry
// — the width operators and the field listing reason in. Their
// difference is the per-record saving the groups buy, before the
// resident dictionaries are paid for.
type InspectLayout struct {
	PulseFormatVersion   int `json:"pulse_format_version"`
	PhysicalRecordStride int `json:"physical_record_stride"`
	LogicalRecordStride  int `json:"logical_record_stride"`
}

// InspectGroup is one parent group's realized dedup figures.
//
// Every number comes from encoding.AssessGroup over the schema and the
// file-length record count, so inspect, import and retro-dedup report
// identical arithmetic. Ratio is record_count ÷ entry_count;
// DictionaryBytes is the group's dictionary, held RESIDENT in memory
// for as long as the cohort is open. Verdict applies the import-time
// ratio floor (encoding.DefaultDedupRatioFloor) through the same
// GroupViability.Finding rule the import gate uses: "low_ratio" means
// the group would draw PULSE_DEDUP_LOW_RATIO at import today. Inspect
// reports it as a figure, never as an envelope warning — a deliberately
// deduped low-ratio group is a legitimate file.
//
// Key / Members / Fields mirror the import report's GroupReport, so a
// group reads back as the `--group KEY:MEMBER` declaration that formed
// it: Key is the declared key (omitted when every member is key, i.e.
// a plain tuple group), Members the remaining members, Fields every
// member in logical (schema) order.
//
// Label numbers the group by its position in the file; the format has
// no group-name slot and no declaration ordinal (see
// encoding.Schema.GroupSpecOf), and it lists a keyless group's members
// in schema order rather than declaration order.
type InspectGroup struct {
	Group           int      `json:"group"`
	Label           string   `json:"label"`
	Kind            string   `json:"kind"`
	Key             []string `json:"key,omitempty"`
	Members         []string `json:"members"`
	Fields          []string `json:"fields"`
	EntryCount      int      `json:"entry_count"`
	EntryWidth      int      `json:"entry_width"`
	DictionaryBytes int64    `json:"dictionary_bytes"`
	MemberRowBytes  int      `json:"member_row_bytes"`
	IndexWidth      int      `json:"index_width"`
	Ratio           float64  `json:"ratio"`
	BreakEvenRatio  float64  `json:"break_even_ratio"`
	RatioFloor      float64  `json:"ratio_floor"`
	ByteDelta       int64    `json:"byte_delta"`
	GrowsFile       bool     `json:"grows_file"`
	Verdict         string   `json:"verdict"`
}

// InspectFieldGroup marks a field stored in a parent group's dictionary
// instead of the row. Kind "constant" means the field is constant-elided:
// one value for the whole cohort and no per-record bytes at all.
type InspectFieldGroup struct {
	Group int    `json:"group"`
	Key   bool   `json:"key"`
	Kind  string `json:"kind"`
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
	// Group is present only when the field is a parent-group member
	// (0x02): its value lives in that group's dictionary, not the row.
	// In a grouped cohort every field's ByteOffset is its LOGICAL-row
	// offset (the schema keeps the ungrouped descriptors), never a
	// physical one — Layout.PhysicalRecordStride is the on-disk row.
	Group *InspectFieldGroup `json:"group,omitempty"`
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
	pulseVersion, err := encoding.ReadHeader(fileData)
	if err != nil {
		// Carry the header error's details (an unsupported version names
		// the offending byte and the accepted set) so the refusal stays
		// actionable through the envelope.
		var details map[string]any
		var ce *errors.CodedError
		if stderrors.As(err, &ce) {
			details = ce.Details
		}
		env.AddError(string(errors.ENCODING_INVALID), "invalid pulse file header: "+err.Error(), details)
		return env
	}

	// Read schema.
	schema, err := encoding.ReadSchema(fileData, pulseVersion)
	if err != nil {
		env.AddError(string(errors.ENCODING_INVALID), "invalid pulse schema: "+err.Error(), nil)
		return env
	}

	result.FieldCount = len(schema.Fields)
	result.Fields = make([]*InspectField, len(schema.Fields))
	for i, f := range schema.Fields {
		result.Fields[i] = renderInspectField(f, limit)
	}

	// Record count for a single-file cohort is derivable from the bytes
	// remaining after the header + schema — no record is read. The
	// archive path populates RecordCount from per-shard headers; before
	// this, the single-file path left it at its zero value, which is
	// indistinguishable on the wire from a genuinely empty cohort.
	count, trailing, ok := deriveSingleFileRecordCount(fileData, schema)
	if pulseVersion != encoding.FormatVersionV1 || schema.HasGroups() {
		// ok == false leaves count at 0: no ratio is measured.
		renderGroups(result, schema, pulseVersion, count)
	}
	if ok {
		result.RecordCount = count
		if trailing != 0 {
			env.AddWarning(string(errors.ENCODING_INVALID),
				"cohort payload length is not a whole multiple of the record stride; record_count is the floor",
				map[string]any{
					"record_stride":  schema.RecordByteSize(),
					"trailing_bytes": trailing,
				})
		}
	}

	return env
}

// deriveSingleFileRecordCount returns the number of whole records in a
// single-file cohort, plus any leftover trailing bytes that do not
// complete a record. The arithmetic is not its own: it measures the
// payload region and hands it to encoding.Schema.RecordCountForPayload,
// which is the ONE derivation Service.CountRecords calls too, so the
// two arms cannot disagree about the NUMBER over identical bytes. They
// disagree only about observability, deliberately — this arm has an
// envelope and warns about a truncated tail, CountRecords has no
// warning channel and stays silent (see RecordCountForPayload).
//
// It must be called immediately after ReadHeader + ReadSchema, with the
// stream positioned at the first record. Cost is two seeks: no record
// byte is read, so the header-only inspect contract holds. The stream
// is left exactly where it was found.
//
// ok is false when the file size cannot be established or the schema's
// stride is zero (a field-less schema has no records to count); the
// caller then leaves RecordCount at 0.
func deriveSingleFileRecordCount(rs io.ReadSeeker, schema *encoding.Schema) (count, trailing int64, ok bool) {
	payloadStart, err := rs.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, 0, false
	}
	end, err := rs.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, 0, false
	}
	if _, err := rs.Seek(payloadStart, io.SeekStart); err != nil {
		return 0, 0, false
	}
	return schema.RecordCountForPayload(end - payloadStart)
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

	if f.Type.HasDictionary() && f.Dictionary != nil {
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
	case f.Type.IsSet():
		return "Set field: " + f.Name
	case f.Type.IsDecimal():
		return "Decimal field: " + f.Name
	}
	return "Numeric field: " + f.Name
}

// renderGroups fills the 0x02 layout, the per-group dedup figures and
// the per-field group markers. rows is the file-length record count.
// Header-only: every input is the schema block plus that count.
func renderGroups(result *InspectResult, schema *encoding.Schema, version byte, rows int64) {
	result.Layout = &InspectLayout{
		PulseFormatVersion:   int(version),
		PhysicalRecordStride: schema.RecordByteSize(),
		LogicalRecordStride:  schema.Logical().RecordByteSize(),
	}
	for g := range schema.Groups {
		spec := schema.GroupSpecOf(g)
		v := encoding.AssessGroup(schema, g, rows, encoding.DefaultDedupRatioFloor)
		verdict := encoding.GroupVerdictAdmitted
		// GrowsFile is the raw byte fact for every kind; the verdict is
		// the import gate's judgement, which never judges a constant
		// group or an empty cohort.
		grows := v.ByteDelta >= 0
		if low, _ := v.Finding(); low {
			verdict = encoding.GroupVerdictLowRatio
		}
		kind := schema.Groups[g].Kind.String()
		result.Groups = append(result.Groups, &InspectGroup{
			Group:           g,
			Label:           spec.Label(g),
			Kind:            kind,
			Key:             spec.Key,
			Members:         nonKeyMembers(spec),
			Fields:          spec.Members,
			EntryCount:      v.EntryCount,
			EntryWidth:      v.EntryWidth,
			DictionaryBytes: int64(len(schema.Groups[g].Entries)),
			MemberRowBytes:  v.MemberRowBytes,
			IndexWidth:      v.IndexWidth,
			Ratio:           v.Ratio,
			BreakEvenRatio:  v.BreakEvenRatio,
			RatioFloor:      v.RatioFloor,
			ByteDelta:       v.ByteDelta,
			GrowsFile:       grows,
			Verdict:         verdict,
		})
		for _, m := range schema.Groups[g].Members {
			if m.Field >= 0 && m.Field < len(result.Fields) {
				result.Fields[m.Field].Group = &InspectFieldGroup{Group: g, Key: m.Key, Kind: kind}
			}
		}
	}
}

// nonKeyMembers is spec.Members minus spec.Key, in member order; with no
// key every member is returned.
func nonKeyMembers(spec encoding.GroupSpec) []string {
	if len(spec.Key) == 0 {
		return spec.Members
	}
	key := make(map[string]bool, len(spec.Key))
	for _, k := range spec.Key {
		key[k] = true
	}
	out := []string{}
	for _, m := range spec.Members {
		if !key[m] {
			out = append(out, m)
		}
	}
	return out
}
