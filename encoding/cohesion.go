package encoding

import (
	"fmt"

	"github.com/frankbardon/pulse/errors"
)

// CohesionWarning is a structured non-fatal divergence emitted by the
// schema-cohesion validators. Its shape mirrors the descriptor envelope's
// {code, message, details} entries so callers (typically
// `service/shard add` and `pulse shard verify`) can forward warnings
// through the standard --json output without reshaping.
//
// encoding/ stays free of descriptor/ imports — this is the local
// equivalent.
type CohesionWarning struct {
	Code    string
	Message string
	Details map[string]any
}

// ValidateStructuralCohesion verifies that incoming's structural
// schema is byte-equal to canonical's. The check is field-count
// strict and per-field strict on:
//
//   - Name
//   - Type byte
//   - ByteOffset
//   - BitPosition
//   - Categorical-width identity (a categorical_u8 cannot become a
//     categorical_u16 across shards — the width is fixed at folder
//     creation)
//
// Descriptions are advisory and may diverge across shards; per-field
// divergence emits a PULSE_SHARD_DESCRIPTION_DIVERGENCE warning but
// does NOT fail validation. Any other mismatch returns a coded
// PULSE_SHARD_SCHEMA_MISMATCH error.
//
// This validator does NOT compare dictionaries — dictionary cohesion
// is governed by the append-only prefix rule (see
// ValidateDictPrefixRule).
func ValidateStructuralCohesion(canonical, incoming *Schema) ([]CohesionWarning, error) {
	if canonical == nil || incoming == nil {
		return nil, errors.NewCodedError(errors.PULSE_SHARD_SCHEMA_MISMATCH,
			"cohesion validation requires non-nil canonical and incoming schemas")
	}
	if len(canonical.Fields) != len(incoming.Fields) {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_SCHEMA_MISMATCH,
			fmt.Sprintf("field count differs: canonical=%d, incoming=%d",
				len(canonical.Fields), len(incoming.Fields)),
			map[string]any{
				"canonical_field_count": len(canonical.Fields),
				"incoming_field_count":  len(incoming.Fields),
			})
	}

	var warnings []CohesionWarning

	for i := range canonical.Fields {
		cf := &canonical.Fields[i]
		nf := &incoming.Fields[i]

		if cf.Name != nf.Name {
			return warnings, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_SCHEMA_MISMATCH,
				fmt.Sprintf("field %d name differs: canonical=%q, incoming=%q",
					i, cf.Name, nf.Name),
				map[string]any{
					"field_index":    i,
					"canonical_name": cf.Name,
					"incoming_name":  nf.Name,
				})
		}
		if cf.Type != nf.Type {
			return warnings, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_SCHEMA_MISMATCH,
				fmt.Sprintf("field %q type differs: canonical=%s, incoming=%s",
					cf.Name, cf.Type, nf.Type),
				map[string]any{
					"field":          cf.Name,
					"canonical_type": cf.Type.String(),
					"incoming_type":  nf.Type.String(),
				})
		}
		if cf.ByteOffset != nf.ByteOffset {
			return warnings, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_SCHEMA_MISMATCH,
				fmt.Sprintf("field %q byte offset differs: canonical=%d, incoming=%d",
					cf.Name, cf.ByteOffset, nf.ByteOffset),
				map[string]any{
					"field":            cf.Name,
					"canonical_offset": cf.ByteOffset,
					"incoming_offset":  nf.ByteOffset,
				})
		}
		if cf.BitPosition != nf.BitPosition {
			return warnings, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_SCHEMA_MISMATCH,
				fmt.Sprintf("field %q bit position differs: canonical=%d, incoming=%d",
					cf.Name, cf.BitPosition, nf.BitPosition),
				map[string]any{
					"field":             cf.Name,
					"canonical_bit_pos": cf.BitPosition,
					"incoming_bit_pos":  nf.BitPosition,
				})
		}
		// Dictionary-bearing width identity is already enforced by the
		// Type-byte check (FieldTypeCategoricalU8/U16/U32 and
		// FieldTypeSetU8/U16/U32/U64/U128/U256 are distinct bytes). The explicit
		// case below makes the contract self-documenting for reviewers
		// and surfaces a clearer error message if the type check ever
		// loosens.
		if cf.Type.HasDictionary() && cf.Type != nf.Type {
			return warnings, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_SCHEMA_MISMATCH,
				fmt.Sprintf("field %q dictionary width differs: canonical=%s, incoming=%s",
					cf.Name, cf.Type, nf.Type),
				map[string]any{
					"field":           cf.Name,
					"canonical_width": cf.Type.String(),
					"incoming_width":  nf.Type.String(),
				})
		}

		if cf.Description != nf.Description {
			warnings = append(warnings, CohesionWarning{
				Code: string(errors.PULSE_SHARD_DESCRIPTION_DIVERGENCE),
				Message: fmt.Sprintf("field %q description diverges between canonical and incoming shard; canonical description wins",
					cf.Name),
				Details: map[string]any{
					"field":                 cf.Name,
					"canonical_description": cf.Description,
					"incoming_description":  nf.Description,
				},
			})
		}
	}
	return warnings, nil
}

