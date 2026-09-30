package processing

import (
	"math"
	"math/bits"

	"github.com/frankbardon/pulse/encoding"
)

// Record represents a single data row with field accessors.
// It provides both numeric and string access for processing operations.
//
// # Storage
//
// A Record stores its row POSITIONALLY, in storage slots. Under the
// full layout a field's slot is its position in the schema (the index
// into Schema.Fields); under a projected layout (BindRecords) only the
// retained fields have slots, densely numbered. For slot i:
//
//   - vals[i] holds the field's numeric value (the float64 echo for
//     decimal128 and set_* fields);
//   - bits carries three presence planes of ceil(n/64) words each —
//     "has a value", "is null", "has a wide value" — so a field absent
//     from the record (projected out, never written) is distinguishable
//     from one present and zero, exactly as a missing map key was;
//   - typed wide values are stored UNBOXED, for wide-capable fields
//     only: an encoding.Decimal128 per decimal128 field in aux.decs, and
//     the mask words of every set field (one uint64 for set_u8..set_u64,
//     two for set_u128, four for set_u256) in the spare capacity of vals
//     past len(vals) (see wordAt). The reuse decoders hand set masks over
//     through encoding.TypedSetRecord, so decoding a set field allocates
//     nothing, and a set-bearing record needs no aux at all.
//
// Name → slot resolution goes through a recordLayout built once per
// binding and shared by every Record under it (record_layout.go), never
// per row and never per record.
//
// A name with no slot — an attribute label injected
// mid-pipeline, a feature or window output column, a key written on a
// synthetic test record, a schema field outside a projection — lands in
// the aux overflow maps (ovVals,
// ovNulls, ovWide), which are allocated only when such a write happens.
// A record that only ever holds schema fields allocates no map at all.
// The routing is a pure function of (schema, name), so a given name is
// always stored on the same side.
//
// Every public accessor keeps the semantics the former
// map[string]float64 / map[string]bool / map[string]any triple had,
// including the asymmetries (SetNull drops the value but not a wide
// value; SetNumeric does not clear a null mark; ClearForRow keeps
// values and clears null and wide marks). One deliberate change: the
// decoder's null signal (SetNullField / SetNullFieldAt) drops the wide
// value, as the map DECODER's delete always did.
type Record struct {
	schema *encoding.Schema
	layout *recordLayout

	// vals is indexed by storage slot; len(vals) is the layout's slot
	// count. Its CAPACITY is len + recordLayout.nWords: the tail past
	// len(vals) holds set-mask words bit-for-bit (math.Float64bits /
	// Float64frombits are exact reinterpretations, never arithmetic), so
	// set masks share the value allocation instead of paying their own
	// slice header and allocation. Every range over vals sees only the
	// slots.
	vals []float64
	// bits holds three planes of recordWords(len(vals)) words each:
	// planeHas, planeNull, planeWide. Bit i of a plane is slot i.
	bits []uint64

	// aux carries the sparse parts: wide slots and the off-schema
	// overflow maps. nil until the first write that needs it.
	aux *recordAux

	// allValuesCache memoizes the result of AllValues(). It is populated on
	// the first call and reused on subsequent calls. Callers must not mutate
	// the returned map; mutations would persist across calls. Cache is
	// invalidated by every mutator that is not a reuse-path decode write
	// (see attribute injection in processor.go).
	allValuesCache map[string]any

	// runOwner is the token of the reader whose index-keyed decode
	// writes are, field for field, the record's whole schema-field
	// state — the precondition for run-skip (encoding.RunSkipRecord).
	// Zero means "no such reader": set by ClearForRow and by every
	// schema-field mutation that is not a reuse-path decode write
	// (resetRun), so the next BeginRunRow forces a full repopulate.
	runOwner uint64
}

// recordAux is the sparse side of a Record.
type recordAux struct {
	// decs is the typed decimal storage, addressed by
	// recordLayout.wideOff; presence is planeWide. (Set masks live in the
	// tail of Record.vals.) Three value shapes are observable through
	// WideValue, exactly as the former map[string]any stored them:
	// encoding.Decimal128 for decimal128 columns, a plain uint64 bitmask
	// for the narrow set rungs set_u8..set_u64 (one word), and an
	// encoding.SetMask for the wide rungs set_u128 / set_u256 (two or four
	// words). Read set fields through SetMaskValue, never by
	// type-asserting WideValue: the assertion that fails is
	// indistinguishable from a null field.
	decs []encoding.Decimal128

	// Overflow for names with no slot. ovWide additionally holds a wide
	// value written to a slot that cannot store it typed and exactly — a
	// field type with no wide storage, a value of another dynamic type
	// than the slot's kind (a SetMask on a narrow rung, say), or a mask
	// with bits past the rung — with that slot's planeWide bit cleared.
	// Same semantics the former per-record maps had.
	ovVals  map[string]float64
	ovNulls map[string]bool
	ovWide  map[string]any
}

