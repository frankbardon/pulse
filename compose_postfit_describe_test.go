package pulse

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// postFitFeatures is a closed profile that runs postFitComposed's batch
// (a count slot, a REG_OLS fit, an optimize block) with the post-fit
// slot offered; extra names are appended.
func postFitFeatures(extra ...string) []string {
	return append([]string{"capability:compose", "capability:compose_fits", "capability:compose_optimize",
		descx.FeatureComposePostFit, string(types.AGG_COUNT), string(types.REG_OLS)}, extra...)
}

// postFitProfiled is postFitPulse under a feature profile.
func postFitProfiled(t *testing.T, features []string) *Pulse {
	t.Helper()
	return postFitPulse(t, Options{FeatureProfile: &FeatureProfile{Profile: "post-fit", Features: features}})
}

// postFitPredictError is PredictCompose's first error (nil when it
// admits the batch).
func postFitPredictError(t *testing.T, p *Pulse, c *ComposedRequest) *descriptor.EnvelopeEntry {
	t.Helper()
	env, err := p.PredictCompose(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) == 0 {
		return nil
	}
	return env.Errors[0]
}

// TestPostFit_PredictMatchesRuntime: every post-fit refusal is the same
// coded error — code, message and details — at PredictCompose and on
// Compose and ComposeParallel; a valid batch predicts clean.
func TestPostFit_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	p := postFitPulse(t, Options{})
	for _, tc := range []struct {
		name string
		raw  string
		code perr.Code
	}{
		{"label missing", `[{"type":"POSTFIT_TEST_ECHO","fits":["m"]}]`, perr.PULSE_POSTFIT_INVALID},
		{"label duplicate", `[{"label":"e","type":"POSTFIT_TEST_ECHO"},{"label":"e","type":"POSTFIT_TEST_ECHO"}]`, perr.PULSE_POSTFIT_INVALID},
		{"type missing", `[{"label":"e","fits":["m"]}]`, perr.PULSE_POSTFIT_INVALID},
		{"type unknown", `[{"label":"e","type":"POSTFIT_TEST_NOPE","fits":["m"]}]`, perr.PULSE_POSTFIT_INVALID},
		{"fit unknown", `[{"label":"e","type":"POSTFIT_TEST_ECHO","fits":["zz"]}]`, perr.PULSE_POSTFIT_INVALID},
		{"fit duplicate", `[{"label":"e","type":"POSTFIT_TEST_ECHO","fits":["m","m"]}]`, perr.PULSE_POSTFIT_INVALID},
		{"param unknown", `[{"label":"e","type":"POSTFIT_TEST_ECHO","fits":["m"],"params":{"scael":2}}]`, perr.PULSE_REQUEST_UNKNOWN_FIELD},
		{"param wrong type", `[{"label":"e","type":"POSTFIT_TEST_ECHO","fits":["m"],"params":{"scale":"two"}}]`, perr.PULSE_REQUEST_PARAM_INVALID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := postFitComposed(t, tc.raw)
			pe := postFitPredictError(t, p, c)
			if pe == nil || pe.Code != string(tc.code) {
				t.Fatalf("predict %+v, want %s", pe, tc.code)
			}
			for name, run := range composeRuns(p) {
				resp, err := run(postFitComposed(t, tc.raw))
				ce := requireCoded(t, err, tc.code)
				if resp != nil {
					t.Fatalf("%s: a refused batch returned a response", name)
				}
				if pe.Message != ce.Message || !reflect.DeepEqual(pe.Details, ce.Details) {
					t.Fatalf("%s: predict %s %v differs from runtime %s %v", name, pe.Message, pe.Details, ce.Message, ce.Details)
				}
			}
		})
	}

	if pe := postFitPredictError(t, p, postFitComposed(t, `[{"label":"echo","type":"POSTFIT_TEST_ECHO","fits":["m"],"params":{"scale":2}}]`)); pe != nil {
		t.Fatalf("predict refused the valid batch: %+v", pe)
	}
}

