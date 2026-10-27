package pulse_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// U30 E4-S2: a series overlay's params.field may name an expanded
// vector element column "<label>[<element label>]" on every host —
// request (grouped Process), Compose and whole-chain. The element is an
// ordinary f64 column, so the layer equals the one scoring a scalar
// aggregation with the same per-group values (AGG_AVERAGE of the member
// — the oracle), an unanswered element (NaN) included: on every host a
// NaN host value is MISSING (entry kept, statistic absent). The bare label
// stays refused array_valued (TestProcessOverlay_ /
// TestComposeOverlay_ / TestChainOverlay_ArrayValuedOnlyWhenScored); an
// element the slot does not write is PULSE_OVERLAY_FIELD_UNKNOWN with
// identical Details on predict (run first) and the runtime.

// elemAggs is a slot's aggregations: AGG_SUM qty "s", then either the
// expanding AGG_VEC_MEAN "m" over b1, b2 (vector) or the oracle
// AGG_AVERAGE b1 "x" / AGG_AVERAGE b2 "y".
func elemAggs(vector, expand bool) []*types.Aggregation {
	s := &types.Aggregation{Type: types.AGG_SUM, Field: "qty", Label: "s"}
	if !vector {
		return []*types.Aggregation{s,
			{Type: types.AGG_AVERAGE, Field: "b1", Label: "x"},
			{Type: types.AGG_AVERAGE, Field: "b2", Label: "y"}}
	}
	m := &types.Aggregation{Type: types.AGG_VEC_MEAN, Fields: []string{"b1", "b2"}, Label: "m"}
	if expand {
		m.Params = vaggExpandParams()
	}
	return []*types.Aggregation{s, m}
}

// elemGroups: GROUP_CATEGORY qty — over e.pulse, qty 5's b1 and qty 4's
// b2 are elements no row answered (vaggElementCohort).
func elemGroups() []*types.Group {
	return []*types.Group{{Type: types.GROUP_CATEGORY, Field: "qty"}}
}

func elemJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := types.MarshalFinite(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// elemPayloads is every layer's payload (the spec echo carries the
// field name, which differs from the oracle's by design).
func elemPayloads(t *testing.T, layers []types.OverlayLayer) string {
	t.Helper()
	out := make([]types.OverlayPayload, len(layers))
	for i, l := range layers {
		out[i] = l.Payload
	}
	return elemJSON(t, out)
}

// elemMissing is the qty bucket whose element no row answered over
// e.pulse (vaggElementCohort): b1 at qty 5, b2 at qty 4. Over v.pulse
// every bucket answers both.
func elemMissing(cohort, elem string) string {
	if cohort != "e.pulse" {
		return ""
	}
	if elem == "m[b1]" {
		return "5"
	}
	return "4"
}

// elemEntries decodes one SERIES payload into (bucket, statistic)
// pairs, the bucket the key's last element with any "qty=" dropped and
// the statistic nil when null or absent.
func elemEntries(t *testing.T, p types.OverlayPayload) ([]string, []*float64) {
	t.Helper()
	var wire struct {
		Series struct {
			Entries []struct {
				Key     []string `json:"key"`
				Summary struct {
					Statistic *float64 `json:"statistic"`
				} `json:"summary"`
			} `json:"entries"`
		} `json:"series"`
	}
	if err := json.Unmarshal([]byte(elemJSON(t, p)), &wire); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, len(wire.Series.Entries))
	stats := make([]*float64, len(wire.Series.Entries))
	for i, e := range wire.Series.Entries {
		k := e.Key[len(e.Key)-1]
		if len(k) > 4 && k[:4] == "qty=" {
			k = k[4:]
		}
		keys[i], stats[i] = k, e.Summary.Statistic
	}
	return keys, stats
}

