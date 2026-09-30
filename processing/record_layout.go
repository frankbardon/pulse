package processing

import (
	"runtime"
	"sync"
	"sync/atomic"
	"weak"

	"github.com/frankbardon/pulse/encoding"
)

// recordLayout is the binding a positional Record resolves names and
// decoder positions through. It is built ONCE per binding — per schema
// for the full layout (cached, shared by every Record over that
// schema), per projection for a projected one (held by the iterator
// that installed the projection) — and never per record or per row.
//
// Records store fields in STORAGE SLOTS. A full layout's slot is the
// schema position itself (slotOf / fieldOf nil ⇒ identity). A projected
// layout stores only the retained fields, densely, so a buffered record
// decoded under projection costs O(retained fields), not O(schema
// width): a 4-field projection over a 200-field cohort allocates 4
// value slots, not 200. A schema field outside the projection has no
// slot and behaves as an off-schema name (overflow side), exactly as a
// never-written key behaved in the former per-record maps.
//
// A full layout deliberately holds no strong reference to its schema:
// it is cached in layoutCache under a weak key and dropped by a runtime
// cleanup when the schema is collected, so a long-lived process that
// opens many cohorts does not pin every schema it ever saw.
type recordLayout struct {
	// owner identifies the schema a cached full layout was built from
	// without keeping it alive. Zero for projected layouts.
	owner weak.Pointer[encoding.Schema]
	// nSchema is len(schema.Fields) at build time. A schema whose field
	// slice has since changed length gets a fresh layout (see layoutFor).
	nSchema int
	// n is the number of storage slots.
	n int
	// index maps a field name to its storage slot.
	index map[string]int
	// slotOf maps a schema position to its storage slot (-1 = not
	// stored); fieldOf maps a slot back to its schema position. Both are
	// nil for a full layout over a schema with unique field names, where
	// slot == position.
	//
	// A name that appears more than once in the schema gets ONE slot,
	// shared by every occurrence: slotOf maps each occurrence to it and
	// fieldOf names the FIRST occurrence — the field Schema.Field(name)
	// returns, and so the one whose dictionary resolves the value. That
	// makes the slot behave exactly as the former map key did: the last
	// occurrence the decoder writes wins the value and the wide value, a
	// null on ANY occurrence marks the name null. Nothing in the import
	// path rejects duplicate column names, so this is reachable.
	slotOf  []int32
	fieldOf []int32
	// wideKind is the typed wide storage a slot owns (wideNone for a
	// type that never carries a wide value — only decimal128 and set_*
	// do), and wideOff its offset into that storage: an index into
	// recordAux.decs for wideDecimal, a word offset into the set-word
	// tail of Record.vals for the set kinds (1 word for a narrow rung, 2 for set_u128, 4 for
	// set_u256). Sparse, so a schema with a handful of wide columns does
	// not pay for them per field.
	wideKind []uint8
	wideOff  []int32
	nDecs    int
	nWords   int
}

// emptyLayout serves a nil schema and a zero-value Record: no slots,
// every name resolves to the overflow side.
var emptyLayout = &recordLayout{}

var (
	// layoutCache maps weak.Pointer[encoding.Schema] → *recordLayout
	// (full layouts only).
	layoutCache sync.Map
	// lastLayout is a one-entry fast path in front of layoutCache: a
	// scan constructs every record over the same schema, so the common
	// lookup is a pointer compare with no weak-handle work at all.
	lastLayout atomic.Pointer[recordLayout]
)

// layoutFor returns the shared FULL layout for s, building it on first
// use.
//
// A schema's Fields slice is exported and could in principle be
// mutated after a layout was built for it. Only the length is
// re-checked (a changed length rebuilds); renaming or reordering fields
// in place under live records is unsupported — the records' positional
// storage was sized and indexed against the old field list.
func layoutFor(s *encoding.Schema) *recordLayout {
	if s == nil {
		return emptyLayout
	}
	if l := lastLayout.Load(); l != nil && l.nSchema == len(s.Fields) && l.owner.Value() == s {
		return l
	}
	key := weak.Make(s)
	if v, ok := layoutCache.Load(key); ok {
		if l := v.(*recordLayout); l.nSchema == len(s.Fields) {
			lastLayout.Store(l)
			return l
		}
	}
	l := buildLayout(s, nil)
	l.owner = key
	if _, loaded := layoutCache.Swap(key, l); !loaded {
		runtime.AddCleanup(s, func(k weak.Pointer[encoding.Schema]) {
			layoutCache.Delete(k)
		}, key)
	}
	lastLayout.Store(l)
	return l
}

