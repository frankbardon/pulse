package pulse_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// End-to-end declared-order parity for the two-pass streaming
// orchestrator: a two-pass attribute reading an EARLIER attribute's
// label must see that label's final per-row value in its PrePass, on
// every arm that can carry it (single-file reuse decode with
// projection, the serial shard iterator, the >threshold single file
// that would take parallel decode if it were mergeable, and the
// ProcessStream chunk surface). The buffered reference is the same
// request with a non-streamable ATTR_PERCENTILE appended.

// tpoCenter is an extension two-pass attribute: v − mean(v), where v
// is Params.src when set (a name the planner can only learn through the
// FieldInputs hook) and the slot's Field otherwise.
type tpoCenter struct {
	src       string
	sum, mean float64
	n         int
	done      bool
}

func (a *tpoCenter) in(field string) string {
	if a.src != "" {
		return a.src
	}
	return field
}

func (a *tpoCenter) PrePass(rec extend.Record, field string) error {
	field = a.in(field)
	if v, ok := rec.NumericValue(field); ok {
		a.sum += v
		a.n++
	}
	return nil
}

func (a *tpoCenter) Finalize() error {
	if a.n > 0 {
		a.mean = a.sum / float64(a.n)
	}
	a.done = true
	return nil
}

func (a *tpoCenter) Row(rec extend.Record, field string) (float64, error) {
	if !a.done {
		return 0, fmt.Errorf("Row before Finalize")
	}
	v, _ := rec.NumericValue(a.in(field))
	return v - a.mean, nil
}

func (a *tpoCenter) Compute(rows extend.Rows, field string) ([]float64, error) {
	b := &tpoCenter{src: a.src}
	for i := 0; i < rows.Len(); i++ {
		_ = b.PrePass(rows.At(i), field)
	}
	_ = b.Finalize()
	out := make([]float64, rows.Len())
	for i := range out {
		out[i], _ = b.Row(rows.At(i), field)
	}
	return out, nil
}

func tpoExtensions() pulse.Extensions {
	srcOf := func(raw json.RawMessage) string {
		var p struct {
			Src string `json:"src"`
		}
		_ = json.Unmarshal(raw, &p)
		return p.Src
	}
	factory := func(spec *types.Attribute, _ *encoding.Schema) (extend.AttributeComputer, error) {
		return &tpoCenter{src: srcOf(spec.Params)}, nil
	}
	return pulse.Extensions{Attributes: []pulse.AttributeRegistration{
		// Declares its inputs: the planner reads Field only.
		{Name: "ATTR_TPO_CENTER", Factory: factory, Mode: pulse.AttributeModeTwoPass,
			FieldInputs: func(raw json.RawMessage) []string {
				if s := srcOf(raw); s != "" {
					return []string{s}
				}
				return nil
			}},
		// No FieldInputs hook: the planner must assume it reads every
		// earlier attribute, which is still correct.
		{Name: "ATTR_TPO_OPAQUE", Factory: factory, Mode: pulse.AttributeModeTwoPass},
	}}
}

type tpoArm struct {
	name string
	open func(t *testing.T) (*pulse.Pulse, string)
}

func tpoArms(t *testing.T, withLarge bool) []tpoArm {
	arms := []tpoArm{
		{"single_file", func(t *testing.T) (*pulse.Pulse, string) {
			fsys := afero.NewMemMapFs()
			writeParityCohort(t, fsys, "tpo.pulse", paritySchema(t), 0, paritySmallRows)
			p, err := pulse.New(pulse.Options{FS: fsys, Extensions: tpoExtensions()})
			if err != nil {
				t.Fatalf("pulse.New: %v", err)
			}
			return p, "tpo.pulse"
		}},
		{"shard_archive", func(t *testing.T) (*pulse.Pulse, string) {
			fsys := afero.NewMemMapFs()
			s := paritySchema(t)
			shards := []string{"s0.pulse", "s1.pulse", "s2.pulse"}
			for i, path := range shards {
				writeParityCohort(t, fsys, path, s, i*40, (i+1)*40)
			}
			p, err := pulse.New(pulse.Options{FS: fsys, ShardWorkers: 3, Extensions: tpoExtensions()})
			if err != nil {
				t.Fatalf("pulse.New: %v", err)
			}
			if _, err := p.CreateShardArchive(context.Background(), "archive.pulse", shards); err != nil {
				t.Fatalf("CreateShardArchive: %v", err)
			}
			return p, "archive.pulse"
		}},
	}
	if withLarge {
		large := parityLargeCohort(t.TempDir())
		arms = append(arms, tpoArm{"large_decode_workers", func(t *testing.T) (*pulse.Pulse, string) {
			dir := large(t)
			p, err := pulse.New(pulse.Options{DataDir: dir, DecodeWorkers: 4, Extensions: tpoExtensions()})
			if err != nil {
				t.Fatalf("pulse.New: %v", err)
			}
			return p, "large.pulse"
		}})
	}
	return arms
}