// elemCompare asserts the element layer got equals the scalar oracle's
// want (AGG_AVERAGE of the member) byte for byte on every host: the
// oracle's mean over a bucket with no value is undefined (NaN) exactly
// as the unanswered element is (E4-S5), so both read it as a MISSING
// host value. On the missing bucket the entry is present but its
// statistic key is ABSENT on the wire — never null — and with bridge
// (an ordered _PRIOR kind) the next entry compares across the gap.
func elemCompare(t *testing.T, got, want types.OverlayPayload, missing string, bridge bool) {
	t.Helper()
	if g, w := elemJSON(t, got), elemJSON(t, want); g != w {
		t.Errorf("element layer =\n%s\nwant (scalar oracle)\n%s", g, w)
	}
	if missing == "" {
		return
	}
	gk, gs := elemEntries(t, got)
	if len(gk) == 0 {
		t.Fatal("no entries")
	}
	raw := elemRawEntries(t, got)
	seen := false
	for i := range gk {
		switch {
		case gk[i] == missing:
			seen = true
			if _, ok := raw[i]["statistic"]; ok {
				t.Errorf("bucket %s (value undefined) carries a statistic key %s, want it absent (missing host value)", gk[i], raw[i]["statistic"])
			}
		case bridge && i > 0 && gk[i-1] == missing:
			if gs[i] == nil {
				t.Errorf("bucket %s does not bridge the gap: no statistic", gk[i])
			}
		}
	}
	if !seen {
		t.Errorf("no entry for the missing bucket %s", missing)
	}
}

// elemRawEntries is each SERIES entry's summary as raw JSON members, so
// an absent statistic key is told apart from a null one.
func elemRawEntries(t *testing.T, p types.OverlayPayload) []map[string]json.RawMessage {
	t.Helper()
	var wire struct {
		Series struct {
			Entries []struct {
				Summary map[string]json.RawMessage `json:"summary"`
			} `json:"entries"`
		} `json:"series"`
	}
	if err := json.Unmarshal([]byte(elemJSON(t, p)), &wire); err != nil {
		t.Fatal(err)
	}
	out := make([]map[string]json.RawMessage, len(wire.Series.Entries))
	for i, e := range wire.Series.Entries {
		out[i] = e.Summary
	}
	return out
}

func elemProcess(cohort string, vector, expand bool, kind types.OverlayKind, field string) *types.Request {
	spec := types.OverlaySpec{Kind: kind, Scope: types.OverlayScopeGroup}
	b, _ := json.Marshal(map[string]any{"field": field})
	spec.Params = b
	return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Groups: elemGroups(),
		Aggregations: elemAggs(vector, expand), Overlays: []types.OverlaySpec{spec}}
}

