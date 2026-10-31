package integration_test

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// group_include_refusal_test.go pins E1-S7: a non-empty `include` on a
// `groups` slot whose built-in grouper keeps no inclusion list
// (GROUP_RANGE, GROUP_ROUNDED, GROUP_QUANTILE, GROUP_NUMERIC_RANGES,
// GROUP_KMEANS) used to be dropped silently — every bucket came back. It
// is refused PROCESSING_CONFIG, identically on Predict, Process and
// ProcessStream. A crosstab axis keeps its include (it orders the axis).

func TestGroupInclude_UnsupportedGrouperRefused_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	fs, cohort := zoneCohort(t)
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	req := func() *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: cohort},
			Groups:       []*types.Group{{Type: types.GROUP_RANGE, Field: "n", Interval: 10, Include: []string{"0-10"}}},
			Aggregations: countAgg()}
	}
	_, err := p.Process(ctx, req())
	ce := requireCode(t, err, errors.PROCESSING_CONFIG)
	if ce.Details["path"] != "groups[0].include" || ce.Details["operator"] != string(types.GROUP_RANGE) {
		t.Fatalf("details = %v, want path groups[0].include, operator GROUP_RANGE", ce.Details)
	}
	sameEntry(t, predictEnvelope(t, p, fs, cohort, req()), err)
	_, serr := p.ProcessStream(ctx, req())
	if sce := requireCode(t, serr, errors.PROCESSING_CONFIG); sce.Message != ce.Message {
		t.Fatalf("stream %q, process %q", sce.Message, ce.Message)
	}

	// Without include the same slot runs on both sides.
	ok := req()
	ok.Groups[0].Include = nil
	if _, err := p.Process(ctx, ok); err != nil {
		t.Fatalf("include-free GROUP_RANGE refused: %v", err)
	}
	if env := predictEnvelope(t, p, fs, cohort, ok); len(env.Errors) != 0 {
		t.Fatalf("predict refused include-free GROUP_RANGE: %+v", env.Errors)
	}

	// An include-honouring grouper keeps its list.
	cat := req()
	cat.Groups[0] = &types.Group{Type: types.GROUP_CATEGORY, Field: "cat", Include: []string{"a"}}
	if _, err := p.Process(ctx, cat); err != nil {
		t.Fatalf("GROUP_CATEGORY include refused: %v", err)
	}

	// A crosstab axis include orders the axis: still accepted.
	ct := &types.Request{Cohort: &types.Cohort{Filename: cohort},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_RANGE, Field: "n", Interval: 10, Include: []string{"30-40", "0-10"}}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "n"},
		}}
	if _, err := p.Process(ctx, ct); err != nil {
		t.Fatalf("crosstab-axis GROUP_RANGE include refused: %v", err)
	}
	if env := predictEnvelope(t, p, fs, cohort, ct); len(env.Errors) != 0 {
		t.Fatalf("predict refused crosstab-axis include: %+v", env.Errors)
	}
}
