package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// inferentialRefusalRequests builds, per refused surface, a request
// over acceptanceCohort whose refused slot carries weight w (the zero
// SlotWeight inherits). Regular operators reuse acceptanceTemplates
// (field x); post-tests, crosstab-axis GROUP_QUANTILE and the
// inferential overlays are built here.
func inferentialRefusalRequests(t *testing.T, cohort string, w types.SlotWeight) map[string]*types.Request {
	t.Helper()
	field := map[string]string{"TEST_CHISQ": "subj", "TEST_FISHER_EXACT": "g", "TEST_PROP_Z": "g", "TEST_PAIRED_T": "y"}
	var ops []string
	for _, tt := range types.AllTestTypes() {
		ops = append(ops, string(tt))
	}
	for _, rt := range types.AllRegressionTypes() {
		ops = append(ops, string(rt))
	}
	ops = append(ops, "ATTR_ZSCORE", "ATTR_TSCORE", "ATTR_PERCENTILE", "ATTR_NORMALIZED", "GROUP_QUANTILE")
	out := map[string]*types.Request{}
	decode := func(body string) *types.Request {
		var r types.Request
		if err := json.Unmarshal([]byte(body), &r); err != nil {
			t.Fatalf("template %s: %v", body, err)
		}
		r.Cohort = &types.Cohort{Filename: cohort}
		return &r
	}
	for _, op := range ops {
		tmpl := acceptanceTemplates[op]
		if tmpl == "" {
			continue // post-test only: built below
		}
		f := field[op]
		if f == "" {
			f = "x"
		}
		r := decode(strings.NewReplacer("$F", f, "$S", "a").Replace(tmpl))
		switch {
		case len(r.Tests) > 0:
			r.Tests[0].Weight = w
		case len(r.Regressions) > 0:
			r.Regressions[0].Weight = w
		case len(r.Attributes) > 0:
			r.Attributes[0].Weight = w
			// The consuming aggregation must not itself refuse or
			// carry the weight being judged.
			r.Aggregations[0].Weight = types.NullSlotWeight()
		case len(r.Groups) > 0:
			r.Groups[0].Weight = w
			r.Aggregations[0].Weight = types.NullSlotWeight()
		}
		out[op] = r
	}
	for _, op := range []string{"TEST_TREND", "TEST_TUKEY_HSD"} {
		r := decode(`{"groups":[{"type":"GROUP_CATEGORY","field":"subj"}],"aggregations":[{"type":"AGG_AVERAGE","field":"x","label":"m","weight":null}],` +
			`"post_tests":[{"type":"` + op + `","field":"m","split_by":"subj","order_by":[{"field":"m"}],"params":{"ms_within":1.5,"df_within":20}}]}`)
		r.PostTests[0].Weight = w
		out["post/"+op] = r
	}
	axis := decode(`{"crosstab":{"rows":[{"type":"GROUP_QUANTILE","field":"x","interval":4}],"columns":[{"type":"GROUP_CATEGORY","field":"g"}],` +
		`"cell":{"type":"AGG_COUNT","field":"x","weight":null}}}`)
	axis.Crosstab.Rows[0].Weight = w
	out["crosstab.rows/GROUP_QUANTILE"] = axis
	for _, kind := range []string{"OVERLAY_CHISQ_ROW", "OVERLAY_CHISQ_MATRIX"} {
		r := decode(`{"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"g"}],"columns":[{"type":"GROUP_CATEGORY","field":"subj"}],` +
			`"cell":{"type":"AGG_COUNT","field":"x","weight":null},"margins":{"rows":true,"columns":true,"grand":true}},` +
			`"overlays":[{"kind":"` + kind + `","scope":"` + map[string]string{"OVERLAY_CHISQ_ROW": "row", "OVERLAY_CHISQ_MATRIX": "matrix"}[kind] + `"}]}`)
		r.Overlays[0].Weight = w
		out["overlay/"+kind] = r
	}
	return out
}

