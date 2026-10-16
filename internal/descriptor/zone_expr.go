package descriptor

import (
	"bytes"
	"slices"

	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/exprident"
	"github.com/frankbardon/pulse/internal/temporal"
	"github.com/frankbardon/pulse/types"
)

// The expression zone transport (U45, TODO #316).
//
// to_day(ts) reads an instant's local day in the request's INHERITED
// zone — request `time_zone`, then Options.DefaultTimeZone, then UTC —
// the resolution ATTR_DATE_PART inherits. The expression slots are not
// zone-capable (ResolveZones still refuses an explicit `tz` on one, and
// lists none in PredictResult.TimeZones), so the zone rides on the slot
// the way ZonedRequest carries it to the date-family operators: an
// execution arm builds its operators from the returned request and the
// engine's builders read the slot `tz` back. Predict needs no zone — it
// never runs to_day, and the inherited zone's name was validated by
// ResolveZones (an unknown one is PULSE_TIMEZONE_UNKNOWN on both sides).

// ZonedExpressions returns the Request an execution arm runs with the
// inherited zone written onto every expression slot that calls to_day:
// an ATTR_FORMULA, a FILTER_EXPRESSION filterer, and any other attribute
// that is not zone-capable whose params carry a nested filter calling it
// (a fitted attribute's params.fit_on, a centroid reference's filter —
// the engine hands its slot `tz` down to those nested
// FILTER_EXPRESSIONs). req itself is returned when the inherited zone is
// UTC-equivalent (or unknown — ResolveZones refused it already) or no
// slot calls to_day; otherwise a shallow copy whose written slots are
// copies. req is never mutated.
func ZonedExpressions(req *types.Request, defaultZone string, load ZoneLoader) *types.Request {
	if req == nil {
		return req
	}
	tz := inheritedExprZone(req.TimeZone, defaultZone, load)
	if tz == "" {
		return req
	}
	var c *types.Request
	copyReq := func() {
		if c == nil {
			cp := *req
			c = &cp
		}
	}
	if fs, ok := zonedExprFilterers(req.Filterers, tz); ok {
		copyReq()
		c.Filterers = fs
	}
	var attrs []*types.Attribute
	for i, a := range req.Attributes {
		if a == nil || a.TimeZone != "" || IsZoneCapable(string(a.Type)) || !attributeCallsToDay(a) {
			continue
		}
		if attrs == nil {
			attrs = append([]*types.Attribute(nil), req.Attributes...)
		}
		cp := *a
		cp.TimeZone = tz
		attrs[i] = &cp
	}
	if attrs != nil {
		copyReq()
		c.Attributes = attrs
	}
	if c == nil {
		return req
	}
	return c
}

// ZonedFacetExpressions is ZonedExpressions for a FacetRequest's
// filterers.
func ZonedFacetExpressions(req *types.FacetRequest, defaultZone string, load ZoneLoader) *types.FacetRequest {
	if req == nil {
		return req
	}
	tz := inheritedExprZone(req.TimeZone, defaultZone, load)
	if tz == "" {
		return req
	}
	fs, ok := zonedExprFilterers(req.Filterers, tz)
	if !ok {
		return req
	}
	c := *req
	c.Filterers = fs
	return &c
}

// inheritedExprZone is the inherited zone's name — requestZone, else
// defaultZone — or "" when it is absent, UTC-equivalent or unknown.
func inheritedExprZone(requestZone, defaultZone string, load ZoneLoader) string {
	name := requestZone
	if name == "" {
		name = defaultZone
	}
	if name == "" {
		return ""
	}
	if load == nil {
		load = temporal.LoadZone
	}
	z, err := load(name)
	if err != nil || z.IsUTC() {
		return ""
	}
	return name
}

// zonedExprFilterers copies every FILTER_EXPRESSION in fs that calls
// to_day and carries no `tz` with tz; ok reports whether any was.
func zonedExprFilterers(fs []*types.Filterer, tz string) ([]*types.Filterer, bool) {
	var out []*types.Filterer
	for i, f := range fs {
		if f == nil || f.Type != types.FILTER_EXPRESSION || f.TimeZone != "" || !callsToDay(f.Expression) {
			continue
		}
		if out == nil {
			out = append([]*types.Filterer(nil), fs...)
		}
		cp := *f
		cp.TimeZone = tz
		out[i] = &cp
	}
	return out, out != nil
}

// attributeCallsToDay reports whether an attribute slot's expression
// calls to_day, or its params mention it (a nested filter's expression;
// a mention in any other param only writes a zone nothing reads).
func attributeCallsToDay(a *types.Attribute) bool {
	return callsToDay(a.Expression) || bytes.Contains(a.Params, []byte(exprenv.FuncToDay))
}

// callsToDay reports whether src calls to_day (total() arguments
// included: they are part of the expression).
func callsToDay(src string) bool {
	if src == "" {
		return false
	}
	callees, _ := exprident.Callees(src)
	return slices.Contains(callees, exprenv.FuncToDay)
}
