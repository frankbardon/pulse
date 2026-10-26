package processing

import (
	stderrors "errors"
	"fmt"
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/groupedtests"
	"github.com/frankbardon/pulse/internal/processing/window"
	"github.com/frankbardon/pulse/internal/seriespartition"
	"github.com/frankbardon/pulse/internal/slotfail"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// RowTest is a tier-1 statistical test consumed during the row scan
// alongside online aggregators. Implementations fold each filter-passing
// record into running state via UpdateRow and produce a TestResult via
// Finalize after the iterator is exhausted.
//
// Per-test state is per-instance; callers construct a fresh instance per
// Process call via a RowTestFactory.
//
// UpdateRow MUST be safe to call zero times (Finalize handles the empty
// input case). Implementations decide whether a row contributes; null
// values are typically skipped via the Record's NumericValue / StringValue
// helpers.
type RowTest interface {
	UpdateRow(record *Record) error
	Finalize() (*types.TestResult, error)
}

// PostTest is a tier-2 statistical test consumed after the window stage
// on the materialized result row set. Run is called once over the post-
// pipeline data and returns a TestResult; tier-2 tests are always
// buffered.
type PostTest interface {
	Run(rows []map[string]any) (*types.TestResult, error)
}

// RowTestFactory creates a RowTest from a specification.
type RowTestFactory func(spec *types.Test, schema *encoding.Schema) (RowTest, error)

// PostTestFactory creates a PostTest from a specification.
type PostTestFactory func(spec *types.Test, schema *encoding.Schema) (PostTest, error)

// rowTestRegistry maps test types to their row-test factory functions.
// Only tier-1-capable tests appear here. A type missing from this registry
// either has no tier-1 implementation yet or is exclusively tier 2.
var rowTestRegistry = map[types.TestType]RowTestFactory{
	types.TEST_T:                   newTTestRow,
	types.TEST_WELCH:               newTTestRow,
	types.TEST_CHISQ:               newChiSqRow,
	types.TEST_ANOVA_F:             newAnovaRow,
	types.TEST_KS:                  newKSRow,
	types.TEST_PAIRED_T:            newPairedTRow,
	types.TEST_PROP_Z:              newPropZRow,
	types.TEST_PEARSON_R:           newPearsonRRow,
	types.TEST_MANN_WHITNEY_U:      newMannWhitneyRow,
	types.TEST_WILCOXON_SR:         newWilcoxonSRRow,
	types.TEST_KRUSKAL_WALLIS:      newKruskalWallisRow,
	types.TEST_SPEARMAN_R:          newSpearmanRRow,
	types.TEST_KENDALL_TAU:         newKendallTauRow,
	types.TEST_ANOVA_WELCH:         newAnovaWelchRow,
	types.TEST_ANOVA_RM:            newAnovaRMRow,
	types.TEST_BROWN_FORSYTHE:      newBrownForsytheRow,
	types.TEST_FISHER_EXACT:        newFisherExactRow,
	types.TEST_SHAPIRO_WILK:        newShapiroWilkRow,
	types.TEST_Z_TWO_SAMPLE:        newZTestRow,
	types.TEST_HOTELLING_T2:        newHotellingRow,
	types.TEST_MANOVA:              newManovaRow,
	types.TEST_BARTLETT_SPHERICITY: newBartlettSphericityRow,
	types.TEST_BOX_M:               newBoxMRow,
	types.TEST_MARDIA:              newMardiaRow,
}

// postTestRegistry maps test types to their post-test factory functions.
// Tier-2 implementations consume the materialized result row set after
// windows. TEST_TUKEY_HSD is intentionally absent in this iteration —
// the studentized-range distribution required for its p-values is
// non-trivial and lands separately.
var postTestRegistry = map[types.TestType]PostTestFactory{
	types.TEST_ANOVA_F:        newAnovaPost,
	types.TEST_TREND:          newTrendPost,
	types.TEST_TUKEY_HSD:      newTukeyHSDPost,
	types.TEST_PEARSON_R:      newPearsonRPost,
	types.TEST_PAIRED_T:       newPairedTPost,
	types.TEST_SPEARMAN_R:     newSpearmanRPost,
	types.TEST_KENDALL_TAU:    newKendallTauPost,
	types.TEST_WILCOXON_SR:    newWilcoxonSRPost,
	types.TEST_ANOVA_WELCH:    newAnovaWelchPost,
	types.TEST_BROWN_FORSYTHE: newBrownForsythePost,
	types.TEST_SHAPIRO_WILK:   newShapiroWilkPost,
	types.TEST_KS:             newKSPost,
}

// rowTestEntry pairs a Test spec with its constructed RowTest instance
// and resolved output label. The processor drives one entry per filter-
// passing record (UpdateRow) and finalizes once per entry at end of pass.
type rowTestEntry struct {
	spec  *types.Test
	test  RowTest
	label string
	// idx is the entry's position in Request.Tests (the ComputePlan
	// may skip others; finalizeRowTests realigns by it).
	idx int
	// groupKey is the bucket a grouped run's entry folds (nil
	// ungrouped): its TestResult's GroupKey, and the group_key its
	// low-n_eff warnings name.
	groupKey types.AxisKey
}

// rowTestPlan is one tier-1 test the ComputePlan computes, ready to
// mint: the caller's spec (its label and result identity), the spec
// the factory reads (vector members resolved) and the factory. A
// grouped run mints one instance per plan per group bucket.
type rowTestPlan struct {
	spec     *types.Test
	resolved *types.Test
	factory  RowTestFactory
	label    string
	idx      int
	// probe is the instance planRowTests built to surface the factory's
	// refusals in request order (even when a grouped run mints no
	// bucket); the first mint takes it instead of building another.
	probe RowTest
}

// mintRowTests constructs one fresh instance of every plan, in plan
// order (the first mint hands out each plan's probe).
func (p *Processor) mintRowTests(plans []rowTestPlan, groupKey types.AxisKey) ([]rowTestEntry, error) {
	out := make([]rowTestEntry, 0, len(plans))
	for i := range plans {
		pl := &plans[i]
		inst := pl.probe
		pl.probe = nil
		if inst == nil {
			var err error
			if inst, err = pl.factory(pl.resolved, p.schema); err != nil {
				return nil, err
			}
		}
		workRowTestFolds.Add(1)
		out = append(out, rowTestEntry{spec: pl.spec, test: &faultLatchedTest{RowTest: inst}, label: pl.label, idx: pl.idx, groupKey: groupKey})
	}
	return out, nil
}

// faultLatchedTest is every minted RowTest's driver view. A row whose
// level read the test refused as corrupt record data
// (slotfail.CorruptRecordData — a split_by, contingency axis or outcome
// code no dictionary entry has, or an extension test's StringValue on
// one, U51 E5-S10) is LATCHED instead of failing the run: no later row
// is folded and Finalize returns the fault, so the slot fails alone
// (finalizeRowTests; a grouped run's bucket gets the sparse-group
// result) on every arm that drives UpdateRow. Every other UpdateRow
// error passes through, fatal as before.
type faultLatchedTest struct {
	RowTest
	fault error
}

func (t *faultLatchedTest) UpdateRow(record *Record) error {
	if t.fault != nil {
		return nil
	}
	err := t.RowTest.UpdateRow(record)
	if err == nil {
		return nil
	}
	var coded *errors.CodedError
	if stderrors.As(err, &coded) && slotfail.CorruptRecordData(coded) {
		t.fault = err
		return nil
	}
	return err
}

func (t *faultLatchedTest) Finalize() (*types.TestResult, error) {
	if t.fault != nil {
		return nil, t.fault
	}
	return t.RowTest.Finalize()
}

// unlatched is the minted test itself, for the optional-sibling
// assertions (lowNEffReporter) the latch view would hide.
func unlatched(t RowTest) RowTest {
	if l, ok := t.(*faultLatchedTest); ok {
		return l.RowTest
	}
	return t
}

// postTestEntry pairs a Test spec with its constructed PostTest instance
// and resolved output label.
type postTestEntry struct {
	spec  *types.Test
	test  PostTest
	label string
	idx   int
}

// buildRowTests constructs RowTest instances for the tier-1 tests
// (req.Tests, req being the stamped request) the ComputePlan computes;
// a skipped one is never constructed, so it folds nothing and raises
// nothing. Returns PULSE_TEST_UNKNOWN_TYPE if any built TestType is not
// registered as a row test.
func (p *Processor) buildRowTests(req *types.Request) ([]rowTestEntry, error) {
	plans, err := p.planRowTests(req)
	if err != nil || len(plans) == 0 {
		return nil, err
	}
	return p.mintRowTests(plans, nil)
}

// planRowTests resolves the tier-1 tests the ComputePlan computes into
// mintable plans (see buildRowTests for the refusals).
//
// A vector-input test's members resolve here (vectors.ResolveTests,
// the leaf the field-reference pass already ran) and are stashed on a
// COPY of its spec: the factory sees Fields / Fields2 as the resolved
// literal member lists (Vector / Vector2 cleared), so the factory
// signature — shared with extension tests — needs no request. A
// registered extension test's member sets resolve and rewrite the same
// way. The result label is the caller's spec's.
func (p *Processor) planRowTests(req *types.Request) ([]rowTestPlan, error) {
	tests := req.Tests
	if len(tests) == 0 {
		return nil, nil
	}
	inputs, verr := vectors.ResolveTests(req, p.schema, p.catalog(), func(t types.TestType) types.TestType {
		if p.exts.isHidden(string(t)) {
			return ""
		}
		return t
	}, func(name string) bool {
		_, ok := p.exts.LookupRowTest(types.TestType(name))
		return ok
	})
	if verr != nil {
		return nil, verr
	}
	out := make([]rowTestPlan, 0, len(tests))
	for i, t := range tests {
		if !p.compute.ComputesTest(i) {
			continue
		}
		factory, ok := p.exts.LookupRowTest(t.Type)
		if !ok {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_UNKNOWN_TYPE,
				fmt.Sprintf("test type %s is not registered as a row test", t.Type),
				map[string]any{"type": string(t.Type), "tier": "tests"})
		}
		spec := t
		if i < len(inputs) && inputs[i].Has() {
			spec = withResolvedMembers(t, inputs[i])
		}
		probe, err := factory(spec, p.schema)
		if err != nil {
			return nil, err
		}
		out = append(out, rowTestPlan{spec: t, resolved: spec, factory: factory, label: testLabel(t), idx: i, probe: probe})
	}
	return out, nil
}

