package processing

import (
	"github.com/frankbardon/pulse/encoding"
)

// Record represents a single data row with field accessors.
// It provides both numeric and string access for processing operations.
type Record struct {
	schema *encoding.Schema
	values map[string]float64
	nulls  map[string]bool

	// wide carries typed values for fields whose representation does not
	// fit in float64: decimal128. Keyed by field name; absent for narrow
	// fields. Values are encoding.Decimal128.
	wide map[string]any

	// allValuesCache memoizes the result of AllValues(). It is populated on
	// the first call and reused on subsequent calls. Callers must not mutate
	// the returned map; mutations would persist across calls. Cache is
	// invalidated when the underlying values map changes (see attribute
	// injection in processor.go).
	allValuesCache map[string]any
}

// NewRecord creates a record with the given schema and field values.
func NewRecord(schema *encoding.Schema, values map[string]float64) *Record {
	return &Record{
		schema: schema,
		values: values,
		nulls:  make(map[string]bool),
		wide:   make(map[string]any),
	}
}

// NewRecordWithNulls creates a record with explicit null tracking.
func NewRecordWithNulls(schema *encoding.Schema, values map[string]float64, nulls map[string]bool) *Record {
	if nulls == nil {
		nulls = make(map[string]bool)
	}
	return &Record{
		schema: schema,
		values: values,
		nulls:  nulls,
		wide:   make(map[string]any),
	}
}

// NewRecordWithWide creates a record with typed wide values for fields
// that do not fit in float64 (decimal128).
func NewRecordWithWide(schema *encoding.Schema, values map[string]float64, nulls map[string]bool, wide map[string]any) *Record {
	if nulls == nil {
		nulls = make(map[string]bool)
	}
	if wide == nil {
		wide = make(map[string]any)
	}
	return &Record{
		schema: schema,
		values: values,
		nulls:  nulls,
		wide:   wide,
	}
}

// WideValue returns the typed wide value for the named field, if present.
// Wide values are populated for decimal128 fields.
func (r *Record) WideValue(name string) (any, bool) {
	if r.nulls[name] {
		return nil, false
	}
	v, ok := r.wide[name]
	return v, ok
}

// SetWide assigns a typed wide value to a field. Used by readers and
// feature operators that produce non-float values (decimal128).
func (r *Record) SetWide(name string, v any) {
	if r.wide == nil {
		r.wide = make(map[string]any)
	}
	r.wide[name] = v
	if r.nulls[name] {
		delete(r.nulls, name)
	}
	r.invalidateAllValuesCache()
}

// NumericValue returns the numeric value for the named field.
// Returns the value and true if present and non-null, or 0 and false if null or missing.
func (r *Record) NumericValue(name string) (float64, bool) {
	if r.nulls[name] {
		return 0, false
	}
	v, ok := r.values[name]
	return v, ok
}

// IsNull reports whether the named field is null on this record. A field
// is null when explicitly marked via SetNull / SetNullField (the on-wire
// bitmap signal during decode), or when the field is absent from both
// the values map and the wide map. Used by the orchestrator's filter
// pass (E2-S9) to track the n_null_input universal-floor counter per
// FiltererComponents slot without coupling each filterer to a particular
// null-detection path.
//
// FILTER_EXPRESSION carries no Field and callers MUST skip the IsNull
// check for that filterer kind — expression filters do not have a
// single source field whose null state would be meaningful.
func (r *Record) IsNull(name string) bool {
	if name == "" {
		return false
	}
	if r.nulls[name] {
		return true
	}
	if _, ok := r.values[name]; ok {
		return false
	}
	if r.wide != nil {
		if _, ok := r.wide[name]; ok {
			return false
		}
	}
	return true
}

// SetValue returns the raw bitmask for a set-typed field. Bit i is set
// when dictionary entry i is selected. Returns (0, false) when the field
// is null, missing, or not a set type.
//
// The mask is sourced from the typed wide map so bit-level precision is
// preserved across all four width tiers (set_u8/u16/u32/u64) — callers
// that need exact-bit semantics MUST NOT read set fields via NumericValue
// because the float64 echo loses high bits for set_u64.
func (r *Record) SetValue(name string) (uint64, bool) {
	if r.nulls[name] {
		return 0, false
	}
	if r.wide == nil {
		return 0, false
	}
	v, ok := r.wide[name]
	if !ok {
		return 0, false
	}
	mask, ok := v.(uint64)
	if !ok {
		return 0, false
	}
	return mask, true
}