// buildLayout builds a layout over s storing the fields keep accepts
// (every field when keep is nil).
func buildLayout(s *encoding.Schema, keep encoding.FieldFilter) *recordLayout {
	nSchema := len(s.Fields)
	l := &recordLayout{nSchema: nSchema, index: make(map[string]int, nSchema)}
	// The identity mapping (slot == position) holds only for a full
	// layout whose names are unique; anything else records the mapping.
	identity := keep == nil
	if identity {
		for i := range s.Fields {
			if _, dup := l.index[s.Fields[i].Name]; dup {
				identity = false
				break
			}
			l.index[s.Fields[i].Name] = i
		}
	}
	if identity {
		l.n = nSchema
	} else {
		clear(l.index)
		l.slotOf = make([]int32, nSchema)
		for i := range s.Fields {
			name := s.Fields[i].Name
			if keep != nil && !keep(name) {
				l.slotOf[i] = -1
				continue
			}
			if slot, dup := l.index[name]; dup {
				l.slotOf[i] = int32(slot)
				continue
			}
			l.index[name] = len(l.fieldOf)
			l.slotOf[i] = int32(len(l.fieldOf))
			l.fieldOf = append(l.fieldOf, int32(i))
		}
		l.n = len(l.fieldOf)
	}
	l.wideKind = make([]uint8, l.n)
	l.wideOff = make([]int32, l.n)
	for slot := 0; slot < l.n; slot++ {
		kind, words := wideKindOf(s.Fields[l.field(slot)].Type)
		l.wideKind[slot] = kind
		switch kind {
		case wideDecimal:
			l.wideOff[slot] = int32(l.nDecs)
			l.nDecs++
		case wideNarrowSet, wideWideSet:
			l.wideOff[slot] = int32(l.nWords)
			l.nWords += words
		}
	}
	return l
}

// Wide storage kinds. A slot's kind is fixed by its field type.
const (
	wideNone      uint8 = iota
	wideDecimal         // encoding.Decimal128 in recordAux.decs
	wideNarrowSet       // set_u8..set_u64: one uint64 word
	wideWideSet         // set_u128 / set_u256: 2 or 4 words, an encoding.SetMask
)

// wideKindOf returns the typed wide storage kind for ft and, for a set
// rung, the uint64 words it occupies.
func wideKindOf(ft encoding.FieldType) (kind uint8, words int) {
	switch {
	case ft == encoding.FieldTypeDecimal128:
		return wideDecimal, 0
	case ft.IsWideSet():
		return wideWideSet, ft.ByteSize() / 8
	case ft.IsSet():
		return wideNarrowSet, 1
	}
	return wideNone, 0
}

// pos resolves name to its storage slot, or -1 when the name has none:
// an off-schema column (an attribute label, a feature output, a
// join-renamed right column) or a schema field outside a projection.
func (l *recordLayout) pos(name string) int {
	if i, ok := l.index[name]; ok {
		return i
	}
	return -1
}

// slot maps a schema position to its storage slot (-1 = not stored).
func (l *recordLayout) slot(idx int) int {
	if l.slotOf == nil {
		return idx
	}
	return int(l.slotOf[idx])
}

// field maps a storage slot to its schema position.
func (l *recordLayout) field(slot int) int {
	if l.fieldOf == nil {
		return slot
	}
	return int(l.fieldOf[slot])
}

// RecordBinding binds Record construction to one schema and, optionally,
// one projection. Build it once per binding — per iterator, per
// SetProjection — and mint a Record per row with NewRecord; all name and
// position resolution was done when the binding was built.
type RecordBinding struct {
	schema *encoding.Schema
	layout *recordLayout
}

// BindRecords returns a binding for records over schema. keep == nil
// binds the full schema (the shared cached layout, the same one
// NewReusableRecord uses). A non-nil keep binds a PROJECTED layout that
// stores only the fields keep accepts, densely: the record for a
// projected decode then costs O(retained fields) instead of O(schema
// width). keep is consulted once per schema field here and never again.
//
// A projected record still accepts a write to any name — a schema field
// outside the projection lands on the overflow side, like an off-schema
// name — so it is correct for any use; it is only SIZED for the decode
// the projection describes. Pair it with the reader's DecodePlan for the
// same keep.
func BindRecords(schema *encoding.Schema, keep encoding.FieldFilter) *RecordBinding {
	if keep == nil || schema == nil {
		return &RecordBinding{schema: schema, layout: layoutFor(schema)}
	}
	return &RecordBinding{schema: schema, layout: buildLayout(schema, keep)}
}

// NewRecord returns a fresh, empty Record under the binding: every field
// absent, nothing allocated beyond the positional storage.
func (b *RecordBinding) NewRecord() *Record {
	return newRecordWithLayout(b.schema, b.layout)
}
