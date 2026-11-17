package pulse

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// multinomial_test.go covers REG_MULTINOMIAL (U65 E4-S2) end to end:
// every refusal is the same coded error at predict and at run
// (internal/regmultinom and the shared rules), the fit answers bit for
// bit the same under every DecodeWorkers / ShardWorkers count, a
// multiplicity block corrects every non-intercept coefficient (and
// predict counts them), and a data-dependent refusal fails the slot
// alone. The figures themselves are pinned to R's nnet::multinom by
// internal/processing/regression/multinomial_oracle_test.go.

const (
	mnCohort  = "multinomial.pulse"
	mnRowsN   = 150
	mnPerShrd = 50
)

var mnColumns = []string{"id", "x1", "x2", "y", "cls", "f"}

// mnRow is row i: two predictors, a numeric y and a three-level class
// cls from a noisy latent score, an integer weight f. The first rows
// carry mid, lo, hi in that order, so the dictionary order is not the
// alphabet's. Products are barriered so every GOARCH builds the same
// rows.
func mnRow(i int) []string {
	fi := float64(i)
	x1 := float64(fi*0.03) - 2
	x2 := math.Sin(float64(0.41*fi) + 0.3)
	s := float64(0.9*x1) - float64(0.7*x2) + float64(1.3*math.Sin(float64(1.7*fi)))
	cls := "lo"
	switch {
	case i == 0:
		cls = "mid"
	case i == 1:
		cls = "lo"
	case i == 2:
		cls = "hi"
	case s > 0.5:
		cls = "hi"
	case s > -0.6:
		cls = "mid"
	}
	return []string{strconv.Itoa(i), jsonNumber(x1), jsonNumber(x2), jsonNumber(1 + x1), cls, strconv.Itoa(1 + i%3)}
}

