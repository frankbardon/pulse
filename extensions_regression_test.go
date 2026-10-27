package pulse_test

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// Extension regressions (U53 E1-S1): an embedder registers a REG_*
// model — streaming (extend.StreamingRegression) or buffered
// (extend.BufferedRegression) — and it runs through Process like a
// built-in, its narrow extend.RegressionOutput mapped onto
// types.RegressionResult by the engine.

const (
	regStreamName   types.RegressionType = "REG_ACME_SLOPE_ON"
	regBufferedName types.RegressionType = "REG_ACME_SLOPE_OFF"
	regAwareName    types.RegressionType = "REG_ACME_SLOPE_WT"
	regVcovName     types.RegressionType = "REG_ACME_SLOPE_VC"
)

// regCalls counts the drive a test model saw.
type regCalls struct {
	update, merge, finalize, fit atomic.Int64
	// weighted counts rows that reported a weight; unweighted the rest.
	weighted, unweighted atomic.Int64
}

// slopeAcc is a one-predictor weighted least-squares fold of target on
// the spec's first predictor.
type slopeAcc struct {
	target, x                 string
	sw, sx, sy, sxx, sxy, syy float64
}

func (a *slopeAcc) add(rec extend.Record, calls *regCalls) {
	w := 1.0
	if v, ok := rec.Weight(); ok {
		w = v
		calls.weighted.Add(1)
	} else {
		calls.unweighted.Add(1)
	}
	y, _ := rec.NumericValue(a.target)
	x, _ := rec.NumericValue(a.x)
	a.sw += w
	a.sx += w * x
	a.sy += w * y
	a.sxx += w * x * x
	a.sxy += w * x * y
	a.syy += w * y * y
}

// output fits y = a + b·x. The intercept reports no inference figure;
// the slope reports all three, so one coefficient carries a p-value.
func (a *slopeAcc) output(vcov bool) (extend.RegressionOutput, error) {
	if a.sw < 3 {
		return extend.RegressionOutput{}, perr.NewCodedError(perr.PROCESSING_REGRESSION_INSUFFICIENT_DATA, "too few rows")
	}
	mx, my := a.sx/a.sw, a.sy/a.sw
	sxx := a.sxx - a.sw*mx*mx
	sxy := a.sxy - a.sw*mx*my
	syy := a.syy - a.sw*my*my
	b := sxy / sxx
	icpt := my - b*mx
	rss := syy - b*sxy
	df := a.sw - 2
	sigma2 := rss / df
	se := math.Sqrt(sigma2 / sxx)
	t := b / se
	p := math.Erfc(math.Abs(t) / math.Sqrt2)
	out := extend.RegressionOutput{
		Coefficients: []extend.RegressionCoefficient{
			{Name: "(intercept)", Estimate: icpt},
			{Name: a.x, Estimate: b, StdErr: extend.Figure(se), Statistic: extend.Figure(t), PValue: extend.Figure(p)},
		},
		Fit:     extend.RegressionFit{R2: extend.Figure(1 - rss/syy), ResidualStdErr: extend.Figure(math.Sqrt(sigma2)), DF: extend.Figure(df)},
		Outputs: map[string]any{"sxx": sxx},
	}
	if !vcov {
		// A model-named parameter without inference figures: present in
		// the table, never a multiplicity member.
		out.Coefficients = append(out.Coefficients,
			extend.RegressionCoefficient{Name: "sigma", Estimate: math.Sqrt(sigma2)},
			// A model-named parameter WITH a p-value joins the default
			// family although no predictor names it.
			extend.RegressionCoefficient{Name: "rho", Estimate: 0, PValue: extend.Figure(0.2)})
	}
	if vcov {
		vb := sigma2 / sxx
		out.Vcov = [][]float64{{sigma2/a.sw + mx*mx*vb, -mx * vb}, {-mx * vb, vb}}
	}
	return out, nil
}

type streamSlope struct {
	acc   *slopeAcc
	calls *regCalls
	vcov  bool
}

func (s *streamSlope) UpdateRow(rec extend.Record) error {
	s.calls.update.Add(1)
	s.acc.add(rec, s.calls)
	return nil
}

