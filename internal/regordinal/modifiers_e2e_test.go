package regordinal_test

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/regordinal"
	"github.com/frankbardon/pulse/types"
)

// modifiers_e2e_test.go runs REG_ORDINAL under the resample and
// selection modifiers (U68 E2-S1, #651) end to end against R
// (ordinal.json's modifiers block: the manual-deletion jackknife on
// polr and MASS::stepAIC on polr, gen_ordinal.R), and pins the level
// pin (decision D5): a bootstrap replicate that lost a level is skipped
// (bootstrap_requested − bootstrap_succeeded counts it), a jackknife replicate that lost one
// fails the slot alone with reason replicate_empty_level.

// modRel is the oracle tolerance: every figure is a polished MLE (or a
// jackknife over polished refits), Newton-converged on both sides.
const modRel = 1e-8

// modSpec is a REG_ORDINAL slot over y ~ predictors with extra keys.
func modSpec(t *testing.T, predictors []string, extra string) *types.RegressionSpec {
	t.Helper()
	preds, _ := json.Marshal(predictors)
	var s types.RegressionSpec
	if err := json.Unmarshal([]byte(`{"type":"REG_ORDINAL","name":"ord","target":"y","predictors":`+string(preds)+extra+`}`), &s); err != nil {
		t.Fatal(err)
	}
	return &s
}

// thresholdNames are the "<lo>|<hi>" coefficient names over levels.
func thresholdNames(levels []string) []string {
	out := make([]string, len(levels)-1)
	for k := range out {
		out[k] = levels[k] + regordinal.ThresholdSeparator + levels[k+1]
	}
	return out
}

// checkFigures compares res's map figures keyed names with want.
func checkFigures(t *testing.T, what string, got map[string]float64, names []string, want []float64) {
	t.Helper()
	if len(names) != len(want) {
		t.Fatalf("%s: %d names, %d golden values", what, len(names), len(want))
	}
	for j, k := range names {
		g, ok := got[k]
		if !ok {
			t.Errorf("%s[%s] missing (%v)", what, k, got)
			continue
		}
		if e := rel(g, want[j]); e > modRel {
			t.Errorf("%s[%s] = %.17g, R %.17g (rel %.3g)", what, k, g, want[j], e)
		}
	}
}

// TestRegOrdinal_JackknifeMatchesR: the leave-one-out and the
// delete-a-cluster jackknife SEs of every coefficient (predictors and
// thresholds) match R's manual-deletion jackknife on polr; the
// estimates stay the full fit's, the thresholds carry no p-value and
// the predictors the Wald-z one over the replicate SE.
func TestRegOrdinal_JackknifeMatchesR(t *testing.T) {
	t.Parallel()
	jk := regordinal.LoadModifiers(t).Jackknife
	p := newInstance(t, nil)
	writeCohort(t, p, "jk.pulse", cohortSpec{p: len(jk.Predictors)}, jk.Rows, nil)
	names := append(slices.Clone(jk.Predictors), thresholdNames(jk.Levels)...)
	for _, tc := range []struct {
		name, extra string
		want        []float64
		groups      int
	}{
		{"leave_one_out", `,"resample":"jackknife"`, jk.StdErrors, 0},
		{"by_cluster", `,"resample":"jackknife","jackknife_by":"g"`, jk.ByStdErrors, jk.ByGroups},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := fitOne(t, p, "jk.pulse", modSpec(t, jk.Predictors, tc.extra))
			checkFigures(t, "coefficients", res.Coefficients, names, jk.Coefficients)
			checkFigures(t, "std_errors", res.StdErrors, names, tc.want)
			if res.Resample != "jackknife" || res.JackknifeGroups != tc.groups || res.NObs != len(jk.Rows) {
				t.Errorf("resample %q, jackknife_groups %d, n_obs %d; want jackknife, %d, %d", res.Resample, res.JackknifeGroups, res.NObs, tc.groups, len(jk.Rows))
			}
			for j, k := range names {
				pv := res.PValues[k]
				if j >= len(jk.Predictors) {
					if !math.IsNaN(pv) {
						t.Errorf("threshold %s carries p-value %v under resample", k, pv)
					}
					continue
				}
				z := res.Coefficients[k] / res.StdErrors[k]
				if want := math.Erfc(math.Abs(z) / math.Sqrt2); rel(pv, want) > 1e-12 {
					t.Errorf("p_values[%s] = %v, want the Wald-z %v", k, pv, want)
				}
			}
		})
	}
}

