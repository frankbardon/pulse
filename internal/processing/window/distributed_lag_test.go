package window

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/distlagparams"
	"github.com/frankbardon/pulse/types"
)

// distLagOracleCase is one case of testdata/reference/distributed_lag.json,
// written by scripts/reference/gen_distributed_lag.R (`make reference`). R is the
// oracle: the kernel by decay^l / pweibull / dweibull, the series by
// stats::filter over the zero-padded series — never Pulse's loop.
type distLagOracleCase struct {
	Name       string          `json:"name"`
	Params     json.RawMessage `json:"params"`
	Weights    []float64       `json:"weights"`
	Partitions []struct {
		Partition string     `json:"partition"`
		X         []*float64 `json:"x"`
		Y         []*float64 `json:"y"`
	} `json:"partitions"`
}

type distLagOracleDoc struct {
	RVersion string              `json:"r_version"`
	Packages map[string]string   `json:"packages"`
	Cases    []distLagOracleCase `json:"cases"`
}

// distLagOracleRelTol is the reference suites' relative tolerance
// (internal/processing/reference_oracle_test.go): a few ulps of drift
// between R's pweibull / dweibull / pow and Go's math, never a bit match.
const distLagOracleRelTol = 1e-10

func loadDistLagOracle(t *testing.T) distLagOracleDoc {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "reference", "distributed_lag.json"))
	if err != nil {
		t.Fatalf("read adstock golden: %v (regenerate with `make reference`)", err)
	}
	s := string(raw)
	if i := strings.LastIndex(s, "\n// golden-hash: "); i >= 0 {
		s = s[:i]
	}
	var doc distLagOracleDoc
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatalf("decode adstock golden: %v", err)
	}
	if doc.RVersion == "" || doc.Packages["stats"] == "" || len(doc.Cases) == 0 {
		t.Fatal("adstock golden lacks R version, packages or cases")
	}
	return doc
}

func oracleClose(got, want float64) bool {
	return math.Abs(got-want) <= distLagOracleRelTol*math.Max(math.Abs(want), 1e-300)
}

// TestReferenceOracle_WinDistributedLag holds every kernel (geometric, Weibull
// CDF, Weibull PDF; normalised and raw) to R: the kernel weights through
// distlagparams, and the series through the full window Apply over two
// partitions with nulls, the rows handed over shuffled so partition_by
// and order_by do the ordering.
func TestReferenceOracle_WinDistributedLag(t *testing.T) {
	doc := loadDistLagOracle(t)
	for _, c := range doc.Cases {
		t.Run(c.Name, func(t *testing.T) {
			spec, ref := distlagparams.Resolve(c.Params)
			if ref != nil {
				t.Fatalf("params refused: %s", ref.Message)
			}
			if len(spec.Weights) != len(c.Weights) {
				t.Fatalf("kernel has %d weights, R %d", len(spec.Weights), len(c.Weights))
			}
			for l, w := range c.Weights {
				if !oracleClose(spec.Weights[l], w) {
					t.Errorf("weight[%d] = %.17g, R %.17g", l, spec.Weights[l], w)
				}
			}

			var rows []map[string]any
			want := map[[2]any]*float64{}
			for _, p := range c.Partitions {
				for i, x := range p.X {
					r := map[string]any{"g": p.Partition, "t": float64(i + 1)}
					if x != nil {
						r["x"] = *x
					} else {
						r["x"] = nil
					}
					rows = append(rows, r)
					want[[2]any{p.Partition, float64(i + 1)}] = p.Y[i]
				}
			}
			// Interleave and reverse, so the window must sort.
			for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
				rows[i], rows[j] = rows[j], rows[i]
			}
			w := &types.Window{
				Type: types.WIN_DISTRIBUTED_LAG, Field: "x", Label: "ad",
				PartitionBy: []string{"g"},
				OrderBy:     []types.OrderKey{{Field: "t"}},
				Params:      c.Params,
			}
			if err := Apply(context.Background(), rows, []*types.Window{w}); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			for _, r := range rows {
				key := [2]any{r["g"], r["t"]}
				exp := want[key]
				got := r["ad"]
				if exp == nil {
					if got != nil {
						t.Errorf("%v: got %v, R null", key, got)
					}
					continue
				}
				g, ok := got.(float64)
				if !ok || !oracleClose(g, *exp) {
					t.Errorf("%v: got %v, R %.17g", key, got, *exp)
				}
			}
		})
	}
}