func (s *streamSlope) Merge(other extend.StreamingRegression) error {
	s.calls.merge.Add(1)
	o := other.(*streamSlope).acc
	a := s.acc
	a.sw, a.sx, a.sy, a.sxx, a.sxy, a.syy = a.sw+o.sw, a.sx+o.sx, a.sy+o.sy, a.sxx+o.sxx, a.sxy+o.sxy, a.syy+o.syy
	return nil
}

func (s *streamSlope) Finalize() (extend.RegressionOutput, error) {
	s.calls.finalize.Add(1)
	return s.acc.output(s.vcov)
}

type bufferedSlope struct {
	acc   *slopeAcc
	calls *regCalls
}

func (b *bufferedSlope) Fit(rows extend.Rows) (extend.RegressionOutput, error) {
	b.calls.fit.Add(1)
	for i := 0; i < rows.Len(); i++ {
		b.acc.add(rows.At(i), b.calls)
	}
	return b.acc.output(false)
}

func newSlopeAcc(spec *types.RegressionSpec) *slopeAcc {
	x := ""
	if len(spec.Predictors) > 0 {
		x = spec.Predictors[0]
	}
	return &slopeAcc{target: spec.Target, x: x}
}

var slopeOutputs = []pulse.RegressionOutputMeta{{Key: "sxx", JSONType: "number", Description: "centred Σw(x−x̄)²"}}

func regressionExtensions(calls *regCalls) pulse.Extensions {
	stream := func(spec *types.RegressionSpec, _ *encoding.Schema) (extend.Regression, error) {
		return &streamSlope{acc: newSlopeAcc(spec), calls: calls, vcov: spec.Vcov}, nil
	}
	buffered := func(spec *types.RegressionSpec, _ *encoding.Schema) (extend.Regression, error) {
		return &bufferedSlope{acc: newSlopeAcc(spec), calls: calls}, nil
	}
	return pulse.Extensions{Regressions: []pulse.RegressionRegistration{
		{Name: regStreamName, Description: "streaming slope", Factory: stream, Streamable: true, PValues: true, OutputSchema: slopeOutputs},
		{Name: regBufferedName, Description: "buffered slope", Factory: buffered, PValues: true, OutputSchema: slopeOutputs},
		{Name: regAwareName, Description: "weighted slope", Factory: stream, Streamable: true, WeightAware: true, PValues: true, OutputSchema: slopeOutputs},
		{Name: regVcovName, Description: "slope with vcov", Factory: stream, Streamable: true, Vcov: true, PValues: true, CovarianceTypes: []types.CovarianceType{types.CovarianceHC1}, OutputSchema: slopeOutputs},
	}}
}

func regressionRequest(cohort *types.Cohort, specs ...*types.RegressionSpec) *types.Request {
	return &types.Request{Cohort: cohort, Regressions: specs}
}

func slopeSpec(t types.RegressionType) *types.RegressionSpec {
	return &types.RegressionSpec{Type: t, Name: "fit", Target: "score", Predictors: []string{"qty"}}
}