const (
	planeHas = iota
	planeNull
	planeWide
	planeCount
)

func recordWords(n int) int { return (n + 63) >> 6 }

// newPositionalRecord allocates an empty positional record bound to
// schema's shared layout: every field absent, no overflow.
func newPositionalRecord(schema *encoding.Schema) *Record {
	return newRecordWithLayout(schema, layoutFor(schema))
}

func newRecordWithLayout(schema *encoding.Schema, l *recordLayout) *Record {
	r := &Record{schema: schema, layout: l}
	if l.n > 0 {
		r.vals = make([]float64, l.n, l.n+l.nWords)
		r.bits = make([]uint64, planeCount*recordWords(l.n))
	}
	return r
}

// NewRecord creates a record with the given schema and field values.
//
// This is the SLOW PATH: the map is converted to positional storage on
// entry (one name lookup per entry) and is not retained, so mutating it
// afterwards does not affect the record. Hot paths build an empty record
// with NewReusableRecord and populate it by position.
func NewRecord(schema *encoding.Schema, values map[string]float64) *Record {
	r := newPositionalRecord(schema)
	r.loadMaps(values, nil, nil)
	return r
}

// NewRecordWithNulls creates a record with explicit null tracking.
// Slow path; see NewRecord. A nulls entry mapped to false is not a null.
func NewRecordWithNulls(schema *encoding.Schema, values map[string]float64, nulls map[string]bool) *Record {
	r := newPositionalRecord(schema)
	r.loadMaps(values, nulls, nil)
	return r
}

// NewRecordWithWide creates a record with typed wide values for fields
// that do not fit in float64 (decimal128, set bitmasks). Slow path; see
// NewRecord.
func NewRecordWithWide(schema *encoding.Schema, values map[string]float64, nulls map[string]bool, wide map[string]any) *Record {
	r := newPositionalRecord(schema)
	r.loadMaps(values, nulls, wide)
	return r
}

// loadMaps converts the map-taking constructors' inputs. The three
// stores are independent, so the order is immaterial.
func (r *Record) loadMaps(values map[string]float64, nulls map[string]bool, wide map[string]any) {
	for k, v := range values {
		r.putValue(k, v)
	}
	for k, isNull := range nulls {
		if isNull {
			r.markNull(k)
		}
	}
	for k, v := range wide {
		r.putWide(k, v)
	}
}

// ---- positional primitives -------------------------------------------

func (r *Record) pos(name string) int {
	if r.layout == nil {
		return -1
	}
	return r.layout.pos(name)
}

func (r *Record) test(plane, i int) bool {
	return r.bits[plane*recordWords(len(r.vals))+i>>6]&(1<<uint(i&63)) != 0
}

func (r *Record) setBit(plane, i int) {
	r.bits[plane*recordWords(len(r.vals))+i>>6] |= 1 << uint(i&63)
}

func (r *Record) clearBit(plane, i int) {
	r.bits[plane*recordWords(len(r.vals))+i>>6] &^= 1 << uint(i&63)
}

// resetRun withdraws the record from run-skip: a schema field was
// mutated outside the reuse decoders' index-keyed writes, so the next
// row must be repopulated in full.
func (r *Record) resetRun() { r.runOwner = 0 }

func (r *Record) auxOrNew() *recordAux {
	if r.aux == nil {
		r.aux = &recordAux{}
	}
	return r.aux
}

// fieldAt returns the schema field stored in slot i.
func (r *Record) fieldAt(i int) *encoding.Field {
	return &r.schema.Fields[r.layout.field(i)]
}

// wideAt returns the wide value of slot i, wherever it lives. A typed
// value is boxed on the way out; only WideValue, AllValues and the test
// helpers read it this way — SetMaskValue and NumericValue never box.
func (r *Record) wideAt(i int) (any, bool) {
	if r.test(planeWide, i) {
		return r.boxWideAt(i), true
	}
	return r.ovWideOf(i)
}

// ovWideOf returns slot i's wide value from the overflow map.
func (r *Record) ovWideOf(i int) (any, bool) {
	if r.aux == nil || len(r.aux.ovWide) == 0 {
		return nil, false
	}
	v, ok := r.aux.ovWide[r.fieldAt(i).Name]
	return v, ok
}

// hasWideAt reports whether slot i carries a wide value, without boxing.
func (r *Record) hasWideAt(i int) bool {
	if r.test(planeWide, i) {
		return true
	}
	_, ok := r.ovWideOf(i)
	return ok
}

// boxWideAt boxes slot i's typed wide value (planeWide must be set) in
// the dynamic type the former map stored: Decimal128, uint64 or SetMask.
func (r *Record) boxWideAt(i int) any {
	off := r.layout.wideOff[i]
	switch r.layout.wideKind[i] {
	case wideDecimal:
		return r.aux.decs[off]
	case wideNarrowSet:
		return r.wordAt(int(off))
	default:
		return r.maskWordsAt(i)
	}
}