// withResolvedMembers returns a copy of t whose Fields / Fields2 are
// the resolved member lists of in (Vector / Vector2 cleared).
func withResolvedMembers(t *types.Test, in vectors.TestInput) *types.Test {
	cp := *t
	cp.Vector, cp.Fields = "", append([]string(nil), in.Members.Members...)
	cp.Vector2, cp.Fields2 = "", nil
	if in.HasSecond {
		cp.Fields2 = append([]string(nil), in.Members2.Members...)
	}
	return &cp
}

// buildPostTests constructs PostTest instances for the tier-2 tests
// (Request.PostTests) the ComputePlan computes. Returns
// PULSE_TEST_UNKNOWN_TYPE if any built TestType is not registered as a
// post test.
func (p *Processor) buildPostTests(tests []*types.Test) ([]postTestEntry, error) {
	if len(tests) == 0 {
		return nil, nil
	}
	out := make([]postTestEntry, 0, len(tests))
	for i, t := range tests {
		if !p.compute.ComputesPostTest(i) {
			continue
		}
		factory, ok := p.exts.LookupPostTest(t.Type)
		if !ok {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_UNKNOWN_TYPE,
				fmt.Sprintf("test type %s is not registered as a post test", t.Type),
				map[string]any{"type": string(t.Type), "tier": "post_tests"})
		}
		inst, err := factory(groupedtests.WithoutPartition(t), p.schema)
		if err != nil {
			return nil, err
		}
		out = append(out, postTestEntry{spec: t, test: inst, label: testLabel(t), idx: i})
	}
	return out, nil
}

