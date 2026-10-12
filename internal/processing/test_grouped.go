package processing

import (
	stderrors "errors"
	"fmt"
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/groupedtests"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/types"
)

// test_grouped.go is Request.Tests on a grouped request (U46 E3): every
// tier-1 test runs once per COMPOSITE group key — the buckets the
// grouped Response.Data rows are, from the same Grouper.Group /
// partitionComposite partition (a fan-out grouper adds a record to
// several buckets). A bucket's instances fold that bucket's records in
// record order, exactly the rows and order an ungrouped run filtered to
// the one group folds, so a per-group result is that run's result.
//
// Response.Tests is flat: len(Request.Tests) x groups entries,
// TEST-MAJOR (every group of tests[0], then every group of tests[1]),
// the groups in the final Data row order (Sort included), each carrying
// TestResult.GroupKey. A test the ComputePlan skips is nil at each of
// its positions, so entry k always belongs to Request.Tests[k / groups]
// (groupedtests.SpecIndex). Grouped tests run on the buffered path only
// (canStream routes tests + groups buffered), which is where every
// grouped request with a test ran before.
//
// A group whose data cannot carry the test — a single split level, too
// few rows, zero variance, a degenerate table, an undefined correlation
// (sparseTestCodes) — gets a result with null statistic and p_value plus
// one Response warning under the test's own code, Details naming the
// group_key; the other groups proceed. An ungrouped run still refuses
// the same data with the error.

// sparseTestCodes are the data-dependent Finalize refusals a grouped run
// turns into a null-statistics result for the one group they hit.
var sparseTestCodes = map[errors.Code]bool{
	errors.PULSE_TEST_SPLIT_GROUPS_LT_2:      true,
	errors.PULSE_TEST_INSUFFICIENT_N:         true,
	errors.PULSE_TEST_VARIANCE_ZERO:          true,
	errors.PULSE_TEST_CONTINGENCY_DEGENERATE: true,
	errors.PULSE_TEST_CORRELATION_UNDEFINED:  true,
}

// groupedRowTests is a grouped run's tier-1 test state: the mintable
// plans and one instance set per bucket key. A nil *groupedRowTests is
// a request without tests (or without groups, or whose ComputePlan
// skips every test): every method is a no-op.
type groupedRowTests struct {
	plans   []rowTestPlan
	nTests  int
	buckets map[string][]rowTestEntry
	limits  limits.Limits
}

// buildGroupedRowTests returns empty per-bucket test state for a
// grouped req (the stamped request), nil when req is ungrouped or
// carries no tier-1 test. It refuses a test whose split field is also
// a grouper (groupedtests.SplitRefusal — the refusal predict reports)
// and surfaces every factory refusal in request order, before any
// bucket folds.
func (p *Processor) buildGroupedRowTests(req *types.Request) (*groupedRowTests, error) {
	if len(req.Groups) == 0 || len(req.Tests) == 0 {
		return nil, nil
	}
	if err := groupedtests.SplitRefusal(req.Tests, req.Groups); err != nil {
		return nil, err
	}
	plans, err := p.planRowTests(req)
	if err != nil || len(plans) == 0 {
		return nil, err
	}
	return &groupedRowTests{
		plans:   plans,
		nTests:  len(req.Tests),
		buckets: make(map[string][]rowTestEntry),
		limits:  p.limits,
	}, nil
}

// admit checks the grouped test fan-out against MaxGroups before any
// bucket folds: every test runs once per group, so tests x groups
// counts toward the limit (predict grades the same product).
func (g *groupedRowTests) admit(groups int) error {
	if g == nil {
		return nil
	}
	if err := limits.Check(g.limits, limits.MaxGroups, int64(groups)*int64(g.nTests)); err != nil {
		return err
	}
	return nil
}

