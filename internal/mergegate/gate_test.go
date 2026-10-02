package mergegate

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// fakeExt registers one extension per category with fixed facts.
type fakeExt struct{}

func (fakeExt) Aggregator(n string) (bool, bool) {
	switch n {
	case "AGG_X_MERGE":
		return true, true
	case "AGG_X_SOLO":
		return false, true
	}
	return false, false
}
func (fakeExt) Grouper(n string) (bool, bool) {
	return n == "GROUP_X_MERGE", strings.HasPrefix(n, "GROUP_X_")
}
func (fakeExt) Filterer(n string) (bool, bool) {
	return n == "FILTER_X_ROW", strings.HasPrefix(n, "FILTER_X_")
}
func (fakeExt) Attribute(n string) (bool, bool) {
	return n == "ATTR_X_ROW", strings.HasPrefix(n, "ATTR_X_")
}

func decSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "n", Type: encoding.FieldTypeU32},
		{Name: "amt", Type: encoding.FieldTypeDecimal128, Precision: 18, Scale: 2},
	}}
}

func TestMergeRefusal(t *testing.T) {
	sum := func(f string) []*types.Aggregation {
		return []*types.Aggregation{{Type: types.AGG_SUM, Field: f}}
	}
	cases := []struct {
		name   string
		req    *types.Request
		ext    Extensions
		reason string // "" = mergeable
	}{
		{"nil request", nil, nil, "nil"},
		{"mergeable", &types.Request{Aggregations: sum("n"), Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "n"}}}, nil, ""},
		{"no aggregator", &types.Request{}, nil, "no aggregator"},
		{"tests", &types.Request{Aggregations: sum("n"), Tests: []*types.Test{{}}}, nil, "windows, features"},
		{"nil attribute", &types.Request{Aggregations: sum("n"), Attributes: []*types.Attribute{nil}}, nil, "attribute slot is nil"},
		{"row-local built-in attribute", &types.Request{Aggregations: sum("n"), Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA}}}, nil, ""},
		{"row-local extension attribute", &types.Request{Aggregations: sum("n"), Attributes: []*types.Attribute{{Type: "ATTR_X_ROW"}}}, fakeExt{}, ""},
		{"two-pass extension attribute", &types.Request{Aggregations: sum("n"), Attributes: []*types.Attribute{{Type: "ATTR_X_TWO"}}}, fakeExt{}, "attribute ATTR_X_TWO is not row-local"},
		{"extension attribute without registry", &types.Request{Aggregations: sum("n"), Attributes: []*types.Attribute{{Type: "ATTR_X_ROW"}}}, nil, "not row-local"},
		{"nil group", &types.Request{Aggregations: sum("n"), Groups: []*types.Group{nil}}, nil, "group slot is nil"},
		{"mergeable extension grouper", &types.Request{Aggregations: sum("n"), Groups: []*types.Group{{Type: "GROUP_X_MERGE"}}}, fakeExt{}, ""},
		{"non-mergeable extension grouper", &types.Request{Aggregations: sum("n"), Groups: []*types.Group{{Type: "GROUP_X_SOLO"}}}, fakeExt{}, "grouper GROUP_X_SOLO is not mergeable"},
		{"nil filterer", &types.Request{Aggregations: sum("n"), Filterers: []*types.Filterer{nil}}, nil, "filterer slot is nil"},
		{"built-in filterer", &types.Request{Aggregations: sum("n"), Filterers: []*types.Filterer{{Type: types.FILTER_INCLUDE}}}, nil, ""},
		{"extension filterer", &types.Request{Aggregations: sum("n"), Filterers: []*types.Filterer{{Type: "FILTER_X_ROW"}}}, fakeExt{}, ""},
		{"non-streamable extension filterer", &types.Request{Aggregations: sum("n"), Filterers: []*types.Filterer{{Type: "FILTER_X_BUF"}}}, fakeExt{}, "filterer FILTER_X_BUF is not streamable"},
		{"nil aggregation", &types.Request{Aggregations: []*types.Aggregation{nil}}, nil, "aggregation slot is nil"},
		{"built-in decimal", &types.Request{Aggregations: sum("amt")}, nil, `decimal128 field "amt"`},
		{"extension decimal", &types.Request{Aggregations: []*types.Aggregation{{Type: "AGG_X_MERGE", Field: "amt"}}}, fakeExt{}, ""},
		{"non-mergeable extension aggregator", &types.Request{Aggregations: []*types.Aggregation{{Type: "AGG_X_SOLO", Field: "n"}}}, fakeExt{}, "aggregator AGG_X_SOLO is not mergeable"},
		{"unknown aggregator", &types.Request{Aggregations: []*types.Aggregation{{Type: "AGG_X_MERGE", Field: "n"}}}, None{}, "aggregator AGG_X_MERGE is not mergeable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MergeRefusal(tc.req, decSchema(), tc.ext)
			if tc.reason == "" {
				if got != "" {
					t.Fatalf("refused: %s", got)
				}
				return
			}
			if !strings.Contains(got, tc.reason) {
				t.Fatalf("reason = %q, want it to contain %q", got, tc.reason)
			}
		})
	}
	// A nil schema skips only the decimal check.
	if got := MergeRefusal(&types.Request{Aggregations: sum("amt")}, nil, nil); got != "" {
		t.Fatalf("nil schema refused: %s", got)
	}
}