// testLabel returns the output label for a test result. Caller-supplied
// Label wins; otherwise synthesize "<TYPE>" or "<TYPE>_<field>".
func testLabel(t *types.Test) string {
	if t.Label != "" {
		return t.Label
	}
	if t.Field != "" {
		return fmt.Sprintf("%s_%s", t.Type, t.Field)
	}
	return string(t.Type)
}

// canRunRowTests reports whether every test in `tests` has a registered
// row-test factory (built-in or extension). Returns false on the first
// miss so the caller can fall through to the buffered path or surface
// PULSE_TEST_UNKNOWN_TYPE.
func canRunRowTests(tests []*types.Test, exts *ExtensionRegistry) bool {
	for _, t := range tests {
		if _, ok := exts.LookupRowTest(t.Type); !ok {
			return false
		}
	}
	return true
}

// finalizeRowTests calls Finalize on every entry and collects the
// TestResults in the same order as the request. Each entry's resolved
// label is written onto its TestResult so callers can look results up by
// caller-supplied alias. n is len(Request.Tests): when the ComputePlan
// skipped some entries the result holds nil at their positions.
func finalizeRowTests(entries []rowTestEntry, n int) ([]*types.TestResult, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	out := make([]*types.TestResult, 0, len(entries))
	for _, e := range entries {
		res, err := e.test.Finalize()
		if err != nil {
			se, ok := slotfail.From(slotfail.KindTests, err)
			if !ok {
				return nil, err
			}
			res = failedTestResult(e.spec, e.label, se)
		}
		if res != nil && res.Label == "" {
			res.Label = e.label
		}
		out = append(out, res)
	}
	idx := make([]int, len(entries))
	for i, e := range entries {
		idx[i] = e.idx
	}
	return realignTestResults(out, idx, n), nil
}

