package integration_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// spliceVerdict is one surface's refusal: code, message and the
// field / placeholder / slot details (a location key like `stage` or
// `request` aside).
type spliceVerdict struct {
	code, message, field, placeholder, slot string
}

func runtimeSplice(t *testing.T, err error) spliceVerdict {
	t.Helper()
	ce := codeOf(t, err)
	return spliceVerdict{string(ce.Code), ce.Message, str(ce.Details["field"]), str(ce.Details["placeholder"]), str(ce.Details["slot"])}
}

func predictSplice(t *testing.T, entries []*descriptor.EnvelopeEntry) spliceVerdict {
	t.Helper()
	if len(entries) == 0 {
		t.Fatal("predict reported no error")
	}
	e := entries[0]
	return spliceVerdict{e.Code, e.Message, str(e.Details["field"]), str(e.Details["placeholder"]), str(e.Details["slot"])}
}

func str(v any) string { s, _ := v.(string); return s }

func decodeInto[T any](t *testing.T, raw string) *T {
	t.Helper()
	v := new(T)
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		t.Fatal(err)
	}
	return v
}

// TestExprSplice_PredictRuntimeParity: an `{{axis}}` token inside an
// expression slot is refused with the same code, message and details by
// predict and the runtime on every surface an axis reaches — a Compose
// sweep (Compose, ComposeParallel, PredictCompose), an item for_each on
// an attribute and on a feature (Process, Materialize, Predict), the
// same item in a Compose slot (Compose, PredictCompose) and in a chain
// stage (ProcessChain, PredictChain). Windows carry no expression slot;
// their for_each beside the refused item is legal and never reached.
func TestExprSplice_PredictRuntimeParity(t *testing.T) {
	t.Parallel()

	p, _ := ewmaCohort(t)
	ctx := context.Background()

	t.Run("compose sweep", func(t *testing.T) {
		req := func() *pulse.ComposedRequest {
			return decodeInto[pulse.ComposedRequest](t, `{"sweep": {"axes": [{"name": "w", "values": [2, 3]}],
				"request": {"cohort": {"filename": "s.pulse"},
					"filterers": [{"type": "FILTER_EXPRESSION", "expression": "week > {{w}}"}],
					"aggregations": [{"type": "AGG_COUNT", "label": "n"}]}}}`)
		}
		want := spliceVerdict{code: string(errors.PULSE_SWEEP_EXPR_SPLICE), field: "sweep.request.filterers[0].expression", placeholder: "w", slot: "expression"}
		env, err := p.PredictCompose(ctx, req())
		if err != nil {
			t.Fatal(err)
		}
		pv := predictSplice(t, env.Errors)
		want.message = pv.message
		if pv != want {
			t.Fatalf("PredictCompose %+v, want %+v", pv, want)
		}
		_, err = p.Compose(ctx, req())
		if got := runtimeSplice(t, err); got != want {
			t.Fatalf("Compose %+v, want %+v", got, want)
		}
		_, err = p.ComposeParallel(ctx, req(), pulse.ComposeOptions{})
		if got := runtimeSplice(t, err); got != want {
			t.Fatalf("ComposeParallel %+v, want %+v", got, want)
		}
	})

	items := []struct {
		name, body string
		want       spliceVerdict
	}{
		{"attribute", `"attributes": [{"for_each": {"axes": [{"name": "k", "values": [2, 3]}]},
				"type": "ATTR_FORMULA", "field": "week", "label": "x_by{{k}}", "expression": "week * {{k}}"}]`,
			spliceVerdict{code: string(errors.PULSE_FOR_EACH_EXPR_SPLICE), field: "attributes[0].expression", placeholder: "k", slot: "expression"}},
		{"feature", `"features": [{"for_each": {"axes": [{"name": "k", "values": [2, 3]}]},
				"type": "FEAT_TARGET_ENCODE", "field": "week", "label": "te{{k}}", "params": {"target": "x", "fit_on": "week < {{k}}"}}]`,
			spliceVerdict{code: string(errors.PULSE_FOR_EACH_EXPR_SPLICE), field: "features[0].params.fit_on", placeholder: "k", slot: "feature_fit_on"}},
	}
	const window = `"windows": [{"for_each": {"axes": [{"name": "d", "values": [0.3, 0.5]}]}, "type": "WIN_EWMA", "field": "x",
		"label": "x_{{d}}", "order_by": [{"field": "week"}], "frame": {"mode": "rows"}, "params": {"decay": {"$var": "d"}}}]`
	for _, it := range items {
		t.Run("for_each "+it.name, func(t *testing.T) {
			stage := func() *types.Request {
				return decodeInto[types.Request](t, `{`+window+`, `+it.body+`, "aggregations": [{"type": "AGG_COUNT", "label": "n"}]}`)
			}
			req := func() *types.Request {
				r := stage()
				r.Cohort = &types.Cohort{Filename: "s.pulse"}
				return r
			}
			want := it.want

			pres, err := p.Predict(ctx, req())
			if err != nil {
				t.Fatal(err)
			}
			if pres.Valid {
				t.Fatal("Predict judged the request valid")
			}
			pv := predictSplice(t, pres.Errors)
			want.message = pv.message
			if pv != want {
				t.Fatalf("Predict %+v, want %+v", pv, want)
			}
			_, err = p.Process(ctx, req())
			if got := runtimeSplice(t, err); got != want {
				t.Fatalf("Process %+v, want %+v", got, want)
			}
			_, err = p.Materialize(ctx, req(), "out.pulse", pulse.MaterializeOptions{})
			if got := runtimeSplice(t, err); got != want {
				t.Fatalf("Materialize %+v, want %+v", got, want)
			}

			// The same item in a Compose slot.
			cenv, err := p.PredictCompose(ctx, &pulse.ComposedRequest{Requests: []*types.Request{req()}})
			if err != nil {
				t.Fatal(err)
			}
			_, err = p.Compose(ctx, &pulse.ComposedRequest{Requests: []*types.Request{req()}})
			rv := runtimeSplice(t, err)
			if pv := predictSplice(t, cenv.Errors); pv.code != want.code || pv.field != want.field || rv.code != want.code ||
				!reflect.DeepEqual(cenv.Errors[0].Details["slot"], codeOf(t, err).Details["slot"]) {
				t.Fatalf("compose slot: predict %+v, runtime %+v, want %+v", pv, rv, want)
			}

			// The same item in a chain stage, located by details.stage.
			chain := func() *pulse.ChainRequest {
				return &pulse.ChainRequest{Cohort: &types.Cohort{Filename: "s.pulse"}, Stages: []*types.ChainStage{{Request: stage()}}}
			}
			_, err = p.ProcessChain(ctx, chain())
			rce := codeOf(t, err)
			chenv, err := p.PredictChain(ctx, chain())
			if err != nil {
				t.Fatal(err)
			}
			if len(chenv.Errors) == 0 || chenv.Errors[0].Code != string(rce.Code) || rce.Code != errors.PULSE_FOR_EACH_EXPR_SPLICE ||
				chenv.Errors[0].Details["stage"] != rce.Details["stage"] || chenv.Errors[0].Details["field"] != want.field ||
				chenv.Errors[0].Message != rce.Message {
				t.Fatalf("chain: predict %+v, runtime %s %s %v", chenv.Errors, rce.Code, rce.Message, rce.Details)
			}
		})
	}
}