// TestRegOrdinal_SelectionMatchesStepAIC: forward, backward and
// stepwise selection under AIC and BIC pick MASS::stepAIC's terms on
// polr (forward in its entry order), and the final fit is the selected
// model's MLE.
func TestRegOrdinal_SelectionMatchesStepAIC(t *testing.T) {
	t.Parallel()
	sel := regordinal.LoadModifiers(t).Selection
	p := newInstance(t, nil)
	writeCohort(t, p, "sel.pulse", cohortSpec{p: len(sel.Predictors)}, sel.Rows, nil)
	for _, run := range sel.Runs {
		t.Run(run.Direction+"_"+run.Criterion, func(t *testing.T) {
			t.Parallel()
			res := fitOne(t, p, "sel.pulse", modSpec(t, sel.Predictors, fmt.Sprintf(`,"selection":%q,"criterion":%q`, run.Direction, run.Criterion)))
			got := slices.Clone(res.SelectedFeatures)
			want := slices.Clone(run.Selected)
			if run.Direction != "forward" {
				slices.Sort(got)
				slices.Sort(want)
			}
			if !slices.Equal(got, want) {
				t.Fatalf("selected %v, stepAIC %v (steps %v)", res.SelectedFeatures, run.Selected, run.Steps)
			}
			if res.Selection != run.Direction || res.Criterion != run.Criterion {
				t.Errorf("echo selection %q criterion %q", res.Selection, res.Criterion)
			}
			names := append(slices.Clone(run.Selected), thresholdNames(sel.Levels)...)
			checkFigures(t, "coefficients", res.Coefficients, names, run.Coefficients)
			if len(res.Coefficients) != len(names) {
				t.Errorf("coefficients %v, want exactly %v", res.Coefficients, names)
			}
			if e := rel(res.Deviance, run.Deviance); e > modRel {
				t.Errorf("deviance %v, R %v", res.Deviance, run.Deviance)
			}
		})
	}
}

// TestRegOrdinal_BootstrapSeedStable: a seeded bootstrap is bit-for-bit
// stable across runs and DecodeWorkers / ShardWorkers counts, every
// replicate refitting (succeeded = requested).
func TestRegOrdinal_BootstrapSeedStable(t *testing.T) {
	t.Parallel()
	jk := regordinal.LoadModifiers(t).Jackknife
	var first string
	for _, w := range []int{1, 4, 1} {
		p := newInstance(t, func(o *pulse.Options) { o.DecodeWorkers, o.ShardWorkers = w, w })
		writeCohort(t, p, "jk.pulse", cohortSpec{p: len(jk.Predictors)}, jk.Rows, nil)
		res := fitOne(t, p, "jk.pulse", modSpec(t, jk.Predictors, `,"resample":"bootstrap","bootstrap_iters":40,"rng_seed":11`))
		if res.BootstrapRequested != 40 || res.BootstrapSucceeded != 40 {
			t.Fatalf("bootstrap %d requested / %d succeeded, want 40 / 40", res.BootstrapRequested, res.BootstrapSucceeded)
		}
		b := mustJSON(t, res)
		if first == "" {
			first = b
			continue
		}
		if b != first {
			t.Errorf("workers %d: the seeded bootstrap moved:\n%s\n%s", w, b, first)
		}
	}
}

// rareLevelRows adds a rare fourth level above the three: the keep
// level-3 rows with the largest x1 move to level 4, their cluster set
// to g. A replicate without them would otherwise fit three levels —
// other threshold names — so only the level pin keeps it comparable.
func rareLevelRows(rows [][]float64, p, keep int, g float64) [][]float64 {
	out := make([][]float64, len(rows))
	var top []int
	for i, r := range rows {
		out[i] = slices.Clone(r)
		if r[p] == 3 {
			top = append(top, i)
		}
	}
	slices.SortFunc(top, func(a, b int) int { return cmp.Compare(rows[b][0], rows[a][0]) })
	for _, i := range top[:keep] {
		out[i][p], out[i][p+2] = 4, g
	}
	return out
}

