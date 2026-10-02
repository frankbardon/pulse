package descriptor

import (
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// request_slots.go is the request-slot gate: a capability-gated slot
// the instance hides is refused exactly as an unrecognised top-level
// JSON key, with PULSE_REQUEST_UNKNOWN_FIELD in the strict-decode shape
// ({unknown_keys, suggestions, valid_keys}). A Go caller cannot misspell
// a slot, so "unknown" for a library caller means "set, but not offered
// by this instance"; the JSON caller and the Go caller see one error.
//
// The gated slots and their owning features:
//
//   - Request.Crosstab  — capability:crosstab
//   - Request.Joins     — capability:joins
//   - Request.Overlays  — an overlay host a plain Request reaches:
//     capability:crosstab (the MATRIX host) or capability:compose (the
//     SERIES host a grouped Process runs)
//   - ComposedRequest.Overlays — capability:compose
//   - ChainRequest.Overlays    — capability:process_chain
//   - FacetRequest.Overlays    — capability:facet
//
// An overlay slot is visible iff at least one of its hosts is enabled
// AND that host lists at least one enabled overlay kind
// (overlayHostKinds). A host with no runnable kind offers nothing to put
// in the slot, so the slot is hidden with it. Nil / unscoped instances
// hide nothing.

// gatedSlot is one capability-gated top-level JSON slot.
type gatedSlot struct {
	key     string
	visible func(inst *InstanceSnapshot) bool
}

// capabilityGate makes a slot visible iff the capability is enabled.
func capabilityGate(capability string) func(*InstanceSnapshot) bool {
	return func(inst *InstanceSnapshot) bool { return inst.Enabled(capability) }
}

// overlayGate makes an overlay slot visible iff some host is enabled and
// lists at least one enabled overlay kind.
func overlayGate(hosts ...string) func(*InstanceSnapshot) bool {
	return func(inst *InstanceSnapshot) bool {
		for _, h := range hosts {
			if !inst.Enabled(h) {
				continue
			}
			for _, k := range overlayHostKinds[h] {
				if inst.Enabled(k) {
					return true
				}
			}
		}
		return false
	}
}

// gatedSlots maps each request root (by its Go type) to its gated slots.
var gatedSlots = map[reflect.Type][]gatedSlot{
	reflect.TypeOf(types.Request{}): {
		{key: "crosstab", visible: capabilityGate(featCrosstab)},
		{key: "joins", visible: capabilityGate(featJoins)},
		{key: "overlays", visible: overlayGate(featCrosstab, featCompose)},
	},
	reflect.TypeOf(types.ComposedRequest{}): {
		{key: "overlays", visible: overlayGate(featCompose)},
	},
	reflect.TypeOf(types.ChainRequest{}): {
		{key: "overlays", visible: overlayGate(featProcessChain)},
	},
	reflect.TypeOf(types.FacetRequest{}): {
		{key: "overlays", visible: overlayGate(featFacet)},
	},
}

// structType resolves sample (a struct or a pointer to one) to its
// struct type, or nil.
func structType(sample any) reflect.Type {
	t := reflect.TypeOf(sample)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return nil
	}
	return t
}

// JSONObjectKeys returns the top-level JSON object keys declared by the
// struct (or pointer to struct) sample, read from each exported field's
// json tag in declaration order. Fields tagged "-" are skipped; an
// untagged field contributes its Go name.
func JSONObjectKeys(sample any) []string {
	t := structType(sample)
	if t == nil {
		return nil
	}
	keys := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "" {
			name = f.Name
		}
		keys = append(keys, name)
	}
	return keys
}

// HiddenSlotKeys returns the top-level JSON keys of the request root
// sample (types.Request, ComposedRequest, ChainRequest, FacetRequest —
// struct or pointer) that inst hides, sorted. Nil when none is hidden,
// on a nil / unscoped instance, or for a root with no gated slot. The
// payload-schema and manifest filters drop exactly these properties.
func HiddenSlotKeys(sample any, inst *InstanceSnapshot) []string {
	var out []string
	for _, g := range gatedSlots[structType(sample)] {
		if !g.visible(inst) {
			out = append(out, g.key)
		}
	}
	sort.Strings(out)
	return out
}

// VisibleSlotKeys returns JSONObjectKeys(sample) minus HiddenSlotKeys,
// in declaration order — the instance's valid top-level keys.
func VisibleSlotKeys(sample any, inst *InstanceSnapshot) []string {
	hidden := HiddenSlotKeys(sample, inst)
	keys := JSONObjectKeys(sample)
	if len(hidden) == 0 {
		return keys
	}
	out := keys[:0]
	for _, k := range keys {
		if !slices.Contains(hidden, k) {
			out = append(out, k)
		}
	}
	return out
}

