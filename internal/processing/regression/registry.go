package regression

import (
	"context"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
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

// ContextBufferedEngine is a BufferedEngine whose fit can run long — a
// Resample / Selection modifier refitting the model many times — and
// so reads the request ctx between refits: a request deadline
// (Options.Limits.RequestTimeout) or a cancel stops it with the ctx's
// own error. FitEachBufferedWithContext prefers it over FitBuffered.
type ContextBufferedEngine interface {
	BufferedEngine
	FitBufferedContext(ctx context.Context, records []Record) (*types.RegressionResult, error)
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

// Resolver resolves a regression type to its factory. The engine passes
// processing.ExtensionRegistry.LookupRegression, which answers a type
// the instance feature set hides as never registered. A nil Resolver is
// the built-in registry alone (Lookup).
type Resolver func(t types.RegressionType) (Factory, bool)

func (r Resolver) lookup(t types.RegressionType) (Factory, bool) {
	if r == nil {
		return Lookup(t)
	}
	return r(t)
}

// Build returns an Engine for each spec. Unknown types surface
// PROCESSING_CONFIG; per-spec factory errors propagate untouched. This
// is the orchestrator's entry point — both streaming and buffered
// paths invoke Build first, then route the returned engines through
// the appropriate execution interface. Equivalent to BuildWith with a
// nil Resolver.
func Build(specs []*types.RegressionSpec, schema *encoding.Schema) ([]Engine, error) {
	return BuildWith(specs, schema, nil)
}

// BuildWith is Build resolving each type through resolve. A type the
// resolver misses surfaces the same PROCESSING_CONFIG
// "unknown regression type" error as a type nothing registered.
func BuildWith(specs []*types.RegressionSpec, schema *encoding.Schema, resolve Resolver) ([]Engine, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	out := make([]Engine, 0, len(specs))
	for _, s := range specs {
		factory, ok := resolve.lookup(s.Type)
		if !ok {
			return nil, errors.NewCodedError(
				errors.PROCESSING_CONFIG,
				"unknown regression type: "+string(s.Type),
			)
		}
		if cerr := regressionFieldTypeRefusal(s, schema); cerr != nil {
			return nil, cerr
		}
		eng, err := factory(s, schema)
		if err != nil {
			return nil, err
		}
		out = append(out, eng)
	}
	return out, nil
}

// regressionFieldTypeRefusal is the accepted-types table's refusal
// (internal/fieldaccept) for s's target and predictors — a categorical's
// dictionary index or a set's bitmask is not a regressor — nil when
// every schema field is accepted. A name the schema lacks (a derived
// column) is not judged here. Predict reports the same refusal.
func regressionFieldTypeRefusal(s *types.RegressionSpec, schema *encoding.Schema) *errors.CodedError {
	op := string(s.Type)
	if cerr := fieldaccept.Check(op, fieldaccept.SlotTarget, s.Target, schema); cerr != nil {
		return cerr
	}
	for _, name := range s.Predictors {
		if cerr := fieldaccept.Check(op, fieldaccept.SlotPredictor, name, schema); cerr != nil {
			return cerr
		}
	}
	return nil
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
	return FitBufferedWith(specs, schema, records, nil)
}

// FitBufferedWith is FitBuffered resolving each type through resolve:
// the first per-spec fit error short-circuits and propagates.
func FitBufferedWith(specs []*types.RegressionSpec, schema *encoding.Schema, records []Record, resolve Resolver) ([]*types.RegressionResult, error) {
	out, errs, err := FitEachBufferedWith(specs, schema, records, resolve)
	if err != nil {
		return nil, err
	}
	for _, e := range errs {
		if e != nil {
			return nil, e
		}
	}
	return out, nil
}

// FitEachBufferedWith builds every engine (a Build error — a
// configuration fault — is returned as err) and then fits EACH one,
// never stopping at a failed fit: results and fitErrs are index-aligned
// with specs, a failed spec holding a nil result and its error. The
// processor turns a data-dependent fit error into that slot's
// per-slot failure (internal/slotfail) and returns any other as fatal.
func FitEachBufferedWith(specs []*types.RegressionSpec, schema *encoding.Schema, records []Record, resolve Resolver) (results []*types.RegressionResult, fitErrs []error, err error) {
	return FitEachBufferedWithContext(context.Background(), specs, schema, records, resolve)
}

// FitEachBufferedWithContext is FitEachBufferedWith under ctx: an
// engine implementing ContextBufferedEngine fits through
// FitBufferedContext, so its refit loop honours ctx.
func FitEachBufferedWithContext(ctx context.Context, specs []*types.RegressionSpec, schema *encoding.Schema, records []Record, resolve Resolver) (results []*types.RegressionResult, fitErrs []error, err error) {
	engines, err := BuildWith(specs, schema, resolve)
	if err != nil {
		return nil, nil, err
	}
	if len(engines) == 0 {
		return nil, nil, nil
	}
	results = make([]*types.RegressionResult, len(engines))
	fitErrs = make([]error, len(engines))
	for i, eng := range engines {
		if ce, ok := eng.(ContextBufferedEngine); ok {
			results[i], fitErrs[i] = ce.FitBufferedContext(ctx, records)
			continue
		}
		if be, ok := eng.(BufferedEngine); ok {
			results[i], fitErrs[i] = be.FitBuffered(records)
			continue
		}
		// Not-implemented stubs ignore the records and surface the
		// canonical PROCESSING_REGRESSION_NOT_IMPLEMENTED via Fit.
		results[i], fitErrs[i] = eng.Fit()
	}
	return results, fitErrs, nil
}

// BuildStreaming constructs StreamingEngine handles for the single-pass
// orchestrator. Specs whose engines do not yet implement StreamingEngine
// are rejected — callers are expected to gate on CanStreamRequest /
// RegressionSpec.Streamable() before reaching this entry point.
//
// Returns nil, nil for an empty spec slice so the orchestrator can call
// it unconditionally on streaming requests.
func BuildStreaming(specs []*types.RegressionSpec, schema *encoding.Schema) ([]StreamingEngine, error) {
	return BuildStreamingWith(specs, schema, nil)
}

// BuildStreamingWith is BuildStreaming resolving each type through
// resolve.
func BuildStreamingWith(specs []*types.RegressionSpec, schema *encoding.Schema, resolve Resolver) ([]StreamingEngine, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	engines, err := BuildWith(specs, schema, resolve)
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
	// Phase 3: REG_GLM lights up via buffered IRLS: binomial+logit,
	// poisson+log and gamma+inverse (its dispersion estimated), each
	// pinned to R glm.
	regressionRegistry[types.REG_GLM] = newGLMEngine
	// Phase 4: REG_BAYES_LINEAR lights up via streaming sufficient stats
	// and a conjugate Normal-Inverse-Gamma posterior at finalize. The
	// factory closure routes modifier specs (Resample / Selection) back
	// to the not-implemented stub for Phase 5.
	regressionRegistry[types.REG_BAYES_LINEAR] = newBayesLinearEngine
}