// mnModes is the cohort as one file (serial decode and 4 decode
// workers) and as a 3-shard archive (1 and 3 shard workers).
func mnModes(t *testing.T) (single, sharded []scorerParityMode) {
	t.Helper()
	var rows [][]string
	for i := 0; i < mnRowsN; i++ {
		rows = append(rows, mnRow(i))
	}
	fsys := afero.NewMemMapFs()
	createTestPulseFile(t, fsys, mnCohort, mnColumns, rows)
	sfs := afero.NewMemMapFs()
	var shards []string
	for s := 0; s*mnPerShrd < mnRowsN; s++ {
		path := "s" + strconv.Itoa(s) + ".pulse"
		createTestPulseFile(t, sfs, path, mnColumns, rows[s*mnPerShrd:(s+1)*mnPerShrd])
		shards = append(shards, path)
	}
	mk := func(fs afero.Fs, o Options) *Pulse {
		o.FS = fs
		p, err := New(o)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	single = []scorerParityMode{
		{"serial", mk(fsys, Options{DecodeWorkers: 1}), mnCohort},
		{"decode_workers_4", mk(fsys, Options{DecodeWorkers: 4}), mnCohort},
	}
	sp := mk(sfs, Options{ShardWorkers: 1})
	if _, err := sp.CreateShardArchive(context.Background(), "archive.pulse", shards); err != nil {
		t.Fatal(err)
	}
	sharded = []scorerParityMode{
		{"shard_workers_1", sp, "archive.pulse"},
		{"shard_workers_3", mk(sfs, Options{ShardWorkers: 3}), "archive.pulse"},
	}
	return single, sharded
}

func mnSpec(mut func(*types.RegressionSpec)) *types.RegressionSpec {
	s := &types.RegressionSpec{Type: types.REG_MULTINOMIAL, Name: "m", Target: "cls", Predictors: []string{"x1", "x2"}}
	if mut != nil {
		mut(s)
	}
	return s
}

func mnReq(cohort string, specs ...*types.RegressionSpec) *Request {
	return &Request{Cohort: &types.Cohort{Filename: cohort}, Regressions: specs}
}

// TestMultinomial_WorkerInvariant: the fits — unweighted with vcov,
// frequency- and probability-weighted, a named reference, under the
// resample and selection modifiers, penalised — are
// byte-identical across decode workers (one file) and shard workers
// (the archive), and their categories follow the dictionary order.
func TestMultinomial_WorkerInvariant(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	specs := []*types.RegressionSpec{
		mnSpec(func(s *types.RegressionSpec) { s.Vcov = true }),
		mnSpec(func(s *types.RegressionSpec) {
			s.Name, s.Reference = "m_freq", "hi"
			s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "f", Kind: types.WeightKindFrequency})
		}),
		mnSpec(func(s *types.RegressionSpec) {
			s.Name = "m_prob"
			s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "f", Kind: types.WeightKindProbability})
		}),
		// A holdout's proper scores fold every held-out row's whole
		// probability vector (U68 E1-S3), weighted.
		mnSpec(func(s *types.RegressionSpec) {
			s.Name, s.Holdout = "m_hold", "id % 4 == 0"
			s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "f", Kind: types.WeightKindFrequency})
		}),
		// The modifiers (U68 E2-S2): a weighted delete-a-cluster
		// jackknife, a bootstrap at the derived seed (rng_seed 0) and a
		// stepwise selection resampled by a seeded grouped jackknife.
		mnSpec(func(s *types.RegressionSpec) {
			s.Name, s.Resample, s.JackknifeBy, s.Vcov = "m_jk", "jackknife", "f", true
			s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "f", Kind: types.WeightKindFrequency})
		}),
		mnSpec(func(s *types.RegressionSpec) { s.Name, s.Resample, s.BootstrapIters = "m_boot", "bootstrap", 25 }),
		mnSpec(func(s *types.RegressionSpec) {
			s.Name, s.Selection, s.Criterion, s.Resample, s.JackknifeGroups, s.RNGSeed = "m_sel", "stepwise", "aic", "jackknife", 6, 4
		}),
		// The penalty (U68 E2-S3): a weighted elastic net with an
		// unpenalised predictor, and a resampled lasso.
		mnSpec(func(s *types.RegressionSpec) {
			s.Name, s.Penalty, s.Alpha, s.L1Ratio, s.Unpenalized = "m_enet", "elasticnet", 0.02, 0.4, []string{"x2"}
			s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "f", Kind: types.WeightKindFrequency})
		}),
		mnSpec(func(s *types.RegressionSpec) {
			s.Name, s.Penalty, s.Alpha, s.Resample, s.JackknifeBy = "m_lasso", "l1", 0.01, "jackknife", "f"
		}),
	}
	single, sharded := mnModes(t)
	for _, group := range [][]scorerParityMode{single, sharded} {
		var want []byte
		for _, m := range group {
			resp, err := m.p.Process(ctx, mnReq(m.cohort, specs...))
			if err != nil {
				t.Fatalf("%s: %v", m.name, err)
			}
			for _, r := range resp.Regressions {
				if r.Error != nil {
					t.Fatalf("%s %s failed: %+v", m.name, r.Name, r.Error)
				}
			}
			if h := resp.Regressions[3].Holdout; h == nil || h.LogLoss == nil || h.Brier == nil || h.Accuracy == nil || h.RPS != nil || h.RMSE != nil {
				t.Fatalf("%s holdout %+v: want log_loss, brier and accuracy alone", m.name, h)
			}
			if r := resp.Regressions[0]; m.name == "serial" && (!slices.Equal(r.Categories, []string{"mid", "lo", "hi"}) || r.Reference != "mid") {
				t.Fatalf("categories %v reference %q, want the dictionary order mid, lo, hi", r.Categories, r.Reference)
			}
			got, err := json.Marshal(resp.Regressions)
			if err != nil {
				t.Fatal(err)
			}
			if want == nil {
				want = got
			} else if string(got) != string(want) {
				t.Errorf("%s differs from %s:\n%s\nvs\n%s", m.name, group[0].name, got, want)
			}
		}
	}
}

