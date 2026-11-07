package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// An embedder overlay kind declaring Inferential + EmitsPValues returns
// RAW p-values at its payload shape's p-site; the engine corrects them
// in the request's multiplicity family like a built-in inferential
// kind's, never moving the raw p (U67 E2-S3). A kind not declaring
// WeightAware is refused under any weight in force, and every
// extension kind is buffered-only — predict and runtime agree on both.

const (
	overlayPGridKind  types.OverlayKind = "OVERLAY_ACME_PGRID"
	overlayPTrendKind types.OverlayKind = "OVERLAY_ACME_PTREND"
)

var pInterpretation = []descriptor.Interpretation{{Field: "p_value", Means: "the chance of a share this far from even under no difference"}}

// pGridMatrix is a p grid over the crosstab: each present cell's share
// of the grand total (a number in [0, 1]).
func pGridMatrix(_ *types.OverlaySpec, host extend.MatrixOverlayHost) (extend.OverlayOutput, error) {
	grand, ok := host.GrandTotal()
	if !ok || grand == 0 {
		return extend.OverlayOutput{}, fmt.Errorf("no grand total")
	}
	m := &types.MatrixPayload{RowHeader: host.RowHeader(), ColumnHeader: host.ColumnHeader(), CellLabel: "p"}
	for r := 0; r < host.Rows(); r++ {
		m.RowKeys = append(m.RowKeys, host.RowKey(r))
		row := make([]types.MatrixCell, host.Columns())
		for c := range row {
			if v, ok := host.Cell(r, c); ok {
				row[c] = types.MatrixCell{Value: v / grand, Present: true}
			}
		}
		m.Cells = append(m.Cells, row)
	}
	for c := 0; c < host.Columns(); c++ {
		m.ColumnKeys = append(m.ColumnKeys, host.ColumnKey(c))
	}
	return extend.OverlayOutput{Payload: types.OverlayPayload{Shape: types.OverlayShapeMatrix, Matrix: m}}, nil
}

// pTrendSeries is one p per group: its value's share of the total.
func pTrendSeries(_ *types.OverlaySpec, host extend.SeriesOverlayHost) (extend.OverlayOutput, error) {
	total := 0.0
	for i := 0; i < host.Groups(); i++ {
		if v, ok := host.Value(i); ok {
			total += v
		}
	}
	if total == 0 {
		return extend.OverlayOutput{}, fmt.Errorf("no total")
	}
	s := &types.SeriesPayload{}
	for i := 0; i < host.Groups(); i++ {
		e := types.SeriesEntry{Key: host.GroupKey(i)}
		if v, ok := host.Value(i); ok {
			p := v / total
			e.Summary.PValue = &p
		}
		s.Entries = append(s.Entries, e)
	}
	return extend.OverlayOutput{Payload: types.OverlayPayload{Shape: types.OverlayShapeSeries, Series: s}}, nil
}

// pGridRegistration is the crosstab p grid; NOT WeightAware.
func pGridRegistration() pulse.OverlayRegistration {
	return pulse.OverlayRegistration{
		Name: overlayPGridKind, Description: "share-of-total p grid",
		Handler:     extend.OverlayHandler{Matrix: pGridMatrix},
		Shapes:      []types.OverlayShape{types.OverlayShapeMatrix},
		Scopes:      []types.OverlayScope{types.OverlayScopeCell},
		Inferential: true, EmitsPValues: true, Interpretation: pInterpretation,
	}
}

// pTrendRegistration is the series p kind; WeightAware.
func pTrendRegistration() pulse.OverlayRegistration {
	return pulse.OverlayRegistration{
		Name: overlayPTrendKind, Description: "share-of-total p per group",
		Handler:     extend.OverlayHandler{Series: pTrendSeries},
		Shapes:      []types.OverlayShape{types.OverlayShapeSeries},
		Scopes:      []types.OverlayScope{types.OverlayScopeGroup},
		Inferential: true, EmitsPValues: true, Interpretation: pInterpretation, WeightAware: true,
	}
}