// TestExtensions_RegressionRunsThroughProcess: a streaming and a
// buffered registration each run through Process, drive the declared
// path, and match REG_OLS's coefficients on the same columns. The
// engine owns n_obs (listwise on target and predictor), and the
// coefficient table is rectangular — the intercept's absent standard
// error, statistic and p-value are null on the wire, key kept.
func TestExtensions_RegressionRunsThroughProcess(t *testing.T) {
	t.Parallel()

	calls := &regCalls{}
	p, cohort := openE2E(t, regressionExtensions(calls))
	ols := processE2E(t, p, regressionRequest(cohort, slopeSpec(types.REG_OLS))).Regressions[0]

	for _, tc := range []struct {
		name   types.RegressionType
		stream bool
	}{{regStreamName, true}, {regBufferedName, false}} {
		t.Run(string(tc.name), func(t *testing.T) {
			*calls = regCalls{}
			resp := processE2E(t, p, regressionRequest(cohort, slopeSpec(tc.name)))
			if len(resp.Regressions) != 1 || resp.Regressions[0] == nil {
				t.Fatalf("Regressions = %+v, want one result", resp.Regressions)
			}
			res := resp.Regressions[0]
			if res.Type != tc.name || res.Name != "fit" {
				t.Errorf("identity = (%s, %q), want (%s, fit)", res.Type, res.Name, tc.name)
			}
			if tc.stream && (calls.update.Load() == 0 || calls.finalize.Load() != 1 || calls.fit.Load() != 0) {
				t.Errorf("streaming model: update=%d finalize=%d fit=%d", calls.update.Load(), calls.finalize.Load(), calls.fit.Load())
			}
			if !tc.stream && (calls.fit.Load() != 1 || calls.update.Load() != 0) {
				t.Errorf("buffered model: fit=%d update=%d", calls.fit.Load(), calls.update.Load())
			}
			if res.NObs != ols.NObs || res.NObs == 0 {
				t.Errorf("n_obs = %d, want REG_OLS's %d", res.NObs, ols.NObs)
			}
			// The nullable column as the predictor: listwise deletion on
			// predictors, not only the target.
			rev := slopeSpec(tc.name)
			rev.Target, rev.Predictors = "qty", []string{"score"}
			revOLS := slopeSpec(types.REG_OLS)
			revOLS.Target, revOLS.Predictors = "qty", []string{"score"}
			got := processE2E(t, p, regressionRequest(cohort, rev)).Regressions[0]
			want := processE2E(t, p, regressionRequest(cohort, revOLS)).Regressions[0]
			if got.NObs != want.NObs || math.Abs(got.Coefficients["score"]-want.Coefficients["score"]) > 1e-9 {
				t.Errorf("score predictor: n_obs %d slope %v, REG_OLS %d %v", got.NObs, got.Coefficients["score"], want.NObs, want.Coefficients["score"])
			}
			*calls = regCalls{}
			resp = processE2E(t, p, regressionRequest(cohort, slopeSpec(tc.name)))
			res = resp.Regressions[0]
			for _, k := range []string{"(intercept)", "qty"} {
				if got, want := res.Coefficients[k], ols.Coefficients[k]; math.Abs(got-want) > 1e-9*math.Max(1, math.Abs(want)) {
					t.Errorf("coefficient %s = %v, REG_OLS %v", k, got, want)
				}
			}
			if math.Abs(res.StdErrors["qty"]-ols.StdErrors["qty"]) > 1e-9 {
				t.Errorf("std_err qty = %v, REG_OLS %v", res.StdErrors["qty"], ols.StdErrors["qty"])
			}
			if res.ResidualStdErr == 0 || res.R2 == 0 || res.Outputs["sxx"] == nil {
				t.Errorf("fit stats / outputs missing: rse=%v r2=%v outputs=%v", res.ResidualStdErr, res.R2, res.Outputs)
			}
			raw, err := json.Marshal(res)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var full map[string]json.RawMessage
			if err := json.Unmarshal(raw, &full); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			for _, key := range []string{"std_errors", "statistics", "p_values"} {
				m := map[string]any{}
				if err := json.Unmarshal(full[key], &m); err != nil {
					t.Fatalf("%s: %v (%s)", key, err, raw)
				}
				v, ok := m["(intercept)"]
				if !ok || v != nil {
					t.Errorf("%s[(intercept)] = %v (present %v), want null with the key kept: %s", key, v, ok, raw)
				}
				if m["qty"] == nil {
					t.Errorf("%s[qty] is null, want the reported figure", key)
				}
			}
			if !strings.Contains(string(raw), `"outputs":{"sxx":`) {
				t.Errorf("outputs missing on the wire: %s", raw)
			}
		})
	}
}

