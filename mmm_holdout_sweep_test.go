package pulse

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/mmmfixture"
	"github.com/frankbardon/pulse/types"
)

// mmm_holdout_sweep_test.go — the one-step media-mix decay sweep
// (transformed predictors E3-S3), library surface: the canonical
// request in internal/mmmfixture (four WIN_DISTRIBUTED_LAG windows with decay
// {"$var":"decay"} → hill() saturations → REG_OLS bounds.nonnegative on
// the channels + controls with a holdout, swept over decay 0.0..0.9 and
// ranked by regressions.mmm.holdout.rmse, `return` excluding data) runs
// with no materialize step. The CLI and MCP twins live in
// internal/cli and internal/mcp (mmm_holdout_test.go).
//
// The fixture's sales carry a TRUE adstock decay of 0.5, so a correct
// sweep ranks decay_0.5 first: the rank is pinned to the data, not just
// to "some order".

// mmmPulse builds a hermetic instance with the fixture cohort written.
func mmmPulse(t *testing.T) *Pulse {
	t.Helper()
	p, _ := matPulse(t, Options{})
	b, err := p.NewCohortBuilder(context.Background(), mmmfixture.CohortFile, mmmfixture.Schema(), CohortBuilderOptions{})
	if err != nil {
		t.Fatalf("NewCohortBuilder: %v", err)
	}
	for _, r := range mmmfixture.Rows() {
		if err := b.Append(CohortRow(r)); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return p
}

// mmmComposed decodes a fixture request, applying edit (when set) to
// the sweep body's decoded JSON object first.
func mmmComposed(t *testing.T, raw []byte, edit func(body map[string]any)) *ComposedRequest {
	t.Helper()
	if edit != nil {
		// UseNumber: the axis values keep their digits ("0.0" labels
		// decay_0.0, as the fixture writes it).
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		var outer map[string]any
		if err := dec.Decode(&outer); err != nil {
			t.Fatal(err)
		}
		edit(outer["sweep"].(map[string]any)["request"].(map[string]any))
		var err error
		if raw, err = json.Marshal(outer); err != nil {
			t.Fatal(err)
		}
	}
	var req ComposedRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return &req
}

// mmmVariants are the two holdout forms: a time cut (the last 14 weeks
// of each market) and the test third of a FEAT_TRAIN_TEST_SPLIT.
var mmmVariants = []struct {
	name  string
	raw   []byte
	heldN int // 0: not fixed by construction
}{
	{"week>=91", mmmfixture.DecaySweep, len(mmmfixture.Markets) * (mmmfixture.Weeks - mmmfixture.HoldoutFrom + 1)},
	{"split==2", mmmfixture.DecaySweepSplit, 0},
}

// assertMMMRanking checks a sweep result against the canonical
// contract and returns the ranking: one entry per decay, ascending,
// decay_0.5 first, every value the slot's own holdout RMSE, every
// slot's RMSE distinct; the held-out n (heldN when fixed) plus n_obs
// cover every row; and no slot carries data.
func assertMMMRanking(t *testing.T, out *ComposedResponse, heldN int) []types.RankEntry {
	t.Helper()
	if len(out.Responses) != len(mmmfixture.Decays) || len(out.Ranking) != len(mmmfixture.Decays) {
		t.Fatalf("slots %d, ranking %d; want %d each", len(out.Responses), len(out.Ranking), len(mmmfixture.Decays))
	}
	byLabel := map[string]*types.RegressionResult{}
	seen := map[float64]string{}
	for i, resp := range out.Responses {
		want := "decay_" + mmmfixture.Decays[i]
		if resp.Label != want {
			t.Fatalf("slot %d label %q, want %q", i, resp.Label, want)
		}
		if len(resp.Data) != 0 {
			t.Fatalf("slot %s carries %d data rows; its return excludes data", want, len(resp.Data))
		}
		if len(resp.Regressions) != 1 || resp.Regressions[0].Error != nil || resp.Regressions[0].Holdout == nil {
			t.Fatalf("slot %s regressions %+v; want one fit with a holdout", want, resp.Regressions)
		}
		r := resp.Regressions[0]
		h := r.Holdout
		if h.N <= 0 || (heldN != 0 && h.N != heldN) || h.NNull != 0 || r.NObs+h.N != len(mmmfixture.Markets)*mmmfixture.Weeks {
			t.Fatalf("slot %s: n_obs %d, holdout %+v; want held %d and n_obs + held = every row", want, r.NObs, h, heldN)
		}
		if !(h.RMSE > 0) || math.IsInf(h.RMSE, 0) {
			t.Fatalf("slot %s holdout rmse %v", want, h.RMSE)
		}
		if prev, dup := seen[h.RMSE]; dup {
			t.Fatalf("slots %s and %s share holdout rmse %v: the swept decay never reached the adstock", prev, want, h.RMSE)
		}
		seen[h.RMSE] = want
		byLabel[want] = r
	}
	for i, e := range out.Ranking {
		r := byLabel[e.Label]
		if r == nil || e.Rank != i+1 || e.Value != r.Holdout.RMSE {
			t.Fatalf("ranking[%d] = %+v; want rank %d valued at the slot's holdout rmse", i, e, i+1)
		}
		if i > 0 && !(out.Ranking[i-1].Value < e.Value) {
			t.Fatalf("ranking not ascending at %d: %+v", i, out.Ranking)
		}
	}
	if out.Ranking[0].Label != "decay_0.5" {
		t.Fatalf("best slot %q, want decay_0.5 (the fixture's true decay); ranking %+v", out.Ranking[0].Label, out.Ranking)
	}
	return out.Ranking
}

// TestHoldout_MMMDecaySweepRanksByHoldoutRMSE: the canonical request
// (and its FEAT_TRAIN_TEST_SPLIT variant) runs through library Compose
// and ComposeParallel with no materialize step; the ranking is
// populated from each slot's own holdout RMSE, the RMSEs differ per
// decay and the true decay ranks first. Serial and parallel rank alike.
func TestHoldout_MMMDecaySweepRanksByHoldoutRMSE(t *testing.T) {
	p := mmmPulse(t)
	ctx := context.Background()
	for _, v := range mmmVariants {
		t.Run(v.name, func(t *testing.T) {
			serial, err := p.Compose(ctx, mmmComposed(t, v.raw, nil))
			if err != nil {
				t.Fatalf("Compose: %v", err)
			}
			want := assertMMMRanking(t, serial, v.heldN)
			parallel, err := p.ComposeParallel(ctx, mmmComposed(t, v.raw, nil), ComposeOptions{MaxWorkers: 4, FailFast: true})
			if err != nil {
				t.Fatalf("ComposeParallel: %v", err)
			}
			if got := assertMMMRanking(t, parallel, v.heldN); !reflect.DeepEqual(got, want) {
				t.Fatalf("parallel ranking %+v, serial %+v", got, want)
			}
		})
	}
}

// TestHoldout_MMMSweepSlotIsTheOneRequestFit: every sweep slot's fit —
// holdout figures included — is bit for bit the plain Process of the
// sweep body with that decay written in: the sweep adds nothing to the
// one-request route the E1–E3 parity gates pin against materialize.
func TestHoldout_MMMSweepSlotIsTheOneRequestFit(t *testing.T) {
	p := mmmPulse(t)
	ctx := context.Background()
	out, err := p.Compose(ctx, mmmComposed(t, mmmfixture.DecaySweep, nil))
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	for i, d := range mmmfixture.Decays {
		body, err := mmmfixture.SlotBody(mmmfixture.DecaySweep, d)
		if err != nil {
			t.Fatal(err)
		}
		var req Request
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("decay %s body: %v", d, err)
		}
		resp, err := p.Process(ctx, &req)
		if err != nil {
			t.Fatalf("decay %s Process: %v", d, err)
		}
		assertRegressionsBitEqual(t, out.Responses[i].Regressions, resp.Regressions)
	}
}