func openInferentialE2E(t *testing.T, opts pulse.Options) (*pulse.Pulse, *types.Cohort) {
	t.Helper()
	fsys := afero.NewMemMapFs()
	writeParityCohort(t, fsys, "e2e.pulse", paritySchema(t), 0, paritySmallRows)
	opts.FS = fsys
	opts.Extensions = pulse.Extensions{Overlays: []pulse.OverlayRegistration{pGridRegistration(), pTrendRegistration(), ratioRegistration()}}
	p, err := pulse.New(opts)
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	return p, &types.Cohort{Filename: "e2e.pulse"}
}

// pGridRequest is SUM(qty) by region with the specs.
func pGridRequest(cohort *types.Cohort, specs ...types.OverlaySpec) *types.Request {
	return &types.Request{
		Cohort: cohort,
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "qty", Label: "s"},
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		},
		Overlays: specs,
	}
}

func layerJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestExtensions_OverlayPValuesJoinFamily: a member extension layer's
// p-values are corrected — Bonferroni over the layer's cells, pooled
// across two layers under `request`, per entry on a SERIES layer — with
// the echo stamped, the raw p grid byte-identical to the uncorrected
// run, and predict's p_values counting exactly the family's m.
func TestExtensions_OverlayPValuesJoinFamily(t *testing.T) {
	t.Parallel()

	p, cohort := openInferentialE2E(t, pulse.Options{})
	ctx := context.Background()
	bonf := func(family types.MultiplicityFamily) *types.Multiplicity {
		return &types.Multiplicity{Method: types.MultiplicityMethodBonferroni, Family: family}
	}
	plain := processE2E(t, p, pGridRequest(cohort, types.OverlaySpec{Kind: overlayPGridKind, Scope: types.OverlayScopeCell}))
	raw := plain.Overlays[0].Payload.Matrix
	cells := 0
	for _, row := range raw.Cells {
		for _, c := range row {
			if c.Present {
				cells++
			}
		}
	}
	if cells != 4 || plain.Overlays[0].Payload.PAdjusted != nil || plain.Overlays[0].Multiplicity != nil {
		t.Fatalf("uncorrected layer = %d cells, p_adjusted %v, echo %v", cells, plain.Overlays[0].Payload.PAdjusted, plain.Overlays[0].Multiplicity)
	}
	check := func(t *testing.T, layer types.OverlayLayer, m int, family types.MultiplicityFamily) {
		t.Helper()
		if layerJSON(t, layer.Payload.Matrix) != layerJSON(t, raw) {
			t.Errorf("raw p grid moved under correction")
		}
		if e := layer.Multiplicity; e == nil || e.Method != types.MultiplicityMethodBonferroni || e.Family != family || e.M != m {
			t.Fatalf("echo = %+v, want bonferroni %s m=%d", e, family, m)
		}
		adj := layer.Payload.PAdjusted
		if adj == nil {
			t.Fatal("no p_adjusted grid")
		}
		for r, row := range raw.Cells {
			for c, cell := range row {
				if !cell.Present {
					continue
				}
				want := math.Min(1, float64(m)*cell.Value.(float64))
				if got := adj.Cells[r][c].Value; got != want {
					t.Errorf("p_adjusted[%d][%d] = %v, want %v", r, c, got, want)
				}
			}
		}
	}

	t.Run("layer", func(t *testing.T) {
		t.Parallel()
		resp := processE2E(t, p, pGridRequest(cohort, types.OverlaySpec{Kind: overlayPGridKind, Scope: types.OverlayScopeCell, Multiplicity: bonf(types.MultiplicityFamilyLayer)}))
		check(t, resp.Overlays[0], 4, types.MultiplicityFamilyLayer)
	})
	t.Run("request_pools_two_layers", func(t *testing.T) {
		t.Parallel()
		req := pGridRequest(cohort,
			types.OverlaySpec{Name: "a", Kind: overlayPGridKind, Scope: types.OverlayScopeCell},
			types.OverlaySpec{Name: "b", Kind: overlayPGridKind, Scope: types.OverlayScopeCell})
		req.Multiplicity = bonf(types.MultiplicityFamilyRequest)
		resp := processE2E(t, p, req)
		check(t, resp.Overlays[0], 8, types.MultiplicityFamilyRequest)
		check(t, resp.Overlays[1], 8, types.MultiplicityFamilyRequest)
		pr, err := p.Predict(ctx, req)
		if err != nil || len(pr.Errors) != 0 {
			t.Fatalf("Predict: %v %+v", err, pr)
		}
		if pv := pr.PValues; pv == nil || pv.Total != 8 || pv.Uncorrected != 0 || pv.Basis != descriptor.PValueBasisDictionary {
			t.Errorf("predict p_values = %+v, want total 8, uncorrected 0, basis dictionary", pv)
		}
	})
	t.Run("series", func(t *testing.T) {
		t.Parallel()
		resp := processE2E(t, p, overlaySeriesRequest(cohort, types.OverlaySpec{Kind: overlayPTrendKind, Scope: types.OverlayScopeGroup, Multiplicity: bonf(types.MultiplicityFamilyLayer)}))
		layer := resp.Overlays[0]
		if e := layer.Multiplicity; e == nil || e.M != 4 {
			t.Fatalf("series echo = %+v, want m=4", e)
		}
		for i, e := range layer.Payload.Series.Entries {
			if e.Summary.PValue == nil || e.Summary.PAdjusted == nil || *e.Summary.PAdjusted != math.Min(1, 4**e.Summary.PValue) {
				t.Errorf("entry %d p %v p_adjusted %v, want 4p", i, e.Summary.PValue, e.Summary.PAdjusted)
			}
		}
	})
	t.Run("none_is_identity", func(t *testing.T) {
		t.Parallel()
		resp := processE2E(t, p, pGridRequest(cohort, types.OverlaySpec{Kind: overlayPGridKind, Scope: types.OverlayScopeCell,
			Multiplicity: &types.Multiplicity{Method: types.MultiplicityMethodNone}}))
		if layerJSON(t, resp.Overlays[0]) != layerJSON(t, plain.Overlays[0]) {
			t.Errorf("method none changed the layer:\n%s\nwant\n%s", layerJSON(t, resp.Overlays[0]), layerJSON(t, plain.Overlays[0]))
		}
	})
}