// TestExtensions_RegressionStreamingModelRoutedBuffered: a request the
// engine cannot stream (a grouper beside the regression) still drives a
// Streamable model row by row — UpdateRow, then Finalize, never Fit —
// and returns the same coefficients as the streaming arm.
func TestExtensions_RegressionStreamingModelRoutedBuffered(t *testing.T) {
	t.Parallel()

	calls := &regCalls{}
	p, cohort := openE2E(t, regressionExtensions(calls))
	streamed := processE2E(t, p, regressionRequest(cohort, slopeSpec(regStreamName))).Regressions[0]

	req := regressionRequest(cohort, slopeSpec(regStreamName))
	req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
	req.Aggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "qty", Label: "n"}}
	if processing.CanStreamRequestWithExtensions(req, paritySchema(t), pulse.ServiceForTest(p).Extensions()) {
		t.Fatal("a grouped regression request streams; the test needs the buffered arm")
	}
	*calls = regCalls{}
	buffered := processE2E(t, p, req).Regressions[0]
	if calls.update.Load() == 0 || calls.finalize.Load() != 1 {
		t.Errorf("buffered arm: update=%d finalize=%d, want the rows streamed through UpdateRow", calls.update.Load(), calls.finalize.Load())
	}
	if a, b := mustJSON(t, streamed), mustJSON(t, buffered); a != b {
		t.Errorf("streaming vs buffered arm differ:\n%s\n%s", a, b)
	}
}

// TestExtensions_RegressionStreamabilityFollowsDeclaration: the
// declared form decides the path, predict agrees with the runtime gate,
// and a declared robust covariance or a resample modifier downgrades a
// streaming model to buffered on both sides
// (types.RegressionSpec.StreamableAs).
func TestExtensions_RegressionStreamabilityFollowsDeclaration(t *testing.T) {
	t.Parallel()

	calls := &regCalls{}
	p, cohort := openE2E(t, regressionExtensions(calls))
	robust := slopeSpec(regVcovName)
	robust.Covariance = &types.RegressionCovariance{Type: types.CovarianceHC1}
	resampled := slopeSpec(regStreamName)
	resampled.Resample = "jackknife"
	for _, tc := range []struct {
		name   string
		spec   *types.RegressionSpec
		stream bool
	}{
		{"streaming", slopeSpec(regStreamName), true},
		{"buffered", slopeSpec(regBufferedName), false},
		{"streaming_robust_covariance", robust, false},
		{"streaming_resampled", resampled, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := regressionRequest(cohort, tc.spec)
			pr, err := p.Predict(context.Background(), req)
			if err != nil {
				t.Fatalf("Predict: %v", err)
			}
			if !pr.Valid {
				t.Fatalf("Predict invalid: %+v", pr.Errors)
			}
			if pr.Streamable != tc.stream {
				t.Errorf("PredictResult.Streamable = %v (%v), want %v", pr.Streamable, pr.StreamableReasons, tc.stream)
			}
			if got := processing.CanStreamRequestWithExtensions(req, paritySchema(t), pulse.ServiceForTest(p).Extensions()); got != tc.stream {
				t.Errorf("CanStreamRequestWithExtensions = %v, want %v", got, tc.stream)
			}
		})
	}
}

// TestExtensions_RegressionMultiplicityOnlyPValuedJoin: a multiplicity
// block on an extension model adjusts only the coefficients carrying a
// p-value — the slope and the model-named rho — so neither the
// intercept nor sigma (no p-value, null on the wire) is a member: m = 2.
func TestExtensions_RegressionMultiplicityOnlyPValuedJoin(t *testing.T) {
	t.Parallel()

	calls := &regCalls{}
	p, cohort := openE2E(t, regressionExtensions(calls))
	spec := slopeSpec(regStreamName)
	spec.Multiplicity = &types.RegressionMultiplicity{Method: types.MultiplicityMethodBonferroni}
	res := processE2E(t, p, regressionRequest(cohort, spec)).Regressions[0]
	if res.Multiplicity == nil || res.Multiplicity.M != 2 || res.Multiplicity.Excluded != 0 {
		t.Fatalf("multiplicity = %+v, want m=2 (qty, rho) excluded=0", res.Multiplicity)
	}
	for _, k := range []string{"(intercept)", "sigma"} {
		if _, ok := res.PAdjusted[k]; ok {
			t.Errorf("p_adjusted carries the p-less %s: %v", k, res.PAdjusted)
		}
	}
	if _, ok := res.PValues["sigma"]; !ok {
		t.Errorf("p_values lacks the sigma key (want it kept, null): %v", res.PValues)
	}
	for _, k := range []string{"qty", "rho"} {
		if got, want := res.PAdjusted[k], math.Min(1, 2*res.PValues[k]); got != want {
			t.Errorf("p_adjusted[%s] = %v, want Bonferroni %v (m = 2)", k, got, want)
		}
	}
}