// maskWordsAt reads a wide-rung slot's words back into a SetMask.
func (r *Record) maskWordsAt(i int) encoding.SetMask {
	off := int(r.layout.wideOff[i])
	var w [encoding.SetMaskWords]uint64
	tail := r.wordTail()[off:]
	for k := range r.fieldAt(i).Type.ByteSize() / 8 {
		w[k] = math.Float64bits(tail[k])
	}
	return encoding.SetMaskFromWords(w)
}

// setMaskAt returns slot i's set mask. isSet is false when the wide
// value is not a set value (a decimal); present is false when the slot
// carries no wide value at all.
func (r *Record) setMaskAt(i int) (m encoding.SetMask, isSet, present bool) {
	if r.test(planeWide, i) {
		switch r.layout.wideKind[i] {
		case wideNarrowSet:
			return encoding.SetMaskFromUint64(r.wordAt(int(r.layout.wideOff[i]))), true, true
		case wideWideSet:
			return r.maskWordsAt(i), true, true
		}
		return encoding.SetMask{}, false, true
	}
	v, ok := r.ovWideOf(i)
	if !ok {
		return encoding.SetMask{}, false, false
	}
	m, isSet = setMaskFromWideValue(v)
	return m, isSet, true
}

func (r *Record) decsOrNew() []encoding.Decimal128 {
	a := r.auxOrNew()
	if a.decs == nil {
		a.decs = make([]encoding.Decimal128, r.layout.nDecs)
	}
	return a.decs
}

// wordAt / setWordAt address set-mask word off in the tail of vals
// (past len(vals), within its capacity). Bit-exact: the word is
// reinterpreted, never converted.
func (r *Record) wordAt(off int) uint64 {
	return math.Float64bits(r.vals[:cap(r.vals)][len(r.vals)+off])
}

func (r *Record) setWordAt(off int, w uint64) {
	r.vals[:cap(r.vals)][len(r.vals)+off] = math.Float64frombits(w)
}

// wordTail is the whole set-word tail of vals, for multi-word access.
func (r *Record) wordTail() []float64 {
	return r.vals[len(r.vals):cap(r.vals)]
}

// typedWideStored marks slot i's typed value present and retires any
// overflow value the slot held before.
func (r *Record) typedWideStored(i int) {
	r.setBit(planeWide, i)
	if r.aux != nil && len(r.aux.ovWide) > 0 {
		delete(r.aux.ovWide, r.fieldAt(i).Name)
	}
}

// putNarrowAt stores a narrow-rung mask in slot i (kind wideNarrowSet).
func (r *Record) putNarrowAt(i int, m uint64) {
	r.setWordAt(int(r.layout.wideOff[i]), m)
	r.typedWideStored(i)
}

// putMaskAt stores a wide-rung mask in slot i (kind wideWideSet). It
// reports false, storing nothing, when m has bits past the slot's rung:
// truncating would silently drop selections, so the caller keeps such a
// value boxed instead.
func (r *Record) putMaskAt(i int, m encoding.SetMask) bool {
	n := r.fieldAt(i).Type.ByteSize() / 8
	w := m.Words()
	for k := n; k < encoding.SetMaskWords; k++ {
		if w[k] != 0 {
			return false
		}
	}
	tail := r.wordTail()[r.layout.wideOff[i]:]
	for k := range n {
		tail[k] = math.Float64frombits(w[k])
	}
	r.typedWideStored(i)
	return true
}

// putWideAt stores v as slot i's wide value: typed when the slot's kind
// holds v's dynamic type exactly, otherwise boxed in the overflow map
// with the typed presence bit cleared. Either way WideValue returns v
// with its dynamic type unchanged.
func (r *Record) putWideAt(i int, v any) {
	switch r.layout.wideKind[i] {
	case wideDecimal:
		if d, ok := v.(encoding.Decimal128); ok {
			r.decsOrNew()[r.layout.wideOff[i]] = d
			r.typedWideStored(i)
			return
		}
	case wideNarrowSet:
		if m, ok := v.(uint64); ok {
			r.putNarrowAt(i, m)
			return
		}
	case wideWideSet:
		if m, ok := v.(encoding.SetMask); ok && r.putMaskAt(i, m) {
			return
		}
	}
	// A schema field's wide value now lives in ovWide, which run-skip's
	// BeginRunRow clears every row; withdraw from run-skip so the next
	// row rewrites it.
	r.resetRun()
	r.clearBit(planeWide, i)
	a := r.auxOrNew()
	if a.ovWide == nil {
		a.ovWide = make(map[string]any)
	}
	a.ovWide[r.fieldAt(i).Name] = v
}

