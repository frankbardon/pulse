package descriptor

import (
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// predict_pvalues.go answers PredictResult.PValues: how many inferential
// p-values a Request emits and how many no multiple-comparison
// correction reaches. No-execute like the rest of predict — the count
// reads the request, the resolved MultiplicityPlan and the schema's
// dictionaries, never a record.

// pvalueBasisRank orders the bases weakest-last so a contribution can
// only weaken the overall basis.
var pvalueBasisRank = map[string]int{
	descriptor.PValueBasisExact:      0,
	descriptor.PValueBasisDictionary: 1,
	descriptor.PValueBasisLowerBound: 2,
}

// countPValues builds PredictResult.PValues for req (defaults resolved)
// over schema, under plan (ResolveMultiplicity's; nil when nothing names
// a block, so nothing is corrected). Tests and post-tests contribute one
// p-value each, the Tukey HSD post-test none (already corrected). An
// inferential overlay kind contributes the p-values its payload carries,
// derived from the crosstab axes' dictionaries where an axis is one
// GROUP_CATEGORY over a categorical or packed_bool field, else counted
// as one (a lower bound). A test type or overlay kind the instance does
// not offer (hidden or never registered) is not counted. Nil when the
// request emits no p-value.
func countPValues(req *types.Request, schema *encoding.Schema, plan *MultiplicityPlan, opts *PredictOptions) *descriptor.PValueCount {
	inst := opts.instance()
	if req == nil {
		return nil
	}
	out := &descriptor.PValueCount{Basis: descriptor.PValueBasisExact, Threshold: descriptor.MultiplicityTriggerThreshold}
	add := func(n int, basis string, member bool) {
		out.Total += n
		if !member {
			out.Uncorrected += n
		}
		if pvalueBasisRank[basis] > pvalueBasisRank[out.Basis] {
			out.Basis = basis
		}
	}
	member := func(list []ResolvedMultiplicity, i int) bool {
		return i < len(list) && list[i].Member
	}
	var tests, postTests, overlays []ResolvedMultiplicity
	if plan != nil {
		tests, postTests, overlays = plan.Tests, plan.PostTests, plan.Overlays
	}
	for _, tier := range []struct {
		list []*types.Test
		plan []ResolvedMultiplicity
	}{{req.Tests, tests}, {req.PostTests, postTests}} {
		for i, t := range tier.list {
			if t == nil {
				continue
			}
			routed := opRoute(inst, t.Type)
			if routed == types.TEST_TUKEY_HSD || (!isKnownTestType(routed) && !isExtensionTestType(opts, t.Type)) {
				continue
			}
			add(1, descriptor.PValueBasisExact, member(tier.plan, i))
		}
	}
	rows, cols := 0, 0
	if req.Crosstab != nil {
		rows = axisBuckets(req.Crosstab.Rows, schema, inst)
		cols = axisBuckets(req.Crosstab.Columns, schema, inst)
	}
	for i := range req.Overlays {
		spec := &req.Overlays[i]
		routed := opRoute(inst, spec.Kind)
		if routed == "" || !overlayCapabilityFor(routed).Inferential {
			continue
		}
		n, basis := overlayPValueCount(routed, spec.Scope, rows, cols)
		add(n, basis, member(overlays, i))
	}
	if out.Total == 0 {
		return nil
	}
	return out
}

// axisBuckets is the bucket count of a crosstab axis read off the
// schema: the dictionary size of a lone GROUP_CATEGORY over a
// categorical field, 2 over a packed_bool; 0 (unknown) otherwise.
func axisBuckets(axis []*types.Group, schema *encoding.Schema, inst *InstanceSnapshot) int {
	if len(axis) != 1 || axis[0] == nil || schema == nil || opRoute(inst, axis[0].Type) != types.GROUP_CATEGORY {
		return 0
	}
	for _, f := range schema.Fields {
		if f.Name != axis[0].Field {
			continue
		}
		switch {
		case f.Type.IsCategorical() && f.Dictionary != nil:
			return f.Dictionary.Count()
		case f.Type == encoding.FieldTypePackedBool:
			return 2
		}
		return 0
	}
	return 0
}

// overlayPValueCount is how many p-values one inferential Request-host
// overlay layer carries over a rows x cols crosstab (0 = unknown
// extent): one for a whole-table kind; one per row / column for the
// per-row / per-column chi-square kinds; one per cell for Fisher's
// exact cell test; one per index pair per opposite-axis entry for the
// pairwise kinds. A kind or extent it cannot derive counts as one, a
// lower bound.
func overlayPValueCount(kind types.OverlayKind, scope types.OverlayScope, rows, cols int) (int, string) {
	pairs := func(k int) int { return k * (k - 1) / 2 }
	derived := func(n int, known bool) (int, string) {
		if !known {
			return 1, descriptor.PValueBasisLowerBound
		}
		return n, descriptor.PValueBasisDictionary
	}
	switch {
	case kind == types.OverlayKindChiSqMatrix, kind == types.OverlayKindChiSqVsRef,
		kind == types.OverlayKindChiSqVsPop, kind == types.OverlayKindKSVsPop:
		return 1, descriptor.PValueBasisExact
	case kind == types.OverlayKindChiSqRow:
		return derived(rows, rows > 0)
	case kind == types.OverlayKindChiSqCol:
		return derived(cols, cols > 0)
	case kind == types.OverlayKindFisherExactCell:
		return derived(rows*cols, rows > 0 && cols > 0)
	case strings.HasPrefix(string(kind), "OVERLAY_PAIRWISE_") && scope == types.OverlayScopeRow:
		return derived(pairs(rows)*cols, rows > 0 && cols > 0)
	case strings.HasPrefix(string(kind), "OVERLAY_PAIRWISE_") && scope == types.OverlayScopeColumn:
		return derived(pairs(cols)*rows, rows > 0 && cols > 0)
	}
	return 1, descriptor.PValueBasisLowerBound
}
