package pulse_test

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// OVERLAY_MATRIX_GROUP_SUMMARY on the MATRIX_RESULT host (U28 E4-S1):
// predict and runtime refuse identically (code, message and details) —
// the shared overlayhost.MatrixResultRefusal, the shape refusal both
// ways (the kind over a crosstab, a crosstab kind over a matrices-only
// request) and the chain-stage refusal; it runs end to end on a process
// request and a Compose slot, survives a `return` that drops the
// matrices, reads an upper encoding as its full twin, and leaves the
// matrix results byte-identical to the overlay-free run.

// mgsRequest is the acceptance cohort's per-g correlation of x, y and
// one integer column, named "r", carrying overlays.
func mgsRequest(cohort string, enc types.MatrixEncoding, overlays ...types.OverlaySpec) *types.Request {
	return &types.Request{
		Cohort:       &types.Cohort{Filename: cohort},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
		Matrices:     []types.MatrixSpec{{Name: "r", Type: types.MAT_CORRELATION, Fields: []string{"x", "y", "t_u16"}, Encoding: enc}},
		Overlays:     overlays,
	}
}

func mgsSpec(name string) types.OverlaySpec {
	return types.OverlaySpec{Kind: types.OverlayKindMatrixGroupSummary, Scope: types.OverlayScopeMatrix,
		Ref: types.OverlayRef{Matrix: &types.OverlayMatrixRef{Name: name}}}
}

func TestMatrixGroupSummary_RefusalsMatchPredict(t *testing.T) {
	t.Parallel()

	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	edit := func(f func(*types.Request)) *types.Request {
		r := mgsRequest(cohort, "", mgsSpec("r"))
		f(r)
		return r
	}
	for _, c := range []struct {
		name string
		req  *types.Request
		want errors.Code
	}{
		{"unknown name", edit(func(r *types.Request) { r.Overlays[0].Ref.Matrix.Name = "nope" }), errors.PULSE_OVERLAY_REFERENCE_UNKNOWN},
		{"ungrouped", edit(func(r *types.Request) { r.Groups = nil }), errors.PULSE_OVERLAY_GROUPS_REQUIRED},
		{"no ref", edit(func(r *types.Request) { r.Overlays[0].Ref = types.OverlayRef{} }), errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"two arms", edit(func(r *types.Request) {
			r.Overlays[0].Ref.Margin = &types.OverlayMarginRef{Axis: types.MarginAxisRow}
		}), errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"cell scope", edit(func(r *types.Request) { r.Overlays[0].Scope = types.OverlayScopeCell }), errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED},
		{"within", edit(func(r *types.Request) { r.Overlays[0].Within = 1 }), errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE},
		{"params", edit(func(r *types.Request) { r.Overlays[0].Params = json.RawMessage(`{"weighted": true}`) }), errors.PULSE_REQUEST_UNKNOWN_FIELD},
		// The kind over a crosstab host (Crosstab + Matrices is refused,
		// so a crosstab request carries no matrix to name).
		{"crosstab host", &types.Request{Cohort: &types.Cohort{Filename: cohort},
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "subj"}},
				Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "x", Label: "n"},
			},
			Overlays: []types.OverlaySpec{mgsSpec("r")}}, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		// A crosstab kind over a matrices-only request.
		{"crosstab kind on matrix results", edit(func(r *types.Request) {
			r.Aggregations = nil
			r.Overlays = append(r.Overlays, types.OverlaySpec{Kind: types.OverlayKindShareOfCol, Scope: types.OverlayScopeCell,
				Ref: types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisColumn}}})
		}), errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
	} {
		t.Run(c.name, func(t *testing.T) {
			env := predictEnvelope(t, p, fs, cohort, c.req)
			_, err := p.Process(ctx, c.req)
			requireCode(t, err, c.want)
			sameEntry(t, env, err)
		})
	}

	t.Run("chain stage", func(t *testing.T) {
		chain := func() *types.ChainRequest {
			s0 := mgsRequest(cohort, "", mgsSpec("r"))
			s0.Cohort = nil
			return &types.ChainRequest{Cohort: &types.Cohort{Filename: cohort}, Stages: []*types.ChainStage{
				{Name: "s0", Request: s0},
				{Name: "s1", Request: &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n", Label: "all"}}}},
			}}
		}
		env, err := p.PredictChain(ctx, chain())
		pe := whFirstError(t, env, err)
		_, rerr := p.ProcessChain(ctx, chain())
		ce := requireCode(t, rerr, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE)
		if ce.Details["stage"] != 0 || ce.Details["host"] != "chain" {
			t.Fatalf("details %v", ce.Details)
		}
		if pe == nil || pe.Code != string(ce.Code) || pe.Message != ce.Message || nsJSON(t, pe.Details) != nsJSON(t, ce.Details) {
			t.Fatalf("predict %+v, runtime %s %q %v", pe, ce.Code, ce.Message, ce.Details)
		}
	})
}