// TestExtensions_RegressionVcov: vcov: true on a registration that does
// not declare Vcov is refused with PROCESSING_CONFIG {regression, slot}
// identically at predict and at runtime; a declaring one returns the
// covariance and its correlation keyed by its coefficients.
func TestExtensions_RegressionVcov(t *testing.T) {
	t.Parallel()

	calls := &regCalls{}
	p, cohort := openE2E(t, regressionExtensions(calls))
	refused := slopeSpec(regStreamName)
	refused.Vcov = true
	req := regressionRequest(cohort, refused)
	_, err := p.Process(context.Background(), req)
	ce := requireCodedError(t, err, perr.PROCESSING_CONFIG)
	if ce.Details["slot"] != "vcov" || ce.Details["regression"] != string(regStreamName) {
		t.Errorf("runtime details = %v, want {regression %s, slot vcov}", ce.Details, regStreamName)
	}
	pr, err := p.Predict(context.Background(), req)
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	found := false
	for _, e := range pr.Errors {
		if e.Code == string(perr.PROCESSING_CONFIG) && e.Details["slot"] == "vcov" && e.Details["regression"] == string(regStreamName) {
			found = true
		}
	}
	if !found {
		t.Errorf("predict errors %+v lack the runtime's vcov refusal", pr.Errors)
	}

	honoured := slopeSpec(regVcovName)
	honoured.Vcov = true
	res := processE2E(t, p, regressionRequest(cohort, honoured)).Regressions[0]
	if res.Vcov == nil || res.Correlation == nil {
		t.Fatalf("vcov / correlation absent: %+v", res)
	}
	if got := res.Vcov.RowKeys; len(got) != 2 || got[0] != "(intercept)" || got[1] != "qty" {
		t.Errorf("vcov keys = %v, want the coefficient order", got)
	}
	if d := math.Sqrt(res.Vcov.Values[1][1]) - res.StdErrors["qty"]; math.Abs(d) > 1e-12 {
		t.Errorf("√vcov[qty,qty] − std_err = %v", d)
	}
}

// TestExtensions_RegressionWeights: a WeightAware model sees each
// admitted row's weight and the engine writes the weighted floor; a
// model that does not declare WeightAware is refused under a weight
// with PULSE_EXTENSION_NOT_WEIGHT_AWARE at predict and at runtime.
func TestExtensions_RegressionWeights(t *testing.T) {
	t.Parallel()

	calls := &regCalls{}
	p, cohort := openE2E(t, regressionExtensions(calls))
	weight := &types.WeightSpec{Field: "qty", Kind: types.WeightKindProbability}

	req := regressionRequest(cohort, slopeSpec(regAwareName))
	req.Regressions[0].Predictors = []string{"day"}
	req.Weight = weight
	*calls = regCalls{}
	res := processE2E(t, p, req).Regressions[0]
	if calls.weighted.Load() == 0 || calls.unweighted.Load() != 0 {
		t.Errorf("model saw weighted=%d unweighted=%d rows, want every row weighted", calls.weighted.Load(), calls.unweighted.Load())
	}
	if res.SumWeights == 0 || res.NEff == 0 {
		t.Errorf("weighted floor absent: sum_weights=%v n_eff=%v", res.SumWeights, res.NEff)
	}

	plain := regressionRequest(cohort, slopeSpec(regStreamName))
	plain.Regressions[0].Predictors = []string{"day"}
	plain.Weight = weight
	_, err := p.Process(context.Background(), plain)
	requireCodedError(t, err, perr.PULSE_EXTENSION_NOT_WEIGHT_AWARE)
	pr, err := p.Predict(context.Background(), plain)
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if len(pr.Errors) == 0 || pr.Errors[0].Code != string(perr.PULSE_EXTENSION_NOT_WEIGHT_AWARE) {
		t.Errorf("predict errors = %+v, want PULSE_EXTENSION_NOT_WEIGHT_AWARE", pr.Errors)
	}
}

// leakyModel fills an undeclared output key on real data only (the
// probe's synthetic target is probe_y).
type leakyModel struct{ target string }

