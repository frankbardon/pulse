package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// ATTR_SCALE_SCORE end to end (U27 E2-S1): every execution mode under
// the default projected decode. The score reads only its members, so a
// member the projection dropped would read as missing and silently move
// every figure: the sums below are the oracle over the generated rows
// (vexprRow), the out-of-range count the oracle's too.

type scaleScoreCase struct {
	name   string
	vecs   []types.VectorSpec
	attr   types.Attribute
	stat   string
	min    int // min_valid (0 = every member)
	lo, hi float64
	ranged bool
	rev    []int
}

func scaleScoreCases() []scaleScoreCase {
	lo, hi := 0.0, 10.0
	return []scaleScoreCase{
		{name: "vector kind scale, mean, every item",
			vecs: []types.VectorSpec{{Name: "battery", Fields: []string{"b*"}, Kind: types.VectorKindScale, ScaleMin: &lo, ScaleMax: &hi}},
			attr: types.Attribute{Vector: "battery"},
			stat: "mean",
			lo:   0, hi: 10, ranged: true},
		{name: "inline fields, prorated sum, reverse b2",
			attr: types.Attribute{Fields: []string{"b*"}, Params: json.RawMessage(`{"stat":"sum","min_valid":2,"scale_min":0,"scale_max":12,"reverse":["b2"]}`)},
			stat: "sum", min: 2, lo: 0, hi: 12, ranged: true, rev: []int{1}},
		{name: "labelled vector, reverse by label, no out of range",
			vecs: []types.VectorSpec{{Name: "battery", Fields: []string{"b*"}, Labels: []string{"w", "x", "y", "z"}, Kind: types.VectorKindScale, ScaleMin: &lo, ScaleMax: &[]float64{20}[0]}},
			attr: types.Attribute{Vector: "battery", Params: json.RawMessage(`{"reverse":["y"],"min_valid":1}`)},
			stat: "mean", min: 1, lo: 0, hi: 20, ranged: true, rev: []int{2}},
	}
}

// oracle returns the score sum, the scored-row count and the
// out-of-range value count over rows [0, rows).
func (c scaleScoreCase) oracle(rows int) (sum float64, scored, outOfRange int) {
	for r := 0; r < rows; r++ {
		x := vexprRow(r)
		for j, v := range x {
			if c.ranged && !math.IsNaN(v) && (v < c.lo || v > c.hi) {
				outOfRange++
				x[j] = math.NaN()
			}
		}
		for _, j := range c.rev {
			if !math.IsNaN(x[j]) {
				x[j] = c.lo + c.hi - x[j]
			}
		}
		k, s := 0, 0.0
		for _, v := range x {
			if !math.IsNaN(v) {
				k++
				s += v
			}
		}
		minValid := c.min
		if minValid == 0 {
			minValid = len(x)
		}
		if k == 0 || k < minValid {
			continue
		}
		score := s / float64(k)
		if c.stat == "sum" {
			score *= float64(len(x))
		}
		sum += score
		scored++
	}
	return sum, scored, outOfRange
}

func TestScaleScore_EveryModeUnderProjection(t *testing.T) {
	ctx := context.Background()
	for _, m := range vexprModes() {
		t.Run(m.name, func(t *testing.T) {
			p, cohort := m.open(t)
			for _, c := range scaleScoreCases() {
				t.Run(c.name, func(t *testing.T) {
					a := c.attr
					a.Type, a.Label = types.ATTR_SCALE_SCORE, "s"
					req := &types.Request{
						Cohort:     &types.Cohort{Filename: cohort},
						Vectors:    c.vecs,
						Attributes: []*types.Attribute{&a},
						Aggregations: []*types.Aggregation{
							{Type: types.AGG_SUM, Field: "s", Label: "sum_s"},
							{Type: types.AGG_COUNT, Field: "s", Label: "n_s"},
							// A column read beside the battery keeps the
							// projection narrow.
							{Type: types.AGG_SUM, Field: "qty", Label: "s_qty"},
						},
					}
					if m.buffered {
						req.Attributes = append(req.Attributes,
							&types.Attribute{Type: types.ATTR_PERCENTILE, Field: "qty", Label: "qty_pct"})
					}
					resp, err := p.Process(ctx, req)
					if err != nil {
						t.Fatalf("Process: %v", err)
					}
					wantSum, wantN, wantOut := c.oracle(m.rows)
					if got, ok := vexprFloat(resp.Data[0]["sum_s"]); !ok || !vexprClose(got, wantSum) {
						t.Errorf("sum_s = %v, want %v", resp.Data[0]["sum_s"], wantSum)
					}
					if got, ok := vexprFloat(resp.Data[0]["n_s"]); !ok || got != float64(wantN) {
						t.Errorf("n_s = %v, want %d", resp.Data[0]["n_s"], wantN)
					}
					var warned []*types.ResponseWarning
					for _, w := range resp.Warnings {
						if w.Code == string(perr.PULSE_ATTR_SCALE_OUT_OF_RANGE) {
							warned = append(warned, w)
						}
					}
					switch {
					case wantOut == 0 && len(warned) != 0:
						t.Errorf("unexpected warning %+v", warned[0])
					case wantOut > 0 && len(warned) != 1:
						t.Errorf("%d out-of-range warnings, want 1 (n_out_of_range %d): %+v", len(warned), wantOut, resp.Warnings)
					case wantOut > 0:
						if got, _ := vexprFloat(warned[0].Details["n_out_of_range"]); got != float64(wantOut) {
							t.Errorf("n_out_of_range = %v, want %d", warned[0].Details["n_out_of_range"], wantOut)
						}
					}
					res, err := p.Predict(ctx, req)
					if err != nil {
						t.Fatal(err)
					}
					if !res.Valid {
						t.Errorf("predict refuses a request Process runs")
					}
				})
			}
		})
	}
}