// TestProcessOverlay_ScoresElementColumn: the request host.
func TestProcessOverlay_ScoresElementColumn(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, data := vaggElementCohort(t)
	for _, cohort := range []string{"e.pulse", "v.pulse"} {
		for _, kind := range []types.OverlayKind{types.OverlayKindDeltaVsPrior, types.OverlayKindShareOfTotal, types.OverlayKindIndexVsTotal} {
			for _, c := range []struct{ elem, oracle string }{{"m[b1]", "x"}, {"m[b2]", "y"}} {
				t.Run(cohort+"/"+string(kind)+"/"+c.elem, func(t *testing.T) {
					if pe := predictCodedError(t, p, data[cohort], elemProcess(cohort, true, true, kind, c.elem)); pe != nil {
						t.Fatalf("Predict = %s %v, want clean", pe.Code, pe.Details)
					}
					got, err := p.Process(ctx, elemProcess(cohort, true, true, kind, c.elem))
					if err != nil {
						t.Fatalf("Process: %v", err)
					}
					want, err := p.Process(ctx, elemProcess(cohort, false, false, kind, c.oracle))
					if err != nil {
						t.Fatalf("oracle Process: %v", err)
					}
					if len(got.Overlays) != 1 || got.Overlays[0].Payload.Series == nil || len(got.Overlays[0].Payload.Series.Entries) == 0 {
						t.Fatalf("overlays = %+v, want one non-empty SERIES layer", got.Overlays)
					}
					elemCompare(t, got.Overlays[0].Payload, want.Overlays[0].Payload, elemMissing(cohort, c.elem), kind == types.OverlayKindDeltaVsPrior)
				})
			}
		}
	}
	// Two overlays scoring two elements of one slot each read their own.
	req := elemProcess("v.pulse", true, true, types.OverlayKindDeltaVsPrior, "m[b1]")
	req.Overlays = append(req.Overlays, types.OverlaySpec{Kind: types.OverlayKindDeltaVsPrior, Scope: types.OverlayScopeGroup,
		Params: json.RawMessage(`{"field": "m[b2]"}`)})
	got, err := p.Process(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	oracle := elemProcess("v.pulse", false, false, types.OverlayKindDeltaVsPrior, "x")
	oracle.Overlays = append(oracle.Overlays, types.OverlaySpec{Kind: types.OverlayKindDeltaVsPrior, Scope: types.OverlayScopeGroup,
		Params: json.RawMessage(`{"field": "y"}`)})
	want, err := p.Process(ctx, oracle)
	if err != nil {
		t.Fatal(err)
	}
	if g, w := elemPayloads(t, got.Overlays), elemPayloads(t, want.Overlays); g != w {
		t.Errorf("two-element layers =\n%s\nwant\n%s", g, w)
	}
}

// elemAvailable is the refusal's Details.available on a vector slot:
// every label, the expanded slot's element columns after its label.
var elemAvailable = []string{"s", "m", "m[b1]", "m[b2]"}

// TestProcessOverlay_ElementColumnRefusedParity: an element the slot
// does not write, or an element of an unexpanded slot, is
// PULSE_OVERLAY_FIELD_UNKNOWN on predict and Process alike, Details
// identical.
func TestProcessOverlay_ElementColumnRefusedParity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, data := vaggElementCohort(t)
	for _, c := range []struct {
		name   string
		expand bool
		field  string
		avail  []string
	}{
		{"unknown element", true, "m[b9]", elemAvailable},
		{"unexpanded slot", false, "m[b1]", []string{"s", "m"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			req := func() *types.Request {
				return elemProcess("e.pulse", true, c.expand, types.OverlayKindDeltaVsPrior, c.field)
			}
			want := map[string]any{"index": 0, "kind": string(types.OverlayKindDeltaVsPrior), "field": c.field, "available": c.avail}
			pe := predictCodedError(t, p, data["e.pulse"], req())
			if pe == nil || pe.Code != string(perr.PULSE_OVERLAY_FIELD_UNKNOWN) || elemJSON(t, pe.Details) != elemJSON(t, want) {
				t.Fatalf("Predict = %+v, want PULSE_OVERLAY_FIELD_UNKNOWN %v", pe, want)
			}
			_, err := p.Process(ctx, req())
			ce := codeOf(t, err)
			if ce.Code != perr.PULSE_OVERLAY_FIELD_UNKNOWN || elemJSON(t, ce.Details) != elemJSON(t, want) || ce.Message != pe.Message {
				t.Fatalf("Process = %s %q %v, want predict's %q %v", ce.Code, ce.Message, ce.Details, pe.Message, want)
			}
		})
	}
}

