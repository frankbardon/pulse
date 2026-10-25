package mmmpulse

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/mmmfixture"
	"github.com/frankbardon/pulse/mcpserve"
	"github.com/frankbardon/pulse/types"
)

// passbar_test.go is the MMM pass bar (mmm-estimation E4-S2): on the
// internal/mmmfixture joint-truth cohort (one market, 156 weeks, four
// channels with known decays and Hill curves) the canonical two-call
// flow — (1) the lhs sweep over the four decays, REG_MMM_HILL per slot,
// ranked by holdout RMSE; (2) `fits` at the winning decays plus the
// budget `optimize` block — must
//
//   - pick every decay within ±passDecayTol of the truth;
//   - give every channel's ROI within ±passROITol (relative) of the
//     true ROI;
//   - converge, with every channel strictly inside its bounds at a
//     marginal ROI equal to the budget multiplier λ within passMROITol
//     (relative), the active set the oracle's;
//   - predict a lift within passLiftTol (relative) of the lift of the
//     optimum on the true curves (mmmfixture.TrueOptimum, an oracle
//     independent of Pulse's solver).
//
// It runs through library ComposeParallel (TestMMMPack_PassBar) and
// through MCP pulse_compose served by mcpserve with the pack registered
// (TestMMMPack_PassBarMCP, serial — so its byte-identity with the
// library run is the serial ≡ parallel check at full size).

// The pass bar. Never loosen these: tune the fixture instead.
const (
	passDecayTol = 0.15
	passROITol   = 0.20
	passLiftTol  = 0.25
	// passMROITol bounds |∂f/∂xᵢ − λ| / |λ| on every free channel. The
	// marginals and the multiplier come from the solver's
	// finite-difference gradient at its stop (tolerance 1e-6); measured
	// within 7.8e-6.
	passMROITol = 1e-4
)

var passBarSeeds = flag.Int("mmm.passbar-seeds", 0,
	"also run the MMM pass bar with sweep rng_seed 1..N (evidence across seeds; about 5 s each)")

