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
//   - SpecIndex maps a flat Response.Tests position back to its
//     Request.Tests slot, for the consumers that pair a result with its
//     spec (the multiplicity fold, explain).
//
// Leaf: stdlib + types + errors + internal/vectors. No-execute.
package groupedtests

import (
	"fmt"

	"github.com/frankbardon/pulse/errors"
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