// TestWeight_InferentialRefusalsMatchPredict: every inferential
// surface U12 owns — each built-in TEST_* (tests and post-tests),
// REG_*, ATTR_ZSCORE / TSCORE / PERCENTILE / NORMALIZED, GROUP_QUANTILE
// (groups and a crosstab axis) and Inferential overlays — is refused
// PULSE_WEIGHT_UNSUPPORTED at runtime and predict with the same code,
// message and details, under the instance default, a request weight and
// a slot weight alike; the slot's `weight: null` opts out on both sides
// (an instance with Options.DefaultWeight still runs TEST_T).
func TestWeight_InferentialRefusalsMatchPredict(t *testing.T) {
	_, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	def := &types.WeightSpec{Field: "y"}
	plain := weightPulse(t, fs, nil)
	withDefault := weightPulse(t, fs, def)

	type source struct {
		p    *pulse.Pulse
		w    types.SlotWeight
		reqW *types.WeightSpec
	}
	sources := map[string]source{
		"default": {withDefault, types.SlotWeight{}, nil},
		"request": {plain, types.SlotWeight{}, def},
		"slot":    {plain, types.SlotWeightField("y"), nil},
	}
	for sname, src := range sources {
		reqs := inferentialRefusalRequests(t, cohort, src.w)
		keys := sortedKeys(reqs)
		for _, key := range keys {
			t.Run(sname+"/"+key, func(t *testing.T) {
				mk := func() *types.Request {
					r := inferentialRefusalRequests(t, cohort, src.w)[key]
					r.Weight = src.reqW
					return r
				}
				_, rerr := src.p.Process(ctx, mk())
				ce := requireCode(t, rerr, errors.PULSE_WEIGHT_UNSUPPORTED)
				if ce.Details["field"] != "y" || ce.Details["slot"] == nil || ce.Details["operator"] == nil {
					t.Fatalf("details = %v", ce.Details)
				}
				sameEntry(t, predictEnvelope(t, src.p, fs, cohort, mk()), rerr)
			})
		}
	}

	for _, key := range sortedKeys(inferentialRefusalRequests(t, cohort, types.NullSlotWeight())) {
		t.Run("null/"+key, func(t *testing.T) {
			mk := func() *types.Request {
				r := inferentialRefusalRequests(t, cohort, types.NullSlotWeight())[key]
				r.Weight = def
				return r
			}
			_, rerr := withDefault.Process(ctx, mk())
			var ce *errors.CodedError
			if rerr != nil && !(stderrors.As(rerr, &ce) && acceptanceDataOutcomes[ce.Code]) {
				t.Fatalf("opted-out slot refused: %v", rerr)
			}
			if env := predictEnvelope(t, withDefault, fs, cohort, mk()); len(env.Errors) != 0 {
				t.Fatalf("predict refused the opted-out slot: %+v", env.Errors)
			}
		})
	}
}

// TestWeight_DefaultInstanceRunsTTestOptedOut is the story's named
// case: an instance with Options.DefaultWeight refuses a bare TEST_T and
// runs it with `weight: null`.
func TestWeight_DefaultInstanceRunsTTestOptedOut(t *testing.T) {
	_, fs, cohort := acceptanceCohort(t)
	p := weightPulse(t, fs, &types.WeightSpec{Field: "y"})
	ctx := context.Background()
	tt := func(w types.SlotWeight) *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: cohort},
			Tests:  []*types.Test{{Type: types.TEST_T, Field: "x", Params: json.RawMessage(`{"mu":10}`), Weight: w}},
		}
	}
	_, err := p.Process(ctx, tt(types.SlotWeight{}))
	requireCode(t, err, errors.PULSE_WEIGHT_UNSUPPORTED)
	resp, err := p.Process(ctx, tt(types.NullSlotWeight()))
	if err != nil {
		t.Fatalf("opted-out TEST_T: %v", err)
	}
	if len(resp.Tests) != 1 || resp.Tests[0].Type != types.TEST_T {
		t.Fatalf("TEST_T result = %+v", resp.Tests)
	}
}