// TestHoldout_MMMRankVetoKeepsHoldout: a slot `return` that excludes
// the regressions (or only their holdout block) still ranks — the rank
// veto computes the fit and its holdout, then shaping trims the wire —
// with the very ranking the canonical request produces.
func TestHoldout_MMMRankVetoKeepsHoldout(t *testing.T) {
	p := mmmPulse(t)
	ctx := context.Background()
	canon, err := p.Compose(ctx, mmmComposed(t, mmmfixture.DecaySweep, nil))
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	for _, exclude := range [][]any{{"data", "regressions"}, {"data", "regressions[*].holdout"}} {
		t.Run(strings.Join([]string{exclude[0].(string), exclude[1].(string)}, "+"), func(t *testing.T) {
			req := mmmComposed(t, mmmfixture.DecaySweep, func(body map[string]any) {
				body["return"] = map[string]any{"preset": "standard", "exclude": exclude}
			})
			out, err := p.Compose(ctx, req)
			if err != nil {
				t.Fatalf("Compose: %v", err)
			}
			if !reflect.DeepEqual(out.Ranking, canon.Ranking) {
				t.Fatalf("ranking under return exclude %v:\n got %+v\nwant %+v", exclude, out.Ranking, canon.Ranking)
			}
			for _, resp := range out.Responses {
				for _, r := range resp.Regressions {
					if r.Holdout != nil {
						t.Fatalf("slot %s kept the excluded holdout on the wire: %+v", resp.Label, r.Holdout)
					}
				}
				if exclude[1] == "regressions" && len(resp.Regressions) != 0 {
					t.Fatalf("slot %s kept excluded regressions", resp.Label)
				}
			}
		})
	}
}