// UnknownFieldError builds the PULSE_REQUEST_UNKNOWN_FIELD refusal for
// the unrecognised top-level keys unknown against the valid key set
// valid: details {unknown_keys (sorted), suggestions (unknown key →
// nearest valid key, when one is close), valid_keys (sorted)} and the
// message naming both lists. Nil when unknown is empty. It is THE shape
// of the error — the MCP strict decoder and the slot gate both build it
// here, so a hidden slot and an unknown key cannot drift apart.
func UnknownFieldError(unknown, valid []string) *errors.CodedError {
	if len(unknown) == 0 {
		return nil
	}
	unknown = append([]string(nil), unknown...)
	sort.Strings(unknown)
	validList := append([]string(nil), valid...)
	sort.Strings(validList)

	suggestions := make(map[string]any, len(unknown))
	parts := make([]string, 0, len(unknown))
	for _, k := range unknown {
		if near := NearestKey(k, valid); near != "" {
			suggestions[k] = near
			parts = append(parts, `"`+k+`" (did you mean "`+near+`"?)`)
		} else {
			parts = append(parts, `"`+k+`"`)
		}
	}

	msg := "request contains unrecognized top-level key(s): " + strings.Join(parts, ", ") +
		". Unknown keys are ignored during decode, so the intended operation is silently dropped. Valid request keys: " +
		strings.Join(validList, ", ") + "."

	return errors.NewCodedErrorWithDetails(errors.PULSE_REQUEST_UNKNOWN_FIELD, msg, map[string]any{
		"unknown_keys": unknown,
		"suggestions":  suggestions,
		"valid_keys":   validList,
	})
}

// NearestKey returns the closest candidate to k by Levenshtein distance,
// or "" when no candidate is close enough. The acceptance threshold is
// edit distance < 4, further capped at the key length, so short or
// unrelated keys do not yield a misleading suggestion. Ties keep the
// earliest candidate.
func NearestKey(k string, candidates []string) string {
	limit := min(4, len(k))
	best := ""
	bestDist := limit
	for _, c := range candidates {
		if d := levenshtein(k, c); d < bestDist {
			bestDist = d
			best = c
		}
	}
	return best
}

// setSlots returns the gated keys a root actually sets (non-nil /
// non-empty). Only the gated slots are probed.
func setSlots(v any) []string {
	var out []string
	switch r := v.(type) {
	case *types.Request:
		if r == nil {
			return nil
		}
		if r.Crosstab != nil {
			out = append(out, "crosstab")
		}
		if len(r.Joins) > 0 {
			out = append(out, "joins")
		}
		if len(r.Overlays) > 0 {
			out = append(out, "overlays")
		}
	case *types.ComposedRequest:
		if r != nil && len(r.Overlays) > 0 {
			out = append(out, "overlays")
		}
	case *types.ChainRequest:
		if r != nil && len(r.Overlays) > 0 {
			out = append(out, "overlays")
		}
	case *types.FacetRequest:
		if r != nil && len(r.Overlays) > 0 {
			out = append(out, "overlays")
		}
	}
	return out
}

// rootSlotRefusal gates ONE root's own top-level slots (no nesting).
func rootSlotRefusal(v any, inst *InstanceSnapshot) *errors.CodedError {
	if !inst.Scoped() {
		return nil
	}
	hidden := HiddenSlotKeys(v, inst)
	if len(hidden) == 0 {
		return nil
	}
	var unknown []string
	for _, k := range setSlots(v) {
		if slices.Contains(hidden, k) {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	return UnknownFieldError(unknown, VisibleSlotKeys(v, inst))
}

// SlotRefusal is the request-slot gate. v is a *types.Request,
// *types.ComposedRequest, *types.ChainRequest or *types.FacetRequest
// (any other value — a *types.SampleRequest, nil — passes). It returns
// PULSE_REQUEST_UNKNOWN_FIELD when v sets a slot inst hides, reporting
// every hidden slot set on the first offending root:
//
//   - the root's own slots first;
//   - then, for a ComposedRequest, each Requests[i] in order with
//     details.request = i; for a ChainRequest each Stages[i].Request
//     with details.stage = i (the keys the service's located refusals
//     use, descx.RefusalAt).
//
// valid_keys and suggestions range over the offending root's VISIBLE
// keys only. Nil on a nil / unscoped instance. Every service funnel and
// Predict run it before any other request check, so a hidden slot is
// refused at the point an unknown JSON key would have been.
func SlotRefusal(v any, inst *InstanceSnapshot) error {
	if !inst.Scoped() {
		return nil
	}
	if ce := rootSlotRefusal(v, inst); ce != nil {
		return ce
	}
	switch r := v.(type) {
	case *types.ComposedRequest:
		if r == nil {
			return nil
		}
		for i, req := range r.Requests {
			if ce := rootSlotRefusal(req, inst); ce != nil {
				return RefusalAt(ce, "request", i)
			}
		}
	case *types.ChainRequest:
		if r == nil {
			return nil
		}
		for i, st := range r.Stages {
			if st == nil {
				continue
			}
			if ce := rootSlotRefusal(st.Request, inst); ce != nil {
				return RefusalAt(ce, "stage", i)
			}
		}
	}
	return nil
}
