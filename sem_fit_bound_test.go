package pulse_test

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// sem_fit_bound_test.go pins the Compose `sem` fit kind (U59 E3-S2) at
// the facade: a `fits` entry holding one MAT_SEM matrix binds its fitted
// parameters ({"sem": …} in the response) and ATTR_SEM_SCORE's
// params.fit scores from it row-local — bit-equal to the inline fit over
// the same rows, streaming beside groupers, merging across shards and
// chain stages; one namespace, three kinds (PULSE_MODEL_KIND_MISMATCH
// both ways, PULSE_MODEL_UNKNOWN), a failed fit's PULSE_FIT_UNAVAILABLE
// — predict ≡ runtime throughout.

// semFitKnobs are the brand survey's joint two-wave scalar fit: the
// CFA plus the outcome, grouped by wave.
func semFitKnobs() map[string]any {
	return map[string]any{"model": semScoreCFA, "group": "wave", "group_equal": []string{"loadings", "intercepts"}}
}

// semFitRequest is a fit request: one MAT_SEM over brandMembers with
// knobs, on brand.pulse.
func semFitRequest(knobs map[string]any) *types.Request {
	raw, _ := json.Marshal(knobs)
	return &types.Request{
		Cohort:   &types.Cohort{Filename: "brand.pulse"},
		Matrices: []types.MatrixSpec{{Name: "sem", Type: types.MAT_SEM, Fields: brandMembers, Params: raw}},
	}
}

// semScoreSlotReq scores "sc" (latent service) with params and folds it
// order-free (count / min / max — exact under any merge) plus, exact
// only serially, its sum.
func semScoreSlotReq(params map[string]any, withSum bool) *types.Request {
	raw, _ := json.Marshal(params)
	r := &types.Request{
		Cohort:     &types.Cohort{Filename: "brand.pulse"},
		Attributes: []*types.Attribute{{Type: types.ATTR_SEM_SCORE, Label: "sc", Fields: brandMembers, Params: raw}},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "sc", Label: "n"},
			{Type: types.AGG_MIN, Field: "sc", Label: "lo"},
			{Type: types.AGG_MAX, Field: "sc", Label: "hi"},
		},
	}
	if withSum {
		r.Aggregations = append(r.Aggregations, &types.Aggregation{Type: types.AGG_SUM, Field: "sc", Label: "sum"})
	}
	return r
}

func boundParams() map[string]any { return map[string]any{"fit": "brand", "latent": "service"} }

func inlineParams() map[string]any {
	p := semFitKnobs()
	p["latent"] = "service"
	return p
}

func byWave(r *types.Request) *types.Request {
	r.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "wave"}}
	return r
}

func dataJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := types.MarshalFinite(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestSEMFit_BoundScoresMatchInline: the bound slot's per-wave figures
// (count / min / max / sum of every row's score) equal the inline fit's
// over the same rows, ungrouped and grouped by wave — where the inline
// twin runs buffered and the bound slot streams (predict says so) —
// and the compose `fits` entry is {"sem": …} with the fit's matrices.
func TestSEMFit_BoundScoresMatchInline(t *testing.T) {
	t.Parallel()

	p, fsys := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	ctx := context.Background()
	for _, grouped := range []bool{false, true} {
		shape := func(r *types.Request) *types.Request {
			if grouped {
				return byWave(r)
			}
			return r
		}
		want, err := p.Process(ctx, shape(semScoreSlotReq(inlineParams(), true)))
		if err != nil {
			t.Fatal(err)
		}
		out, err := p.Compose(ctx, &pulse.ComposedRequest{
			Fits:     []types.FitSpec{{Name: "brand", Request: semFitRequest(semFitKnobs())}},
			Requests: []*types.Request{shape(semScoreSlotReq(boundParams(), true))},
		})
		if err != nil {
			t.Fatalf("grouped=%v: %v", grouped, err)
		}
		if g, w := dataJSON(t, out.Responses[0].Data), dataJSON(t, want.Data); g != w {
			t.Fatalf("grouped=%v: bound figures differ from the inline fit\n got %s\nwant %s", grouped, g, w)
		}
		if !strings.Contains(dataJSON(t, want.Data), `"n":`) {
			t.Fatal("vacuous: no figures")
		}
		fit := out.Fits["brand"]
		if fit == nil || fit.SEM == nil || fit.Vector != nil || fit.RegressionResult != nil {
			t.Fatalf("fits[brand] = %+v, want the sem arm", fit)
		}
		wire, err := json.Marshal(fit)
		if err != nil {
			t.Fatal(err)
		}
		var shapeOf map[string]map[string]any
		if err := json.Unmarshal(wire, &shapeOf); err != nil || len(shapeOf) != 1 || shapeOf["sem"] == nil {
			t.Fatalf("wire %s, want {\"sem\": {...}}", wire)
		}
		if s := fit.SEM; s.Operator != "MAT_SEM" || s.Group != "wave" || len(s.Groups) != 2 || s.Groups[0].Level != "1" || s.Groups[1].Level != "2" ||
			len(s.Groups[1].Lambda) != len(s.Observed) || len(s.Groups[1].Alpha) != len(s.Inner) || s.Conventions != "lavaan" {
			t.Fatalf("sem fit %s", wire)
		}
		// Predict: the bound slot streams on either shape; the inline
		// twin is buffered when grouped.
		env := predictEnvelope(t, p, fsys, "brand.pulse", shape(semScoreSlotReq(inlineParams(), false)))
		if got := env.Data.(*descriptor.PredictResult).Streamable; got == grouped {
			t.Fatalf("predict inline grouped=%v streamable %v", grouped, got)
		}
		penv, err := p.PredictCompose(ctx, &pulse.ComposedRequest{
			Fits:     []types.FitSpec{{Name: "brand", Request: semFitRequest(semFitKnobs())}},
			Requests: []*types.Request{shape(semScoreSlotReq(boundParams(), false))},
		})
		if err != nil || len(penv.Errors) != 0 {
			t.Fatalf("predict compose: %v %+v", err, penv)
		}
		pj, _ := json.Marshal(penv.Data)
		if !bytes.Contains(pj, []byte(`"sem":{"operator":"MAT_SEM","labels":["q1"`)) {
			t.Fatalf("predict compose lists no sem fit summary: %s", pj)
		}
		var slots struct {
			Slots []struct {
				Streamable bool `json:"streamable"`
			} `json:"slots"`
		}
		if json.Unmarshal(pj, &slots) != nil || len(slots.Slots) != 1 || !slots.Slots[0].Streamable {
			t.Fatalf("grouped=%v: predict calls the bound slot buffered: %s", grouped, pj)
		}
	}
}

// TestSEMFit_BoundMergesAcrossShards: the bound slot merges, so a shard
// archive runs it on the per-shard reducer (ShardWorkers 3) to the
// single file's figures, grouped and not; the fit itself, taken on the
// archive, is the single file's fit.
func TestSEMFit_BoundMergesAcrossShards(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	run := func(shards, workers int, grouped bool) (string, string) {
		p, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8, shards: shards}.build(t, pulse.Options{ShardWorkers: workers})
		slot := semScoreSlotReq(boundParams(), false)
		if grouped {
			slot = byWave(slot)
		}
		out, err := p.Compose(ctx, &pulse.ComposedRequest{
			Fits:     []types.FitSpec{{Name: "brand", Request: semFitRequest(semFitKnobs())}},
			Requests: []*types.Request{slot},
		})
		if err != nil {
			t.Fatalf("shards=%d workers=%d: %v", shards, workers, err)
		}
		fit, _ := json.Marshal(out.Fits["brand"])
		return dataJSON(t, out.Responses[0].Data) + dataJSON(t, out.Responses[0].Warnings), string(fit)
	}
	for _, grouped := range []bool{false, true} {
		want, wantFit := run(0, 1, grouped)
		for _, w := range []int{1, 3} {
			got, gotFit := run(400, w, grouped)
			if got != want {
				t.Fatalf("grouped=%v ShardWorkers %d: archive differs from the single file\n got %s\nwant %s", grouped, w, got, want)
			}
			if gotFit != wantFit {
				t.Fatalf("ShardWorkers %d: the archive's fit differs from the single file's", w)
			}
		}
	}
}