// TestExtensions_OverlayWeightRefusalMatchesPredict: a weight in force
// on a non-WeightAware extension overlay — request, slot or instance
// default, and on a Compose slot it reads — is
// PULSE_EXTENSION_NOT_WEIGHT_AWARE at predict and at runtime with one
// message and one details map; `weight: null` opts the spec out, and a
// WeightAware kind runs weighted.
func TestExtensions_OverlayWeightRefusalMatchesPredict(t *testing.T) {
	t.Parallel()

	p, cohort := openInferentialE2E(t, pulse.Options{})
	def, _ := openInferentialE2E(t, pulse.Options{DefaultWeight: &types.WeightSpec{Field: "qty", Kind: types.WeightKindFrequency}})
	ctx := context.Background()
	freq := &types.WeightSpec{Field: "qty", Kind: types.WeightKindFrequency}
	grid := func(w types.SlotWeight) types.OverlaySpec {
		return types.OverlaySpec{Name: "g", Kind: overlayPGridKind, Scope: types.OverlayScopeCell, Weight: w}
	}
	weighted := func(spec types.OverlaySpec) *types.Request {
		r := pGridRequest(cohort, spec)
		r.Weight = freq
		return r
	}
	cases := []struct {
		name string
		p    *pulse.Pulse
		req  *types.Request
	}{
		{name: "request", p: p, req: weighted(grid(types.SlotWeight{}))},
		{name: "slot", p: p, req: pGridRequest(cohort, grid(types.SlotWeightOf(*freq)))},
		{name: "default", p: def, req: pGridRequest(cohort, grid(types.SlotWeight{}))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pr, err := tc.p.Predict(ctx, tc.req)
			if err != nil {
				t.Fatalf("Predict: %v", err)
			}
			if len(pr.Errors) != 1 {
				t.Fatalf("predict errors = %+v, want exactly one", pr.Errors)
			}
			_, err = tc.p.Process(ctx, tc.req)
			sameRefusal(t, tc.name, pr.Errors[0], err, perr.PULSE_EXTENSION_NOT_WEIGHT_AWARE)
			if got := pr.Errors[0].Details["operator"]; got != string(overlayPGridKind) {
				t.Errorf("details.operator = %v", got)
			}
		})
	}
	t.Run("opt_out", func(t *testing.T) {
		t.Parallel()
		resp := processE2E(t, p, weighted(grid(types.NullSlotWeight())))
		if len(resp.Overlays) != 1 || resp.Overlays[0].Payload.Matrix == nil {
			t.Fatalf("opted-out layer = %+v", resp.Overlays)
		}
		if pr, err := p.Predict(ctx, weighted(grid(types.NullSlotWeight()))); err != nil || len(pr.Errors) != 0 {
			t.Errorf("predict refused the opted-out spec: %v %+v", err, pr.Errors)
		}
	})
	t.Run("aware_runs_weighted", func(t *testing.T) {
		t.Parallel()
		req := overlaySeriesRequest(cohort, types.OverlaySpec{Kind: overlayPTrendKind, Scope: types.OverlayScopeGroup})
		req.Weight = freq
		resp := processE2E(t, p, req)
		if len(resp.Overlays) != 1 || resp.Overlays[0].Payload.Series == nil {
			t.Fatalf("aware layer = %+v", resp.Overlays)
		}
		if pr, err := p.Predict(ctx, req); err != nil || len(pr.Errors) != 0 {
			t.Errorf("predict refused the aware kind: %v %+v", err, pr.Errors)
		}
	})
	t.Run("compose", func(t *testing.T) {
		t.Parallel()
		req := composeWith(cohort, types.OverlayShapeMatrix, types.ComposeOverlaySpec{Name: "r", Kind: overlayRatioKind, Scope: types.OverlayScopeCell, Reference: "a", Targets: []string{"b"}})
		req.Requests[1].Weight = freq
		env, err := p.PredictCompose(ctx, req)
		if err != nil {
			t.Fatalf("PredictCompose: %v", err)
		}
		if len(env.Errors) != 1 {
			t.Fatalf("predict-compose errors = %+v, want exactly one", env.Errors)
		}
		_, err = p.Compose(ctx, req)
		sameRefusal(t, "compose", env.Errors[0], err, perr.PULSE_EXTENSION_NOT_WEIGHT_AWARE)
		if got := env.Errors[0].Details["host"]; got != "requests[1].crosstab.cell" {
			t.Errorf("details.host = %v", got)
		}
	})
}