// fold mints bucket key's instances (its GroupKey tuple) and folds the
// bucket's records into them, in record order.
func (g *groupedRowTests) fold(p *Processor, key string, tuple types.AxisKey, records []*Record) error {
	if g == nil {
		return nil
	}
	entries, err := p.mintRowTests(g.plans, tuple)
	if err != nil {
		return err
	}
	g.buckets[key] = entries
	for _, e := range entries {
		for _, rec := range records {
			if err := e.test.UpdateRow(rec); err != nil {
				return err
			}
		}
	}
	return nil
}

// finalize renders the per-bucket results over keys — the final order
// of the Data rows — test-major, then bucket; the flat entries (for
// applyLowNEff, in the same order); and one warning per sparse group.
// Nil results when no group was observed.
func (g *groupedRowTests) finalize(keys []string) ([]*types.TestResult, []rowTestEntry, []*types.ResponseWarning, error) {
	if g == nil || len(keys) == 0 {
		return nil, nil, nil, nil
	}
	out := make([]*types.TestResult, g.nTests*len(keys))
	entries := make([]rowTestEntry, 0, len(g.plans)*len(keys))
	var warns []*types.ResponseWarning
	for j, pl := range g.plans {
		for k, key := range keys {
			set, ok := g.buckets[key]
			if !ok || j >= len(set) {
				return nil, nil, nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
					"grouped tests: a Data row's bucket folded no test instance")
			}
			e := set[j]
			entries = append(entries, e)
			res, err := e.test.Finalize()
			if err != nil {
				var coded *errors.CodedError
				if !stderrors.As(err, &coded) || !sparseTestCodes[coded.Code] {
					return nil, nil, nil, err
				}
				res, warn := sparseGroupResult(e, coded)
				out[pl.idx*len(keys)+k] = res
				warns = append(warns, warn)
				continue
			}
			if res != nil {
				if res.Label == "" {
					res.Label = e.label
				}
				res.GroupKey = e.groupKey
			}
			out[pl.idx*len(keys)+k] = res
		}
	}
	return out, entries, warns, nil
}

// sparseGroupResult is the result and warning of one group whose data
// the test refused with a sparse code: null statistic and p_value
// (never reject_null), the test's alpha, the refusal's message as a
// per-test warning; the Response warning carries the code, the
// refusal's own Details plus {test, type, index, group_key}.
func sparseGroupResult(e rowTestEntry, coded *errors.CodedError) (*types.TestResult, *types.ResponseWarning) {
	alpha := e.spec.Alpha
	if alpha == 0 {
		alpha = 0.05
	}
	msg := fmt.Sprintf("%s in group %v: %s; its statistic and p_value are null and the other groups proceed",
		e.label, []any(e.groupKey), coded.Message)
	details := make(map[string]any, len(coded.Details)+4)
	for k, v := range coded.Details {
		details[k] = v
	}
	details["test"] = e.label
	details["type"] = string(e.spec.Type)
	details["index"] = e.idx
	details["group_key"] = e.groupKey
	res := &types.TestResult{
		Label:     e.label,
		Type:      e.spec.Type,
		GroupKey:  e.groupKey,
		Statistic: math.NaN(),
		PValue:    math.NaN(),
		Alpha:     alpha,
		Warnings:  []string{string(coded.Code) + ": " + coded.Message},
	}
	return res, &types.ResponseWarning{Code: string(coded.Code), Message: msg, Details: details}
}

// bucketFolds is the per-bucket state a buffered grouped exit folds
// each bucket's records into: the grouped matrices and the grouped
// tier-1 tests. Either may be nil.
type bucketFolds struct {
	matrices *GroupedMatrices
	tests    *groupedRowTests
}

// foldRecords folds bucket key's records (tuple: its GroupKey) into
// every per-bucket state.
func (f bucketFolds) foldRecords(p *Processor, key string, tuple types.AxisKey, records []*Record) error {
	if err := f.matrices.foldRecords(key, records); err != nil {
		return err
	}
	return f.tests.fold(p, key, tuple, records)
}