// TestRegOrdinal_ReplicateLevelLoss: every replicate keeps the full
// fit's levels (D5). A bootstrap replicate whose draw lost the rare
// fourth level is skipped (bootstrap_succeeded < bootstrap_requested);
// a jackknife replicate that loses it — the leave-one-out
// deletion of its only row, or the deleted cluster holding every row of
// it — fails the slot alone (PULSE_SLOT_FAILED,
// PROCESSING_REGRESSION_INSUFFICIENT_DATA, reason replicate_empty_level)
// with predict accepting the request; Strict escalates it.
func TestRegOrdinal_ReplicateLevelLoss(t *testing.T) {
	t.Parallel()
	jk := regordinal.LoadModifiers(t).Jackknife
	np := len(jk.Predictors)
	p := newInstance(t, nil)
	strict := newInstance(t, func(o *pulse.Options) { o.Strict = true })
	for _, inst := range []*pulse.Pulse{p, strict} {
		writeCohort(t, inst, "rare.pulse", cohortSpec{p: np}, rareLevelRows(jk.Rows, np, 3, 9), nil)
		writeCohort(t, inst, "one.pulse", cohortSpec{p: np}, rareLevelRows(jk.Rows, np, 1, 9), nil)
	}
	ctx := context.Background()

	t.Run("bootstrap_skips", func(t *testing.T) {
		t.Parallel()
		res := fitOne(t, p, "rare.pulse", modSpec(t, jk.Predictors, `,"resample":"bootstrap","bootstrap_iters":60,"rng_seed":5`))
		if res.BootstrapRequested != 60 || res.BootstrapSucceeded >= res.BootstrapRequested || res.BootstrapSucceeded < 10 {
			t.Fatalf("bootstrap %d requested, %d succeeded: want some skipped", res.BootstrapRequested, res.BootstrapSucceeded)
		}
		b := mustJSON(t, res)
		if want := fmt.Sprintf(`"bootstrap_succeeded":%d`, res.BootstrapSucceeded); !strings.Contains(b, want) {
			t.Errorf("wire form lacks %s: %s", want, b)
		}
		if !slices.Equal(anyStrings(res.Outputs["levels"]), []string{"1", "2", "3", "4"}) {
			t.Errorf("levels %v", res.Outputs["levels"])
		}
	})

	for name, tc := range map[string]struct {
		cohort, extra string
		group         bool
	}{
		"leave_one_out": {"one.pulse", `,"resample":"jackknife"`, false},
		"by_cluster":    {"rare.pulse", `,"resample":"jackknife","jackknife_by":"g"`, true},
	} {
		t.Run("jackknife_"+name, func(t *testing.T) {
			t.Parallel()
			r := &types.Request{Cohort: &types.Cohort{Filename: tc.cohort}, Regressions: []*types.RegressionSpec{modSpec(t, jk.Predictors, tc.extra)}}
			if pr, err := p.Predict(ctx, r); err != nil || len(pr.Errors) != 0 {
				t.Fatalf("predict refused: %v %+v", err, pr.Errors)
			}
			resp, err := p.Process(ctx, r)
			if err != nil {
				t.Fatalf("Process: %v (the slot should fail alone)", err)
			}
			res := resp.Regressions[0]
			if res.Error == nil || res.Error.Code != string(perr.PROCESSING_REGRESSION_INSUFFICIENT_DATA) ||
				res.Error.Details["reason"] != regordinal.ReasonReplicateEmptyLevel || res.Error.Details["level"] != "4" {
				t.Fatalf("slot error %s, want INSUFFICIENT_DATA reason %s level 4", mustJSON(t, res.Error), regordinal.ReasonReplicateEmptyLevel)
			}
			if _, ok := res.Error.Details["jackknife_group"]; ok != tc.group {
				t.Errorf("details.jackknife_group present %v, want %v (%v)", ok, tc.group, res.Error.Details)
			}
			if len(resp.Warnings) == 0 || resp.Warnings[0].Code != string(perr.PULSE_SLOT_FAILED) {
				t.Errorf("warnings %s, want PULSE_SLOT_FAILED", mustJSON(t, resp.Warnings))
			}
			_, serr := strict.Process(ctx, r)
			var ce *perr.CodedError
			if !asCoded(serr, &ce) || ce.Code != perr.PROCESSING_REGRESSION_INSUFFICIENT_DATA || ce.Details["reason"] != regordinal.ReasonReplicateEmptyLevel {
				t.Errorf("Strict Process: %v, want the slot's own INSUFFICIENT_DATA (%s)", serr, regordinal.ReasonReplicateEmptyLevel)
			}
		})
	}
}
