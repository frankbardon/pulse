// Package seriespartition is the one reading of a series overlay's
// `params.partition_by` and the one partition engine over a grouped
// host's composite rows. With two or more groupers a series overlay
// runs once per PARTITION — the rows sharing the leading partition
// keys — and the series runs down the remaining keys; partitions never
// leak into each other.
//
//   - Implicit (absent or null `partition_by`): every key but the last,
//     so the series runs down the last grouper (the _TOTAL kinds take
//     each row's total over the rows sharing every other key). One grouper means no
//     partition (the pre-U46 walk).
//   - Explicit: a LEADING PREFIX of the request's group key columns
//     (vectors.GroupKeyField). `[]` means no partition: one walk over
//     every composite row. Anything else — an unknown column, a column
//     out of prefix order, a non-string-list value — is refused
//     PULSE_OVERLAY_PARTITION_INVALID.
//
// Partitions are built by KEY EQUALITY on the prefix, never by
// contiguity: a user `sort` can interleave buckets, so two rows of one
// partition need not be adjacent. Within a partition rows keep host
// order.
//
// The runtime fold (internal/processing) and predict
// (internal/descriptor) both call Depth, so the refusal — code, message
// and Details — cannot drift between them. Partition / RowKeys are the
// reusable engine for any consumer that splits composite rows by a key
// prefix.
//
// Leaf: stdlib + types + errors + internal/vectors. No-execute.
package seriespartition

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// Param is the OverlaySpec.Params key naming the partition keys.
const Param = "partition_by"

// ordered is the set of process-host series overlay kinds that read the
// host as an ORDERED series (a lag, a window, a position, a sibling
// row): within a partition the series runs down the remaining keys.
var ordered = map[types.OverlayKind]bool{
	types.OverlayKindDeltaVsPrior:       true,
	types.OverlayKindIndexVsPrior:       true,
	types.OverlayKindIndexVsRollingMean: true,
	types.OverlayKindZScoreVsRolling:    true,
	types.OverlayKindDeltaVsBaseline:    true,
	types.OverlayKindIndexVsBaseline:    true,
	types.OverlayKindDeltaVsSibling:     true,
	types.OverlayKindIndexVsSibling:     true,
}

// kinds is every process-host series overlay kind that runs per
// partition and honours Param: the ordered kinds plus the three _TOTAL
// kinds, whose total (or mean and SD) is the partition's own, order-free.
var kinds = map[types.OverlayKind]bool{
	types.OverlayKindDeltaVsPrior:       true,
	types.OverlayKindIndexVsPrior:       true,
	types.OverlayKindIndexVsRollingMean: true,
	types.OverlayKindZScoreVsRolling:    true,
	types.OverlayKindDeltaVsBaseline:    true,
	types.OverlayKindIndexVsBaseline:    true,
	types.OverlayKindDeltaVsSibling:     true,
	types.OverlayKindIndexVsSibling:     true,
	types.OverlayKindIndexVsTotal:       true,
	types.OverlayKindShareOfTotal:       true,
	types.OverlayKindZScoreVsTotal:      true,
}

// Applies reports whether kind is a series overlay kind that runs per
// partition and reads Param.
func Applies(kind types.OverlayKind) bool { return kinds[kind] }

// Ordered reports whether kind is one of the partitioned kinds that
// reads its partition as an ordered series, so the last grouper's key
// order is the series order (the _TOTAL kinds are order-free).
func Ordered(kind types.OverlayKind) bool { return ordered[kind] }

