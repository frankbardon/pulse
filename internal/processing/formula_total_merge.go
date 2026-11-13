package processing

import (
	"fmt"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/prevstate"
	"github.com/frankbardon/pulse/internal/reprosum"
	"github.com/frankbardon/pulse/internal/totalcall"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// Two-phase total() on the parallel reducers (U51 E3-S1, #399).
//
// A total-bearing ATTR_FORMULA is not row-local: each row reads the
// cohort-wide sum of its total() arguments. The serial arms run it as a
// TwoPassAttribute (processStreamingTwoPass, computeTwoPass). The
// parallel reducers (ShardWorkers / DecodeWorkers) run it in two
// phases instead:
//
//   - phase 1, a parallel pre-pass: each partition folds its
//     filter-passing records into a TotalsPrepass — the layer-0
//     row-local attributes a total() argument reads, then every
//     formula's PrePass — so each total argument holds a binned
//     {sum, count} for its rows (PartialTotals);
//   - the partitions merge (TotalsPrepass.Merge → AbsorbTotals): the
//     binned sums merge exactly (reprosum.Sum.Merge, order-free) and the
//     counts are integers, so the merged state is bit for bit the
//     serial pre-pass state, whatever the split;
//   - Publish finalizes the merged state into PublishedTotals;
//   - phase 2, the ordinary parallel reduce: every partition's formula
//     instance is bound to the published values before its first row
//     (PublishedTotals.Bind → PublishTotals), so Row reads the same
//     total the serial Row reads.
//
// The merge gate (mergegate.ParallelTotals) admits every total() formula
// of a request without a crosstab, never on a ProcessChain stage. Groups
// are admitted (U74 E3-S1, #494): a total() is the cohort-wide
// post-filter sum, so the one published value binds every partition
// before its first row, whatever buckets the partition's groupers make.
// So are later pre-pass layers (U74 E3-S2, #494): a total() whose
// argument reads an earlier total (directly or through row-local slots)
// sits in layer L > 0 of the serial two-pass plan, and phase 1 runs one
// parallel pre-pass per layer, in order — layer L's partitions are built
// with every earlier layer's published values bound (so the stages a
// layer-L argument reads score exactly as on the serial layer-L scan),
// fold only the layer-L formulas' PrePass, and merge and publish them;
// the published values accumulate across layers, and phase 2 binds them
// all.

// TotalsPartial is one total-bearing formula's pre-pass state: per
// distinct total() argument (Keys, totalcall.Args order), the binned
// sum and the contributing-row count.
type TotalsPartial struct {
	Keys   []string
	Sums   []reprosum.Sum
	Counts []int
}

// MergeableTotals is the merge-facing hook of a total-bearing
// ATTR_FORMULA (formulaTotalAttribute): PartialTotals hands its
// pre-pass state out, AbsorbTotals folds another partition's in, and
// PublishTotals is the bind path that installs merged, finalized values
// on an instance that never ran the pre-pass.
type MergeableTotals interface {
	TwoPassAttribute
	PartialTotals() (TotalsPartial, error)
	AbsorbTotals(TotalsPartial) error
	PublishTotals(values []float64) error
}

var _ MergeableTotals = (*formulaTotalAttribute)(nil)

// PartialTotals returns a copy of the pre-pass state.
func (a *formulaTotalAttribute) PartialTotals() (TotalsPartial, error) {
	if err := a.prepare(); err != nil {
		return TotalsPartial{}, err
	}
	t := a.totals
	return TotalsPartial{
		Keys:   append([]string(nil), t.keys...),
		Sums:   append([]reprosum.Sum(nil), t.sums...),
		Counts: append([]int(nil), t.counts...),
	}, nil
}

// AbsorbTotals folds another partition's pre-pass state into this one:
// sum by binned merge, count by integer add. The other state must
// carry the same arguments in the same order (it is the same slot
// built over the same request); anything else is PROCESSING_INTERNAL.
func (a *formulaTotalAttribute) AbsorbTotals(o TotalsPartial) error {
	if err := a.prepare(); err != nil {
		return err
	}
	t := a.totals
	if t.finalized {
		return errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"formula total() absorbed a partition after its pre-pass finalized")
	}
	if len(o.Keys) != len(t.keys) || len(o.Sums) != len(t.keys) || len(o.Counts) != len(t.keys) {
		return errors.NewCodedError(errors.PROCESSING_INTERNAL,
			fmt.Sprintf("formula total() partitions disagree: %d arguments merged into %d", len(o.Keys), len(t.keys)))
	}
	for i, k := range t.keys {
		if o.Keys[i] != k {
			return errors.NewCodedError(errors.PROCESSING_INTERNAL,
				fmt.Sprintf("formula total() partitions disagree: argument %q merged into %q", o.Keys[i], k))
		}
	}
	for i := range t.keys {
		t.sums[i].Merge(&o.Sums[i])
		t.counts[i] += o.Counts[i]
	}
	return nil
}

