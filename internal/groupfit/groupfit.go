// Package groupfit is the one reading of a data-dependent grouper's
// params.fit (GROUP_QUANTILE, GROUP_KMEANS): "global" (the default)
// fits the grouper ONCE over the whole filtered set, whatever its
// position; "within" refits it inside each bucket of the groupers
// before it — the outer segment — so [region, quartile] gives
// per-region quartiles.
//
// The rule is identical in plain groups, both crosstab axes (the
// composite order is rows then columns, so a column position's outer
// segment is the full row tuple plus its column prefix) and every
// Compose slot. The outermost position (groups[0], crosstab rows[0])
// has no outer segment, so "within" there is a no-op: it fits over the
// whole set exactly as "global" does.
//
// Predict (internal/descriptor) and the runtime grouper factories
// (internal/processing) both call Parse, so a bad value is refused
// with the same code, message and details on either side. No-execute:
// stdlib + errors + types only.
package groupfit

import (
	"bytes"
	"encoding/json"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Param is the params key.
const Param = "fit"

// The two fit values.
const (
	// Global fits once over the whole filtered set (the default).
	Global = "global"
	// Within refits inside each outer-segment bucket.
	Within = "within"
)

// Values lists the accepted values, default first.
func Values() []string { return []string{Global, Within} }

// Applies reports whether t takes params.fit.
func Applies(t types.GroupType) bool {
	return t == types.GROUP_QUANTILE || t == types.GROUP_KMEANS
}

// Parse reads params.fit of a t grouper. Absent, null, or params that
// are not a JSON object (another validator owns that refusal) read as
// Global. A present value other than "global" / "within" is refused
// PROCESSING_CONFIG with details {type, param, value, valid, reason}.
func Parse(t types.GroupType, raw json.RawMessage) (string, *errors.CodedError) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return Global, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return Global, nil
	}
	v, ok := m[Param]
	if !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
		return Global, nil
	}
	var s string
	if err := json.Unmarshal(v, &s); err == nil && (s == Global || s == Within) {
		return s, nil
	}
	value := string(bytes.TrimSpace(v))
	if s != "" {
		value = s
	}
	return "", errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
		string(t)+` params.fit must be "global" (fit once over the whole filtered set, the default) or "within" (refit inside each bucket of the groupers before it)`,
		map[string]any{"type": string(t), "param": Param, "value": value, "valid": Values(), "reason": "bad_fit"})
}

// IsWithin reports whether g is a data-dependent grouper asking for
// "within". A value Parse refuses reads as Global (validation refuses
// it before any record is partitioned).
func IsWithin(g *types.Group) bool {
	if g == nil || !Applies(g.Type) {
		return false
	}
	mode, err := Parse(g.Type, g.Params)
	return err == nil && mode == Within
}
