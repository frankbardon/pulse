package pulse

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"sync/atomic"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/internal/sweep"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// sweepSlotBody is a grouped slot over the parity cohort whose
// aggregator is the `op` axis.
const sweepSlotBody = `{"cohort":{"filename":"` + parityCohort + `"},
	"groups":[{"type":"GROUP_CATEGORY","field":"region"}],
	"aggregations":[{"type":"{{op}}","field":"age","label":"v"}]}`

func sweepPulse(t *testing.T, opts Options) *Pulse {
	t.Helper()
	opts.FS = parityFS(t)
	p, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func decodeComposedJSON(t *testing.T, raw string) *ComposedRequest {
	t.Helper()
	var c ComposedRequest
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatal(err)
	}
	return &c
}

// groupedSlot is the hand-written twin of one sweepSlotBody slot.
func groupedSlot(label string, op types.AggregationType) *Request {
	return &Request{
		Label:        label,
		Cohort:       &types.Cohort{Filename: parityCohort},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Aggregations: []*types.Aggregation{{Type: op, Field: "age", Label: "v"}},
	}
}

func requireCoded(t *testing.T, err error, code perr.Code) *perr.CodedError {
	t.Helper()
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
	return ce
}

// TestComposeSweep_MatchesHandWritten: a sweep's slot responses are
// byte-identical to the same slots written by hand, on Compose and
// ComposeParallel, explicit slots first.
func TestComposeSweep_MatchesHandWritten(t *testing.T) {
	p := sweepPulse(t, Options{})
	ctx := context.Background()
	swept := decodeComposedJSON(t, `{"requests":[{"label":"base","cohort":{"filename":"`+parityCohort+`"},
		"groups":[{"type":"GROUP_CATEGORY","field":"region"}],"aggregations":[{"type":"AGG_COUNT","field":"age","label":"v"}]}],
		"sweep":{"axes":[{"name":"op","values":["AGG_SUM","AGG_MAX","AGG_AVERAGE"]}],"label":"m_{{op}}","request":`+sweepSlotBody+`}}`)
	hand := &ComposedRequest{Requests: []*Request{
		groupedSlot("base", types.AGG_COUNT),
		groupedSlot("m_AGG_SUM", types.AGG_SUM),
		groupedSlot("m_AGG_MAX", types.AGG_MAX),
		groupedSlot("m_AGG_AVERAGE", types.AGG_AVERAGE),
	}}
	runs := map[string]func(*ComposedRequest) (*ComposedResponse, error){
		"Compose": func(c *ComposedRequest) (*ComposedResponse, error) { return p.Compose(ctx, c) },
		"ComposeParallel": func(c *ComposedRequest) (*ComposedResponse, error) {
			return p.ComposeParallel(ctx, c, ComposeOptions{MaxWorkers: 3})
		},
	}
	for name, run := range runs {
		t.Run(name, func(t *testing.T) {
			got, err := run(swept)
			if err != nil {
				t.Fatalf("sweep: %v", err)
			}
			want, err := run(hand)
			if err != nil {
				t.Fatalf("hand-written: %v", err)
			}
			if len(got.Responses) != 4 {
				t.Fatalf("got %d responses, want 4", len(got.Responses))
			}
			gb, _ := json.Marshal(got)
			wb, _ := json.Marshal(want)
			if !bytes.Equal(gb, wb) {
				t.Fatalf("sweep responses differ from hand-written:\n got %s\nwant %s", gb, wb)
			}
		})
	}
}

// TestComposeSweep_SweepOnly: a sweep with no explicit requests runs.
func TestComposeSweep_SweepOnly(t *testing.T) {
	p := sweepPulse(t, Options{})
	c := decodeComposedJSON(t, `{"sweep":{"axes":[{"name":"op","values":["AGG_SUM","AGG_MAX"]}],"request":`+sweepSlotBody+`}}`)
	resp, err := p.Compose(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Responses) != 2 {
		t.Fatalf("%d responses", len(resp.Responses))
	}
	if c.Sweep == nil || len(c.Requests) != 0 {
		t.Fatal("Compose mutated the caller's request")
	}
}