// elemComposeFixture: e.pulse and w.pulse (two cohorts, so the slots
// differ) under one instance.
func elemComposeFixture(t *testing.T) *pulse.Pulse {
	t.Helper()
	fsys := afero.NewMemMapFs()
	writeVexprCohort(t, fsys, "e.pulse", 1, 8)
	writeVexprCohort(t, fsys, "w.pulse", 0, 27)
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func elemCompose(vector, expand bool, kind types.OverlayKind, params map[string]any) *pulse.ComposedRequest {
	slot := func(label, cohort string) *types.Request {
		return &types.Request{Label: label, Cohort: &types.Cohort{Filename: cohort}, Groups: elemGroups(),
			Aggregations: elemAggs(vector, expand)}
	}
	return &pulse.ComposedRequest{Requests: []*types.Request{slot("a", "e.pulse"), slot("b", "w.pulse")},
		Overlays: []types.ComposeOverlaySpec{{Name: "o", Kind: kind, Reference: "a", Targets: []string{"b"}, Params: params}}}
}

// TestComposeOverlay_ScoresElementColumn: the Compose host, predict
// first; the unknown-element refusal is identical on both sides.
func TestComposeOverlay_ScoresElementColumn(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := elemComposeFixture(t)
	for _, kind := range []types.OverlayKind{types.OverlayKindIndexVsRef, types.OverlayKindDeltaVsRef} {
		for _, c := range []struct{ elem, oracle string }{{"m[b1]", "x"}, {"m[b2]", "y"}} {
			t.Run(string(kind)+"/"+c.elem, func(t *testing.T) {
				env, err := p.PredictCompose(ctx, elemCompose(true, true, kind, map[string]any{"field": c.elem}))
				if err != nil {
					t.Fatal(err)
				}
				if len(env.Errors) != 0 {
					t.Fatalf("PredictCompose errors = %+v, want none", env.Errors)
				}
				got, err := p.Compose(ctx, elemCompose(true, true, kind, map[string]any{"field": c.elem}))
				if err != nil {
					t.Fatalf("Compose: %v", err)
				}
				want, err := p.Compose(ctx, elemCompose(false, false, kind, map[string]any{"field": c.oracle}))
				if err != nil {
					t.Fatalf("oracle Compose: %v", err)
				}
				if len(got.Overlays) != 1 || got.Overlays[0].Payload.Series == nil || len(got.Overlays[0].Payload.Series.Entries) == 0 {
					t.Fatalf("overlays = %+v, want one non-empty SERIES layer", got.Overlays)
				}
				// The reference slot reads e.pulse: its unanswered
				// element is an undefined figure, so that pair has none.
				elemCompare(t, got.Overlays[0].Payload, want.Overlays[0].Payload, elemMissing("e.pulse", c.elem), false)
			})
		}
	}

	for _, c := range []struct {
		name   string
		expand bool
		field  string
		avail  []string
	}{
		{"unknown element", true, "m[b9]", elemAvailable},
		{"unexpanded slot", false, "m[b1]", []string{"s", "m"}},
	} {
		t.Run("refused/"+c.name, func(t *testing.T) {
			req := func() *pulse.ComposedRequest {
				return elemCompose(true, c.expand, types.OverlayKindIndexVsRef, map[string]any{"field": c.field})
			}
			want := map[string]any{"index": 0, "kind": string(types.OverlayKindIndexVsRef), "field": c.field,
				"available": c.avail, "slot": "a"}
			env, err := p.PredictCompose(ctx, req())
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PULSE_OVERLAY_FIELD_UNKNOWN) || elemJSON(t, env.Errors[0].Details) != elemJSON(t, want) {
				t.Fatalf("PredictCompose = %+v, want PULSE_OVERLAY_FIELD_UNKNOWN %v first", env.Errors, want)
			}
			_, err = p.Compose(ctx, req())
			ce := codeOf(t, err)
			if ce.Code != perr.PULSE_OVERLAY_FIELD_UNKNOWN || elemJSON(t, ce.Details) != elemJSON(t, want) || ce.Message != env.Errors[0].Message {
				t.Fatalf("Compose = %s %q %v, want predict's %q %v", ce.Code, ce.Message, ce.Details, env.Errors[0].Message, want)
			}
		})
	}

	// A two-sample vs-ref kind reads an element as a bare mean: its
	// variance / n params are required, named by the element column.
	t.Run("two-sample element needs params", func(t *testing.T) {
		req := func() *pulse.ComposedRequest {
			return elemCompose(true, true, types.OverlayKindTVsRef, map[string]any{"field": "m[b1]"})
		}
		env, err := p.PredictCompose(ctx, req())
		if err != nil {
			t.Fatal(err)
		}
		if len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PULSE_OVERLAY_PARAM_MISSING) || env.Errors[0].Details["label"] != "m[b1]" {
			t.Fatalf("PredictCompose = %+v, want PULSE_OVERLAY_PARAM_MISSING label m[b1] first", env.Errors)
		}
		_, err = p.Compose(ctx, req())
		ce := codeOf(t, err)
		if ce.Code != perr.PULSE_OVERLAY_PARAM_MISSING || elemJSON(t, ce.Details) != elemJSON(t, env.Errors[0].Details) {
			t.Fatalf("Compose = %s %v, want predict's %v", ce.Code, ce.Details, env.Errors[0].Details)
		}
	})
}

