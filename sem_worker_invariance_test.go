package pulse_test

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/testutil/invariance"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// sem_worker_invariance_test.go is the MAT_SEM family on the shared
// worker-invariance harness: a fit answers the same whole response
// (LayoutFree), bitwise, over a shard archive (230-row shards) under
// ShardWorkers 1/2/3/8 and DecodeWorkers 7 as over its single-file twin:
//
//   - multi-group fits (the per-level co-moments merge through the fixed
//     tree, so a grouped fit takes the shard reducer) on every supported
//     group field type, and the := rows over a grouped invariance fit
//     (they read the merged fit only) — and (heavy,
//     sem_worker_invariance_heavy_test.go) under DecodeWorkers 2/3/7/8
//     over the brand rows repeated past the parallel-decode threshold;
//   - (heavy) seeded and data-derived-seed bootstraps, ungrouped and
//     within each wave, ML and WLSMV: the slot buffers its rows in
//     record order and the request runs serially, so every cell names
//     the serial arm; a rerun on the reference knobs is one more cell
//     (run to run).
//
// What the knobs do not move stays in the operator tests: another seed
// moves the SEs (TestSEM_BootstrapDeterministic), the derived seed's
// rule (TestSEM_BootstrapDerivedSeed), the group field types' fits
// agreeing (TestSEM_MultiGroupFieldTypesAgree), the ordinal bootstrap's
// SE coverage and predict (TestSEM_OrdinalBootstrapDeterministic).

// semInvShape is one fit request of the family.
type semInvShape struct {
	name string
	csv  string
	wave encoding.FieldType
	// mergeable: the fit takes the parallel arms (else every cell runs
	// serially).
	mergeable bool
	req       func(t *testing.T) *types.Request
}

func semInvBoot(seeded bool, params map[string]any) map[string]any {
	out := map[string]any{"resample": "bootstrap", "bootstrap_iters": 12}
	if seeded {
		out["bootstrap_iters"], out["rng_seed"], out["ci_level"] = 25, 7, 0.9
	}
	for k, v := range params {
		out[k] = v
	}
	return out
}

func semInvShapes() []semInvShape {
	cat := encoding.FieldTypeCategoricalU8
	mg := func(wave encoding.FieldType) semInvShape {
		return semInvShape{"multigroup_" + wave.String(), "brand_uneq", wave, true, func(t *testing.T) *types.Request {
			return semGroupRequest(t, "brand_mgmis", nil)
		}}
	}
	single, grouped := map[string]any{"group": nil}, map[string]any{"group_equal": "loadings,intercepts"}
	ml := func(name, model string, seeded bool, params map[string]any) semInvShape {
		return semInvShape{name, "brand", cat, false, func(t *testing.T) *types.Request {
			return semGroupRequest(t, model, semInvBoot(seeded, params))
		}}
	}
	ord := func(name string, params map[string]any) semInvShape {
		return semInvShape{name, "brand", cat, false, func(t *testing.T) *types.Request {
			p := semInvBoot(true, params)
			p["bootstrap_iters"], p["ci_level"] = 12, nil
			return ordinalGroupRequest(t, "brand_ord", brandOrdered, p)
		}}
	}
	return []semInvShape{
		mg(cat), mg(encoding.FieldTypePackedBool), mg(encoding.FieldTypeU8),
		{"defined", "brand", cat, true, func(t *testing.T) *types.Request {
			return semGroupRequest(t, "brand_mgdef", map[string]any{"group_equal": "loadings,intercepts"})
		}},
		ml("bootstrap_single", "brand_def", true, single),
		ml("bootstrap_grouped", "brand_mgdef", true, grouped),
		ml("derived_seed_single", "brand_def", false, single),
		ml("derived_seed_grouped", "brand_mgdef", false, grouped),
		ord("ordinal_bootstrap_single", map[string]any{"group": nil}),
		ord("ordinal_bootstrap_grouped", map[string]any{"group_equal": "thresholds,loadings"}),
	}
}

