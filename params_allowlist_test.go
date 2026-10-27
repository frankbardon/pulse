package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"slices"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// params_allowlist_test.go pins U44 #301 on the public surface: a
// slot's params object is checked against the operator's manifest
// Param list, and an unknown key is PULSE_REQUEST_UNKNOWN_FIELD —
// details.path locating it, a suggestion when one exists — identically
// in predict and every runtime arm. An extension operator is checked
// when its registration declares params; one that declares none passes
// its params through and predict says so with
// PULSE_ADVISORY_EXTENSION_PARAMS_UNDECLARED.

// unknownParam asserts err is the params refusal at path suggesting
// suggest ("" = no suggestion) and returns it.
func unknownParam(t *testing.T, err error, path, suggest string) *errors.CodedError {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_REQUEST_UNKNOWN_FIELD {
		t.Fatalf("err = %v, want PULSE_REQUEST_UNKNOWN_FIELD", err)
	}
	if ce.Details["path"] != path {
		t.Fatalf("details.path = %v, want %s (details %v)", ce.Details["path"], path, ce.Details)
	}
	sug, _ := ce.Details["suggestions"].(map[string]any)
	key := path[len(path)-len(lastSegment(path)):]
	if suggest != "" && sug[key] != suggest {
		t.Fatalf("suggestions = %v, want %s → %s", sug, key, suggest)
	}
	return ce
}

func lastSegment(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '.' {
			return path[i+1:]
		}
	}
	return path
}

// TestParamsRefusal_PredictMatchesRuntime: the GROUP_DATE params.unit
// misnomer (the bug internal/examples/crosstab/11 carried, silently
// grouping by month) is refused on Predict, Process and ProcessStream
// with the same entry, suggesting params.component; a nested root
// (Compose slot, Compose overlay, chain stage, facet overlay) adds its
// locator.
func TestParamsRefusal_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	fs, cohort := zoneCohort(t)
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	req := func() *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: cohort},
			Groups:       []*types.Group{{Type: types.GROUP_DATE, Field: "d", Params: json.RawMessage(`{"unit":"month"}`)}},
			Aggregations: countAgg()}
	}
	_, err := p.Process(ctx, req())
	pce := unknownParam(t, err, "groups[0].params.unit", "component")
	sameEntry(t, predictEnvelope(t, p, fs, cohort, req()), err)
	_, serr := p.ProcessStream(ctx, req())
	if ce := unknownParam(t, serr, "groups[0].params.unit", "component"); ce.Message != pce.Message {
		t.Fatalf("stream %q, process %q", ce.Message, pce.Message)
	}

	// The corrected key runs on both sides.
	ok := req()
	ok.Groups[0].Params = json.RawMessage(`{"component":"month"}`)
	if _, err := p.Process(ctx, ok); err != nil {
		t.Fatalf("params.component refused: %v", err)
	}
	if env := predictEnvelope(t, p, fs, cohort, ok); len(env.Errors) != 0 {
		t.Fatalf("predict refused params.component: %+v", env.Errors)
	}

	t.Run("compose slot", func(t *testing.T) {
		cr := func() *types.ComposedRequest {
			return &types.ComposedRequest{Requests: []*types.Request{req()}}
		}
		_, err := p.Compose(ctx, cr())
		ce := unknownParam(t, err, "groups[0].params.unit", "component")
		if ce.Details["request"] != 0 {
			t.Fatalf("details %v lack request 0", ce.Details)
		}
		env, perr := p.PredictCompose(ctx, cr())
		if perr != nil || len(env.Errors) == 0 || env.Errors[0].Code != string(ce.Code) || env.Errors[0].Message != ce.Message {
			t.Fatalf("PredictCompose = %+v / %v, want %s %q", env.Errors, perr, ce.Code, ce.Message)
		}
	})
	t.Run("compose overlay", func(t *testing.T) {
		slot := func(label string) *types.Request {
			return &types.Request{Label: label, Cohort: &types.Cohort{Filename: cohort}, Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}}, Aggregations: countAgg()}
		}
		cr := func() *types.ComposedRequest {
			return &types.ComposedRequest{Requests: []*types.Request{slot("a"), slot("b")},
				Overlays: []types.ComposeOverlaySpec{{Kind: types.OverlayKindIndexVsRef, Reference: "a", Targets: []string{"b"},
					Params: map[string]any{"scael": 1.0}}}}
		}
		_, err := p.Compose(ctx, cr())
		ce := unknownParam(t, err, "overlays[0].params.scael", "scale")
		env, perr := p.PredictCompose(ctx, cr())
		if perr != nil || len(env.Errors) == 0 || env.Errors[0].Code != string(ce.Code) || env.Errors[0].Message != ce.Message {
			t.Fatalf("PredictCompose = %+v / %v, want %s %q", env.Errors, perr, ce.Code, ce.Message)
		}
	})
	t.Run("chain stage", func(t *testing.T) {
		r := req()
		r.Cohort = nil
		ch := func() *types.ChainRequest {
			return &types.ChainRequest{Cohort: &types.Cohort{Filename: cohort}, Stages: []*types.ChainStage{{Name: "s", Request: r}}}
		}
		_, err := p.ProcessChain(ctx, ch())
		ce := unknownParam(t, err, "groups[0].params.unit", "component")
		if ce.Details["stage"] != 0 {
			t.Fatalf("details %v lack stage 0", ce.Details)
		}
		env, perr := p.PredictChain(ctx, ch())
		if perr != nil || len(env.Errors) == 0 || env.Errors[0].Code != string(ce.Code) || env.Errors[0].Message != ce.Message {
			t.Fatalf("PredictChain = %+v / %v, want %s %q", env.Errors, perr, ce.Code, ce.Message)
		}
	})
	t.Run("facet overlay", func(t *testing.T) {
		fr := func() *pulse.FacetRequest {
			return &pulse.FacetRequest{Cohort: &types.Cohort{Filename: cohort}, Fields: []string{"cat"},
				Overlays: []types.OverlaySpec{{Kind: types.OverlayKindShareOfTotal, Scope: types.OverlayScopeGroup,
					Params: json.RawMessage(`{"feild":"cat"}`)}}}
		}
		_, err := p.FacetSchema(ctx, fr())
		ce := unknownParam(t, err, "overlays[0].params.feild", "field")
		env, perr := p.PredictFacet(ctx, fr())
		if perr != nil || len(env.Errors) == 0 || env.Errors[0].Code != string(ce.Code) || env.Errors[0].Message != ce.Message {
			t.Fatalf("PredictFacet = %+v / %v, want %s %q", env.Errors, perr, ce.Code, ce.Message)
		}
	})
}

