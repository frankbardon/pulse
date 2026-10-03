package pulse

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/spf13/afero"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

const guidanceCohort = "guidance.pulse"

// guidanceExemplarRequest exercises every operator that declares a
// Purpose today (AGG_AVERAGE, TEST_ANOVA_F, TEST_PEARSON_R) on a
// four-region cohort with two numeric measures.
func guidanceExemplarRequest() *types.Request {
	return &types.Request{
		Cohort:       &types.Cohort{Filename: guidanceCohort},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "score", Label: "avg_score"}},
		Tests: []*types.Test{
			{Type: types.TEST_ANOVA_F, Field: "score", SplitBy: "region"},
			{Type: types.TEST_PEARSON_R, Field: "score", Field2: "spend"},
		},
	}
}

// TestManifestGuidanceBudget_DefaultResults is the runtime half of
// TestManifestGuidanceBudget (internal/descriptor): a default Response
// and PredictResult for a request using the purpose-declaring operators
// carry no declared guidance prose. Guidance is served on demand only.
func TestManifestGuidanceBudget_DefaultResults(t *testing.T) {
	fsys := afero.NewMemMapFs()
	regions := []string{"north", "south", "east", "west"}
	var rows [][]string
	for i := 0; i < 40; i++ {
		score := 10 + (i*7)%53 + 3*(i%4)
		rows = append(rows, []string{regions[i%4], strconv.Itoa(score), strconv.Itoa(2*score + i%5)})
	}
	createTestPulseFile(t, fsys, guidanceCohort, []string{"region", "score", "spend"}, rows)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Every exemplar operator must actually declare a Purpose, or the
	// request no longer exercises guidance-bearing operators.
	for _, name := range []string{"AGG_AVERAGE", "TEST_ANOVA_F", "TEST_PEARSON_R"} {
		if _, ok := descx.PurposeOf(name); !ok {
			t.Fatalf("%s declares no Purpose; pick a purpose-declaring exemplar", name)
		}
	}
	if len(descx.GuidanceProse()) == 0 {
		t.Fatal("GuidanceProse is empty: the ban would check nothing")
	}

	ctx := context.Background()
	resp, err := p.Process(ctx, guidanceExemplarRequest())
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Tests) != 2 {
		t.Fatalf("Process returned %d test results, want 2", len(resp.Tests))
	}
	pred, err := p.Predict(ctx, guidanceExemplarRequest())
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	for name, v := range map[string]any{"Response": resp, "PredictResult": pred} {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		for _, leak := range descx.LeakedGuidanceProse(raw) {
			t.Errorf("default %s carries guidance prose from %s: %q", name, leak.Source, leak.Text)
		}
	}
}
