package pulse

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/optfixture"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// compose_optimize_solve_test.go pins the Compose `optimize` runtime
// (mmm-estimation E3-S2) at the facade: a price optimisation on a fitted
// demand curve and on a saved model reaches the analytic optimum, a
// hill() budget allocation equalises the free channels' marginals with
// the budget multiplier, a block that fails on its numbers fails alone
// (Strict: fatal with its own code), and the bytes are the same serial,
// parallel and run after run.

// optFixtureFS is the optfixture cohort plus its saved demand model.
func optFixtureFS(t *testing.T) afero.Fs {
	t.Helper()
	fsys := afero.NewMemMapFs()
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.NewCohortBuilder(context.Background(), optfixture.CohortFile, optfixture.Schema(), CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range optfixture.Rows() {
		if err := b.Append(CohortRow(r)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	mf, err := optfixture.ModelFile()
	if err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fsys, optfixture.ModelPath, mf, 0o644); err != nil {
		t.Fatal(err)
	}
	return fsys
}

func optPulse(t *testing.T, opts Options) *Pulse {
	t.Helper()
	opts.FS = optFixtureFS(t)
	p, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// decodeOpt decodes a fixture body, optionally edited first.
func decodeOpt(t *testing.T, raw []byte, edit func(*ComposedRequest)) *ComposedRequest {
	t.Helper()
	c := decodeComposedJSON(t, string(raw))
	if edit != nil {
		edit(c)
	}
	return c
}

// oneOptimum is the single block's converged result, or a failure.
func oneOptimum(t *testing.T, name string, resp *ComposedResponse) types.OptimizeResult {
	t.Helper()
	if len(resp.Optimizations) != 1 || resp.Optimizations[0].Error != nil || !resp.Optimizations[0].Converged {
		t.Fatalf("%s: optimizations %+v", name, resp.Optimizations)
	}
	return resp.Optimizations[0]
}

// TestOptimize_PriceOnFittedDemand: maximising (p − cost)·(a + b·p)
// over the fitted `demand` curve lands on the analytic optimum
// p* = (b·cost − a) / 2b of the fit's own coefficients with a zero
// marginal, every start agreeing, the pinned seed echoed — on both
// orchestrators.
func TestOptimize_PriceOnFittedDemand(t *testing.T) {
	p := optPulse(t, Options{})
	for name, run := range composeRuns(p) {
		resp, err := run(decodeOpt(t, optfixture.Price, nil))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		coefs := resp.Fits["demand"].RegressionResult.Coefficients
		a, b := coefs["(intercept)"], coefs["price"]
		want := (b*optfixture.Cost - a) / (2 * b)
		got := oneOptimum(t, name, resp)
		v := got.Variables[0]
		// The objective is quadratic, so SLSQP's quadratic model is exact
		// up to rounding: measured |p − p*| = 6.1e-12 and marginal 0
		// (FMA-free, the same on every architecture); 1e-9 is that with
		// room.
		if math.Abs(v.Value-want) > 1e-9 || math.Abs(v.Marginal) > 1e-6 || v.Active != "" {
			t.Errorf("%s: p %+v, want %v free with a zero marginal", name, v, want)
		}
		if f := (want - optfixture.Cost) * (a + b*want); math.Abs(got.Objective-f) > 1e-9*f {
			t.Errorf("%s: f* %v, want %v", name, got.Objective, f)
		}
		if got.Starts != 4 || got.StartsAgreeing != 4 || got.RNGSeed != 7 || got.Direction != types.OptimizeMaximize || got.Name != "price" {
			t.Errorf("%s: %+v", name, got)
		}
	}
}

// TestOptimize_PriceFromSavedModel: the same block over the saved model
// file (`optimize[0].models`) optimises without any fit — no `fits` in
// the response — and lands on the model's analytic optimum; predict
// accepts it.
func TestOptimize_PriceFromSavedModel(t *testing.T) {
	p := optPulse(t, Options{})
	for name, run := range composeRuns(p) {
		resp, err := run(decodeOpt(t, optfixture.PriceModel, nil))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if resp.Fits != nil {
			t.Fatalf("%s: a saved-model batch ran fits %v", name, resp.Fits)
		}
		got := oneOptimum(t, name, resp)
		if v := got.Variables[0]; math.Abs(v.Value-optfixture.ModelOptimum) > 1e-9 {
			t.Errorf("%s: p %v, want %v", name, v.Value, optfixture.ModelOptimum)
		}
	}
	env, err := p.PredictCompose(context.Background(), decodeOpt(t, optfixture.PriceModel, nil))
	if err != nil || len(env.Errors) != 0 {
		t.Fatalf("predict: %v %+v", err, env)
	}
}

// TestOptimize_ModelsParity: every refusal of a block's `models` —
// a missing file, a name the batch's fit binds, a term the loaded model
// lacks — is refused by predict exactly as by both orchestrators (code,
// message, details incl. details.optimize / field), before any fit or
// slot runs.
func TestOptimize_ModelsParity(t *testing.T) {
	p := optPulse(t, Options{})
	ctx := context.Background()
	cases := []struct {
		name string
		edit func(c *ComposedRequest)
		code perr.Code
	}{
		{"missing file", func(c *ComposedRequest) { c.Optimize[0].Models[0].Path = "models/gone.pulse-model.json" }, perr.PULSE_MODEL_FILE_UNREADABLE},
		{"collides with a fit", func(c *ComposedRequest) {
			c.Fits = decodeOpt(t, optfixture.Price, nil).Fits
		}, perr.PULSE_MODEL_NAME_COLLISION},
		{"unknown term", func(c *ComposedRequest) { c.Optimize[0].Objective = `coef("demand", "region") * p` }, perr.PULSE_MODEL_TERM_UNKNOWN},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, err := p.PredictCompose(ctx, decodeOpt(t, optfixture.PriceModel, tc.edit))
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(tc.code) || env.Errors[0].Details["optimize"] != 0 {
				t.Fatalf("predict %+v, want %s at the block", env.Errors, tc.code)
			}
			for name, run := range composeRuns(p) {
				resp, rerr := run(decodeOpt(t, optfixture.PriceModel, tc.edit))
				ce := requireCoded(t, rerr, tc.code)
				if resp != nil || env.Errors[0].Message != ce.Message || !reflect.DeepEqual(env.Errors[0].Details, ce.Details) {
					t.Fatalf("%s: predict %+v, runtime %s %v", name, env.Errors[0], ce.Message, ce.Details)
				}
			}
		})
	}
}

// TestOptimize_HiddenModelsParity: an instance hiding capability:models
// refuses a block's `models` as an unknown field (details.optimize) on
// predict and both orchestrators alike.
func TestOptimize_HiddenModelsParity(t *testing.T) {
	h := newParityHostWith(t, optFixtureFS(t), "no-models", parityHostConfig{
		profiles: map[string][]string{"no-models": {"capability:compose", "capability:compose_optimize", "capability:params", string(types.AGG_COUNT)}},
	})
	ctx := context.Background()
	_, err := h.p.Compose(ctx, decodeOpt(t, optfixture.PriceModel, nil))
	serial := slotGateFromErr(err)
	if serial == nil || serial.code != perr.PULSE_REQUEST_UNKNOWN_FIELD || !reflect.DeepEqual(toStrings(serial.details["unknown_keys"]), []string{"models"}) || serial.details["optimize"] != 0 {
		t.Fatalf("Compose: %+v (%v)", serial, err)
	}
	_, err = h.p.ComposeParallel(ctx, decodeOpt(t, optfixture.PriceModel, nil), ComposeOptions{MaxWorkers: 2})
	if par := slotGateFromErr(err); par == nil || !reflect.DeepEqual(par, serial) {
		t.Fatalf("ComposeParallel %+v, Compose %+v", par, serial)
	}
	env, err := h.p.PredictCompose(ctx, decodeOpt(t, optfixture.PriceModel, nil))
	if err != nil {
		t.Fatal(err)
	}
	if pred := slotGateFromEnv(env); pred == nil || pred.code != serial.code || pred.details["optimize"] != 0 ||
		!reflect.DeepEqual(toStrings(pred.details["unknown_keys"]), []string{"models"}) {
		t.Fatalf("PredictCompose %+v, Compose %+v", pred, serial)
	}
}

// allocationTol is the mROI equalisation tolerance of the budget
// fixture: |∂f/∂xᵢ − μ| ≤ allocationTol·max(1, |μ|) on every channel not
// at a bound. Measured: |Δ| ≤ 3.8e-6 (tv 0.6453081, search 0.6453027,
// μ 0.6453064) at the default tolerance 1e-6 — the solver stops on a
// 1e-6 objective change, which leaves the optimum good to about
// sqrt(1e-6) in x and so the marginals to a few 1e-6; 1e-4 is that
// with room, and still 450 times tighter than the gap to the bound
// channels (social +0.80, display −0.045).
const allocationTol = 1e-4

// TestOptimize_BudgetEqualisesMarginals: the hill() budget allocation
// spends exactly the budget, every channel not at a bound has its
// marginal return equal to the budget multiplier (mROI equalised), a
// channel capped at its upper bound returns more than the multiplier
// and one at zero less — on both orchestrators.
func TestOptimize_BudgetEqualisesMarginals(t *testing.T) {
	p := optPulse(t, Options{})
	for name, run := range composeRuns(p) {
		resp, err := run(decodeOpt(t, optfixture.Budget, nil))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := oneOptimum(t, name, resp)
		mu := got.Constraints[0].Multiplier
		sum, free := 0.0, 0
		for _, v := range got.Variables {
			sum += v.Value
			if math.IsNaN(v.Marginal) {
				t.Fatalf("%s: %s marginal is NaN (a stencil left the bounds)", name, v.Name)
			}
			switch v.Active {
			case "":
				free++
				if d := math.Abs(v.Marginal - mu); d > allocationTol*math.Max(1, math.Abs(mu)) {
					t.Errorf("%s: %s marginal %v vs multiplier %v (|Δ| %g)", name, v.Name, v.Marginal, mu, d)
				}
			case types.OptimizeActiveUpper:
				if v.Marginal <= mu {
					t.Errorf("%s: %s capped with marginal %v <= multiplier %v", name, v.Name, v.Marginal, mu)
				}
			case types.OptimizeActiveLower:
				if v.Marginal >= mu {
					t.Errorf("%s: %s at zero with marginal %v >= multiplier %v", name, v.Name, v.Marginal, mu)
				}
			}
		}
		if free < 2 || math.Abs(sum-120) > 1e-9 || got.MaxViolation > 1e-9 || mu <= 0 {
			t.Errorf("%s: %d free channels, spend %v, violation %v, multiplier %v", name, free, sum, got.MaxViolation, mu)
		}
		active := map[string]types.OptimizeActiveBound{}
		for _, v := range got.Variables {
			active[v.Name] = v.Active
		}
		if want := map[string]types.OptimizeActiveBound{"tv": "", "search": "", "social": "upper", "display": "lower"}; !reflect.DeepEqual(active, want) {
			t.Errorf("%s: active bounds %v, want %v", name, active, want)
		}
	}
}

// TestOptimize_ByteIdentical: the same request gives the same bytes run
// after run and on both orchestrators; the derived seed is echoed and a
// pinned one replays it.
func TestOptimize_ByteIdentical(t *testing.T) {
	p := optPulse(t, Options{})
	enc := func(run func(*ComposedRequest) (*ComposedResponse, error), raw []byte, edit func(*ComposedRequest)) []byte {
		resp, err := run(decodeOpt(t, raw, edit))
		if err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	runs := composeRuns(p)
	for _, raw := range [][]byte{optfixture.Price, optfixture.PriceModel, optfixture.Budget} {
		want := enc(runs["Compose"], raw, nil)
		for name, run := range runs {
			for k := 0; k < 2; k++ {
				if got := enc(run, raw, nil); !bytes.Equal(got, want) {
					t.Fatalf("%s run %d differs:\n%s\n%s", name, k, got, want)
				}
			}
		}
	}
	derived := decodeOpt(t, optfixture.Budget, nil)
	resp, err := p.Compose(context.Background(), derived)
	if err != nil {
		t.Fatal(err)
	}
	seed := resp.Optimizations[0].RNGSeed
	if seed == 0 {
		t.Fatal("no effective seed echoed")
	}
	pinned := enc(runs["Compose"], optfixture.Budget, func(c *ComposedRequest) { c.Optimize[0].RNGSeed = seed })
	if want := enc(runs["Compose"], optfixture.Budget, nil); !bytes.Equal(pinned, want) {
		t.Fatalf("pinning the echoed seed changed the result:\n%s\n%s", pinned, want)
	}
}

// failingBlocks is the budget batch plus an infeasible block and one
// capped at a single iteration, after the good block.
func failingBlocks(t *testing.T) func(*ComposedRequest) {
	extra := decodeComposedJSON(t, `{"requests":[],"optimize":[
		{"name":"infeasible","direction":"minimize","variables":[{"name":"x","lower":0,"upper":10}],"objective":"x * x",
		 "constraints":[{"expr":"x - 5","type":"ineq"},{"expr":"2 - x","type":"ineq"}],"starts":2},
		{"name":"capped","direction":"minimize","variables":[{"name":"x","lower":-3,"upper":3},{"name":"y","lower":-3,"upper":3}],
		 "objective":"(1 - x) * (1 - x) + 100 * (y - x * x) * (y - x * x)","max_iterations":1,"starts":2}]}`)
	return func(c *ComposedRequest) { c.Optimize = append(c.Optimize, extra.Optimize...) }
}

// TestOptimize_FailsAlone: a block that fails on its numbers keeps its
// identity and error {code, message, details} with every figure absent,
// warns PULSE_SLOT_FAILED (slot_kind optimizations, the block index and
// name), and leaves the other blocks and the slots whole; under Strict
// the batch fails with the first failing block's own code.
func TestOptimize_FailsAlone(t *testing.T) {
	p := optPulse(t, Options{})
	for name, run := range composeRuns(p) {
		resp, err := run(decodeOpt(t, optfixture.Budget, failingBlocks(t)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(resp.Responses) != 1 || resp.Responses[0] == nil || len(resp.Optimizations) != 3 || resp.Optimizations[0].Error != nil {
			t.Fatalf("%s: responses %d, optimizations %+v", name, len(resp.Responses), resp.Optimizations)
		}
		for k, code := range map[int]perr.Code{1: perr.PULSE_OPTIMIZE_INFEASIBLE, 2: perr.PULSE_OPTIMIZE_NOT_CONVERGED} {
			r := resp.Optimizations[k]
			if r.Error == nil || r.Error.Code != string(code) || r.Error.Details["optimize"] != k {
				t.Fatalf("%s: block %d %+v, want %s", name, k, r, code)
			}
			wire, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.HasPrefix(wire, []byte(`{"name":"`+r.Name+`","direction":"minimize","error":{"code":"`+string(code)+`"`)) ||
				bytes.Contains(wire, []byte(`"variables"`)) || bytes.Contains(wire, []byte(`"objective"`)) {
				t.Fatalf("%s: failed block wire %s", name, wire)
			}
		}
		var warned []int
		for _, w := range resp.Warnings {
			if w.Code == string(perr.PULSE_SLOT_FAILED) && w.Details["slot_kind"] == "optimizations" {
				warned = append(warned, w.Details["index"].(int))
				if !strings.Contains(w.Message, resp.Optimizations[w.Details["index"].(int)].Name) {
					t.Errorf("%s: warning %q names no block", name, w.Message)
				}
			}
		}
		if !reflect.DeepEqual(warned, []int{1, 2}) {
			t.Fatalf("%s: slot-failed warnings %v (%+v)", name, warned, resp.Warnings)
		}
	}
	strict := optPulse(t, Options{Strict: true})
	for name, run := range composeRuns(strict) {
		resp, err := run(decodeOpt(t, optfixture.Budget, failingBlocks(t)))
		ce := requireCoded(t, err, perr.PULSE_OPTIMIZE_INFEASIBLE)
		if resp != nil || ce.Details["slot_kind"] != "optimizations" || ce.Details["index"] != 1 || ce.Details["label"] != "infeasible" {
			t.Fatalf("%s strict: %v %+v", name, ce, ce.Details)
		}
	}
}

// TestOptimize_FailedFitBlockFailsAlone: a block whose coef() reads a
// Compose fit that failed on its data fails alone with the fit's own
// code (the fit's failure warns too); Strict fails the batch at the fit.
func TestOptimize_FailedFitBlockFailsAlone(t *testing.T) {
	empty := func(c *ComposedRequest) {
		c.Fits[0].Request.Filterers = []*types.Filterer{{Type: types.FILTER_RANGE, Field: "week", Values: []string{"1000", "2000"}}}
	}
	p := optPulse(t, Options{})
	for name, run := range composeRuns(p) {
		resp, err := run(decodeOpt(t, optfixture.Price, empty))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		fitErr := resp.Fits["demand"].RegressionResult.Error
		r := resp.Optimizations[0]
		if fitErr == nil || r.Error == nil || r.Error.Code != fitErr.Code || r.Error.Details["fit"] != "demand" || r.Error.Details["optimize"] != 0 {
			t.Fatalf("%s: fit %+v, block %+v", name, fitErr, r.Error)
		}
		n := 0
		for _, w := range resp.Warnings {
			if w.Code == string(perr.PULSE_SLOT_FAILED) {
				n++
			}
		}
		if n != 2 {
			t.Fatalf("%s: %d slot-failed warnings, want the fit's and the block's: %+v", name, n, resp.Warnings)
		}
	}
}