// TestMultinomial_PredictMatchesRuntime: each refusal is the runtime's
// coded error, message and details, and predict reports that very error.
func TestMultinomial_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	single, _ := mnModes(t)
	p := single[0].p
	for _, tc := range []struct {
		name string
		req  *Request
		code errors.Code
		// requires, when set, is the refusal's details.requires.
		requires string
	}{
		{"family", mnReq(mnCohort, mnSpec(func(s *types.RegressionSpec) { s.Family = "binomial" })), errors.PROCESSING_CONFIG, "no_family"},
		// U68 E2-S3: the penalty runs; a penalised fit is symmetric (no
		// reference) and has no covariance; the knobs' coupling is the
		// shared regpenalty rule's.
		{"reference under penalty", mnReq(mnCohort, mnSpec(func(s *types.RegressionSpec) { s.Penalty, s.Alpha, s.Reference = "l2", 0.1, "lo" })), errors.PROCESSING_CONFIG, "unpenalized"},
		{"penalty vcov", mnReq(mnCohort, mnSpec(func(s *types.RegressionSpec) { s.Penalty, s.Alpha, s.Vcov = "l2", 0.1, true })), errors.PROCESSING_REGRESSION_VCOV_UNSUPPORTED, ""},
		{"alpha without penalty", mnReq(mnCohort, mnSpec(func(s *types.RegressionSpec) { s.Alpha = 0.1 })), errors.PROCESSING_CONFIG, ""},
		{"unpenalized without penalty", mnReq(mnCohort, mnSpec(func(s *types.RegressionSpec) { s.Unpenalized = []string{"x1"} })), errors.PROCESSING_CONFIG, ""},
		{"unpenalized unknown", mnReq(mnCohort, mnSpec(func(s *types.RegressionSpec) { s.Penalty, s.Alpha, s.Unpenalized = "l1", 0.1, []string{"f"} })), errors.PROCESSING_CONFIG, ""},
		// U68 E2-S2: a weighted bootstrap or selection is the built-in
		// rule's refusal (a weighted jackknife runs, below).
		{"weighted bootstrap", mnReq(mnCohort, mnSpec(func(s *types.RegressionSpec) {
			s.Resample, s.BootstrapIters = "bootstrap", 20
			s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "f", Kind: types.WeightKindFrequency})
		})), errors.PULSE_WEIGHT_UNSUPPORTED, ""},
		{"weighted selection", mnReq(mnCohort, mnSpec(func(s *types.RegressionSpec) {
			s.Selection, s.Criterion = "forward", "aic"
			s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "f", Kind: types.WeightKindFrequency})
		})), errors.PULSE_WEIGHT_UNSUPPORTED, ""},
		{"selection without criterion", mnReq(mnCohort, mnSpec(func(s *types.RegressionSpec) { s.Selection = "forward" })), errors.SERVICE_VALIDATION, ""},
		{"jackknife_groups and jackknife_by", mnReq(mnCohort, mnSpec(func(s *types.RegressionSpec) {
			s.Resample, s.JackknifeGroups, s.JackknifeBy = "jackknife", 4, "f"
		})), errors.PROCESSING_CONFIG, ""},
		{"robust covariance", mnReq(mnCohort, mnSpec(func(s *types.RegressionSpec) {
			s.Covariance = &types.RegressionCovariance{Type: types.CovarianceHC0}
		})), errors.PROCESSING_CONFIG, "classical_covariance"},
		{"margins", mnReq(mnCohort, mnSpec(func(s *types.RegressionSpec) { s.Margins = &types.RegressionMargins{} })), errors.PROCESSING_CONFIG, "no_margins"},
		{"collinearity", mnReq(mnCohort, mnSpec(func(s *types.RegressionSpec) { s.Collinearity = &types.RegressionCollinearity{} })), errors.PROCESSING_CONFIG, "no_collinearity"},
		{"intercept", mnReq(mnCohort, mnSpec(func(s *types.RegressionSpec) { s.Intercept = new(bool) })), errors.PROCESSING_CONFIG, "intercept"},
		{"unknown reference", mnReq(mnCohort, mnSpec(func(s *types.RegressionSpec) { s.Reference = "top" })), errors.PROCESSING_CONFIG, "a level of the target"},
		{"reference on REG_OLS", mnReq(mnCohort, &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x1"}, Reference: "lo"}), errors.PROCESSING_CONFIG, "type REG_MULTINOMIAL"},
		{"derived target", func() *Request {
			r := mnReq(mnCohort, mnSpec(func(s *types.RegressionSpec) { s.Target = "x1_log" }))
			r.Features = []*types.Feature{{Type: types.FEAT_LOG, Field: "x1", Label: "x1_log"}}
			return r
		}(), errors.PROCESSING_CONFIG, "categorical_target"},
		{"numeric target", mnReq(mnCohort, mnSpec(func(s *types.RegressionSpec) { s.Target = "y" })), errors.PULSE_FIELD_TYPE_UNSUPPORTED, ""},
		{"conf_level", mnReq(mnCohort, mnSpec(func(s *types.RegressionSpec) { s.ConfLevel = 1.5 })), errors.PROCESSING_CONFIG, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := p.Process(ctx, tc.req)
			ce := codedOf(t, err)
			if ce.Code != tc.code || (tc.requires != "" && ce.Details["requires"] != tc.requires) {
				t.Fatalf("Process: %v (details %v), want %s requires %q", err, ce.Details, tc.code, tc.requires)
			}
			pr, perr := p.Predict(ctx, tc.req)
			if perr != nil {
				t.Fatal(perr)
			}
			matched := false
			for _, e := range pr.Errors {
				if e.Code == string(ce.Code) && e.Message == ce.Message && jsonEqual(t, e.Details, ce.Details) {
					matched = true
				}
			}
			if pr.Valid || !matched {
				t.Errorf("predict errors %+v do not carry the runtime refusal %q %v", pr.Errors, ce.Message, ce.Details)
			}
		})
	}
	// The resample and selection modifiers run (U68 E2-S2) and predict
	// accepts them: no refusal on either side.
	t.Run("modifiers run", func(t *testing.T) {
		req := mnReq(mnCohort,
			mnSpec(func(s *types.RegressionSpec) { s.Name, s.Resample = "jk", "jackknife" }),
			mnSpec(func(s *types.RegressionSpec) {
				s.Name, s.Resample, s.JackknifeBy = "jk_w", "jackknife", "f"
				s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "f", Kind: types.WeightKindProbability})
			}),
			mnSpec(func(s *types.RegressionSpec) {
				s.Name, s.Resample, s.BootstrapIters, s.RNGSeed = "boot", "bootstrap", 20, 3
			}),
			mnSpec(func(s *types.RegressionSpec) {
				s.Name, s.Selection, s.Criterion, s.Resample, s.JackknifeGroups = "sel", "backward", "bic", "jackknife", 5
			}),
		)
		pr, err := p.Predict(ctx, req)
		if err != nil || !pr.Valid || len(pr.Errors) != 0 {
			t.Fatalf("predict: %v %+v", err, pr.Errors)
		}
		resp, err := p.Process(ctx, req)
		if err != nil {
			t.Fatalf("runtime: %v", err)
		}
		for _, r := range resp.Regressions {
			if r.Error != nil || r.Resample == "" || len(r.StdErrors) == 0 {
				t.Fatalf("%s: %+v", r.Name, r)
			}
		}
		if r := resp.Regressions[3]; r.Selection != "backward" || r.JackknifeGroups != 5 || len(r.Coefficients) != 2*(len(r.SelectedFeatures)+1) {
			t.Errorf("selection + resample: %+v", r)
		}
	})
	// The penalty runs (U68 E2-S3) and predict accepts it: the
	// symmetric fit reports every category's coefficients and no
	// reference; without resample its inference is null and predict and
	// the result raise the same PENALIZED_INFERENCE_UNDEFINED advisory
	// (predict counts no p-value); with resample the replicate p-values
	// are corrected over every category (predict counts levels ×
	// predictors); a penalised selection runs and predict warns
	// REGULARIZED_SELECTION, as for REG_OLS / REG_GLM.
	t.Run("penalty runs", func(t *testing.T) {
		req := mnReq(mnCohort,
			mnSpec(func(s *types.RegressionSpec) { s.Name, s.Penalty, s.Alpha = "l2", "l2", 0.05 }),
			mnSpec(func(s *types.RegressionSpec) {
				s.Name, s.Penalty, s.Alpha, s.L1Ratio, s.Unpenalized = "enet", "elasticnet", 0.02, 0.5, []string{"x2"}
			}),
			mnSpec(func(s *types.RegressionSpec) {
				s.Name, s.Penalty, s.Alpha, s.Selection, s.Criterion = "sel", "l1", 0.01, "forward", "aic"
			}),
		)
		pr, err := p.Predict(ctx, req)
		if err != nil || !pr.Valid || len(pr.Errors) != 0 {
			t.Fatalf("predict: %v %+v", err, pr.Errors)
		}
		resp, err := p.Process(ctx, req)
		if err != nil {
			t.Fatalf("runtime: %v", err)
		}
		for i, r := range resp.Regressions {
			if r.Error != nil || r.Reference != "" || r.Penalty == "" || len(r.Advisories) != 1 {
				t.Fatalf("%s: %+v", r.Name, r)
			}
			if i < 2 && len(r.Coefficients) != 3*3 {
				t.Errorf("%s: %d coefficients, want every category's", r.Name, len(r.Coefficients))
			}
			matched := false
			for _, a := range pr.Advisories {
				if a.Code == r.Advisories[0].Code && a.Message == r.Advisories[0].Message {
					matched = true
				}
			}
			if !matched {
				t.Errorf("%s: predict advisories %+v lack the result's %+v", r.Name, pr.Advisories, r.Advisories[0])
			}
		}
		warned := 0
		for _, w := range pr.Warnings {
			if w.Code == "PROCESSING_REGRESSION_REGULARIZED_SELECTION" {
				warned++
			}
		}
		if warned != 1 {
			t.Errorf("predict warnings %+v: want REGULARIZED_SELECTION once", pr.Warnings)
		}
		if pr.PValues != nil && pr.PValues.Total != 0 {
			t.Errorf("predict p_values %+v: a penalised fit without resample reports none", pr.PValues)
		}
		boot := mnReq(mnCohort, mnSpec(func(s *types.RegressionSpec) {
			s.Penalty, s.Alpha, s.Resample, s.JackknifeGroups, s.RNGSeed = "l2", 0.05, "jackknife", 6, 2
			s.Multiplicity = &types.RegressionMultiplicity{Method: types.MultiplicityMethodHolm}
		}))
		pr, err = p.Predict(ctx, boot)
		if err != nil || !pr.Valid || pr.PValues == nil || pr.PValues.Total != 6 {
			t.Fatalf("resampled predict: %v %+v, p_values %+v; want 6 (3 levels × 2 predictors)", err, pr.Errors, pr.PValues)
		}
		resp, err = p.Process(ctx, boot)
		if err != nil {
			t.Fatal(err)
		}
		if r := resp.Regressions[0]; r.Error != nil || len(r.Advisories) != 0 || r.Multiplicity == nil || r.Multiplicity.M != 6 {
			t.Fatalf("resampled penalised fit: %+v", r)
		}
	})
	// A holdout runs (U68 E1-S3) and predict accepts it: no refusal on
	// either side, the slot buffered.
	t.Run("holdout runs", func(t *testing.T) {
		req := mnReq(mnCohort, mnSpec(func(s *types.RegressionSpec) { s.Holdout = "x1 > 1" }))
		pr, err := p.Predict(ctx, req)
		if err != nil || !pr.Valid || len(pr.Errors) != 0 {
			t.Fatalf("predict: %v %+v", err, pr.Errors)
		}
		resp, err := p.Process(ctx, req)
		if err != nil || resp.Regressions[0].Error != nil || resp.Regressions[0].Holdout == nil {
			t.Fatalf("runtime: %v %+v", err, resp)
		}
	})
}

