package descriptor

import (
	"math"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/groupedtests"
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
// p-value each — a grouped request's tier-1 tests one per group and its
// post-tests one per partition (see below) — the Tukey HSD post-test none (already corrected). An
// inferential overlay kind contributes the p-values its payload carries,
// derived from the crosstab axes' dictionaries where an axis is one
// GROUP_CATEGORY over a categorical or packed_bool field, else counted
// as one (a lower bound). A MAT_CORRELATION / MAT_PARTIAL_CORRELATION
// under params.inference contributes one per output pair (per bucket
// when grouped). A test type or overlay kind the instance does
// not offer (hidden or never registered) is not counted. Nil when the
// request emits no p-value.
func countPValues(req *types.Request, schema *encoding.Schema, plan *MultiplicityPlan, opts *PredictOptions) *descriptor.PValueCount {
	inst := opts.instance()
	if req == nil {
		return nil
	}
	out := &descriptor.PValueCount{Basis: descriptor.PValueBasisExact, Threshold: descriptor.MultiplicityTriggerThreshold}
	add := func(n int, basis string, member bool) { addPValues(out, n, basis, member) }
	member := func(list []ResolvedMultiplicity, i int) bool {
		return i < len(list) && list[i].Member
	}
	var tests, postTests, overlays []ResolvedMultiplicity
	if plan != nil {
		tests, postTests, overlays = plan.Tests, plan.PostTests, plan.Overlays
	}
	// A grouped request runs every tier-1 test once per group: each
	// contributes the EstimateGroups bound (an upper bound read off the
	// dictionaries) or, when a grouper's keys depend on the data, one
	// p-value as a lower bound.
	perTest, perTestBasis := groupsPValues(req.Groups, schema, inst)
	// A grouped request runs every post-test once per partition — the
	// leading PostTestDepth keys (every key but the last by default).
	perPost, perPostBasis := 1, descriptor.PValueBasisExact
	if depth, err := groupedtests.PostTestDepth(req.PostTests, req.Groups); err == nil && depth > 0 {
		perPost, perPostBasis = groupsPValues(req.Groups[:depth], schema, inst)
	}
	for _, tier := range []struct {
		list  []*types.Test
		plan  []ResolvedMultiplicity
		n     int
		basis string
	}{{req.Tests, tests, perTest, perTestBasis}, {req.PostTests, postTests, perPost, perPostBasis}} {
		for i, t := range tier.list {
			if t == nil {
				continue
			}
			routed := opRoute(inst, t.Type)
			if routed == types.TEST_TUKEY_HSD || (!isKnownTestType(routed) && !isExtensionTestType(opts, t.Type)) {
				continue
			}
			add(tier.n, tier.basis, member(tier.plan, i))
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
	// A matrix under params.inference (set, or implied by its own
	// multiplicity block) carries one p per distinct output pair — the
	// off-diagonal upper triangle, k(k-1)/2, the `matrix` family's m —
	// per bucket on a grouped request (the tier-1 tests' bound); a
	// member spec's pairs are corrected.
	if len(req.Matrices) > 0 {
		var matrices []ResolvedMultiplicity
		if plan != nil {
			matrices = plan.Matrices
		}
		if plans, err := resolveMatricesFor(req, schema, nil, inst); err == nil {
			for _, m := range plans {
				// An extension operator declaring a p-value key emits
				// one per output pair, like a built-in under inference.
				if !m.Inference && !(m.Extension && matrixPValueKey(inst, m.Type) != "") {
					continue
				}
				k := len(m.OutputIndices())
				if pairs := k * (k - 1) / 2; pairs > 0 {
					add(pairs*perTest, perTestBasis, member(matrices, m.Index))
				}
			}
		}
	}
	if out.Total == 0 {
		return nil
	}
	return out
}

// groupsPValues is how many times a test runs over groups' buckets and
// on what basis: once (exact) ungrouped; else the EstimateGroups bound
// (an upper bound read off the dictionaries), or one as a lower bound
// when a grouper's keys depend on the data.
func groupsPValues(groups []*types.Group, schema *encoding.Schema, inst *InstanceSnapshot) (int, string) {
	if len(groups) == 0 {
		return 1, descriptor.PValueBasisExact
	}
	if g, ok := EstimateGroups(groups, schema, inst, -1); ok && g > 0 && g <= math.MaxInt32 {
		return int(g), descriptor.PValueBasisDictionary
	}
	return 1, descriptor.PValueBasisLowerBound
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
// exact cell test and the adjusted residuals; one per index pair per
// opposite-axis entry for the pairwise kinds. A kind or extent it
// cannot derive counts as one, a lower bound.
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
		kind == types.OverlayKindChiSqVsPop, kind == types.OverlayKindKSVsPop,
		kind == types.OverlayKindSymmetry:
		return 1, descriptor.PValueBasisExact
	case kind == types.OverlayKindChiSqRow:
		return derived(rows, rows > 0)
	case kind == types.OverlayKindChiSqCol:
		return derived(cols, cols > 0)
	case kind == types.OverlayKindFisherExactCell, kind == types.OverlayKindStdResidual:
		return derived(rows*cols, rows > 0 && cols > 0)
	case strings.HasPrefix(string(kind), "OVERLAY_PAIRWISE_") && scope == types.OverlayScopeRow:
		return derived(pairs(rows)*cols, rows > 0 && cols > 0)
	case strings.HasPrefix(string(kind), "OVERLAY_PAIRWISE_") && scope == types.OverlayScopeColumn:
		return derived(pairs(cols)*rows, rows > 0 && cols > 0)
	}
	return 1, descriptor.PValueBasisLowerBound
}
