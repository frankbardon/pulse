package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/types"
)

// TestFacetOverlay_PopulationResolvesAsHostCohort (#789): a FACET-host
// overlay's ref.population.cohort resolves exactly as the host cohort
// does — under the host's `data_dir`, on the instance filesystem
// (whose root Options.DataDir / PULSE_DATA_DIR sets: a BasePathFs).
// An absolute population path stands alone. Before, the population was
// opened as the bare name, from the filesystem root, so a host
// addressed through `data_dir` failed to find a population sitting
// beside it.
func TestFacetOverlay_PopulationResolvesAsHostCohort(t *testing.T) {
	t.Parallel()
	fixture := facetOverlayFixture(t)
	mem := afero.NewMemMapFs()
	for _, p := range []string{"/data/sub/host.pulse", "/data/sub/pop.pulse"} {
		if err := afero.WriteFile(mem, p, fixture, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	req := func(cohort *types.Cohort, pop string) *types.FacetRequest {
		return &types.FacetRequest{
			Cohort: cohort,
			Fields: []string{"category"},
			Overlays: []types.OverlaySpec{{
				Name: "chisq", Kind: types.OverlayKindChiSqVsPop, Scope: types.OverlayScopeGroup,
				Ref:    types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: pop}},
				Params: json.RawMessage(`{"field":"category"}`),
			}},
		}
	}
	ctx := context.Background()
	t.Run("request_data_dir", func(t *testing.T) {
		cfg, err := fs.New(fs.WithFs(mem))
		if err != nil {
			t.Fatal(err)
		}
		res, err := New(cfg).FacetSchema(ctx, req(&types.Cohort{Filename: "host.pulse", DataDir: "/data/sub"}, "pop.pulse"))
		if err != nil {
			t.Fatalf("FacetSchema: %v", err)
		}
		if len(res.Overlays) != 1 || res.Overlays[0].Kind != types.OverlayKindChiSqVsPop {
			t.Fatalf("overlays = %+v, want the one chi-square layer", res.Overlays)
		}
		// The population is read from data_dir, never the root: a
		// population present only at the root is not found.
		if err := afero.WriteFile(mem, "/root_only.pulse", fixture, 0o644); err != nil {
			t.Fatal(err)
		}
		_, err = New(cfg).FacetSchema(ctx, req(&types.Cohort{Filename: "host.pulse", DataDir: "/data/sub"}, "root_only.pulse"))
		if err == nil {
			t.Fatal("a population outside data_dir was found at the filesystem root")
		}
		// An absolute population path stands alone, never joined to
		// data_dir.
		if _, err := New(cfg).FacetSchema(ctx, req(&types.Cohort{Filename: "host.pulse", DataDir: "/data/sub"}, "/root_only.pulse")); err != nil {
			t.Fatalf("absolute population path under a data_dir host: %v", err)
		}
	})
	t.Run("instance_data_dir", func(t *testing.T) {
		cfg, err := fs.New(fs.WithFs(afero.NewBasePathFs(mem, "/data")))
		if err != nil {
			t.Fatal(err)
		}
		res, err := New(cfg).FacetSchema(ctx, req(&types.Cohort{Filename: "host.pulse", DataDir: "sub"}, "pop.pulse"))
		if err != nil {
			t.Fatalf("FacetSchema: %v", err)
		}
		if len(res.Overlays) != 1 {
			t.Fatalf("overlays = %d, want 1", len(res.Overlays))
		}
	})
}