// TestPostFit_HiddenOperatorActsNeverRegistered: a profile offering the
// post-fit slot but omitting the operator hides it on every surface —
// predict and both runs refuse it as an unknown type with nothing
// available, the manifest lists no post-fit operator and the instance
// payload schema offers no post-fit type; the profile enabling it
// describes it everywhere.
func TestPostFit_HiddenOperatorActsNeverRegistered(t *testing.T) {
	t.Parallel()

	raw := `[{"label":"echo","type":"POSTFIT_TEST_ECHO","fits":["m"]}]`
	hidden := postFitProfiled(t, postFitFeatures())
	pe := postFitPredictError(t, hidden, postFitComposed(t, raw))
	if pe == nil || pe.Code != string(perr.PULSE_POSTFIT_INVALID) || pe.Details["reason"] != "type_unknown" {
		t.Fatalf("predict %+v, want type_unknown", pe)
	}
	if got := toStrings(pe.Details["available"]); len(got) != 0 {
		t.Fatalf("predict available %v, want none", got)
	}
	for name, run := range composeRuns(hidden) {
		_, err := run(postFitComposed(t, raw))
		ce := requireCoded(t, err, perr.PULSE_POSTFIT_INVALID)
		if ce.Message != pe.Message || !reflect.DeepEqual(ce.Details, pe.Details) {
			t.Fatalf("%s: runtime %s %v differs from predict %s %v", name, ce.Message, ce.Details, pe.Message, pe.Details)
		}
	}
	if got := hidden.Manifest(context.Background()).Extensions.PostFits; len(got) != 0 {
		t.Fatalf("manifest lists a hidden post-fit operator: %+v", got)
	}
	if enum := postFitTypeEnum(t, hidden); len(enum) != 0 {
		t.Fatalf("payload schema offers hidden post-fit types %v", enum)
	}

	shown := postFitProfiled(t, postFitFeatures("POSTFIT_TEST_ECHO"))
	if pe := postFitPredictError(t, shown, postFitComposed(t, raw)); pe != nil {
		t.Fatalf("predict refused the enabled operator: %+v", pe)
	}
	if got := shown.Manifest(context.Background()).Extensions.PostFits; len(got) != 1 || got[0].Name != "POSTFIT_TEST_ECHO" {
		t.Fatalf("manifest post_fits %+v", got)
	}
	if enum := postFitTypeEnum(t, shown); !slices.Equal(enum, []string{"POSTFIT_TEST_ECHO"}) {
		t.Fatalf("payload schema post-fit types %v", enum)
	}
}

// TestPostFit_HiddenCapability: without capability:compose_post_fit the
// `post_fit` request and response keys are hidden — a set slot is an
// unknown field at predict and on both runs — and a profile naming a
// post-fit operator without the capability is refused at pulse.New.
func TestPostFit_HiddenCapability(t *testing.T) {
	t.Parallel()

	feats := slices.DeleteFunc(postFitFeatures(), func(n string) bool { return n == descx.FeatureComposePostFit })
	h := postFitProfiled(t, feats)
	snap := h.svc.InstanceSnapshot()
	hidden := descx.HiddenSlotKeys(&types.ComposedRequest{}, snap)
	if !slices.Contains(hidden, "post_fit") || slices.Contains(hidden, "fits") {
		t.Fatalf("request hidden keys %v, want post_fit (and not fits)", hidden)
	}
	if !slices.Contains(descx.HiddenSlotKeys(&types.ComposedResponse{}, snap), "post_fit") {
		t.Fatal("the response post_fit key is not hidden with the capability")
	}
	raw := `[{"label":"echo","type":"POSTFIT_TEST_ECHO","fits":["m"]}]`
	ctx := context.Background()
	entries := map[string]func() *slotGateOutcome{
		"Compose": func() *slotGateOutcome {
			_, err := h.Compose(ctx, postFitComposed(t, raw))
			return slotGateFromErr(err)
		},
		"ComposeParallel": func() *slotGateOutcome {
			_, err := h.ComposeParallel(ctx, postFitComposed(t, raw), ComposeOptions{MaxWorkers: 2})
			return slotGateFromErr(err)
		},
		"PredictCompose": func() *slotGateOutcome {
			env, err := h.PredictCompose(ctx, postFitComposed(t, raw))
			if err != nil {
				t.Fatal(err)
			}
			return slotGateFromEnv(env)
		},
	}
	for name, run := range entries {
		t.Run(name, func(t *testing.T) {
			assertSlotRefusal(t, run(), "post_fit", hidden)
		})
	}

	_, err := New(Options{FS: parityFS(t), Extensions: Extensions{PostFits: []PostFitRegistration{postFitEcho()}},
		FeatureProfile: &FeatureProfile{Profile: "no-slot", Features: append(feats, "POSTFIT_TEST_ECHO")}})
	requireCoded(t, err, perr.PULSE_FEATURE_PROFILE_DEPENDENCY)
}