func tpoAssertClose(t *testing.T, what string, got, want map[string]any) {
	t.Helper()
	nonZero := false
	for k, wv := range want {
		w, wok := wv.(float64)
		g, gok := got[k].(float64)
		if !wok || !gok {
			t.Errorf("%s %s: got %v (%T), want %v (%T)", what, k, got[k], got[k], wv, wv)
			continue
		}
		if math.Abs(g-w) > 1e-9*math.Max(1, math.Abs(w)) {
			t.Errorf("%s %s: got %v, want %v", what, k, g, w)
		}
		if w != 0 {
			nonZero = true
		}
	}
	if !nonZero {
		t.Fatalf("%s: degenerate fixture, every reference aggregate is zero: %v", what, want)
	}
}

func runTPOParity(t *testing.T, p *pulse.Pulse, path string, req *types.Request) {
	t.Helper()
	ctx := context.Background()
	r := *req
	r.Cohort = &types.Cohort{Filename: path}

	pr, err := p.Predict(ctx, &r)
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if !pr.Streamable {
		t.Fatalf("request must take the streaming two-pass path; reasons %v", pr.StreamableReasons)
	}

	buf := r
	buf.Attributes = append(append([]*types.Attribute(nil), r.Attributes...),
		&types.Attribute{Type: types.ATTR_PERCENTILE, Field: "qty", Label: "qty_pct"})
	bResp, err := p.Process(ctx, &buf)
	if err != nil {
		t.Fatalf("buffered Process: %v", err)
	}
	sResp, err := p.Process(ctx, &r)
	if err != nil {
		t.Fatalf("streaming Process: %v", err)
	}
	if len(bResp.Data) != 1 || len(sResp.Data) != 1 {
		t.Fatalf("rows: buffered %d, streaming %d", len(bResp.Data), len(sResp.Data))
	}
	tpoAssertClose(t, "Process", sResp.Data[0], bResp.Data[0])
	if sResp.Metadata.TotalRows != bResp.Metadata.TotalRows || sResp.Metadata.FilteredRows != bResp.Metadata.FilteredRows {
		t.Errorf("metadata: streaming %+v, buffered %+v", *sResp.Metadata, *bResp.Metadata)
	}
	tpoAssertClose(t, "ProcessStream terminal chunk", streamE2E(t, p, &r), bResp.Data[0])
}

func tpoAggs(label string) []*types.Aggregation {
	return []*types.Aggregation{
		{Type: types.AGG_MAX, Field: label, Label: "max"},
		{Type: types.AGG_MIN, Field: label, Label: "min"},
		{Type: types.AGG_SUM, Field: label, Label: "sum"},
	}
}

