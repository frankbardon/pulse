package regordinal_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
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
	c := goldenCase(t, "cloglog_prob_nominal")
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