func TestChainRefusal(t *testing.T) {
	ok := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n"}}}
	if err := ChainRefusal(ok, decSchema(), nil, 0, "s0"); err != nil {
		t.Fatalf("refused a mergeable stage: %v", err)
	}
	for _, tc := range []struct {
		name   string
		req    *types.Request
		reason string
	}{
		{"merge refusal", &types.Request{}, "no aggregator"},
		{"non-scalar", &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n"}, {Type: types.AGG_MODE, Field: "n"}}}, "aggregator AGG_MODE emits a non-scalar value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ChainRefusal(tc.req, decSchema(), nil, 3, "s3")
			ce, isCoded := err.(*errors.CodedError)
			if !isCoded || ce.Code != errors.PULSE_CHAIN_NOT_MERGEABLE {
				t.Fatalf("err = %v", err)
			}
			if !strings.HasPrefix(ce.Message, "chain stage is not mergeable: ") || !strings.Contains(ce.Message, tc.reason) {
				t.Fatalf("message = %q, want %q", ce.Message, tc.reason)
			}
			if ce.Details["stage_index"] != 3 || ce.Details["stage_name"] != "s3" || len(ce.Details) != 2 {
				t.Fatalf("details = %v", ce.Details)
			}
		})
	}
}

func TestStageJoinRefusal(t *testing.T) {
	joined := &types.Request{Joins: []*types.JoinSpec{{}, {}}}
	if StageJoinRefusal(joined, 0, "s0") != nil {
		t.Fatal("stage 0 may join")
	}
	if StageJoinRefusal(&types.Request{}, 2, "s2") != nil || StageJoinRefusal(nil, 2, "s2") != nil {
		t.Fatal("a join-free later stage was refused")
	}
	ce, isCoded := StageJoinRefusal(joined, 2, "s2").(*errors.CodedError)
	if !isCoded || ce.Code != errors.PULSE_CHAIN_STAGE_JOIN {
		t.Fatalf("err = %v", ce)
	}
	if ce.Details["stage"] != 2 || ce.Details["stage_name"] != "s2" || ce.Details["count"] != 2 {
		t.Fatalf("details = %v", ce.Details)
	}
}

func TestEmitsScalar(t *testing.T) {
	for _, a := range types.AllAggregationTypes() {
		want := a != types.AGG_FREQUENCY && a != types.AGG_MODE
		if EmitsScalar(a) != want {
			t.Errorf("EmitsScalar(%s) = %v", a, !want)
		}
	}
}