// paramsProbeAgg records the params object its factory was handed.
type paramsProbeAgg struct{ stubAggregator }

func paramsProbeFactory(seen *[]json.RawMessage) extend.AggregatorFactory {
	return func(a *types.Aggregation, _ *encoding.Schema) (extend.Aggregator, error) {
		*seen = append(*seen, a.Params)
		return &paramsProbeAgg{stubAggregator{value: 1}}, nil
	}
}

// TestParamsRefusal_ExtensionOperators: an extension aggregator whose
// registration declares params refuses an undeclared key (predict ≡
// runtime, with the nearest declared key suggested); one that declares
// none runs with its params passed through untouched, and predict
// stays valid but adds PULSE_ADVISORY_EXTENSION_PARAMS_UNDECLARED
// naming the slot and its keys — never for a declared operator, a
// built-in, or an undeclared operator called without params.
func TestParamsRefusal_ExtensionOperators(t *testing.T) {
	t.Parallel()

	var seen []json.RawMessage
	ext := pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{
		{Name: "AGG_ACME_DECLARED", Description: "Declares its params.", Factory: paramsProbeFactory(&seen), Streamable: true,
			Accepts: []encoding.FieldType{encoding.FieldTypeF64},
			Params:  []pulse.ParamMeta{{Name: "scale", JSONType: "number"}}},
		{Name: "AGG_ACME_FREEFORM", Description: "Declares no params.", Factory: paramsProbeFactory(&seen), Streamable: true,
			Accepts: []encoding.FieldType{encoding.FieldTypeF64}},
	}}
	p, cohort := brandScoreCohort(t, ext)
	ctx := context.Background()
	req := func(op types.AggregationType, params string) *types.Request {
		a := &types.Aggregation{Type: op, Field: "score", Label: "v"}
		if params != "" {
			a.Params = json.RawMessage(params)
		}
		return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Aggregations: []*types.Aggregation{a}}
	}
	advisories := func(r *types.Request) []descriptor.Advisory {
		t.Helper()
		res, err := p.Predict(ctx, r)
		if err != nil {
			t.Fatalf("Predict: %v", err)
		}
		if !res.Valid {
			t.Fatalf("predict invalid: %+v", res)
		}
		var out []descriptor.Advisory
		for _, a := range res.Advisories {
			if a.Code == string(errors.PULSE_ADVISORY_EXTENSION_PARAMS_UNDECLARED) {
				out = append(out, a)
			}
		}
		return out
	}

	t.Run("declared refuses an unknown key", func(t *testing.T) {
		_, err := p.Process(ctx, req("AGG_ACME_DECLARED", `{"scael":2}`))
		ce := unknownParam(t, err, "aggregations[0].params.scael", "scale")
		res, perr := p.Predict(ctx, req("AGG_ACME_DECLARED", `{"scael":2}`))
		if perr != nil || res.Valid || len(res.Errors) == 0 || res.Errors[0].Code != string(ce.Code) || res.Errors[0].Message != ce.Message {
			t.Fatalf("Predict = %+v / %v, want %s %q", res, perr, ce.Code, ce.Message)
		}
		if a := advisories(req("AGG_ACME_DECLARED", `{"scale":2}`)); len(a) != 0 {
			t.Fatalf("declared operator advised: %+v", a)
		}
	})
	t.Run("undeclared passes params through, with an advisory", func(t *testing.T) {
		seen = nil
		if _, err := p.Process(ctx, req("AGG_ACME_FREEFORM", `{"anything":1,"else":true}`)); err != nil {
			t.Fatalf("Process: %v", err)
		}
		if len(seen) == 0 || string(seen[len(seen)-1]) != `{"anything":1,"else":true}` {
			t.Fatalf("factory saw %q, want the params untouched", seen)
		}
		a := advisories(req("AGG_ACME_FREEFORM", `{"anything":1,"else":true}`))
		if len(a) != 1 || a[0].Details["slot"] != "aggregations[0]" || a[0].Details["operator"] != "AGG_ACME_FREEFORM" ||
			!slices.Equal(a[0].Details["keys"].([]string), []string{"anything", "else"}) {
			t.Fatalf("advisories = %+v", a)
		}
		if a := advisories(req("AGG_ACME_FREEFORM", "")); len(a) != 0 {
			t.Fatalf("params-free slot advised: %+v", a)
		}
		if a := advisories(req(types.AGG_SUM, "")); len(a) != 0 {
			t.Fatalf("built-in advised: %+v", a)
		}
	})
}
