package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	stderrors "errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// credit_workbench_e2e_test.go is the model-workbench (U49) end-to-end
// acceptance (E3-S4) on a committed credit-shaped loan book
// (internal/processing/testdata/reference/credit/loans.csv): a train /
// holdout split by vintage, a fit_on target encoding, a ridge binomial
// REG_GLM with delete-a-vintage jackknife standard errors, compose
// `fits` + ATTR_REG_PREDICT holdout scoring, AGG_AUC with its DeLong
// interval, a GROUP_NUMERIC_RANGES train-vs-holdout table and the
// holdout decile gains table (GROUP_QUANTILE D1…D10 with WIN_SHARE /
// WIN_LIFT). Every expected figure is R's (scripts/reference/
// gen_credit_workbench.R → credit_workbench.json: glmnet + pROC); none
// is transcribed here. Out-of-fold folds are not on this path: R cannot
// replay Go's fold deal — TestTargetEncodeCredit_FitOnFoldsGLMHoldoutAUC
// pins fit_on + folds through the same fit / score / AUC chain.

const creditRefDir = "internal/processing/testdata/reference"

// creditRef is credit_workbench.json.
type creditRef struct {
	FixtureSHA256   string             `json:"fixture_sha256"`
	Rows            int                `json:"rows"`
	TrainVintageMax int                `json:"train_vintage_max"`
	Smoothing       float64            `json:"smoothing"`
	Alpha           float64            `json:"alpha"`
	Predictors      []string           `json:"predictors"`
	TEPurpose       map[string]float64 `json:"te_purpose"`
	Coefficients    []float64          `json:"coefficients"`
	JackknifeGroups int                `json:"jackknife_groups"`
	StdErrors       []float64          `json:"std_errors"`
	HoldoutAUC      creditAUC          `json:"holdout_auc"`
	TrainAUC        creditAUC          `json:"train_auc"`
	Deciles         []creditDecile     `json:"deciles"`
}

type creditAUC struct {
	N       int     `json:"n"`
	NPos    int     `json:"n_pos"`
	NNeg    int     `json:"n_neg"`
	AUC     float64 `json:"auc"`
	SE      float64 `json:"se"`
	CILower float64 `json:"ci_lower"`
	CIUpper float64 `json:"ci_upper"`
}

type creditDecile struct {
	Key             string  `json:"key"`
	Loans           int     `json:"loans"`
	Defaults        int     `json:"defaults"`
	MeanPD          float64 `json:"mean_pd"`
	DefaultShare    float64 `json:"default_share"`
	Lift            float64 `json:"lift"`
	CumDefaultShare float64 `json:"cum_default_share"`
	CumLift         float64 `json:"cum_lift"`
}

func loadCreditRef(t *testing.T) creditRef {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(creditRefDir, "credit_workbench.json"))
	if err != nil {
		t.Fatalf("read credit_workbench.json: %v (regenerate with `make reference`)", err)
	}
	s := string(raw)
	if i := strings.LastIndex(s, "\n// golden-hash: "); i >= 0 {
		s = s[:i]
	}
	var ref creditRef
	if err := json.Unmarshal([]byte(s), &ref); err != nil {
		t.Fatalf("decode credit_workbench.json: %v", err)
	}
	if len(ref.Coefficients) != len(ref.Predictors)+1 || len(ref.Deciles) != 10 {
		t.Fatalf("credit_workbench.json is malformed: %+v", ref)
	}
	return ref
}

// creditPulse imports the committed fixture into a MemMap filesystem
// (after checking it is the CSV the oracle computed on) and returns an
// instance over it plus the cohort path.
func creditPulse(t *testing.T, ref creditRef, strict bool) (*pulse.Pulse, string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(creditRefDir, "credit", "loans.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256(raw); hex.EncodeToString(sum[:]) != ref.FixtureSHA256 {
		t.Fatalf("credit/loans.csv drifted from its oracle (sha256 %x, want %s); regenerate with `make reference`", sum, ref.FixtureSHA256)
	}
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "loans.csv", raw, 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.ImportFile(context.Background(), pulse.ImportSpec{SourcePath: "loans.csv"})
	if err != nil {
		t.Fatal(err)
	}
	if strict {
		if p, err = pulse.New(pulse.Options{FS: fs, Strict: true}); err != nil {
			t.Fatal(err)
		}
	}
	return p, res.Path
}

