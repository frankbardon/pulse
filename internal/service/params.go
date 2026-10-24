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
	if req == nil || len(req.Params) == 0 {
		return s.extensions
	}
	params, cerr := exprenv.NormalizeParams(req.Params)
	if cerr != nil {
		return s.extensions
	}
	return s.extensions.WithParams(params)
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