// SetLabels decodes a set-typed field's bitmask into a slice of resolved
// dictionary labels in bit order (i.e. dictionary insertion order, which
// is also the bit-assignment order). Returns (nil, false) when the field
// is null, missing, not a set type, or has no dictionary. Empty mask
// returns ([]string{}, true) — the field is present but the respondent
// picked nothing.
func (r *Record) SetLabels(name string) ([]string, bool) {
	mask, ok := r.SetValue(name)
	if !ok {
		return nil, false
	}
	f := r.schema.Field(name)
	if f == nil || !f.Type.IsSet() || f.Dictionary == nil {
		return nil, false
	}
	return resolveSetLabels(mask, f.Dictionary), true
}

// resolveSetLabels walks the bits of a set mask in ascending bit order
// and returns the dictionary labels for set bits. Bit i ↔ dict entry i.
// Bits beyond dict.Len() are ignored (the writer must not set them; the
// reader stays defensive against a corrupt or remapped payload).
func resolveSetLabels(mask uint64, dict *encoding.Dictionary) []string {
	if mask == 0 || dict == nil {
		return []string{}
	}
	dictLen := dict.Count()
	out := make([]string, 0, 8)
	for i := 0; i < dictLen && i < 64; i++ {
		if mask&(uint64(1)<<uint(i)) == 0 {
			continue
		}
		label := dict.Resolve(uint32(i))
		if label == "" {
			continue
		}
		out = append(out, label)
	}
	return out
}

// StringValue returns the resolved string value for categorical fields.
// For non-categorical fields, returns the empty string and false.
func (r *Record) StringValue(name string) (string, bool) {
	if r.nulls[name] {
		return "", false
	}

	f := r.schema.Field(name)
	if f == nil {
		return "", false
	}

	if !f.Type.IsCategorical() || f.Dictionary == nil {
		return "", false
	}

	v, ok := r.values[name]
	if !ok {
		return "", false
	}

	resolved := f.Dictionary.Resolve(uint32(v))
	if resolved == "" {
		return "", false
	}
	return resolved, true
}

// Schema returns the record's schema.
func (r *Record) Schema() *encoding.Schema {
	return r.schema
}

// AllValues returns all field values as a map (for expression evaluation).
//
// The returned map is cached on the Record after the first call and reused on
// subsequent calls; callers MUST NOT mutate it. If a caller mutates the
// underlying values map directly (e.g., the processor injecting computed
// attributes), it must call invalidateAllValuesCache to discard the cache.
func (r *Record) AllValues() map[string]any {
	if r.allValuesCache != nil {
		return r.allValuesCache
	}
	out := make(map[string]any, len(r.values)+len(r.wide))
	for k, v := range r.values {
		if r.nulls[k] {
			continue
		}
		f := r.schema.Field(k)
		if f != nil && f.Type.IsCategorical() && f.Dictionary != nil {
			out[k] = f.Dictionary.Resolve(uint32(v))
		} else {
			out[k] = v
		}
	}
	for k, v := range r.wide {
		if r.nulls[k] {
			continue
		}
		f := r.schema.Field(k)
		if f != nil && f.Type.IsSet() && f.Dictionary != nil {
			if mask, ok := v.(uint64); ok {
				out[k] = resolveSetLabels(mask, f.Dictionary)
				continue
			}
		}
		out[k] = v
	}
	r.allValuesCache = out
	return out
}

// invalidateAllValuesCache discards the cached result of AllValues. Call this
// after directly mutating the Record's values map so the next AllValues call
// reflects the new state.
func (r *Record) invalidateAllValuesCache() {
	r.allValuesCache = nil
}

