package service

import (
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// optimizeRefusal is the batch's `optimize` rules (descx.OptimizeRefusal,
// predict's too) against this instance's embedder ExprFunctions and
// LookupTables: nil when the batch declares no block or every block
// passes. It runs right after model.CheckFits, before any fit or slot.
func (s *Service) optimizeRefusal(composed *types.ComposedRequest) error {
	if len(composed.Optimize) == 0 {
		return nil
	}
	var names []string
	lookup := false
	if s.extensions != nil {
		for _, fn := range s.extensions.ExprFunctions {
			names = append(names, fn.Name)
		}
		lookup = len(s.extensions.LookupTables) > 0
	}
	if cerr := descx.OptimizeRefusal(composed, names, lookup); cerr != nil {
		return cerr
	}
	return nil
}