func TestMatrixGroupSummary_EndToEnd(t *testing.T) {
	t.Parallel()

	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	req := mgsRequest(cohort, "", mgsSpec("r"))
	if env := predictEnvelope(t, p, fs, cohort, req); len(env.Errors) != 0 {
		t.Fatalf("predict %+v", env.Errors)
	}
	resp, err := p.Process(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Matrices) != 2 || len(resp.Overlays) != 1 {
		t.Fatalf("%d matrices, %d overlays", len(resp.Matrices), len(resp.Overlays))
	}
	layer := resp.Overlays[0]
	if layer.Kind != types.OverlayKindMatrixGroupSummary || layer.Payload.Matrix == nil || len(layer.Payload.Parts) != 4 {
		t.Fatalf("layer %+v", layer)
	}
	// Every cell is the plain mean of the two buckets' cells.
	mean := layer.Payload.Matrix
	for i, row := range mean.Cells {
		for j, cell := range row {
			a, b := resp.Matrices[0].Primary.Values[i][j], resp.Matrices[1].Primary.Values[i][j]
			if got := cell.Value.(float64); got != (a+b)/2 {
				t.Errorf("mean [%d][%d] = %v, want (%v + %v) / 2", i, j, got, a, b)
			}
			if n := layer.Payload.Parts["n_groups"].Matrix.Cells[i][j].Value.(float64); n != 2 {
				t.Errorf("n_groups [%d][%d] = %v", i, j, n)
			}
			if r := layer.Payload.Parts["range"].Matrix.Cells[i][j].Value.(float64); r != math.Abs(a-b) {
				t.Errorf("range [%d][%d] = %v, want %v", i, j, r, math.Abs(a-b))
			}
		}
	}
	got, _ := json.Marshal(layer)

	// The matrix results and rows are byte-identical to the overlay-free
	// run, which carries no overlays at all.
	bare, err := p.Process(ctx, mgsRequest(cohort, ""))
	if err != nil {
		t.Fatal(err)
	}
	if bare.Overlays != nil || nsJSON(t, bare.Matrices) != nsJSON(t, resp.Matrices) || nsJSON(t, bare.Data) != nsJSON(t, resp.Data) {
		t.Fatal("the overlay moved the host results")
	}

	// An upper encoding is expanded first: the same layer.
	upper, err := p.Process(ctx, mgsRequest(cohort, types.MatrixEncodingUpper, mgsSpec("r")))
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(upper.Overlays[0]); string(b) != string(got) {
		t.Errorf("upper encoding layer differs:\n%s\n%s", b, got)
	}

	// A `return` that drops the matrices still computes them for the
	// overlay.
	shaped := mgsRequest(cohort, "", mgsSpec("r"))
	shaped.Return = &types.Return{Exclude: []string{"matrices"}}
	sr, err := p.Process(ctx, shaped)
	if err != nil {
		t.Fatal(err)
	}
	if sr.Matrices != nil || len(sr.Overlays) != 1 {
		t.Fatalf("shaped: matrices %v, overlays %d", sr.Matrices != nil, len(sr.Overlays))
	}
	if b, _ := json.Marshal(sr.Overlays[0]); string(b) != string(got) {
		t.Errorf("shaped layer differs:\n%s\n%s", b, got)
	}

	// A Compose slot's own overlay: the same layer.
	composed := &types.ComposedRequest{Requests: []*types.Request{mgsRequest(cohort, "", mgsSpec("r"))}}
	if env, err := p.PredictCompose(ctx, composed); whFirstError(t, env, err) != nil {
		t.Fatalf("predict compose: %+v", env.Errors)
	}
	out, err := p.Compose(ctx, &types.ComposedRequest{Requests: []*types.Request{mgsRequest(cohort, "", mgsSpec("r"))}})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(out.Responses[0].Overlays[0]); string(b) != string(got) {
		t.Errorf("Compose slot layer differs:\n%s\n%s", b, got)
	}
}

// TestMatrixGroupSummary_MatricesOnlyInstance: on an instance whose only
// overlay host is MATRIX_RESULT (capability:matrices + the kind, no
// crosstab, no compose), the request `overlays` slot and the response
// `overlays` it produces are visible — predict accepts, the runtime
// runs, and the instance payload schema names both slots. The slot gate
// once listed only the crosstab and compose hosts, so this valid profile
// refused the overlay it enables as PULSE_REQUEST_UNKNOWN_FIELD.
func TestMatrixGroupSummary_MatricesOnlyInstance(t *testing.T) {
	t.Parallel()

	_, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	p, err := pulse.New(pulse.Options{FS: fs, FeatureProfile: &pulse.FeatureProfile{Features: []string{
		"capability:process", "capability:matrices", "MAT_CORRELATION", "GROUP_CATEGORY", "AGG_COUNT",
		"OVERLAY_MATRIX_GROUP_SUMMARY",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	req := mgsRequest(cohort, "", mgsSpec("r"))
	if env := predictEnvelope(t, p, fs, cohort, req); len(env.Errors) != 0 {
		t.Fatalf("predict %+v", env.Errors)
	}
	resp, err := p.Process(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Overlays) != 1 || resp.Overlays[0].Kind != types.OverlayKindMatrixGroupSummary {
		t.Fatalf("overlays %+v", resp.Overlays)
	}
	schema, err := p.PayloadSchema()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Defs map[string]struct {
			Properties map[string]any `json:"properties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(schema, &doc); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{"Request", "Response"} {
		if _, ok := doc.Defs[root].Properties["overlays"]; !ok {
			t.Errorf("instance schema %s has no overlays", root)
		}
	}
}