func elemChain(vector, expand bool, kind types.OverlayKind, field string) *pulse.ChainRequest {
	zero := 0
	spec := &types.ChainOverlaySpec{Name: "o", Kind: kind, Ref: types.StageRef{Index: &zero}, Params: map[string]any{"field": field}}
	return &pulse.ChainRequest{Cohort: &types.Cohort{Filename: "e.pulse"}, Stages: []*types.ChainStage{
		{Name: "profile", Request: &types.Request{Groups: elemGroups(), Aggregations: elemAggs(vector, expand)}},
	}, Overlays: []*types.ChainOverlaySpec{spec}}
}

// TestChainOverlay_ScoresElementColumn: the whole-chain host (one
// stage compared with itself).
func TestChainOverlay_ScoresElementColumn(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := elemComposeFixture(t)
	for _, kind := range []types.OverlayKind{types.OverlayKindDeltaVsStage, types.OverlayKindIndexVsStage} {
		for _, c := range []struct{ elem, oracle string }{{"m[b1]", "x"}, {"m[b2]", "y"}} {
			t.Run(string(kind)+"/"+c.elem, func(t *testing.T) {
				env, err := p.PredictChain(ctx, elemChain(true, true, kind, c.elem))
				if err != nil {
					t.Fatal(err)
				}
				if len(env.Errors) != 0 {
					t.Fatalf("PredictChain errors = %+v, want none", env.Errors)
				}
				got, err := p.ProcessChain(ctx, elemChain(true, true, kind, c.elem))
				if err != nil {
					t.Fatalf("ProcessChain: %v", err)
				}
				want, err := p.ProcessChain(ctx, elemChain(false, false, kind, c.oracle))
				if err != nil {
					t.Fatalf("oracle ProcessChain: %v", err)
				}
				if len(got.Overlays) != 1 || got.Overlays[0].Payload.Series == nil || len(got.Overlays[0].Payload.Series.Entries) == 0 {
					t.Fatalf("overlays = %+v, want one non-empty SERIES layer", got.Overlays)
				}
				elemCompare(t, got.Overlays[0].Payload, want.Overlays[0].Payload, elemMissing("e.pulse", c.elem), false)
			})
		}
	}

	for _, c := range []struct {
		name   string
		expand bool
		field  string
		avail  []string
	}{
		{"unknown element", true, "m[b9]", elemAvailable},
		{"unexpanded slot", false, "m[b1]", []string{"s", "m"}},
	} {
		t.Run("refused/"+c.name, func(t *testing.T) {
			want := map[string]any{"index": 0, "kind": string(types.OverlayKindDeltaVsStage), "field": c.field,
				"available": c.avail, "which": "target", "stage_index": 0}
			env, err := p.PredictChain(ctx, elemChain(true, c.expand, types.OverlayKindDeltaVsStage, c.field))
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PULSE_OVERLAY_FIELD_UNKNOWN) || elemJSON(t, env.Errors[0].Details) != elemJSON(t, want) {
				t.Fatalf("PredictChain = %+v, want PULSE_OVERLAY_FIELD_UNKNOWN %v first", env.Errors, want)
			}
			_, err = p.ProcessChain(ctx, elemChain(true, c.expand, types.OverlayKindDeltaVsStage, c.field))
			ce := codeOf(t, err)
			if ce.Code != perr.PULSE_OVERLAY_FIELD_UNKNOWN || elemJSON(t, ce.Details) != elemJSON(t, want) || ce.Message != env.Errors[0].Message {
				t.Fatalf("ProcessChain = %s %q %v, want predict's %q %v", ce.Code, ce.Message, ce.Details, env.Errors[0].Message, want)
			}
		})
	}
}
