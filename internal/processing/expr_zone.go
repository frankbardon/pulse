package processing

import (
	"github.com/frankbardon/pulse/internal/temporal"
	"github.com/frankbardon/pulse/types"
)

// The zone an expression's to_day() reads an instant's local day in.
//
// ATTR_FORMULA and FILTER_EXPRESSION are not zone-capable (an explicit
// slot `tz` on one is refused by the zone resolver), but the request's
// INHERITED zone — request `time_zone`, then Options.DefaultTimeZone —
// is written onto every such slot whose expression calls to_day by
// internal/descriptor.ZonedRequest, the same transport that carries the
// zone to ATTR_DATE_PART. The builders read it back here; an absent or
// UTC-equivalent zone keeps the always-on UTC to_day (exprenv.Bound.Zone
// nil), so a zone-free request compiles exactly as before.

// exprZone loads the slot zone tz for an expression's to_day(): nil for
// "" or a UTC-equivalent zone, else a fork of the shared zone (each
// constructed operator keeps its own lookup cache, as resolveDateField
// does). An unknown tz is PULSE_TIMEZONE_UNKNOWN.
func exprZone(tz string) (*temporal.Zone, error) {
	if tz == "" {
		return nil, nil
	}
	z, err := slotZones.Load(tz)
	if err != nil {
		return nil, err
	}
	if z.IsUTC() {
		return nil, nil
	}
	return z.Fork(), nil
}

// inheritExprZone returns filterers with every FILTER_EXPRESSION slot
// that carries no `tz` copied with tz — the zone a nested filter (a
// fitted attribute's params.fit_on, a centroid reference's filter)
// inherits from the attribute slot holding it. filterers itself is
// returned when tz is "" or no slot changes; it is never mutated.
func inheritExprZone(filterers []*types.Filterer, tz string) []*types.Filterer {
	if tz == "" {
		return filterers
	}
	var out []*types.Filterer
	for i, f := range filterers {
		if f == nil || f.Type != types.FILTER_EXPRESSION || f.TimeZone != "" {
			continue
		}
		if out == nil {
			out = append([]*types.Filterer(nil), filterers...)
		}
		cp := *f
		cp.TimeZone = tz
		out[i] = &cp
	}
	if out == nil {
		return filterers
	}
	return out
}