// creditJSON substitutes the cohort path and the oracle's knobs into a
// request body.
func creditJSON(t *testing.T, ref creditRef, cohort, body string, out any) {
	t.Helper()
	pred, _ := json.Marshal(ref.Predictors)
	r := strings.NewReplacer(
		"$COHORT", cohort,
		"$TRAIN", "vintage <= 18",
		"$HOLDOUT", "vintage > 18",
		"$PREDICTORS", string(pred),
		"$TE", `{"type": "FEAT_TARGET_ENCODE", "field": "purpose", "label": "te_purpose", "params": {"target": "defaulted", "smoothing": 5, "fit_on": "vintage <= 18"}}`,
	)
	if ref.TrainVintageMax != 18 || ref.Smoothing != 5 || ref.Alpha != 0.002 {
		t.Fatalf("oracle knobs moved (train ≤ %d, smoothing %v, alpha %v): update the request bodies", ref.TrainVintageMax, ref.Smoothing, ref.Alpha)
	}
	if err := json.Unmarshal([]byte(r.Replace(body)), out); err != nil {
		t.Fatalf("decode request: %v", err)
	}
}

const creditWorkbenchBatch = `{
  "fits": [{"name": "pd", "request": {
    "cohort": {"filename": "$COHORT"},
    "features": [$TE],
    "filterers": [{"type": "FILTER_EXPRESSION", "expression": "$TRAIN"}],
    "regressions": [{"name": "pd", "type": "REG_GLM", "family": "binomial", "target": "defaulted",
      "predictors": $PREDICTORS, "penalty": "l2", "alpha": 0.002,
      "resample": "jackknife", "jackknife_by": "vintage"}]
  }}],
  "requests": [
    {"label": "holdout", "cohort": {"filename": "$COHORT"},
     "features": [$TE],
     "filterers": [{"type": "FILTER_EXPRESSION", "expression": "$HOLDOUT"}],
     "attributes": [{"type": "ATTR_REG_PREDICT", "label": "pd_score", "params": {"fit": "pd"}}],
     "aggregations": [{"type": "AGG_AUC", "field": "pd_score", "label": "auc", "params": {"outcome": "defaulted"}}]},
    {"label": "train_vs_holdout", "cohort": {"filename": "$COHORT"},
     "features": [$TE],
     "attributes": [{"type": "ATTR_REG_PREDICT", "label": "pd_score", "params": {"fit": "pd"}}],
     "groups": [{"type": "GROUP_NUMERIC_RANGES", "field": "vintage",
       "params": {"ranges": [{"label": "train", "end": 19}, {"label": "holdout", "start": 19}]}}],
     "aggregations": [{"type": "AGG_AUC", "field": "pd_score", "label": "auc", "params": {"outcome": "defaulted"}}]},
    {"label": "deciles", "cohort": {"filename": "$COHORT"},
     "features": [$TE],
     "filterers": [{"type": "FILTER_EXPRESSION", "expression": "$HOLDOUT"}],
     "attributes": [{"type": "ATTR_REG_PREDICT", "label": "pd_score", "params": {"fit": "pd"}}],
     "groups": [{"type": "GROUP_QUANTILE", "field": "pd_score", "interval": 10}],
     "aggregations": [
       {"type": "AGG_COUNT", "field": "id", "label": "loans"},
       {"type": "AGG_SUM", "field": "defaulted", "label": "defaults"},
       {"type": "AGG_AVERAGE", "field": "pd_score", "label": "mean_pd"}],
     "windows": [
       {"type": "WIN_SHARE", "field": "defaults", "label": "default_share"},
       {"type": "WIN_LIFT", "field": "defaults", "label": "lift", "params": {"base": "loans"}},
       {"type": "WIN_SHARE", "field": "defaults", "label": "cum_default_share",
        "order_by": [{"field": "mean_pd", "desc": true}], "params": {"cumulative": true}},
       {"type": "WIN_LIFT", "field": "defaults", "label": "cum_lift",
        "order_by": [{"field": "mean_pd", "desc": true}], "params": {"base": "loans", "cumulative": true}}]}
  ]
}`

func creditRelNear(got, want, tol float64) bool {
	if want == 0 {
		return math.Abs(got) <= tol
	}
	return math.Abs(got-want) <= tol*math.Abs(want)
}

