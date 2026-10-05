package processing

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// Row weighting — the engine half of .claude/reference/weighting.md.
//
// The resolved weight reaches an aggregator through its SPEC (the
// design recorded in weighting.md, "Threading"): StampWeights rewrites
// a request's aggregation slots — on a copy, never the caller's value —
// so each slot's own `weight` IS its resolved weight: set when a weight
// applies, `null` when none does. A factory then reads slotWeight(agg)
// and returns the weighted form, and every execution mode (buffered,
// streaming, grouped streaming, two-pass, the crosstab arms, both
// parallel reducers) weights identically because they all construct
// aggregators from the same stamped spec. The per-row weight value is
// read off the record inside the aggregator.
//
// The resolver (internal/descriptor.ResolveWeights) has already refused
// every request whose weights cannot apply, so stamping never refuses;
// it only decides, slot by slot, by the same precedence and the same
// internal/weighting class table.

// weightedMeanField returns AGG_WEIGHTED_MEAN's params.weight_field,
// "" otherwise (mirrors internal/descriptor.WeightedMeanParamField).
func weightedMeanField(a *types.Aggregation) string {
	if a == nil || a.Type != types.AGG_WEIGHTED_MEAN || len(a.Params) == 0 {
		return ""
	}
	var p weightedMeanParams
	if json.Unmarshal(a.Params, &p) != nil {
		return ""
	}
	return p.WeightField
}

// effectiveAggWeight is the weight in force on one aggregation slot:
// its own `weight` (null ⇒ none), AGG_WEIGHTED_MEAN's weight_field,
// the request weight, then the instance default.
func effectiveAggWeight(a *types.Aggregation, reqW, def *types.WeightSpec) *types.WeightSpec {
	switch {
	case a.Weight.IsNull():
		return nil
	case !a.Weight.IsZero():
		return a.Weight.Spec()
	}
	if f := weightedMeanField(a); f != "" {
		return &types.WeightSpec{Field: f, Kind: types.WeightKindProbability}
	}
	if reqW != nil {
		return reqW
	}
	return def
}

// stampAgg returns a copy of a whose `weight` is its resolved state:
// the applied weight (kind spelled out) on a weight-aware operator,
// `null` everywhere else.
func stampAgg(a *types.Aggregation, reqW, def *types.WeightSpec, aware func(string) bool) *types.Aggregation {
	if a == nil {
		return nil
	}
	c := *a
	if w := effectiveAggWeight(a, reqW, def); w != nil && aware(string(a.Type)) {
		c.Weight = types.SlotWeightOf(types.WeightSpec{Field: w.Field, Kind: w.EffectiveKind()})
	} else {
		c.Weight = types.NullSlotWeight()
	}
	return &c
}

// effectiveSlotWeight is the weight in force on a non-aggregation slot
// (a test or an attribute): its own `weight` (null ⇒ none), the
// request weight, then the instance default.
func effectiveSlotWeight(own types.SlotWeight, reqW, def *types.WeightSpec) *types.WeightSpec {
	switch {
	case own.IsNull():
		return nil
	case !own.IsZero():
		return own.Spec()
	case reqW != nil:
		return reqW
	}
	return def
}

// stampSlotWeight is the resolved `weight` of a test or attribute
// slot: the applied weight (kind spelled out) on a weight-aware
// operator — a built-in row test whose class has a kind
// (weighting.IsAware) or a WeightAware extension — `null` on any other
// slot that names a weight, and own unchanged otherwise. ok=false
// means "no change". The resolver already refused a weight in force on
// a non-aware extension test or attribute, on every refused built-in
// test and on every post-test, and a probability weight on a
// frequency-only one, so a slot that keeps a set weight here is one the
// weight applies to.
func stampSlotWeight(own types.SlotWeight, aware bool, reqW, def *types.WeightSpec) (types.SlotWeight, bool) {
	if aware {
		if w := effectiveSlotWeight(own, reqW, def); w != nil {
			return types.SlotWeightOf(types.WeightSpec{Field: w.Field, Kind: w.EffectiveKind()}), true
		}
		return types.NullSlotWeight(), !own.IsNull()
	}
	if own.Spec() != nil {
		// A built-in row-local attribute skips its weight; nulling it
		// keeps the slot out of the invalid-row tally.
		return types.NullSlotWeight(), true
	}
	return own, false
}

// StampWeights is StampWeightsWith over the built-in class table alone
// (no extension operators).
func StampWeights(req *types.Request, def *types.WeightSpec) *types.Request {
	return StampWeightsWith(req, def, nil)
}

