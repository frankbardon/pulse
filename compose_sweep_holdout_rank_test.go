package pulse

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/afero"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// compose_sweep_holdout_rank_test.go: a sweep whose axis reaches a
// window param, each slot an OLS fit with a holdout, ranked by the
// fit's own holdout RMSE — the generic sweep × window × holdout × rank
// path on a small linear cohort. y depends on x two rows back, so the
// swept WIN_LAG offset 2 ranks first: the rank is pinned to the data,
// not just to "some order".

const holdoutRankCohort = "lagged.pulse"

// holdoutRankRows is the number of rows; rows t > holdoutRankCut are
// held out.
const (
	holdoutRankRows = 80
	holdoutRankCut  = 60
)

// holdoutRankPulse writes the cohort: t = 1..80, x a deterministic
// zig-zag, y = 5 + 3·x(t−2) + a small deterministic wiggle.
func holdoutRankPulse(t *testing.T) *Pulse {
	t.Helper()
	x := func(i int) float64 { return float64((i*37)%23 + i%5) }
	var rows [][]string
	for i := 1; i <= holdoutRankRows; i++ {
		y := 5 + float64(3*x(i-2)) + float64(0.1*float64((i*13)%7-3))
		rows = append(rows, []string{strconv.Itoa(i), strconv.FormatFloat(x(i), 'f', -1, 64), strconv.FormatFloat(y, 'f', -1, 64)})
	}
	fsys := afero.NewMemMapFs()
	createTestPulseFile(t, fsys, holdoutRankCohort, []string{"t", "x", "y"}, rows)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// holdoutRankBody is the sweep body: WIN_LAG of x by {"$var":"k"}, an
// OLS of y on the lag holding out t > 60, the slot's `return` (when
// set) in place of the default excluding data.
func holdoutRankBody(ret string) string {
	if ret == "" {
		ret = `{"exclude":["data"]}`
	}
	return `{"cohort":{"filename":"` + holdoutRankCohort + `"},
		"windows":[{"type":"WIN_LAG","field":"x","label":"x_lag","order_by":[{"field":"t"}],"params":{"offset":{"$var":"k"}}}],
		"regressions":[{"type":"REG_OLS","name":"fit","target":"y","predictors":["x_lag"],"holdout":"t > ` + strconv.Itoa(holdoutRankCut) + `"}],
		"return":` + ret + `}`
}

var holdoutRankOffsets = []int{0, 1, 2, 3, 4}

func holdoutRankSweep(t *testing.T, ret string) *ComposedRequest {
	t.Helper()
	return decodeComposedJSON(t, `{"sweep":{"axes":[{"name":"k","values":[0,1,2,3,4]}],"label":"k{{k}}",
		"request":`+holdoutRankBody(ret)+`,"rank":{"by":"regressions.fit.holdout.rmse"}}}`)
}

// assertHoldoutRanking checks one ranked run: a slot per offset, no
// data rows, one fit per slot with a holdout of the 20 held-out rows,
// every RMSE distinct (the swept offset reached the window), the
// ranking ascending and valued at each slot's own RMSE, offset 2 first.
func assertHoldoutRanking(t *testing.T, out *ComposedResponse) []types.RankEntry {
	t.Helper()
	if len(out.Responses) != len(holdoutRankOffsets) || len(out.Ranking) != len(holdoutRankOffsets) {
		t.Fatalf("slots %d, ranking %d; want %d each", len(out.Responses), len(out.Ranking), len(holdoutRankOffsets))
	}
	seen := map[float64]string{}
	for i, resp := range out.Responses {
		if want := "k" + strconv.Itoa(holdoutRankOffsets[i]); resp.Label != want {
			t.Fatalf("slot %d label %q, want %q", i, resp.Label, want)
		}
		if len(resp.Data) != 0 {
			t.Fatalf("slot %s carries %d data rows; its return excludes data", resp.Label, len(resp.Data))
		}
		if len(resp.Regressions) != 1 || resp.Regressions[0].Error != nil || resp.Regressions[0].Holdout == nil {
			t.Fatalf("slot %s regressions %+v; want one fit with a holdout", resp.Label, resp.Regressions)
		}
		h := resp.Regressions[0].Holdout
		if h.N != holdoutRankRows-holdoutRankCut || !(h.RMSE > 0) || math.IsInf(h.RMSE, 0) {
			t.Fatalf("slot %s holdout %+v; want %d held-out rows and a finite RMSE", resp.Label, h, holdoutRankRows-holdoutRankCut)
		}
		if prev, dup := seen[h.RMSE]; dup {
			t.Fatalf("slots %s and %s share holdout rmse %v: the swept offset never reached the window", prev, resp.Label, h.RMSE)
		}
		seen[h.RMSE] = resp.Label
	}
	for i, e := range out.Ranking {
		slot := out.Responses[e.Slot]
		if e.Rank != i+1 || e.Label != slot.Label || e.Value != slot.Regressions[0].Holdout.RMSE {
			t.Fatalf("ranking[%d] = %+v; want rank %d valued at slot %q's holdout rmse", i, e, i+1, slot.Label)
		}
		if i > 0 && !(out.Ranking[i-1].Value < e.Value) {
			t.Fatalf("ranking not ascending at %d: %+v", i, out.Ranking)
		}
	}
	if out.Ranking[0].Label != "k2" {
		t.Fatalf("best slot %q, want k2 (y reads x two rows back); ranking %+v", out.Ranking[0].Label, out.Ranking)
	}
	return out.Ranking
}

// TestComposeSweep_HoldoutRankReachesWindow: the sweep runs through
// Compose and ComposeParallel alike; each slot is ranked by its own
// holdout RMSE, the RMSEs differ per swept offset and the data's true
// lag ranks first. Every slot's fit is bit for bit the plain Process of
// the sweep body with that offset written in.
func TestComposeSweep_HoldoutRankReachesWindow(t *testing.T) {
	p := holdoutRankPulse(t)
	ctx := context.Background()
	serial, err := p.Compose(ctx, holdoutRankSweep(t, ""))
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	want := assertHoldoutRanking(t, serial)
	parallel, err := p.ComposeParallel(ctx, holdoutRankSweep(t, ""), ComposeOptions{MaxWorkers: 4, FailFast: true})
	if err != nil {
		t.Fatalf("ComposeParallel: %v", err)
	}
	if got := assertHoldoutRanking(t, parallel); !reflect.DeepEqual(got, want) {
		t.Fatalf("parallel ranking %+v, serial %+v", got, want)
	}
	for i, k := range holdoutRankOffsets {
		var req Request
		body := strings.Replace(holdoutRankBody(""), `{"$var":"k"}`, strconv.Itoa(k), 1)
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatalf("offset %d body: %v", k, err)
		}
		resp, err := p.Process(ctx, &req)
		if err != nil {
			t.Fatalf("offset %d Process: %v", k, err)
		}
		assertRegressionsBitEqual(t, serial.Responses[i].Regressions, resp.Regressions)
	}
}

