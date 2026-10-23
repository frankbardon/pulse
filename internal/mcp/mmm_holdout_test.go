package mcp

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/mmmfixture"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestHoldout_MMMMCPComposeSweep (transformed-predictors E3-S3):
// pulse_compose runs the canonical media-mix decay sweep (and its
// FEAT_TRAIN_TEST_SPLIT variant) with no materialize step — a
// populated ranking equal to the library's for the same batch with the
// fixture's true decay first, every slot without data rows (the
// request's own `return` wins over the MCP standard default) and with
// its own holdout rmse.
func TestHoldout_MMMMCPComposeSweep(t *testing.T) {
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.NewCohortBuilder(context.Background(), mmmfixture.CohortFile, mmmfixture.Schema(), pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range mmmfixture.Rows() {
		if err := b.Append(pulse.CohortRow(r)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string][]byte{"week": mmmfixture.DecaySweep, "split": mmmfixture.DecaySweepSplit} {
		t.Run(name, func(t *testing.T) {
			out := invokeTool(t, p, Config{}, "pulse_compose", string(raw))
			var resp struct {
				Ranking   []types.RankEntry            `json:"ranking"`
				Responses []map[string]json.RawMessage `json:"responses"`
			}
			if err := json.Unmarshal(out, &resp); err != nil || len(resp.Responses) != len(mmmfixture.Decays) {
				t.Fatalf("want %d sweep slots: %v\n%s", len(mmmfixture.Decays), err, out)
			}
			var req pulse.ComposedRequest
			if err := json.Unmarshal(raw, &req); err != nil {
				t.Fatal(err)
			}
			lib, err := p.Compose(context.Background(), &req)
			if err != nil {
				t.Fatalf("library Compose: %v", err)
			}
			if len(lib.Ranking) != len(mmmfixture.Decays) || !slices.Equal(resp.Ranking, lib.Ranking) {
				t.Fatalf("MCP ranking %+v, library %+v", resp.Ranking, lib.Ranking)
			}
			if resp.Ranking[0].Label != "decay_0.5" {
				t.Fatalf("best slot %q, want decay_0.5 (the fixture's true decay)", resp.Ranking[0].Label)
			}
			seen := map[float64]bool{}
			for i, s := range resp.Responses {
				if _, ok := s["data"]; ok {
					t.Fatalf("slot %d carries data; its return excludes it", i)
				}
				var regs []types.RegressionResult
				if err := json.Unmarshal(s["regressions"], &regs); err != nil || len(regs) != 1 || regs[0].Holdout == nil {
					t.Fatalf("slot %d regressions %s: %v", i, s["regressions"], err)
				}
				if rmse := regs[0].Holdout.RMSE; seen[rmse] || rmse != lib.Responses[i].Regressions[0].Holdout.RMSE {
					t.Fatalf("slot %d holdout rmse %v: duplicated, or not the library's %v", i, rmse, lib.Responses[i].Regressions[0].Holdout.RMSE)
				}
				seen[regs[0].Holdout.RMSE] = true
			}
		})
	}
}
