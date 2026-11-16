package pulse

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"math"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// postFitEcho is the test-only operator: it echoes the fit's
// coefficients as a table, reads a slot value and the optimize block's
// objective, scales by params.scale and writes an undefined scalar.
// A params.mode of "error" / "coded" / "panic" / "stray" makes it fail
// that way.
func postFitEcho() PostFitRegistration {
	return PostFitRegistration{
		Name:        "POSTFIT_TEST_ECHO",
		Description: "echoes a fit's coefficients",
		Params: []ParamMeta{
			{Name: "scale", JSONType: "number"},
			{Name: "mode", JSONType: "string"},
		},
		Outputs: extend.PostFitSchema{
			Scalars: []string{"slope", "n", "objective", "undefined"},
			Tables: []extend.PostFitTableSchema{{Name: "coefs", Columns: []extend.PostFitColumn{
				{Name: "term", Kind: extend.PostFitString}, {Name: "estimate", Kind: extend.PostFitFloat}, {Name: "note", Kind: extend.PostFitString},
			}}},
		},
		Run: func(in extend.PostFitInput) (extend.PostFitOutput, error) {
			var p struct {
				Scale float64 `json:"scale"`
				Mode  string  `json:"mode"`
			}
			if raw := in.Params(); raw != nil {
				if err := json.Unmarshal(raw, &p); err != nil {
					return extend.PostFitOutput{}, err
				}
			}
			switch p.Mode {
			case "error":
				return extend.PostFitOutput{}, stderrors.New("operator gave up")
			case "coded":
				return extend.PostFitOutput{}, perr.NewCodedError(perr.PULSE_RESULT_INVALID, "coded refusal")
			case "panic":
				panic("post-fit operator panicked")
			}
			f := in.Fit(in.Fits()[0])
			coefs := f.Coefficients()
			n, err := in.Result("count", "n")
			if err != nil {
				return extend.PostFitOutput{}, err
			}
			var objective float64
			if sols := in.Optimizations(); len(sols) > 0 {
				objective = sols[0].Objective
			}
			table := &types.PostFitTable{Columns: []string{"term", "estimate", "note"}}
			for _, term := range []string{"(intercept)", "age"} {
				table.Rows = append(table.Rows, []any{term, coefs[term], nil})
			}
			out := extend.PostFitOutput{
				Scalars: map[string]float64{"slope": p.Scale * coefs["age"], "n": n, "objective": objective, "undefined": math.NaN()},
				Tables:  map[string]*types.PostFitTable{"coefs": table},
			}
			if p.Mode == "stray" {
				out.Scalars["stray"] = 1
			}
			return out, nil
		},
	}
}

// postFitComposed is a batch over the parity cohort: a count slot, a
// regression fit `m`, an optimize block and the post_fit entries raw (a
// JSON array).
func postFitComposed(t *testing.T, raw string) *ComposedRequest {
	t.Helper()
	return decodeComposedJSON(t, `{"requests":[{"label":"count","cohort":{"filename":"`+parityCohort+`"},"aggregations":[{"type":"AGG_COUNT","field":"age","label":"n"}]}],
		"fits":[{"name":"m","request":{"cohort":{"filename":"`+parityCohort+`"},"regressions":[{"type":"REG_OLS","target":"age","predictors":["age"]}]}}],
		"optimize":[{"name":"o","direction":"minimize","variables":[{"name":"x","lower":0,"upper":10}],"objective":"(x - 3) * (x - 3) + 2"}],
		"post_fit":`+raw+`}`)
}

func postFitPulse(t *testing.T, opts Options) *Pulse {
	t.Helper()
	opts.Extensions.PostFits = append(opts.Extensions.PostFits, postFitEcho())
	return sweepPulse(t, opts)
}