// StampWeightsWith returns req with every aggregation slot (the
// top-level aggregations, the crosstab cell and margin aggregations)
// carrying its resolved weight, def being pulse.Options.DefaultWeight
// and exts the instance's extension registry (nil: built-ins only). An
// extension aggregator is weight-aware iff its registration declared
// WeightAware; a weight-aware row test (`tests`: a built-in whose class
// has a kind, read by its factory off the slot, or a WeightAware
// extension, which reads it through extend.Record.Weight()), a
// WeightAware extension post-test or attribute slot, and a built-in
// regression whose class has a kind, carries its resolved weight too. When nothing names
// a weight — no request weight, no default, no slot weight, no
// AGG_WEIGHTED_MEAN weight_field — req itself is returned, so an
// unweighted request executes the unchanged object. Otherwise the
// result is a shallow copy with copied slots; req is never mutated.
// Idempotent: stamping a stamped request changes nothing.
func StampWeightsWith(req *types.Request, def *types.WeightSpec, exts *ExtensionRegistry) *types.Request {
	if req == nil || !namesWeight(req, def) {
		return req
	}
	// weighting.IsAware covers ClassFrequencyOnly too: the resolver
	// has already refused a probability weight on such a slot, so a
	// weight that reaches it here is a frequency weight to apply.
	aware := func(op string) bool {
		return weighting.IsAware(op) || exts.IsExtensionWeightAware("aggregator", op)
	}
	c := *req
	c.Aggregations = make([]*types.Aggregation, len(req.Aggregations))
	for i, a := range req.Aggregations {
		c.Aggregations[i] = stampAgg(a, req.Weight, def, aware)
	}
	if req.Crosstab != nil {
		ct := *req.Crosstab
		ct.Cell = stampAgg(req.Crosstab.Cell, req.Weight, def, aware)
		ct.MarginAggregations = make([]*types.Aggregation, len(req.Crosstab.MarginAggregations))
		for i, a := range req.Crosstab.MarginAggregations {
			ct.MarginAggregations[i] = stampAgg(a, req.Weight, def, aware)
		}
		c.Crosstab = &ct
	}
	// Built-in row tests read the weight; a built-in post-test reads
	// aggregated rows and never does (the resolver refused it).
	c.Tests = stampTests(req.Tests, req.Weight, def, func(op string) bool {
		return weighting.IsAware(op) || exts.IsExtensionWeightAware("test", op)
	})
	c.PostTests = stampTests(req.PostTests, req.Weight, def, func(op string) bool {
		return exts.IsExtensionWeightAware("test", op)
	})
	// A built-in regression reads the weight when its class has a kind
	// (there is no extension REG_* category); the resolver has already
	// refused a weight in force on a pending regression, on one carrying
	// a resample or selection modifier, and a probability weight on a
	// frequency-only one.
	if len(req.Regressions) > 0 {
		c.Regressions = make([]*types.RegressionSpec, len(req.Regressions))
		for i, r := range req.Regressions {
			c.Regressions[i] = r
			if r == nil {
				continue
			}
			// A resample / selection modifier refits on row subsets with no
			// weighted form: such a slot is never stamped (the resolver
			// refuses any weight in force on it).
			aware := weighting.IsAware(string(r.Type)) && r.Resample == "" && r.Selection == ""
			if w, ok := stampSlotWeight(r.Weight, aware, req.Weight, def); ok {
				cp := *r
				cp.Weight = w
				c.Regressions[i] = &cp
			}
		}
	}
	if len(req.Attributes) > 0 {
		c.Attributes = make([]*types.Attribute, len(req.Attributes))
		for i, a := range req.Attributes {
			c.Attributes[i] = a
			if a == nil {
				continue
			}
			if w, ok := stampSlotWeight(a.Weight, exts.IsExtensionWeightAware("attribute", string(a.Type)), req.Weight, def); ok {
				cp := *a
				cp.Weight = w
				c.Attributes[i] = &cp
			}
		}
	}
	return &c
}

// stampTests stamps a test slice (see stampSlotWeight), aware deciding
// per operator; a nil or empty slice is returned as-is.
func stampTests(tests []*types.Test, reqW, def *types.WeightSpec, aware func(string) bool) []*types.Test {
	if len(tests) == 0 {
		return tests
	}
	out := make([]*types.Test, len(tests))
	for i, t := range tests {
		out[i] = t
		if t == nil {
			continue
		}
		if w, ok := stampSlotWeight(t.Weight, aware(string(t.Type)), reqW, def); ok {
			cp := *t
			cp.Weight = w
			out[i] = &cp
		}
	}
	return out
}

