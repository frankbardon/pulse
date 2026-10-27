//go:build heavy

package pulse_test

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/examples"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestLimitsDefaultsNeverTripGoldens is the FR-29 gate: every embedded
// example — every category, including the Compose-, chain- and
// Facet-host overlays and facet/, which TestExamples_RunEndToEnd does
// not drive — runs clean on an instance with the built-in limit
// defaults. Predict reports no LimitFindings for any Request (a single
// Request, every Compose slot, a chain's stage 0), and no run trips a
// limit at runtime. Lowering a default is breaking; this gate is what
// catches it.
func TestLimitsDefaultsNeverTripGoldens(t *testing.T) {
	tmp := t.TempDir()
	fs := afero.NewOsFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	want := pulse.Limits{
		RequestTimeout:     pulse.DefaultRequestTimeout,
		MaxGroups:          pulse.DefaultMaxGroups,
		MaxCrosstabCells:   pulse.DefaultMaxCrosstabCells,
		MaxEstimatedMemory: pulse.DefaultMaxEstimatedMemory,
		MaxMatrixDim:       pulse.DefaultMaxMatrixDim,
		MaxComposeSlots:    pulse.DefaultMaxComposeSlots,
		MaxChainStages:     pulse.DefaultMaxChainStages,
		MaxJoinBuildRows:   pulse.DefaultMaxJoinBuildRows,
		MaxDerivedColumns:  pulse.DefaultMaxDerivedColumns,
	}
	if got := p.Limits(); got != want {
		t.Fatalf("effective limits %+v, want the built-in defaults %+v", got, want)
	}
	buildExampleFixtures(t, p, fs, tmp)
	ctx := context.Background()

	noFindings := func(t *testing.T, where string, req *types.Request) {
		t.Helper()
		res, err := p.Predict(ctx, req)
		if err != nil {
			t.Fatalf("%s: Predict: %v", where, err)
		}
		if len(res.LimitFindings) != 0 {
			t.Fatalf("%s: predict reports limit findings under the defaults: %+v", where, res.LimitFindings)
		}
	}

	all := examples.All()
	if len(all) != examples.Count() {
		t.Fatalf("examples.All() = %d, Count() = %d", len(all), examples.Count())
	}
	hosts := map[exampleHost]int{}
	for _, ex := range all {
		t.Run(ex.Category+"/"+ex.Name, func(t *testing.T) {
			var tree any
			if err := json.Unmarshal(ex.Body, &tree); err != nil {
				t.Fatalf("parse body: %v", err)
			}
			rebaseDataDirs(tree, tmp)
			rebaseModelFiles(tree, tmp)
			pops := map[string]bool{}
			rebasePopulationCohorts(tree, tmp, pops)
			for name := range pops {
				seedPopulationCohort(t, fs, tmp, name)
			}
			body, err := json.Marshal(tree)
			if err != nil {
				t.Fatal(err)
			}
			var keys map[string]json.RawMessage
			if err := json.Unmarshal(body, &keys); err != nil {
				t.Fatal(err)
			}
			host := classifyExample(keys)
			hosts[host]++

			var resp any
			switch host {
			case hostCompose:
				var req types.ComposedRequest
				if err := json.Unmarshal(body, &req); err != nil {
					t.Fatalf("parse compose: %v", err)
				}
				for i, slot := range req.Requests {
					noFindings(t, "slot "+strconv.Itoa(i), slot)
				}
				resp, err = p.Compose(ctx, &req)
			case hostChain:
				var req types.ChainRequest
				if err := json.Unmarshal(body, &req); err != nil {
					t.Fatalf("parse chain: %v", err)
				}
				stage0 := *req.Stages[0].Request
				stage0.Cohort = req.Cohort
				noFindings(t, "stage 0", &stage0)
				resp, err = p.ProcessChain(ctx, &req)
			case hostFacet:
				var req types.FacetRequest
				if err := json.Unmarshal(body, &req); err != nil {
					t.Fatalf("parse facet: %v", err)
				}
				resp, err = p.FacetSchema(ctx, &req)
			default:
				var req pulse.Request
				if err := json.Unmarshal(body, &req); err != nil {
					t.Fatalf("parse request: %v", err)
				}
				noFindings(t, "request", &req)
				resp, err = p.Process(ctx, &req)
			}
			if trip := limitTripIn(t, err, resp); trip != "" {
				t.Fatalf("%s host tripped a limit under the defaults: %s", host, trip)
			}
			if err != nil {
				t.Fatalf("%s host: %v", host, err)
			}
		})
	}
	// No example's file write escaped tmp: a relative `save` the rebase
	// missed would have landed in the working directory (the repo).
	if _, err := os.Stat(examplePopulationSave); err == nil {
		t.Errorf("an example wrote %s into the working directory; rebaseModelFiles missed a save path", examplePopulationSave)
	}
	// Every host the catalog carries must have been driven — a host
	// that silently stopped matching would otherwise drop out.
	for _, h := range []exampleHost{hostRequest, hostCompose, hostChain, hostFacet} {
		if hosts[h] == 0 {
			t.Errorf("no example ran through the %s host", h)
		}
	}
}
