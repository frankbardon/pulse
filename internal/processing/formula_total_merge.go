package processing

import (
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/reprosum"
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
// The merge gate admits only the first cut (mergegate.ParallelTotals):
// ungrouped, one pre-pass layer, never a ProcessChain stage.

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

// TotalsPrepass is one partition's phase-1 state: the request's
// attributes built and bound like the reducers' row-local set, planned
// by the serial two-pass planner (planTwoPassStages), so each record
// evaluates exactly the layer-0 stages a total() argument reads and
// folds every formula's PrePass at its declared position — the record
// state the serial pre-pass scan observes.
type TotalsPrepass struct {
	stages []totalsStage
	needed []int // declared-order indices evaluated (Row + Set)
	plan   twoPassPlan
}

// NewTotalsPrepass builds one partition's phase-1 operators for attrs
// over schema, bound to the registry, the bound models and the
// resolved named vectors (the reducers' buildRowLocalAttrSpecs
// binding). Every attribute must be row-local or a total-bearing
// formula, in one pre-pass layer — the shape mergegate admits;
// anything else is PROCESSING_INTERNAL.
func NewTotalsPrepass(attrs []*types.Attribute, schema *encoding.Schema, exts *ExtensionRegistry, models *model.Set, vecs []vectors.Resolved) (*TotalsPrepass, error) {
	p := &TotalsPrepass{stages: make([]totalsStage, 0, len(attrs))}
	twoPass := make([]bool, len(attrs))
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
		if err := BindAttributeWithVectors(computer, exts, models, vecs); err != nil {
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
	if p.plan.layers != 1 {
		return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
			fmt.Sprintf("parallel total() pre-pass needs exactly one layer, the plan has %d (mergegate.ParallelTotals admitted it)", p.plan.layers))
	}
	p.needed = p.plan.needed[0]
	return p, nil
}

// Fold folds one filter-passing record: each needed stage writes its
// output in declared order, and each total-bearing formula folds its
// pre-pass at its own position.
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
		if st.totals != nil {
			if err := st.totals.PrePass(r, st.attr.Field); err != nil {
				return err
			}
		}
	}
	return nil
}

// Merge folds another partition's pre-pass into p (AbsorbTotals per
// total-bearing formula). Order-free: the binned sums merge exactly.
func (p *TotalsPrepass) Merge(o *TotalsPrepass) error {
	if o == nil {
		return nil
	}
	if len(o.stages) != len(p.stages) {
		return errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"parallel total() pre-pass partitions disagree on the attribute count")
	}
	for i := range p.stages {
		if p.stages[i].totals == nil {
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

// Publish finalizes every total-bearing formula of the merged pre-pass
// and returns the values phase 2 binds.
func (p *TotalsPrepass) Publish() (*PublishedTotals, error) {
	out := &PublishedTotals{values: make(map[int][]float64)}
	for i := range p.stages {
		st := &p.stages[i]
		if st.totals == nil {
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

// TotalsPrepassRequest is req narrowed to what the parallel total()
// pre-pass reads — its filters, weights, zones and vectors, and only
// the attributes the pre-pass evaluates (the layer-0 stages a total()
// argument reads, then the total-bearing formulas; declared order) —
// so the reducers project the pre-pass decode to the filter and
// total() argument fields (Service.decodeProjection over it).
// Aggregations, groups and matrices are dropped: phase 1 folds none of
// them. A plan with other than one layer returns req itself (the full
// projection: correct, only wider; NewTotalsPrepass refuses it).
func TotalsPrepassRequest(req *types.Request, exts *ExtensionRegistry) *types.Request {
	plan := planTwoPassLayers(req.Attributes, exts)
	if plan.layers != 1 {
		return req
	}
	var attrs []*types.Attribute
	k, needed := 0, plan.needed[0]
	for i, a := range req.Attributes {
		if k < len(needed) && needed[k] == i {
			k++
			attrs = append(attrs, a)
			continue
		}
		if plan.twoPass[i] {
			attrs = append(attrs, a)
		}
	}
	out := *req
	out.Attributes = attrs
	out.Aggregations = nil
	out.Matrices = nil
	out.Groups = nil
	return &out
}