// TestPostFit_RunsAfterOptimize: an entry reads its fit's coefficients,
// a slot's value and the optimize solution; its typed output lands in
// ComposedResponse.PostFit (NaN as null on the wire) and Compose and
// ComposeParallel answer the same bytes.
func TestPostFit_RunsAfterOptimize(t *testing.T) {
	t.Parallel()

	p := postFitPulse(t, Options{})
	c := postFitComposed(t, `[{"label":"echo","type":"POSTFIT_TEST_ECHO","fits":["m"],"params":{"scale":2}}]`)
	var wires [][]byte
	for name, run := range composeRuns(p) {
		resp, err := run(c)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(resp.PostFit) != 1 {
			t.Fatalf("%s: post_fit %+v", name, resp.PostFit)
		}
		r := resp.PostFit[0]
		coefs := resp.Fits["m"].RegressionResult.Coefficients
		if r.Label != "echo" || r.Type != "POSTFIT_TEST_ECHO" || r.Error != nil {
			t.Fatalf("%s: identity %+v", name, r)
		}
		if r.Scalars["slope"] != 2*coefs["age"] || r.Scalars["n"] != 40 || r.Scalars["objective"] != resp.Optimizations[0].Objective || !math.IsNaN(r.Scalars["undefined"]) {
			t.Fatalf("%s: scalars %+v (coefs %v, objective %v)", name, r.Scalars, coefs, resp.Optimizations[0].Objective)
		}
		tab := r.Tables["coefs"]
		if tab == nil || len(tab.Rows) != 2 || tab.Rows[1][0] != "age" || tab.Rows[1][1] != coefs["age"] || tab.Rows[1][2] != nil {
			t.Fatalf("%s: table %+v", name, tab)
		}
		raw, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.Contains(raw, []byte(`"undefined":null`)) || !bytes.Contains(raw, []byte(`"post_fit":[{"label":"echo","type":"POSTFIT_TEST_ECHO"`)) {
			t.Fatalf("%s: wire %s", name, raw)
		}
		wires = append(wires, raw)
	}
	if !bytes.Equal(wires[0], wires[1]) {
		t.Fatalf("Compose and ComposeParallel differ:\n%s\n%s", wires[0], wires[1])
	}

	// Without a post_fit entry the root is absent.
	c.PostFit = nil
	resp, err := p.Compose(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(resp)
	if bytes.Contains(raw, []byte(`"post_fit"`)) {
		t.Fatalf("post_fit on a post-fit-free batch: %s", raw)
	}
}

// TestPostFit_Refusals: a malformed entry is refused before any fit or
// slot runs, identically on both orchestrators.
func TestPostFit_Refusals(t *testing.T) {
	t.Parallel()

	p := postFitPulse(t, Options{})
	cases := []struct {
		name   string
		raw    string
		code   perr.Code
		reason string
	}{
		{"label missing", `[{"type":"POSTFIT_TEST_ECHO","fits":["m"]}]`, perr.PULSE_POSTFIT_INVALID, "label_missing"},
		{"type unknown", `[{"label":"e","type":"POSTFIT_TEST_NOPE","fits":["m"]}]`, perr.PULSE_POSTFIT_INVALID, "type_unknown"},
		{"fit unknown", `[{"label":"e","type":"POSTFIT_TEST_ECHO","fits":["zz"]}]`, perr.PULSE_POSTFIT_INVALID, "fit_unknown"},
		{"fit duplicate", `[{"label":"e","type":"POSTFIT_TEST_ECHO","fits":["m","m"]}]`, perr.PULSE_POSTFIT_INVALID, "fit_duplicate"},
		{"param unknown", `[{"label":"e","type":"POSTFIT_TEST_ECHO","fits":["m"],"params":{"scael":2}}]`, perr.PULSE_REQUEST_UNKNOWN_FIELD, ""},
		{"param wrong type", `[{"label":"e","type":"POSTFIT_TEST_ECHO","fits":["m"],"params":{"scale":"two"}}]`, perr.PULSE_REQUEST_PARAM_INVALID, "wrong_type"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for name, run := range composeRuns(p) {
				resp, err := run(postFitComposed(t, tc.raw))
				ce := requireCoded(t, err, tc.code)
				if resp != nil {
					t.Fatalf("%s: a refused batch returned a response", name)
				}
				if tc.reason != "" && ce.Details["reason"] != tc.reason {
					t.Fatalf("%s: details %+v, want reason %s", name, ce.Details, tc.reason)
				}
			}
		})
	}
}