// TestPostFit_ManifestEntry: a registered post-fit operator is listed in
// the manifest's extensions with its params and its declared output
// schema, and the instance payload schema offers its name as a post-fit
// type; a post-fit-free instance lists none and offers none.
func TestPostFit_ManifestEntry(t *testing.T) {
	t.Parallel()

	p := postFitPulse(t, Options{})
	got := p.Manifest(context.Background()).Extensions.PostFits
	if len(got) != 1 {
		t.Fatalf("manifest post_fits %+v", got)
	}
	m := got[0]
	if m.Name != "POSTFIT_TEST_ECHO" || m.Namespace != "TEST" || m.Description != "echoes a fit's coefficients" {
		t.Fatalf("identity %+v", m)
	}
	if len(m.Params) != 2 || m.Params[0].Name != "scale" || m.Params[0].JSONType != "number" {
		t.Fatalf("params %+v", m.Params)
	}
	want := descriptor.PostFitOutputsMeta{
		Scalars: []string{"slope", "n", "objective", "undefined"},
		Tables: []descriptor.PostFitTableMeta{{Name: "coefs", Columns: []descriptor.PostFitColumnMeta{
			{Name: "term", Kind: "string"}, {Name: "estimate", Kind: "float"}, {Name: "note", Kind: "string"},
		}}},
	}
	if !reflect.DeepEqual(m.Outputs, want) {
		t.Fatalf("outputs %+v, want %+v", m.Outputs, want)
	}
	if enum := postFitTypeEnum(t, p); !slices.Equal(enum, []string{"POSTFIT_TEST_ECHO"}) {
		t.Fatalf("payload schema post-fit types %v", enum)
	}

	bare := sweepPulse(t, Options{})
	raw, err := json.Marshal(bare.Manifest(context.Background()).Extensions)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"post_fits":[]`)) {
		t.Fatalf("a post-fit-free manifest does not list an empty post_fits: %s", raw)
	}
	if enum := postFitTypeEnum(t, bare); enum == nil || len(enum) != 0 {
		t.Fatalf("post-fit-free payload schema post-fit types %v, want an empty enum", enum)
	}
}

// TestPostFit_OutsideComposeReturn: a Compose-level `return` shapes the
// top-level overlays only; `post_fit` is always emitted whole, at full
// precision, like `optimizations` and `comparisons`.
func TestPostFit_OutsideComposeReturn(t *testing.T) {
	t.Parallel()

	p := postFitPulse(t, Options{})
	c := postFitComposed(t, `[{"label":"echo","type":"POSTFIT_TEST_ECHO","fits":["m"],"params":{"scale":3}}]`)
	unshaped, err := p.Compose(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(unshaped.PostFit)
	if err != nil {
		t.Fatal(err)
	}
	c = postFitComposed(t, `[{"label":"echo","type":"POSTFIT_TEST_ECHO","fits":["m"],"params":{"scale":3}}]`)
	c.Return = &types.Return{Include: []string{"overlays"}, Precision: 1}
	for name, run := range composeRuns(p) {
		resp, err := run(c)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if resp.Returned == nil {
			t.Fatalf("%s: vacuous: the Compose return did not shape the response", name)
		}
		raw, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		var wire struct {
			PostFit json.RawMessage `json:"post_fit"`
		}
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(wire.PostFit, want) {
			t.Fatalf("%s: post_fit under a Compose return\n got %s\nwant %s", name, wire.PostFit, want)
		}
	}

	// The root is not addressable: like every non-overlay root, a
	// Compose return naming it is refused.
	c.Return = &types.Return{Exclude: []string{"post_fit"}}
	for name, run := range composeRuns(p) {
		if _, err := run(c); err == nil {
			t.Fatalf("%s: a Compose return excluding post_fit was accepted", name)
		} else {
			requireCoded(t, err, perr.PULSE_RETURN_INVALID)
		}
	}
}

// TestPostFit_RegistrationGuidance: a post-fit registration's Purpose is
// validated with the built-in tier's rules and projects its intents onto
// the manifest entry; an unknown DependsOn is refused like any extension
// operator's.
func TestPostFit_RegistrationGuidance(t *testing.T) {
	t.Parallel()

	reg := postFitEcho()
	reg.Purpose = validExtPurpose()
	p, err := New(Options{FS: parityFS(t), Extensions: Extensions{PostFits: []PostFitRegistration{reg}}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := p.Manifest(context.Background()).Extensions.PostFits
	if len(got) != 1 || !slices.Equal(got[0].Intents, []string{descx.IntentDescribe, descx.IntentMeasureConstruct}) {
		t.Fatalf("manifest intents %+v", got)
	}

	bad := postFitEcho()
	bad.Purpose = validExtPurpose()
	bad.Purpose.Plain = ""
	_, err = New(Options{FS: parityFS(t), Extensions: Extensions{PostFits: []PostFitRegistration{bad}}})
	if ce := purposeInvalid(t, err); ce.Details["category"] != string(categoryPostFit) {
		t.Fatalf("details %+v", ce.Details)
	}

	dep := postFitEcho()
	dep.DependsOn = []string{"AGG_NOPE_MISSING"}
	_, err = New(Options{FS: parityFS(t), Extensions: Extensions{PostFits: []PostFitRegistration{dep}}})
	requireCoded(t, err, perr.PULSE_FEATURE_PROFILE_UNKNOWN)
}

// postFitTypeEnum is the instance payload schema's PostFitType enum (nil
// when the def is absent).
func postFitTypeEnum(t *testing.T, p *Pulse) []string {
	t.Helper()
	raw, err := p.PayloadSchema()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Defs map[string]struct {
			Enum []string `json:"enum"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	d, ok := doc.Defs["PostFitType"]
	if !ok {
		return nil
	}
	if d.Enum == nil {
		return []string{}
	}
	return d.Enum
}

