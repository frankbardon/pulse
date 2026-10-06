package service

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/temporal"
	"github.com/frankbardon/pulse/types"
)

// SetTimeZones installs the engine-wide default zone name
// (pulse.Options.DefaultTimeZone; empty means UTC) and the zone cache
// every request-time resolution loads through. A nil cache loads
// uncached through temporal.LoadZone.
func (s *Service) SetTimeZones(defaultZone string, cache *temporal.Cache) {
	s.defaultZone = defaultZone
	s.zones = cache
}

// DefaultTimeZone returns the installed default zone name ("" = UTC).
func (s *Service) DefaultTimeZone() string { return s.defaultZone }

// ZoneLoader returns the loader request-time zone resolution uses, so
// the facade's predict resolves through the same cache.
func (s *Service) ZoneLoader() descx.ZoneLoader {
	if s.zones == nil {
		return temporal.LoadZone
	}
	return s.zones.Load
}

// resolveZones runs the single zone-resolution pass
// (internal/descriptor.ResolveZones — the function predict calls) for
// one Request against the schema it executes over. Every execution
// mode calls it exactly once per Request, after smart defaults and
// before any record is read; a refusal is returned as the coded error
// itself. The resolved zones feed zoned, which the mode applies to the
// request it hands its arm.
func (s *Service) resolveZones(req *types.Request, schema *encoding.Schema) ([]descriptor.ResolvedZone, error) {
	zones, err := descx.ResolveZones(req, schema, s.defaultZone, s.ZoneLoader(), s.instance)
	return zones, markLocated(err)
}

// zoned returns the request an execution arm runs
// (internal/descriptor.ZonedRequest): req itself unless an inherited
// non-UTC zone must be written onto a slot, in which case a copy — so
// the caller's request (echo, hashing) never changes. Each mode calls it
// after its last caller-visible mutation of req (auto labels), right
// before dispatching to its arm.
func (s *Service) zoned(req *types.Request, zones []descriptor.ResolvedZone) *types.Request {
	return descx.ZonedRequest(req, zones, s.ZoneLoader())
}

// checkFieldRefs runs the one field-reference rule
// (internal/descriptor.FieldRefRefusal — the rule predict and the
// validators report) for one Request against the schema it executes
// over. Every execution mode calls it right after resolveZones, before
// any record is read, so an unknown name is refused instead of reading
// as an all-null column. A located refusal: Compose adds
// details.request, a chain details.stage.
//
// Weight resolution (resolveWeights) runs here too, right after the
// references pass, so every caller resolves weights at the same point
// predict does.
func (s *Service) checkFieldRefs(req *types.Request, schema *encoding.Schema) error {
	if err := descx.ScopedFieldRefRefusal(req, schema, s.instance); err != nil {
		return markLocated(err)
	}
	// `return` data columns — the rule predict applies right after the
	// field-reference walk, on the same defaults-resolved request.
	if err := descx.ReturnColumnRefusal(req, schema, s.instance); err != nil {
		return markLocated(err)
	}
	return s.resolveWeights(req, schema)
}

// checkFacetFieldRefs is checkFieldRefs for a FacetRequest's
// filterers (internal/descriptor.FacetFieldRefRefusals — the rule
// ValidateFacet reports). FacetSchema calls it right after
// resolveFacetZones, before any record is read.
func (s *Service) checkFacetFieldRefs(req *types.FacetRequest, schema *encoding.Schema) error {
	if all := descx.ScopedFacetFieldRefRefusals(req, schema, s.instance); len(all) > 0 {
		return markLocated(all[0])
	}
	return nil
}

// resolveFacetZones is resolveZones for a FacetRequest's filterers.
func (s *Service) resolveFacetZones(req *types.FacetRequest, schema *encoding.Schema) ([]descriptor.ResolvedZone, error) {
	zones, err := descx.ResolveFacetZones(req, schema, s.defaultZone, s.ZoneLoader(), s.instance)
	return zones, markLocated(err)
}
