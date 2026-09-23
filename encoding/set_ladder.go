package encoding

// The set-width ladder lives here, beside the type bytes it names, so
// there is exactly ONE rung table in the module.
//
// io/set_width.go used to own it. That was fine while only the import
// path needed to choose a rung, but the shard auto-widen path needs the
// same choice and `encoding` cannot import `io`. A second copy inside
// `encoding` would be the exact failure io/set_width.go's own doc
// comment records: a duplicated ladder does not fail loudly when it
// falls behind — it just goes on refusing the widths the other copy
// already types.

// setLadder is the set-width ladder, narrowest rung first. A rung's
// capacity is read off FieldType.MaxSetEntries rather than written down
// again, so the ladder and the bitmask widths cannot drift apart.
var setLadder = [...]FieldType{
	FieldTypeSetU8,
	FieldTypeSetU16,
	FieldTypeSetU32,
	FieldTypeSetU64,
	FieldTypeSetU128,
	FieldTypeSetU256,
}

// SetLadder returns the set-width ladder narrowest rung first. The
// returned slice is a fresh copy; callers may mutate it freely.
func SetLadder() []FieldType {
	out := make([]FieldType, len(setLadder))
	copy(out, setLadder[:])
	return out
}

// SetTypeFor returns the narrowest set_* type with a bit for each of
// `elements` elements, and reports false when no rung is wide enough
// (or when `elements` is not positive — a set with no elements has no
// type, and that is a caller error rather than a width verdict).
func SetTypeFor(elements int) (FieldType, bool) {
	if elements <= 0 {
		return 0, false
	}
	for _, ft := range setLadder {
		if elements <= int(ft.MaxSetEntries()) {
			return ft, true
		}
	}
	return 0, false
}

// WidestSetType returns the top rung of the ladder — the widest set_*
// type Pulse has. Callers that must NAME the ceiling in a diagnostic
// use this rather than writing "set_u256" into a string, so the message
// cannot outlive the type it names.
func WidestSetType() FieldType {
	return setLadder[len(setLadder)-1]
}
