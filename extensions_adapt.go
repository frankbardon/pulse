package pulse

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
)

// This file adapts operators authored against the public extend
// contract onto the engine's processing interfaces. Built-ins never
// pass through here; they keep the concrete *processing.Record path.
//
// Every optional sibling interface is forwarded EXPLICITLY. The adapter
// picks one concrete wrapper type per capability combination, so an
// engine type assertion (OnlineAggregator, RichAggregator,
// MetaAggregator, …) succeeds on the wrapper iff the embedder's value
// implements the matching extend sibling (or, for Meta*, iff the
// registration supplies a ComponentsFunc). Embedding an INTERFACE in a
// wrapper would promote only that interface's method set and silently
// hide every sibling — the historical meta-wrapper bug where a
// Streamable aggregator with a ComponentsFunc fell back to the buffered
// path. Embedding the small concrete forwarders below is safe: a
// struct's promoted methods are its full method set.

// The engine's row type satisfies the public read contract directly, so
// a per-row adapter costs nothing: *processing.Record is handed to
// extend operators as-is.
var _ extend.Record = (*processing.Record)(nil)

// recordRows is the zero-copy extend.Rows view over the engine's
// buffered row slice. Converting the slice header to recordRows copies
// no rows.
type recordRows []*processing.Record

func (r recordRows) Len() int               { return len(r) }
func (r recordRows) At(i int) extend.Record { return r[i] }

// ---- aggregators -------------------------------------------------------

// aggCore forwards the base Aggregator method.
type aggCore struct{ inner extend.Aggregator }

func (a aggCore) Aggregate(records []*processing.Record, field string) (float64, error) {
	return a.inner.Aggregate(recordRows(records), field)
}

// aggOnline forwards extend.OnlineAggregator as
// processing.OnlineAggregator.
type aggOnline struct{ online extend.OnlineAggregator }

func (a aggOnline) UpdateRow(rec *processing.Record, field string) error {
	return a.online.UpdateRow(rec, field)
}

func (a aggOnline) Finalize() (float64, error) { return a.online.Finalize() }

// aggRich forwards extend.RichAggregator.
type aggRich struct{ rich extend.RichAggregator }

func (a aggRich) Rich() (any, error) { return a.rich.Rich() }

// aggMeta synthesizes processing.MetaAggregator from the registration's
// ComponentsFunc. The emitter receives the embedder's own instance, so
// a type assertion to its concrete type inside the func succeeds.
type aggMeta struct {
	inner extend.Aggregator
	emit  AggregatorComponentsFunc
}

func (a aggMeta) Components() (map[string]any, error) { return a.emit(a.inner) }

// componentsEmitter is the structural shape of a value that emits its
// own per-operator components (the type-level twin of ComponentsFunc).
type componentsEmitter interface {
	Components() (map[string]any, error)
}

// One wrapper per capability combination: (O)nline, (R)ich, (M)eta.
type (
	aggAdapted  struct{ aggCore }
	aggAdaptedO struct {
		aggCore
		aggOnline
	}
	aggAdaptedR struct {
		aggCore
		aggRich
	}
	aggAdaptedM struct {
		aggCore
		aggMeta
	}
	aggAdaptedOR struct {
		aggCore
		aggOnline
		aggRich
	}
	aggAdaptedOM struct {
		aggCore
		aggOnline
		aggMeta
	}
	aggAdaptedRM struct {
		aggCore
		aggRich
		aggMeta
	}
	aggAdaptedORM struct {
		aggCore
		aggOnline
		aggRich
		aggMeta
	}
)

// adaptAggregator wraps an extend.Aggregator so the engine sees exactly
// the siblings it implements, plus MetaAggregator when emit is non-nil.
//
// A value that carries its own Components() method and no registration
// ComponentsFunc keeps the type-level emission path: the method is
// adopted as the emitter, exactly as the engine's MetaAggregator
// assertion saw it before adaptation.
func adaptAggregator(inner extend.Aggregator, emit AggregatorComponentsFunc) processing.Aggregator {
	if emit == nil {
		if self, ok := inner.(componentsEmitter); ok {
			emit = func(extend.Aggregator) (map[string]any, error) { return self.Components() }
		}
	}
	core := aggCore{inner: inner}
	online, isOnline := inner.(extend.OnlineAggregator)
	rich, isRich := inner.(extend.RichAggregator)
	o := aggOnline{online: online}
	r := aggRich{rich: rich}
	m := aggMeta{inner: inner, emit: emit}
	switch {
	case isOnline && isRich && emit != nil:
		return &aggAdaptedORM{core, o, r, m}
	case isOnline && isRich:
		return &aggAdaptedOR{core, o, r}
	case isOnline && emit != nil:
		return &aggAdaptedOM{core, o, m}
	case isRich && emit != nil:
		return &aggAdaptedRM{core, r, m}
	case isOnline:
		return &aggAdaptedO{core, o}
	case isRich:
		return &aggAdaptedR{core, r}
	case emit != nil:
		return &aggAdaptedM{core, m}
	default:
		return &aggAdapted{core}
	}
}

// adaptAggregatorFactory turns a registration's extend factory into the
// engine factory registered on processing.ExtensionRegistry. A nil
// instance passes through as nil (the engine reports it); probe-
// validation already refuses a factory that returns nil.
func adaptAggregatorFactory(reg AggregatorRegistration) processing.AggregatorFactory {
	inner := reg.Factory
	emit := reg.ComponentsFunc
	return func(agg *types.Aggregation, schema *encoding.Schema) (processing.Aggregator, error) {
		instance, err := inner(agg, schema)
		if err != nil {
			return nil, err
		}
		if instance == nil {
			return nil, nil
		}
		return adaptAggregator(instance, emit), nil
	}
}
