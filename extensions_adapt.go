package pulse

import (
	stderrors "errors"
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
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

// ---- groupers ----------------------------------------------------------

// grpCore forwards the base Grouper method, translating the extend
// index map back to the engine's record-slice map. The name is carried
// only for the out-of-range diagnostic.
type grpCore struct {
	inner extend.Grouper
	name  types.GroupType
}

func (g grpCore) Group(records []*processing.Record, field string) (map[string][]*processing.Record, error) {
	idx, err := g.inner.Group(recordRows(records), field)
	if err != nil {
		return nil, err
	}
	if idx == nil {
		return nil, nil
	}
	out := make(map[string][]*processing.Record, len(idx))
	for key, rows := range idx {
		bucket := make([]*processing.Record, len(rows))
		for j, i := range rows {
			if i < 0 || i >= len(records) {
				return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
					fmt.Sprintf("extension grouper %s returned row index %d outside [0, %d) for bucket %q", g.name, i, len(records), key),
					map[string]any{"group_type": string(g.name), "bucket": key, "index": i, "rows": len(records)})
			}
			bucket[j] = records[i]
		}
		out[key] = bucket
	}
	return out, nil
}

// grpStreaming forwards extend.StreamingGrouper as
// processing.StreamingGrouper. extend.ErrGrouperKeyNull maps to
// ok=false, the engine's "no bucket" answer.
type grpStreaming struct{ streaming extend.StreamingGrouper }

func (g grpStreaming) KeyForRow(rec *processing.Record, field string) (string, bool, error) {
	key, ok, err := g.streaming.KeyForRow(rec, field)
	if err != nil {
		if stderrors.Is(err, extend.ErrGrouperKeyNull) {
			return "", false, nil
		}
		return "", false, err
	}
	return key, ok, nil
}

// grpMulti forwards extend.MultiKeyStreamingGrouper as
// processing.MultiKeyStreamingGrouper, with the same null mapping.
type grpMulti struct {
	multi extend.MultiKeyStreamingGrouper
}

func (g grpMulti) KeysForRow(rec *processing.Record, field string) ([]string, bool, error) {
	keys, ok, err := g.multi.KeysForRow(rec, field)
	if err != nil {
		if stderrors.Is(err, extend.ErrGrouperKeyNull) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return keys, ok, nil
}

// grpMeta synthesizes processing.MetaGrouper from the registration's
// ComponentsFunc, handing the emitter the embedder's own instance.
type grpMeta struct {
	inner extend.Grouper
	emit  GrouperComponentsFunc
}

func (g grpMeta) Components() (map[string]any, error) { return g.emit(g.inner) }

// One wrapper per capability combination: (S)treaming, multi-(K)ey,
// (M)eta.
type (
	grpAdapted  struct{ grpCore }
	grpAdaptedS struct {
		grpCore
		grpStreaming
	}
	grpAdaptedK struct {
		grpCore
		grpMulti
	}
	grpAdaptedM struct {
		grpCore
		grpMeta
	}
	grpAdaptedSK struct {
		grpCore
		grpStreaming
		grpMulti
	}
	grpAdaptedSM struct {
		grpCore
		grpStreaming
		grpMeta
	}
	grpAdaptedKM struct {
		grpCore
		grpMulti
		grpMeta
	}
	grpAdaptedSKM struct {
		grpCore
		grpStreaming
		grpMulti
		grpMeta
	}
)

// adaptGrouper wraps an extend.Grouper so the engine sees exactly the
// keying siblings it implements, plus MetaGrouper when emit is non-nil
// (or the value carries its own Components() method).
func adaptGrouper(name types.GroupType, inner extend.Grouper, emit GrouperComponentsFunc) processing.Grouper {
	if emit == nil {
		if self, ok := inner.(componentsEmitter); ok {
			emit = func(extend.Grouper) (map[string]any, error) { return self.Components() }
		}
	}
	core := grpCore{inner: inner, name: name}
	streaming, isS := inner.(extend.StreamingGrouper)
	multi, isK := inner.(extend.MultiKeyStreamingGrouper)
	s := grpStreaming{streaming: streaming}
	k := grpMulti{multi: multi}
	m := grpMeta{inner: inner, emit: emit}
	isM := emit != nil
	switch {
	case isS && isK && isM:
		return &grpAdaptedSKM{core, s, k, m}
	case isS && isK:
		return &grpAdaptedSK{core, s, k}
	case isS && isM:
		return &grpAdaptedSM{core, s, m}
	case isK && isM:
		return &grpAdaptedKM{core, k, m}
	case isS:
		return &grpAdaptedS{core, s}
	case isK:
		return &grpAdaptedK{core, k}
	case isM:
		return &grpAdaptedM{core, m}
	default:
		return &grpAdapted{core}
	}
}

// adaptGrouperFactory turns a registration's extend factory into the
// engine factory. A nil instance passes through as nil.
func adaptGrouperFactory(reg GrouperRegistration) processing.GrouperFactory {
	inner, emit, name := reg.Factory, reg.ComponentsFunc, reg.Name
	return func(grp *types.Group, schema *encoding.Schema) (processing.Grouper, error) {
		instance, err := inner(grp, schema)
		if err != nil {
			return nil, err
		}
		if instance == nil {
			return nil, nil
		}
		return adaptGrouper(name, instance, emit), nil
	}
}

// ---- filterers ---------------------------------------------------------

// fltCore forwards Build, wrapping the extend FilterFunc onto the
// engine's *processing.Record signature (the record is passed as-is).
type fltCore struct{ inner extend.FiltererBuilder }

func (f fltCore) Build(spec *types.Filterer, schema *encoding.Schema) (processing.FilterFunc, error) {
	fn, err := f.inner.Build(spec, schema)
	if err != nil || fn == nil {
		return nil, err
	}
	return func(rec *processing.Record) (bool, error) { return fn(rec) }, nil
}

// fltMeta synthesizes processing.MetaFilterer from the registration's
// ComponentsFunc.
type fltMeta struct {
	inner extend.FiltererBuilder
	emit  FiltererComponentsFunc
}

func (f fltMeta) Components() (map[string]any, error) { return f.emit(f.inner) }

type (
	fltAdapted  struct{ fltCore }
	fltAdaptedM struct {
		fltCore
		fltMeta
	}
)

// adaptFilterer wraps an extend.FiltererBuilder, adding MetaFilterer
// when emit is non-nil (or the value carries its own Components()).
func adaptFilterer(inner extend.FiltererBuilder, emit FiltererComponentsFunc) processing.FiltererBuilder {
	if emit == nil {
		if self, ok := inner.(componentsEmitter); ok {
			emit = func(extend.FiltererBuilder) (map[string]any, error) { return self.Components() }
		}
	}
	core := fltCore{inner: inner}
	if emit != nil {
		return &fltAdaptedM{core, fltMeta{inner: inner, emit: emit}}
	}
	return &fltAdapted{core}
}

// adaptFiltererFactory turns a registration's extend factory into the
// engine factory. A nil builder passes through as nil.
func adaptFiltererFactory(reg FiltererRegistration) processing.FiltererFactory {
	inner, emit := reg.Factory, reg.ComponentsFunc
	return func() processing.FiltererBuilder {
		builder := inner()
		if builder == nil {
			return nil
		}
		return adaptFilterer(builder, emit)
	}
}
