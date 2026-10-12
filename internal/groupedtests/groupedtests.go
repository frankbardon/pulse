// Package groupedtests is the one reading of tier-1 tests on a grouped
// request (U46 E3). With `groups` set, every Request.Tests entry runs
// once per COMPOSITE group key — the buckets the grouped Response.Data
// rows are — and Response.Tests is flat: one TestResult per (test x
// group), test-major, the groups in the final Data row order, each
// carrying TestResult.GroupKey. An ungrouped request is unchanged.
//
//   - SplitRefusal is the structural refusal shared by predict
//     (internal/descriptor) and the runtime (internal/processing): a
//     test whose split field is also a grouper key would see a single
//     split level in every group, so it is refused before any record is
//     read, with the same code, message and Details on both sides.
//
//   - SpecIndex maps a flat Response.Tests position back to its
//     Request.Tests slot, for the consumers that pair a result with its
//     spec (the multiplicity fold, explain).
//
//   - PostTestDepth is the post-tests' partition reading (the series-
//     overlay prefix rule), also shared by predict and runtime.
//
// Leaf: stdlib + types + errors + internal/vectors +
// internal/seriespartition. No-execute.
package groupedtests

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/seriespartition"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// ReasonSplitIsGrouper is Details["reason"] of the SplitRefusal error.
const ReasonSplitIsGrouper = "split_is_grouper"

// SplitRefusal returns the refusal of the first tier-1 test (in
// request order) whose split field names a grouper's field: split_by
// (PULSE_TEST_SPLIT_GROUPS_LT_2 — every group holds one split level) or
// a chi-square rows / cols axis (PULSE_TEST_CONTINGENCY_DEGENERATE —
// every group's table has a single row or column). Details {index,
// type, slot, field, groups, reason}. Nil when groups is empty or no
// test splits on a grouper. A vector grouper (GROUP_KMEANS) keys no
// record field and never matches.
func SplitRefusal(tests []*types.Test, groups []*types.Group) *errors.CodedError {
	if len(groups) == 0 || len(tests) == 0 {
		return nil
	}
	grouped := map[string]bool{}
	var fields []string
	for _, g := range groups {
		if g == nil || g.Field == "" || vectors.IsVectorGroup(g.Type) {
			continue
		}
		if !grouped[g.Field] {
			grouped[g.Field] = true
			fields = append(fields, g.Field)
		}
	}
	if len(fields) == 0 {
		return nil
	}
	for i, t := range tests {
		if t == nil {
			continue
		}
		for _, s := range []struct {
			slot, field string
			code        errors.Code
		}{
			{"split_by", t.SplitBy, errors.PULSE_TEST_SPLIT_GROUPS_LT_2},
			{"rows", t.Rows, errors.PULSE_TEST_CONTINGENCY_DEGENERATE},
			{"cols", t.Cols, errors.PULSE_TEST_CONTINGENCY_DEGENERATE},
		} {
			if s.field == "" || !grouped[s.field] {
				continue
			}
			return errors.NewCodedErrorWithDetails(s.code,
				fmt.Sprintf("tests[%d] %s %s %q is also a grouper: a grouped request runs the test once per group, and every group holds a single %s level, so there is nothing to compare; drop %q from groups or split on another field",
					i, t.Type, s.slot, s.field, s.field, s.field),
				map[string]any{
					"index":  i,
					"type":   string(t.Type),
					"slot":   s.slot,
					"field":  s.field,
					"groups": append([]string(nil), fields...),
					"reason": ReasonSplitIsGrouper,
				})
		}
	}
	return nil
}

// SpecIndex is the Request.Tests slot of flat Response.Tests position
// k, given the request's test count and the result count. An ungrouped
// response (one result per slot) maps k to k; a grouped one holds
// specs x groups results, test-major, so k maps to k / groups. -1 when
// k is out of range or the counts do not divide.
func SpecIndex(k, specs, results int) int {
	if specs <= 0 || k < 0 || k >= results || results%specs != 0 {
		return -1
	}
	return k / (results / specs)
}

// ReasonSplitIsPartitionKey is Details["reason"] of the PostTestDepth
// refusal of a post-test splitting on one of its partition key columns.
const ReasonSplitIsPartitionKey = "split_is_partition_key"

// ReasonMixedDepth is Details["reason"] of the PostTestDepth refusal of
// post-tests that resolve to different partition depths.
const ReasonMixedDepth = "mixed_depth"