func (m leakyModel) Fit(rows extend.Rows) (extend.RegressionOutput, error) {
	out := extend.RegressionOutput{Coefficients: []extend.RegressionCoefficient{{Name: "b", Estimate: 1}}}
	if m.target != "probe_y" {
		out.Outputs = map[string]any{"leak": 1.0}
	}
	return out, nil
}

// TestExtensions_RegressionRuntimeOutputMismatch: an undeclared output
// key filled on a real run is refused with the probe's code.
func TestExtensions_RegressionRuntimeOutputMismatch(t *testing.T) {
	t.Parallel()

	p, cohort := openE2E(t, pulse.Extensions{Regressions: []pulse.RegressionRegistration{{
		Name: "REG_ACME_LEAKY",
		Factory: func(spec *types.RegressionSpec, _ *encoding.Schema) (extend.Regression, error) {
			return leakyModel{target: spec.Target}, nil
		},
	}}})
	_, err := p.Process(context.Background(), regressionRequest(cohort, slopeSpec("REG_ACME_LEAKY")))
	ce := requireCodedError(t, err, perr.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH)
	if ce.Details["reason"] != "undeclared_output_key" {
		t.Errorf("details = %v, want reason undeclared_output_key", ce.Details)
	}
}

// TestExtensions_RegressionUnregisteredUnknown: an unregistered REG_*
// name stays an unknown type at predict and at runtime.
func TestExtensions_RegressionUnregisteredUnknown(t *testing.T) {
	t.Parallel()

	p, cohort := openE2E(t, regressionExtensions(&regCalls{}))
	req := regressionRequest(cohort, slopeSpec("REG_ACME_NOPE"))
	if _, err := p.Process(context.Background(), req); err == nil {
		t.Fatal("Process accepted an unregistered regression")
	}
	pr, err := p.Predict(context.Background(), req)
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if pr.Valid {
		t.Error("Predict accepted an unregistered regression")
	}
}

// probeModel is a configurable test model for the registration probe.
type probeModel struct {
	out      extend.RegressionOutput
	err      error
	panicFit bool
}

func (m probeModel) Fit(extend.Rows) (extend.RegressionOutput, error) {
	if m.panicFit {
		panic("boom")
	}
	return m.out, m.err
}

// streamOnly implements UpdateRow / Merge / Finalize but not Fit.
type streamOnly struct{ out extend.RegressionOutput }

func (streamOnly) UpdateRow(extend.Record) error                { return nil }
func (streamOnly) Merge(extend.StreamingRegression) error       { return nil }
func (s streamOnly) Finalize() (extend.RegressionOutput, error) { return s.out, nil }
func (s streamOnly) asStreaming() extend.StreamingRegression    { return s }
func okOutput() extend.RegressionOutput {
	return extend.RegressionOutput{Coefficients: []extend.RegressionCoefficient{{Name: "b", Estimate: 1}}}
}
func modelFactory(m extend.Regression) extend.RegressionFactory {
	return func(*types.RegressionSpec, *encoding.Schema) (extend.Regression, error) { return m, nil }
}

// noMerge streams without Merge.
type noMerge struct{}

func (noMerge) UpdateRow(extend.Record) error              { return nil }
func (noMerge) Finalize() (extend.RegressionOutput, error) { return okOutput(), nil }

