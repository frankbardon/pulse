package service

import (
	"bytes"
	"fmt"
	"math"
	"testing"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Undefined figures are null, round 2 (matrix-extensions-hardening
// E4-S6). Beyond the empty bucket (E4-S5), a figure is undefined where
// its formula is 0/0 on the data it has: skewness / kurtosis over one
// value or zero spread, the z-score over zero spread, and a SAMPLE
// variance (AGG_WELFORD's, weighted_variance) where n < 2 / Σw ≤ 1. Each
// is NaN in Go and null on the wire on every execution arm, weighted
// too — never the 0 it used to read. The population spread of one value
// (AGG_VARIANCE) and a sample variance over zero spread stay a defined 0.

// degenRows: g = i % 5. Every g = 3 row but the first (i = 3) has x and
// y null, and that one carries weight 1 — one value, Σw = 1. Every g = 4
// row has x = 5 and y = 2.5 (zero spread, exact on every arm). g = 0..2
// keep parityRows' mix.
func degenRows(n int) []parityRow {
	rows := parityRows(n, freqWeight)
	for i := range rows {
		r := &rows[i]
		r.g = uint8(i % 5)
		switch r.g {
		case 3:
			if i == 3 {
				r.xNull, r.yNull, r.w = false, false, 1
			} else {
				r.xNull, r.yNull = true, true
			}
		case 4:
			r.x, r.y, r.xNull, r.yNull = 5, 2.5, false, false
		}
	}
	return rows
}

// degenOp is one aggregator under the sweep. groups maps g to the
// figure's expectation there: NaN for undefined, else the defined value.
// key names the operator-map key that mirrors the figure (checked the
// same way); pinned holds operator keys that stay defined, by g.
type degenOp struct {
	op       types.AggregationType
	field    string
	weighted bool
	groups   map[int]float64
	key      string
	pinned   map[int]map[string]float64
}

var nan = math.NaN()

var degenOps = []degenOp{
	{op: types.AGG_SKEWNESS, field: "y", weighted: true, groups: map[int]float64{3: nan, 4: nan}, key: "skewness"},
	{op: types.AGG_KURTOSIS, field: "y", weighted: true, groups: map[int]float64{3: nan, 4: nan}, key: "kurtosis"},
	// AGG_ZSCORE is buffered-only and not weight-aware. pop_mean,
	// pop_stddev (0) and target_value stay defined at zero spread.
	{op: types.AGG_ZSCORE, field: "x", groups: map[int]float64{3: nan, 4: nan}, key: "zscore",
		pinned: map[int]map[string]float64{4: {"pop_mean": 5, "pop_stddev": 0, "target_value": 5}}},
	// Control: the population variance of one value or of a constant is
	// a defined 0.
	{op: types.AGG_VARIANCE, field: "x", weighted: true, groups: map[int]float64{3: 0, 4: 0}, key: "variance"},
}

// degenComponents returns label's operator map for the bucket g.
func degenComponents(t *testing.T, resp *types.Response, label string, g int) map[string]any {
	t.Helper()
	if resp.Components == nil {
		t.Fatal("Components nil")
	}
	for _, a := range resp.Components.Aggregations {
		if a.Label != label {
			continue
		}
		for _, grp := range a.Groups {
			if len(grp.GroupKey) == 1 && fmt.Sprint(grp.GroupKey[0]) == fmt.Sprint(g) {
				return grp.Operator
			}
		}
	}
	t.Fatalf("no %s components for g=%d", label, g)
	return nil
}

// sameFigure: both NaN, or equal.
func sameFigure(got, want float64) bool {
	if math.IsNaN(want) {
		return math.IsNaN(got)
	}
	return got == want
}

// TestUndefinedFigures_DegenerateIsNull: skewness / kurtosis over one
// value or zero spread and the z-score over zero spread answer NaN (Go)
// / null (wire), scalar and mirroring component alike, on the buffered,
// streaming, grouped-streaming, two-pass, shard-parallel and
// parallel-decode arms, unweighted and (weight-aware operators) under a
// request frequency weight.
func TestUndefinedFigures_DegenerateIsNull(t *testing.T) {
	t.Parallel()

	store := newParityStore(t, "degen", degenRows)
	sources := []*paritySource{nil}
	for _, s := range paritySources() {
		if s.name == "request_frequency" {
			sources = append(sources, &s)
		}
	}
	for _, o := range degenOps {
		for _, src := range sources {
			if src != nil && !o.weighted {
				continue
			}
			name := string(o.op)
			if src != nil {
				name += "/weighted"
			}
			t.Run(name, func(t *testing.T) {
				ran := 0
				for _, mode := range parityModes() {
					path := store.paths[mode.cohort]
					build := func() *types.Request {
						return &types.Request{Cohort: &types.Cohort{Filename: path},
							Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
							Aggregations: []*types.Aggregation{{Type: o.op, Field: o.field, Label: "f"}}}
					}
					if src != nil && runArmRequest(t, store, mode, path, build, nil) == nil {
						continue
					}
					resp := runArmRequest(t, store, mode, path, build, src)
					if resp == nil {
						continue
					}
					ran++
					for g, want := range o.groups {
						goV, wire := undefCell(t, resp, "f", g)
						if math.IsNaN(want) {
							if !math.IsNaN(goV) || wire != nil {
								t.Errorf("%s: g=%d figure = %v (wire %v), want NaN / null", mode.name, g, goV, wire)
							}
						} else if goV != want || wire != want {
							t.Errorf("%s: g=%d figure = %v (wire %v), want %v", mode.name, g, goV, wire, want)
						}
						ops := degenComponents(t, resp, "f", g)
						if got, ok := ops[o.key].(float64); !ok || !sameFigure(got, want) {
							t.Errorf("%s: g=%d components %s = %v, want %v", mode.name, g, o.key, ops[o.key], want)
						}
						for k, w := range o.pinned[g] {
							if got, ok := ops[k].(float64); !ok || got != w {
								t.Errorf("%s: g=%d components %s = %v, want %v (defined)", mode.name, g, k, ops[k], w)
							}
						}
					}
					if goV, wire := undefCell(t, resp, "f", 0); math.IsNaN(goV) || wire == nil {
						t.Errorf("%s: g=0 figure = %v (wire %v), want a number", mode.name, goV, wire)
					}
				}
				if ran < 1 || o.op.Streamable() && ran < 3 {
					t.Fatalf("only %d arm(s) ran", ran)
				}
			})
		}
	}
}

// TestUndefinedFigures_SampleVarianceIsNull: AGG_WELFORD's triple and
// components carry an undefined (NaN / null) variance and stddev for a
// bucket with one value (unweighted n < 2; weighted Σw = 1 ≤ 1), and a
// defined 0 over zero spread; a weighted AGG_AVERAGE's weighted_variance
// component is undefined at Σw ≤ 1 too — on every arm the request
// reaches.
func TestUndefinedFigures_SampleVarianceIsNull(t *testing.T) {
	t.Parallel()

	store := newParityStore(t, "degenvar", degenRows)
	var freq *paritySource
	for _, s := range paritySources() {
		if s.name == "request_frequency" {
			freq = &s
		}
	}
	for _, src := range []*paritySource{nil, freq} {
		name := "unweighted"
		if src != nil {
			name = "weighted"
		}
		t.Run(name, func(t *testing.T) {
			ran := 0
			for _, mode := range parityModes() {
				path := store.paths[mode.cohort]
				build := func() *types.Request {
					return &types.Request{Cohort: &types.Cohort{Filename: path},
						Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
						Aggregations: []*types.Aggregation{
							{Type: types.AGG_WELFORD, Field: "x", Label: "f"},
							{Type: types.AGG_AVERAGE, Field: "x", Label: "avg"},
						}}
				}
				if src != nil && runArmRequest(t, store, mode, path, build, nil) == nil {
					continue
				}
				resp := runArmRequest(t, store, mode, path, build, src)
				if resp == nil {
					continue
				}
				ran++
				env := envelopeJSON(t, resp)
				for g, want := range map[int]float64{3: nan, 4: 0} {
					var triple processing.WelfordTriple
					for _, row := range resp.Data {
						if fmt.Sprint(row["g"]) == fmt.Sprint(g) {
							triple, _ = row["f"].(processing.WelfordTriple)
						}
					}
					if triple.N == 0 || !sameFigure(triple.Variance, want) {
						t.Errorf("%s: g=%d triple = %+v, want variance %v", mode.name, g, triple, want)
					}
					wire, _ := dataRow(t, env, float64(g))["f"].(map[string]any)
					if v, present := wire["variance"]; !present || math.IsNaN(want) && v != nil || !math.IsNaN(want) && v != want {
						t.Errorf("%s: g=%d wire triple = %v, want variance %v", mode.name, g, wire, want)
					}
					ops := degenComponents(t, resp, "f", g)
					for _, k := range []string{"variance", "stddev"} {
						if got, ok := ops[k].(float64); !ok || !sameFigure(got, want) {
							t.Errorf("%s: g=%d components %s = %v, want %v", mode.name, g, k, ops[k], want)
						}
					}
				}
				if src != nil {
					// Σw = 1 at g = 3: the sample variance m2 / (Σw − 1) is
					// undefined; g = 4 (Σw > 1, zero spread) is a defined 0.
					for g, want := range map[int]float64{3: nan, 4: 0} {
						ops := degenComponents(t, resp, "avg", g)
						if got, ok := ops["weighted_variance"].(float64); !ok || !sameFigure(got, want) {
							t.Errorf("%s: g=%d weighted_variance = %v, want %v", mode.name, g, ops["weighted_variance"], want)
						}
					}
				}
			}
			// AGG_WELFORD is streamable, not request-mergeable: buffered,
			// streaming and grouped streaming (its fused crosstab merge is
			// TestCrosstabComponents_CellComponents_BufferedVsFused_ParityByteEqual).
			if ran < 3 {
				t.Fatalf("only %d arm(s) ran", ran)
			}
		})
	}
}

// TestUndefinedFigures_DegenerateCrosstabCellAndMargin: a crosstab whose
// cell is AGG_SKEWNESS answers NaN (null) in a cell with one value or
// zero spread and in the row margin of a zero-spread row, while a
// mixed margin stays a number.
func TestUndefinedFigures_DegenerateCrosstabCellAndMargin(t *testing.T) {
	t.Parallel()

	store := newParityStore(t, "degenct", degenRows)
	for _, weighted := range []bool{false, true} {
		t.Run(fmt.Sprint("weighted=", weighted), func(t *testing.T) {
			svc := New(store.cfg(paritySingleFile))
			req := &types.Request{Cohort: &types.Cohort{Filename: store.paths[paritySingleFile]},
				Crosstab: &types.CrosstabSpec{
					Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
					Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "h"}},
					Cell:    &types.Aggregation{Type: types.AGG_SKEWNESS, Field: "y", Label: "cell"},
					Shape:   types.CrosstabShapeMatrix,
					Margins: types.CrosstabMargins{Rows: true, Columns: true},
				}}
			if weighted {
				req.Weight = &types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}
			}
			resp, err := svc.Process(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			mx := resp.Crosstab.Matrix
			rowOf := map[string]int{}
			for i, k := range mx.RowKeys {
				rowOf[fmt.Sprint(k[0])] = i
			}
			for _, g := range []string{"3", "4"} {
				r := rowOf[g]
				for c, cell := range mx.Cells[r] {
					if !cell.Present {
						continue
					}
					if v, ok := cell.Value.(float64); !ok || !math.IsNaN(v) {
						t.Errorf("g=%s cell %d = %v, want NaN", g, c, cell.Value)
					}
				}
				if v, ok := mx.RowMargins[r].Value.(float64); !ok || !math.IsNaN(v) {
					t.Errorf("g=%s row margin = %v, want NaN", g, mx.RowMargins[r].Value)
				}
			}
			for c, m := range mx.ColumnMargins {
				if v, ok := m.Value.(float64); !ok || math.IsNaN(v) {
					t.Errorf("column margin %d = %v, want a number", c, m.Value)
				}
			}
			body := envelopeJSON(t, resp)
			if nullKeyCount(body, "value") == 0 {
				t.Error("no null cell value on the wire")
			}
		})
	}
}