// TestSEMFit_BoundChainStage: a ProcessChain stage scoring a bound fit
// passes the chain's mergeability gate (row-local) where its inline twin
// is PULSE_CHAIN_NOT_MERGEABLE, on predict and runtime alike; a chain
// binds no compose fit, so a bound stage naming none is its unbound
// name, PULSE_MODEL_UNKNOWN (one loading a saved SEM fit file runs:
// TestSEMFit_LoadedChainStage).
func TestSEMFit_BoundChainStage(t *testing.T) {
	t.Parallel()

	p, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	ctx := context.Background()
	chain := func(params map[string]any) *pulse.ChainRequest {
		s0 := semScoreSlotReq(params, false)
		s0.Cohort = nil
		s1 := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n", Label: "rows"}}}
		return &pulse.ChainRequest{Cohort: &types.Cohort{Filename: "brand.pulse"},
			Stages: []*types.ChainStage{{Name: "s0", Request: s0}, {Name: "s1", Request: s1}}}
	}
	inline := chain(inlineParams())
	env, err := p.PredictChain(ctx, inline)
	if err != nil {
		t.Fatal(err)
	}
	pe := semEnvError(t, env, errors.PULSE_CHAIN_NOT_MERGEABLE)
	_, rerr := p.ProcessChain(ctx, inline)
	semSameRefusal(t, "ProcessChain", rerr, pe)
	bound := chain(boundParams())
	env, err = p.PredictChain(ctx, bound)
	if err != nil {
		t.Fatal(err)
	}
	pe = semEnvError(t, env, errors.PULSE_MODEL_UNKNOWN)
	_, rerr = p.ProcessChain(ctx, bound)
	semSameRefusal(t, "ProcessChain", rerr, pe)
}

// TestSEMFit_KindMismatchAndUnknown: one namespace, three kinds — a SEM
// consumer naming a vector or regression fit, a vector consumer (a
// fitted attribute, a matrix params.fit) or a regression score naming
// the SEM fit, are PULSE_MODEL_KIND_MISMATCH (kind / expected); an
// unbound name is PULSE_MODEL_UNKNOWN listing the SEM fits. Runtime and
// PredictCompose return the same entry.
func TestSEMFit_KindMismatchAndUnknown(t *testing.T) {
	t.Parallel()

	p, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	ctx := context.Background()
	vecFit := semFitRequest(nil)
	vecFit.Matrices[0].Type, vecFit.Matrices[0].Params = types.MAT_COVARIANCE, nil
	regFit := &types.Request{Cohort: &types.Cohort{Filename: "brand.pulse"},
		Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "recommend", Predictors: []string{"q1", "v1"}}}}
	fits := []types.FitSpec{
		{Name: "brand", Request: semFitRequest(semFitKnobs())},
		{Name: "vec", Request: vecFit},
		{Name: "reg", Request: regFit},
	}
	slot := func(typ types.AttributeType, params string) *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: "brand.pulse"},
			Attributes:   []*types.Attribute{{Type: typ, Label: "x", Fields: brandMembers, Params: json.RawMessage(params)}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}}}
	}
	matrixSlot := &types.Request{Cohort: &types.Cohort{Filename: "brand.pulse"},
		Matrices: []types.MatrixSpec{{Name: "m", Type: types.MAT_CORRELATION, Fields: brandMembers, Params: json.RawMessage(`{"fit":"brand"}`)}}}
	predictSlot := &types.Request{Cohort: &types.Cohort{Filename: "brand.pulse"},
		Attributes:   []*types.Attribute{{Type: types.ATTR_REG_PREDICT, Label: "yhat", Params: json.RawMessage(`{"fit":"brand"}`)}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "yhat", Label: "n"}}}
	for _, c := range []struct {
		name           string
		slot           *types.Request
		code           errors.Code
		kind, expected string
	}{
		{"sem score names a vector fit", slot(types.ATTR_SEM_SCORE, `{"fit":"vec","latent":"service"}`), errors.PULSE_MODEL_KIND_MISMATCH, "vector", "sem"},
		{"sem score names a regression", slot(types.ATTR_SEM_SCORE, `{"fit":"reg","latent":"service"}`), errors.PULSE_MODEL_KIND_MISMATCH, "regression", "sem"},
		{"fitted attribute names the sem fit", slot(types.ATTR_MAHALANOBIS, `{"fit":"brand"}`), errors.PULSE_MODEL_KIND_MISMATCH, "sem", "vector"},
		{"matrix names the sem fit", matrixSlot, errors.PULSE_MODEL_KIND_MISMATCH, "sem", "vector"},
		{"regression score names the sem fit", predictSlot, errors.PULSE_MODEL_KIND_MISMATCH, "sem", "regression"},
		{"unbound", slot(types.ATTR_SEM_SCORE, `{"fit":"nope","latent":"service"}`), errors.PULSE_MODEL_UNKNOWN, "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			composed := &pulse.ComposedRequest{Fits: fits, Requests: []*types.Request{c.slot}}
			_, rerr := p.Compose(ctx, composed)
			var ce *errors.CodedError
			if !stderrors.As(rerr, &ce) || ce.Code != c.code {
				t.Fatalf("runtime %v, want %s", rerr, c.code)
			}
			if c.kind != "" && (ce.Details["kind"] != c.kind || ce.Details["expected"] != c.expected) {
				t.Fatalf("details %v, want kind %s expected %s", ce.Details, c.kind, c.expected)
			}
			if c.code == errors.PULSE_MODEL_UNKNOWN {
				if av, _ := ce.Details["available"].([]string); len(av) != 1 || av[0] != "brand" {
					t.Fatalf("available %v, want the SEM fits only", ce.Details["available"])
				}
			}
			env, err := p.PredictCompose(ctx, composed)
			if err != nil {
				t.Fatal(err)
			}
			semSameRefusal(t, "Compose", rerr, semEnvError(t, env, c.code))
		})
	}
}