// Explicit reports whether spec's Params carry a non-null partition_by
// (valid or not). Absent, null or undecodable Params is the implicit
// default.
func Explicit(spec *types.OverlaySpec) bool {
	if spec == nil || len(spec.Params) == 0 {
		return false
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(spec.Params, &m); err != nil {
		return false
	}
	raw, present := m[Param]
	return present && string(raw) != "null"
}

// Kinds returns every kind Applies accepts, in types.AllOverlayKinds
// order.
func Kinds() []types.OverlayKind {
	out := make([]types.OverlayKind, 0, len(kinds))
	for _, k := range types.AllOverlayKinds() {
		if kinds[k] {
			out = append(out, k)
		}
	}
	return out
}

// KeyColumns returns the key column each grouper writes its bucket key
// under, in request order (vectors.GroupKeyField over the slot's own
// type). A nil slot contributes "".
func KeyColumns(groups []*types.Group) []string {
	out := make([]string, len(groups))
	for i, g := range groups {
		if g != nil {
			out[i] = vectors.GroupKeyField(g.Type, g)
		}
	}
	return out
}

// Depth returns how many leading groupers spec partitions by: the
// implicit default len(groups)-1 (0 for zero or one grouper) when
// Params carries no (or a null) partition_by, else the length of the
// explicit list, which must be a leading prefix of KeyColumns(groups).
// A refusal is PULSE_OVERLAY_PARTITION_INVALID with Details {index,
// kind, partition_by, groups, reason} plus `field` for a named column —
// index is the spec's position in Request.Overlays, groups the key
// columns in order, reason one of "not_string_list", "unknown_field",
// "not_prefix".
//
// Params that do not decode as a JSON object carry no partition_by;
// the kind's own params gate owns that refusal.
func Depth(groups []*types.Group, spec *types.OverlaySpec, index int) (int, error) {
	implicit := 0
	if len(groups) > 1 {
		implicit = len(groups) - 1
	}
	if spec == nil || len(spec.Params) == 0 {
		return implicit, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(spec.Params, &m); err != nil {
		return implicit, nil
	}
	raw, present := m[Param]
	if !present || string(raw) == "null" {
		return implicit, nil
	}
	cols := KeyColumns(groups)
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		var given any
		_ = json.Unmarshal(raw, &given)
		return 0, refusal(spec.Kind, index, given, cols, "not_string_list", "",
			"params.partition_by must be a list of group key columns")
	}
	for i, name := range names {
		known := false
		for _, c := range cols {
			if c == name {
				known = true
				break
			}
		}
		if !known {
			return 0, refusal(spec.Kind, index, names, cols, "unknown_field", name,
				"params.partition_by names \""+name+"\", which is not a group key column")
		}
		if i >= len(cols) || cols[i] != name {
			return 0, refusal(spec.Kind, index, names, cols, "not_prefix", name,
				"params.partition_by must be a leading prefix of the group key columns; \""+name+"\" is out of place")
		}
	}
	return len(names), nil
}

// refusal builds the one PULSE_OVERLAY_PARTITION_INVALID error.
func refusal(kind types.OverlayKind, index int, given any, cols []string, reason, field, why string) error {
	details := map[string]any{
		"index":        index,
		"kind":         string(kind),
		"partition_by": given,
		"groups":       append([]string(nil), cols...),
		"reason":       reason,
	}
	if field != "" {
		details["field"] = field
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_PARTITION_INVALID,
		"overlay "+string(kind)+" "+why+" (groups: "+strings.Join(cols, ", ")+"); omit it for every key but the last, or pass [] for no partition",
		details)
}

// RowKeys returns each row's composite key: the row's value under each
// key column, in column order (nil where the row carries none).
func RowKeys(rows []map[string]any, cols []string) []types.AxisKey {
	keys := make([]types.AxisKey, len(rows))
	for i, row := range rows {
		key := make(types.AxisKey, len(cols))
		for j, c := range cols {
			if v, ok := row[c]; ok {
				key[j] = v
			}
		}
		keys[i] = key
	}
	return keys
}

// Partition splits row ordinals 0..len(keys)-1 into partitions by KEY
// EQUALITY on the first depth elements of each key (a key shorter than
// depth compares its missing elements as nil). Partitions are ordered
// by first appearance and each lists its ordinals ascending, so rows
// keep host order however a sort interleaved them. depth <= 0 or an
// empty host yields one partition holding every row (none when keys is
// empty).
func Partition(keys []types.AxisKey, depth int) [][]int {
	if len(keys) == 0 {
		return nil
	}
	if depth <= 0 {
		all := make([]int, len(keys))
		for i := range all {
			all[i] = i
		}
		return [][]int{all}
	}
	index := map[string]int{}
	var parts [][]int
	for i, k := range keys {
		id := prefixID(k, depth)
		p, ok := index[id]
		if !ok {
			p = len(parts)
			index[id] = p
			parts = append(parts, nil)
		}
		parts[p] = append(parts[p], i)
	}
	return parts
}

// prefixID is an injective identity for the first depth elements of k:
// each element contributes its dynamic type and value, so the string
// "1" and the number 1 stay distinct, as do nil and "".
func prefixID(k types.AxisKey, depth int) string {
	var b strings.Builder
	for j := 0; j < depth; j++ {
		var v any
		if j < len(k) {
			v = k[j]
		}
		s := fmt.Sprintf("%T:%v", v, v)
		fmt.Fprintf(&b, "%d:%s", len(s), s)
	}
	return b.String()
}