// ValidateDictPrefixRule enforces the append-only prefix rule for
// every categorical field shared by canonical and incoming. For each
// `categorical_*` field:
//
//   - If incoming's dictionary is a prefix of canonical's, accept the
//     shard as-is; no canonical change. (Older shard that never saw
//     newer dictionary values.)
//   - If canonical's dictionary is a prefix of incoming's, the
//     canonical schema adopts the extension. The returned
//     extendedCanonical carries the merged dictionaries and the caller
//     is responsible for rewriting `_schema.pulse` to publish them
//     before the new shard is placed (crash-safety ordering documented
//     in placeholder §3.2).
//   - Neither is a prefix of the other → PULSE_SHARD_DICT_DIVERGENCE.
//   - An extension that would exceed the field's width capacity
//     (256 for u8, 65 536 for u16, 2^32 for u32) →
//     PULSE_SHARD_DICT_WIDTH_OVERFLOW.
//
// The structural validator (ValidateStructuralCohesion) must run
// FIRST — this validator assumes field positions, names, and
// categorical widths already match.
//
// The returned extendedCanonical is a deep copy when an extension is
// adopted, and is identically the canonical input otherwise. Callers
// can compare pointers to detect whether a rewrite is required.
func ValidateDictPrefixRule(canonical, incoming *Schema) (*Schema, error) {
	if canonical == nil || incoming == nil {
		return nil, errors.NewCodedError(errors.PULSE_SHARD_DICT_DIVERGENCE,
			"dict prefix validation requires non-nil canonical and incoming schemas")
	}
	if len(canonical.Fields) != len(incoming.Fields) {
		return nil, errors.NewCodedError(errors.PULSE_SHARD_SCHEMA_MISMATCH,
			"dict prefix validation requires identical field counts; run ValidateStructuralCohesion first")
	}

	var extended *Schema
	for i := range canonical.Fields {
		cf := &canonical.Fields[i]
		nf := &incoming.Fields[i]
		if !cf.Type.HasDictionary() {
			continue
		}
		// Defense in depth — structural validator already rejects
		// width changes.
		if cf.Type != nf.Type {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_SCHEMA_MISMATCH,
				fmt.Sprintf("field %q dictionary width differs", cf.Name),
				map[string]any{"field": cf.Name})
		}
		cv := dictValuesOrEmpty(cf.Dictionary)
		nv := dictValuesOrEmpty(nf.Dictionary)
		switch {
		case isPrefix(nv, cv):
			// Incoming is a prefix (or equal) — accept, no canonical change.
			continue
		case isPrefix(cv, nv):
			// Canonical is a prefix; incoming extends it. Verify the
			// extension fits the declared width before accepting.
			maxEntries := cf.Type.MaxDictEntries()
			if uint32(len(nv)) > maxEntries {
				return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW,
					fmt.Sprintf("field %q dictionary growth (%d entries) exceeds %s capacity (%d entries)",
						cf.Name, len(nv), cf.Type, maxEntries),
					map[string]any{
						"field":            cf.Name,
						"type":             cf.Type.String(),
						"capacity":         maxEntries,
						"incoming_entries": len(nv),
					})
			}
			if extended == nil {
				extended = cloneSchema(canonical)
			}
			extDict := NewDictionary()
			for _, v := range nv {
				if _, err := extDict.Add(v); err != nil {
					return nil, err
				}
			}
			extended.Fields[i].Dictionary = extDict
		default:
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_DICT_DIVERGENCE,
				fmt.Sprintf("field %q dictionaries are not prefix-related; align dictionaries upstream",
					cf.Name),
				map[string]any{
					"field":             cf.Name,
					"canonical_entries": len(cv),
					"incoming_entries":  len(nv),
					"canonical_values":  cv,
					"incoming_values":   nv,
				})
		}
	}
	if extended != nil {
		return extended, nil
	}
	return canonical, nil
}

