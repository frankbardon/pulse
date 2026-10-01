package descriptor

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
