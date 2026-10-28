package service

import (
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// Request parameters (types.Request.Params, U58 E3-S1).
//
// A run of a request declaring params compiles every expression —
// ATTR_FORMULA, FILTER_EXPRESSION, total() arguments, a regression
// holdout, OVERLAY_FORMULA — through a registry that folds them as
// constants (processing.ExtensionRegistry.WithParams over
// exprenv.Bound.Params). The values are validated, and every params
// refusal raised, by the field-reference walk (checkFieldRefs —
// predict's walk) before any build; here an invalid block binds
// nothing, so a path that compiled anyway meets the unknown name, never
// a guessed value.

// requestExtensions is s.extensions folding req's params (s.extensions
// itself when req declares none or the block is invalid).
func (s *Service) requestExtensions(req *types.Request) *processing.ExtensionRegistry {
	if req == nil {
		return s.extensions
	}
	return s.paramsExtensions(req.Params)
}

// paramsExtensions is s.extensions folding params (s.extensions itself
// when params is empty or invalid — the walk refused it already).
func (s *Service) paramsExtensions(p types.Params) *processing.ExtensionRegistry {
	if len(p) == 0 {
		return s.extensions
	}
	params, cerr := exprenv.NormalizeParams(p)
	if cerr != nil {
		return s.extensions
	}
	return s.extensions.WithParams(params)
}

// forFacet is forRequest for a FacetRequest (FacetRequest.Params): its
// base and additive filterers, labels and overlays all compile through
// the folding registry.
func (s *Service) forFacet(req *types.FacetRequest) *Service {
	if req == nil {
		return s
	}
	exts := s.paramsExtensions(req.Params)
	if exts == s.extensions {
		return s
	}
	cp := *s
	cp.extensions = exts
	return &cp
}

// forRequest is the Service one run of req executes on: s itself when
// req declares no params (byte-identical), else a shallow copy whose
// registry folds them, so every arm, pre-pass and overlay pass of the
// run — whichever reads s.extensions — compiles with the request's
// values, and concurrent runs never share them. The copy shares every
// pointer-held cache (zones, fingerprints) and is never stored.
func (s *Service) forRequest(req *types.Request) *Service {
	exts := s.requestExtensions(req)
	if exts == s.extensions {
		return s
	}
	cp := *s
	cp.extensions = exts
	return &cp
}

// composeExtensions is s.extensions folding the batch's top-level params
// (types.ComposedRequest.Params, U58 E3-S2) — the registry the
// Compose-host overlay fold compiles an OVERLAY_FORMULA through, so a
// params.<name> read there sees the same values every slot inherited.
// s.extensions itself when the batch declares none (or the block is
// invalid: descx.InheritComposeParams refused it before any slot ran).
func (s *Service) composeExtensions(c *types.ComposedRequest) *processing.ExtensionRegistry {
	if c == nil {
		return s.extensions
	}
	return s.paramsExtensions(c.Params)
}