// TestExtensions_RegressionRegistrationRefusals: the naming policy,
// the REG_ prefix reserved to Regressions, the nil factory and the
// OutputSchema shape, each refused at pulse.New.
func TestExtensions_RegressionRegistrationRefusals(t *testing.T) {
	t.Parallel()

	good := modelFactory(probeModel{out: okOutput()})
	aggFactory := func(*types.Aggregation, *encoding.Schema) (extend.Aggregator, error) { return &stubAggregator{}, nil }
	for _, tc := range []struct {
		name   string
		ext    pulse.Extensions
		code   perr.Code
		reason string
	}{
		{"lowercase", pulse.Extensions{Regressions: []pulse.RegressionRegistration{{Name: "REG_acme_X", Factory: good}}}, perr.PULSE_EXTENSION_NAME_INVALID, ""},
		{"two_segment", pulse.Extensions{Regressions: []pulse.RegressionRegistration{{Name: "REG_HILL", Factory: good}}}, perr.PULSE_EXTENSION_NAME_INVALID, ""},
		{"reserved_namespace", pulse.Extensions{Regressions: []pulse.RegressionRegistration{{Name: "REG_PULSE_HILL", Factory: good}}}, perr.PULSE_EXTENSION_NAME_RESERVED, ""},
		{"builtin_collision", pulse.Extensions{Regressions: []pulse.RegressionRegistration{{Name: types.REG_OLS, Factory: good}}}, perr.PULSE_EXTENSION_NAME_COLLISION, ""},
		{"builtin_collision_bayes", pulse.Extensions{Regressions: []pulse.RegressionRegistration{{Name: types.REG_BAYES_LINEAR, Factory: good}}}, perr.PULSE_EXTENSION_NAME_COLLISION, ""},
		{"wrong_prefix_under_regressions", pulse.Extensions{Regressions: []pulse.RegressionRegistration{{Name: "MAT_ACME_HILL", Factory: good}}}, perr.PULSE_EXTENSION_NAME_INVALID, ""},
		{"reg_prefix_under_aggregators", pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{{Name: "REG_ACME_HILL", Factory: aggFactory}}}, perr.PULSE_EXTENSION_NAME_INVALID, ""},
		{"duplicate", pulse.Extensions{Regressions: []pulse.RegressionRegistration{{Name: "REG_ACME_HILL", Factory: good}, {Name: "REG_ACME_HILL", Factory: good}}}, perr.PULSE_EXTENSION_DUPLICATE, ""},
		{"nil_factory", pulse.Extensions{Regressions: []pulse.RegressionRegistration{{Name: "REG_ACME_HILL"}}}, perr.PULSE_EXTENSION_PARAM_INVALID, "missing_factory"},
		{"empty_output_key", pulse.Extensions{Regressions: []pulse.RegressionRegistration{{Name: "REG_ACME_HILL", Factory: good,
			OutputSchema: []pulse.RegressionOutputMeta{{Key: "", JSONType: "number"}}}}}, perr.PULSE_EXTENSION_PARAM_INVALID, "invalid_output_key"},
		{"repeated_output_key", pulse.Extensions{Regressions: []pulse.RegressionRegistration{{Name: "REG_ACME_HILL", Factory: good,
			OutputSchema: []pulse.RegressionOutputMeta{{Key: "k", JSONType: "number"}, {Key: "k", JSONType: "number"}}}}}, perr.PULSE_EXTENSION_PARAM_INVALID, "invalid_output_key"},
		{"bad_output_type", pulse.Extensions{Regressions: []pulse.RegressionRegistration{{Name: "REG_ACME_HILL", Factory: good,
			OutputSchema: []pulse.RegressionOutputMeta{{Key: "k", JSONType: "float"}}}}}, perr.PULSE_EXTENSION_PARAM_INVALID, "invalid_output_key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := pulse.New(newTestOptions(tc.ext))
			ce := requireCodedError(t, err, tc.code)
			if tc.reason != "" && ce.Details["reason"] != tc.reason {
				t.Errorf("details.reason = %v, want %s (%v)", ce.Details["reason"], tc.reason, ce.Details)
			}
		})
	}
	if _, err := pulse.New(newTestOptions(pulse.Extensions{Regressions: []pulse.RegressionRegistration{{Name: "REG_ACME_HILL", Factory: good,
		OutputSchema: []pulse.RegressionOutputMeta{{Key: "k", JSONType: "number"}}}}})); err != nil {
		t.Fatalf("a valid registration was refused: %v", err)
	}
}