// TestExtensions_OverlayStreamableMatchesRuntime: an extension overlay
// kind routes its request buffered — predict reports Streamable false
// naming it, and the runtime runs the buffered arm — while the same
// request with a streamable built-in kind (or none) streams on both.
func TestExtensions_OverlayStreamableMatchesRuntime(t *testing.T) {
	t.Parallel()

	var (
		mu   sync.Mutex
		last observe.Arm
	)
	p, cohort := openInferentialE2E(t, pulse.Options{Hooks: &observe.Hooks{
		OnOperationEnd: func(_ context.Context, info observe.OperationInfo, res observe.OperationResult) {
			if info.Kind == observe.OpProcess {
				mu.Lock()
				last = res.Arm
				mu.Unlock()
			}
		},
	}})
	ctx := context.Background()
	cases := []struct {
		name   string
		specs  []types.OverlaySpec
		stream bool
		reason string
	}{
		{name: "none", stream: true},
		{name: "builtin_streamable", specs: []types.OverlaySpec{{Kind: types.OverlayKindShareOfTotal, Scope: types.OverlayScopeGroup}}, stream: true},
		{name: "builtin_buffered", specs: []types.OverlaySpec{{Kind: types.OverlayKindIndexVsSibling, Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{Sibling: &types.OverlaySiblingRef{Field: "region", Value: "north"}}}}, reason: "is buffered-only"},
		{name: "extension", specs: []types.OverlaySpec{{Kind: overlayPTrendKind, Scope: types.OverlayScopeGroup}}, reason: "is an extension overlay kind"},
	}
	for _, tc := range cases {
		// serial subtests: the hook records the last Process arm.
		req := overlaySeriesRequest(cohort, tc.specs...)
		pr, err := p.Predict(ctx, req)
		if err != nil || len(pr.Errors) != 0 {
			t.Fatalf("%s: Predict: %v %+v", tc.name, err, pr)
		}
		processE2E(t, p, req)
		mu.Lock()
		arm := last
		mu.Unlock()
		if pr.Streamable != tc.stream || (arm == observe.ArmStreaming) != tc.stream {
			t.Errorf("%s: predict streamable %v (%v), runtime arm %q, want streaming=%v", tc.name, pr.Streamable, pr.StreamableReasons, arm, tc.stream)
		}
		if tc.reason != "" && !strings.Contains(strings.Join(pr.StreamableReasons, "; "), tc.reason) {
			t.Errorf("%s: reasons %v, want one containing %q", tc.name, pr.StreamableReasons, tc.reason)
		}
		if tc.name == "extension" && (len(pr.OverlaysApplied) != 1 || pr.OverlaysApplied[0].Streamable) {
			t.Errorf("extension overlays_applied = %+v, want one buffered entry", pr.OverlaysApplied)
		}
	}
}