// TestComposeSweep_HoldoutRankVetoKeepsHoldout: a slot `return` that
// excludes the regressions (or only their holdout block) still ranks —
// the rank veto computes the fit and its holdout, then shaping trims
// the wire — with the very ranking the unshaped request produces.
func TestComposeSweep_HoldoutRankVetoKeepsHoldout(t *testing.T) {
	p := holdoutRankPulse(t)
	ctx := context.Background()
	canon, err := p.Compose(ctx, holdoutRankSweep(t, ""))
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	for _, exclude := range []string{"regressions", "regressions[*].holdout"} {
		t.Run(exclude, func(t *testing.T) {
			out, err := p.Compose(ctx, holdoutRankSweep(t, `{"preset":"standard","exclude":["data","`+exclude+`"]}`))
			if err != nil {
				t.Fatalf("Compose: %v", err)
			}
			if !reflect.DeepEqual(out.Ranking, canon.Ranking) {
				t.Fatalf("ranking under return exclude %s:\n got %+v\nwant %+v", exclude, out.Ranking, canon.Ranking)
			}
			for _, resp := range out.Responses {
				if exclude == "regressions" && len(resp.Regressions) != 0 {
					t.Fatalf("slot %s kept excluded regressions", resp.Label)
				}
				for _, r := range resp.Regressions {
					if r.Holdout != nil {
						t.Fatalf("slot %s kept the excluded holdout on the wire: %+v", resp.Label, r.Holdout)
					}
				}
			}
		})
	}
}

// TestComposeSweep_HoldoutRankPredict: predict validates the sweep
// (one expanded slot per offset, no error).
func TestComposeSweep_HoldoutRankPredict(t *testing.T) {
	p := holdoutRankPulse(t)
	env, err := p.PredictCompose(context.Background(), holdoutRankSweep(t, ""))
	if err != nil || len(env.Errors) != 0 {
		t.Fatalf("PredictCompose: %v %+v", err, env.Errors)
	}
	cv, ok := env.Data.(*descx.ComposeValidationResult)
	if !ok || !cv.Valid || cv.Sweep == nil || cv.Sweep.ExpandedCount != len(holdoutRankOffsets) {
		t.Fatalf("compose predict %+v", env.Data)
	}
}