// TestWeight_WindowsAndUnaffectedMatchPredict: a WIN_* is
// PROCESSING_CONFIG under a request weight (identically in predict) and
// runs under the instance default; filterers, features, row-local
// attributes and the other groupers run under a request weight.
func TestWeight_WindowsAndUnaffectedMatchPredict(t *testing.T) {
	_, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	def := &types.WeightSpec{Field: "y"}
	win := func(reqW *types.WeightSpec) *types.Request {
		var r types.Request
		if err := json.Unmarshal([]byte(strings.ReplaceAll(acceptanceTemplates["WIN_LAG"], "$F", "x")), &r); err != nil {
			t.Fatal(err)
		}
		r.Cohort, r.Weight = &types.Cohort{Filename: cohort}, reqW
		return &r
	}
	plain := weightPulse(t, fs, nil)
	_, rerr := plain.Process(ctx, win(def))
	ce := requireCode(t, rerr, errors.PROCESSING_CONFIG)
	if ce.Details["slot"] != "windows[0]" || ce.Details["operator"] != "WIN_LAG" {
		t.Fatalf("details = %v", ce.Details)
	}
	sameEntry(t, predictEnvelope(t, plain, fs, cohort, win(def)), rerr)
	withDefault := weightPulse(t, fs, def)
	if _, err := withDefault.Process(ctx, win(nil)); err != nil {
		t.Fatalf("window under the default: %v", err)
	}

	unaffected := []string{"FILTER_INCLUDE", "FILTER_RANGE", "FILTER_EXPRESSION", "FEAT_LOG", "FEAT_BUCKETIZE", "ATTR_FORMULA",
		"GROUP_CATEGORY", "GROUP_RANGE", "GROUP_ROUNDED"}
	field := map[string]string{"FILTER_INCLUDE": "g", "GROUP_CATEGORY": "g", "FILTER_EXPRESSION": "x"}
	for _, op := range unaffected {
		t.Run(op, func(t *testing.T) {
			f := field[op]
			if f == "" {
				f = "x"
			}
			v := map[string]string{"g": "a", "x": "1"}[f]
			body := strings.NewReplacer("$F", f, "$V", v, "$W", "20").Replace(acceptanceTemplates[op])
			var r types.Request
			if err := json.Unmarshal([]byte(body), &r); err != nil {
				t.Fatal(err)
			}
			r.Cohort, r.Weight = &types.Cohort{Filename: cohort}, def
			if _, err := plain.Process(ctx, &r); err != nil {
				t.Fatalf("%s under a request weight: %v", op, err)
			}
			if env := predictEnvelope(t, plain, fs, cohort, &r); len(env.Errors) != 0 {
				t.Fatalf("%s: predict errors %+v", op, env.Errors)
			}
		})
	}
}

func sortedKeys(m map[string]*types.Request) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestWeight_ComposeInferentialOverlayMatchesValidator: a Compose-host
// Inferential overlay (OVERLAY_Z_VS_REF) over slots an instance default
// weight reaches is PULSE_WEIGHT_UNSUPPORTED at runtime and in
// ValidateComposeWithOptions with the same code, message and details;
// `weight: null` on the host slots' aggregations runs it.
func TestWeight_ComposeInferentialOverlayMatchesValidator(t *testing.T) {
	_, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	def := &types.WeightSpec{Field: "y"}
	p := weightPulse(t, fs, def)
	mk := func(w types.SlotWeight) *types.ComposedRequest {
		slot := func(label, v string) *types.Request {
			return &types.Request{
				Label:        label,
				Cohort:       &types.Cohort{Filename: cohort},
				Filterers:    []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "g", Values: []string{v}}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_WELFORD, Field: "x", Label: "t", Weight: w}},
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "subj"}},
			}
		}
		return &types.ComposedRequest{
			Requests: []*types.Request{slot("control", "a"), slot("variant", "b")},
			Overlays: []types.ComposeOverlaySpec{{Kind: types.OverlayKindZVsRef, Scope: types.OverlayScopeGroup, Reference: "control", Targets: []string{"variant"}}},
		}
	}
	_, rerr := p.Compose(ctx, mk(types.SlotWeight{}))
	ce := requireCode(t, rerr, errors.PULSE_WEIGHT_UNSUPPORTED)
	if ce.Details["host"] != "requests[0].aggregations[0]" || ce.Details["operator"] != "OVERLAY_Z_VS_REF" {
		t.Fatalf("details = %v", ce.Details)
	}
	opts := &descx.PredictOptions{SchemaLoader: schemaLoaderFor(fs), DefaultWeight: def}
	sameEntry(t, descx.ValidateComposeWithOptions(mk(types.SlotWeight{}), opts), rerr)

	if _, err := p.Compose(ctx, mk(types.NullSlotWeight())); err != nil {
		t.Fatalf("host opted out: %v", err)
	}
	if env := descx.ValidateComposeWithOptions(mk(types.NullSlotWeight()), opts); len(env.Errors) != 0 {
		t.Fatalf("validator refused the opted-out host: %+v", env.Errors)
	}
}
