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
// frequency- and probability-weighted, a named reference — are
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
		{"penalty", mnReq(mnCohort, mnSpec(func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "l2", 0.1 })), errors.PROCESSING_CONFIG, "unpenalized"},
		{"selection", mnReq(mnCohort, mnSpec(func(s *types.RegressionSpec) { s.Selection, s.Criterion = "forward", "aic" })), errors.PROCESSING_CONFIG, "no_selection"},
		{"resample", mnReq(mnCohort, mnSpec(func(s *types.RegressionSpec) { s.Resample = "jackknife" })), errors.PROCESSING_CONFIG, "no_resample"},
		{"robust covariance", mnReq(mnCohort, mnSpec(func(s *types.RegressionSpec) {
			s.Covariance = &types.RegressionCovariance{Type: types.CovarianceHC0}
		})), errors.PROCESSING_CONFIG, "classical_covariance"},
		{"margins", mnReq(mnCohort, mnSpec(func(s *types.RegressionSpec) { s.Margins = &types.RegressionMargins{} })), errors.PROCESSING_CONFIG, "no_margins"},
		{"collinearity", mnReq(mnCohort, mnSpec(func(s *types.RegressionSpec) { s.Collinearity = &types.RegressionCollinearity{} })), errors.PROCESSING_CONFIG, "no_collinearity"},
		{"save", mnReq(mnCohort, mnSpec(func(s *types.RegressionSpec) { s.Save = "m.pulse-model.json" })), errors.PROCESSING_CONFIG, "no_save"},
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
		{"holdout", mnReq(mnCohort, mnSpec(func(s *types.RegressionSpec) { s.Holdout = "x1 > 1" })), errors.PULSE_MODEL_SCORE_INVALID, ""},
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
	// A `fits` entry cannot bind a multinomial fit: Compose and
	// PredictCompose refuse it alike.
	creq := &ComposedRequest{
		Fits:     []types.FitSpec{{Name: "mf", Request: mnReq(mnCohort, mnSpec(nil))}},
		Requests: []*Request{mnReq(mnCohort, mnSpec(nil))},
	}
	_, err := p.Compose(ctx, creq)
	ce := codedOf(t, err)
	if ce.Code != errors.PULSE_FIT_INVALID || ce.Details["reason"] != "regression_type" {
		t.Fatalf("Compose: %v (details %v), want PULSE_FIT_INVALID regression_type", err, ce.Details)
	}
	env, err := p.PredictCompose(ctx, creq)
	if err != nil {
		t.Fatal(err)
	}
	matched := false
	for _, e := range env.Errors {
		if e.Code == string(ce.Code) && e.Message == ce.Message {
			matched = true
		}
	}
	if !matched {
		t.Errorf("PredictCompose errors %+v lack the runtime's %q", env.Errors, ce.Message)
	}
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