// failedTestResult is the result of a test slot whose data the test
// refused with a data-dependent code (slotfail.TestCodes): its label,
// type and alpha, NaN statistic and p-value (absent on the wire, which
// writes the identity and the error only) and the SlotError. Only that
// slot fails; the service lifts it to a PULSE_SLOT_FAILED warning.
func failedTestResult(spec *types.Test, label string, se *types.SlotError) *types.TestResult {
	alpha := spec.Alpha
	if alpha == 0 {
		alpha = 0.05
	}
	return &types.TestResult{
		Label:     label,
		Type:      spec.Type,
		Statistic: math.NaN(),
		PValue:    math.NaN(),
		Alpha:     alpha,
		Error:     se,
	}
}

// realignTestResults places results (one per built entry, at request
// positions idx) back at their request positions in an n-long slice,
// nil where the ComputePlan skipped the entry. Every entry built is the
// unplanned slice, returned as is.
func realignTestResults(results []*types.TestResult, idx []int, n int) []*types.TestResult {
	if len(results) == n {
		return results
	}
	out := make([]*types.TestResult, n)
	for i, r := range results {
		out[idx[i]] = r
	}
	return out
}

// runPostTests constructs the post-test instances the ComputePlan
// computes and runs each one over the materialized result row set.
// Returns nil when none is built so the caller can leave
// Response.PostTests as the zero value; a partially skipped slot holds
// nil at the skipped positions.
//
// On a grouped request (groups: the request's) every post-test runs
// once per PARTITION of rows (groupedtests.PostTestDepth: the rows
// sharing the leading depth group keys, every key but the last by
// default) — see runPartitionedPostTests. Depth 0 (ungrouped, one
// grouper, or partition_by []) is the one run over every row, unchanged.
// The warnings are the partitioned run's sparse-partition warnings.
func (p *Processor) runPostTests(tests []*types.Test, groups []*types.Group, rows []map[string]any) ([]*types.TestResult, []*types.ResponseWarning, error) {
	return p.runPostTestsOrdered(tests, groups, groups, rows)
}

// runPostTestsOrdered is runPostTests whose row key columns are the key
// columns of keyGroups (a crosstab's row and column axes, unpartitioned:
// groups nil): a keyOrderedPostTest (TEST_TREND) whose order_by names
// one sorts it by that grouper's intrinsic key order (postTestKeyOrders).
func (p *Processor) runPostTestsOrdered(tests []*types.Test, groups, keyGroups []*types.Group, rows []map[string]any) ([]*types.TestResult, []*types.ResponseWarning, error) {
	depth, derr := groupedtests.PostTestDepth(tests, groups)
	if derr != nil {
		return nil, nil, derr
	}
	entries, err := p.buildPostTests(tests)
	if err != nil {
		return nil, nil, err
	}
	if len(entries) == 0 {
		return nil, nil, nil
	}
	orders := p.postTestKeyOrders(entries, keyGroups)
	if depth > 0 {
		return p.runPartitionedPostTests(entries, len(tests), groups, depth, rows, orders)
	}
	out := make([]*types.TestResult, 0, len(entries))
	idx := make([]int, 0, len(entries))
	for _, e := range entries {
		stampKeyOrders(e.test, orders)
		workPostTestRuns.Add(1)
		res, err := e.test.Run(rows)
		if err != nil {
			se, ok := slotfail.From(slotfail.KindPostTests, err)
			if !ok {
				return nil, nil, err
			}
			res = failedTestResult(e.spec, e.label, se)
		}
		if res != nil && res.Label == "" {
			res.Label = e.label
		}
		out = append(out, res)
		idx = append(idx, e.idx)
	}
	return realignTestResults(out, idx, len(tests)), nil, nil
}