// TestSEMFit_FailedFitScoresNull: a SEM fit whose MAT_SEM fails on its
// data (one wave left: the multi-group fit needs two) keeps its entry
// {"sem": {operator, error}}, lifts PULSE_SLOT_FAILED (details.fit) and
// binds a failed fit: the scoring slot runs, every score null with
// PULSE_FIT_UNAVAILABLE carrying the fit's code; a vector consumer of it
// is still the kind mismatch (its save writes nothing:
// TestSEMFit_SaveAndLoadRefusals).
func TestSEMFit_FailedFitScoresNull(t *testing.T) {
	t.Parallel()

	p, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	ctx := context.Background()
	oneWave := semFitRequest(semFitKnobs())
	oneWave.Filterers = []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "wave", Values: []string{"1"}}}
	out, err := p.Compose(ctx, &pulse.ComposedRequest{
		Fits:     []types.FitSpec{{Name: "brand", Request: oneWave}},
		Requests: []*types.Request{semScoreSlotReq(boundParams(), false)},
	})
	if err != nil {
		t.Fatal(err)
	}
	fit := out.Fits["brand"]
	if fit == nil || fit.SEM == nil || fit.SEM.Error == nil || fit.SEM.Error.Code != string(errors.PULSE_MATRIX_INSUFFICIENT_N) {
		t.Fatalf("fits[brand] = %+v, want a failed sem entry", fit)
	}
	wire, _ := json.Marshal(fit)
	var arm map[string]map[string]any
	if json.Unmarshal(wire, &arm) != nil || len(arm["sem"]) != 2 || arm["sem"]["operator"] != "MAT_SEM" {
		t.Fatalf("failed wire %s, want {\"sem\": {operator, error}}", wire)
	}
	lifted := false
	for _, w := range out.Warnings {
		if w.Code == string(errors.PULSE_SLOT_FAILED) && w.Details["fit"] == 0 {
			lifted = true
		}
	}
	if !lifted {
		t.Fatalf("compose warnings %+v lack the fit's PULSE_SLOT_FAILED", out.Warnings)
	}
	resp := out.Responses[0]
	if !strings.Contains(dataJSON(t, resp.Data), `"n":0`) {
		t.Fatalf("scores not null: %s", dataJSON(t, resp.Data))
	}
	unavailable := false
	for _, w := range resp.Warnings {
		if w.Code == string(errors.PULSE_FIT_UNAVAILABLE) && w.Details["code"] == string(errors.PULSE_MATRIX_INSUFFICIENT_N) {
			unavailable = true
		}
	}
	if !unavailable {
		t.Fatalf("slot warnings %+v lack PULSE_FIT_UNAVAILABLE", resp.Warnings)
	}
}