// TestExtensions_OverlayInferentialRegistrationRefused: EmitsPValues
// needs Inferential, Inferential needs an Interpretation, and the
// probe holds a handler's p-values to EmitsPValues both ways.
func TestExtensions_OverlayInferentialRegistrationRefused(t *testing.T) {
	t.Parallel()

	noP := func(spec *types.OverlaySpec, host extend.SeriesOverlayHost) (extend.OverlayOutput, error) {
		out, err := pTrendSeries(spec, host)
		for i := range out.Payload.Series.Entries {
			out.Payload.Series.Entries[i].Summary.PValue = nil
		}
		return out, err
	}
	cases := []struct {
		name   string
		mut    func(*pulse.OverlayRegistration)
		code   perr.Code
		detail string
		want   string
	}{
		{name: "p_values_not_inferential", mut: func(r *pulse.OverlayRegistration) { r.Inferential = false }, code: perr.PULSE_EXTENSION_PARAM_INVALID, detail: "reason", want: "p_values_not_inferential"},
		{name: "interpretation_required", mut: func(r *pulse.OverlayRegistration) { r.Interpretation = nil }, code: perr.PULSE_EXTENSION_PURPOSE_INVALID, detail: "rule", want: "required"},
		{name: "undeclared_p_values", mut: func(r *pulse.OverlayRegistration) {
			r.EmitsPValues, r.Inferential, r.Interpretation = false, false, nil
		},
			code: perr.PULSE_EXTENSION_OVERLAY_OUTPUT_MISMATCH, detail: "reason", want: "undeclared_p_values"},
		{name: "missing_p_values", mut: func(r *pulse.OverlayRegistration) { r.Handler.Series = noP }, code: perr.PULSE_EXTENSION_OVERLAY_OUTPUT_MISMATCH, detail: "reason", want: "missing_p_values"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reg := pTrendRegistration()
			tc.mut(&reg)
			_, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: pulse.Extensions{Overlays: []pulse.OverlayRegistration{reg}}})
			ce := requireCodedError(t, err, tc.code)
			if got := ce.Details[tc.detail]; !reflect.DeepEqual(got, tc.want) {
				t.Errorf("details.%s = %v, want %s (%q)", tc.detail, got, tc.want, ce.Message)
			}
		})
	}
}
