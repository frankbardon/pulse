package pulse

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"testing"

	"github.com/spf13/afero"

	perr "github.com/frankbardon/pulse/errors"
)

// target_encode_credit_test.go pins the composition the credit-risk
// acceptance (model-workbench E3-S4) runs on FEAT_TARGET_ENCODE's
// params.fit_on / folds (E3-S3): a train/test split, a per-purpose
// encoding fit on split 0 out-of-fold, a penalised binomial REG_GLM fit
// on the training rows, holdout scoring through ATTR_REG_PREDICT and
// AGG_AUC — and that the holdout's outcomes reach neither the fit nor
// the score.

const creditCohort = "credit_te.pulse"

// creditFS writes 300 loans: purpose (5 levels, each with its own
// default rate), income and a 0/1 default. bump perturbs the outcome of
// every row whose index it admits.
func creditFS(t *testing.T, flip func(i int) bool) afero.Fs {
	t.Helper()
	purposes := []string{"car", "home", "edu", "biz", "misc"}
	rows := make([][]string, 300)
	for i := range rows {
		k := i % 5
		// Per-purpose default rate 5%..85%; income adds a little.
		income := 20 + float64((i*53)%61)
		u := (i*7919 + 13) % 100
		d := 0
		if u < 5+20*k-int(income-50)/10 {
			d = 1
		}
		if flip != nil && flip(i) {
			d = 1 - d
		}
		rows[i] = []string{purposes[k], strconv.FormatFloat(income, 'f', -1, 64), strconv.Itoa(d), strconv.Itoa(i)}
	}
	fsys := afero.NewMemMapFs()
	createTestPulseFile(t, fsys, creditCohort, []string{"purpose", "income", "dflt", "id"}, rows)
	return fsys
}

// creditCompose is the E3-S4 shape: one fit on the training rows, one
// holdout slot scoring it.
const creditCompose = `{
  "fits": [{"name": "pd", "request": {
    "cohort": {"filename": "credit_te.pulse"},
    "features": [
      {"type": "FEAT_TRAIN_TEST_SPLIT", "label": "split", "params": {"ratios": [0.7, 0.3], "seed": 11}},
      {"type": "FEAT_TARGET_ENCODE", "field": "purpose", "label": "te_purpose",
       "params": {"target": "dflt", "fit_on": "split == 0", "folds": 5, "rng_seed": 7, "smoothing": 2}}
    ],
    "filterers": [{"type": "FILTER_EXPRESSION", "expression": "split == 0"}],
    "regressions": [{"type": "REG_GLM", "family": "binomial", "target": "dflt",
      "predictors": ["te_purpose", "income"], "penalty": "l2", "alpha": 0.01}]
  }}],
  "requests": [{
    "label": "holdout",
    "cohort": {"filename": "credit_te.pulse"},
    "features": [
      {"type": "FEAT_TRAIN_TEST_SPLIT", "label": "split", "params": {"ratios": [0.7, 0.3], "seed": 11}},
      {"type": "FEAT_TARGET_ENCODE", "field": "purpose", "label": "te_purpose",
       "params": {"target": "dflt", "fit_on": "split == 0", "folds": 5, "rng_seed": 7, "smoothing": 2}}
    ],
    "filterers": [{"type": "FILTER_EXPRESSION", "expression": "split != 0"}],
    "attributes": [{"type": "ATTR_REG_PREDICT", "label": "score", "params": {"fit": "pd"}}],
    "aggregations": [
      {"type": "AGG_AUC", "field": "score", "label": "auc", "params": {"outcome": "dflt"}},
      {"type": "AGG_COUNT", "field": "score", "label": "n"}
    ]
  }]
}`

func runCredit(t *testing.T, fsys afero.Fs) (*ComposedResponse, []string) {
	return runCreditWith(t, fsys, 0)
}