// TestPostFit_FailsAlone: an operator error (uncoded →
// PULSE_POSTFIT_FAILED, coded → its own code) and a failed fit each fail
// the entry alone — identity and error kept, PULSE_SLOT_FAILED warned —
// beside a healthy entry; Strict fails the batch with the entry's code.
func TestPostFit_FailsAlone(t *testing.T) {
	t.Parallel()

	raw := `[{"label":"ok","type":"POSTFIT_TEST_ECHO","fits":["m"]},
		{"label":"err","type":"POSTFIT_TEST_ECHO","fits":["m"],"params":{"mode":"error"}},
		{"label":"coded","type":"POSTFIT_TEST_ECHO","fits":["m"],"params":{"mode":"coded"}}]`
	p := postFitPulse(t, Options{})
	for name, run := range composeRuns(p) {
		resp, err := run(postFitComposed(t, raw))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		pf := resp.PostFit
		if len(pf) != 3 || pf[0].Error != nil || pf[0].Scalars == nil {
			t.Fatalf("%s: %+v", name, pf)
		}
		if pf[1].Label != "err" || pf[1].Error == nil || pf[1].Error.Code != string(perr.PULSE_POSTFIT_FAILED) || pf[1].Scalars != nil || pf[1].Tables != nil {
			t.Fatalf("%s: uncoded failure %+v", name, pf[1])
		}
		if pf[2].Error == nil || pf[2].Error.Code != string(perr.PULSE_RESULT_INVALID) {
			t.Fatalf("%s: coded failure %+v", name, pf[2])
		}
		var slots []any
		for _, w := range resp.Warnings {
			if w.Code == string(perr.PULSE_SLOT_FAILED) && w.Details["slot_kind"] == "post_fit" {
				slots = append(slots, w.Details["label"])
			}
		}
		if len(slots) != 2 || slots[0] != "err" || slots[1] != "coded" {
			t.Fatalf("%s: slot-failed warnings %+v", name, resp.Warnings)
		}
	}

	strict := postFitPulse(t, Options{Strict: true})
	for name, run := range composeRuns(strict) {
		resp, err := run(postFitComposed(t, raw))
		ce := requireCoded(t, err, perr.PULSE_POSTFIT_FAILED)
		if resp != nil || ce.Details["slot_kind"] != "post_fit" || ce.Details["index"] != 1 || ce.Details["label"] != "err" {
			t.Fatalf("%s strict: %v %+v", name, ce, ce.Details)
		}
	}

	// A failed fit fails the entry with the fit's own code; the operator
	// never runs.
	empty := postFitComposed(t, `[{"label":"e","type":"POSTFIT_TEST_ECHO","fits":["m"],"params":{"mode":"panic"}}]`)
	empty.Fits[0].Request.Filterers = []*types.Filterer{{Type: types.FILTER_RANGE, Field: "age", Values: []string{"1000", "2000"}}}
	for name, run := range composeRuns(p) {
		resp, err := run(empty)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		fitErr := resp.Fits["m"].RegressionResult.Error
		r := resp.PostFit[0]
		if fitErr == nil || r.Error == nil || r.Error.Code != fitErr.Code || r.Error.Details["fit"] != "m" {
			t.Fatalf("%s: fit %+v, entry %+v", name, fitErr, r.Error)
		}
	}
}