// semEnvError returns predict's envelope error carrying code, or fails.
func semEnvError(t *testing.T, env *descriptor.Envelope, code errors.Code) *descriptor.EnvelopeEntry {
	t.Helper()
	for _, e := range env.Errors {
		if e.Code == string(code) {
			return e
		}
	}
	t.Fatalf("predict reports no %s: %+v", code, env.Errors)
	return nil
}

// semSameRefusal: the runtime error carries predict's code, message and
// details exactly.
func semSameRefusal(t *testing.T, what string, rerr error, pe *descriptor.EnvelopeEntry) {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(rerr, &ce) {
		t.Fatalf("%s: runtime error is not coded: %v", what, rerr)
	}
	want, _ := json.Marshal(ce.Details)
	have, _ := json.Marshal(pe.Details)
	if string(ce.Code) != pe.Code || ce.Message != pe.Message || string(want) != string(have) {
		t.Errorf("%s: runtime %s %q %s\npredict %s %q %s", what, ce.Code, ce.Message, want, pe.Code, pe.Message, have)
	}
}

// TestSEMFit_BoundScoresMatchInlineAboveMergeBlock: past one merge block
// per group (linalg.MergeBlockSize fit rows a wave) the inline fit folds
// its co-moments through MAT_SEM's blocked merge, so its scores still
// equal the bound fit's to the bit — the single file at DecodeWorkers 1
// and 4, and a shard archive (shard edges inside blocks) at ShardWorkers
// 1 and 3, ungrouped and by wave; the archive's inline figures are the
// single file's.
func TestSEMFit_BoundScoresMatchInlineAboveMergeBlock(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const repeat = 8 // 1,200 brand rows × 8: 4,800 fit rows a wave
	for _, grouped := range []bool{false, true} {
		shape := func(r *types.Request) *types.Request {
			if grouped {
				return byWave(r)
			}
			return r
		}
		var single string
		for _, c := range []struct {
			name   string
			shards int
			opts   pulse.Options
		}{
			{"single/decode1", 0, pulse.Options{DecodeWorkers: 1}},
			{"single/decode4", 0, pulse.Options{DecodeWorkers: 4}},
			{"archive/shard1", 1500, pulse.Options{ShardWorkers: 1}},
			{"archive/shard3", 1500, pulse.Options{ShardWorkers: 3}},
		} {
			p, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8, shards: c.shards,
				repeat: func(int) int { return repeat }}.build(t, c.opts)
			// The sum is exact only serially; the order-free figures
			// carry the bits on the merging arms.
			withSum := c.opts.DecodeWorkers == 1 || c.opts.ShardWorkers == 1
			want, err := p.Process(ctx, shape(semScoreSlotReq(inlineParams(), withSum)))
			if err != nil {
				t.Fatalf("%s grouped=%v: %v", c.name, grouped, err)
			}
			out, err := p.Compose(ctx, &pulse.ComposedRequest{
				Fits:     []types.FitSpec{{Name: "brand", Request: semFitRequest(semFitKnobs())}},
				Requests: []*types.Request{shape(semScoreSlotReq(boundParams(), withSum))},
			})
			if err != nil {
				t.Fatalf("%s grouped=%v: %v", c.name, grouped, err)
			}
			g, w := dataJSON(t, out.Responses[0].Data), dataJSON(t, want.Data)
			if g != w {
				t.Fatalf("%s grouped=%v: bound figures differ from the inline fit\n got %s\nwant %s", c.name, grouped, g, w)
			}
			if !strings.Contains(w, `"n":4800`) && !strings.Contains(w, `"n":9600`) {
				t.Fatalf("%s grouped=%v: vacuous — the fit is not past one merge block a wave: %s", c.name, grouped, w)
			}
			if len(want.Warnings) != 0 {
				t.Fatalf("%s grouped=%v: inline warnings %+v", c.name, grouped, want.Warnings)
			}
			if !withSum {
				continue
			}
			if single == "" {
				single = w
			} else if w != single {
				t.Fatalf("%s grouped=%v: inline figures differ from the single file's\n got %s\nwant %s", c.name, grouped, w, single)
			}
		}
	}
}
