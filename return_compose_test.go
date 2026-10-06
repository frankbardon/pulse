package pulse_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// composeReturnFixture is a two-slot Compose with one Compose-host
// overlay (a per-cell t comparison of slot "b" against slot "a").
func composeReturnFixture(cohort string) *types.ComposedRequest {
	slot := func(label string) *types.Request {
		return &types.Request{
			Label:  label,
			Cohort: &types.Cohort{Filename: cohort},
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "subj"}},
				Cell:    &types.Aggregation{Type: types.AGG_AVERAGE, Field: "x", Label: "m"},
				Shape:   types.CrosstabShapeMatrix,
			},
		}
	}
	return &types.ComposedRequest{
		Requests: []*types.Request{slot("a"), slot("b")},
		Overlays: []types.ComposeOverlaySpec{{Name: "t", Kind: types.OverlayKindTCell, Scope: types.OverlayScopeCell, Reference: "a", Targets: []string{"b"}}},
	}
}

func composeJSON(t *testing.T, p *pulse.Pulse, req *types.ComposedRequest, parallel bool) (*types.ComposedResponse, []byte) {
	t.Helper()
	var (
		out *types.ComposedResponse
		err error
	)
	if parallel {
		out, err = p.ComposeParallel(context.Background(), req, pulse.ComposeOptions{MaxWorkers: 2})
	} else {
		out, err = p.Compose(context.Background(), req)
	}
	if err != nil {
		t.Fatalf("compose (parallel=%v): %v", parallel, err)
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	return out, b
}

func chainReturnFixture(cohort string) *types.ChainRequest {
	return &types.ChainRequest{
		Cohort: &types.Cohort{Filename: cohort},
		Stages: []*types.ChainStage{
			{Name: "by_cell", Request: &types.Request{
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}, {Type: types.GROUP_CATEGORY, Field: "subj"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}},
			}},
			{Name: "by_g", Request: &types.Request{
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "s", Label: "total"}},
			}},
		},
	}
}