// TestMultinomial_Multiplicity: an own multiplicity block corrects every
// non-intercept coefficient of every non-reference level — the family a
// predict count names, (levels − 1) × predictors on the dictionary
// basis — and an explicit list exactly its keys; the raw p-values never
// move.
func TestMultinomial_Multiplicity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	single, _ := mnModes(t)
	p := single[0].p
	plain, err := p.Process(ctx, mnReq(mnCohort, mnSpec(nil)))
	if err != nil {
		t.Fatal(err)
	}
	holm := mnSpec(func(s *types.RegressionSpec) {
		s.Multiplicity = &types.RegressionMultiplicity{Method: types.MultiplicityMethodHolm}
	})
	req := mnReq(mnCohort, holm)
	resp, err := p.Process(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	r := resp.Regressions[0]
	var want []string
	for k := range r.PValues {
		if !strings.HasSuffix(k, ":(intercept)") {
			want = append(want, k)
		}
	}
	slices.Sort(want)
	var got []string
	for k := range r.PAdjusted {
		got = append(got, k)
	}
	slices.Sort(got)
	if len(want) != 4 || !slices.Equal(got, want) || r.Multiplicity == nil || r.Multiplicity.M != 4 {
		t.Fatalf("p_adjusted keys %v (multiplicity %+v), want %v", got, r.Multiplicity, want)
	}
	for k, v := range plain.Regressions[0].PValues {
		if r.PValues[k] != v {
			t.Errorf("p_values[%s] moved under multiplicity: %v vs %v", k, r.PValues[k], v)
		}
	}
	pr, err := p.Predict(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if pr.PValues == nil || pr.PValues.Total != 4 || pr.PValues.Uncorrected != 0 || pr.PValues.Basis != descriptor.PValueBasisDictionary {
		t.Fatalf("predict p_values %+v, want 4 corrected on the dictionary basis", pr.PValues)
	}
	explicit := mnSpec(func(s *types.RegressionSpec) {
		s.Multiplicity = &types.RegressionMultiplicity{Method: types.MultiplicityMethodBonferroni,
			Coefficients: []string{"lo:x1", "hi:x2"}}
	})
	resp, err = p.Process(ctx, mnReq(mnCohort, explicit))
	if err != nil {
		t.Fatal(err)
	}
	if r := resp.Regressions[0]; len(r.PAdjusted) != 2 || r.PAdjusted["lo:x1"] != math.Min(1, 2*r.PValues["lo:x1"]) {
		t.Fatalf("explicit list: p_adjusted %v", r.PAdjusted)
	}
	pr, err = p.Predict(ctx, mnReq(mnCohort, explicit))
	if err != nil {
		t.Fatal(err)
	}
	if pr.PValues == nil || pr.PValues.Total != 2 || pr.PValues.Basis != descriptor.PValueBasisExact {
		t.Fatalf("explicit list: predict p_values %+v, want 2 exact", pr.PValues)
	}
}

// TestMultinomial_FailsAlone: a named reference no fitted row carries
// fails the slot alone — its result keeps name and type with the coded
// error, the response warns PULSE_SLOT_FAILED — while predict, which
// reads no row, calls the request valid.
func TestMultinomial_FailsAlone(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	single, _ := mnModes(t)
	p := single[0].p
	req := mnReq(mnCohort, mnSpec(func(s *types.RegressionSpec) { s.Reference = "hi" }), mnSpec(func(s *types.RegressionSpec) { s.Name = "ok" }))
	req.Filterers = []*types.Filterer{{Type: types.FILTER_EXCLUDE, Field: "cls", Values: []string{"hi"}}}
	resp, err := p.Process(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	failed, ok := resp.Regressions[0], resp.Regressions[1]
	if failed.Error == nil || failed.Error.Code != string(errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA) || failed.Error.Details["reason"] != "reference_empty" || failed.Coefficients != nil {
		t.Fatalf("failed slot %+v", failed)
	}
	if ok.Error != nil || !slices.Equal(ok.Categories, []string{"mid", "lo"}) || len(ok.Coefficients) != 3 {
		t.Fatalf("surviving slot %+v", ok)
	}
	warned := false
	for _, w := range resp.Warnings {
		warned = warned || w.Code == string(errors.PULSE_SLOT_FAILED)
	}
	if !warned {
		t.Errorf("no PULSE_SLOT_FAILED warning in %+v", resp.Warnings)
	}
	pr, err := p.Predict(ctx, req)
	if err != nil || !pr.Valid {
		t.Fatalf("predict %+v %v, want valid", pr, err)
	}
}

// TestMultinomial_ReplicateCategoryLossFailsAlone: a leave-one-out
// jackknife whose deletion empties a category the full fit has fails the
// slot alone (PROCESSING_REGRESSION_INSUFFICIENT_DATA, reason
// replicate_empty_level, details.level the category; PULSE_SLOT_FAILED)
// while predict — which reads no row — accepts the request; Strict fails
// the request with the slot's own code. A bootstrap over the same rows
// skips the replicates that lost it (U68 E2-S2, decision D5).
func TestMultinomial_ReplicateCategoryLossFailsAlone(t *testing.T) {
	t.Parallel()

	// hi kept on one interior row (the hi row of 50..110 with the
	// smallest |x2|), every other hi row relabelled mid.
	keep, best := -1, math.Inf(1)
	for i := 50; i <= 110; i++ {
		if r := mnRow(i); r[4] == "hi" {
			if v, _ := strconv.ParseFloat(r[2], 64); math.Abs(v) < best {
				keep, best = i, math.Abs(v)
			}
		}
	}
	var rows [][]string
	for i := 0; i < mnRowsN; i++ {
		r := mnRow(i)
		if r[4] == "hi" && i != 2 && i != keep {
			r[4] = "mid"
		}
		if i == 2 {
			r[4] = "lo" // the dictionary order stays mid, lo, hi
		}
		rows = append(rows, r)
	}
	fsys := afero.NewMemMapFs()
	createTestPulseFile(t, fsys, "one_hi.pulse", mnColumns, rows)
	newP := func(strict bool) *Pulse {
		p, err := New(Options{FS: fsys, Strict: strict})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	ctx := context.Background()
	p := newP(false)
	req := mnReq("one_hi.pulse",
		mnSpec(func(s *types.RegressionSpec) { s.Name, s.Resample = "jk", "jackknife" }),
		mnSpec(func(s *types.RegressionSpec) {
			s.Name, s.Resample, s.BootstrapIters, s.RNGSeed = "boot", "bootstrap", 40, 9
		}),
	)
	if pr, err := p.Predict(ctx, req); err != nil || !pr.Valid || len(pr.Errors) != 0 {
		t.Fatalf("predict: %v %+v", err, pr)
	}
	resp, err := p.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v (the slot should fail alone)", err)
	}
	jk, boot := resp.Regressions[0], resp.Regressions[1]
	if jk.Error == nil || jk.Error.Code != string(errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA) ||
		jk.Error.Details["reason"] != "replicate_empty_level" || jk.Error.Details["level"] != "hi" || jk.Coefficients != nil {
		t.Fatalf("jackknife slot %+v", jk.Error)
	}
	warned := false
	for _, w := range resp.Warnings {
		warned = warned || w.Code == string(errors.PULSE_SLOT_FAILED)
	}
	if !warned {
		t.Errorf("no PULSE_SLOT_FAILED warning in %+v", resp.Warnings)
	}
	if boot.Error != nil || boot.BootstrapSucceeded >= boot.BootstrapRequested || !slices.Equal(boot.Categories, []string{"mid", "lo", "hi"}) {
		t.Fatalf("bootstrap %+v: want some replicates skipped, every category kept", boot)
	}
	_, serr := newP(true).Process(ctx, req)
	if ce := codedOf(t, serr); ce.Code != errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA || ce.Details["reason"] != "replicate_empty_level" {
		t.Errorf("Strict Process: %v, want the slot's own INSUFFICIENT_DATA", serr)
	}
}