// dropWide removes a wide value by name, wherever it lives — the former
// `delete(r.wide, name)`.
func (r *Record) dropWide(name string) {
	if i := r.pos(name); i >= 0 {
		r.resetRun()
		r.clearBit(planeWide, i)
	}
	if r.aux != nil && len(r.aux.ovWide) > 0 {
		delete(r.aux.ovWide, name)
	}
}

// ---- name-keyed primitives (the former map operations) ---------------

// getValue is the former `v, ok := r.values[name]`.
func (r *Record) getValue(name string) (float64, bool) {
	if i := r.pos(name); i >= 0 {
		if r.test(planeHas, i) {
			return r.vals[i], true
		}
		return 0, false
	}
	if r.aux == nil {
		return 0, false
	}
	v, ok := r.aux.ovVals[name]
	return v, ok
}

// putValue is the former `r.values[name] = v`.
func (r *Record) putValue(name string, v float64) {
	if i := r.pos(name); i >= 0 {
		r.resetRun()
		r.vals[i] = v
		r.setBit(planeHas, i)
		return
	}
	a := r.auxOrNew()
	if a.ovVals == nil {
		a.ovVals = make(map[string]float64)
	}
	a.ovVals[name] = v
}

// dropValue is the former `delete(r.values, name)`.
func (r *Record) dropValue(name string) {
	if i := r.pos(name); i >= 0 {
		r.resetRun()
		r.vals[i] = 0
		r.clearBit(planeHas, i)
		return
	}
	if r.aux != nil {
		delete(r.aux.ovVals, name)
	}
}

// nullMarked is the former `r.nulls[name]`.
func (r *Record) nullMarked(name string) bool {
	if i := r.pos(name); i >= 0 {
		return r.test(planeNull, i)
	}
	return r.aux != nil && r.aux.ovNulls[name]
}

// markNull is the former `r.nulls[name] = true`.
func (r *Record) markNull(name string) {
	if i := r.pos(name); i >= 0 {
		r.resetRun()
		r.setBit(planeNull, i)
		return
	}
	a := r.auxOrNew()
	if a.ovNulls == nil {
		a.ovNulls = make(map[string]bool)
	}
	a.ovNulls[name] = true
}

// unmarkNull is the former `delete(r.nulls, name)`.
func (r *Record) unmarkNull(name string) {
	if i := r.pos(name); i >= 0 {
		r.resetRun()
		r.clearBit(planeNull, i)
		return
	}
	if r.aux != nil {
		delete(r.aux.ovNulls, name)
	}
}

// getWide is the former `v, ok := r.wide[name]`.
func (r *Record) getWide(name string) (any, bool) {
	if i := r.pos(name); i >= 0 {
		return r.wideAt(i)
	}
	if r.aux == nil {
		return nil, false
	}
	v, ok := r.aux.ovWide[name]
	return v, ok
}

// putWide is the former `r.wide[name] = v`.
func (r *Record) putWide(name string, v any) {
	if i := r.pos(name); i >= 0 {
		r.resetRun()
		r.putWideAt(i, v)
		return
	}
	a := r.auxOrNew()
	if a.ovWide == nil {
		a.ovWide = make(map[string]any)
	}
	a.ovWide[name] = v
}

// rawValue returns the stored numeric value with none of NumericValue's
// guards (no null check, no set refusal) — the former direct
// `r.values[name]` read. For in-package callers that reason about the
// raw echo themselves (join keys).
func (r *Record) rawValue(name string) (float64, bool) {
	return r.getValue(name)
}

// injectValue writes a value without touching the null mark and
// invalidates the AllValues cache — the former
// `r.values[label] = v; r.invalidateAllValuesCache()` attribute
// injection in processor.go.
func (r *Record) injectValue(name string, v float64) {
	r.putValue(name, v)
	r.invalidateAllValuesCache()
}

// ---- public accessors -------------------------------------------------

// WideValue returns the typed wide value for the named field, if present.
// Wide values are populated for decimal128 and set-typed fields; see
// recordAux.wide for the shapes. Set fields have a dedicated
// accessor — SetMaskValue — and callers should prefer it.
func (r *Record) WideValue(name string) (any, bool) {
	if r.nullMarked(name) {
		return nil, false
	}
	return r.getWide(name)
}

// SetWide assigns a typed wide value to a field. Used by readers and
// feature operators that produce non-float values (decimal128, set
// bitmasks).
func (r *Record) SetWide(name string, v any) {
	r.putWide(name, v)
	r.unmarkNull(name)
	r.invalidateAllValuesCache()
}