// runPartitionedPostTests runs every built post-test once per partition
// of rows — key equality on the leading depth group key columns
// (seriespartition.Partition), first-appearance order of the final Data
// rows, each partition's rows in Data order — so a partition's result
// is the post-test's result over a request holding only that partition's
// rows. The output is flat: n (len(Request.PostTests)) x partitions,
// TEST-MAJOR, each result carrying the partition key as GroupKey; a
// post-test the ComputePlan skips is nil at each of its positions, so
// entry k belongs to Request.PostTests[k / partitions]
// (groupedtests.SpecIndex). A partition whose rows the test refuses with
// a data-dependent code (sparseTestCodes) gets a null-statistics result
// plus one warning naming its group_key; the others proceed. No rows,
// no results.
func (p *Processor) runPartitionedPostTests(entries []postTestEntry, n int, groups []*types.Group, depth int, rows []map[string]any, orders window.KeyOrders) ([]*types.TestResult, []*types.ResponseWarning, error) {
	keys := seriespartition.RowKeys(rows, seriespartition.KeyColumns(groups))
	parts := seriespartition.Partition(keys, depth)
	if len(parts) == 0 {
		return nil, nil, nil
	}
	out := make([]*types.TestResult, n*len(parts))
	var warns []*types.ResponseWarning
	for _, e := range entries {
		factory, _ := p.exts.LookupPostTest(e.spec.Type) // buildPostTests resolved it
		for k, part := range parts {
			sub := make([]map[string]any, len(part))
			for j, r := range part {
				sub[j] = rows[r]
			}
			partKey := append(types.AxisKey(nil), keys[part[0]][:depth]...)
			inst, err := factory(groupedtests.WithoutPartition(e.spec), p.schema)
			if err != nil {
				return nil, nil, err
			}
			stampKeyOrders(inst, orders)
			workPostTestRuns.Add(1)
			res, err := inst.Run(sub)
			if err != nil {
				var coded *errors.CodedError
				if !stderrors.As(err, &coded) || !sparseTestCodes[coded.Code] {
					return nil, nil, err
				}
				res, warn := sparseTestResult(e.spec, e.label, e.idx, partKey, coded, "partition")
				out[e.idx*len(parts)+k] = res
				warns = append(warns, warn)
				continue
			}
			if res != nil {
				if res.Label == "" {
					res.Label = e.label
				}
				res.GroupKey = partKey
			}
			out[e.idx*len(parts)+k] = res
		}
	}
	return out, warns, nil
}

// keyOrderedPostTest is a post-test whose order_by sorts a grouper key
// column by that grouper's intrinsic key order rather than byte-wise
// (TEST_TREND): the run stamps the per-column orders before Run.
type keyOrderedPostTest interface {
	setKeyOrders(orders window.KeyOrders)
}

// stampKeyOrders hands orders to t when it orders by grouper keys.
func stampKeyOrders(t PostTest, orders window.KeyOrders) {
	if ko, ok := t.(keyOrderedPostTest); ok && orders != nil {
		ko.setKeyOrders(orders)
	}
}

// postTestKeyOrders is the run's grouper key orders (groupKeyOrders)
// when some entry orders by keys (keyOrderedPostTest), else nil.
func (p *Processor) postTestKeyOrders(entries []postTestEntry, groups []*types.Group) window.KeyOrders {
	for _, e := range entries {
		if _, ok := e.test.(keyOrderedPostTest); ok {
			return windowKeyOrders(p.groupKeyOrders(groups))
		}
	}
	return nil
}
