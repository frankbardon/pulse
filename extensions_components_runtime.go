package pulse

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
)

// This file wires ComponentsFunc registrations into the runtime by
// wrapping each extension factory so the returned instance satisfies
// the matching processing.Meta* sibling interface. The orchestrator's
// type assertions (e.g. `grp.(processing.MetaGrouper)`) then succeed
// for extension operators on the same dispatch path the built-ins use.
//
// Aggregators are adapted in extensions_adapt.go, which forwards every
// optional sibling explicitly. The grouper and filterer wrappers below
// still embed the base INTERFACE, which promotes only that interface's
// method set: a sibling the underlying value implements (for groupers
// StreamingGrouper / MultiKeyStreamingGrouper) is NOT visible on the
// wrapper. They are replaced by explicit adapters as each category
// moves onto the extend contract. When the registration omits
// ComponentsFunc the wrapper is not installed — the factory is
// registered verbatim and the orchestrator's type assertion to Meta*
// simply returns false (the floor-only path).
//
// Probe-validation mismatches surface as
// PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH; runtime emission errors
// ride the same routing as Aggregate / Finalize.

// metaGrouperWrapper wraps an embedder-supplied Grouper so the
// orchestrator's processing.MetaGrouper assertion succeeds. It embeds
// the Grouper interface, so sibling interfaces of the underlying value
// are NOT promoted (see the file comment).
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

// wrapGrouperFactory installs metaGrouperWrapper when the
// registration supplies a ComponentsFunc.
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

// wrapFiltererFactory installs metaFiltererWrapper when the
// registration supplies a ComponentsFunc.
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