// TestWinDistributedLag_GeometricApproximatesEWMA: a normalised geometric
// kernel at a long max_lag is WIN_EWMA with alpha = 1 − decay once the
// start-up has faded. The two differ only in how they begin — EWMA seeds
// at the first value with full weight, adstock starts from zero and
// renormalises a truncated geometric sum — and that difference shrinks
// as decay^i. Tolerance: with decay 0.5 and max_lag 60 the gap at row i
// is bounded by decay^i · max|x| plus decay^61 (truncation); past row 40
// both are below 1e-9 relative on this series.
func TestWinDistributedLag_GeometricApproximatesEWMA(t *testing.T) {
	const n, burnIn = 80, 40
	const relTol = 1e-9
	rows := make([]map[string]any, n)
	for i := range rows {
		rows[i] = map[string]any{"t": float64(i), "x": 50 + 30*math.Sin(float64(i)/3) + float64(i%7)}
	}
	ws := []*types.Window{
		{Type: types.WIN_DISTRIBUTED_LAG, Field: "x", Label: "ad", OrderBy: []types.OrderKey{{Field: "t"}},
			Params: json.RawMessage(`{"kernel": "geometric", "decay": 0.5, "max_lag": 60}`)},
		{Type: types.WIN_EWMA, Field: "x", Label: "ew", OrderBy: []types.OrderKey{{Field: "t"}},
			Frame: &types.FrameSpec{Mode: "rows"}, Params: json.RawMessage(`{"alpha": 0.5}`)},
	}
	if err := Apply(context.Background(), rows, ws); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for i, r := range rows {
		ad, ew := r["ad"].(float64), r["ew"].(float64)
		if i < burnIn {
			continue
		}
		if math.Abs(ad-ew) > relTol*math.Abs(ew) {
			t.Errorf("row %d: adstock %.17g vs EWMA %.17g (rel %.3g)", i, ad, ew, math.Abs(ad-ew)/math.Abs(ew))
		}
	}
	// The start-up genuinely differs (adstock starts from zero), so the
	// burn-in is not a formality.
	if ad, ew := rows[0]["ad"].(float64), rows[0]["ew"].(float64); math.Abs(ad-ew) < 1 {
		t.Fatalf("row 0: adstock %v and EWMA %v agree; the parity window proves nothing", ad, ew)
	}
}

// TestWinDistributedLag_NullsAndPartitions pins the row rules by hand: a null
// row reads null and adds nothing later (the carry-over still decays
// across it — unlike WIN_EWMA), a lag never crosses a partition, and the
// weights are not renormalised over the values present.
func TestWinDistributedLag_NullsAndPartitions(t *testing.T) {
	rows := []map[string]any{
		{"g": "a", "t": 1.0, "x": 10.0},
		{"g": "a", "t": 2.0, "x": nil},
		{"g": "a", "t": 3.0, "x": 20.0},
		{"g": "b", "t": 1.0, "x": nil},
		{"g": "b", "t": 2.0, "x": 4.0},
	}
	w := &types.Window{Type: types.WIN_DISTRIBUTED_LAG, Field: "x", Label: "ad",
		PartitionBy: []string{"g"}, OrderBy: []types.OrderKey{{Field: "t"}},
		Params: json.RawMessage(`{"kernel": "geometric", "decay": 0.5, "max_lag": 2, "normalize": false}`)}
	if err := Apply(context.Background(), rows, []*types.Window{w}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := []any{10.0, nil, 20.0 + 0.25*10.0, nil, 4.0}
	for i, r := range rows {
		if r["ad"] != want[i] {
			t.Errorf("row %d: ad = %v, want %v", i, r["ad"], want[i])
		}
	}
}

// TestWinDistributedLag_DeterministicAcrossRuns: the same request twice gives
// bit-identical columns (the kernel sum order is fixed, lag 0 first).
func TestWinDistributedLag_DeterministicAcrossRuns(t *testing.T) {
	run := func() []any {
		rows := make([]map[string]any, 50)
		for i := range rows {
			rows[i] = map[string]any{"t": float64(49 - i), "x": float64((i*37)%11) + 0.1*float64(i)}
		}
		w := &types.Window{Type: types.WIN_DISTRIBUTED_LAG, Field: "x", Label: "ad", OrderBy: []types.OrderKey{{Field: "t"}},
			Params: json.RawMessage(`{"kernel": "weibull_pdf", "shape": 2.2, "scale": 3.1, "max_lag": 12}`)}
		if err := Apply(context.Background(), rows, []*types.Window{w}); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		out := make([]any, len(rows))
		for i, r := range rows {
			out[i] = r["ad"]
		}
		return out
	}
	a, b := run(), run()
	for i := range a {
		if math.Float64bits(a[i].(float64)) != math.Float64bits(b[i].(float64)) {
			t.Fatalf("row %d: %v vs %v", i, a[i], b[i])
		}
	}
}
