package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	pulse "github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/mmmfixture"
	"github.com/frankbardon/pulse/types"
)

// mmmCLIFixture writes the media-mix fixture cohort into a temp dir
// and returns the dir, an instance and a func that points a fixture
// request (or slot body) at the written cohort's absolute path.
func mmmCLIFixture(t *testing.T) (string, *pulse.Pulse, func([]byte) []byte) {
	t.Helper()
	dir := withTempDataDir(t)
	p, err := newPulse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	cohort := filepath.Join(dir, mmmfixture.CohortFile)
	quoted, err := json.Marshal(cohort)
	if err != nil {
		t.Fatal(err)
	}
	at := func(raw []byte) []byte {
		out := bytes.ReplaceAll(raw, []byte(`"`+mmmfixture.CohortFile+`"`), quoted)
		if bytes.Equal(out, raw) {
			t.Fatalf("request names no %s cohort", mmmfixture.CohortFile)
		}
		return out
	}
	b, err := p.NewCohortBuilder(t.Context(), cohort, mmmfixture.Schema(), pulse.CohortBuilderOptions{})
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
	return dir, p, at
}

// TestHoldout_MMMCLIComposeSweep (transformed-predictors E3-S3):
// `pulse api compose --json` runs the canonical media-mix decay sweep
// (and its FEAT_TRAIN_TEST_SPLIT variant) with no materialize step —
// no envelope error, a populated ranking equal to the library's for the
// same batch with the fixture's true decay first, every slot without
// data rows and with its own holdout rmse.
func TestHoldout_MMMCLIComposeSweep(t *testing.T) {
	dir, p, at := mmmCLIFixture(t)
	for name, raw := range map[string][]byte{"week": at(mmmfixture.DecaySweep), "split": at(mmmfixture.DecaySweepSplit)} {
		t.Run(name, func(t *testing.T) {
			reqPath := filepath.Join(dir, name+".json")
			if err := os.WriteFile(reqPath, raw, 0o644); err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			if err := runAPICLI(t, &buf, "compose", "--json", "-r", reqPath); err != nil {
				t.Fatalf("compose: %v\n%s", err, buf.String())
			}
			var env struct {
				Errors []any `json:"errors"`
				Data   struct {
					Ranking   []types.RankEntry            `json:"ranking"`
					Responses []map[string]json.RawMessage `json:"responses"`
				} `json:"data"`
			}
			if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
				t.Fatalf("envelope: %v\n%s", err, buf.String())
			}
			if len(env.Errors) != 0 || len(env.Data.Responses) != len(mmmfixture.Decays) {
				t.Fatalf("want %d slots and no errors; envelope %s", len(mmmfixture.Decays), buf.String())
			}

			var req pulse.ComposedRequest
			if err := json.Unmarshal(raw, &req); err != nil {
				t.Fatal(err)
			}
			lib, err := p.Compose(t.Context(), &req)
			if err != nil {
				t.Fatalf("library Compose: %v", err)
			}
			if len(lib.Ranking) != len(mmmfixture.Decays) || !sameRanking(t, env.Data.Ranking, lib.Ranking) {
				t.Fatalf("CLI ranking %+v, library %+v", env.Data.Ranking, lib.Ranking)
			}
			if env.Data.Ranking[0].Label != "decay_0.5" {
				t.Fatalf("best slot %q, want decay_0.5 (the fixture's true decay)", env.Data.Ranking[0].Label)
			}
			seen := map[float64]bool{}
			for i, s := range env.Data.Responses {
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

// TestHoldout_MMMCLIPredict (transformed-predictors E3-S3): `pulse api
// predict-compose` validates the canonical sweep, and `pulse api
// predict` on the request one slot runs validates it and reports it
// non-streamable with the holdout reason.
func TestHoldout_MMMCLIPredict(t *testing.T) {
	dir, _, at := mmmCLIFixture(t)
	sweepPath := filepath.Join(dir, "sweep.json")
	if err := os.WriteFile(sweepPath, at(mmmfixture.DecaySweep), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := runAPICLI(t, &buf, "predict-compose", "--json", "-r", sweepPath); err != nil {
		t.Fatalf("predict-compose: %v\n%s", err, buf.String())
	}
	var cenv struct {
		Errors []any `json:"errors"`
		Data   struct {
			Valid bool `json:"valid"`
			Sweep struct {
				ExpandedCount int `json:"expanded_count"`
			} `json:"sweep"`
		} `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &cenv); err != nil || len(cenv.Errors) != 0 || !cenv.Data.Valid ||
		cenv.Data.Sweep.ExpandedCount != len(mmmfixture.Decays) {
		t.Fatalf("predict-compose envelope (%v): %s", err, buf.String())
	}

	body, err := mmmfixture.SlotBody(mmmfixture.DecaySweep, "0.5")
	if err != nil {
		t.Fatal(err)
	}
	slotPath := filepath.Join(dir, "slot.json")
	if err := os.WriteFile(slotPath, at(body), 0o644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := runAPICLI(t, &buf, "predict", "--json", "-r", slotPath); err != nil {
		t.Fatalf("predict: %v\n%s", err, buf.String())
	}
	var penv struct {
		Errors []any `json:"errors"`
		Data   struct {
			Valid             bool     `json:"valid"`
			Streamable        bool     `json:"streamable"`
			StreamableReasons []string `json:"streamable_reasons"`
		} `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &penv); err != nil || len(penv.Errors) != 0 || !penv.Data.Valid || penv.Data.Streamable {
		t.Fatalf("predict envelope (%v): %s", err, buf.String())
	}
	if !slices.ContainsFunc(penv.Data.StreamableReasons, func(s string) bool {
		return strings.Contains(s, "REG_OLS holdout requires the buffered path")
	}) {
		t.Fatalf("streamable reasons %q lack the holdout reason", penv.Data.StreamableReasons)
	}
}

// sameRanking compares two rankings on the wire: one decoded from JSON
// holds float64 axis values where the library's hold json.Number (whose
// text may differ, 0.0 against 0), so both are compared as decoded JSON.
func sameRanking(t *testing.T, a, b []types.RankEntry) bool {
	t.Helper()
	var docs [2]any
	for i, r := range [][]types.RankEntry{a, b} {
		raw, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &docs[i]); err != nil {
			t.Fatal(err)
		}
	}
	return reflect.DeepEqual(docs[0], docs[1])
}
