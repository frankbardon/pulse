package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// The request-slot gate at the facade: a capability-gated slot the
// instance hides is refused as PULSE_REQUEST_UNKNOWN_FIELD on every
// entry point that takes the slot, whether the request is a Go struct
// or decoded from JSON, with valid_keys / suggestions free of hidden
// slots. The default instance refuses nothing.

// slotGateOutcome is the coded refusal an entry point returned, or nil.
type slotGateOutcome struct {
	code    perr.Code
	details map[string]any
}

func slotGateFromErr(err error) *slotGateOutcome {
	var ce *perr.CodedError
	if err != nil && stderrors.As(err, &ce) {
		return &slotGateOutcome{code: ce.Code, details: ce.Details}
	}
	return nil
}

func slotGateFromEnv(env *descriptor.Envelope) *slotGateOutcome {
	if env == nil || len(env.Errors) == 0 {
		return nil
	}
	return &slotGateOutcome{code: perr.Code(env.Errors[0].Code), details: env.Errors[0].Details}
}

// requestSlotAppliers set each gated Request slot to a well-formed value.
var requestSlotAppliers = map[string]func(r *types.Request){
	"crosstab": func(r *types.Request) {
		r.Crosstab = &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "age", Label: "n"},
		}
	},
	"joins": func(r *types.Request) {
		r.Joins = []*types.JoinSpec{{Right: parityCohort, On: []types.OnPair{{LeftField: "region", RightField: "region"}}}}
	},
	"overlays": func(r *types.Request) {
		r.Overlays = []types.OverlaySpec{{Kind: types.OverlayKindShareOfRow}}
	},
	"weight": func(r *types.Request) {
		r.Weight = &types.WeightSpec{Field: "age"}
	},
	"multiplicity": func(r *types.Request) {
		r.Multiplicity = &types.Multiplicity{Method: types.MultiplicityMethodHolm}
	},
	"vectors": func(r *types.Request) {
		r.Vectors = []types.VectorSpec{{Name: "v", Fields: []string{"age"}}}
	},
	"matrices": func(r *types.Request) {
		r.Matrices = []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Fields: []string{"age"}}}
	},
}

// requestSlotEntryPoints run one Request through every entry point
// that carries a Request.
var requestSlotEntryPoints = map[string]func(h *parityHost, req *types.Request) *slotGateOutcome{
	"Process": func(h *parityHost, req *types.Request) *slotGateOutcome {
		_, err := h.p.Process(context.Background(), req)
		return slotGateFromErr(err)
	},
	"ProcessStream": func(h *parityHost, req *types.Request) *slotGateOutcome {
		_, err := h.p.ProcessStream(context.Background(), req)
		return slotGateFromErr(err)
	},
	"Compose": func(h *parityHost, req *types.Request) *slotGateOutcome {
		_, err := h.p.Compose(context.Background(), &ComposedRequest{Requests: []*Request{withCohort(h), req}})
		return slotGateFromErr(err)
	},
	"ComposeParallel": func(h *parityHost, req *types.Request) *slotGateOutcome {
		_, err := h.p.ComposeParallel(context.Background(), &ComposedRequest{Requests: []*Request{withCohort(h), req}}, ComposeOptions{MaxWorkers: 2})
		return slotGateFromErr(err)
	},
	"ProcessChain/stage0": func(h *parityHost, req *types.Request) *slotGateOutcome {
		_, err := h.p.ProcessChain(context.Background(), &ChainRequest{
			Cohort: &types.Cohort{Filename: h.cohort},
			Stages: []*types.ChainStage{{Name: "probe", Request: req}},
		})
		return slotGateFromErr(err)
	},
	"ProcessChain/stage1": func(h *parityHost, req *types.Request) *slotGateOutcome {
		stage1 := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n", Label: "total"}}}
		stage1.Crosstab, stage1.Joins, stage1.Overlays, stage1.Weight = req.Crosstab, req.Joins, req.Overlays, req.Weight
		stage1.Multiplicity, stage1.Vectors, stage1.Matrices = req.Multiplicity, req.Vectors, req.Matrices
		_, err := h.p.ProcessChain(context.Background(), &ChainRequest{
			Cohort: &types.Cohort{Filename: h.cohort},
			Stages: []*types.ChainStage{{Name: "base", Request: h.base()}, {Name: "probe", Request: stage1}},
		})
		return slotGateFromErr(err)
	},
	"Predict": func(h *parityHost, req *types.Request) *slotGateOutcome {
		res, err := h.p.Predict(context.Background(), req)
		if err != nil {
			return slotGateFromErr(err)
		}
		if res.Valid {
			return nil
		}
		// Predict returns the result only; PredictBytes carries the
		// envelope — the gate's code must be reachable from both.
		data := readCohortBytes(h)
		env, _ := h.p.PredictBytes(context.Background(), data, req)
		return slotGateFromEnv(env)
	},
	"PredictBytes": func(h *parityHost, req *types.Request) *slotGateOutcome {
		env, err := h.p.PredictBytes(context.Background(), readCohortBytes(h), req)
		if err != nil {
			return slotGateFromErr(err)
		}
		return slotGateFromEnv(env)
	},
}