// TestScaleScore_RefusalsPredictRuntimeParity: a slot predict refuses
// is refused by Process with the same code, before any record.
func TestScaleScore_RefusalsPredictRuntimeParity(t *testing.T) {
	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	writeVexprCohort(t, fsys, "v.pulse", 0, 20)
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	data, err := afero.ReadFile(fsys, "v.pulse")
	if err != nil {
		t.Fatal(err)
	}
	for name, params := range map[string]string{
		"reverse without range": `{"reverse":["b1"]}`,
		"min_valid beyond p":    `{"min_valid":5}`,
		"bad stat":              `{"stat":"median"}`,
		"unknown reverse label": `{"reverse":["b9"],"scale_min":0,"scale_max":20}`,
	} {
		t.Run(name, func(t *testing.T) {
			req := &types.Request{
				Cohort:       &types.Cohort{Filename: "v.pulse"},
				Attributes:   []*types.Attribute{{Type: types.ATTR_SCALE_SCORE, Fields: []string{"b*"}, Label: "s", Params: json.RawMessage(params)}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "s"}},
			}
			_, err := p.Process(ctx, req)
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Code != perr.SERVICE_VALIDATION {
				t.Fatalf("Process: %v, want SERVICE_VALIDATION", err)
			}
			env, _ := p.PredictBytes(ctx, data, req)
			if env == nil || len(env.Errors) == 0 || env.Errors[0].Code != string(perr.SERVICE_VALIDATION) {
				t.Errorf("predict errors = %+v, want %s", env, perr.SERVICE_VALIDATION)
			}
		})
	}
}

// TestScaleScore_ReliabilityContrastFacade: on the same cohort and
// kind "scale" vector, MAT_RELIABILITY refuses the out-of-range battery
// while ATTR_SCALE_SCORE scores it with a warning.
func TestScaleScore_ReliabilityContrastFacade(t *testing.T) {
	ctx := context.Background()
	p, cohort := vexprModes()[0].open(t)
	lo, hi := 0.0, 10.0
	vecs := []types.VectorSpec{{Name: "battery", Fields: []string{"b*"}, Kind: types.VectorKindScale, ScaleMin: &lo, ScaleMax: &hi}}
	_, err := p.Process(ctx, &types.Request{
		Cohort:   &types.Cohort{Filename: cohort},
		Vectors:  vecs,
		Matrices: []types.MatrixSpec{{Type: types.MAT_RELIABILITY, Name: "rel", Vector: "battery"}},
	})
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PROCESSING_CONFIG {
		t.Fatalf("MAT_RELIABILITY: %v, want PROCESSING_CONFIG", err)
	}
	resp, err := p.Process(ctx, &types.Request{
		Cohort:       &types.Cohort{Filename: cohort},
		Vectors:      vecs,
		Attributes:   []*types.Attribute{{Type: types.ATTR_SCALE_SCORE, Vector: "battery", Label: "s"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "s"}},
	})
	if err != nil {
		t.Fatalf("ATTR_SCALE_SCORE: %v", err)
	}
	n := 0
	for _, w := range resp.Warnings {
		if w.Code == string(perr.PULSE_ATTR_SCALE_OUT_OF_RANGE) {
			n++
		}
	}
	if n != 1 {
		t.Errorf("want one PULSE_ATTR_SCALE_OUT_OF_RANGE, got %+v", resp.Warnings)
	}
}