// jointInstance is an instance with the pack registered and the slot
// budget raised to the canonical sweep's samples, over a fresh
// filesystem holding the joint-truth cohort.
func jointInstance(t testing.TB) (*pulse.Pulse, mmmfixture.JointData) {
	t.Helper()
	p := instance(t, afero.NewMemMapFs(), func(o *pulse.Options) { o.Limits.MaxComposeSlots = mmmfixture.JointLHSSamples })
	d := mmmfixture.Joint()
	b, err := p.NewCohortBuilder(context.Background(), mmmfixture.JointCohortFile, mmmfixture.JointSchema(), pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range d.Rows {
		if err := b.Append(pulse.CohortRow(r)); err != nil {
			t.Fatalf("Append row %d: %v", i, err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	return p, d
}

// decode decodes a request body.
func decode(t testing.TB, body []byte) *pulse.ComposedRequest {
	t.Helper()
	var req pulse.ComposedRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	return &req
}

// sweepBody is call 1, its rng_seed set to seed and its samples to n
// when non-zero.
func sweepBody(t testing.TB, seed int64, n int) *pulse.ComposedRequest {
	t.Helper()
	req := decode(t, mmmfixture.JointLHSSweep)
	req.Sweep.RNGSeed = seed
	if n > 0 {
		req.Sweep.Samples = n
	}
	return req
}

// winningDecays reads the decays off the best-ranked slot's default
// label (sample=<i>_tv=<v>_search=<v>_social=<v>_display=<v>), checking
// that slot's fit succeeded.
func winningDecays(t testing.TB, out *pulse.ComposedResponse) map[string]float64 {
	t.Helper()
	if len(out.Ranking) == 0 {
		t.Fatal("the sweep ranked no slot")
	}
	label := out.Ranking[0].Label
	for _, r := range out.Responses {
		if r.Label == label && (len(r.Regressions) != 1 || r.Regressions[0].Error != nil || r.Regressions[0].Holdout == nil) {
			t.Fatalf("winning slot %s: regressions %s", label, mustJSON(t, r.Regressions))
		}
	}
	got := map[string]float64{}
	for kv := range strings.SplitSeq(label, "_") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "sample" {
			continue
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			t.Fatalf("label %q: %v", label, err)
		}
		got[k] = f
	}
	if len(got) != len(mmmfixture.JointChannels) {
		t.Fatalf("label %q carries %d decays", label, len(got))
	}
	return got
}

// secondCall is call 2 filled for the winning decays.
func secondCall(t testing.TB, d mmmfixture.JointData, decays map[string]float64) []byte {
	t.Helper()
	body, err := mmmfixture.Fill(mmmfixture.JointFitOptimize, d.JointVars(decays))
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// passBar checks call 2's result (at the winning decays) against the
// bar, logging the achieved figures, and returns every violation (none:
// met).
func passBar(t testing.TB, d mmmfixture.JointData, decays map[string]float64, out *pulse.ComposedResponse) (violations []string) {
	t.Helper()
	bad := func(format string, a ...any) { violations = append(violations, fmt.Sprintf(format, a...)) }
	fit := out.Fits["mmm"]
	if fit == nil || fit.RegressionResult == nil || fit.Error != nil {
		return []string{"the final fit failed: " + mustJSON(t, fit)}
	}
	coef := fit.Coefficients
	if len(out.Responses) != 1 || len(out.Responses[0].Data) != mmmfixture.JointWeeks {
		return []string{"the contribution slot has no weekly rows"}
	}
	fitCur := 0.0 // the fitted response at the current spend
	for _, c := range mmmfixture.JointChannels {
		a := c.Name + "_ad"
		if e := decays[c.Name] - c.Decay; math.Abs(e) > passDecayTol {
			bad("%s decay %.3f, truth %.2f (error %+.3f, bar ±%.2f)", c.Name, decays[c.Name], c.Decay, e, passDecayTol)
		}
		contrib, spend := 0.0, 0.0
		for _, row := range out.Responses[0].Data {
			contrib += row[c.Name+"_contrib"].(float64)
			spend += row[c.Name].(float64)
		}
		roi, truth := contrib/spend, d.TrueROI(c.Name)
		if e := roi/truth - 1; math.Abs(e) > passROITol {
			bad("%s ROI %.3f, truth %.3f (error %+.1f%%, bar ±%.0f%%)", c.Name, roi, truth, 100*e, 100*passROITol)
		}
		t.Logf("%-8s decay %.3f (truth %.2f, %+.3f)  ROI %.3f (truth %.3f, %+.1f%%)",
			c.Name, decays[c.Name], c.Decay, decays[c.Name]-c.Decay, roi, truth, 100*(roi/truth-1))
		fitCur += coef[a] * mmmfixture.JointHill(mmmfixture.SteadyState(decays[c.Name])*d.MeanSpend(c.Name), coef["k:"+a], coef["s:"+a])
	}

	if len(out.Optimizations) != 1 || out.Optimizations[0].Error != nil || len(out.Optimizations[0].Constraints) != 1 {
		return append(violations, "the optimize block failed: "+mustJSON(t, out.Optimizations))
	}
	o := out.Optimizations[0]
	if !o.Converged {
		bad("the optimize block did not converge: %s", o.Status)
	}
	if 2*o.StartsAgreeing <= o.Starts {
		bad("only %d of %d starts agree", o.StartsAgreeing, o.Starts)
	}
	lambda := o.Constraints[0].Multiplier
	oracle, oracleLambda, trueLift := d.TrueOptimum()
	v := d.JointVars(decays)
	free := 0
	for _, x := range o.Variables {
		want := types.OptimizeActiveBound("")
		switch at := oracle[x.Name]; {
		case math.Abs(at-v["lo_"+x.Name]) <= 1e-9*at:
			want = types.OptimizeActiveLower
		case math.Abs(at-v["hi_"+x.Name]) <= 1e-9*at:
			want = types.OptimizeActiveUpper
		}
		if x.Active != want {
			bad("%s active %q at %.2f, the true optimum's %q at %.2f", x.Name, x.Active, x.Value, want, oracle[x.Name])
		}
		if x.Active != "" {
			continue
		}
		free++
		e := math.Abs(x.Marginal-lambda) / math.Abs(lambda)
		if !(e < passMROITol) {
			bad("%s marginal ROI %.6f vs λ %.6f (relative %.1e, bar %.0e)", x.Name, x.Marginal, lambda, e, passMROITol)
		}
		t.Logf("%-8s free at %.2f (true optimum %.2f): marginal ROI %.6f, λ %.6f (relative %.1e)", x.Name, x.Value, oracle[x.Name], x.Marginal, lambda, e)
	}
	if free < 2 {
		bad("%d free channels: the mROI equalisation is vacuous", free)
	}
	lift := o.Objective - fitCur
	if e := lift/trueLift - 1; !(math.Abs(e) <= passLiftTol) {
		bad("predicted lift %.3f, the true-curve optimum's %.3f (error %+.1f%%, bar ±%.0f%%)", lift, trueLift, 100*e, 100*passLiftTol)
	}
	t.Logf("lift %.3f vs true %.3f (%+.1f%%); λ %.4f vs true %.4f; converged %v, %d/%d starts agree",
		lift, trueLift, 100*(lift/trueLift-1), lambda, oracleLambda, o.Converged, o.StartsAgreeing, o.Starts)
	return violations
}

// composeFn runs one compose batch.
type composeFn func(*pulse.ComposedRequest) (*pulse.ComposedResponse, error)

func parallel(p *pulse.Pulse) composeFn {
	return func(r *pulse.ComposedRequest) (*pulse.ComposedResponse, error) {
		return p.ComposeParallel(context.Background(), r, pulse.ComposeOptions{MaxWorkers: 8, FailFast: true})
	}
}

func serial(p *pulse.Pulse) composeFn {
	return func(r *pulse.ComposedRequest) (*pulse.ComposedResponse, error) {
		return p.Compose(context.Background(), r)
	}
}

// twoCall runs the flow: call 1, then call 2 at its winning decays.
func twoCall(t testing.TB, d mmmfixture.JointData, call1 *pulse.ComposedRequest, compose composeFn) (sweep, final *pulse.ComposedResponse, decays map[string]float64) {
	t.Helper()
	sweep, err := compose(call1)
	if err != nil {
		t.Fatalf("call 1: %v", err)
	}
	decays = winningDecays(t, sweep)
	final, err = compose(decode(t, secondCall(t, d, decays)))
	if err != nil {
		t.Fatalf("call 2: %v", err)
	}
	return sweep, final, decays
}

// canonical is the canonical two-call flow through library
// ComposeParallel, run once per test binary and shared by the library
// and MCP passes (each on its own instance; the bytes are a function of
// the bodies and the cohort alone).
var canonical struct {
	once         sync.Once
	sweep, final *pulse.ComposedResponse
	decays       map[string]float64
	elapsed      time.Duration
	failed       bool
}

func canonicalRun(t *testing.T) (sweep, final *pulse.ComposedResponse, decays map[string]float64) {
	t.Helper()
	canonical.once.Do(func() {
		canonical.failed = true
		p, d := jointInstance(t)
		start := time.Now()
		canonical.sweep, canonical.final, canonical.decays = twoCall(t, d, sweepBody(t, 0, 0), parallel(p))
		canonical.elapsed, canonical.failed = time.Since(start), false
	})
	if canonical.failed {
		t.Fatal("the canonical two-call flow failed (see the first test that ran it)")
	}
	return canonical.sweep, canonical.final, canonical.decays
}

// TestMMMPack_PassBar: the canonical two-call flow through library
// ComposeParallel meets the pass bar; predict validates both calls,
// counting the sweep's samples. With -mmm.passbar-seeds=N the bar must
// also hold for sweep seeds 1..N.
//
// Measured over the canonical body and sweep seeds 1..12 (2,000
// samples; arm64, and amd64 for the canonical body and seeds 1..4 with
// identical figures): worst decay error 0.103 (display), worst ROI
// error 6.3% (social), free-channel marginals within 7.8e-6 of λ, worst
// lift error 13.7%, all 8 starts agreeing on every run. At 400 samples
// seeds 1..8 reach 0.133 and 21.9%: the bars are met by search density,
// so the canonical body samples 2,000.
func TestMMMPack_PassBar(t *testing.T) {
	p, d := jointInstance(t)
	_, final, decays := canonicalRun(t)
	t.Logf("two-call flow, %d samples, ComposeParallel: %v", mmmfixture.JointLHSSamples, canonical.elapsed)
	for _, v := range passBar(t, d, decays, final) {
		t.Error(v)
	}

	for i, body := range [][]byte{mmmfixture.JointLHSSweep, secondCall(t, d, decays)} {
		env, err := p.PredictCompose(context.Background(), decode(t, body))
		if err != nil || len(env.Errors) != 0 {
			t.Fatalf("predict call %d: %v %+v", i+1, err, env)
		}
		cv, ok := env.Data.(*descx.ComposeValidationResult)
		if !ok || !cv.Valid {
			t.Fatalf("predict call %d: %+v", i+1, env.Data)
		}
		if i == 0 && (cv.Sweep == nil || cv.Sweep.ExpandedCount != mmmfixture.JointLHSSamples) {
			t.Fatalf("predict call 1 sweep %+v, want %d slots", cv.Sweep, mmmfixture.JointLHSSamples)
		}
	}

	for seed := int64(1); seed <= int64(*passBarSeeds); seed++ {
		t.Logf("sweep rng_seed %d:", seed)
		_, final, decays := twoCall(t, d, sweepBody(t, seed, 0), parallel(p))
		for _, v := range passBar(t, d, decays, final) {
			t.Errorf("rng_seed %d: %s", seed, v)
		}
	}
}

// TestMMMPack_PassBarDeterministic: the same seed gives the same bytes.
// A 200-sample sweep and its call 2 give byte-identical results serial
// and parallel, on a rerun, and with the echoed sweep_seed pinned as
// rng_seed (the full-size serial ≡ parallel check is the MCP pass).
func TestMMMPack_PassBarDeterministic(t *testing.T) {
	p, d := jointInstance(t)
	sweep, final, _ := twoCall(t, d, sweepBody(t, 0, 200), parallel(p))
	if sweep.SweepSeed == nil {
		t.Fatal("no sweep_seed echoed")
	}
	want := mustJSON(t, sweep) + mustJSON(t, final)
	for name, run := range map[string]func() (*pulse.ComposedResponse, *pulse.ComposedResponse){
		"serial": func() (*pulse.ComposedResponse, *pulse.ComposedResponse) {
			s, f, _ := twoCall(t, d, sweepBody(t, 0, 200), serial(p))
			return s, f
		},
		"parallel rerun": func() (*pulse.ComposedResponse, *pulse.ComposedResponse) {
			s, f, _ := twoCall(t, d, sweepBody(t, 0, 200), parallel(p))
			return s, f
		},
		"pinned seed": func() (*pulse.ComposedResponse, *pulse.ComposedResponse) {
			s, f, _ := twoCall(t, d, sweepBody(t, *sweep.SweepSeed, 200), parallel(p))
			return s, f
		},
	} {
		s, f := run()
		if got := mustJSON(t, s) + mustJSON(t, f); got != want {
			t.Errorf("%s: bytes differ from the first run", name)
		}
	}
}

// mcpClient speaks newline-delimited JSON-RPC to an mcpserve.Serve
// loop over in-memory pipes.
type mcpClient struct {
	in  io.WriteCloser
	out *bufio.Reader
	id  int
}

func startMCP(t *testing.T, p *pulse.Pulse) *mcpClient {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = mcpserve.Serve(ctx, p, mcpserve.Options{DisableCohortScan: true}, inR, outW)
	}()
	t.Cleanup(func() {
		cancel()
		_ = inW.Close()
		_ = outW.Close()
		<-done
	})
	c := &mcpClient{in: inW, out: bufio.NewReader(outR)}
	c.rpc(t, "initialize", `{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"mmmpulse-test","version":"0"}}`)
	if _, err := io.WriteString(inW, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n"); err != nil {
		t.Fatal(err)
	}
	return c
}

// rpc sends one request and returns its result.
func (c *mcpClient) rpc(t *testing.T, method, params string) json.RawMessage {
	t.Helper()
	c.id++
	msg := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%q,"params":%s}`, c.id, method, params)
	if _, err := io.WriteString(c.in, msg+"\n"); err != nil {
		t.Fatal(err)
	}
	for {
		line, err := c.out.ReadBytes('\n')
		if err != nil {
			t.Fatalf("%s: read: %v", method, err)
		}
		var resp struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(line, &resp); err != nil {
			t.Fatalf("%s: %v in %s", method, err, line)
		}
		if resp.ID == nil || *resp.ID != c.id {
			continue // a notification
		}
		if resp.Error != nil {
			t.Fatalf("%s: %s", method, resp.Error)
		}
		return resp.Result
	}
}

// compose calls pulse_compose with body and returns the result text.
func (c *mcpClient) compose(t *testing.T, body []byte) []byte {
	t.Helper()
	res := c.rpc(t, "tools/call", `{"name":"pulse_compose","arguments":`+string(body)+`}`)
	var r struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(res, &r); err != nil || len(r.Content) != 1 || r.IsError {
		t.Fatalf("pulse_compose: %v %.2000s", err, res)
	}
	return []byte(r.Content[0].Text)
}

// TestMMMPack_PassBarMCP: an MCP client of mcpserve over an instance
// with the pack registered runs the canonical two-call flow through
// pulse_compose and meets the pass bar; both results are byte for byte
// the library ComposeParallel results — MCP compose runs serially, so
// this is also the full-size serial ≡ parallel check.
func TestMMMPack_PassBarMCP(t *testing.T) {
	p, d := jointInstance(t)
	c := startMCP(t, p)
	start := time.Now()
	sweepText := c.compose(t, mmmfixture.JointLHSSweep)
	var sweep pulse.ComposedResponse
	if err := json.Unmarshal(sweepText, &sweep); err != nil {
		t.Fatal(err)
	}
	decays := winningDecays(t, &sweep)
	finalText := c.compose(t, secondCall(t, d, decays))
	var final pulse.ComposedResponse
	if err := json.Unmarshal(finalText, &final); err != nil {
		t.Fatal(err)
	}
	t.Logf("two-call flow over MCP (serial): %v", time.Since(start))
	for _, v := range passBar(t, d, decays, &final) {
		t.Error(v)
	}

	libSweep, libFinal, _ := canonicalRun(t)
	for name, pair := range map[string][2][]byte{
		"call 1": {sweepText, []byte(mustJSON(t, libSweep))},
		"call 2": {finalText, []byte(mustJSON(t, libFinal))},
	} {
		var got bytes.Buffer
		if err := json.Compact(&got, pair[0]); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got.Bytes(), pair[1]) {
			t.Errorf("%s: the MCP result differs from the library's (%d vs %d bytes)", name, got.Len(), len(pair[1]))
		}
	}
}