func chainJSON(t *testing.T, p *pulse.Pulse, req *types.ChainRequest) (*types.ChainResponse, []byte) {
	t.Helper()
	out, err := p.ProcessChain(context.Background(), req)
	if err != nil {
		t.Fatalf("chain: %v", err)
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	return out, b
}

// TestReturnFullIsIdentity_ComposeAndChain extends the identity gate to
// the multi-response roots: no block, an empty block and preset `full`
// on every slot / stage and on ComposedRequest.Return produce
// byte-identical responses with no `returned` marker anywhere; a
// non-identity block changes the bytes (the gate is not vacuous).
func TestReturnFullIsIdentity_ComposeAndChain(t *testing.T) {
	p, _, cohort := acceptanceCohort(t)
	variants := map[string]func() *types.Return{
		"preset full": func() *types.Return { return &types.Return{Preset: types.ReturnPresetFull} },
		"empty block": func() *types.Return { return &types.Return{} },
	}
	for _, parallel := range []bool{false, true} {
		_, baseline := composeJSON(t, p, composeReturnFixture(cohort), parallel)
		if bytes.Contains(baseline, []byte(`"returned"`)) || !bytes.Contains(baseline, []byte(`"overlays"`)) {
			t.Fatalf("compose baseline: %s", baseline)
		}
		for name, ret := range variants {
			req := composeReturnFixture(cohort)
			req.Return = ret()
			for _, s := range req.Requests {
				s.Return = ret()
			}
			if _, got := composeJSON(t, p, req, parallel); !bytes.Equal(got, baseline) {
				t.Errorf("compose %s (parallel=%v) differs:\n got %s\nwant %s", name, parallel, got, baseline)
			}
		}
		req := composeReturnFixture(cohort)
		req.Return = &types.Return{Exclude: []string{"overlays[*].summary"}}
		if _, got := composeJSON(t, p, req, parallel); bytes.Equal(got, baseline) || !bytes.Contains(got, []byte(`"returned"`)) {
			t.Errorf("a compose-level exclude left the response unshaped: %s", got)
		}
	}

	_, baseline := chainJSON(t, p, chainReturnFixture(cohort))
	if bytes.Contains(baseline, []byte(`"returned"`)) {
		t.Fatalf("chain baseline carries a marker: %s", baseline)
	}
	for name, ret := range variants {
		req := chainReturnFixture(cohort)
		for _, st := range req.Stages {
			st.Request.Return = ret()
		}
		if _, got := chainJSON(t, p, req); !bytes.Equal(got, baseline) {
			t.Errorf("chain %s differs:\n got %s\nwant %s", name, got, baseline)
		}
	}
	req := chainReturnFixture(cohort)
	req.Stages[1].Request.Return = &types.Return{Exclude: []string{"metadata"}}
	if _, got := chainJSON(t, p, req); bytes.Equal(got, baseline) || !bytes.Contains(got, []byte(`"returned"`)) {
		t.Errorf("a stage exclude left the chain unshaped: %s", got)
	}
}

// TestReturn_ComposeShapesPerSlotAndOverlays: each slot is shaped by its
// own block (an unblocked slot falls back to the instance default), the
// Compose-level block shapes only the top-level overlays and stamps
// ComposedResponse.Returned, the overlay figures equal the unshaped
// run's (the fold read unshaped slots) and ComposeParallel answers
// byte-identically to Compose.
func TestReturn_ComposeShapesPerSlotAndOverlays(t *testing.T) {
	_, fs, cohort := acceptanceCohort(t)
	p := newReturnInstance(t, fs, pulse.Options{})
	pd := newReturnInstance(t, fs, pulse.Options{DefaultReturn: &types.Return{Exclude: []string{"components"}}})

	base, _ := composeJSON(t, p, composeReturnFixture(cohort), false)
	if len(base.Overlays) != 1 || base.Overlays[0].Summary == nil {
		t.Fatalf("baseline overlay layer: %+v", base.Overlays)
	}
	baseLayer, _ := json.Marshal(base.Overlays[0])

	shapedReq := func() *types.ComposedRequest {
		req := composeReturnFixture(cohort)
		// Slot a keeps only its rows; slot b has no block.
		req.Requests[0].Return = &types.Return{Include: []string{"data"}}
		req.Return = &types.Return{Preset: types.ReturnPresetMinimal}
		return req
	}
	var serial []byte
	for _, parallel := range []bool{false, true} {
		out, b := composeJSON(t, pd, shapedReq(), parallel)
		if parallel && !bytes.Equal(b, serial) {
			t.Errorf("ComposeParallel differs from Compose:\n par %s\n ser %s", b, serial)
		}
		serial = b
		top := decodeTop(t, b)
		if keys := topKeys(t, b); !subsetOf(keys, "responses", "overlays", "returned") || top["returned"] == nil {
			t.Fatalf("top keys %v", keys)
		}
		if out.Returned == nil || out.Returned.Preset != string(types.ReturnPresetMinimal) {
			t.Errorf("ComposedResponse.Returned = %+v", out.Returned)
		}
		slots := decodeArr(t, top["responses"])
		if keys := objKeys(t, rawJSON(t, slots[0])); !subsetOf(keys, "data", "warnings", "returned") {
			t.Errorf("slot 0 keys %v, want data/warnings/returned only", keys)
		}
		// Slot b: no request block → the instance default (no components).
		if _, ok := slots[1]["components"]; ok {
			t.Errorf("slot 1 kept components under the instance default")
		}
		if _, ok := slots[1]["metadata"]; !ok {
			t.Errorf("slot 1 lost metadata: %v", objKeys(t, rawJSON(t, slots[1])))
		}
		if out.Responses[1].Returned == nil {
			t.Errorf("slot 1 not marked by the instance default")
		}
		layers := decodeArr(t, top["overlays"])
		if keys := objKeys(t, rawJSON(t, layers[0])); !subsetOf(keys, "name", "kind", "ref", "summary") {
			t.Errorf("minimal overlay keys %v", keys)
		}
		// The overlay's figures are the unshaped run's.
		gotSummary, _ := json.Marshal(out.Overlays[0].Summary)
		wantSummary, _ := json.Marshal(base.Overlays[0].Summary)
		if !bytes.Equal(gotSummary, wantSummary) {
			t.Errorf("overlay summary moved:\n got %s\nwant %s (layer %s)", gotSummary, wantSummary, baseLayer)
		}
	}

	// A Compose-level precision rounds the overlays only: every slot's
	// bytes equal the unshaped run's.
	req := composeReturnFixture(cohort)
	req.Return = &types.Return{Precision: 2}
	_, b := composeJSON(t, p, req, false)
	_, bb := composeJSON(t, p, composeReturnFixture(cohort), false)
	if got, want := decodeTop(t, b)["responses"], decodeTop(t, bb)["responses"]; !bytes.Equal(got, want) {
		t.Errorf("compose precision reached the slots:\n got %s\nwant %s", got, want)
	}
}

func rawJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestReturn_ChainShapesAfterWholeChain: a stage that excludes `data`
// still feeds the next stage its rows (shaping runs after the whole
// chain), and `final` follows the last stage's block.
func TestReturn_ChainShapesAfterWholeChain(t *testing.T) {
	p, _, cohort := acceptanceCohort(t)
	base, _ := chainJSON(t, p, chainReturnFixture(cohort))

	req := chainReturnFixture(cohort)
	req.Stages[0].Request.Return = &types.Return{Exclude: []string{"data"}}
	req.Stages[1].Request.Return = &types.Return{Exclude: []string{"metadata"}}
	out, b := chainJSON(t, p, req)

	if len(out.Stages[0].Data) != 0 || out.Stages[0].Returned == nil {
		t.Errorf("stage 0 kept data or lost its marker: %+v", out.Stages[0].Returned)
	}
	got, _ := json.Marshal(out.Stages[1].Data)
	want, _ := json.Marshal(base.Stages[1].Data)
	if len(base.Stages[1].Data) == 0 || !bytes.Equal(got, want) {
		t.Errorf("stage 1 rows moved:\n got %s\nwant %s", got, want)
	}
	top := decodeTop(t, b)
	final := decodeTop(t, top["final"])
	if _, ok := final["metadata"]; ok {
		t.Errorf("final kept metadata the last stage excluded")
	}
	if final["returned"] == nil || final["data"] == nil {
		t.Errorf("final keys %v", objKeys(t, top["final"]))
	}
	if s0 := decodeTop(t, decodeArr2(t, top["stages"])[0]); s0["data"] != nil {
		t.Errorf("stage 0 wire carries data")
	}
}

func decodeArr2(t *testing.T, b json.RawMessage) []json.RawMessage {
	t.Helper()
	var out []json.RawMessage
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestReturn_ComposeAndChainRefusalsMatchPredict: a bad path in a slot,
// a stage or the Compose-level block is refused by the runtime with the
// slot / stage index in details, exactly as ValidateCompose /
// ValidateChain report first.
func TestReturn_ComposeAndChainRefusalsMatchPredict(t *testing.T) {
	_, fs, cohort := acceptanceCohort(t)
	p := newReturnInstance(t, fs, pulse.Options{})
	ctx := context.Background()
	opts := &descx.PredictOptions{SchemaLoader: schemaLoaderFor(fs)}
	data, err := afero.ReadFile(fs, cohort)
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name    string
		code    errors.Code
		mutate  func(*types.ComposedRequest)
		request any
	}{
		{"slot path unknown", errors.PULSE_RETURN_PATH_UNKNOWN, func(r *types.ComposedRequest) {
			r.Requests[1].Return = &types.Return{Include: []string{"nope"}}
		}, 1},
		{"slot data column unknown", errors.PULSE_RETURN_PATH_UNKNOWN, func(r *types.ComposedRequest) {
			// A grouped slot: its column set is closed (a crosstab's is open).
			r.Overlays = nil
			r.Requests[1].Crosstab = nil
			r.Requests[1].Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}
			r.Requests[1].Aggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}}
			r.Requests[1].Return = &types.Return{Include: []string{"data[*].zz"}}
		}, 1},
		{"compose-level responses path", errors.PULSE_RETURN_INVALID, func(r *types.ComposedRequest) {
			r.Return = &types.Return{Exclude: []string{"responses[*].data"}}
		}, nil},
		{"compose-level overlay path unknown", errors.PULSE_RETURN_PATH_UNKNOWN, func(r *types.ComposedRequest) {
			r.Return = &types.Return{Include: []string{"overlays[*].nope"}}
		}, nil},
	} {
		mk := func() *types.ComposedRequest {
			r := composeReturnFixture(cohort)
			c.mutate(r)
			return r
		}
		for _, parallel := range []bool{false, true} {
			var rerr error
			if parallel {
				_, rerr = p.ComposeParallel(ctx, mk(), pulse.ComposeOptions{MaxWorkers: 2, FailFast: true})
			} else {
				_, rerr = p.Compose(ctx, mk())
			}
			ce := requireCode(t, rerr, c.code)
			if got := ce.Details["request"]; got != c.request {
				t.Errorf("%s (parallel=%v): details.request = %v, want %v (%v)", c.name, parallel, got, c.request, ce.Details)
			}
			sameEntry(t, descx.ValidateComposeWithOptions(mk(), opts), rerr)
		}
	}

	for _, c := range []struct {
		name  string
		stage int
		ret   *types.Return
	}{
		{"stage 1 path unknown", 1, &types.Return{Exclude: []string{"nope"}}},
		{"stage 1 data column unknown", 1, &types.Return{Include: []string{"data[*].x"}}},
		{"stage 0 data column unknown", 0, &types.Return{Include: []string{"data[*].zz"}}},
	} {
		mk := func() *types.ChainRequest {
			r := chainReturnFixture(cohort)
			r.Stages[c.stage].Request.Return = c.ret
			return r
		}
		_, rerr := p.ProcessChain(ctx, mk())
		ce := requireCode(t, rerr, errors.PULSE_RETURN_PATH_UNKNOWN)
		if ce.Details["stage"] != c.stage {
			t.Errorf("%s: details = %v, want stage=%d", c.name, ce.Details, c.stage)
		}
		sameEntry(t, descx.ValidateChainWithOptions(bytes.NewReader(data), mk(), opts), rerr)
	}
}