func namesWeight(req *types.Request, def *types.WeightSpec) bool {
	if req.Weight != nil || def != nil {
		return true
	}
	named := func(a *types.Aggregation) bool {
		return a != nil && (!a.Weight.IsZero() || weightedMeanField(a) != "")
	}
	for _, a := range req.Aggregations {
		if named(a) {
			return true
		}
	}
	if ct := req.Crosstab; ct != nil {
		if named(ct.Cell) {
			return true
		}
		for _, a := range ct.MarginAggregations {
			if named(a) {
				return true
			}
		}
	}
	for _, t := range append(append([]*types.Test(nil), req.Tests...), req.PostTests...) {
		if t != nil && t.Weight.Spec() != nil {
			return true
		}
	}
	for _, r := range req.Regressions {
		if r != nil && r.Weight.Spec() != nil {
			return true
		}
	}
	for _, a := range req.Attributes {
		if a != nil && a.Weight.Spec() != nil {
			return true
		}
	}
	return false
}

// Weight satisfies extend.Record: the engine's own row carries no
// per-slot weight (slot weights differ within one request), so it
// always reports (0, false). A WeightAware extension operator on a
// weighted slot is handed a per-slot view instead (the root extension
// adapter), whose Weight reports the row's valid weight.
func (r *Record) Weight() (float64, bool) { return 0, false }

// readWeight reads and judges one row's weight under spec.
func readWeight(r *Record, spec *types.WeightSpec) (float64, weighting.Reason) {
	w, ok := r.NumericValue(spec.Field)
	return w, weighting.Classify(w, ok, spec.EffectiveKind())
}

// WeightFloor tallies the weighted universal-floor keys of one
// aggregation slot — sum_weights, n_eff, n_weight_invalid — beside the
// orchestrator's {n, n_null}. It is inert (every method a no-op) on an
// unweighted slot, so an unweighted Components entry is unchanged.
// Every execution mode feeds it the same filter-passing rows it feeds
// {n, n_null}; the parallel reducers fold partitions with Merge.
type WeightFloor struct {
	spec    *types.WeightSpec
	sumW    float64
	sumWSq  float64
	invalid int
}

// NewWeightFloor returns the floor tally for a stamped slot.
func NewWeightFloor(agg *types.Aggregation) WeightFloor {
	return WeightFloor{spec: slotWeight(agg)}
}

// Observe folds one filter-passing record: a row whose value is
// present contributes its weight when valid and counts as invalid
// otherwise; a null value contributes nothing (it is n_null).
func (f *WeightFloor) Observe(r *Record, field string) {
	if f.spec == nil || !FieldPresent(r, field) {
		return
	}
	w, reason := readWeight(r, f.spec)
	if reason != weighting.Valid {
		f.invalid++
		return
	}
	f.sumW += w
	f.sumWSq += w * w
}

// Merge folds another partition's tally of the same slot.
func (f *WeightFloor) Merge(o WeightFloor) {
	f.sumW += o.sumW
	f.sumWSq += o.sumWSq
	f.invalid += o.invalid
}

// Stamp writes the weighted floor keys onto a Components entry; no-op
// on an unweighted slot.
func (f *WeightFloor) Stamp(e *types.AggregationComponents) {
	if f.spec == nil || e == nil {
		return
	}
	sumW, invalid := f.sumW, f.invalid
	e.SumWeights = &sumW
	e.NWeightInvalid = &invalid
	if f.spec.EffectiveKind() == types.WeightKindProbability {
		nEff := kishNEff(f.sumW, f.sumWSq)
		e.NEff = &nEff
	}
}

// stampMap writes the weighted floor keys onto a flat crosstab
// component map (cell, margin or auxiliary margin) beside {n, n_null};
// no-op on an unweighted slot. A key the operator already emits wins:
// AGG_WEIGHTED_MEAN's own sum_weights / n_eff are the same Σw over the
// same rows, and its n_eff is part of its declared operator schema even
// under kind frequency.
func (f *WeightFloor) stampMap(m map[string]any) {
	if f.spec == nil || m == nil {
		return
	}
	var e types.AggregationComponents
	f.Stamp(&e)
	setIfAbsent := func(k string, v any) {
		if _, ok := m[k]; !ok {
			m[k] = v
		}
	}
	setIfAbsent("sum_weights", *e.SumWeights)
	setIfAbsent("n_weight_invalid", *e.NWeightInvalid)
	if e.NEff != nil {
		setIfAbsent("n_eff", *e.NEff)
	}
}

// WeightRowTally counts, per weight field, the filter-passing rows
// whose weight is invalid, by reason. It backs the response's single
// PULSE_WEIGHT_INVALID_ROWS warning per weight field. A nil tally (no
// weighted slot) is valid and inert.
type WeightRowTally struct {
	entries []weightTallyEntry
}