// DictRemap is the per-field index translation produced by
// MergeDictUnion. The key is the incoming dictionary index; the value
// is the corresponding canonical (union) dictionary index. Fields
// whose incoming dictionary is already a prefix of the canonical
// (union) dictionary are absent from the returned map — callers can
// skip remapping their record bytes.
type DictRemap = map[uint32]uint32

// MergeDictUnion is the relaxed dictionary cohesion check. For each
// categorical field it computes the union of canonical's and
// incoming's dictionaries: canonical entries first in their existing
// order, then any new entries from incoming in their order. Returns
// the extended canonical schema (deep copy when an extension is
// adopted; identical pointer when not) and a per-field-index map of
// DictRemap entries describing how to translate incoming record
// indices into canonical indices.
//
// The structural validator (ValidateStructuralCohesion) must run
// FIRST — this validator assumes field positions, names, and
// categorical widths already match.
//
// Errors:
//
//   - PULSE_SHARD_DICT_WIDTH_OVERFLOW when the union would exceed the
//     field's categorical width capacity.
//
// MergeDictUnion is the default cohesion mode at insert time
// (CreateShardArchive / AddShard). The stricter prefix-only rule
// (ValidateDictPrefixRule) is retained for callers that want to
// surface divergence as an error instead of merging — used by
// VerifyShardArchive when checking archives written before the
// union-merge default landed and by embedders that prefer to align
// dictionaries upstream.
func MergeDictUnion(canonical, incoming *Schema) (*Schema, map[int]DictRemap, error) {
	if canonical == nil || incoming == nil {
		return nil, nil, errors.NewCodedError(errors.PULSE_SHARD_SCHEMA_MISMATCH,
			"dict union validation requires non-nil canonical and incoming schemas")
	}
	if len(canonical.Fields) != len(incoming.Fields) {
		return nil, nil, errors.NewCodedError(errors.PULSE_SHARD_SCHEMA_MISMATCH,
			"dict union validation requires identical field counts; run ValidateStructuralCohesion first")
	}

	var extended *Schema
	remaps := map[int]DictRemap{}

	for i := range canonical.Fields {
		cf := &canonical.Fields[i]
		nf := &incoming.Fields[i]
		if !cf.Type.HasDictionary() {
			continue
		}
		if cf.Type != nf.Type {
			return nil, nil, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_SCHEMA_MISMATCH,
				fmt.Sprintf("field %q dictionary width differs", cf.Name),
				map[string]any{"field": cf.Name})
		}

		cv := dictValuesOrEmpty(cf.Dictionary)
		nv := dictValuesOrEmpty(nf.Dictionary)

		// Fast path: incoming is a prefix of canonical. No extension,
		// no remap.
		if isPrefix(nv, cv) {
			continue
		}

		// Build the union: canonical entries first, then any
		// incoming entries not already in canonical, in incoming
		// order. The remap follows from the resulting index
		// assignments.
		unionVals, canonicalIndex := unionDictValues(cv, nv)

		maxEntries := cf.Type.MaxDictEntries()
		if uint32(len(unionVals)) > maxEntries {
			return nil, nil, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW,
				fmt.Sprintf("field %q dictionary union (%d entries) exceeds %s capacity (%d entries)",
					cf.Name, len(unionVals), cf.Type, maxEntries),
				map[string]any{
					"field":            cf.Name,
					"type":             cf.Type.String(),
					"capacity":         maxEntries,
					"union_entries":    len(unionVals),
					"canonical_values": cv,
					"incoming_values":  nv,
				})
		}

		// Adopt the extension on canonical.
		if extended == nil {
			extended = cloneSchema(canonical)
		}
		extDict := NewDictionary()
		for _, v := range unionVals {
			if _, err := extDict.Add(v); err != nil {
				return nil, nil, err
			}
		}
		extended.Fields[i].Dictionary = extDict

		// Build the incoming → union remap. Identity entries are
		// omitted so callers can skip work when remap[oldIdx] == oldIdx.
		fieldRemap := make(DictRemap, len(nv))
		for incomingIdx, v := range nv {
			newIdx, ok := canonicalIndex[v]
			if !ok {
				// Defensive — every incoming value was added above.
				return nil, nil, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_DICT_DIVERGENCE,
					fmt.Sprintf("internal: field %q value %q absent from union after merge", cf.Name, v),
					map[string]any{"field": cf.Name, "value": v})
			}
			if uint32(incomingIdx) != newIdx {
				fieldRemap[uint32(incomingIdx)] = newIdx
			}
		}
		if len(fieldRemap) > 0 {
			remaps[i] = fieldRemap
		}
	}

	if extended != nil {
		return extended, remaps, nil
	}
	return canonical, remaps, nil
}