// Set assigns a numeric value to the named field on this record. It clears
// any prior null marker and invalidates the AllValues cache. Used by
// pre-filter feature operators to inject derived columns into the record
// stream so downstream stages (filters, attributes, groupers, aggregators)
// can reference them by label.
func (r *Record) Set(name string, value float64) {
	r.values[name] = value
	if r.nulls[name] {
		delete(r.nulls, name)
	}
	r.invalidateAllValuesCache()
}

// SetNull marks the named field as null. Used by feature operators to
// propagate input nulls into the derived column.
func (r *Record) SetNull(name string) {
	r.nulls[name] = true
	delete(r.values, name)
	r.invalidateAllValuesCache()
}

// SetNumeric implements encoding.ReusableRecord. Assigns a numeric value
// without touching the null marker or invalidating the AllValues cache.
// Intended only for the streaming reuse path that calls ClearForRow before
// each row.
func (r *Record) SetNumeric(name string, value float64) {
	r.values[name] = value
}

// SetNullField implements encoding.ReusableRecord. Marks a field as null
// in the reuse path; does not invalidate the AllValues cache (reuse path
// resets the cache once per row via ClearForRow).
func (r *Record) SetNullField(name string) {
	r.nulls[name] = true
}

// SetWideField implements encoding.ReusableRecord. Stores a typed wide
// value (decimal128) without invalidating the AllValues cache.
func (r *Record) SetWideField(name string, v any) {
	if r.wide == nil {
		r.wide = make(map[string]any)
	}
	r.wide[name] = v
}

// ClearForRow implements encoding.ReusableRecord. Resets per-row state
// so the next ReadRecordReused call starts from a clean slate while
// keeping the underlying maps allocated.
//
// values is left intact because every field is overwritten on every row.
// nulls and wide are cleared because their entries are sparse.
func (r *Record) ClearForRow() {
	if len(r.nulls) > 0 {
		clear(r.nulls)
	}
	if len(r.wide) > 0 {
		clear(r.wide)
	}
	r.allValuesCache = nil
}

// NewReusableRecord constructs a Record whose internal maps are sized
// for the given schema and intended to be reused across many
// ReadRecordReused calls. Returns a Record that callers must NOT retain
// past the next iteration step.
func NewReusableRecord(schema *encoding.Schema) *Record {
	return &Record{
		schema: schema,
		values: make(map[string]float64, len(schema.Fields)),
		nulls:  make(map[string]bool),
		wide:   make(map[string]any),
	}
}

// RecordIterator provides sequential access to records.
type RecordIterator interface {
	// Next advances to the next record. Returns false when exhausted.
	Next() bool
	// Record returns the current record. Only valid after Next returns true.
	Record() *Record
	// Reset resets the iterator to the beginning.
	Reset()
}

// ReusableIterator is an optional interface implemented by iterators
// that can return the same Record pointer across Next() calls,
// refreshing its values/nulls/wide maps in place. Streaming consumers
// that consume each record inline (no slice retention) can opt in to
// drop the per-row map allocations.
//
// Callers MUST consume each record before invoking Next() again — the
// next call will overwrite the Record's contents.
type ReusableIterator interface {
	SetReuse(bool)
}

// EnableReuse opts the iterator into per-row Record reuse if it
// implements ReusableIterator. Safe no-op for iterators that do not
// support reuse (e.g. SliceIterator, whose records already exist as
// independent values).
func EnableReuse(iter RecordIterator) {
	if r, ok := iter.(ReusableIterator); ok {
		r.SetReuse(true)
	}
}

// SliceIterator implements RecordIterator over a slice of records.
type SliceIterator struct {
	records []*Record
	pos     int
}

// NewSliceIterator creates an iterator over the given records.
func NewSliceIterator(records []*Record) *SliceIterator {
	return &SliceIterator{
		records: records,
		pos:     -1,
	}
}

// Next advances to the next record.
func (it *SliceIterator) Next() bool {
	it.pos++
	return it.pos < len(it.records)
}

// Record returns the current record.
func (it *SliceIterator) Record() *Record {
	return it.records[it.pos]
}

// Reset resets the iterator to the beginning.
func (it *SliceIterator) Reset() {
	it.pos = -1
}