// semInvSmoke runs in the fast tier.
var semInvSmoke = map[string]bool{"multigroup_categorical_u8": true, "defined": true}

// semInvRepeat writes each row this many times on the BigFile layout:
// 101,000 (brand_uneq) or 121,200 (brand) records, past the
// parallel-decode threshold.
const semInvRepeat = 101

// newSEMInvFixture writes s's twin and archive (hermetic); heavy adds
// the decode-threshold file on disk for a mergeable shape.
func newSEMInvFixture(t *testing.T, s semInvShape, heavy bool) map[invariance.Layout]invCohort {
	t.Helper()
	_, twin := brandGroupCohort{csv: s.csv, wave: s.wave}.build(t, pulse.Options{})
	_, archive := brandGroupCohort{csv: s.csv, wave: s.wave, shards: 230}.build(t, pulse.Options{})
	out := map[invariance.Layout]invCohort{invariance.File: {twin, "brand.pulse"}, invariance.Archive: {archive, "brand.pulse"}}
	if heavy && s.mergeable {
		disk := afero.NewBasePathFs(afero.NewOsFs(), t.TempDir())
		brandGroupCohort{csv: s.csv, wave: s.wave, fsys: disk, repeat: func(int) int { return semInvRepeat }}.build(t, pulse.Options{})
		out[invariance.BigFile] = invCohort{disk, "brand.pulse"}
	}
	return out
}

// semInvGroups are s's groups: the archive group (a serial shape names
// the buffered arm on every cell and reruns the reference knobs), and
// in the heavy tier the decode group for a mergeable shape.
func semInvGroups(s semInvShape, heavy bool) []invariance.Group {
	archive := invariance.ArchiveGroup()
	if !s.mergeable {
		for i := range archive.Cells {
			archive.Cells[i].Arm = observe.ArmBuffered
		}
		archive.Cells = append(archive.Cells, invariance.Cell{Knobs: archive.Ref.Knobs, Arm: observe.ArmBuffered})
	}
	gs := []invariance.Group{archive}
	if heavy && s.mergeable {
		gs = append(gs, invariance.DecodeGroup("decode", invariance.BigFile))
	}
	return gs
}

// semInvFamily is shape s over groups.
func semInvFamily(t *testing.T, s semInvShape, heavy bool) invariance.Family[*types.Response] {
	cohorts := newSEMInvFixture(t, s, heavy)
	return invariance.Family[*types.Response]{
		Name:   s.name,
		Groups: semInvGroups(s, heavy),
		Run: func(t *testing.T, k invariance.Knobs) invariance.Result[*types.Response] {
			co, ok := cohorts[k.Layout]
			if !ok {
				t.Fatalf("layout %s not built in this tier", k.Layout)
			}
			p, arm := invEngine(t, pulse.Options{}, co, k)
			resp, err := p.Process(context.Background(), s.req(t))
			if err != nil {
				t.Fatalf("%s: Process: %v", k, err)
			}
			return invariance.Result[*types.Response]{Answer: invariance.LayoutFree(resp), Arm: arm()}
		},
		Check: func(t *testing.T, _ invariance.Knobs, resp *types.Response) {
			if len(resp.Matrices) != 1 || resp.Matrices[0].Error != nil || len(resp.Matrices[0].Auxiliary["estimates"].Values) == 0 {
				t.Fatalf("degenerate fit: %+v", resp.Matrices)
			}
			if _, boot := resp.Matrices[0].Scalars["bootstrap_succeeded"]; boot == s.mergeable {
				t.Fatalf("bootstrap_succeeded present = %v on a mergeable = %v shape", boot, s.mergeable)
			}
		},
	}
}

// TestSEMInvariance_Archive is the fast-tier smoke of the MAT_SEM
// family (semInvSmoke); TestSEMInvariance_Heavy runs every shape and
// adds the decode group.
func TestSEMInvariance_Archive(t *testing.T) {
	t.Parallel()

	for _, s := range semInvShapes() {
		if semInvSmoke[s.name] {
			invariance.Run(t, semInvFamily(t, s, false))
		}
	}
}
