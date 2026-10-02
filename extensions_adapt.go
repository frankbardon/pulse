package pulse

import (
	stderrors "errors"
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/internal/processing/feature"
	"github.com/frankbardon/pulse/internal/processing/window"
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

// extendInner exposes the embedder's own value. Every aggregator
// wrapper embeds aggCore, so aggMerge can unwrap its merge partner.
func (a aggCore) extendInner() extend.Aggregator { return a.inner }

// extendAggregatorWrapper is satisfied by every adapted aggregator.
type extendAggregatorWrapper interface {
	extendInner() extend.Aggregator
}

// aggOnline forwards extend.OnlineAggregator as
// processing.OnlineAggregator.
type aggOnline struct{ online extend.OnlineAggregator }

func (a aggOnline) UpdateRow(rec *processing.Record, field string) error {
	return a.online.UpdateRow(rec, field)
}

func (a aggOnline) Finalize() (float64, error) { return a.online.Finalize() }

// aggMerge forwards extend.MergeableAggregator as
// processing.MergeableAggregator. The engine hands MergeOnline another
// ADAPTED instance; Merge receives the embedder's own value behind it,
// so the embedder's type assertion to its concrete type succeeds. A
// partner the adapter did not build is a programming error, reported
// as PROCESSING_INTERNAL like the built-ins' type-mismatch merge.
type aggMerge struct {
	merge extend.MergeableAggregator
	name  types.AggregationType
}

func (a aggMerge) MergeOnline(other processing.OnlineAggregator) error {
	w, ok := other.(extendAggregatorWrapper)
	var partner extend.OnlineAggregator
	if ok {
		partner, ok = w.extendInner().(extend.OnlineAggregator)
	}
	if !ok {
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			fmt.Sprintf("extension aggregator %s: MergeOnline partner %T is not an adapted extension aggregator", a.name, other),
			map[string]any{"aggregation_type": string(a.name), "partner": fmt.Sprintf("%T", other)})
	}
	return a.merge.Merge(partner)
}

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

// One wrapper per capability combination: (O)nline, (R)ich, (M)eta,
// mer(G)e. extend.MergeableAggregator embeds OnlineAggregator, so G
// only ever appears alongside O: twelve combinations, not sixteen.
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
	aggAdaptedOG struct {
		aggCore
		aggOnline
		aggMerge
	}
	aggAdaptedORG struct {
		aggCore
		aggOnline
		aggRich
		aggMerge
	}
	aggAdaptedOMG struct {
		aggCore
		aggOnline
		aggMeta
		aggMerge
	}
	aggAdaptedORMG struct {
		aggCore
		aggOnline
		aggRich
		aggMeta
		aggMerge
	}
)

// Capability bits for the adaptAggregator switch.
const (
	aggCapOnline = 1 << iota
	aggCapRich
	aggCapMeta
	aggCapMerge
)