// TestHoldout_MMMReturnExcludesData: the documented pattern — a sweep
// slot's `return` excluding data — keeps each slot to its fit: no data
// rows, and the slot is a small fraction of the unshaped slot, which
// carries one row per market-week with every adstock and saturation
// column. The ranking is the same either way.
func TestHoldout_MMMReturnExcludesData(t *testing.T) {
	p := mmmPulse(t)
	ctx := context.Background()
	shaped, err := p.Compose(ctx, mmmComposed(t, mmmfixture.DecaySweep, nil))
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	full, err := p.Compose(ctx, mmmComposed(t, mmmfixture.DecaySweep, func(body map[string]any) { delete(body, "return") }))
	if err != nil {
		t.Fatalf("Compose (no return): %v", err)
	}
	if !reflect.DeepEqual(shaped.Ranking, full.Ranking) {
		t.Fatalf("ranking moved with the return: shaped %+v, full %+v", shaped.Ranking, full.Ranking)
	}
	for i := range shaped.Responses {
		s, f := shaped.Responses[i], full.Responses[i]
		if len(s.Data) != 0 || len(f.Data) != len(mmmfixture.Markets)*mmmfixture.Weeks {
			t.Fatalf("slot %s: shaped data %d rows, full %d rows", s.Label, len(s.Data), len(f.Data))
		}
		if s.Returned == nil || f.Returned != nil {
			t.Fatalf("slot %s: shaped returned %+v, full returned %+v", s.Label, s.Returned, f.Returned)
		}
		sb, _ := json.Marshal(s)
		fb, _ := json.Marshal(f)
		if len(sb)*10 > len(fb) {
			t.Fatalf("slot %s: shaped %d bytes, full %d; want under a tenth", s.Label, len(sb), len(fb))
		}
	}
}

// TestHoldout_MMMPredict: predict validates the canonical sweep (ten
// expanded slots, no errors) and, on the request one slot runs, reports
// it non-streamable with the holdout reason and the windows reason.
func TestHoldout_MMMPredict(t *testing.T) {
	p := mmmPulse(t)
	ctx := context.Background()
	for _, v := range mmmVariants {
		t.Run(v.name, func(t *testing.T) {
			env, err := p.PredictCompose(ctx, mmmComposed(t, v.raw, nil))
			if err != nil || len(env.Errors) != 0 {
				t.Fatalf("PredictCompose: %v %+v", err, env.Errors)
			}
			cv, ok := env.Data.(*descx.ComposeValidationResult)
			if !ok || !cv.Valid || cv.Sweep == nil || cv.Sweep.ExpandedCount != len(mmmfixture.Decays) {
				t.Fatalf("compose predict %+v", env.Data)
			}
			body, err := mmmfixture.SlotBody(v.raw, "0.5")
			if err != nil {
				t.Fatal(err)
			}
			var req Request
			if err := json.Unmarshal(body, &req); err != nil {
				t.Fatal(err)
			}
			penv, err := p.PredictEnvelope(ctx, &req)
			if err != nil || len(penv.Errors) != 0 {
				t.Fatalf("PredictEnvelope: %v %+v", err, penv.Errors)
			}
			pr := penv.Data.(*descriptor.PredictResult)
			if !pr.Valid || pr.Streamable {
				t.Fatalf("predict valid %v streamable %v; want valid and non-streamable", pr.Valid, pr.Streamable)
			}
			for _, want := range []string{"REG_OLS holdout requires the buffered path", "windows run over"} {
				if !slices.ContainsFunc(pr.StreamableReasons, func(s string) bool { return strings.Contains(s, want) }) {
					t.Fatalf("streamable reasons %q lack %q", pr.StreamableReasons, want)
				}
			}
		})
	}
}