// TestExtensions_RegressionProbeRefusals: the registration probe fits
// every model over synthetic rows and refuses a form mismatch in both
// directions (Merge included), an output the registration does not
// allow, and a factory or fit panic / error; a data-dependent refusal
// on the synthetic rows is tolerated.
func TestExtensions_RegressionProbeRefusals(t *testing.T) {
	t.Parallel()

	undeclared := okOutput()
	undeclared.Outputs = map[string]any{"leak": 1.0}
	dup := extend.RegressionOutput{Coefficients: []extend.RegressionCoefficient{{Name: "b"}, {Name: "b"}}}
	badVcov := okOutput()
	badVcov.Vcov = [][]float64{{1, 0}}
	panicky := func(*types.RegressionSpec, *encoding.Schema) (extend.Regression, error) { panic("factory boom") }
	weightedOnlyFails := func(spec *types.RegressionSpec, _ *encoding.Schema) (extend.Regression, error) {
		if spec.Weight.Spec() != nil {
			return probeModel{out: undeclared}, nil
		}
		return probeModel{out: okOutput()}, nil
	}
	for _, tc := range []struct {
		name     string
		reg      pulse.RegressionRegistration
		code     perr.Code
		reason   string
		weighted bool
	}{
		{"streaming_declared_buffered_model", pulse.RegressionRegistration{Factory: modelFactory(probeModel{out: okOutput()}), Streamable: true}, perr.PULSE_EXTENSION_STREAMABLE_MISMATCH, "not_streaming", false},
		{"streaming_without_merge", pulse.RegressionRegistration{Factory: modelFactory(noMerge{}), Streamable: true}, perr.PULSE_EXTENSION_STREAMABLE_MISMATCH, "missing_merge", false},
		{"buffered_declared_streaming_model", pulse.RegressionRegistration{Factory: modelFactory(streamOnly{out: okOutput()}.asStreaming())}, perr.PULSE_EXTENSION_STREAMABLE_MISMATCH, "not_buffered", false},
		{"undeclared_output", pulse.RegressionRegistration{Factory: modelFactory(probeModel{out: undeclared})}, perr.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH, "undeclared_output_key", false},
		{"no_coefficients", pulse.RegressionRegistration{Factory: modelFactory(probeModel{})}, perr.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH, "missing_coefficients", false},
		{"repeated_coefficient", pulse.RegressionRegistration{Factory: modelFactory(probeModel{out: dup})}, perr.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH, "invalid_coefficient", false},
		{"declared_vcov_missing", pulse.RegressionRegistration{Factory: modelFactory(probeModel{out: okOutput()}), Vcov: true}, perr.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH, "missing_vcov", false},
		{"declared_vcov_misshapen", pulse.RegressionRegistration{Factory: modelFactory(probeModel{out: badVcov}), Vcov: true}, perr.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH, "vcov_shape", false},
		{"factory_panic", pulse.RegressionRegistration{Factory: panicky}, perr.PULSE_EXTENSION_FACTORY_PANIC, "", false},
		{"fit_panic", pulse.RegressionRegistration{Factory: modelFactory(probeModel{panicFit: true})}, perr.PULSE_EXTENSION_FACTORY_PANIC, "", false},
		{"fit_error", pulse.RegressionRegistration{Factory: modelFactory(probeModel{err: perr.NewCodedError(perr.PROCESSING_CONFIG, "no")})}, perr.PULSE_EXTENSION_FACTORY_PANIC, "", false},
		{"weighted_probe", pulse.RegressionRegistration{Factory: weightedOnlyFails, WeightAware: true}, perr.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH, "undeclared_output_key", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.reg.Name = "REG_ACME_PROBE"
			_, err := pulse.New(newTestOptions(pulse.Extensions{Regressions: []pulse.RegressionRegistration{tc.reg}}))
			ce := requireCodedError(t, err, tc.code)
			if tc.reason != "" && ce.Details["reason"] != tc.reason {
				t.Errorf("details.reason = %v, want %s (%v)", ce.Details["reason"], tc.reason, ce.Details)
			}
			if tc.weighted && ce.Details["weighted"] != true {
				t.Errorf("details.weighted = %v, want true", ce.Details["weighted"])
			}
		})
	}
	t.Run("data_refusal_tolerated", func(t *testing.T) {
		m := probeModel{err: perr.NewCodedError(perr.PROCESSING_REGRESSION_INSUFFICIENT_DATA, "needs more rows")}
		if _, err := pulse.New(newTestOptions(pulse.Extensions{Regressions: []pulse.RegressionRegistration{{Name: "REG_ACME_PROBE", Factory: modelFactory(m)}}})); err != nil {
			t.Fatalf("a data-dependent probe refusal failed registration: %v", err)
		}
	})
}