// PublishTotals is the bind path: it installs the published per-
// argument totals (Finalize's values over the merged pre-pass) so Row
// reads them, exactly as after this instance's own Finalize.
func (a *formulaTotalAttribute) PublishTotals(values []float64) error {
	if err := a.prepare(); err != nil {
		return err
	}
	t := a.totals
	if len(values) != len(t.keys) {
		return errors.NewCodedError(errors.PROCESSING_INTERNAL,
			fmt.Sprintf("formula total() published %d values for %d arguments", len(values), len(t.keys)))
	}
	t.values = append([]float64(nil), values...)
	t.finalized = true
	return nil
}

// publishedValues is the finalized per-argument totals.
func (a *formulaTotalAttribute) publishedValues() []float64 {
	return append([]float64(nil), a.totals.values...)
}

// HasFormulaTotals reports whether any attribute is a total-bearing
// ATTR_FORMULA — the parallel reducers' trigger for the two-phase
// drive (the gate, mergegate.ParallelTotals, has admitted the shape).
func HasFormulaTotals(attrs []*types.Attribute) bool {
	for _, a := range attrs {
		if formulaCallsTotal(a) {
			return true
		}
	}
	return false
}

// totalsStage is one declared-order attribute of a TotalsPrepass.
type totalsStage struct {
	attr     *types.Attribute
	row      RowLocalAttribute
	totals   MergeableTotals // non-nil for a total-bearing formula
	label    string
	nullable bool
}

// TotalsPrepass is one partition's phase-1 state for one pre-pass
// layer: the request's attributes built and bound like the reducers'
// row-local set, planned by the serial two-pass planner
// (planTwoPassStages), so each record evaluates exactly the stages a
// layer-L total() argument reads and folds every layer-L formula's
// PrePass at its declared position — the record state the serial
// layer-L pre-pass scan observes.
type TotalsPrepass struct {
	stages []totalsStage
	needed []int // declared-order indices evaluated (Row + Set)
	plan   twoPassPlan
	layer  int               // the pre-pass layer this state folds
	prior  map[int][]float64 // earlier layers' published values
}

// TotalsPrepassLayers is the number of parallel pre-pass layers attrs
// need (the serial two-pass plan's layer count, read from the
// registry): phase 1 runs NewTotalsPrepass for layers 0..n-1 in order.
func TotalsPrepassLayers(attrs []*types.Attribute, exts *ExtensionRegistry) int {
	return planTwoPassLayers(attrs, exts).layers
}