const postFitExtSkill = `---
name: op-postfit-test-echo
description: Echoes a fit's coefficients after the batch's fits have run.
kind: operator
category: POSTFIT
operator: POSTFIT_TEST_ECHO
type: reference
applies_to: compose
---

# POSTFIT_TEST_ECHO

## Params
scale, mode.

## Inputs
One regression fit.

## Output
Scalars and one table.

## Gotchas
None.

## See
None.
`

// TestPostFit_EmbedderSkill: an embedder's atomic op-postfit-* skill
// documents a registered post-fit operator like any extension operator's
// — served, graphed under an operator node that requires
// capability:compose_post_fit — and is pruned with the operator.
func TestPostFit_EmbedderSkill(t *testing.T) {
	t.Parallel()

	skills := fstest.MapFS{"op-postfit-test-echo.md": {Data: []byte(postFitExtSkill)}}
	p := postFitPulse(t, Options{Extensions: Extensions{Skills: skills}})
	if body, ok := p.Skill("op-postfit-test-echo"); !ok || !strings.Contains(body, "POSTFIT_TEST_ECHO") {
		t.Fatalf("skill not served: %v %q", ok, body)
	}
	g := p.Ontology()
	for _, e := range []descriptor.OntologyEdge{
		{From: "operator:POSTFIT_TEST_ECHO", To: "skill:op-postfit-test-echo", Kind: descriptor.OntologyEdgeDocumentedBy},
		{From: "operator:POSTFIT_TEST_ECHO", To: "capability:" + strings.TrimPrefix(descx.FeatureComposePostFit, "capability:"), Kind: descriptor.OntologyEdgeRequiresCapability},
	} {
		if !slices.ContainsFunc(g.Edges, func(x descriptor.OntologyEdge) bool { return x.From == e.From && x.To == e.To && x.Kind == e.Kind }) {
			t.Errorf("no %s edge %s → %s", e.Kind, e.From, e.To)
		}
	}

	hidden := postFitPulse(t, Options{Extensions: Extensions{Skills: skills},
		FeatureProfile: &FeatureProfile{Profile: "post-fit", Features: postFitFeatures()}})
	if _, ok := hidden.Skill("op-postfit-test-echo"); ok {
		t.Fatal("the skill of a hidden post-fit operator is served")
	}
}