// TestTwoPassOrder_UpstreamRowLocal is the E4-S8 matrix: every built-in
// and extension two-pass attribute fed by every row-local kind, across
// the execution arms.
func TestTwoPassOrder_UpstreamRowLocal(t *testing.T) {
	upstreams := map[string]*types.Attribute{
		"formula":      {Type: types.ATTR_FORMULA, Expression: "qty * qty + 1", Label: "u"},
		"date_part":    {Type: types.ATTR_DATE_PART, Field: "day", Params: []byte(`{"part":"day"}`), Label: "u"},
		"set_popcount": {Type: types.ATTR_SET_POPCOUNT, Field: "tags", Label: "u"},
		"set_has":      {Type: types.ATTR_SET_HAS, Field: "tags", Params: []byte(`{"label":"t001"}`), Label: "u"},
	}
	twoPass := map[string]*types.Attribute{
		"zscore":       {Type: types.ATTR_ZSCORE, Field: "u", Label: "out"},
		"tscore":       {Type: types.ATTR_TSCORE, Field: "u", Label: "out"},
		"normalized":   {Type: types.ATTR_NORMALIZED, Field: "u", Label: "out"},
		"reg_fitted":   {Type: types.ATTR_REG_FITTED, Target: "score", Predictors: []string{"u", "qty"}, Label: "out"},
		"reg_residual": {Type: types.ATTR_REG_RESIDUAL, Target: "score", Predictors: []string{"u", "qty"}, Label: "out"},
		"reg_leverage": {Type: types.ATTR_REG_LEVERAGE, Target: "score", Predictors: []string{"u", "qty"}, Label: "out"},
		"ext_declared": {Type: "ATTR_TPO_CENTER", Field: "u", Label: "out"},
		"ext_opaque":   {Type: "ATTR_TPO_OPAQUE", Field: "u", Label: "out"},
		// The upstream label reaches the extension ONLY through Params.
		"ext_declared_param": {Type: "ATTR_TPO_CENTER", Field: "qty", Params: []byte(`{"src":"u"}`), Label: "out"},
		"ext_opaque_param":   {Type: "ATTR_TPO_OPAQUE", Field: "qty", Params: []byte(`{"src":"u"}`), Label: "out"},
	}
	for _, arm := range tpoArms(t, false) {
		t.Run(arm.name, func(t *testing.T) {
			p, path := arm.open(t)
			for un, up := range upstreams {
				for tn, tp := range twoPass {
					t.Run(un+"_into_"+tn, func(t *testing.T) {
						runTPOParity(t, p, path, &types.Request{
							Attributes:   []*types.Attribute{up, tp},
							Aggregations: tpoAggs("out"),
						})
					})
				}
			}
		})
	}
}

// TestTwoPassOrder_Layers covers row-local → two-pass → row-local →
// two-pass chains (including an extension two-pass in the middle and a
// filter), on every arm including the >threshold single file.
func TestTwoPassOrder_Layers(t *testing.T) {
	cases := map[string]*types.Request{
		"repro_formula_zscore": {
			Attributes: []*types.Attribute{
				{Type: types.ATTR_FORMULA, Expression: "qty * 3 + 2", Label: "x"},
				{Type: types.ATTR_ZSCORE, Field: "x", Label: "zx"},
			},
			Aggregations: []*types.Aggregation{{Type: types.AGG_MAX, Field: "zx", Label: "max_zx"}},
		},
		"rl_tp_rl_tp": {
			Filterers: []*types.Filterer{{Type: types.FILTER_RANGE, Field: "qty", Values: []string{"1", "11"}}},
			Attributes: []*types.Attribute{
				{Type: types.ATTR_FORMULA, Expression: "qty * qty + 1", Label: "u"},
				{Type: types.ATTR_ZSCORE, Field: "u", Label: "z1"},
				{Type: types.ATTR_FORMULA, Expression: "z1 * z1 + qty", Label: "w"},
				{Type: "ATTR_TPO_CENTER", Field: "w", Label: "c"},
				{Type: types.ATTR_SET_POPCOUNT, Field: "tags", Label: "pc"},
				{Type: types.ATTR_FORMULA, Expression: "c * pc", Label: "v"},
				{Type: types.ATTR_NORMALIZED, Field: "v", Label: "out"},
			},
			Aggregations: append(tpoAggs("out"),
				&types.Aggregation{Type: types.AGG_SUM, Field: "z1", Label: "sum_z1"},
				&types.Aggregation{Type: types.AGG_MAX, Field: "c", Label: "max_c"}),
		},
		"opaque_extension_layer": {
			Attributes: []*types.Attribute{
				{Type: types.ATTR_ZSCORE, Field: "qty", Label: "z"},
				{Type: types.ATTR_FORMULA, Expression: "z + qty", Label: "w"},
				{Type: "ATTR_TPO_OPAQUE", Field: "qty", Params: []byte(`{"src":"w"}`), Label: "out"},
			},
			Aggregations: tpoAggs("out"),
		},
	}
	for _, arm := range tpoArms(t, true) {
		t.Run(arm.name, func(t *testing.T) {
			p, path := arm.open(t)
			for name, req := range cases {
				t.Run(name, func(t *testing.T) { runTPOParity(t, p, path, req) })
			}
		})
	}
}
