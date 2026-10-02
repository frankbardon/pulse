package descriptor

import (
	"fmt"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/temporal"
	"github.com/frankbardon/pulse/types"
)

// ZoneLoader resolves a zone name to a *temporal.Zone. A nil loader
// means temporal.LoadZone; long-lived owners pass a temporal.Cache's
// Load so a request does not rebuild a transition table per call.
type ZoneLoader func(name string) (*temporal.Zone, error)

// zoneSlot is one slot that can carry a per-slot `tz`, flattened out of
// a request so the resolution rules are written once over every slot
// family.
type zoneSlot struct {
	slot     string
	operator string
	field    string
	tz       string
}

// ResolveZones is the single zone-resolution pass for a Request. The
// runtime (internal/service, every execution mode) and predict both
// call it, so a request is refused or accepted identically by both.
//
// Precedence per zone-capable slot: slot `tz` → req.TimeZone →
// defaultZone (pulse.Options.DefaultTimeZone) → "UTC". Rules, in
// evaluation order:
//
//   - an unknown req.TimeZone or defaultZone → PULSE_TIMEZONE_UNKNOWN,
//     whether or not a zone-capable slot is present;
//   - an explicit slot `tz` on an operator that is not zone-capable
//     (every extension operator included) → PROCESSING_CONFIG;
//   - an explicit slot `tz` whose field is in the schema and is not
//     `datetime` (a calendar `date`, or an epoch-day count) →
//     PROCESSING_CONFIG;
//   - an unknown slot `tz` → PULSE_TIMEZONE_UNKNOWN;
//   - an INHERITED zone on a non-`datetime` field is not applied: the
//     slot echoes a null `tz`;
//   - a zone that is not UTC-equivalent (temporal.Zone.IsUTC — "UTC" and
//     the fixed-zero Etc aliases are equivalent) resolving onto a
//     `datetime` field, or onto a field absent from the schema (a
//     derived column that may carry instants), → PROCESSING_CONFIG with
//     details {slot, operator, tz}. Zone-aware operator arithmetic is
//     not implemented yet; refusing keeps a zone from being silently
//     ignored.
//
// It never mutates req. The returned slice is never nil.
func ResolveZones(req *types.Request, schema *encoding.Schema, defaultZone string, load ZoneLoader) ([]descriptor.ResolvedZone, error) {
	if req == nil {
		return []descriptor.ResolvedZone{}, nil
	}
	var slots []zoneSlot
	for i, f := range req.Filterers {
		if f != nil {
			slots = append(slots, zoneSlot{fmt.Sprintf("filterers[%d]", i), string(f.Type), f.Field, f.TimeZone})
		}
	}
	for i, f := range req.Features {
		if f != nil {
			slots = append(slots, zoneSlot{fmt.Sprintf("features[%d]", i), string(f.Type), f.Field, f.TimeZone})
		}
	}
	for i, a := range req.Attributes {
		if a != nil {
			slots = append(slots, zoneSlot{fmt.Sprintf("attributes[%d]", i), string(a.Type), a.Field, a.TimeZone})
		}
	}
	for i, g := range req.Groups {
		if g != nil {
			slots = append(slots, zoneSlot{fmt.Sprintf("groups[%d]", i), string(g.Type), g.Field, g.TimeZone})
		}
	}
	if ct := req.Crosstab; ct != nil {
		for i, g := range ct.Rows {
			if g != nil {
				slots = append(slots, zoneSlot{fmt.Sprintf("crosstab.rows[%d]", i), string(g.Type), g.Field, g.TimeZone})
			}
		}
		for i, g := range ct.Columns {
			if g != nil {
				slots = append(slots, zoneSlot{fmt.Sprintf("crosstab.columns[%d]", i), string(g.Type), g.Field, g.TimeZone})
			}
		}
	}
	return resolveZoneSlots(slots, req.TimeZone, schema, defaultZone, load)
}

// ResolveFacetZones is ResolveZones for a FacetRequest: its filterers
// resolve through slot `tz` → req.TimeZone → defaultZone → "UTC" under
// the same rules.
func ResolveFacetZones(req *types.FacetRequest, schema *encoding.Schema, defaultZone string, load ZoneLoader) ([]descriptor.ResolvedZone, error) {
	if req == nil {
		return []descriptor.ResolvedZone{}, nil
	}
	var slots []zoneSlot
	for i, f := range req.Filterers {
		if f != nil {
			slots = append(slots, zoneSlot{fmt.Sprintf("filterers[%d]", i), string(f.Type), f.Field, f.TimeZone})
		}
	}
	return resolveZoneSlots(slots, req.TimeZone, schema, defaultZone, load)
}