// TestComposeSweep_OverlayPerSlot: `sweep.overlays` yields one
// OVERLAY_DELTA_VS_REF layer per sweep slot against an explicit
// baseline, after the explicit layers.
func TestComposeSweep_OverlayPerSlot(t *testing.T) {
	p := sweepPulse(t, Options{})
	ctx := context.Background()
	c := decodeComposedJSON(t, `{"requests":[{"label":"base","cohort":{"filename":"`+parityCohort+`"},
		"groups":[{"type":"GROUP_CATEGORY","field":"region"}],"aggregations":[{"type":"AGG_SUM","field":"age","label":"v"}]}],
		"sweep":{"axes":[{"name":"op","values":["AGG_MAX","AGG_MIN","AGG_AVERAGE"]}],"label":"m_{{op}}","request":`+sweepSlotBody+`,
		"overlays":[{"name":"delta_{{op}}","kind":"OVERLAY_DELTA_VS_REF","scope":"group","reference":"base","targets":["m_{{op}}"]}]}}`)
	for name, run := range map[string]func() (*ComposedResponse, error){
		"Compose":         func() (*ComposedResponse, error) { return p.Compose(ctx, c) },
		"ComposeParallel": func() (*ComposedResponse, error) { return p.ComposeParallel(ctx, c, ComposeOptions{}) },
	} {
		t.Run(name, func(t *testing.T) {
			resp, err := run()
			if err != nil {
				t.Fatal(err)
			}
			if len(resp.Overlays) != 3 {
				t.Fatalf("got %d overlay layers, want one per sweep slot (3): %+v", len(resp.Overlays), resp.Overlays)
			}
			for i, want := range []string{"delta_AGG_MAX", "delta_AGG_MIN", "delta_AGG_AVERAGE"} {
				l := resp.Overlays[i]
				if l.Name != want || l.Kind != "OVERLAY_DELTA_VS_REF" {
					t.Fatalf("layer %d = %s/%s, want %s", i, l.Name, l.Kind, want)
				}
				if l.Payload.Shape == "" {
					t.Fatalf("layer %s carries no payload", want)
				}
			}
			for _, r := range resp.Responses {
				if len(r.Overlays) != 0 {
					t.Fatal("a Compose overlay wrote a slot's Response.Overlays")
				}
			}
		})
	}
}

// TestComposeSweep_LimitBeforeAnySlot: MaxComposeSlots counts explicit
// + expanded slots and refuses before a single slot runs, on both entry
// points. Predict's parity on the same limit is
// TestPredictCompose_SlotLimitParity.
func TestComposeSweep_LimitBeforeAnySlot(t *testing.T) {
	var slotsRun atomic.Int64
	hooks := &observe.Hooks{OnOperationStart: func(ctx context.Context, info observe.OperationInfo) context.Context {
		if info.Scope == observe.ScopeChild {
			slotsRun.Add(1)
		}
		return nil
	}}
	p := sweepPulse(t, Options{Limits: Limits{MaxComposeSlots: 4}, Hooks: hooks})
	ctx := context.Background()
	build := func(values string) *ComposedRequest {
		return decodeComposedJSON(t, `{"requests":[`+sweepSlotBodyWith("AGG_COUNT")+`],
			"sweep":{"axes":[{"name":"op","values":`+values+`}],"request":`+sweepSlotBody+`}}`)
	}
	// At the limit (1 + 3) it runs — and the hook does count slots.
	if _, err := p.Compose(ctx, build(`["AGG_SUM","AGG_MAX","AGG_MIN"]`)); err != nil {
		t.Fatalf("at limit: %v", err)
	}
	if slotsRun.Load() != 4 {
		t.Fatalf("hook counted %d slots at the limit, want 4", slotsRun.Load())
	}
	over := `["AGG_SUM","AGG_MAX","AGG_MIN","AGG_AVERAGE"]`
	for name, run := range map[string]func() error{
		"Compose": func() error { _, err := p.Compose(ctx, build(over)); return err },
		"ComposeParallel": func() error {
			_, err := p.ComposeParallel(ctx, build(over), ComposeOptions{FailFast: false})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			slotsRun.Store(0)
			ce := requireCoded(t, run(), perr.PULSE_LIMIT_EXCEEDED)
			if ce.Details["observed"] != int64(5) || ce.Details["limit"] != "max_compose_slots" {
				t.Fatalf("details %v, want observed 5 of max_compose_slots", ce.Details)
			}
			if n := slotsRun.Load(); n != 0 {
				t.Fatalf("%d slots ran before the refusal", n)
			}
		})
	}
}