// NewTotalsPrepass builds one partition's phase-1 operators for attrs
// over schema and pre-pass layer layer, bound to the registry, the
// bound models and the resolved named vectors (the reducers'
// buildRowLocalAttrSpecs binding) and to prior — the values every
// earlier layer published (nil for layer 0). Every attribute must be
// row-local or a total-bearing formula — the shape mergegate admits;
// anything else, a layer past the plan or an earlier-layer formula
// prior does not carry, is PROCESSING_INTERNAL.
func NewTotalsPrepass(attrs []*types.Attribute, schema *encoding.Schema, exts *ExtensionRegistry, models *model.Set, vecs []vectors.Resolved, layer int, prior *PublishedTotals) (*TotalsPrepass, error) {
	p := &TotalsPrepass{stages: make([]totalsStage, 0, len(attrs)), layer: layer}
	twoPass := make([]bool, len(attrs))
	aliases := prevstate.U64Aliases(attrs, schema)
	for i, attr := range attrs {
		factory, ok := exts.LookupAttribute(attr.Type)
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("unknown attribute type: %s", attr.Type))
		}
		computer, err := factory(attr, schema)
		if err != nil {
			return nil, err
		}
		if err := BindAttributeWithAliases(computer, exts, models, vecs, aliases); err != nil {
			return nil, err
		}
		st := totalsStage{attr: attr, label: attributeSlotLabel(attr), nullable: EmitsNull(computer)}
		_, twoPassOnly := computer.(TwoPassAttribute)
		if pb, ok := computer.(prebuiltAttribute); ok && pb.prebuiltFit() {
			twoPassOnly = false // closed at bind: row-local (vectors.FitBound)
		}
		if mt, ok := computer.(MergeableTotals); ok {
			st.totals, st.row = mt, mt
			twoPass[i] = true
		} else if rl, ok := computer.(RowLocalAttribute); ok && !twoPassOnly {
			st.row = rl
		} else {
			return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
				fmt.Sprintf("attribute %s is not row-local; not eligible for the parallel total() pre-pass", attr.Type))
		}
		p.stages = append(p.stages, st)
	}
	p.plan = planTwoPassStages(attrs, twoPass, exts)
	if layer < 0 || layer >= p.plan.layers {
		return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
			fmt.Sprintf("parallel total() pre-pass layer %d is outside the plan's %d layers", layer, p.plan.layers))
	}
	p.needed = p.plan.needed[layer]
	// An earlier layer's formula is a finished total here: bind its
	// published values, so a stage reading it scores as on the serial
	// layer-L scan (its own Finalize'd values, the same bits).
	if prior != nil {
		p.prior = prior.values
	}
	for i := range p.stages {
		st := &p.stages[i]
		if st.totals == nil || p.plan.layer[i] >= layer {
			continue
		}
		values, ok := p.prior[i]
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
				fmt.Sprintf("parallel total() pre-pass layer %d: attribute slot %d (layer %d) has no published total() values", layer, i, p.plan.layer[i]))
		}
		if err := st.totals.PublishTotals(values); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// Layers is the plan's pre-pass layer count.
func (p *TotalsPrepass) Layers() int { return p.plan.layers }

// folds reports whether stage i is a total() formula this layer folds.
func (p *TotalsPrepass) folds(i int) bool {
	return p.stages[i].totals != nil && p.plan.layer[i] == p.layer
}

// Fold folds one filter-passing record: each needed stage writes its
// output in declared order, and each total-bearing formula of this
// layer folds its pre-pass at its own position (an earlier layer's is
// finished, a later layer's inputs are not available yet).
func (p *TotalsPrepass) Fold(r *Record) error {
	k := 0
	for i := range p.stages {
		st := &p.stages[i]
		if k < len(p.needed) && p.needed[k] == i {
			k++
			val, err := st.row.Row(r, st.attr.Field)
			if err != nil {
				return err
			}
			SetAttributeOutput(r, st.label, val, st.nullable)
			continue
		}
		if p.folds(i) {
			if err := st.totals.PrePass(r, st.attr.Field); err != nil {
				return err
			}
		}
	}
	return nil
}

// Merge folds another partition's pre-pass into p (AbsorbTotals per
// total-bearing formula of the layer). Order-free: the binned sums
// merge exactly.
func (p *TotalsPrepass) Merge(o *TotalsPrepass) error {
	if o == nil {
		return nil
	}
	if len(o.stages) != len(p.stages) {
		return errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"parallel total() pre-pass partitions disagree on the attribute count")
	}
	if o.layer != p.layer {
		return errors.NewCodedError(errors.PROCESSING_INTERNAL,
			fmt.Sprintf("parallel total() pre-pass partitions disagree on the layer: %d merged into %d", o.layer, p.layer))
	}
	for i := range p.stages {
		if !p.folds(i) {
			continue
		}
		part, err := o.stages[i].totals.PartialTotals()
		if err != nil {
			return err
		}
		if err := p.stages[i].totals.AbsorbTotals(part); err != nil {
			return err
		}
	}
	return nil
}

