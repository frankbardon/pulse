package service

import (
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
// itself.
func (s *Service) resolveZones(req *types.Request, schema *encoding.Schema) error {
	_, err := descx.ResolveZones(req, schema, s.defaultZone, s.ZoneLoader())
	return markLocated(err)
}

// resolveFacetZones is resolveZones for a FacetRequest's filterers.
func (s *Service) resolveFacetZones(req *types.FacetRequest, schema *encoding.Schema) error {
	_, err := descx.ResolveFacetZones(req, schema, s.defaultZone, s.ZoneLoader())
	return markLocated(err)
}
