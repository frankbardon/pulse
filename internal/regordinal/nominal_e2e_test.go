package regordinal_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/regordinal"
	"github.com/frankbardon/pulse/types"
)

// nominal_e2e_test.go holds params.nominal_test (U68 E6-S1) through
// Process beside the golden comparison in checkAgainstGolden: the wire
// form of a failed figure, the lifted warnings, Strict, worker
// invariance and the absent knob.

// TestRegOrdinal_NominalTestNullOnTheWire: the pinned non-convergent
// case (x3 separates the lowest split) keeps the fit and the converged
// figures; the failed ones are null on the wire, their keys kept, and
// each warning reaches Response.Warnings (details.regression added).
// Under Strict the first one fails the slot with its own code.
func TestRegOrdinal_NominalTestNullOnTheWire(t *testing.T) {
	t.Parallel()
	c := goldenCase(t, "nominal_noconverge")
	p := newInstance(t, nil)
	writeCohort(t, p, "ord.pulse", cohortSpec{p: 3}, c.Rows, nil)
	req := &types.Request{Cohort: &types.Cohort{Filename: "ord.pulse"}, Regressions: []*types.RegressionSpec{caseSpec(c, nil)}}
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	res := resp.Regressions[0]
	if res.Error != nil {
		t.Fatalf("slot failed: %v", res.Error)
	}
	raw, err := types.MarshalFinite(res)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Outputs struct {
			NominalTest map[string]json.RawMessage `json:"nominal_test"`
		} `json:"outputs"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	nt := wire.Outputs.NominalTest
	if string(nt["statistic"]) != "null" || string(nt["p_value"]) != "null" || string(nt["df"]) != "3" {
		t.Errorf("omnibus on the wire %s %s %s, want null null 3", nt["statistic"], nt["p_value"], nt["df"])
	}
	if !strings.Contains(string(nt["terms"]), `"x3":{"df":1,"p_value":null,"statistic":null}`) {
		t.Errorf("terms on the wire %s, want x3 null", nt["terms"])
	}
	lifted := 0
	for _, w := range resp.Warnings {
		if w.Code == string(perr.PROCESSING_REGRESSION_DIAGNOSTIC) && w.Details["regression"] != nil {
			lifted++
		}
	}
	if lifted != 2 {
		t.Errorf("response warnings %v, want the two nominal_test diagnostics lifted", resp.Warnings)
	}
	strict := newInstance(t, func(o *pulse.Options) { o.Strict = true })
	writeCohort(t, strict, "ord.pulse", cohortSpec{p: 3}, c.Rows, nil)
	sresp, err := strict.Process(context.Background(), req)
	code := ""
	var ce *perr.CodedError
	switch {
	case err != nil && asCoded(err, &ce):
		code = string(ce.Code)
	case err == nil && sresp.Regressions[0].Error != nil:
		code = sresp.Regressions[0].Error.Code
	}
	if code != string(perr.PROCESSING_REGRESSION_DIAGNOSTIC) {
		t.Errorf("strict: err %v, code %q; want PROCESSING_REGRESSION_DIAGNOSTIC", err, code)
	}
}

// TestRegOrdinal_NominalTestAbsentIsByteIdentical: params.nominal_test
// adds outputs.nominal_test and moves nothing else; without it the
// result carries no key.
func TestRegOrdinal_NominalTestAbsentIsByteIdentical(t *testing.T) {
	t.Parallel()
	c := goldenCase(t, "probit_freq_nominal")
	p := newInstance(t, nil)
	writeCohort(t, p, "ord.pulse", cohortSpec{p: 2}, c.Rows, nil)
	with := fitOne(t, p, "ord.pulse", caseSpec(c, nil))
	c.NominalTest = nil
	without := fitOne(t, p, "ord.pulse", caseSpec(c, nil))
	if _, ok := without.Outputs["nominal_test"]; ok {
		t.Fatal("outputs.nominal_test present without params.nominal_test")
	}
	delete(with.Outputs, "nominal_test")
	if a, b := mustJSON(t, with), mustJSON(t, without); a != b {
		t.Errorf("params.nominal_test moved another figure:\n%s\n%s", a, b)
	}
}

// TestRegOrdinal_NominalTestWorkerInvariant: the test's figures are
// byte-identical across DecodeWorkers / ShardWorkers counts.
func TestRegOrdinal_NominalTestWorkerInvariant(t *testing.T) {
	t.Parallel()
	c := goldenCase(t, "cloglog_freq_nominal")
	var first string
	for _, w := range []int{1, 3, 8} {
		p := newInstance(t, func(o *pulse.Options) { o.DecodeWorkers, o.ShardWorkers = w, w })
		writeCohort(t, p, "ord.pulse", cohortSpec{p: 2}, c.Rows, nil)
		raw, err := types.MarshalFinite(fitOne(t, p, "ord.pulse", caseSpec(c, nil)).Outputs["nominal_test"])
		if err != nil {
			t.Fatal(err)
		}
		if first == "" {
			first = string(raw)
		} else if string(raw) != first {
			t.Errorf("workers %d: nominal_test differs:\n%s\n%s", w, first, raw)
		}
	}
}

// TestRegOrdinal_NominalTestAbsentUnderProbabilityWeight: a resolved
// probability weight (on the slot, or inherited from the request)
// leaves outputs.nominal_test absent — no likelihood, as the
// multinomial term_tests LR — with no warning and no refusal; a
// frequency weight and no weight keep it. Predict counts its p-values
// (one per predictor plus the omnibus) exactly when the run emits it.
func TestRegOrdinal_NominalTestAbsentUnderProbabilityWeight(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := goldenCase(t, "probit_freq_nominal")
	p := newInstance(t, nil)
	writeCohort(t, p, "ord.pulse", cohortSpec{p: 2}, c.Rows, nil)
	prob := &types.WeightSpec{Field: "w", Kind: types.WeightKindProbability}
	for name, tc := range map[string]struct {
		slot, request *types.WeightSpec
		present       bool
	}{
		"unweighted":    {present: true},
		"frequency":     {slot: &types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}, present: true},
		"probability":   {slot: prob},
		"request_level": {request: prob},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			build := func(nominal bool) *types.Request {
				s := &types.RegressionSpec{Type: types.REG_ORDINAL, Target: "y", Predictors: c.Predictors}
				s.Params = json.RawMessage(`{"link":"probit"}`)
				if nominal {
					s.Params = json.RawMessage(`{"link":"probit","nominal_test":{}}`)
				}
				if tc.slot != nil {
					s.Weight = types.SlotWeightOf(*tc.slot)
				}
				return &types.Request{Cohort: &types.Cohort{Filename: "ord.pulse"}, Weight: tc.request, Regressions: []*types.RegressionSpec{s}}
			}
			resp, err := p.Process(ctx, build(true))
			if err != nil || resp.Regressions[0].Error != nil {
				t.Fatalf("Process: %v %v", err, resp)
			}
			res := resp.Regressions[0]
			if _, ok := res.Outputs["nominal_test"]; ok != tc.present || len(res.Warnings) != 0 {
				t.Fatalf("outputs.nominal_test present %v (want %v), warnings %v", ok, tc.present, res.Warnings)
			}
			with, err := p.Predict(ctx, build(true))
			if err != nil || len(with.Errors) != 0 {
				t.Fatalf("Predict: %v %+v", err, with.Errors)
			}
			without, err := p.Predict(ctx, build(false))
			if err != nil || len(without.Errors) != 0 {
				t.Fatalf("Predict: %v %+v", err, without.Errors)
			}
			total := func(pr *descriptor.PredictResult) int {
				if pr.PValues == nil {
					return 0
				}
				return pr.PValues.Total
			}
			want := 0
			if tc.present {
				want = len(c.Predictors) + 1
			}
			if got := total(with) - total(without); got != want {
				t.Errorf("predict counts %d nominal_test p-values, the run emits %d", got, want)
			}
		})
	}
}

// TestRegOrdinal_ReplicatesSkipAuxTests: resample replicates fit
// without the Brant and nominal tests (their outputs are discarded) —
// one test-running fit per resampled slot, the base fit — and the
// resampled result carries both outputs identical to the unresampled
// fit's; selection candidates keep them, so the chosen model reports
// its own.
//
// serial: it reads the package's process-wide AuxTestFits counter,
// which a parallel ordinal fit would move.
func TestRegOrdinal_ReplicatesSkipAuxTests(t *testing.T) {
	c := goldenCase(t, "logit_two")
	p := newInstance(t, nil)
	writeCohort(t, p, "ord.pulse", cohortSpec{p: 2}, c.Rows, nil)
	spec := func(mut func(*types.RegressionSpec)) *types.RegressionSpec {
		s := &types.RegressionSpec{Type: types.REG_ORDINAL, Name: "ord", Target: "y", Predictors: c.Predictors,
			Params: json.RawMessage(`{"brant":{},"nominal_test":{}}`)}
		if mut != nil {
			mut(s)
		}
		return s
	}
	run := func(s *types.RegressionSpec) (*types.RegressionResult, int64) {
		before := regordinal.AuxTestFits()
		res := fitOne(t, p, "ord.pulse", s)
		return res, regordinal.AuxTestFits() - before
	}
	aux := func(res *types.RegressionResult) string {
		raw, err := types.MarshalFinite(map[string]any{"brant": res.Outputs["brant"], "nominal_test": res.Outputs["nominal_test"]})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	base, n := run(spec(nil))
	if n != 1 || base.Outputs["brant"] == nil || base.Outputs["nominal_test"] == nil {
		t.Fatalf("base fit: %d test-running fits, outputs %v", n, base.Outputs)
	}
	for name, mut := range map[string]func(*types.RegressionSpec){
		"jackknife":    func(s *types.RegressionSpec) { s.Resample = "jackknife" },
		"jackknife_by": func(s *types.RegressionSpec) { s.Resample, s.JackknifeBy = "jackknife", "g" },
		"bootstrap":    func(s *types.RegressionSpec) { s.Resample, s.BootstrapIters, s.RNGSeed = "bootstrap", 20, 3 },
	} {
		res, n := run(spec(mut))
		if n != 1 {
			t.Errorf("%s: %d fits ran the tests, want 1 (the base fit; replicates skip them)", name, n)
		}
		if got, want := aux(res), aux(base); got != want {
			t.Errorf("%s: resampled outputs differ from the base fit's:\n%s\n%s", name, got, want)
		}
	}
	res, n := run(spec(func(s *types.RegressionSpec) { s.Selection, s.Criterion = "forward", "aic" }))
	if n < 2 || res.Outputs["brant"] == nil || res.Outputs["nominal_test"] == nil {
		t.Errorf("selection: %d test-running fits (candidates keep the tests), outputs %v", n, res.Outputs)
	}
}

// TestRegOrdinal_PredictCountsTestPValues: predict's p_values count the
// Brant and nominal tests' p-values (omnibus plus one per predictor,
// uncorrected) exactly as the run emits them — each alone, together, and
// under a probability weight (Brant runs, the nominal test is absent).
func TestRegOrdinal_PredictCountsTestPValues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := goldenCase(t, "logit_two")
	p := newInstance(t, nil)
	writeCohort(t, p, "ord.pulse", cohortSpec{p: 2}, c.Rows, nil)
	build := func(params string, weighted bool) *types.Request {
		s := &types.RegressionSpec{Type: types.REG_ORDINAL, Target: "y", Predictors: c.Predictors, Params: json.RawMessage(params)}
		if weighted {
			s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindProbability})
		}
		return &types.Request{Cohort: &types.Cohort{Filename: "ord.pulse"}, Regressions: []*types.RegressionSpec{s}}
	}
	// emitted counts the p_value keys outputs.brant / nominal_test carry.
	emitted := func(res *types.RegressionResult) int {
		n := 0
		for _, key := range []string{"brant", "nominal_test"} {
			o, ok := res.Outputs[key].(map[string]any)
			if !ok {
				continue
			}
			n++
			terms, _ := o["terms"].(map[string]any)
			for _, tm := range terms {
				if _, ok := tm.(map[string]any)["p_value"]; ok {
					n++
				}
			}
		}
		return n
	}
	predicted := func(r *types.Request) (total, uncorrected int) {
		pr, err := p.Predict(ctx, r)
		if err != nil || len(pr.Errors) != 0 {
			t.Fatalf("Predict: %v %+v", err, pr.Errors)
		}
		if pr.PValues == nil {
			return 0, 0
		}
		return pr.PValues.Total, pr.PValues.Uncorrected
	}
	for name, tc := range map[string]struct {
		params   string
		weighted bool
		want     int
	}{
		"brant":                  {`{"brant":{}}`, false, 3},
		"nominal_test":           {`{"nominal_test":{}}`, false, 3},
		"both":                   {`{"brant":{},"nominal_test":{}}`, false, 6},
		"both_probability":       {`{"brant":{},"nominal_test":{}}`, true, 3},
		"brant_probability":      {`{"brant":{}}`, true, 3},
		"nominal_probability":    {`{"nominal_test":{}}`, true, 0},
		"neither_with_link_only": {`{"link":"logit"}`, false, 0},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := build(tc.params, tc.weighted)
			resp, err := p.Process(ctx, r)
			if err != nil || resp.Regressions[0].Error != nil {
				t.Fatalf("Process: %v", err)
			}
			if got := emitted(resp.Regressions[0]); got != tc.want {
				t.Fatalf("run emits %d test p-values, want %d", got, tc.want)
			}
			total, unc := predicted(r)
			baseTotal, baseUnc := predicted(build(`{}`, tc.weighted))
			if total-baseTotal != tc.want || unc-baseUnc != tc.want {
				t.Errorf("predict counts %d (%d uncorrected) test p-values, the run emits %d", total-baseTotal, unc-baseUnc, tc.want)
			}
		})
	}
}
