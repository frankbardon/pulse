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
	}
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
	out := make(map[string]any, len(r.values))
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

// RecordIterator provides sequential access to records.
type RecordIterator interface {
	// Next advances to the next record. Returns false when exhausted.
	Next() bool
	// Record returns the current record. Only valid after Next returns true.
	Record() *Record
	// Reset resets the iterator to the beginning.
	Reset()
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
