package descriptor

import (
	"math"
	"slices"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/groupedtests"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/internal/prevstate"
	"github.com/frankbardon/pulse/internal/regpenalty"
	"github.com/frankbardon/pulse/internal/sem"
	"github.com/frankbardon/pulse/internal/vectors"
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
// when grouped). A REG_OLS / REG_GLM regression contributes one per
// non-intercept predictor, corrected for the coefficients its own
// multiplicity block claims — none on an l1 / elasticnet fit without
// resample, whose p-values are null. An extension REG_* model
// contributes its explicit multiplicity coefficient list (exact) or one
// per non-intercept predictor (lower_bound). A test type, overlay kind or regression
// type the instance does not offer (hidden or never registered) is not
// counted. Nil when the
// request emits no p-value.
func countPValues(req *types.Request, schema *encoding.Schema, plan *MultiplicityPlan, opts *PredictOptions) *descriptor.PValueCount {
	return countPValuesVisit(req, schema, plan, opts, nil)
}

// pvalueVisit receives each corrected countPValues contribution: n
// p-values on basis joining the family rm resolved (a member slot).
type pvalueVisit func(n int, basis string, rm ResolvedMultiplicity)

// countPValuesVisit is countPValues also handing every corrected
// contribution to visit (nil visits nothing) — the one count Compose
// predict's per-slot families read, so they never drift from p_values.
func countPValuesVisit(req *types.Request, schema *encoding.Schema, plan *MultiplicityPlan, opts *PredictOptions, visit pvalueVisit) *descriptor.PValueCount {
	inst := opts.instance()
	if req == nil {
		return nil
	}
	out := &descriptor.PValueCount{Basis: descriptor.PValueBasisExact, Threshold: descriptor.MultiplicityTriggerThreshold}
	// addAt folds n p-values on basis for slot i of list: corrected iff
	// list[i] is a member, and then visited.
	addAt := func(n int, basis string, list []ResolvedMultiplicity, i int) {
		member := i < len(list) && list[i].Member
		addPValues(out, n, basis, member)
		if member && visit != nil && n > 0 {
			visit(n, basis, list[i])
		}
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
			addAt(tier.n, tier.basis, tier.plan, i)
		}
	}
	rows, cols := 0, 0
	if req.Crosstab != nil {
		// The dictionary-inheritance view: an ATTR_PREVIOUS_STATE output
		// axis is bounded by its state field's dictionary, as the
		// runtime's GROUP_CATEGORY keys it.
		keys := prevstate.KeySchema(req.Attributes, schema)
		rows = axisBuckets(req.Crosstab.Rows, keys, inst)
		cols = axisBuckets(req.Crosstab.Columns, keys, inst)
		if req.Crosstab.HasImplicitTotalColumn() {
			cols = 1 // the one implicit Total column
		}
	}
	for i := range req.Overlays {
		spec := &req.Overlays[i]
		routed := opRoute(inst, spec.Kind)
		if routed == "" || !overlayCapabilityFor(routed).Inferential {
			continue
		}
		n, basis := overlayPValueCount(routed, spec.Scope, rows, cols)
		if overlayhost.PriorTestKind(routed) {
			n, basis = priorPValueCount(req, schema, inst, rows)
		}
		// A multi-measure crosstab runs a pairwise kind once per measure
		// (the only kinds it runs): M times the single-measure count.
		if m := len(req.Crosstab.MeasureLabels()); m > 0 && basis == descriptor.PValueBasisDictionary {
			n *= m
		}
		addAt(n, basis, overlays, i)
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
		if plans, err := resolveMatricesFor(req, schema, nil, inst, opts.boundModels()); err == nil {
			for _, m := range plans {
				if m.Type == types.MAT_SEM {
					// A member MAT_SEM (its own block) corrects every
					// estimates row carrying a p: the free rows of each
					// level, then the := rows. A non-member's p-values
					// are not counted (they never were: a parameter
					// table is no battery of tests).
					if m.Index < len(matrices) && matrices[m.Index].Member {
						n, basis := semPValueRows(m)
						addAt(n*perTest, weakerBasis(basis, perTestBasis), matrices, m.Index)
					}
					continue
				}
				// An extension operator declaring a p-value key emits
				// one per output pair, like a built-in under inference.
				if !m.Inference && !(m.Extension && matrixPValueKey(inst, m.Type) != "") {
					continue
				}
				k := len(m.OutputIndices())
				if pairs := k * (k - 1) / 2; pairs > 0 {
					addAt(pairs*perTest, perTestBasis, matrices, m.Index)
				}
			}
		}
	}
	// A frequentist regression (REG_OLS, REG_GLM) carries one p-value
	// per non-intercept predictor — the hypotheses a readout tests; the
	// intercept's is not counted. A member regression's resolved
	// coefficients are corrected (its own list may name the intercept)
	// and every other predictor's p-value stays uncorrected. A Selection
	// fit counts its candidate predictors. REG_BAYES_LINEAR emits none,
	// and neither does an l1 / elasticnet fit without resample
	// (regpenalty.InferenceUndefined, the engines' own predicate): its
	// p-values are null, which the runtime fold leaves out of every
	// family's m (reference-oracles E2-S3). An extension REG_* model is
	// counted per its member list or its predictors (below) when it
	// declares PValues, else not at all.
	var regressions []ResolvedMultiplicity
	if plan != nil {
		regressions = plan.Regressions
	}
	for i, r := range req.Regressions {
		if r == nil {
			continue
		}
		member := i < len(regressions) && regressions[i].Member
		if _, decl, ok := regressionExtensionFor(inst, nil, r.Type); ok {
			// An extension model names its own coefficients, so its
			// count is read off the request: a member's explicit list
			// exactly, otherwise one per non-intercept predictor on the
			// lower_bound basis (the model may report more — a
			// nonlinear parameter — or fewer). A model not declaring
			// PValues emits none.
			if !decl.PValues {
				continue
			}
			if member && regressions[i].ExplicitCoefficients {
				addAt(len(regressions[i].Coefficients), descriptor.PValueBasisExact, regressions, i)
				continue
			}
			n := 0
			for _, p := range r.Predictors {
				if p != regressionInterceptKey {
					n++
				}
			}
			addAt(n, descriptor.PValueBasisLowerBound, regressions, i)
			continue
		}
		routed := opRoute(inst, r.Type)
		if _, ok := knownRegressionTypes[routed]; !ok || routed == types.REG_BAYES_LINEAR || regpenalty.InferenceUndefined(r) {
			continue
		}
		var corrected []string
		if member {
			corrected = regressions[i].Coefficients
		}
		uncorrected := 0
		for _, p := range r.Predictors {
			if p != regressionInterceptKey && !slices.Contains(corrected, p) {
				uncorrected++
			}
		}
		if member {
			addAt(len(corrected), descriptor.PValueBasisExact, regressions, i)
		}
		addPValues(out, uncorrected, descriptor.PValueBasisExact, false)
	}
	if out.Total == 0 {
		return nil
	}
	return out
}

