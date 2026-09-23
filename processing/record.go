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
	// fit in float64. Keyed by field name; absent for plain numeric
	// fields. Three value shapes live here: encoding.Decimal128 for
	// decimal128 columns, a plain uint64 bitmask for the narrow set rungs
	// (set_u8..set_u64, whose storage is deliberately unchanged), and an
	// encoding.SetMask for the wide rungs (set_u128, set_u256). Read set
	// fields through SetMaskValue, never by type-asserting this map: the
	// assertion that fails is indistinguishable from a null field.
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
// that do not fit in float64 (decimal128, set bitmasks).
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
// Wide values are populated for decimal128 and set-typed fields; see the
// wide field's doc comment for the shapes. Set fields have a dedicated
// accessor — SetMaskValue — and callers should prefer it.
func (r *Record) WideValue(name string) (any, bool) {
	if r.nulls[name] {
		return nil, false
	}
	v, ok := r.wide[name]
	return v, ok
}

// SetWide assigns a typed wide value to a field. Used by readers and
// feature operators that produce non-float values (decimal128, set
// bitmasks).
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
//
// A SET-TYPED FIELD IS NEVER NUMERIC HERE and always reports false,
// even though the decoder does write a float64 echo of the mask into
// the values map. The echo is not a number the caller can use: from
// set_u64 upward it loses bits to float64's 53-bit mantissa, and for a
// wide rung (set_u128 / set_u256) it is only the LOW 64 BITS of the
// mask. Both failures are silent — a set of 206 members read through
// this accessor returns a perfectly plausible float that is simply the
// wrong selection. Refusing is the only signal available: the accessor
// has no error channel, so it answers the honest question ("is there a
// number here?") with no, exactly as StringValue already answers for a
// non-categorical field. Read set fields through SetMaskValue.
//
// The guard costs nothing on the common path: a record with no wide
// fields skips the map probe entirely, and a decimal128 wide value
// still returns its float echo because setMaskFromWideValue rejects it.
func (r *Record) NumericValue(name string) (float64, bool) {
	if r.nulls[name] {
		return 0, false
	}
	if len(r.wide) > 0 {
		if wv, present := r.wide[name]; present {
			if _, isSet := setMaskFromWideValue(wv); isSet {
				return 0, false
			}
		}
	}
	v, ok := r.values[name]
	return v, ok
}

// IsNull reports whether the named field is null on this record. A field
// is null when explicitly marked via SetNull / SetNullField (the on-wire
// bitmap signal during decode), or when the field is absent from both
// the values map and the wide map. Used by the orchestrator's filter
// pass to track the n_null_input universal-floor counter per
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

// setMaskFromWideValue lifts a value out of the wide map into the shared
// SetMask type. Narrow rungs (set_u8..set_u64) store a plain uint64 so
// per-record memory for existing cohorts does not grow; the wide rungs
// (set_u128, set_u256) store an encoding.SetMask directly. Anything else
// is not a set value and reports false.
func setMaskFromWideValue(v any) (encoding.SetMask, bool) {
	switch m := v.(type) {
	case encoding.SetMask:
		return m, true
	case uint64:
		return encoding.SetMaskFromUint64(m), true
	}
	return encoding.SetMask{}, false
}

// SetMaskValue returns the membership bitmask for a set-typed field. It
// is the single accessor for set fields at every rung — narrow
// (set_u8..set_u64) and wide (set_u128, set_u256) alike. Bit i is set
// when dictionary entry i is selected. Returns (zero mask, false) when
// the field is null, missing, or does not carry a set value.
//
// Narrow storage is unchanged: a narrow rung still holds a uint64 in the
// wide map, and SetMaskValue widens it on read through
// encoding.SetMaskFromUint64 — register work that allocates nothing.
// Wide rungs are returned as stored. encoding.SetMask is a fixed-array
// VALUE with no aliasing, so the returned mask is safe to retain past
// the record; there is deliberately no reuse or invalidation contract.
//
// This REPLACES the former SetValue (uint64, bool). That accessor
// reported a failed `v.(uint64)` type assertion with the same (0, false)
// it used for a null field, so a wide mask reaching it read as "selected
// nothing" — wrong answers, no error, green tests. There is no uint64
// set accessor any more, exported or otherwise, by design: the
// temporary narrowing bridge every set operator leaned on during the
// widening (narrowSetValue) is gone now that no operator holds uint64
// set state. Anything that needs the low word asks the returned
// SetMask for it and handles the !fits case itself.
//
// Callers that need exact-bit semantics MUST NOT read set fields via
// NumericValue: the float64 echo loses high bits from set_u64 upward.
func (r *Record) SetMaskValue(name string) (encoding.SetMask, bool) {
	if r.nulls[name] {
		return encoding.SetMask{}, false
	}
	if r.wide == nil {
		return encoding.SetMask{}, false
	}
	v, ok := r.wide[name]
	if !ok {
		return encoding.SetMask{}, false
	}
	return setMaskFromWideValue(v)
}

// SetLabels decodes a set-typed field's bitmask into a slice of resolved
// dictionary labels in ascending bit order (i.e. dictionary insertion
// order, which is also the bit-assignment order). Returns (nil, false)
// when the field is null, missing, not a set type, or has no dictionary.
// An empty mask returns ([]string{}, true) — the field is present but
// the respondent picked nothing.
//
// The walk is bounded by the DICTIONARY size, not by 64 and not by the
// mask width, so a wide rung resolves every selected member and a bit
// past the dictionary (corrupt or mid-remap payload) is skipped rather
// than resolved or fatal. encoding.SetMask.Labels is the single
// implementation.
func (r *Record) SetLabels(name string) ([]string, bool) {
	mask, ok := r.SetMaskValue(name)
	if !ok {
		return nil, false
	}
	f := r.schema.Field(name)
	if f == nil || !f.Type.IsSet() || f.Dictionary == nil {
		return nil, false
	}
	return mask.Labels(f.Dictionary), true
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
			if mask, ok := setMaskFromWideValue(v); ok {
				out[k] = mask.Labels(f.Dictionary)
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
// value (decimal128, or a set bitmask as uint64 for the narrow rungs /
// encoding.SetMask for the wide ones) without invalidating the
// AllValues cache.
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