// NumericValue returns the numeric value for the named field.
// Returns the value and true if present and non-null, or 0 and false if null or missing.
//
// A SET-TYPED FIELD IS NEVER NUMERIC HERE and always reports false,
// even though the decoder does write a float64 echo of the mask into
// the record. The echo is not a number the caller can use: from
// set_u64 upward it loses bits to float64's 53-bit mantissa, and for a
// wide rung (set_u128 / set_u256) it is only the LOW 64 BITS of the
// mask. Both failures are silent — a set of 206 members read through
// this accessor returns a perfectly plausible float that is simply the
// wrong selection. Refusing is the only signal available: the accessor
// has no error channel, so it answers the honest question ("is there a
// number here?") with no, exactly as StringValue already answers for a
// non-categorical field. Read set fields through SetMaskValue.
//
// The refusal keys off the stored wide VALUE, not the schema type: a
// decimal128 wide value still returns its float echo because
// setMaskFromWideValue rejects it. The guard costs one bit test on the
// common path.
func (r *Record) NumericValue(name string) (float64, bool) {
	if i := r.pos(name); i >= 0 {
		if r.test(planeNull, i) {
			return 0, false
		}
		if _, isSet, _ := r.setMaskAt(i); isSet {
			return 0, false
		}
		if r.test(planeHas, i) {
			return r.vals[i], true
		}
		return 0, false
	}
	if r.aux == nil {
		return 0, false
	}
	if r.aux.ovNulls[name] {
		return 0, false
	}
	if wv, present := r.aux.ovWide[name]; present {
		if _, isSet := setMaskFromWideValue(wv); isSet {
			return 0, false
		}
	}
	v, ok := r.aux.ovVals[name]
	return v, ok
}

// IsNull reports whether the named field is null on this record. A field
// is null when explicitly marked via SetNull / SetNullField (the on-wire
// bitmap signal during decode), or when the field carries neither a
// value nor a wide value. Used by the orchestrator's filter pass to
// track the n_null_input universal-floor counter per FiltererComponents
// slot without coupling each filterer to a particular null-detection
// path.
//
// FILTER_EXPRESSION carries no Field and callers MUST skip the IsNull
// check for that filterer kind — expression filters do not have a
// single source field whose null state would be meaningful.
func (r *Record) IsNull(name string) bool {
	if name == "" {
		return false
	}
	if r.nullMarked(name) {
		return true
	}
	if _, ok := r.getValue(name); ok {
		return false
	}
	if i := r.pos(name); i >= 0 {
		return !r.hasWideAt(i)
	}
	if _, ok := r.getWide(name); ok {
		return false
	}
	return true
}

// setMaskFromWideValue lifts a stored wide value into the shared
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
// Narrow storage is unchanged: a narrow rung still holds a uint64 wide
// value, and SetMaskValue widens it on read through
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
	if r.nullMarked(name) {
		return encoding.SetMask{}, false
	}
	if i := r.pos(name); i >= 0 {
		m, isSet, _ := r.setMaskAt(i)
		return m, isSet
	}
	v, ok := r.getWide(name)
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
	i := r.pos(name)
	if i < 0 {
		return r.stringValueOverflow(name)
	}
	if r.test(planeNull, i) {
		return "", false
	}
	f := r.fieldAt(i)
	if !f.Type.IsCategorical() || f.Dictionary == nil {
		return "", false
	}
	if !r.test(planeHas, i) {
		return "", false
	}
	resolved := f.Dictionary.Resolve(uint32(r.vals[i]))
	if resolved == "" {
		return "", false
	}
	return resolved, true
}