func sweepSlotBodyWith(op string) string {
	return `{"cohort":{"filename":"` + parityCohort + `"},"groups":[{"type":"GROUP_CATEGORY","field":"region"}],"aggregations":[{"type":"` + op + `","field":"age","label":"v"}]}`
}

// TestComposeSweep_InvalidRefusedEverywhere: FR-3 faults are
// PULSE_SWEEP_INVALID on Compose, ComposeParallel and PredictCompose.
func TestComposeSweep_InvalidRefusedEverywhere(t *testing.T) {
	p := sweepPulse(t, Options{})
	ctx := context.Background()
	cases := map[string]string{
		sweep.ReasonAxisUnreferenced:   `{"axes":[{"name":"op","values":["AGG_SUM"]},{"name":"x","values":[1,2]}],"request":` + sweepSlotBody + `}`,
		sweep.ReasonPlaceholderUnknown: `{"axes":[{"name":"op","values":["AGG_SUM"]}],"label":"{{nope}}","request":` + sweepSlotBody + `}`,
		sweep.ReasonRequestDecode:      `{"axes":[{"name":"op","values":["AGG_SUM"]}],"request":{"cohort":{"filename":"x"},"aggregatoins":[{"type":"{{op}}"}]}}`,
	}
	for reason, sw := range cases {
		t.Run(reason, func(t *testing.T) {
			c := decodeComposedJSON(t, `{"requests":[],"sweep":`+sw+`}`)
			check := func(err error) {
				t.Helper()
				ce := requireCoded(t, err, perr.PULSE_SWEEP_INVALID)
				if ce.Details["reason"] != reason {
					t.Fatalf("reason %v, want %s", ce.Details["reason"], reason)
				}
			}
			_, err := p.Compose(ctx, c)
			check(err)
			_, err = p.ComposeParallel(ctx, c, ComposeOptions{})
			check(err)
			env, err := p.PredictCompose(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) != 1 || env.Errors[0].Code != string(perr.PULSE_SWEEP_INVALID) || env.Errors[0].Details["reason"] != reason {
				t.Fatalf("predict errors %+v", env.Errors)
			}
			if env.Data.(*ComposePredictResult).Valid {
				t.Fatal("predict reports a refused sweep valid")
			}
		})
	}
}

