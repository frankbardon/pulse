package types

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// WeightKind names how a row weight is read.
type WeightKind string

const (
	// WeightKindProbability reads a weight as an inverse-probability
	// (sampling / survey) weight: any positive finite value is valid.
	// The default when Kind is empty.
	WeightKindProbability WeightKind = "probability"

	// WeightKindFrequency reads a weight as a replication count: each
	// row stands for w identical rows, so a non-integer value is
	// invalid.
	WeightKindFrequency WeightKind = "frequency"
)

// WeightSpec names a row weight: the cohort field carrying it and how
// the value is read. An empty Kind means WeightKindProbability.
//
// The field must be an unsigned-integer (u4 … u64) or float (f32,
// f64) column of the schema the request executes over — a joined
// (prefixed) name included; anything else is refused before a record
// is read. See .claude/reference/weighting.md.
type WeightSpec struct {
	// Field is the weight column.
	Field string `json:"field"`

	// Kind is "probability" (default when empty) or "frequency".
	Kind WeightKind `json:"kind,omitempty"`
}

// EffectiveKind returns Kind, or WeightKindProbability when empty.
func (w WeightSpec) EffectiveKind() WeightKind {
	if w.Kind == "" {
		return WeightKindProbability
	}
	return w.Kind
}

// SlotWeight is a per-slot weight override with THREE states, because
// on a slot JSON `null` is not the same as an absent key:
//
//   - absent (the zero value) — the slot inherits the request weight,
//     then pulse.Options.DefaultWeight;
//   - null (NullSlotWeight) — the slot opts OUT: it runs unweighted
//     whatever the request or the instance default says;
//   - set (SlotWeightField / SlotWeightOf) — the slot uses its own
//     weight.
//
// On the wire a set weight is either a bare field-name string or a
// {field, kind} object; a weight with no Kind marshals as the bare
// string. The type rides `omitzero`, so an absent slot weight marshals
// exactly as a request written before the slot existed (and hashes
// identically through CanonicalHash).
type SlotWeight struct {
	set  bool
	spec *WeightSpec // nil on a set slot means null (opted out)
}

// SlotWeightField returns a set slot weight on field with the default
// kind.
func SlotWeightField(field string) SlotWeight {
	return SlotWeight{set: true, spec: &WeightSpec{Field: field}}
}

// SlotWeightOf returns a set slot weight carrying spec.
func SlotWeightOf(spec WeightSpec) SlotWeight {
	s := spec
	return SlotWeight{set: true, spec: &s}
}

// NullSlotWeight returns the explicit opt-out: the JSON `null` form.
func NullSlotWeight() SlotWeight {
	return SlotWeight{set: true}
}

// IsZero reports whether the slot weight is absent (inherits). It is
// what `omitzero` consults.
func (w SlotWeight) IsZero() bool { return !w.set }

// IsNull reports whether the slot explicitly opted out (`null`).
func (w SlotWeight) IsNull() bool { return w.set && w.spec == nil }

// Spec returns the slot's own weight, or nil when the slot is absent
// or null. The returned value is a copy.
func (w SlotWeight) Spec() *WeightSpec {
	if !w.set || w.spec == nil {
		return nil
	}
	s := *w.spec
	return &s
}

// MarshalJSON emits `null` for an opted-out slot, a bare field-name
// string for a set weight with no Kind, and a {field, kind} object
// otherwise. An absent slot is never marshalled (omitzero); marshalled
// directly it reads as null.
func (w SlotWeight) MarshalJSON() ([]byte, error) {
	if w.spec == nil {
		return []byte("null"), nil
	}
	if w.spec.Kind == "" {
		return json.Marshal(w.spec.Field)
	}
	return json.Marshal(*w.spec)
}

// UnmarshalJSON accepts `null` (opt out), a field-name string, or a
// {field, kind} object. Any other JSON shape, or an object carrying a
// key other than field / kind, is an error.
func (w *SlotWeight) UnmarshalJSON(b []byte) error {
	trimmed := bytes.TrimSpace(b)
	switch {
	case bytes.Equal(trimmed, []byte("null")):
		*w = NullSlotWeight()
		return nil
	case len(trimmed) > 0 && trimmed[0] == '"':
		var field string
		if err := json.Unmarshal(trimmed, &field); err != nil {
			return err
		}
		*w = SlotWeightField(field)
		return nil
	case len(trimmed) > 0 && trimmed[0] == '{':
		dec := json.NewDecoder(bytes.NewReader(trimmed))
		dec.DisallowUnknownFields()
		var spec WeightSpec
		if err := dec.Decode(&spec); err != nil {
			return fmt.Errorf("weight: %w", err)
		}
		*w = SlotWeightOf(spec)
		return nil
	default:
		return fmt.Errorf("weight: want a field-name string, a {\"field\", \"kind\"} object or null, got %s", trimmed)
	}
}
