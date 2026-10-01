package regression

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// regressionRegistry maps RegressionType to its Factory. Per-operator
// init() functions register here; Phase 0 wires every type to the
// notImplemented stub. Phase 1 replaces REG_OLS with the unpenalized
// olsEngine factory; that factory itself routes penalized / modifier
// requests back to the not-implemented stub so the legacy code paths
// continue to surface PROCESSING_REGRESSION_NOT_IMPLEMENTED until
// Phases 2-5 land their dedicated engines.
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

// BufferedEngine is the Phase 1 entry point for buffered-orchestrator
// regression fits. Implementations consume an in-memory record slice
// and emit the populated result. Phase 0 stub engines ignore the slice
// and return PROCESSING_REGRESSION_NOT_IMPLEMENTED; the unpenalized
// olsEngine folds every record through its Welford accumulator.
type BufferedEngine interface {
	Engine
	FitBuffered(records []Record) (*types.RegressionResult, error)
}

// StreamingEngine is the Phase 1 entry point for single-pass orchestrator
// regression fits. Implementations consume records one at a time via
// UpdateRow, then close the fit via Finalize. The orchestrator wraps
// the same record stream that drives streamable aggregators.
//
// Only the unpenalized OLS engine implements this interface today.
// Phase 2 (regularized OLS), Phase 4 (Bayes), and Phases 5+ (modifiers)
// will expand the set.
type StreamingEngine interface {
	UpdateRow(rec Record) error
	Finalize() (*types.RegressionResult, error)
}

// Build returns an Engine for each spec. Unknown types surface
// PROCESSING_CONFIG; per-spec factory errors propagate untouched. This
// is the orchestrator's entry point — both streaming and buffered
// paths invoke Build first, then route the returned engines through
// the appropriate execution interface.
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
// PROCESSING_REGRESSION_NOT_IMPLEMENTED from Fit. Phase 1 retires this
// path for the buffered orchestrator: callers now use FitBuffered to
// pass in the filtered record slice. Fit() is retained for callers
// that have no records (legacy / unit tests); engines that need rows
// return PROCESSING_REGRESSION_INSUFFICIENT_DATA from Fit() since their
// accumulator is empty.
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

// FitBuffered is the Phase 1 buffered-orchestrator entry point. It
// builds engines, then for each engine routes through FitBuffered with
// the filtered record slice. Engines that implement BufferedEngine
// consume the records; legacy not-implemented engines ignore the slice
// and surface PROCESSING_REGRESSION_NOT_IMPLEMENTED via Fit().
//
// The records slice is shared across every spec — every engine sees
// the same filtered set, so per-spec listwise null filtering happens
// inside the engine (callers don't pre-filter per spec).
func FitBuffered(specs []*types.RegressionSpec, schema *encoding.Schema, records []Record) ([]*types.RegressionResult, error) {
	engines, err := Build(specs, schema)
	if err != nil {
		return nil, err
	}
	if len(engines) == 0 {
		return nil, nil
	}
	out := make([]*types.RegressionResult, 0, len(engines))
	for _, eng := range engines {
		if be, ok := eng.(BufferedEngine); ok {
			res, err := be.FitBuffered(records)
			if err != nil {
				return nil, err
			}
			out = append(out, res)
			continue
		}
		// Not-implemented stubs ignore the records and surface the
		// canonical PROCESSING_REGRESSION_NOT_IMPLEMENTED via Fit.
		res, err := eng.Fit()
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	return out, nil
}

// BuildStreaming constructs StreamingEngine handles for the single-pass
// orchestrator. Specs whose engines do not yet implement StreamingEngine
// are rejected — callers are expected to gate on CanStreamRequest /
// RegressionSpec.Streamable() before reaching this entry point.
//
// Returns nil, nil for an empty spec slice so the orchestrator can call
// it unconditionally on streaming requests.
func BuildStreaming(specs []*types.RegressionSpec, schema *encoding.Schema) ([]StreamingEngine, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	engines, err := Build(specs, schema)
	if err != nil {
		return nil, err
	}
	out := make([]StreamingEngine, 0, len(engines))
	for i, eng := range engines {
		se, ok := eng.(StreamingEngine)
		if !ok {
			return nil, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_INTERNAL,
				"regression engine does not implement StreamingEngine; orchestrator should have routed buffered",
				map[string]any{"type": string(specs[i].Type), "name": specs[i].Name},
			)
		}
		out = append(out, se)
	}
	return out, nil
}

// init wires every registered RegressionType to its Phase 0 stub, then
// upgrades the operators whose engines have landed. Phase 1 replaces
// REG_OLS with newOLSEngine; that constructor falls back to the
// not-implemented stub for penalized / modifier specs so the
// not-implemented error surface stays intact.
func init() {
	for _, rt := range types.AllRegressionTypes() {
		register(rt, newNotImplemented)
	}
	// Phase 1: unpenalized OLS lights up. The factory closure routes
	// penalized / modifier specs back to the not-implemented stub.
	regressionRegistry[types.REG_OLS] = newOLSEngine
	// Phase 3: REG_GLM lights up via buffered IRLS. Implementations for
	// binomial+logit and poisson+log are numerically validated;
	// gamma+inverse is wired but tests are deferred.
	regressionRegistry[types.REG_GLM] = newGLMEngine
	// Phase 4: REG_BAYES_LINEAR lights up via streaming sufficient stats
	// and a conjugate Normal-Inverse-Gamma posterior at finalize. The
	// factory closure routes modifier specs (Resample / Selection) back
	// to the not-implemented stub for Phase 5.
	regressionRegistry[types.REG_BAYES_LINEAR] = newBayesLinearEngine
}