func readCohortBytes(h *parityHost) []byte {
	data, err := afero.ReadFile(h.p.fsys, h.cohort)
	if err != nil {
		panic(err)
	}
	return data
}

// assertSlotRefusal checks out is the unknown-field refusal for slot,
// free of every slot the instance hides.
func assertSlotRefusal(t *testing.T, out *slotGateOutcome, slot string, hidden []string) {
	t.Helper()
	if out == nil || out.code != perr.PULSE_REQUEST_UNKNOWN_FIELD {
		t.Fatalf("want PULSE_REQUEST_UNKNOWN_FIELD for hidden %q, got %+v", slot, out)
	}
	if got := toStrings(out.details["unknown_keys"]); !slices.Equal(got, []string{slot}) {
		t.Errorf("unknown_keys = %v, want [%s]", got, slot)
	}
	valid := toStrings(out.details["valid_keys"])
	if len(valid) == 0 {
		t.Errorf("valid_keys empty")
	}
	for _, h := range hidden {
		if slices.Contains(valid, h) {
			t.Errorf("valid_keys carries hidden slot %q", h)
		}
	}
	sugg, _ := out.details["suggestions"].(map[string]any)
	for k, v := range sugg {
		if s, _ := v.(string); slices.Contains(hidden, s) {
			t.Errorf("suggestion %s → hidden slot %s", k, s)
		}
	}
}

// toStrings reads a details list whether it is still a []string (Go
// error) or came back through JSON ([]any).
func toStrings(v any) []string {
	switch l := v.(type) {
	case []string:
		return l
	case []any:
		out := make([]string, 0, len(l))
		for _, x := range l {
			s, _ := x.(string)
			out = append(out, s)
		}
		return out
	}
	return nil
}

// TestHiddenRequestSlot_RefusedAsUnknownField: per fixture, every slot
// the fixture hides, every entry point, Go struct and JSON-decoded.
func TestHiddenRequestSlot_RefusedAsUnknownField(t *testing.T) {
	fsys := parityFS(t)
	control := newParityHost(t, fsys, "")
	ran := 0
	for _, fixture := range []string{"minimal", "survey-crosstab"} {
		h := newParityHost(t, fsys, fixture)
		hidden := descx.HiddenSlotKeys(&types.Request{}, h.p.svc.InstanceSnapshot())
		if len(hidden) == 0 {
			t.Fatalf("vacuous: %s hides no Request slot", fixture)
		}
		control.base = h.base
		for _, slot := range hidden {
			for name, run := range requestSlotEntryPoints {
				build := func() *types.Request {
					req := h.base()
					req.Cohort = &types.Cohort{Filename: h.cohort}
					requestSlotAppliers[slot](req)
					return req
				}
				t.Run(fixture+"/"+slot+"/"+name, func(t *testing.T) {
					ran++
					if open := run(control, build()); open != nil && open.code == perr.PULSE_REQUEST_UNKNOWN_FIELD {
						t.Fatalf("default instance refuses %s: %+v", slot, open)
					}
					assertSlotRefusal(t, run(h, build()), slot, hidden)

					// The same request arriving as JSON.
					raw, err := json.Marshal(build())
					if err != nil {
						t.Fatal(err)
					}
					var decoded types.Request
					if err := json.Unmarshal(raw, &decoded); err != nil {
						t.Fatal(err)
					}
					assertSlotRefusal(t, run(h, &decoded), slot, hidden)
				})
			}
		}
	}
	if ran == 0 {
		t.Fatal("no cell ran")
	}
}

