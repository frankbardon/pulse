package pulse

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestComposeSweep_RankProfile: rank.profile adds `ranking_profile`
// beside `ranking` on both orchestrators — per axis value the best t
// statistic over the other axis (min asc / max desc, ties in expansion
// order), the slot achieving it and n, in declared value order — and a
// Compose-level `return` neither drops nor rounds it.
func TestComposeSweep_RankProfile(t *testing.T) {
	t.Parallel()

	p := sweepPulse(t, Options{})
	for name, run := range composeRuns(p) {
		t.Run(name, func(t *testing.T) {
			for _, order := range []string{"asc", "desc"} {
				c := rankedSweep(t, `{"by":"tests.t.statistic","order":"`+order+`","top":1,"profile":true}`)
				c.Return = &types.Return{Preset: types.ReturnPresetMinimal, Precision: 1}
				resp, err := run(c)
				if err != nil {
					t.Fatal(err)
				}
				better := func(a, b float64) bool {
					if order == "desc" {
						return a > b
					}
					return a < b
				}
				// The hand table: walk the slots in expansion order,
				// keeping the first best per axis value.
				mus, reps := []string{"30", "10", "50"}, []string{"1", "2"}
				type cell struct {
					best  float64
					label string
					n     int
				}
				table := map[string]map[string]*cell{"mu": {}, "rep": {}}
				slot := 1
				for _, mu := range mus {
					for _, rep := range reps {
						label := "mu" + mu + "_" + rep
						v := tStat(t, resp.Responses[slot])
						slot++
						for ax, val := range map[string]string{"mu": mu, "rep": rep} {
							c := table[ax][val]
							if c == nil {
								table[ax][val] = &cell{v, label, 1}
								continue
							}
							c.n++
							if better(v, c.best) {
								c.best, c.label = v, label
							}
						}
					}
				}
				want := map[string][]types.RankProfileEntry{}
				for ax, vals := range map[string][]string{"mu": mus, "rep": reps} {
					for _, val := range vals {
						c := table[ax][val]
						f, _ := strconv.ParseFloat(val, 64)
						want[ax] = append(want[ax], types.RankProfileEntry{Value: f, Best: c.best, Label: c.label, N: c.n})
					}
				}
				if len(resp.Ranking) != 1 {
					t.Fatalf("top trimmed: %+v", resp.Ranking)
				}
				raw, err := json.Marshal(resp)
				if err != nil {
					t.Fatal(err)
				}
				var wire struct {
					Profile map[string][]types.RankProfileEntry `json:"ranking_profile"`
				}
				if err := json.Unmarshal(raw, &wire); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(wire.Profile, want) {
					t.Fatalf("%s: wire profile %+v\nwant %+v", order, wire.Profile, want)
				}
			}
		})
	}
}

// TestComposeSweep_RankProfileOffByteIdentical: rank.profile absent or
// false writes no `ranking_profile` key and the same bytes; turned on,
// the response minus that key is byte-identical to the off run.
func TestComposeSweep_RankProfileOffByteIdentical(t *testing.T) {
	t.Parallel()

	p := sweepPulse(t, Options{})
	for name, run := range composeRuns(p) {
		t.Run(name, func(t *testing.T) {
			wire := func(rank string) []byte {
				t.Helper()
				resp, err := run(rankedSweep(t, rank))
				if err != nil {
					t.Fatal(err)
				}
				raw, err := json.Marshal(resp)
				if err != nil {
					t.Fatal(err)
				}
				return raw
			}
			off := wire(`{"by":"tests.t.statistic"}`)
			offFalse := wire(`{"by":"tests.t.statistic","profile":false}`)
			if !bytes.Equal(off, offFalse) || bytes.Contains(off, []byte(`ranking_profile`)) {
				t.Fatalf("off differs or carries a profile:\n%s\n%s", off, offFalse)
			}
			on := wire(`{"by":"tests.t.statistic","profile":true}`)
			var m map[string]json.RawMessage
			if err := json.Unmarshal(on, &m); err != nil {
				t.Fatal(err)
			}
			if _, ok := m["ranking_profile"]; !ok {
				t.Fatalf("profile on, no key: %s", on)
			}
			delete(m, "ranking_profile")
			var offMap map[string]json.RawMessage
			if err := json.Unmarshal(off, &offMap); err != nil {
				t.Fatal(err)
			}
			a, _ := json.Marshal(m)
			b, _ := json.Marshal(offMap)
			if !bytes.Equal(a, b) {
				t.Fatalf("profile moved another part:\n%s\n%s", a, b)
			}
		})
	}
}

// TestComposeSweepBind_RankProfile: a sweep-bound batch ranks in phase 0
// and reports the profile it computed there.
func TestComposeSweepBind_RankProfile(t *testing.T) {
	t.Parallel()

	p, err := New(Options{FS: fitsFacadeFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	c := sweepBindBatch(t, []types.FitSpec{sweepBound("best", "m")}, sweepBindBody(t, `["x"]`, ""),
		func(s *types.SweepSpec) { s.Rank.Profile = true })
	for name, run := range composeRuns(p) {
		out, err := run(c)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		hi := out.RankingProfile["hi"]
		if len(out.Ranking) != 2 || len(hi) != 2 {
			t.Fatalf("%s: ranking %+v profile %+v", name, out.Ranking, out.RankingProfile)
		}
		for i, v := range []string{"60", "90"} {
			e := hi[i]
			if e.Value != json.Number(v) || e.Label != "hi_"+v || e.N != 1 {
				t.Errorf("%s: hi[%d] = %+v", name, i, e)
			}
			for _, r := range out.Ranking {
				if r.Label == e.Label && r.Value != e.Best {
					t.Errorf("%s: %s best %v, ranked %v", name, e.Label, e.Best, r.Value)
				}
			}
		}
	}
}