// unionDictValues builds the merged dictionary two shards imply:
// canonical entries first in their existing order, then any incoming
// entry not already present, in incoming order. It also returns the
// value → union-index map the remap is derived from.
//
// MergeDictUnion and PlanSetWidening both call it. They must agree on
// the union SIZE to the entry — the plan decides which rung the archive
// is rewritten to and the merge then has to fit inside that rung — and
// a second, independently-coded union would disagree silently: the plan
// would widen to set_u128, the merge would overflow at 129, and the
// archive would already have been re-laid-out.
func unionDictValues(canonical, incoming []string) ([]string, map[string]uint32) {
	unionVals := make([]string, len(canonical))
	copy(unionVals, canonical)
	index := make(map[string]uint32, len(canonical)+len(incoming))
	for i, v := range canonical {
		index[v] = uint32(i)
	}
	for _, v := range incoming {
		if _, ok := index[v]; ok {
			continue
		}
		index[v] = uint32(len(unionVals))
		unionVals = append(unionVals, v)
	}
	return unionVals, index
}

// SetWidenPlan describes the rung promotion one set field needs before
// a shard can join an archive: the merged dictionary the two schemas
// imply no longer fits the rung both currently declare.
type SetWidenPlan struct {
	// FieldIndex is the field's position in both schemas (structural
	// cohesion has already established they are the same position).
	FieldIndex int
	// Field is the field name, for diagnostics.
	Field string
	// From is the rung both schemas currently declare.
	From FieldType
	// To is the narrowest rung that holds the merged dictionary.
	To FieldType
	// UnionEntries is the size of the merged dictionary that forced it.
	UnionEntries int
}

// PlanSetWidening reports which set fields must be promoted to a wider
// rung before canonical and incoming can be dictionary-union-merged,
// in field order.
//
// It answers the question MergeDictUnion cannot: MergeDictUnion sees a
// union that exceeds the declared bitmask width and can only refuse
// (PULSE_SHARD_DICT_WIDTH_OVERFLOW), because widening is an archive-wide
// byte rewrite and nothing at the schema layer may perform one. Running
// this FIRST lets the caller widen the whole archive and then merge into
// a rung that fits.
//
// An empty plan and a nil error is the ordinary case: every merged
// dictionary already fits.
//
// Errors:
//   - PULSE_SHARD_DICT_WIDTH_OVERFLOW when the union exceeds the WIDEST
//     rung. There is nowhere left to widen to, so this stays fatal — it
//     is the one set-width overflow auto-widen does not remove.
//   - PULSE_SHARD_SCHEMA_MISMATCH on nil schemas, differing field counts
//     or a per-field type divergence (run ValidateStructuralCohesion first).
//
// Categorical fields are ignored entirely: a categorical's width is
// fixed at folder creation by contract, and its capacity (256 / 65 536 /
// 2^32) is not a bitmask a record rewrite can widen the same way.
func PlanSetWidening(canonical, incoming *Schema) ([]SetWidenPlan, error) {
	if canonical == nil || incoming == nil {
		return nil, errors.NewCodedError(errors.PULSE_SHARD_SCHEMA_MISMATCH,
			"set-widen planning requires non-nil canonical and incoming schemas")
	}
	if len(canonical.Fields) != len(incoming.Fields) {
		return nil, errors.NewCodedError(errors.PULSE_SHARD_SCHEMA_MISMATCH,
			"set-widen planning requires identical field counts; run ValidateStructuralCohesion first")
	}

	var plans []SetWidenPlan
	for i := range canonical.Fields {
		cf := &canonical.Fields[i]
		nf := &incoming.Fields[i]
		if !cf.Type.IsSet() {
			continue
		}
		if cf.Type != nf.Type {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_SCHEMA_MISMATCH,
				fmt.Sprintf("field %q set width differs", cf.Name),
				map[string]any{"field": cf.Name,
					"canonical_type": cf.Type.String(), "incoming_type": nf.Type.String()})
		}

		unionVals, _ := unionDictValues(dictValuesOrEmpty(cf.Dictionary), dictValuesOrEmpty(nf.Dictionary))
		if uint32(len(unionVals)) <= cf.Type.MaxSetEntries() {
			continue
		}
		target, ok := SetTypeFor(len(unionVals))
		if !ok {
			widest := WidestSetType()
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW,
				fmt.Sprintf("field %q dictionary union (%d entries) exceeds the widest set rung %s (%d entries); there is no wider type to widen to",
					cf.Name, len(unionVals), widest, widest.MaxSetEntries()),
				map[string]any{
					"field":         cf.Name,
					"type":          cf.Type.String(),
					"widest_type":   widest.String(),
					"capacity":      widest.MaxSetEntries(),
					"union_entries": len(unionVals),
				})
		}
		plans = append(plans, SetWidenPlan{
			FieldIndex:   i,
			Field:        cf.Name,
			From:         cf.Type,
			To:           target,
			UnionEntries: len(unionVals),
		})
	}
	return plans, nil
}