func resolveZoneSlots(slots []zoneSlot, requestZone string, schema *encoding.Schema, defaultZone string, load ZoneLoader) ([]descriptor.ResolvedZone, error) {
	if load == nil {
		load = temporal.LoadZone
	}
	out := []descriptor.ResolvedZone{}

	// The inherited zone: request → options → default. Validated up
	// front so an unknown name is refused even with no capable slot.
	inherited, inheritedSource := "UTC", descriptor.ZoneSourceDefault
	switch {
	case requestZone != "":
		inherited, inheritedSource = requestZone, descriptor.ZoneSourceRequest
	case defaultZone != "":
		inherited, inheritedSource = defaultZone, descriptor.ZoneSourceOptions
	}
	if requestZone != "" {
		if _, err := load(requestZone); err != nil {
			return nil, err
		}
	}
	if defaultZone != "" {
		if _, err := load(defaultZone); err != nil {
			return nil, err
		}
	}

	for _, s := range slots {
		capable := IsZoneCapable(s.operator)
		if !capable {
			if s.tz != "" {
				return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
					fmt.Sprintf("%s: operator %s does not accept `tz`; only zone-capable operators (GROUP_DATE, GROUP_DATE_RANGES, FILTER_DATE_RANGES, ATTR_DATE_PART, FEAT_DATE_FEATURES) take a per-slot time zone", s.slot, s.operator),
					map[string]any{"slot": s.slot, "operator": s.operator, errors.DetailTimeZone: s.tz})
			}
			continue
		}

		fieldType, known := "", false
		if schema != nil && s.field != "" {
			if f := schema.Field(s.field); f != nil {
				fieldType, known = f.Type.String(), true
			}
		}
		instants := !known || fieldType == encoding.FieldTypeDateTime.String()

		name, source := inherited, inheritedSource
		if s.tz != "" {
			if !instants {
				return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
					fmt.Sprintf("%s: `tz` is set on %s over field %q of type %s; a time zone applies only to a datetime field (a calendar date carries no instant)", s.slot, s.operator, s.field, fieldType),
					map[string]any{"slot": s.slot, "operator": s.operator, errors.DetailTimeZone: s.tz, "field": s.field, "field_type": fieldType})
			}
			if _, err := load(s.tz); err != nil {
				if ce, ok := err.(*errors.CodedError); ok {
					details := map[string]any{"slot": s.slot}
					for k, v := range ce.Details {
						details[k] = v
					}
					return nil, errors.NewCodedErrorWithDetails(ce.Code, ce.Message, details)
				}
				return nil, err
			}
			name, source = s.tz, descriptor.ZoneSourceSlot
		}

		rz := descriptor.ResolvedZone{Slot: s.slot, Operator: s.operator, FieldType: fieldType, Source: source}
		if !instants {
			// Inherited zone on a calendar-date field: not applied.
			out = append(out, rz)
			continue
		}
		z, err := load(name)
		if err != nil {
			return nil, err
		}
		if !z.IsUTC() {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				fmt.Sprintf("%s: %s resolves time zone %q (from %s); zone-aware evaluation of datetime fields is not supported yet — only UTC (or a fixed-zero alias such as \"Etc/UTC\") is accepted", s.slot, s.operator, name, source),
				map[string]any{"slot": s.slot, "operator": s.operator, errors.DetailTimeZone: name})
		}
		n := name
		rz.TZ = &n
		out = append(out, rz)
	}
	return out, nil
}

// addCodedError records err on env under its own code (a
// *errors.CodedError keeps its Code and Details), falling back to
// PROCESSING_CONFIG for an uncoded error.
func addCodedError(env *descriptor.Envelope, err error) {
	if ce, ok := err.(*errors.CodedError); ok {
		env.AddError(string(ce.Code), ce.Message, ce.Details)
		return
	}
	env.AddError(string(errors.PROCESSING_CONFIG), err.Error(), nil)
}
