package pulse

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// compose_adstock_lineage_test.go pins the adstock lineage record (G9)
// across a sweep: a REG_MMM_HILL fit bound from the sweep's winner
// (`from_sweep`) records the winning decay its window read, and a slot's
// REG_MMM_HILL whose window decay is sweep_axis("best", "tv") records the
// number sweep_axis() returned — each written as the model file's
// adstock feature and bound back, bit for bit, when the file loads; on
// Compose and ComposeParallel alike.

const (
	bestHillPath = "models/best_hill.pulse-model.json"
	slotHillPath = "models/slot_hill.pulse-model.json"
)

// hillAdstockWindow is x's geometric adstock into x_ad at decay (a JSON
// value: a number or an expression string).
func hillAdstockWindow(decay string) *types.Window {
	return &types.Window{Type: types.WIN_DISTRIBUTED_LAG, Field: "x", Label: "x_ad", OrderBy: []types.OrderKey{{Field: "week"}},
		Params: json.RawMessage(`{"kernel": "geometric", "max_lag": 3, "decay": ` + decay + `}`)}
}

// hillLineageBatch is a ranked zip sweep of REG_MMM_HILL "m" of y on
// x_ad over weeks [0, hi], its window decay params.tv (the axis tv), the fit "best"
// bound from its winner and saved, and a slot refitting the Hill model
// over every week with its decay read through sweep_axis("best", "tv"),
// saved too.
func hillLineageBatch(t *testing.T) *ComposedRequest {
	t.Helper()
	body := `{"cohort":{"filename":"` + fitsFacadeCohort + `"},"params":{"tv":{"$var":"tv"}},` +
		`"filterers":[{"type":"FILTER_RANGE","field":"week","values":["0","{{hi}}"]}],` +
		`"windows":[{"type":"WIN_DISTRIBUTED_LAG","field":"x","label":"x_ad","order_by":[{"field":"week"}],"params":{"kernel":"geometric","max_lag":3,"decay":"params.tv"}}],` +
		`"regressions":[{"type":"REG_MMM_HILL","name":"m","target":"y","predictors":["x_ad"],"params":{"channels":["x_ad"]}}]}`
	best := sweepBound("best", "m")
	best.Save = bestHillPath
	c := sweepBindBatch(t, []types.FitSpec{best}, json.RawMessage(body), func(s *types.SweepSpec) {
		s.Mode = types.SweepModeZip
		s.Axes = append(s.Axes, types.SweepAxis{Name: "tv", Values: []any{json.Number("0.4"), json.Number("0.7")}})
		s.Label = "hi_{{hi}}_tv_{{tv}}"
	})
	c.Requests = []*Request{{
		Label:   "refit",
		Cohort:  &types.Cohort{Filename: fitsFacadeCohort},
		Windows: []*types.Window{hillAdstockWindow(`"sweep_axis(\"best\", \"tv\")"`)},
		Regressions: []*types.RegressionSpec{{Type: types.REG_MMM_HILL, Name: "refit", Target: "y", Predictors: []string{"x_ad"},
			Params: json.RawMessage(`{"channels": ["x_ad"]}`), Save: slotHillPath}},
	}}
	return c
}

// savedAdstock decodes the model file at path, requires feature adstock
// and returns the bound record of x_ad.
func savedAdstock(t *testing.T, fsys afero.Fs, path string) model.Adstock {
	t.Helper()
	data, err := afero.ReadFile(fsys, path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := model.DecodeFile(path, data)
	if err != nil {
		t.Fatalf("%v\n%s", err, data)
	}
	if !slices.Contains(f.Features, model.FeatureAdstock) {
		t.Fatalf("%s lists features %v, want adstock:\n%s", path, f.Features, data)
	}
	b, err := f.Bind("loaded")
	if err != nil {
		t.Fatal(err)
	}
	a, ok := b.Adstock("x_ad")
	if !ok {
		t.Fatalf("%s binds no x_ad record:\n%s", path, data)
	}
	return a
}

// TestAdstockLineage_SweepBoundDecay: both saved Hill models — the
// sweep-bound fit and the slot whose decay reads sweep_axis() — carry
// x_ad's record at exactly the winning tv (geometric, max_lag 3,
// normalized), and load it back bit for bit; Compose and
// ComposeParallel agree.
func TestAdstockLineage_SweepBoundDecay(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	for name, run := range map[string]func(p *Pulse, c *ComposedRequest) (*ComposedResponse, error){
		"Compose": func(p *Pulse, c *ComposedRequest) (*ComposedResponse, error) { return p.Compose(ctx, c) },
		"ComposeParallel": func(p *Pulse, c *ComposedRequest) (*ComposedResponse, error) {
			return p.ComposeParallel(ctx, c, ComposeOptions{MaxWorkers: 2})
		},
	} {
		fsys := fitsFacadeFS(t)
		p, err := New(Options{FS: fsys})
		if err != nil {
			t.Fatal(err)
		}
		out, err := run(p, hillLineageBatch(t))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(out.Ranking) != 2 {
			t.Fatalf("%s: ranking %+v", name, out.Ranking)
		}
		tv, err := out.Ranking[0].Axes["tv"].(json.Number).Float64()
		if err != nil {
			t.Fatalf("%s: winning axes %v", name, out.Ranking[0].Axes)
		}
		for _, path := range []string{bestHillPath, slotHillPath} {
			a := savedAdstock(t, fsys, path)
			if a.Kernel != "geometric" || a.Decay == nil || math.Float64bits(*a.Decay) != math.Float64bits(tv) ||
				a.MaxLag != 3 || !a.Normalize || a.Shape != nil || a.Scale != nil {
				t.Errorf("%s %s: record %+v (decay %v), want geometric decay %v max_lag 3 normalized", name, path, a, a.Decay, tv)
			}
		}
	}
}