// PostTestDepth is the one reading of Request.PostTests on a grouped
// request (U46 E3-S2): every post-test runs once per PARTITION of the
// result rows — the rows sharing the leading depth group keys — by the
// series-overlay prefix rule (seriespartition.PrefixDepth over the
// test's Params): absent or null params.partition_by partitions by
// every key but the last, an explicit leading prefix by its length, []
// not at all. Response.PostTests is flat — post-tests x partitions,
// test-major, partitions in first-appearance order of the final Data
// rows — so every post-test of one request must resolve to the SAME
// depth; it is returned (0 for an ungrouped or one-grouper request, or
// no post-test).
//
// Refusals, identical on predict and runtime because both call this,
// in request order:
//   - a partition_by that is not a leading prefix:
//     PULSE_TEST_PARTITION_INVALID, Details {index, type, partition_by,
//     groups, reason[, field]} (reason not_string_list, unknown_field,
//     not_prefix);
//   - two post-tests at different depths: PULSE_TEST_PARTITION_INVALID
//     reason mixed_depth, Details {index, type, partition_by, groups,
//     reason, depths} (index: the first post-test that differs from
//     post_tests[0]; depths: every post-test's depth, request order);
//   - a post-test whose split_by (PULSE_TEST_SPLIT_GROUPS_LT_2) or rows /
//     cols (PULSE_TEST_CONTINGENCY_DEGENERATE) names a partition key
//     column — every partition holds one level — Details {index, type,
//     slot, field, groups, reason: split_is_partition_key}, groups the
//     partition key columns.
func PostTestDepth(postTests []*types.Test, groups []*types.Group) (int, *errors.CodedError) {
	if len(postTests) == 0 {
		return 0, nil
	}
	depths := make([]int, len(postTests))
	first := -1
	for i, t := range postTests {
		if t == nil {
			depths[i] = -1
			continue
		}
		d, r := seriespartition.PrefixDepth(groups, t.Params)
		if r != nil {
			details := map[string]any{
				"index":        i,
				"type":         string(t.Type),
				"partition_by": r.Given,
				"groups":       r.Columns,
				"reason":       r.Reason,
			}
			if r.Field != "" {
				details["field"] = r.Field
			}
			return 0, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_PARTITION_INVALID,
				fmt.Sprintf("post_tests[%d] %s %s (groups: %s); omit it for every key but the last, or pass [] for no partition",
					i, t.Type, r.Why, strings.Join(r.Columns, ", ")),
				details)
		}
		depths[i] = d
		if first < 0 {
			first = i
		}
	}
	if first < 0 {
		return 0, nil
	}
	depth := depths[first]
	cols := seriespartition.KeyColumns(groups)
	for i, d := range depths {
		if d < 0 || d == depth {
			continue
		}
		var given any
		var m map[string]json.RawMessage
		if json.Unmarshal(postTests[i].Params, &m) == nil {
			if raw, ok := m[seriespartition.Param]; ok {
				_ = json.Unmarshal(raw, &given)
			}
		}
		return 0, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_PARTITION_INVALID,
			fmt.Sprintf("post_tests[%d] %s partitions by %d group key(s) and post_tests[%d] by %d: every post-test of one request must partition alike (Response.PostTests is one post-tests x partitions list); give them the same partition_by or run them as separate requests",
				i, postTests[i].Type, d, first, depth),
			map[string]any{
				"index":        i,
				"type":         string(postTests[i].Type),
				"partition_by": given,
				"groups":       append([]string(nil), cols...),
				"reason":       ReasonMixedDepth,
				"depths":       append([]int(nil), depths...),
			})
	}
	if depth == 0 {
		return 0, nil
	}
	keys := cols[:depth]
	partitioned := map[string]bool{}
	for _, c := range keys {
		partitioned[c] = true
	}
	for i, t := range postTests {
		if t == nil {
			continue
		}
		for _, s := range []struct {
			slot, field string
			code        errors.Code
		}{
			{"split_by", t.SplitBy, errors.PULSE_TEST_SPLIT_GROUPS_LT_2},
			{"rows", t.Rows, errors.PULSE_TEST_CONTINGENCY_DEGENERATE},
			{"cols", t.Cols, errors.PULSE_TEST_CONTINGENCY_DEGENERATE},
		} {
			if s.field == "" || !partitioned[s.field] {
				continue
			}
			return 0, errors.NewCodedErrorWithDetails(s.code,
				fmt.Sprintf("post_tests[%d] %s %s %q is a partition key: a grouped request runs each post-test once per partition (%s), and every partition holds a single %s level, so there is nothing to compare; pass params.partition_by [] to compare across it, or put %q last in groups",
					i, t.Type, s.slot, s.field, strings.Join(keys, ", "), s.field, s.field),
				map[string]any{
					"index":  i,
					"type":   string(t.Type),
					"slot":   s.slot,
					"field":  s.field,
					"groups": append([]string(nil), keys...),
					"reason": ReasonSplitIsPartitionKey,
				})
		}
	}
	return depth, nil
}

// WithoutPartition returns t with params.partition_by removed — the spec
// a post-test's own factory decodes (the partition is the engine's,
// read by PostTestDepth). t itself is returned when it carries none.
func WithoutPartition(t *types.Test) *types.Test {
	if t == nil || len(t.Params) == 0 {
		return t
	}
	stripped := seriespartition.WithoutParam(t.Params)
	if len(stripped) == len(t.Params) && string(stripped) == string(t.Params) {
		return t
	}
	out := *t
	out.Params = stripped
	return &out
}
