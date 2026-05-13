package regression

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// regressionRegistry maps RegressionType to its Factory. Per-operator
// init() functions register here; Phase 0 wires every type to the
// notImplemented stub.
var regressionRegistry = map[types.RegressionType]Factory{}

// register installs a factory. Duplicate registration panics at boot so
// the wiring drift surfaces immediately.
func register(t types.RegressionType, f Factory) {
	if _, exists := regressionRegistry[t]; exists {
		panic("regression: duplicate registration for " + string(t))
	}
	regressionRegistry[t] = f
}

// Lookup returns the factory for a regression type, if registered.
func Lookup(t types.RegressionType) (Factory, bool) {
	f, ok := regressionRegistry[t]
	return f, ok
}

// RegisteredTypes returns every registered regression type. Order is
// map-iteration order; callers that need determinism sort the result.
func RegisteredTypes() []types.RegressionType {
	out := make([]types.RegressionType, 0, len(regressionRegistry))
	for k := range regressionRegistry {
		out = append(out, k)
	}
	return out
}

// Build returns an Engine for each spec. Unknown types surface
// PROCESSING_CONFIG; per-spec factory errors propagate untouched. This
// is the parent processing package's entry point — it consumes Build
// during Phase 1+ orchestration without depending on per-operator
// internals.
func Build(specs []*types.RegressionSpec, schema *encoding.Schema) ([]Engine, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	out := make([]Engine, 0, len(specs))
	for _, s := range specs {
		factory, ok := regressionRegistry[s.Type]
		if !ok {
			return nil, errors.NewCodedError(
				errors.PROCESSING_CONFIG,
				"unknown regression type: "+string(s.Type),
			)
		}
		eng, err := factory(s, schema)
		if err != nil {
			return nil, err
		}
		out = append(out, eng)
	}
	return out, nil
}

// Fit drives Build then invokes Fit on every engine in order. Returns
// (results, nil) on success; the first per-spec error short-circuits
// the loop and propagates to the caller.
//
// Phase 0 contract: every spec returns
// PROCESSING_REGRESSION_NOT_IMPLEMENTED from Fit, so this function
// returns that error for any non-empty specs slice. Callers handle the
// error by surfacing it on the response envelope without populating
// Response.Regressions.
func Fit(specs []*types.RegressionSpec, schema *encoding.Schema) ([]*types.RegressionResult, error) {
	engines, err := Build(specs, schema)
	if err != nil {
		return nil, err
	}
	if len(engines) == 0 {
		return nil, nil
	}
	out := make([]*types.RegressionResult, 0, len(engines))
	for _, eng := range engines {
		res, err := eng.Fit()
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	return out, nil
}

// init wires every registered RegressionType to the Phase 0
// not-implemented stub. Phases 1–4 replace these calls with real
// factories per operator file.
func init() {
	for _, rt := range types.AllRegressionTypes() {
		register(rt, newNotImplemented)
	}
}
