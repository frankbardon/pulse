package pulse_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/mergegate"
	"github.com/frankbardon/pulse/internal/testutil/invariance"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// previous_state_invariance_test.go is the ATTR_PREVIOUS_STATE family on
// the shared worker-invariance harness: every matrix request
// (prevModeRequests — grouped by the previous state, the ungrouped null
// count, the previous × state crosstab, and an expression filter
// reading the output by name, grouped and as a crosstab) answers the
// same whole response (LayoutFree), bitwise — data, crosstab, warnings
// and Components alike:
//
//   - archive: a shard archive whose subjects' records span shards —
//     later waves in earlier shards, an empty shard, per-shard
//     dictionaries in different orders — under ShardWorkers 1/2/3/8 and
//     DecodeWorkers 7 ≡ the single file. The merge gate refuses the
//     request as not row-local, so every cell runs the serial buffered
//     scan (a partition-local previous state would read across the
//     wrong records);
//   - decode (heavy, previous_state_invariance_heavy_test.go): over an
//     on-disk cohort past the parallel-decode threshold — the panel
//     interleaved with filler subjects prevModeKeep drops —
//     DecodeWorkers 2/3/7/8 ≡ serial; a crosstab decodes its records in
//     parallel segments (the attribute still scores the whole decoded
//     set, never a segment), every other request stays serial; and the
//     reference's data, crosstab and warnings are the panel's
//     single-file answer.

// newPrevInvFixture writes the panel and its archive (hermetic); heavy
// adds the filler-interleaved file on disk.
func newPrevInvFixture(t *testing.T, heavy bool) map[invariance.Layout]invCohort {
	t.Helper()
	mem := afero.NewMemMapFs()
	w, err := pulse.New(pulse.Options{FS: mem})
	if err != nil {
		t.Fatal(err)
	}
	panelCohort(t, w, "panel.pulse", prevModeRows)
	var shards []string
	for i, rows := range prevModeShards() {
		name := fmt.Sprintf("panel_s%d.pulse", i)
		panelCohort(t, w, name, rows)
		shards = append(shards, name)
	}
	if _, err := w.CreateShardArchive(context.Background(), "panel_archive.pulse", shards); err != nil {
		t.Fatal(err)
	}
	out := map[invariance.Layout]invCohort{invariance.File: {mem, "panel.pulse"}, invariance.Archive: {mem, "panel_archive.pulse"}}
	if heavy {
		disk := afero.NewBasePathFs(afero.NewOsFs(), t.TempDir())
		dw, err := pulse.New(pulse.Options{FS: disk})
		if err != nil {
			t.Fatal(err)
		}
		rows := make([][3]any, 0, len(prevModeRows)+prevModeDecodeFiller)
		every := (len(prevModeRows) + prevModeDecodeFiller) / len(prevModeRows)
		next := 0
		for i := 0; i < len(prevModeRows)+prevModeDecodeFiller; i++ {
			if next < len(prevModeRows) && i == next*every {
				rows = append(rows, prevModeRows[next])
				next++
				continue
			}
			rows = append(rows, [3]any{uint64(1_000_000 + i), int32(1), "free"})
		}
		panelCohort(t, dw, "panel_big.pulse", rows)
		out[invariance.BigFile] = invCohort{disk, "panel_big.pulse"}
	}
	return out
}