// TestComposeSweep_LabelCollisionAllSites: explicit and sweep slots
// share ONE label namespace. A sweep label equal to an explicit label —
// or to an explicit slot's `request_<i+1>` default — is
// PULSE_COMPOSE_LABEL_COLLISION at every site that consumes the shared
// expansion: the runtime (Compose, ComposeParallel), predict (with and
// without overlays), the overlay resolve, and the hash normaliser's
// label default, which the expansion leaves the explicit slots.
func TestComposeSweep_LabelCollisionAllSites(t *testing.T) {
	p := sweepPulse(t, Options{})
	ctx := context.Background()
	cases := map[string]string{
		"explicit label": `{"requests":[{"label":"m_AGG_MAX","cohort":{"filename":"` + parityCohort + `"},"aggregations":[{"type":"AGG_COUNT","field":"age","label":"v"}]}],
			"sweep":{"axes":[{"name":"op","values":["AGG_SUM","AGG_MAX"]}],"label":"m_{{op}}","request":` + sweepSlotBody + `}}`,
		"explicit default": `{"requests":[` + sweepSlotBodyWith("AGG_COUNT") + `],
			"sweep":{"axes":[{"name":"n","values":[1,2]}],"label":"request_{{n}}","request":{"cohort":{"filename":"` + parityCohort + `"},"aggregations":[{"type":"AGG_SUM","field":"age","label":"v{{n}}"}]}}}`,
		"with overlays": `{"requests":[{"label":"m_AGG_MAX","cohort":{"filename":"` + parityCohort + `"},"aggregations":[{"type":"AGG_COUNT","field":"age","label":"v"}]}],
			"sweep":{"axes":[{"name":"op","values":["AGG_SUM","AGG_MAX"]}],"label":"m_{{op}}","request":` + sweepSlotBody + `,
			"overlays":[{"kind":"OVERLAY_DELTA_VS_REF","scope":"group","reference":"m_AGG_MAX","targets":["m_{{op}}"]}]}}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			c := decodeComposedJSON(t, raw)
			_, err := p.Compose(ctx, c)
			requireCoded(t, err, perr.PULSE_COMPOSE_LABEL_COLLISION)
			_, err = p.ComposeParallel(ctx, c, ComposeOptions{})
			requireCoded(t, err, perr.PULSE_COMPOSE_LABEL_COLLISION)

			env, err := p.PredictCompose(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, e := range env.Errors {
				found = found || e.Code == string(perr.PULSE_COMPOSE_LABEL_COLLISION)
			}
			if !found || env.Data.(*descx.ComposeValidationResult).Valid {
				t.Fatalf("predict missed the collision: %+v", env.Errors)
			}

			exp, err := sweep.Expand(c, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = processing.ResolveComposeSlots(exp.Composed, make([]*types.Response, len(exp.Composed.Requests)))
			requireCoded(t, err, perr.PULSE_COMPOSE_LABEL_COLLISION)

			// Hash normaliser: the explicit slot's default label is the
			// one the expansion's namespace collides on — an unlabeled
			// slot hashes as `request_1` would.
			if name == "explicit default" {
				labeled := decodeComposedJSON(t, raw)
				labeled.Requests[0].Label = "request_1"
				if labeled.Hash() != c.Hash() {
					t.Fatal("hash normaliser's label default diverges from the expansion namespace")
				}
			}
		})
	}
}

// TestComposeSweep_HiddenSlotInSweepBody: a sweep slot carrying a slot
// the instance hides is refused as on an explicit slot — runtime and
// predict alike, details.request locating the expanded slot.
func TestComposeSweep_HiddenSlotInSweepBody(t *testing.T) {
	fsys := parityFS(t)
	feats := []string{"capability:compose", "capability:compose_sweep", string(types.AGG_COUNT), string(types.GROUP_CATEGORY)}
	h := newParityHostWith(t, fsys, "sweep-no-crosstab", parityHostConfig{
		profiles: map[string][]string{"sweep-no-crosstab": feats},
	})
	c := decodeComposedJSON(t, `{"requests":[`+sweepSlotBodyWith("AGG_COUNT")+`],
		"sweep":{"axes":[{"name":"f","values":["age","region"]}],"request":{"cohort":{"filename":"`+parityCohort+`"},
		"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"region"}],"columns":[{"type":"GROUP_CATEGORY","field":"region"}],
		"cell":{"type":"AGG_COUNT","field":"{{f}}"}}}}}`)
	ctx := context.Background()
	_, err := h.p.Compose(ctx, c)
	ce := requireCoded(t, err, perr.PULSE_REQUEST_UNKNOWN_FIELD)
	if ce.Details["request"] != 1 {
		t.Fatalf("details.request = %v, want 1 (the first sweep slot)", ce.Details["request"])
	}
	_, err = h.p.ComposeParallel(ctx, c, ComposeOptions{})
	requireCoded(t, err, perr.PULSE_REQUEST_UNKNOWN_FIELD)
	env, err := h.p.PredictCompose(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PULSE_REQUEST_UNKNOWN_FIELD) || env.Errors[0].Details["request"] != 1 {
		t.Fatalf("predict errors %+v", env.Errors)
	}
}