// TestPostFit_OutputMismatch: an output its declaration does not admit
// is the fatal PULSE_EXTENSION_POSTFIT_OUTPUT_MISMATCH, on both
// orchestrators. (The panic case lives in panic_recovery_postfit_test.go.)
func TestPostFit_OutputMismatch(t *testing.T) {
	t.Parallel()

	p := postFitPulse(t, Options{})
	for name, run := range composeRuns(p) {
		resp, err := run(postFitComposed(t, `[{"label":"s","type":"POSTFIT_TEST_ECHO","fits":["m"],"params":{"mode":"stray"}}]`))
		ce := requireCoded(t, err, perr.PULSE_EXTENSION_POSTFIT_OUTPUT_MISMATCH)
		if resp != nil || ce.Details["reason"] != "undeclared_scalar" || ce.Details["name"] != "stray" {
			t.Fatalf("%s: %v %+v", name, ce, ce.Details)
		}
	}
}

// TestPostFit_RegistrationValidation: pulse.New refuses a malformed
// registration — the naming policy, a duplicate, a nil Run, a malformed
// output schema, an expression-capable param.
func TestPostFit_RegistrationValidation(t *testing.T) {
	t.Parallel()

	edit := func(f func(*PostFitRegistration)) []PostFitRegistration {
		r := postFitEcho()
		f(&r)
		return []PostFitRegistration{r}
	}
	cases := []struct {
		name   string
		regs   []PostFitRegistration
		code   perr.Code
		reason string
	}{
		{"bad pattern", edit(func(r *PostFitRegistration) { r.Name = "POSTFIT_ECHO" }), perr.PULSE_EXTENSION_NAME_INVALID, ""},
		{"wrong prefix", edit(func(r *PostFitRegistration) { r.Name = "OVERLAY_TEST_ECHO" }), perr.PULSE_EXTENSION_NAME_INVALID, ""},
		{"reserved namespace", edit(func(r *PostFitRegistration) { r.Name = "POSTFIT_PULSE_ECHO" }), perr.PULSE_EXTENSION_NAME_RESERVED, ""},
		{"duplicate", []PostFitRegistration{postFitEcho(), postFitEcho()}, perr.PULSE_EXTENSION_DUPLICATE, ""},
		{"nil run", edit(func(r *PostFitRegistration) { r.Run = nil }), perr.PULSE_EXTENSION_PARAM_INVALID, "missing_factory"},
		{"empty schema", edit(func(r *PostFitRegistration) { r.Outputs = extend.PostFitSchema{} }), perr.PULSE_EXTENSION_PARAM_INVALID, "empty_output_schema"},
		{"bad column kind", edit(func(r *PostFitRegistration) { r.Outputs.Tables[0].Columns[0].Kind = "int" }), perr.PULSE_EXTENSION_PARAM_INVALID, "invalid_column_kind"},
		{"expression param", edit(func(r *PostFitRegistration) {
			r.Params = []ParamMeta{{Name: "k", JSONType: "number", Expression: true}}
		}), perr.PULSE_EXTENSION_PARAM_INVALID, "expression_unsupported"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(Options{FS: parityFS(t), Extensions: Extensions{PostFits: tc.regs}})
			ce := requireCoded(t, err, tc.code)
			if tc.reason != "" && ce.Details["reason"] != tc.reason {
				t.Fatalf("details %+v, want reason %s", ce.Details, tc.reason)
			}
			if !strings.Contains(ce.Message, "post_fit") && tc.reason != "" {
				t.Fatalf("message %q does not name the category", ce.Message)
			}
		})
	}
	// Two Extensions values merged collide like any category.
	_, err := New(Options{FS: parityFS(t), Extensions: mergeExtensions([]Extensions{{PostFits: []PostFitRegistration{postFitEcho()}}, {PostFits: []PostFitRegistration{postFitEcho()}}})})
	requireCoded(t, err, perr.PULSE_EXTENSION_DUPLICATE)
}