type weightTallyEntry struct {
	field string
	// frequency: some slot reads this field as a frequency weight, so
	// a fractional value is invalid for it.
	frequency bool
	byReason  map[weighting.Reason]int64
}

// NewWeightRowTally builds the tally for a stamped request's weighted
// slots — the top-level aggregations, the crosstab cell, the crosstab
// margin aggregations, every weight-aware row test (built-in or
// extension), every weight-aware regression and any WeightAware
// extension attribute; nil when no slot is weighted.
func NewWeightRowTally(req *types.Request) *WeightRowTally {
	if req == nil {
		return nil
	}
	idx := map[string]int{}
	var t WeightRowTally
	slots := req.Aggregations
	if ct := req.Crosstab; ct != nil {
		// The crosstab cell and auxiliary margin slots weight rows too.
		slots = append(append(append([]*types.Aggregation(nil), slots...), ct.Cell), ct.MarginAggregations...)
	}
	weights := make([]*types.WeightSpec, 0, len(slots)+len(req.Tests)+len(req.Regressions)+len(req.Attributes))
	for _, a := range slots {
		weights = append(weights, slotWeight(a))
	}
	// A weight-aware row test (built-in or extension) or attribute
	// reads the row weight too (StampWeightsWith left its weight set);
	// tier-2 post tests see no rows.
	for _, t := range req.Tests {
		if t != nil {
			weights = append(weights, t.Weight.Spec())
		}
	}
	for _, r := range req.Regressions {
		if r != nil {
			weights = append(weights, r.Weight.Spec())
		}
	}
	for _, a := range req.Attributes {
		if a != nil {
			weights = append(weights, a.Weight.Spec())
		}
	}
	for _, w := range weights {
		if w == nil {
			continue
		}
		i, ok := idx[w.Field]
		if !ok {
			i = len(t.entries)
			idx[w.Field] = i
			t.entries = append(t.entries, weightTallyEntry{field: w.Field, byReason: map[weighting.Reason]int64{}})
		}
		if w.EffectiveKind() == types.WeightKindFrequency {
			t.entries[i].frequency = true
		}
	}
	if len(t.entries) == 0 {
		return nil
	}
	sort.Slice(t.entries, func(i, j int) bool { return t.entries[i].field < t.entries[j].field })
	return &t
}

// Observe counts one filter-passing record.
func (t *WeightRowTally) Observe(r *Record) {
	if t == nil {
		return
	}
	for i := range t.entries {
		e := &t.entries[i]
		kind := types.WeightKindProbability
		if e.frequency {
			kind = types.WeightKindFrequency
		}
		w, ok := r.NumericValue(e.field)
		if reason := weighting.Classify(w, ok, kind); reason != weighting.Valid {
			e.byReason[reason]++
		}
	}
}

// ObserveAll counts every record of a buffered set.
func (t *WeightRowTally) ObserveAll(records []*Record) {
	if t == nil {
		return
	}
	for _, r := range records {
		t.Observe(r)
	}
}

// Merge folds another partition's tally of the same request.
func (t *WeightRowTally) Merge(o *WeightRowTally) {
	if t == nil || o == nil {
		return
	}
	for i := range t.entries {
		if i >= len(o.entries) {
			return
		}
		for r, n := range o.entries[i].byReason {
			t.entries[i].byReason[r] += n
		}
	}
}

// Apply attaches one PULSE_WEIGHT_INVALID_ROWS warning per weight field
// that excluded rows — details {field, count, by_reason{null, negative,
// nan_inf, non_integer_frequency}} — or, under strict, returns the
// first as an error instead.
func (t *WeightRowTally) Apply(resp *types.Response, strict bool) error {
	if t == nil || resp == nil {
		return nil
	}
	for _, e := range t.entries {
		var count int64
		by := make(map[string]any, len(weighting.InvalidReasons))
		for _, r := range weighting.InvalidReasons {
			by[r.Key()] = e.byReason[r]
			count += e.byReason[r]
		}
		if count == 0 {
			continue
		}
		msg := fmt.Sprintf("%d row(s) had an invalid weight in %q and were excluded from the weighted figures (invalid weights are never coerced)", count, e.field)
		details := map[string]any{"field": e.field, "count": count, "by_reason": by}
		if strict {
			return errors.NewCodedErrorWithDetails(errors.PULSE_WEIGHT_INVALID_ROWS, msg, details)
		}
		resp.Warnings = append(resp.Warnings, &types.ResponseWarning{
			Code:    string(errors.PULSE_WEIGHT_INVALID_ROWS),
			Message: msg,
			Details: details,
		})
	}
	return nil
}