func creditFloat(t *testing.T, row map[string]any, key string) float64 {
	t.Helper()
	switch v := row[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	t.Fatalf("row %v: %s is %T, want a number", row, key, row[key])
	return 0
}

// assertCreditAUC compares one AGG_AUC result (its row value and its
// components) with the pROC reference.
func assertCreditAUC(t *testing.T, where string, auc float64, comp map[string]any, want creditAUC) {
	t.Helper()
	if !creditRelNear(auc, want.AUC, 1e-12) {
		t.Errorf("%s: auc %.15g, pROC %.15g", where, auc, want.AUC)
	}
	for key, w := range map[string]float64{"auc": want.AUC, "se": want.SE, "ci_lower": want.CILower, "ci_upper": want.CIUpper} {
		got, ok := comp[key].(float64)
		if !ok || !creditRelNear(got, w, 1e-9) {
			t.Errorf("%s: components %s = %v, pROC %.15g", where, key, comp[key], w)
		}
	}
	for key, w := range map[string]int{"n_pos": want.NPos, "n_neg": want.NNeg} {
		if got, ok := comp[key].(int); !ok || got != w {
			t.Errorf("%s: components %s = %v (%T), want %d", where, key, comp[key], comp[key], w)
		}
	}
}

func aucComponents(t *testing.T, resp *types.Response, label string) map[string]any {
	t.Helper()
	if resp.Components == nil {
		t.Fatalf("response %q carries no components", resp.Label)
	}
	for _, a := range resp.Components.Aggregations {
		if a.Label == label {
			return a.Operator
		}
	}
	t.Fatalf("response %q: no components for %s", resp.Label, label)
	return nil
}

// TestCreditWorkbench_EndToEnd is the U49 acceptance: every figure of the
// credit pipeline against R.
func TestCreditWorkbench_EndToEnd(t *testing.T) {
	t.Parallel()

	ref := loadCreditRef(t)
	p, cohort := creditPulse(t, ref, true) // strict: the fit_on encoder raises no leakage refusal
	var batch types.ComposedRequest
	creditJSON(t, ref, cohort, creditWorkbenchBatch, &batch)
	out, err := p.Compose(context.Background(), &batch)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(out.Warnings) != 0 {
		t.Errorf("batch warnings %+v", out.Warnings)
	}

	// The fit: glmnet's ridge coefficients and the delete-a-vintage
	// jackknife standard errors.
	fit := out.Fits["pd"]
	if fit == nil || fit.RegressionResult == nil || fit.Error != nil {
		t.Fatalf("fit pd = %+v", fit)
	}
	reg := fit.RegressionResult
	keys := append([]string{"(intercept)"}, ref.Predictors...)
	for j, k := range keys {
		if !creditRelNear(reg.Coefficients[k], ref.Coefficients[j], 1e-7) {
			t.Errorf("coef %s = %.12g, glmnet %.12g", k, reg.Coefficients[k], ref.Coefficients[j])
		}
		if !creditRelNear(reg.StdErrors[k], ref.StdErrors[j], 1e-6) {
			t.Errorf("jackknife se %s = %.12g, R %.12g", k, reg.StdErrors[k], ref.StdErrors[j])
		}
	}
	if reg.JackknifeGroups != ref.JackknifeGroups || reg.Resample != "jackknife" || reg.Penalty != "l2" {
		t.Errorf("fit echo: jackknife_groups %d (want %d), resample %q, penalty %q", reg.JackknifeGroups, ref.JackknifeGroups, reg.Resample, reg.Penalty)
	}

	byLabel := map[string]*types.Response{}
	for _, r := range out.Responses {
		byLabel[r.Label] = r
	}

	// Holdout AUC through ATTR_REG_PREDICT → AGG_AUC, against pROC.
	hold := byLabel["holdout"]
	if hold == nil || len(hold.Data) != 1 {
		t.Fatalf("holdout response = %+v", hold)
	}
	assertCreditAUC(t, "holdout", creditFloat(t, hold.Data[0], "auc"), aucComponents(t, hold, "auc"), ref.HoldoutAUC)

	// GROUP_NUMERIC_RANGES: train then holdout, in supplied order, each
	// AUC pROC's (the holdout bucket's equals the holdout slot's).
	tvh := byLabel["train_vs_holdout"]
	if tvh == nil || len(tvh.Data) != 2 {
		t.Fatalf("train_vs_holdout response = %+v", tvh)
	}
	for i, w := range []struct {
		key string
		ref creditAUC
	}{{"train", ref.TrainAUC}, {"holdout", ref.HoldoutAUC}} {
		row := tvh.Data[i]
		if row["vintage"] != w.key {
			t.Errorf("train_vs_holdout row %d key %v, want %s", i, row["vintage"], w.key)
		}
		if got := creditFloat(t, row, "auc"); !creditRelNear(got, w.ref.AUC, 1e-12) {
			t.Errorf("%s auc %.15g, pROC %.15g", w.key, got, w.ref.AUC)
		}
	}

	// The decile gains table: D1…D10 in bucket order (never D1, D10,
	// D2), each figure R's.
	dec := byLabel["deciles"]
	if dec == nil || len(dec.Data) != 10 {
		t.Fatalf("deciles response = %+v", dec)
	}
	for i, w := range ref.Deciles {
		row := dec.Data[i]
		if row["pd_score"] != w.Key {
			t.Errorf("decile row %d key %v, want %s (bucket order)", i, row["pd_score"], w.Key)
		}
		if got := creditFloat(t, row, "loans"); got != float64(w.Loans) {
			t.Errorf("%s loans %v, R %d", w.Key, got, w.Loans)
		}
		if got := creditFloat(t, row, "defaults"); got != float64(w.Defaults) {
			t.Errorf("%s defaults %v, R %d", w.Key, got, w.Defaults)
		}
		for key, want := range map[string]float64{"mean_pd": w.MeanPD, "default_share": w.DefaultShare, "lift": w.Lift,
			"cum_default_share": w.CumDefaultShare, "cum_lift": w.CumLift} {
			tol := 1e-12
			if key == "mean_pd" {
				tol = 1e-7 // the scores carry the fit's 1e-7 agreement with glmnet
			}
			if got := creditFloat(t, row, key); !creditRelNear(got, want, tol) {
				t.Errorf("%s %s %.15g, R %.15g", w.Key, key, got, want)
			}
		}
	}
}

// creditSeparationBody is a Process request over the training rows with
// a good ridge GLM beside a GLM on chargeoff_amt — a post-outcome leak
// that separates the outcome completely.
const creditSeparationBody = `{
  "cohort": {"filename": "$COHORT"},
  "features": [$TE],
  "filterers": [{"type": "FILTER_EXPRESSION", "expression": "$TRAIN"}],
  "regressions": [
    {"name": "pd", "type": "REG_GLM", "family": "binomial", "target": "defaulted",
     "predictors": $PREDICTORS, "penalty": "l2", "alpha": 0.002},
    {"name": "leaky", "type": "REG_GLM", "family": "binomial", "target": "defaulted",
     "predictors": ["chargeoff_amt", "income"]}
  ]
}`

const creditSeparationBatch = `{
  "fits": [
    {"name": "pd", "request": {"cohort": {"filename": "$COHORT"}, "features": [$TE],
      "filterers": [{"type": "FILTER_EXPRESSION", "expression": "$TRAIN"}],
      "regressions": [{"name": "pd", "type": "REG_GLM", "family": "binomial", "target": "defaulted",
        "predictors": $PREDICTORS, "penalty": "l2", "alpha": 0.002}]}},
    {"name": "leaky", "request": {"cohort": {"filename": "$COHORT"},
      "filterers": [{"type": "FILTER_EXPRESSION", "expression": "$TRAIN"}],
      "regressions": [{"name": "leaky", "type": "REG_GLM", "family": "binomial", "target": "defaulted",
        "predictors": ["chargeoff_amt", "income"]}]}}
  ],
  "requests": [
    {"label": "holdout", "cohort": {"filename": "$COHORT"}, "features": [$TE],
     "filterers": [{"type": "FILTER_EXPRESSION", "expression": "$HOLDOUT"}],
     "attributes": [
       {"type": "ATTR_REG_PREDICT", "label": "pd_score", "params": {"fit": "pd"}},
       {"type": "ATTR_REG_PREDICT", "label": "leaky_score", "params": {"fit": "leaky"}}],
     "aggregations": [
       {"type": "AGG_AUC", "field": "pd_score", "label": "auc", "params": {"outcome": "defaulted"}},
       {"type": "AGG_COUNT", "field": "leaky_score", "label": "leaky_n"}]}
  ]
}`

func slotFailed(ws []*types.ResponseWarning, key string, want any) *types.ResponseWarning {
	for _, w := range ws {
		if w.Code == string(perr.PULSE_SLOT_FAILED) && w.Details[key] == want {
			return w
		}
	}
	return nil
}

// TestCreditWorkbench_SeparatedSlotFailsAlone: on the credit book a
// separated regression slot fails alone beside a good one — Process and
// a Compose fit — with PROCESSING_REGRESSION_SEPARATION, the good slot's
// figures untouched; Strict keeps fail-fast with the slot's own code.
func TestCreditWorkbench_SeparatedSlotFailsAlone(t *testing.T) {
	t.Parallel()

	ref := loadCreditRef(t)
	ctx := context.Background()
	p, cohort := creditPulse(t, ref, false)
	strict, _ := creditPulse(t, ref, true)

	// Process: the good fit equals the same fit run alone.
	var req types.Request
	creditJSON(t, ref, cohort, creditSeparationBody, &req)
	resp, err := p.Process(ctx, &req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Regressions) != 2 {
		t.Fatalf("regressions = %+v", resp.Regressions)
	}
	good, bad := resp.Regressions[0], resp.Regressions[1]
	if good.Error != nil || len(good.Coefficients) == 0 {
		t.Fatalf("good slot = %+v", good)
	}
	if bad.Error == nil || bad.Error.Code != string(perr.PROCESSING_REGRESSION_SEPARATION) || len(bad.Coefficients) != 0 || bad.Name != "leaky" {
		t.Fatalf("separated slot = %+v (error %+v)", bad, bad.Error)
	}
	if w := slotFailed(resp.Warnings, "index", 1); w == nil || w.Details["code"] != string(perr.PROCESSING_REGRESSION_SEPARATION) {
		t.Errorf("no PULSE_SLOT_FAILED for regressions[1] in %+v", resp.Warnings)
	}
	var alone types.Request
	creditJSON(t, ref, cohort, creditSeparationBody, &alone)
	alone.Regressions = alone.Regressions[:1]
	aloneResp, err := p.Process(ctx, &alone)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(aloneResp.Regressions[0])
	b, _ := json.Marshal(good)
	if string(a) != string(b) {
		t.Errorf("the failed slot moved the good one:\n%s\n%s", a, b)
	}

	// Strict: the whole call fails with the separated slot's code.
	var sreq types.Request
	creditJSON(t, ref, cohort, creditSeparationBody, &sreq)
	_, err = strict.Process(ctx, &sreq)
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PROCESSING_REGRESSION_SEPARATION || ce.Details["slot_kind"] == nil || ce.Details["index"] != 1 {
		t.Fatalf("strict Process: %v (details %v), want %s naming regressions[1]", err, detailsOf(ce), perr.PROCESSING_REGRESSION_SEPARATION)
	}

	// Compose: the leaky fit fails alone; its scores are null on every
	// holdout row while the good fit's AUC is the end-to-end one.
	var batch types.ComposedRequest
	creditJSON(t, ref, cohort, creditSeparationBatch, &batch)
	out, err := p.Compose(ctx, &batch)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if lf := out.Fits["leaky"]; lf == nil || lf.Error == nil || lf.Error.Code != string(perr.PROCESSING_REGRESSION_SEPARATION) {
		t.Fatalf("fit leaky = %+v", lf)
	}
	if pf := out.Fits["pd"]; pf == nil || pf.Error != nil || pf.RegressionResult == nil {
		t.Fatalf("fit pd = %+v", pf)
	}
	if w := slotFailed(out.Warnings, "fit", 1); w == nil {
		t.Errorf("no batch PULSE_SLOT_FAILED for fits[1] in %+v", out.Warnings)
	}
	hold := out.Responses[0]
	if got := creditFloat(t, hold.Data[0], "leaky_n"); got != 0 {
		t.Errorf("leaky_score counted %v present rows, want 0 (all null)", got)
	}
	unavailable := false
	for _, w := range hold.Warnings {
		if w.Code == string(perr.PULSE_FIT_UNAVAILABLE) && w.Details["fit"] == "leaky" {
			unavailable = true
		}
	}
	if !unavailable {
		t.Errorf("holdout warnings %+v carry no PULSE_FIT_UNAVAILABLE for leaky", hold.Warnings)
	}
	assertCreditAUC(t, "holdout beside a failed fit", creditFloat(t, hold.Data[0], "auc"), aucComponents(t, hold, "auc"), ref.HoldoutAUC)

	var sbatch types.ComposedRequest
	creditJSON(t, ref, cohort, creditSeparationBatch, &sbatch)
	if _, err := strict.Compose(ctx, &sbatch); !stderrors.As(err, &ce) || ce.Code != perr.PROCESSING_REGRESSION_SEPARATION {
		t.Fatalf("strict Compose: %v, want %s", err, perr.PROCESSING_REGRESSION_SEPARATION)
	}
}