// Publish finalizes every total-bearing formula of the layer's merged
// pre-pass and returns them beside every earlier layer's values: the
// next layer's prior, and after the last layer the values phase 2
// binds.
func (p *TotalsPrepass) Publish() (*PublishedTotals, error) {
	out := &PublishedTotals{values: make(map[int][]float64, len(p.prior)+1)}
	for i, v := range p.prior {
		out.values[i] = v
	}
	for i := range p.stages {
		st := &p.stages[i]
		if !p.folds(i) {
			continue
		}
		if err := st.totals.Finalize(); err != nil {
			return nil, err
		}
		ft, ok := st.totals.(*formulaTotalAttribute)
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
				fmt.Sprintf("attribute %s has no published totals", st.attr.Type))
		}
		out.values[i] = ft.publishedValues()
	}
	return out, nil
}

// PublishedTotals is the merged, finalized phase-1 output: per
// attribute slot (declared index), the values its total() calls read.
type PublishedTotals struct {
	values map[int][]float64
}

// Bind is phase 2's bind hook: it publishes slot i's totals onto
// computer (a freshly built partition instance) when it is a
// total-bearing formula. A nil receiver binds nothing — an unbound
// total() formula then refuses its first Row (the patched read's
// PROCESSING_INTERNAL fault, surfaced through the formula evaluation),
// never reads a silent 0.
func (p *PublishedTotals) Bind(i int, computer AttributeComputer) error {
	mt, ok := computer.(MergeableTotals)
	if !ok || p == nil {
		return nil
	}
	values, ok := p.values[i]
	if !ok {
		return errors.NewCodedError(errors.PROCESSING_INTERNAL,
			fmt.Sprintf("attribute slot %d has no published total() values", i))
	}
	return mt.PublishTotals(values)
}

// TotalsPrepassRequest is req narrowed to what pre-pass layer layer
// reads — its filters, weights, zones and vectors, and only the
// attributes that layer evaluates (the stages a layer-L total()
// argument reads — an earlier layer's formula among them whole, it
// scores the row — then each layer-L formula reduced to its total()
// arguments, totalArgumentsOnly; declared order) — so the reducers
// project each pre-pass decode to the filter and that layer's total()
// argument fields (Service.decodeProjection over it). The narrowed
// request is a PROJECTION input only: the pre-pass itself runs the
// request's own attributes (NewTotalsPrepass).
// Aggregations, groups and matrices are dropped: phase 1 folds none of
// them. A layer outside the plan returns req itself (the full
// projection: correct, only wider; NewTotalsPrepass refuses it).
func TotalsPrepassRequest(req *types.Request, exts *ExtensionRegistry, layer int) *types.Request {
	plan := planTwoPassLayers(req.Attributes, exts)
	if layer < 0 || layer >= plan.layers {
		return req
	}
	var attrs []*types.Attribute
	k, needed := 0, plan.needed[layer]
	for i, a := range req.Attributes {
		if k < len(needed) && needed[k] == i {
			k++
			attrs = append(attrs, a)
			continue
		}
		if plan.twoPass[i] && plan.layer[i] == layer {
			attrs = append(attrs, totalArgumentsOnly(a))
		}
	}
	out := *req
	out.Attributes = attrs
	out.Aggregations = nil
	out.Matrices = nil
	out.Groups = nil
	return &out
}

// totalArgumentsOnly is a copy of a total-bearing formula whose
// expression lists only its distinct total() arguments ("[a1, a2]") —
// what its PrePass evaluates — so the projection keeps the argument
// fields (and the slot's weight / zone fields, which the copy keeps)
// and not the fields only the formula's row expression reads.
func totalArgumentsOnly(a *types.Attribute) *types.Attribute {
	out := *a
	out.Expression = "[" + strings.Join(totalcall.Args(a.Expression), ", ") + "]"
	return &out
}