// TestUndefinedFigures_PairwiseWelfordLegUndefined: an AGG_WELFORD
// crosstab cell with one value (unweighted n = 1; frequency-weighted
// Σw = 1, PAIRWISE_WELCH_T — the z twin is not weightable) carries an undefined variance, and OVERLAY_PAIRWISE_WELCH_T /
// _TWO_MEANS_Z give every pair it is a leg of NO p-value (absent, one
// aggregated skip warning) — never a p-value off a fake 0 variance —
// while every other pair scores. Predict accepts the request on both
// (no new refusal: predict reads no value).
func TestUndefinedFigures_PairwiseWelfordLegUndefined(t *testing.T) {
	t.Parallel()

	store := newParityStore(t, "degenpw", degenRows)
	for _, kind := range []types.OverlayKind{types.OverlayKindPairwiseWelchT, types.OverlayKindPairwiseTwoMeansZ} {
		for _, weighted := range []bool{false, true} {
			if weighted && kind == types.OverlayKindPairwiseTwoMeansZ {
				continue // not weightable (PULSE_WEIGHT_UNSUPPORTED)
			}
			t.Run(fmt.Sprintf("%s/weighted=%v", kind, weighted), func(t *testing.T) {
				svc := New(store.cfg(paritySingleFile))
				req := &types.Request{Cohort: &types.Cohort{Filename: store.paths[paritySingleFile]},
					Crosstab: &types.CrosstabSpec{
						Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
						Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "h"}},
						Cell:    &types.Aggregation{Type: types.AGG_WELFORD, Field: "x", Label: "cell"},
						Shape:   types.CrosstabShapeMatrix,
					},
					Overlays: []types.OverlaySpec{{Name: "pw", Kind: kind, Scope: types.OverlayScopeRow}}}
				if weighted {
					req.Weight = &types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}
				}
				data, err := afero.ReadFile(store.cfg(paritySingleFile).Fs(), store.paths[paritySingleFile])
				if err != nil {
					t.Fatal(err)
				}
				if env := descx.Predict(bytes.NewReader(data), req, &descx.PredictOptions{}); len(env.Errors) != 0 {
					t.Fatalf("predict errors %+v, want none", *env.Errors[0])
				}
				resp, err := svc.Process(t.Context(), req)
				if err != nil {
					t.Fatal(err)
				}
				// g = 3's one valued row sits in column h = 1 (i = 3).
				cc := resp.Components.Crosstab
				mx := resp.Crosstab.Matrix
				for i, rk := range mx.RowKeys {
					for j, ck := range mx.ColumnKeys {
						if fmt.Sprint(rk[0]) == "3" && fmt.Sprint(ck[0]) == "b" {
							if v, ok := cc.CellComponents[i][j]["variance"].(float64); !ok || !math.IsNaN(v) {
								t.Errorf("g=3/h=b variance = %v, want NaN", cc.CellComponents[i][j]["variance"])
							}
						}
					}
				}
				layer := resp.Overlays[0].Payload.Matrix
				scored, skipped := 0, 0
				for p, pk := range layer.RowKeys {
					legOf3 := fmt.Sprint(pk[0]) == "3" || fmt.Sprint(pk[1]) == "3"
					for o := range layer.ColumnKeys {
						c := layer.Cells[p][o]
						if legOf3 {
							if c.Present {
								t.Errorf("pair %v col %d scored %v, want absent (an undefined leg)", pk, o, c.Value)
							}
							skipped++
							continue
						}
						if v, ok := c.Value.(float64); !c.Present || !ok || math.IsNaN(v) {
							t.Errorf("pair %v col %d = %+v, want a p-value", pk, o, c)
						}
						scored++
					}
				}
				if scored == 0 || skipped == 0 {
					t.Fatalf("scored %d, skipped %d pair-cells", scored, skipped)
				}
				tooSmall := false
				for _, w := range resp.Overlays[0].Warnings {
					if w.Details["reason"] == "PULSE_STATS_SKIP_N_TOO_SMALL" {
						tooSmall = true
					}
				}
				if !tooSmall {
					t.Errorf("warnings %+v, want an N_TOO_SMALL skip for the one-value leg", resp.Overlays[0].Warnings)
				}
			})
		}
	}
}