// TestHiddenRootSlot_RefusedAsUnknownField: the root-level overlay
// slots of ComposedRequest, ChainRequest and FacetRequest.
func TestHiddenRootSlot_RefusedAsUnknownField(t *testing.T) {
	fsys := parityFS(t)
	control := newParityHost(t, fsys, "")
	h := newParityHost(t, fsys, "survey-crosstab")
	ctx := context.Background()
	cases := []struct {
		name string
		root any
		run  func(h *parityHost) *slotGateOutcome
	}{
		{"ComposedRequest.Overlays/Compose", &types.ComposedRequest{}, func(h *parityHost) *slotGateOutcome {
			_, err := h.p.Compose(ctx, &ComposedRequest{
				Requests: []*Request{withCohort(h), withCohort(h)},
				Overlays: []types.ComposeOverlaySpec{{Kind: types.OverlayKindRank}},
			})
			return slotGateFromErr(err)
		}},
		{"ComposedRequest.Overlays/ComposeParallel", &types.ComposedRequest{}, func(h *parityHost) *slotGateOutcome {
			_, err := h.p.ComposeParallel(ctx, &ComposedRequest{
				Requests: []*Request{withCohort(h), withCohort(h)},
				Overlays: []types.ComposeOverlaySpec{{Kind: types.OverlayKindRank}},
			}, ComposeOptions{MaxWorkers: 2})
			return slotGateFromErr(err)
		}},
		{"ChainRequest.Overlays", &types.ChainRequest{}, func(h *parityHost) *slotGateOutcome {
			_, err := h.p.ProcessChain(ctx, &ChainRequest{
				Cohort:   &types.Cohort{Filename: h.cohort},
				Stages:   []*types.ChainStage{{Name: "a", Request: h.base()}, {Name: "b", Request: h.base()}},
				Overlays: []*types.ChainOverlaySpec{{Kind: types.OverlayKindIndexVsStage, Ref: types.StageRef{Name: "a"}}},
			})
			return slotGateFromErr(err)
		}},
		{"FacetRequest.Overlays", &types.FacetRequest{}, func(h *parityHost) *slotGateOutcome {
			_, err := h.p.FacetSchema(ctx, &types.FacetRequest{
				Cohort:   &types.Cohort{Filename: h.cohort},
				Fields:   []string{"region"},
				Overlays: []types.OverlaySpec{{Kind: types.OverlayKindIndexVsPop}},
			})
			return slotGateFromErr(err)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hidden := descx.HiddenSlotKeys(c.root, h.p.svc.InstanceSnapshot())
			if !slices.Contains(hidden, "overlays") {
				t.Fatalf("vacuous: survey-crosstab does not hide %s", c.name)
			}
			control.base = h.base
			if open := c.run(control); open != nil && open.code == perr.PULSE_REQUEST_UNKNOWN_FIELD {
				t.Fatalf("default instance refuses: %+v", open)
			}
			assertSlotRefusal(t, c.run(h), "overlays", hidden)
		})
	}
}

func withCohort(h *parityHost) *types.Request {
	r := h.base()
	r.Cohort = &types.Cohort{Filename: h.cohort}
	return r
}

// TestHiddenRequestSlot_RefusedBeforeJoinCount: the gate runs before the
// join-count rule and the crosstab / joins dispatch — a request the
// default instance refuses as PULSE_JOIN_TOO_MANY is an unknown field
// on an instance hiding its slots.
func TestHiddenRequestSlot_RefusedBeforeJoinCount(t *testing.T) {
	fsys := parityFS(t)
	control := newParityHost(t, fsys, "")
	h := newParityHost(t, fsys, "minimal")
	control.base = h.base
	build := func() *types.Request {
		req := withCohort(h)
		requestSlotAppliers["crosstab"](req)
		req.Joins = []*types.JoinSpec{
			{Right: parityCohort, On: []types.OnPair{{LeftField: "region", RightField: "region"}}},
			{Right: parityCohort, On: []types.OnPair{{LeftField: "region", RightField: "region"}}},
		}
		return req
	}
	for name, run := range requestSlotEntryPoints {
		t.Run(name, func(t *testing.T) {
			// A later chain stage may not join at all; that refusal
			// stands in for the join-count rule there.
			want := perr.PULSE_JOIN_TOO_MANY
			if name == "ProcessChain/stage1" {
				want = perr.PULSE_CHAIN_STAGE_JOIN
			}
			open := run(control, build())
			// ComposeParallel reports a failed slot under
			// SERVICE_INTERNAL, naming the slot's own error.
			if open != nil && open.code == perr.SERVICE_INTERNAL {
				if fe, _ := open.details["first_error"].(string); strings.HasPrefix(fe, string(want)+":") {
					open.code = want
				}
			}
			if open == nil || open.code != want {
				t.Fatalf("vacuous: default instance outcome %+v, want %s", open, want)
			}
			got := run(h, build())
			if got == nil || got.code != perr.PULSE_REQUEST_UNKNOWN_FIELD {
				t.Fatalf("got %+v, want PULSE_REQUEST_UNKNOWN_FIELD first", got)
			}
			if keys := toStrings(got.details["unknown_keys"]); !slices.Equal(keys, []string{"crosstab", "joins"}) {
				t.Errorf("unknown_keys = %v", keys)
			}
		})
	}
}