// semPValueRows is a MAT_SEM spec's p-carrying estimates rows — the
// free parameter-table rows, every level's when grouped, an ordinal
// fit's threshold rows (vectors.SEMPlan.ThresholdRowsBound), then one
// per := line — and the count's basis: exact ungrouped; dictionary when
// grouped over the schema's levels (a level without rows lowers the
// real count); a lower bound on an integer group field, whose levels
// are its data (one group's rows counted). A free row whose SE is
// undefined has no p and joins no family at run time.
func semPValueRows(m vectors.Matrix) (int, string) {
	if m.SEM == nil || m.SEM.Model == nil {
		return 0, descriptor.PValueBasisLowerBound
	}
	defs := m.SEM.Model.DefinedCount()
	free := 0
	for _, e := range sem.SpecifiedRows(m.SEM.Model) {
		if e.Free {
			free++
		}
	}
	// An ordinal fit's threshold rows (`x | tk`) carry p too: the
	// schema's category bound (an upper bound, basis dictionary), else
	// the fewest a fitting member has (a lower bound).
	th, known := m.SEM.ThresholdRowsBound()
	if !m.SEM.Grouped() {
		switch {
		case !m.SEM.Ordinal():
			return free + defs, descriptor.PValueBasisExact
		case known:
			return free + th + defs, descriptor.PValueBasisDictionary
		}
		return free + th + defs, descriptor.PValueBasisLowerBound
	}
	if len(m.SEM.GroupLevels) < 2 {
		return free + th + defs, descriptor.PValueBasisLowerBound
	}
	levels := len(m.SEM.GroupLevels)
	n, err := sem.GroupFreeRows(m.SEM.Model, levels)
	if err != nil {
		return free + th + defs, descriptor.PValueBasisLowerBound
	}
	// Grouped ordinal: every level's thresholds, and each non-reference
	// level's free scale factors (group_equal thresholds).
	n += th*levels + m.SEM.FreeScaleRowsBound()*(levels-1)
	if !known {
		return n + defs, descriptor.PValueBasisLowerBound
	}
	return n + defs, descriptor.PValueBasisDictionary
}

// weakerBasis is the weaker of two p-value count bases.
func weakerBasis(a, b string) string {
	if pvalueBasisRank[b] > pvalueBasisRank[a] {
		return b
	}
	return a
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

// priorPValueCount is how many p-values an inferential prior-period
// layer carries: one per period after the first — rows - 1 on a crosstab
// whose one row grouper's buckets the dictionary bounds (rows), groups -
// 1 on a one-grouper SERIES host the dictionaries bound; one as a lower
// bound otherwise (a data-dependent axis, or partitions).
func priorPValueCount(req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot, rows int) (int, string) {
	switch {
	case req.Crosstab != nil && rows > 0:
		return rows - 1, descriptor.PValueBasisDictionary
	case req.Crosstab == nil && len(req.Groups) == 1:
		if g, basis := groupsPValues(req.Groups, schema, inst); basis == descriptor.PValueBasisDictionary {
			return g - 1, basis
		}
	}
	return 1, descriptor.PValueBasisLowerBound
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
