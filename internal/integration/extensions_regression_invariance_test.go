package integration_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/testutil/invariance"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// extensions_regression_invariance_test.go is the custom-regression
// family on the shared worker-invariance harness: a regression never
// merges, so every worker count folds the admitted rows in record order
// and every cell — parallel knobs included — runs on the serial
// buffered arm and answers the serial whole response (LayoutFree),
// bitwise:
//
//   - fits: REG_PARITY_OLS (streaming) and its buffered twin, weighted
//     and not, a multiplicity block on one;
//   - resample: a bootstrap at rng_seed 0 (the data-derived seed) and a
//     jackknife — ten replicates: the bits under test are the seed's and
//     the row order's, whatever the count — also rerun on the reference
//     knobs (run to run);
//
// over a shard archive (a 1-row and an empty shard) under ShardWorkers
// 1/2/3/8 and DecodeWorkers 7 ≡ its single-file twin, and (heavy,
// extensions_regression_invariance_heavy_test.go) DecodeWorkers 3 and 8
// over a cohort past the parallel-decode threshold ≡ serial.

// regInvBounds splits the twin's 120 rows into 1, 40, 0 and 79.
var regInvBounds = []int{0, 1, 41, 41, 120}

// newRegInvFixture writes the twin and the archive (hermetic); heavy
// adds the decode-threshold cohort on disk.
func newRegInvFixture(t *testing.T, heavy bool) map[invariance.Layout]invCohort {
	t.Helper()
	mem := afero.NewMemMapFs()
	s := paritySchema(t)
	writeParityCohort(t, mem, "twin.pulse", s, 0, regInvBounds[len(regInvBounds)-1])
	var shards []string
	for i := 0; i+1 < len(regInvBounds); i++ {
		path := fmt.Sprintf("s%d.pulse", i)
		writeParityCohort(t, mem, path, s, regInvBounds[i], regInvBounds[i+1])
		shards = append(shards, path)
	}
	w, err := pulse.New(pulse.Options{FS: mem})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.CreateShardArchive(context.Background(), "archive.pulse", shards); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	out := map[invariance.Layout]invCohort{invariance.File: {mem, "twin.pulse"}, invariance.Archive: {mem, "archive.pulse"}}
	if heavy {
		dir := t.TempDir()
		writeParityCohort(t, afero.NewOsFs(), filepath.Join(dir, "large.pulse"), s, 0, parityLargeRows)
		out[invariance.BigFile] = invCohort{afero.NewBasePathFs(afero.NewOsFs(), dir), "large.pulse"}
	}
	return out
}

// regInvShape is one request of the family.
type regInvShape struct {
	name     string
	ext      pulse.Extensions
	specs    func() []*types.RegressionSpec
	resample bool
}

func regInvShapes() []regInvShape {
	prob := types.SlotWeightOf(types.WeightSpec{Field: "qty", Kind: types.WeightKindProbability})
	return []regInvShape{
		{"fits", pulse.Extensions{Regressions: regressionParityRegistrations()}, func() []*types.RegressionSpec {
			return []*types.RegressionSpec{
				{Type: regParityOLS, Name: "s", Target: "score", Predictors: []string{"qty", "day"}},
				{Type: regParityOLS, Name: "sw", Target: "score", Predictors: []string{"day"}, Weight: prob},
				{Type: regParityOLSBuf, Name: "b", Target: "score", Predictors: []string{"qty", "day"}},
				{Type: regParityOLSBuf, Name: "bw", Target: "score", Predictors: []string{"day"}, Weight: prob,
					Multiplicity: &types.RegressionMultiplicity{Method: types.MultiplicityMethodHolm}},
			}
		}, false},
		{"resample", pulse.Extensions{Regressions: modifierRegistrations()}, func() []*types.RegressionSpec {
			return []*types.RegressionSpec{
				{Type: regParityOLS, Name: "s", Target: "score", Predictors: []string{"qty", "day"}, Resample: "bootstrap", BootstrapIters: 10},
				{Type: regParityOLSBuf, Name: "b", Target: "score", Predictors: []string{"qty"}, Resample: "jackknife", JackknifeGroups: 10},
			}
		}, true},
	}
}

// regInvGroups are a shape's groups, every cell on the serial arm: the
// archive group in the fast tier (the resample shape reruns the
// reference knobs), the serial decode group in the heavy one.
func regInvGroups(s regInvShape, heavy bool) []invariance.Group {
	serial := func(g invariance.Group) invariance.Group {
		for i := range g.Cells {
			g.Cells[i].Arm = observe.ArmBuffered
		}
		return g
	}
	if heavy {
		return []invariance.Group{invSerialDecodeGroup("decode", invariance.BigFile, observe.ArmBuffered)}
	}
	archive := invariance.ArchiveGroup()
	if s.resample {
		archive.Cells = append(archive.Cells, invariance.Cell{Knobs: archive.Ref.Knobs})
	}
	return []invariance.Group{serial(archive)}
}

// regInvFamily is shape s over groups.
func regInvFamily(cohorts map[invariance.Layout]invCohort, s regInvShape, groups []invariance.Group) invariance.Family[*types.Response] {
	return invariance.Family[*types.Response]{
		Name:   s.name,
		Groups: groups,
		Run: func(t *testing.T, k invariance.Knobs) invariance.Result[*types.Response] {
			co, ok := cohorts[k.Layout]
			if !ok {
				t.Fatalf("layout %s not built in this tier", k.Layout)
			}
			p, arm := invEngine(t, pulse.Options{Extensions: s.ext}, co, k)
			resp, err := p.Process(context.Background(), &types.Request{Cohort: &types.Cohort{Filename: co.path}, Regressions: s.specs()})
			if err != nil {
				t.Fatalf("%s: Process: %v", k, err)
			}
			return invariance.Result[*types.Response]{Answer: invariance.LayoutFree(resp), Arm: arm()}
		},
		Check: func(t *testing.T, _ invariance.Knobs, resp *types.Response) {
			if len(resp.Regressions) != len(s.specs()) {
				t.Fatalf("%d regressions, want %d", len(resp.Regressions), len(s.specs()))
			}
			for _, r := range resp.Regressions {
				if r == nil || r.Error != nil || r.NObs == 0 || (s.resample && r.Resample == "") {
					t.Fatalf("regression failed: %+v", r)
				}
			}
		},
	}
}

// TestExtensionRegressionInvariance_Archive is the fast-tier half of the
// custom-regression family; TestExtensionRegressionInvariance_Heavy adds
// the decode group.
func TestExtensionRegressionInvariance_Archive(t *testing.T) {
	t.Parallel()

	cohorts := newRegInvFixture(t, false)
	for _, s := range regInvShapes() {
		invariance.Run(t, regInvFamily(cohorts, s, regInvGroups(s, false)))
	}
}