// prevInvNames is prevModeRequests' names, sorted.
func prevInvNames() []string {
	var out []string
	for name := range prevModeRequests("") {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// prevInvGroups are request name's groups — the archive group in the
// fast tier, the decode group in the heavy one: every cell on the
// serial buffered arm (DecodeWorkers 3 and 8 pin that), except a
// crosstab's parallel segment decode (2/3/7/8).
func prevInvGroups(name string, heavy bool) []invariance.Group {
	serial := func(g invariance.Group, arm observe.Arm) invariance.Group {
		for i := range g.Cells {
			g.Cells[i].Arm = arm
		}
		return g
	}
	if !heavy {
		return []invariance.Group{serial(invariance.ArchiveGroup(), observe.ArmBuffered)}
	}
	if strings.Contains(name, "crosstab") {
		return []invariance.Group{serial(invariance.DecodeGroup("decode", invariance.BigFile), observe.ArmParallelDecode)}
	}
	return []invariance.Group{invSerialDecodeGroup("decode", invariance.BigFile, observe.ArmBuffered)}
}

// prevInvFamily is request name over groups; panel (heavy only) is the
// panel's single-file answer the big file's reference must match, its
// filler-moved counts aside.
func prevInvFamily(cohorts map[invariance.Layout]invCohort, name string, groups []invariance.Group, panel map[string]string) invariance.Family[*types.Response] {
	return invariance.Family[*types.Response]{
		Name:   name,
		Groups: groups,
		Run: func(t *testing.T, k invariance.Knobs) invariance.Result[*types.Response] {
			co, ok := cohorts[k.Layout]
			if !ok {
				t.Fatalf("layout %s not built in this tier", k.Layout)
			}
			p, arm := invEngine(t, pulse.Options{}, co, k)
			resp, err := p.Process(context.Background(), prevModeRequests(co.path)[name]())
			if err != nil {
				t.Fatalf("%s: Process: %v", k, err)
			}
			return invariance.Result[*types.Response]{Answer: invariance.LayoutFree(resp), Arm: arm()}
		},
		Check: func(t *testing.T, k invariance.Knobs, resp *types.Response) {
			doc := prevModeDoc(t, resp, true)
			if strings.Contains(name, "crosstab") {
				if name == "crosstab" && (!strings.Contains(doc, "PULSE_PREVIOUS_STATE_AMBIGUOUS_TIE") || !strings.Contains(doc, `"row_keys":[["free"],["paid"],["pro"]]`)) {
					t.Fatalf("crosstab lacks the tie warning or is not keyed by state name: %s", doc)
				}
				if !strings.Contains(doc, `"row_keys":[[`) {
					t.Fatalf("degenerate crosstab: %s", doc)
				}
			} else if len(resp.Data) == 0 {
				t.Fatal("no rows")
			}
			if k.Layout != invariance.BigFile {
				return
			}
			var single types.Response
			if err := json.Unmarshal([]byte(panel[name]), &single); err != nil {
				t.Fatal(err)
			}
			if got, w := prevModeDoc(t, resp, false), prevModeDoc(t, &single, false); got != w {
				t.Errorf("filler-interleaved file\n%s\nsingle-file panel\n%s", got, w)
			}
		},
	}
}

// TestPreviousStateInvariance_Archive is the fast-tier half of the
// previous-state family; TestPreviousStateInvariance_Heavy adds the
// decode group. It also pins the gates the arms follow: the merge gate
// refuses every non-crosstab request as not row-local, and predict
// reports each non-streamable.
func TestPreviousStateInvariance_Archive(t *testing.T) {
	t.Parallel()

	cohorts := newPrevInvFixture(t, false)
	p, _ := invEngine(t, pulse.Options{}, cohorts[invariance.Archive], invariance.Knobs{ShardWorkers: 3})
	for name, mk := range prevModeRequests("panel_archive.pulse") {
		if mk().Crosstab == nil {
			if why := mergegate.MergeRefusal(mk(), nil, nil); !strings.Contains(why, "attribute ATTR_PREVIOUS_STATE is not row-local") {
				t.Errorf("%s: merge refusal %q, want the not-row-local attribute", name, why)
			}
		}
		if pr, err := p.Predict(context.Background(), mk()); err != nil || pr.Streamable {
			t.Errorf("%s: predict streamable over the archive (err %v)", name, err)
		}
	}
	for _, name := range prevInvNames() {
		invariance.Run(t, prevInvFamily(cohorts, name, prevInvGroups(name, false), nil))
	}
}