// SetWidthHeadroom reports how much of one set field's bitmask capacity
// the canonical dictionary has consumed, and which rung it would be
// widened to next.
//
// It exists so an impending archive-wide widen is FORESEEABLE. The
// widen itself is correct but expensive (every record of every shard is
// re-laid-out), and without this a caller learns the field was one entry
// from its ceiling only by paying for the rewrite.
type SetWidthHeadroom struct {
	Field string `json:"field"`
	// Type is the rung the field currently declares.
	Type string `json:"type"`
	// Used is the number of dictionary entries in the canonical schema.
	Used int `json:"used"`
	// Capacity is the rung's bitmask width.
	Capacity int `json:"capacity"`
	// Headroom is Capacity - Used: how many more distinct members the
	// archive can absorb before the next `shard add` widens it.
	Headroom int `json:"headroom"`
	// NextType is the rung a widen would promote to, or "" when the
	// field is already at the widest rung — at which point Headroom is
	// the hard remainder and an overflow is fatal, not widenable.
	NextType string `json:"next_type,omitempty"`
}

// SetWidthHeadroomFor returns one SetWidthHeadroom per set field in s,
// in field order. A schema with no set fields returns nil.
func SetWidthHeadroomFor(s *Schema) []SetWidthHeadroom {
	if s == nil {
		return nil
	}
	var out []SetWidthHeadroom
	for i := range s.Fields {
		f := &s.Fields[i]
		if !f.Type.IsSet() {
			continue
		}
		used := len(dictValuesOrEmpty(f.Dictionary))
		capacity := int(f.Type.MaxSetEntries())
		// One member past this rung's capacity is by definition the
		// next rung — and at the top of the ladder there is no such
		// rung, so SetTypeFor reports false and NextType stays empty.
		// The ladder is the only authority here; a separate
		// "am I the widest" branch would be a second one.
		next := ""
		if nt, ok := SetTypeFor(capacity + 1); ok {
			next = nt.String()
		}
		out = append(out, SetWidthHeadroom{
			Field:    f.Name,
			Type:     f.Type.String(),
			Used:     used,
			Capacity: capacity,
			Headroom: capacity - used,
			NextType: next,
		})
	}
	return out
}

// dictValuesOrEmpty returns the dictionary's values in insertion
// order, or an empty slice when the dictionary is nil (treat absent
// dict as the zero-length prefix).
func dictValuesOrEmpty(d *Dictionary) []string {
	if d == nil {
		return nil
	}
	return d.Values()
}

// isPrefix reports whether p is a prefix of s (insertion-ordered
// equality on the first len(p) entries). An empty p is a prefix of
// any s, including itself.
func isPrefix(p, s []string) bool {
	if len(p) > len(s) {
		return false
	}
	for i := range p {
		if p[i] != s[i] {
			return false
		}
	}
	return true
}

// cloneSchema returns a deep copy of s suitable for mutation by the
// dict-extension path. Field descriptors are copied value-for-value
// and dictionaries are cloned independently.
func cloneSchema(s *Schema) *Schema {
	if s == nil {
		return nil
	}
	out := &Schema{Fields: make([]Field, len(s.Fields))}
	for i, f := range s.Fields {
		out.Fields[i] = f
		if f.Dictionary != nil {
			d := NewDictionary()
			for _, v := range f.Dictionary.Values() {
				_, _ = d.Add(v)
			}
			out.Fields[i].Dictionary = d
		}
	}
	return out
}