func runCreditWith(t *testing.T, fsys afero.Fs, decodeWorkers int) (*ComposedResponse, []string) {
	t.Helper()
	p, err := New(Options{FS: fsys, Strict: true, DecodeWorkers: decodeWorkers})
	if err != nil {
		t.Fatal(err)
	}
	var req ComposedRequest
	if err := json.Unmarshal([]byte(creditCompose), &req); err != nil {
		t.Fatal(err)
	}
	out, err := p.Compose(context.Background(), &req)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	// Strict refuses a leaking encoder: getting here means fit_on
	// silenced PULSE_FEAT_TARGET_LEAKAGE_RISK in the fit and the slot.
	var codes []string
	for _, w := range out.Warnings {
		codes = append(codes, w.Code)
	}
	for _, r := range out.Responses {
		for _, w := range r.Warnings {
			codes = append(codes, w.Code)
		}
	}
	return out, codes
}

func TestTargetEncodeCredit_FitOnFoldsGLMHoldoutAUC(t *testing.T) {
	t.Parallel()

	base, codes := runCredit(t, creditFS(t, nil))
	for _, c := range codes {
		if c == string(perr.PULSE_FEAT_TARGET_LEAKAGE_RISK) {
			t.Fatalf("fit_on encoder still warns: %v", codes)
		}
	}
	fit := base.Fits["pd"]
	if fit == nil || fit.RegressionResult == nil || fit.Error != nil {
		t.Fatalf("fit pd = %+v", fit)
	}
	row := base.Responses[0].Data[0]
	auc, ok := row["auc"].(float64)
	if !ok || math.IsNaN(auc) || auc <= 0.5 || auc > 1 {
		t.Fatalf("holdout row %v: want an AUC above chance", row)
	}

	// Flip every HOLDOUT outcome: the split does not read the outcome,
	// so the same rows are holdout; the fit (training rows, encodings
	// fit on them) must not move a bit — the holdout's AUC does.
	splitOf := holdoutRows(t, creditFS(t, nil))
	flipped, _ := runCredit(t, creditFS(t, func(i int) bool { return splitOf[i] }))
	a, _ := json.Marshal(fit)
	b, _ := json.Marshal(flipped.Fits["pd"])
	if string(a) != string(b) {
		t.Errorf("holdout outcomes moved the fit:\n%s\n%s", a, b)
	}
	if frow := flipped.Responses[0].Data[0]; frow["auc"] == row["auc"] {
		t.Errorf("flipping every holdout outcome left the holdout AUC at %v", row["auc"])
	}

	// Bit-identical across decode workers: the fold deal follows the
	// record order, which the buffered decode keeps.
	for _, w := range []int{1, 3} {
		other, _ := runCreditWith(t, creditFS(t, nil), w)
		x, _ := json.Marshal(other.Fits["pd"])
		y, _ := json.Marshal(other.Responses[0].Data)
		z, _ := json.Marshal(base.Responses[0].Data)
		if string(x) != string(a) || string(y) != string(z) {
			t.Errorf("DecodeWorkers=%d changed the result", w)
		}
	}
}

// holdoutRows reports, per row index, whether the split puts it outside
// split 0, read from the engine's own split column.
func holdoutRows(t *testing.T, fsys afero.Fs) map[int]bool {
	t.Helper()
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	var req Request
	body := `{"cohort":{"filename":"credit_te.pulse"},
	  "features":[{"type":"FEAT_TRAIN_TEST_SPLIT","label":"split","params":{"ratios":[0.7,0.3],"seed":11}}],
	  "windows":[{"type":"WIN_ROW_NUMBER","label":"rn","order_by":[{"field":"id"}]}]}`
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	resp, err := p.Process(context.Background(), &req)
	if err != nil {
		t.Fatal(err)
	}
	out := map[int]bool{}
	for _, r := range resp.Data {
		id, ok := r["id"].(float64)
		if !ok {
			t.Fatalf("row %v carries no id", r)
		}
		if s, _ := r["split"].(float64); s != 0 {
			out[int(id)] = true
		}
	}
	if len(out) == 0 {
		t.Fatal("no holdout rows")
	}
	return out
}
