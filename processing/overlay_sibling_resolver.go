package processing

// Sibling resolver — runtime support for the sibling-reference overlay
// family. The DELTA_VS_SIBLING / INDEX_VS_SIBLING handlers both need to
// map an OverlaySpec.Ref.Sibling `(Field, Value)` pair to a single host
// group ordinal so the per-group fold can subtract or divide against
// that fixed reference. The resolver walks the host's GroupKeys list
// and matches against the SERIES host's grouper-field list (surfaced
// via the SeriesHostView's `groupFields` slot). It matches against the
// first grouper field (the SERIES host shape emits a single-axis
// grouper list of length 1 in the common case — the row-axis tuple is
// the only key component) AND falls back to scanning every axis-key
// element for a match when the field-list is empty (a defensive
// fallback when the orchestrator has not populated the field list).
//
// A future iteration may factor this resolver out behind a richer
// interface that surfaces composite-key tuples (Field1=v1 AND
// Field2=v2) and integrates the field-list directly. For now the
// simpler single-field lookup is enough to power the two
// sibling-reference handlers.
//
// Structural invariants:
//
//   - This file MUST NOT import service/ or descriptor/. Runtime
//     overlay execution rides inside processing/ alongside overlay.go.
//   - No fmt.Sprintf in any JSON-bearing path. The resolver returns
//     `(value, present)` and never builds messages.

// resolveSibling looks up a single host group identified by the
// OverlaySpec.Ref.Sibling `(field, value)` pair on the SERIES host.
// Returns `(value, present)` mirroring the SeriesHostView.ValueAt
// contract:
//
//   - present=true: the resolver found exactly one host group whose
//     axis key matches the requested `(field, value)` pair. The
//     returned value is the host's metric for that group ordinal.
//   - present=false: the field is unknown to the host's grouper list,
//     the value does not appear among the host's observed axis keys for
//     the named field, OR the host's resolver itself reported the
//     matching group as absent (the orchestrator did not produce a
//     value for that group). The caller surfaces
//     PULSE_OVERLAY_REF_UNKNOWN regardless of which path triggered the
//     `(0, false)` return.
//
// Lookup policy: matching against a single grouper field is sufficient
// for v1 because the SERIES host today exposes a single-axis tuple per
// group (the orchestrator's grouper list is consumed as the row axis;
// multi-grouper composite axes lower to a single AxisKey tuple where
// element `i` matches `groupFields[i]`). The resolver consults the host
// view's `GroupFields()` slot when populated — when the slot is empty
// the resolver defensively scans every element of every axis key tuple
// looking for an `any` value whose string form matches `value`. The
// scan path is byte-equivalent on a single-axis host (the only element
// matches by definition) and produces a sensible fallback on a
// multi-axis host that surfaces the matching value in any position
// (matches the "find the first sibling whose key contains the
// requested value" semantics until a composite-key resolver lands).
//
// Returns `(0, false)` when host is nil, when the host has no group
// keys, when field or value is empty (defensive — the validator rejects
// empty pairs at predict time, but the runtime resolver stays robust),
// or when the field/value combination does not resolve to a present
// group. A field that resolves to a known grouper but a value that does
// not appear in any axis key for that field returns `(0, false)` — the
// caller emits PULSE_OVERLAY_REF_UNKNOWN.
//
// Distinct from `SeriesHostView.ValueAt(i)` — that helper indexes by
// host ordinal; resolveSibling indexes by `(field, value)` semantics
// and walks the host's group-key list to find the matching ordinal.
//
// Forward-compat (ordered-axis kinds): the windowed Process catalog
// (Baseline / Prior / RollingMean) wants resolution against an ordered
// axis index, not a `(field, value)` pair. No new resolver helper is
// needed — those kinds call `host.ValueAt(i)` directly with the
// desired index ordinal; this resolver stays scoped to the
// `(field, value)` sibling shape.
func resolveSibling(host *SeriesHostView, field, value string) (float64, bool) {
	if host == nil || field == "" || value == "" {
		return 0, false
	}
	groupCount := host.GroupCount()
	if groupCount == 0 {
		return 0, false
	}
	// Determine the axis-key element index `field` resolves to via the
	// host's optional GroupFields() slot. When the slot is empty (the
	// orchestrator has not wired the field list onto the SeriesHostView)
	// the column index stays at -1 and the scan path matches against
	// every element of every axis-key tuple.
	colIdx := -1
	for fi, name := range host.GroupFields() {
		if name == field {
			colIdx = fi
			break
		}
	}
	if len(host.GroupFields()) > 0 && colIdx < 0 {
		// The field list is populated AND the requested field is not in
		// it — unknown field, no match possible.
		return 0, false
	}
	for i := 0; i < groupCount; i++ {
		key, ok := host.GroupKey(i)
		if !ok || len(key) == 0 {
			continue
		}
		if colIdx >= 0 {
			if colIdx >= len(key) {
				continue
			}
			if axisKeyElementMatches(key[colIdx], value) {
				v, present := host.ValueAt(i)
				return v, present
			}
			continue
		}
		// Field list empty — scan every element of the key.
		for _, elem := range key {
			if axisKeyElementMatches(elem, value) {
				v, present := host.ValueAt(i)
				return v, present
			}
		}
	}
	return 0, false
}

// axisKeyElementMatches reports whether the given axis-key element
// (declared as `any` because the host's AxisKey carries mixed types —
// categorical strings, numerics, dates) matches the on-wire string
// value the caller named in `Ref.Sibling.Value`.
//
// String equality is the primary path — categorical axis keys are
// already string-valued. For non-string axis-key elements the function
// returns false; v1 of the sibling resolver does NOT coerce numerics to
// strings (e.g. `123` does not match `"123"` automatically). A future
// iteration may widen the policy if a non-categorical sibling axis
// surfaces a concrete need; today every sibling-reference test rides
// on a categorical grouper field.
func axisKeyElementMatches(elem any, value string) bool {
	switch v := elem.(type) {
	case string:
		return v == value
	}
	return false
}
