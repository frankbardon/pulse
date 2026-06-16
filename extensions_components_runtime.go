package pulse

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
)

// This file wires ComponentsFunc registrations into the runtime by
// wrapping each extension factory so the returned instance satisfies
// the matching processing.Meta* sibling interface. The orchestrator's
// type assertions (e.g. `agg.(processing.MetaAggregator)`) then
// succeed for extension operators on the same dispatch path the
// built-ins use.
//
// Three wrapper shapes — one per category. All embed the underlying
// interface so other Pulse-side type assertions (OnlineAggregator,
// MergeableAggregator, RichAggregator, StreamingGrouper, etc.) still
// land on the original instance via interface-embedded method
// promotion. When the registration omits ComponentsFunc the wrapper is
// not installed — the factory is registered verbatim and the
// orchestrator's type assertion to Meta* simply returns false (the
// floor-only path).
//
// Probe-validation mismatches surface as
// PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH; runtime emission errors
// ride the same routing as Aggregate / Finalize.

// metaAggregatorWrapper wraps an embedder-supplied Aggregator so the
// orchestrator's processing.MetaAggregator assertion succeeds. The
// wrapper embeds the Aggregator interface so any sibling interface the
// underlying value satisfies (OnlineAggregator / MergeableAggregator /
// RichAggregator) stays visible via Go's interface-embedded method
// promotion.
type metaAggregatorWrapper struct {
	processing.Aggregator
	emit AggregatorComponentsFunc
}

// Components implements processing.MetaAggregator by delegating to the
// registration's ComponentsFunc. The orchestrator invokes this once
// after Aggregate / Finalize.
func (w *metaAggregatorWrapper) Components() (map[string]any, error) {
	if w.emit == nil {
		return nil, nil
	}
	return w.emit(w.Aggregator)
}

// wrapAggregatorFactory composes a factory that wraps the
// registration's Factory output in metaAggregatorWrapper when
// ComponentsFunc is supplied. Floor-only registrations (no emitter)
// pass through unwrapped — the orchestrator's MetaAggregator type
// assertion will return false and the universal-floor pass handles
// the per-record bookkeeping unconditionally.
func wrapAggregatorFactory(reg AggregatorRegistration) processing.AggregatorFactory {
	if reg.ComponentsFunc == nil {
		return reg.Factory
	}
	emit := reg.ComponentsFunc
	inner := reg.Factory
	return func(agg *types.Aggregation, schema *encoding.Schema) (processing.Aggregator, error) {
		instance, err := inner(agg, schema)
		if err != nil {
			return nil, err
		}
		if instance == nil {
			return nil, nil
		}
		return &metaAggregatorWrapper{Aggregator: instance, emit: emit}, nil
	}
}

// metaGrouperWrapper wraps an embedder-supplied Grouper so the
// orchestrator's processing.MetaGrouper assertion succeeds. The
// wrapper embeds Grouper so StreamingGrouper / StreamableGrouper /
// MultiKeyStreamingGrouper assertions still land on the underlying
// value when the embedder's Grouper implements them.
type metaGrouperWrapper struct {
	processing.Grouper
	emit GrouperComponentsFunc
}

// Components implements processing.MetaGrouper.
func (w *metaGrouperWrapper) Components() (map[string]any, error) {
	if w.emit == nil {
		return nil, nil
	}
	return w.emit(w.Grouper)
}

// wrapGrouperFactory mirrors wrapAggregatorFactory for groupers.
func wrapGrouperFactory(reg GrouperRegistration) processing.GrouperFactory {
	if reg.ComponentsFunc == nil {
		return reg.Factory
	}
	emit := reg.ComponentsFunc
	inner := reg.Factory
	return func(grp *types.Group, schema *encoding.Schema) (processing.Grouper, error) {
		instance, err := inner(grp, schema)
		if err != nil {
			return nil, err
		}
		if instance == nil {
			return nil, nil
		}
		return &metaGrouperWrapper{Grouper: instance, emit: emit}, nil
	}
}

// metaFiltererWrapper wraps an embedder-supplied FiltererBuilder so
// the orchestrator's processing.MetaFilterer assertion succeeds. The
// wrapper embeds FiltererBuilder so the orchestrator's Build call
// still lands on the underlying value.
type metaFiltererWrapper struct {
	processing.FiltererBuilder
	emit FiltererComponentsFunc
}

// Components implements processing.MetaFilterer.
func (w *metaFiltererWrapper) Components() (map[string]any, error) {
	if w.emit == nil {
		return nil, nil
	}
	return w.emit(w.FiltererBuilder)
}

// wrapFiltererFactory mirrors wrapAggregatorFactory for filterers.
// Filterer factories take no arguments so the wrapper is a closure
// over the original factory and the emitter.
func wrapFiltererFactory(reg FiltererRegistration) processing.FiltererFactory {
	if reg.ComponentsFunc == nil {
		return reg.Factory
	}
	emit := reg.ComponentsFunc
	inner := reg.Factory
	return func() processing.FiltererBuilder {
		builder := inner()
		if builder == nil {
			return nil
		}
		return &metaFiltererWrapper{FiltererBuilder: builder, emit: emit}
	}
}