// stringValueOverflow is StringValue for a name with no slot. Such a
// name can still be a schema field (one outside a projection written by
// name), so the schema is consulted exactly as the map-backed form did.
func (r *Record) stringValueOverflow(name string) (string, bool) {
	if r.aux == nil || r.aux.ovNulls[name] || r.schema == nil {
		return "", false
	}
	f := r.schema.Field(name)
	if f == nil || !f.Type.IsCategorical() || f.Dictionary == nil {
		return "", false
	}
	v, ok := r.aux.ovVals[name]
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
// subsequent calls; callers MUST NOT mutate it. Every non-reuse mutator
// (Set, SetNull, SetWide, attribute injection) discards the cache; the
// reuse-path decode writes rely on ClearForRow discarding it once per row.
//
// Content: every non-null field carrying a value (categoricals resolved
// to their dictionary label), then every non-null field carrying a wide
// value (set fields as their label slice), the wide entry winning where
// a field has both.
func (r *Record) AllValues() map[string]any {
	if r.allValuesCache != nil {
		return r.allValuesCache
	}
	out := make(map[string]any, r.valueCountHint())
	for i := range r.vals {
		if !r.test(planeHas, i) || r.test(planeNull, i) {
			continue
		}
		f := r.fieldAt(i)
		v := r.vals[i]
		if f.Type.IsCategorical() && f.Dictionary != nil {
			out[f.Name] = f.Dictionary.Resolve(uint32(v))
		} else {
			out[f.Name] = v
		}
	}
	if r.aux != nil {
		for k, v := range r.aux.ovVals {
			if r.aux.ovNulls[k] {
				continue
			}
			// An overflow name can be a schema field outside a
			// projection; resolve it exactly as the map-backed form did.
			var f *encoding.Field
			if r.schema != nil {
				f = r.schema.Field(k)
			}
			if f != nil && f.Type.IsCategorical() && f.Dictionary != nil {
				out[k] = f.Dictionary.Resolve(uint32(v))
			} else {
				out[k] = v
			}
		}
	}
	for i := range r.vals {
		if !r.test(planeWide, i) || r.test(planeNull, i) {
			continue
		}
		f := r.fieldAt(i)
		out[f.Name] = allValuesWide(f, r.boxWideAt(i))
	}
	if r.aux != nil {
		for k, v := range r.aux.ovWide {
			if r.nullMarked(k) {
				continue
			}
			var f *encoding.Field
			if r.schema != nil {
				f = r.schema.Field(k)
			}
			out[k] = allValuesWide(f, v)
		}
	}
	r.allValuesCache = out
	return out
}

// allValuesWide renders one wide value for AllValues: a set field with a
// dictionary becomes its label slice, anything else passes through.
func allValuesWide(f *encoding.Field, v any) any {
	if f != nil && f.Type.IsSet() && f.Dictionary != nil {
		if mask, ok := setMaskFromWideValue(v); ok {
			return mask.Labels(f.Dictionary)
		}
	}
	return v
}

// valueCountHint sizes the AllValues map: present values plus wide
// values, the same hint the map-backed form used.
func (r *Record) valueCountHint() int {
	n := 0
	if len(r.vals) > 0 {
		w := recordWords(len(r.vals))
		for _, word := range r.bits[planeHas*w : planeHas*w+w] {
			n += bits.OnesCount64(word)
		}
		for _, word := range r.bits[planeWide*w : planeWide*w+w] {
			n += bits.OnesCount64(word)
		}
	}
	if r.aux != nil {
		n += len(r.aux.ovVals) + len(r.aux.ovWide)
	}
	return n
}

// invalidateAllValuesCache discards the cached result of AllValues. Call this
// after mutating the Record outside the public mutators so the next
// AllValues call reflects the new state.
func (r *Record) invalidateAllValuesCache() {
	r.allValuesCache = nil
}

// Set assigns a numeric value to the named field on this record. It clears
// any prior null marker and invalidates the AllValues cache. Used by
// pre-filter feature operators to inject derived columns into the record
// stream so downstream stages (filters, attributes, groupers, aggregators)
// can reference them by label.
func (r *Record) Set(name string, value float64) {
	r.putValue(name, value)
	r.unmarkNull(name)
	r.invalidateAllValuesCache()
}

// SetNull marks the named field as null and drops its value (a wide
// value, if any, is left in place and masked by the null mark). Used by
// feature operators to propagate input nulls into the derived column.
func (r *Record) SetNull(name string) {
	r.markNull(name)
	r.dropValue(name)
	r.invalidateAllValuesCache()
}

// SetNumeric implements encoding.ReusableRecord. Assigns a numeric value
// without touching the null marker or invalidating the AllValues cache.
// Intended only for the streaming reuse path that calls ClearForRow before
// each row.
func (r *Record) SetNumeric(name string, value float64) {
	r.putValue(name, value)
}

// SetNullField implements encoding.ReusableRecord. Marks a field as null
// in the reuse path and drops its wide value; does not invalidate the
// AllValues cache (reuse path resets the cache once per row via
// ClearForRow).
//
// Dropping the wide value mirrors the map decoder, which deletes the
// wide entry of a field the bitmap reports null. Every accessor already
// hides a wide value behind the null mark, but a later Set(name, v) —
// which clears the mark — would otherwise resurface the decoded mask or
// decimal of a null field (and NumericValue would then refuse v as a set
// value). Unlike SetNull, this is a DECODE signal: the field has no
// value on this row, typed or not.
func (r *Record) SetNullField(name string) {
	r.markNull(name)
	r.dropWide(name)
}

// SetWideField implements encoding.ReusableRecord. Stores a typed wide
// value (decimal128, or a set bitmask as uint64 for the narrow rungs /
// encoding.SetMask for the wide ones) without invalidating the
// AllValues cache.
func (r *Record) SetWideField(name string, v any) {
	r.putWide(name, v)
}

// Compile-time proof that *Record satisfies both reuse-decoder contracts.
// Because it implements IndexedReusableRecord, the encoding reuse
// decoders drive the index-keyed methods below and never the name-keyed
// ones above.
var (
	_ encoding.ReusableRecord        = (*Record)(nil)
	_ encoding.IndexedReusableRecord = (*Record)(nil)
	_ encoding.TypedSetRecord        = (*Record)(nil)
	_ encoding.RunSkipRecord         = (*Record)(nil)
)

// SetNumericAt implements encoding.IndexedReusableRecord: a slice store
// plus one presence bit, no name, no hash (a projected layout adds one
// position → slot load; a position outside the projection falls back to
// the overflow side by name). idx is the field's position
// in the READER's schema — the decoder's schema-walk counter — and it
// indexes this record's positional storage directly, so the record MUST
// have been built over that same schema or a structurally identical one
// (same field order). Every in-tree reuse site builds both from one
// schema (service/stream.go, service/shard_iter.go,
// service/parallel_decode.go); an idx past the record's schema panics
// rather than landing on the wrong field.
// Same no-invalidation contract as SetNumeric.
func (r *Record) SetNumericAt(idx int, value float64) {
	if s := r.layout.slot(idx); s >= 0 {
		r.vals[s] = value
		r.setBit(planeHas, s)
		return
	}
	r.putValue(r.schema.Fields[idx].Name, value)
}

// SetNullFieldAt implements encoding.IndexedReusableRecord; the
// positional twin of SetNullField.
// Like SetNullField it drops the wide value.
func (r *Record) SetNullFieldAt(idx int) {
	if s := r.layout.slot(idx); s >= 0 {
		r.setBit(planeNull, s)
		r.clearBit(planeWide, s)
		if r.aux != nil && len(r.aux.ovWide) > 0 {
			delete(r.aux.ovWide, r.fieldAt(s).Name)
		}
		return
	}
	r.SetNullField(r.schema.Fields[idx].Name)
}

// SetWideFieldAt implements encoding.IndexedReusableRecord; the
// positional twin of SetWideField.
func (r *Record) SetWideFieldAt(idx int, v any) {
	if s := r.layout.slot(idx); s >= 0 {
		r.putWideAt(s, v)
		return
	}
	r.putWide(r.schema.Fields[idx].Name, v)
}

// SetNarrowSetAt implements encoding.TypedSetRecord: the unboxed twin of
// SetWideFieldAt(idx, mask) for a set_u8..set_u64 field. A slot of any
// other kind (a projected-out position, a schema whose duplicate name
// resolved to a differently typed first occurrence) takes the boxed path,
// so the stored value is identical either way.
func (r *Record) SetNarrowSetAt(idx int, mask uint64) {
	if s := r.layout.slot(idx); s >= 0 && r.layout.wideKind[s] == wideNarrowSet {
		r.putNarrowAt(s, mask)
		return
	}
	r.SetWideFieldAt(idx, mask)
}

// SetWideSetAt implements encoding.TypedSetRecord: the unboxed twin of
// SetWideFieldAt(idx, m) for a set_u128 / set_u256 field.
func (r *Record) SetWideSetAt(idx int, m encoding.SetMask) {
	if s := r.layout.slot(idx); s >= 0 && r.layout.wideKind[s] == wideWideSet && r.putMaskAt(s, m) {
		return
	}
	r.SetWideFieldAt(idx, m)
}

// ClearForRow implements encoding.ReusableRecord. Resets per-row state
// so the next ReadRecordReused call starts from a clean slate while
// keeping the underlying storage allocated. The reuse decoders reach it
// through BeginRunRow, and only when run-skip cannot keep the previous
// row's state; it also withdraws the record from run-skip.
//
// Values (and their presence bits) are left intact because every
// retained field is overwritten on every row. The null and wide
// presence planes are cleared — two short word-slice clears — as are
// the overflow null and wide maps. Stale wide slot contents are left in
// place: the cleared presence bit already hides them, and the next
// decode overwrites them.
func (r *Record) ClearForRow() {
	r.runOwner = 0
	if len(r.vals) > 0 {
		w := recordWords(len(r.vals))
		clear(r.bits[planeNull*w : planeCount*w])
	}
	if r.aux != nil {
		if len(r.aux.ovNulls) > 0 {
			clear(r.aux.ovNulls)
		}
		if len(r.aux.ovWide) > 0 {
			clear(r.aux.ovWide)
		}
	}
	r.allValuesCache = nil
}

// BeginRunRow implements encoding.RunSkipRecord. It keeps the record's
// schema-field state for a partial rewrite — clearing only the
// off-schema overflow null / wide marks and the AllValues cache, which
// the decoder never writes — when keep is true, token is the reader that
// began the previous row on this record, no schema field has been
// mutated outside that reader's index-keyed writes since (runOwner is
// still token), and the layout is the full identity layout (one slot per
// schema position, no shared slots). Otherwise it does a full
// ClearForRow and returns false. Either way the record is now owned by
// token.
func (r *Record) BeginRunRow(token uint64, keep bool) bool {
	if keep && token != 0 && r.runOwner == token && r.identityLayout() {
		if r.aux != nil {
			if len(r.aux.ovNulls) > 0 {
				clear(r.aux.ovNulls)
			}
			if len(r.aux.ovWide) > 0 {
				clear(r.aux.ovWide)
			}
		}
		r.allValuesCache = nil
		return true
	}
	r.ClearForRow()
	r.runOwner = token
	return false
}

// identityLayout reports whether every schema position owns its own
// storage slot, slot == position: the full layout over a schema with
// unique field names. A projected layout sends unretained positions to
// the overflow side and a duplicated name shares one slot between
// positions; either would let a skipped position observe a sibling's
// write, so run-skip requires this.
func (r *Record) identityLayout() bool {
	return r.schema != nil && r.layout != nil && r.layout.slotOf == nil &&
		r.layout.n == len(r.schema.Fields)
}

// ClearNullAt implements encoding.RunSkipRecord: clears field idx's null
// mark without touching its value or wide value. Decoder-only, like the
// other *At writes, so it does not withdraw the record from run-skip.
func (r *Record) ClearNullAt(idx int) {
	if s := r.layout.slot(idx); s >= 0 {
		r.clearBit(planeNull, s)
		return
	}
	r.unmarkNull(r.schema.Fields[idx].Name)
}

// NewReusableRecord constructs an empty positional Record over schema.
// It is the record the reuse decoders populate in place across many
// ReadRecordReused calls — a caller doing so must NOT retain it past
// the next iteration step — and it is also the cheapest FRESH record:
// the buffered iterators build one per row and decode into it by
// position, with no map at all.
func NewReusableRecord(schema *encoding.Schema) *Record {
	return newPositionalRecord(schema)
}

// copyStateInto copies every value, null mark and wide value r holds
// into dst, renaming each name through rename. When positional is true
// the caller guarantees that r's schema field i is dst's schema field
// offset+i (true for a HashJoinIterator side whose record was built
// over the schema JoinedSchema was derived from), so schema fields copy
// by position with no name lookup; otherwise every name resolves
// through dst's layout. Overflow entries always take the name path.
func (r *Record) copyStateInto(dst *Record, rename func(string) string, offset int, positional bool) {
	// The positional shortcut needs slot == schema position on both
	// sides; a projected layout on either side takes the name path.
	positional = positional && r.layout.fieldOf == nil && dst.layout.slotOf == nil
	dst.resetRun()
	for i := range r.vals {
		hasV := r.test(planeHas, i)
		isNull := r.test(planeNull, i)
		hasW := r.hasWideAt(i)
		if !hasV && !isNull && !hasW {
			continue
		}
		j := offset + i
		if !positional {
			j = dst.pos(rename(r.fieldAt(i).Name))
		}
		if j >= 0 {
			// j is a dst STORAGE SLOT (dst.pos, or offset+i under the
			// identity layout the positional shortcut requires), so write
			// through the slot primitives — not the *At methods, which
			// take a schema position and would re-map it.
			if hasV {
				dst.vals[j] = r.vals[i]
				dst.setBit(planeHas, j)
			}
			if isNull {
				dst.setBit(planeNull, j)
			}
			if hasW && !r.copyTypedWide(dst, j, i) {
				wv, _ := r.wideAt(i)
				dst.putWideAt(j, wv)
			}
			continue
		}
		name := rename(r.fieldAt(i).Name)
		if hasV {
			dst.putValue(name, r.vals[i])
		}
		if isNull {
			dst.markNull(name)
		}
		if hasW {
			wv, _ := r.wideAt(i)
			dst.putWide(name, wv)
		}
	}
	if r.aux == nil {
		return
	}
	for k, v := range r.aux.ovVals {
		dst.putValue(rename(k), v)
	}
	for k, isNull := range r.aux.ovNulls {
		if isNull {
			dst.markNull(rename(k))
		}
	}
	for k, v := range r.aux.ovWide {
		dst.putWide(rename(k), v)
	}
}

// copyTypedWide copies slot i's typed wide value into dst slot j without
// boxing, when both slots are the same kind and rung. It reports false
// (copying nothing) otherwise, and the caller takes the boxed path.
func (r *Record) copyTypedWide(dst *Record, j, i int) bool {
	kind := r.layout.wideKind[i]
	if !r.test(planeWide, i) || dst.layout.wideKind[j] != kind {
		return false
	}
	switch kind {
	case wideDecimal:
		dst.decsOrNew()[dst.layout.wideOff[j]] = r.aux.decs[r.layout.wideOff[i]]
	case wideNarrowSet:
		dst.setWordAt(int(dst.layout.wideOff[j]), r.wordAt(int(r.layout.wideOff[i])))
	case wideWideSet:
		n := r.fieldAt(i).Type.ByteSize() / 8
		if dst.fieldAt(j).Type.ByteSize()/8 != n {
			return false
		}
		src, off := int(r.layout.wideOff[i]), int(dst.layout.wideOff[j])
		for k := range n {
			dst.setWordAt(off+k, r.wordAt(src+k))
		}
	default:
		return false
	}
	dst.typedWideStored(j)
	return true
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
