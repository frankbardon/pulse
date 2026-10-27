package service

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestPostTest_LagColumnNaturalNull (U36 E1-S5, #294): a WIN_LAG column
// keeps its natural null first row (no params.default) and feeds
// TEST_PEARSON_R as a post-test directly. The post-test drops the null
// row listwise — R's cor.test default — and reports n = rows − 1; the
// lag-1 autocorrelation equals the Pearson r of (x[i-1], x[i]) computed
// here from the fixture. A default of 0 would instead pair x[0] with 0
// and bias r.
func TestPostTest_LagColumnNaturalNull(t *testing.T) {
	t.Parallel()

	svc, _ := overlayFoldService(t)
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "m.pulse"},
		Windows: []*types.Window{{
			Type: types.WIN_LAG, Field: "x", Label: "x_lag",
			OrderBy: []types.OrderKey{{Field: "id"}}, Params: json.RawMessage(`{"offset":1}`),
		}},
		PostTests: []*types.Test{{Type: types.TEST_PEARSON_R, Field: "x_lag", Field2: "x", Label: "acf1"}},
	}
	resp := mustProcess(t, svc, req)
	if len(resp.Data) != 80 {
		t.Fatalf("%d data rows, want 80", len(resp.Data))
	}
	if first := resp.Data[0]["x_lag"]; first != nil {
		t.Fatalf("first x_lag = %v, want the natural null", first)
	}
	if len(resp.PostTests) != 1 || resp.PostTests[0] == nil {
		t.Fatalf("post_tests = %+v, want one result", resp.PostTests)
	}
	res := resp.PostTests[0]
	if res.Error != nil {
		t.Fatalf("post-test failed: %+v", res.Error)
	}
	if n := res.Details["n"]; n != int64(79) && n != 79 {
		t.Fatalf("n = %v (%T), want 79 (80 rows, the null lag row dropped)", n, n)
	}

	// Reference: Pearson r over the 79 complete (x[i-1], x[i]) pairs.
	recs := multRows(80, 0)
	xs := make([]float64, 80)
	for i, r := range recs {
		xs[i] = math.Float64frombits(r[3])
	}
	a, b := xs[:79], xs[1:]
	var ma, mb float64
	for i := range a {
		ma += a[i]
		mb += b[i]
	}
	ma /= 79
	mb /= 79
	var sab, saa, sbb float64
	for i := range a {
		sab += (a[i] - ma) * (b[i] - mb)
		saa += (a[i] - ma) * (a[i] - ma)
		sbb += (b[i] - mb) * (b[i] - mb)
	}
	want := sab / math.Sqrt(saa*sbb)
	if d := math.Abs(res.Statistic - want); d > 1e-12*math.Abs(want) {
		t.Errorf("r = %.17g, want %.17g (rel %.3g)", res.Statistic, want, d/math.Abs(want))
	}
	if res.DF != 77 {
		t.Errorf("df = %v, want 77", res.DF)
	}
}