// adaptAggregator wraps an extend.Aggregator so the engine sees exactly
// the siblings it implements, plus MetaAggregator when emit is non-nil.
// name is carried only for the merge-partner diagnostic.
//
// A value that carries its own Components() method and no registration
// ComponentsFunc keeps the type-level emission path: the method is
// adopted as the emitter, exactly as the engine's MetaAggregator
// assertion saw it before adaptation.
func adaptAggregator(name types.AggregationType, inner extend.Aggregator, emit AggregatorComponentsFunc) processing.Aggregator {
	if emit == nil {
		if self, ok := inner.(componentsEmitter); ok {
			emit = func(extend.Aggregator) (map[string]any, error) { return self.Components() }
		}
	}
	core := aggCore{inner: inner}
	online, isOnline := inner.(extend.OnlineAggregator)
	rich, isRich := inner.(extend.RichAggregator)
	merge, isMerge := inner.(extend.MergeableAggregator)
	o := aggOnline{online: online}
	r := aggRich{rich: rich}
	m := aggMeta{inner: inner, emit: emit}
	g := aggMerge{merge: merge, name: name}
	caps := 0
	if isOnline {
		caps |= aggCapOnline
	}
	if isRich {
		caps |= aggCapRich
	}
	if emit != nil {
		caps |= aggCapMeta
	}
	if isMerge {
		caps |= aggCapMerge
	}
	switch caps {
	case aggCapOnline | aggCapRich | aggCapMeta | aggCapMerge:
		return &aggAdaptedORMG{core, o, r, m, g}
	case aggCapOnline | aggCapRich | aggCapMerge:
		return &aggAdaptedORG{core, o, r, g}
	case aggCapOnline | aggCapMeta | aggCapMerge:
		return &aggAdaptedOMG{core, o, m, g}
	case aggCapOnline | aggCapMerge:
		return &aggAdaptedOG{core, o, g}
	case aggCapOnline | aggCapRich | aggCapMeta:
		return &aggAdaptedORM{core, o, r, m}
	case aggCapOnline | aggCapRich:
		return &aggAdaptedOR{core, o, r}
	case aggCapOnline | aggCapMeta:
		return &aggAdaptedOM{core, o, m}
	case aggCapRich | aggCapMeta:
		return &aggAdaptedRM{core, r, m}
	case aggCapOnline:
		return &aggAdaptedO{core, o}
	case aggCapRich:
		return &aggAdaptedR{core, r}
	case aggCapMeta:
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
	inner, emit, name := reg.Factory, reg.ComponentsFunc, reg.Name
	return func(agg *types.Aggregation, schema *encoding.Schema) (processing.Aggregator, error) {
		instance, err := inner(agg, schema)
		if err != nil {
			return nil, err
		}
		if instance == nil {
			return nil, nil
		}
		return adaptAggregator(name, instance, emit), nil
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
//
// It also synthesizes processing.StreamableGrouper (KeyFor) from the
// field the grouper was constructed for, so a single-key extension
// grouper takes the fused crosstab arm like its built-in twins. The
// engine's field-bound callers (the fused crosstab keyer) pass "" to
// KeyForRow because built-ins bind their field at factory time; the
// embedder's KeyForRow always receives the real field name instead.
// StreamableGrouper itself stays engine-only — extend never exposes it.
type grpStreaming struct {
	streaming extend.StreamingGrouper
	field     string
}

func (g grpStreaming) KeyForRow(rec *processing.Record, field string) (string, bool, error) {
	if field == "" {
		field = g.field
	}
	key, ok, err := g.streaming.KeyForRow(rec, field)
	if err != nil {
		if stderrors.Is(err, extend.ErrGrouperKeyNull) {
			return "", false, nil
		}
		return "", false, err
	}
	return key, ok, nil
}

// KeyFor is processing.StreamableGrouper's field-bound key: ok=false
// (or extend.ErrGrouperKeyNull) becomes processing.ErrGrouperKeyNull,
// the engine's in-band "no bucket" sentinel for this shape.
func (g grpStreaming) KeyFor(rec *processing.Record) (string, error) {
	key, ok, err := g.KeyForRow(rec, g.field)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", processing.ErrGrouperKeyNull
	}
	return key, nil
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

// extendInner exposes the embedder's own value. Every grouper wrapper
// embeds grpCore, so grpMerge can unwrap its merge partner.
func (g grpCore) extendInner() extend.Grouper { return g.inner }

// extendGrouperWrapper is satisfied by every adapted grouper.
type extendGrouperWrapper interface {
	extendInner() extend.Grouper
}

// grpMerge forwards extend.MergeableGrouper as
// processing.MergeableGrouper. The engine hands MergeGrouperState
// another ADAPTED instance; MergeState receives the embedder's own
// value behind it. A nil merge is the declared-only fold: a Mergeable
// registration whose grouper emits no components has no state to fold
// (probe-validation requires extend.MergeableGrouper whenever it
// emits), so the fold is a no-op — the engine merges the per-key
// bucket aggregators and counts the floor itself. A partner the
// adapter did not build is PROCESSING_INTERNAL on both shapes.
type grpMerge struct {
	merge extend.MergeableGrouper
	name  types.GroupType
}

func (g grpMerge) MergeGrouperState(other processing.Grouper) error {
	w, ok := other.(extendGrouperWrapper)
	if !ok {
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			fmt.Sprintf("extension grouper %s: MergeGrouperState partner %T is not an adapted extension grouper", g.name, other),
			map[string]any{"group_type": string(g.name), "partner": fmt.Sprintf("%T", other)})
	}
	if g.merge == nil {
		return nil
	}
	return g.merge.MergeState(w.extendInner())
}

// One wrapper per capability combination: (S)treaming, multi-(K)ey,
// (M)eta, mer(G)e — sixteen, selected by the bitmask switch in
// adaptGrouper. Every sibling is forwarded by an explicit field, never
// by embedding the embedder's interface (the R6 bug).
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
	grpAdaptedG struct {
		grpCore
		grpMerge
	}
	grpAdaptedSG struct {
		grpCore
		grpStreaming
		grpMerge
	}
	grpAdaptedKG struct {
		grpCore
		grpMulti
		grpMerge
	}
	grpAdaptedMG struct {
		grpCore
		grpMeta
		grpMerge
	}
	grpAdaptedSKG struct {
		grpCore
		grpStreaming
		grpMulti
		grpMerge
	}
	grpAdaptedSMG struct {
		grpCore
		grpStreaming
		grpMeta
		grpMerge
	}
	grpAdaptedKMG struct {
		grpCore
		grpMulti
		grpMeta
		grpMerge
	}
	grpAdaptedSKMG struct {
		grpCore
		grpStreaming
		grpMulti
		grpMeta
		grpMerge
	}
)

// Capability bits for the adaptGrouper switch.
const (
	grpCapStreaming = 1 << iota
	grpCapMulti
	grpCapMeta
	grpCapMerge
)

// adaptGrouper wraps an extend.Grouper so the engine sees exactly the
// keying siblings it implements, plus MetaGrouper when emit is non-nil
// (or the value carries its own Components() method), plus
// MergeableGrouper when the value implements extend.MergeableGrouper or
// the registration declares mergeable (the no-op fold of a grouper with
// no components state). field is the grouper's target field
// (types.Group.Field), bound so a streaming grouper also satisfies
// processing.StreamableGrouper.
func adaptGrouper(name types.GroupType, field string, inner extend.Grouper, emit GrouperComponentsFunc, mergeable bool) processing.Grouper {
	if emit == nil {
		if self, ok := inner.(componentsEmitter); ok {
			emit = func(extend.Grouper) (map[string]any, error) { return self.Components() }
		}
	}
	core := grpCore{inner: inner, name: name}
	streaming, isS := inner.(extend.StreamingGrouper)
	multi, isK := inner.(extend.MultiKeyStreamingGrouper)
	merge, isG := inner.(extend.MergeableGrouper)
	s := grpStreaming{streaming: streaming, field: field}
	k := grpMulti{multi: multi}
	m := grpMeta{inner: inner, emit: emit}
	g := grpMerge{merge: merge, name: name}
	caps := 0
	if isS {
		caps |= grpCapStreaming
	}
	if isK {
		caps |= grpCapMulti
	}
	if emit != nil {
		caps |= grpCapMeta
	}
	if isG || mergeable {
		caps |= grpCapMerge
	}
	switch caps {
	case grpCapStreaming | grpCapMulti | grpCapMeta | grpCapMerge:
		return &grpAdaptedSKMG{core, s, k, m, g}
	case grpCapStreaming | grpCapMulti | grpCapMeta:
		return &grpAdaptedSKM{core, s, k, m}
	case grpCapStreaming | grpCapMulti | grpCapMerge:
		return &grpAdaptedSKG{core, s, k, g}
	case grpCapStreaming | grpCapMeta | grpCapMerge:
		return &grpAdaptedSMG{core, s, m, g}
	case grpCapMulti | grpCapMeta | grpCapMerge:
		return &grpAdaptedKMG{core, k, m, g}
	case grpCapStreaming | grpCapMulti:
		return &grpAdaptedSK{core, s, k}
	case grpCapStreaming | grpCapMeta:
		return &grpAdaptedSM{core, s, m}
	case grpCapStreaming | grpCapMerge:
		return &grpAdaptedSG{core, s, g}
	case grpCapMulti | grpCapMeta:
		return &grpAdaptedKM{core, k, m}
	case grpCapMulti | grpCapMerge:
		return &grpAdaptedKG{core, k, g}
	case grpCapMeta | grpCapMerge:
		return &grpAdaptedMG{core, m, g}
	case grpCapStreaming:
		return &grpAdaptedS{core, s}
	case grpCapMulti:
		return &grpAdaptedK{core, k}
	case grpCapMeta:
		return &grpAdaptedM{core, m}
	case grpCapMerge:
		return &grpAdaptedG{core, g}
	default:
		return &grpAdapted{core}
	}
}

// adaptGrouperFactory turns a registration's extend factory into the
// engine factory. A nil instance passes through as nil.
func adaptGrouperFactory(reg GrouperRegistration) processing.GrouperFactory {
	inner, emit, name, mergeable := reg.Factory, reg.ComponentsFunc, reg.Name, reg.Mergeable
	return func(grp *types.Group, schema *encoding.Schema) (processing.Grouper, error) {
		instance, err := inner(grp, schema)
		if err != nil {
			return nil, err
		}
		if instance == nil {
			return nil, nil
		}
		field := ""
		if grp != nil {
			field = grp.Field
		}
		return adaptGrouper(name, field, instance, emit, mergeable), nil
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

// ---- attributes --------------------------------------------------------

// attrCore forwards the base AttributeComputer method over the
// zero-copy Rows view.
type attrCore struct{ inner extend.AttributeComputer }

func (a attrCore) Compute(records []*processing.Record, field string) ([]float64, error) {
	return a.inner.Compute(recordRows(records), field)
}

// attrRow forwards extend.RowLocalAttribute as
// processing.RowLocalAttribute.
type attrRow struct{ row extend.RowLocalAttribute }

func (a attrRow) Row(rec *processing.Record, field string) (float64, error) {
	return a.row.Row(rec, field)
}

// attrTwoPass forwards the two extra extend.TwoPassAttribute methods;
// paired with attrRow it satisfies processing.TwoPassAttribute.
type attrTwoPass struct{ two extend.TwoPassAttribute }

func (a attrTwoPass) PrePass(rec *processing.Record, field string) error {
	return a.two.PrePass(rec, field)
}

func (a attrTwoPass) Finalize() error { return a.two.Finalize() }

// One wrapper per capability tier: buffered, (R)ow-local, (T)wo-pass.
// TwoPassAttribute embeds RowLocalAttribute, so the tiers nest and
// there is no fourth combination.
type (
	attrAdapted  struct{ attrCore }
	attrAdaptedR struct {
		attrCore
		attrRow
	}
	attrAdaptedT struct {
		attrCore
		attrRow
		attrTwoPass
	}
)

// adaptAttribute wraps an extend.AttributeComputer so the engine's
// RowLocalAttribute / TwoPassAttribute assertions succeed iff the
// embedder value implements the matching extend sibling. ExtensionAware
// is engine-only and never forwarded.
func adaptAttribute(inner extend.AttributeComputer) processing.AttributeComputer {
	core := attrCore{inner: inner}
	if two, ok := inner.(extend.TwoPassAttribute); ok {
		return &attrAdaptedT{core, attrRow{row: two}, attrTwoPass{two: two}}
	}
	if row, ok := inner.(extend.RowLocalAttribute); ok {
		return &attrAdaptedR{core, attrRow{row: row}}
	}
	return &attrAdapted{core}
}

// adaptAttributeFactory turns a registration's extend factory into the
// engine factory. A nil instance passes through as nil.
func adaptAttributeFactory(reg AttributeRegistration) processing.AttributeFactory {
	inner := reg.Factory
	return func(attr *types.Attribute, schema *encoding.Schema) (processing.AttributeComputer, error) {
		instance, err := inner(attr, schema)
		if err != nil {
			return nil, err
		}
		if instance == nil {
			return nil, nil
		}
		return adaptAttribute(instance), nil
	}
}

// ---- tests -------------------------------------------------------------

// rowTestAdapted forwards extend.RowTest as processing.RowTest. Row
// tests have no optional siblings.
type rowTestAdapted struct{ inner extend.RowTest }

func (t *rowTestAdapted) UpdateRow(rec *processing.Record) error { return t.inner.UpdateRow(rec) }

func (t *rowTestAdapted) Finalize() (*types.TestResult, error) { return t.inner.Finalize() }

// adaptRowTestFactory turns a tier-1 registration's extend factory
// into the engine factory. A nil instance passes through as nil.
func adaptRowTestFactory(reg TestRegistration) processing.RowTestFactory {
	inner := reg.RowFactory
	return func(spec *types.Test, schema *encoding.Schema) (processing.RowTest, error) {
		instance, err := inner(spec, schema)
		if err != nil {
			return nil, err
		}
		if instance == nil {
			return nil, nil
		}
		return &rowTestAdapted{inner: instance}, nil
	}
}

// adaptPostTestFactory turns a tier-2 registration's extend factory
// into the engine factory. extend.PostTest has the engine's method set
// exactly (it never sees a Record), so the instance is handed over
// as-is; only the factory's named return type differs.
func adaptPostTestFactory(reg TestRegistration) processing.PostTestFactory {
	inner := reg.PostFactory
	return func(spec *types.Test, schema *encoding.Schema) (processing.PostTest, error) {
		instance, err := inner(spec, schema)
		if err != nil || instance == nil {
			return nil, err
		}
		return instance, nil
	}
}

// ---- windows -----------------------------------------------------------

// adaptWindowFactory turns a registration's extend factory into the
// engine factory. extend.WindowComputer has the engine's method set
// exactly, so the instance is handed over as-is; the options value is
// translated (both are empty today).
func adaptWindowFactory(reg WindowRegistration) window.WindowFactory {
	inner := reg.Factory
	return func(w *types.Window, _ window.WindowOptions) (window.WindowComputer, error) {
		instance, err := inner(w, extend.WindowOptions{})
		if err != nil || instance == nil {
			return nil, err
		}
		return instance, nil
	}
}

// ---- features ----------------------------------------------------------

// featureRecord returns the read-only extend view of a feature.Record.
// The engine always hands features its own *processing.Record, which
// satisfies extend.Record directly; any other implementation of the
// narrow feature.Record contract degrades to featureRecordView rather
// than panicking.
func featureRecord(r feature.Record) extend.Record {
	if rec, ok := r.(extend.Record); ok {
		return rec
	}
	return featureRecordView{r}
}

// featureRecordView lifts the four-method feature.Record onto
// extend.Record. Only the numeric and categorical reads exist on the
// source; every other read reports "no value".
type featureRecordView struct{ r feature.Record }

func (v featureRecordView) Schema() *encoding.Schema { return nil }

func (v featureRecordView) IsNull(field string) bool {
	if _, ok := v.r.NumericValue(field); ok {
		return false
	}
	_, ok := v.r.StringValue(field)
	return !ok
}

func (v featureRecordView) NumericValue(field string) (float64, bool) {
	return v.r.NumericValue(field)
}

func (v featureRecordView) StringValue(field string) (string, bool) {
	return v.r.StringValue(field)
}

func (v featureRecordView) SetMaskValue(string) (encoding.SetMask, bool) {
	return encoding.SetMask{}, false
}

func (v featureRecordView) DecimalValue(string) (encoding.Decimal128, bool) {
	return encoding.Decimal128{}, false
}

// featureRows is the extend.Rows view over the engine's feature record
// slice. It copies no rows.
type featureRows []feature.Record

func (r featureRows) Len() int               { return len(r) }
func (r featureRows) At(i int) extend.Record { return featureRecord(r[i]) }

// toEngineFeatureOutputs converts an extend output map to the engine's.
// FeatureOutput and feature.Output share one underlying struct, so each
// entry converts without copying its slices.
func toEngineFeatureOutputs(in map[string]extend.FeatureOutput) map[string]feature.Output {
	if in == nil {
		return nil
	}
	out := make(map[string]feature.Output, len(in))
	for label, o := range in {
		out[label] = feature.Output(o)
	}
	return out
}

// featCore forwards the base FeatureComputer method.
type featCore struct{ inner extend.FeatureComputer }

func (f featCore) Compute(records []feature.Record, field string) (map[string]feature.Output, error) {
	out, err := f.inner.Compute(featureRows(records), field)
	if err != nil {
		return nil, err
	}
	return toEngineFeatureOutputs(out), nil
}

// featStreaming forwards extend.StreamingFeatureComputer as
// feature.StreamingComputer. EmitRow outputs are checked for the
// single-row shape so a malformed extension output is a coded error,
// never an index panic in the engine's write loop.
type featStreaming struct {
	streaming extend.StreamingFeatureComputer
	name      types.FeatureType
}

func (f featStreaming) PrePass(rec feature.Record, field string) error {
	return f.streaming.PrePass(featureRecord(rec), field)
}

func (f featStreaming) Finalize() error { return f.streaming.Finalize() }

func (f featStreaming) EmitRow(rec feature.Record, field string) (map[string]feature.Output, error) {
	out, err := f.streaming.EmitRow(featureRecord(rec), field)
	if err != nil {
		return nil, err
	}
	for label, o := range out {
		isNull := len(o.Nulls) > 0 && o.Nulls[0]
		if len(o.Nulls) > 1 || (!isNull && len(o.Values) != 1) {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
				fmt.Sprintf("extension feature %s EmitRow produced %d values / %d nulls for one row (label %s); want exactly one", f.name, len(o.Values), len(o.Nulls), label),
				map[string]any{"feature_type": string(f.name), "label": label, "values": len(o.Values), "nulls": len(o.Nulls)})
		}
	}
	return toEngineFeatureOutputs(out), nil
}

// One wrapper per capability combination: buffered, (S)treaming.
type (
	featAdapted  struct{ featCore }
	featAdaptedS struct {
		featCore
		featStreaming
	}
)

// adaptFeature wraps an extend.FeatureComputer so the engine's
// StreamingComputer assertion succeeds iff the embedder value
// implements extend.StreamingFeatureComputer.
func adaptFeature(name types.FeatureType, inner extend.FeatureComputer) feature.Computer {
	core := featCore{inner: inner}
	if s, ok := inner.(extend.StreamingFeatureComputer); ok {
		return &featAdaptedS{core, featStreaming{streaming: s, name: name}}
	}
	return &featAdapted{core}
}

// adaptFeatureFactory turns a registration's extend factory into the
// engine factory. A nil instance passes through as nil.
func adaptFeatureFactory(reg FeatureRegistration) feature.Factory {
	inner, name := reg.Factory, reg.Name
	return func(feat *types.Feature, schema *encoding.Schema) (feature.Computer, error) {
		instance, err := inner(feat, schema)
		if err != nil {
			return nil, err
		}
		if instance == nil {
			return nil, nil
		}
		return adaptFeature(name, instance), nil
	}
}
