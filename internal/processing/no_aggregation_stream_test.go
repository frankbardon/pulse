package processing

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestNoAggregationRequests_StreamAndMatchBuffered: a request with no
// aggregation whose work is tier-1 tests or regressions folds on the
// streaming path (U48 #336) — no throwaway AGG_COUNT — and its whole
// response is byte-identical to the forced-buffered run. A request
// with neither (empty, attributes only) and one whose test or
// regression is buffered-only still routes buffered.
func TestNoAggregationRequests_StreamAndMatchBuffered(t *testing.T) {
	schema := weightedTestSchema()
	var recs []*Record
	for _, r := range weightedTestFixture() {
		recs = append(recs, NewRecord(schema, map[string]float64{"x": r.x, "y": r.y, "w": r.w, "g": float64(dictIDOrAdd(schema, "g", r.g))}))
	}
	weight := &types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}
	ols := func() *types.RegressionSpec {
		return &types.RegressionSpec{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x"}}
	}
	attr := func() *types.Attribute {
		return &types.Attribute{Type: types.ATTR_FORMULA, Label: "x2", Expression: "x * 2"}
	}
	filter := func() *types.Filterer {
		return &types.Filterer{Type: types.FILTER_EXPRESSION, Expression: "x > 3.5"}
	}
	cases := []struct {
		name   string
		req    func() *types.Request
		stream bool
	}{
		{"one-sample TEST_T", func() *types.Request {
			return &types.Request{Tests: []*types.Test{{Type: types.TEST_T, Field: "x", Params: json.RawMessage(`{"mu":5.5}`)}}}
		}, true},
		{"test battery", func() *types.Request {
			return &types.Request{Tests: []*types.Test{
				{Type: types.TEST_ANOVA_F, Field: "x", SplitBy: "g"},
				{Type: types.TEST_PEARSON_R, Field: "x", Field2: "y"},
				{Type: types.TEST_CHISQ, Rows: "g", Cols: "g"},
			}}
		}, true},
		{"weighted tests", func() *types.Request {
			return &types.Request{Weight: weight, Tests: []*types.Test{{Type: types.TEST_ANOVA_F, Field: "x", SplitBy: "g"}}}
		}, true},
		{"tests beside a filter and a row-local attribute", func() *types.Request {
			return &types.Request{Filterers: []*types.Filterer{filter()}, Attributes: []*types.Attribute{attr()},
				Tests: []*types.Test{{Type: types.TEST_PEARSON_R, Field: "x2", Field2: "y"}}}
		}, true},
		{"regression only", func() *types.Request {
			return &types.Request{Regressions: []*types.RegressionSpec{ols()}}
		}, true},
		{"weighted regression beside a filter", func() *types.Request {
			return &types.Request{Weight: weight, Filterers: []*types.Filterer{filter()}, Regressions: []*types.RegressionSpec{ols()}}
		}, true},
		{"rank test only buffers", func() *types.Request {
			return &types.Request{Tests: []*types.Test{{Type: types.TEST_KRUSKAL_WALLIS, Field: "x", SplitBy: "g"}}}
		}, false},
		{"robust regression only buffers", func() *types.Request {
			r := ols()
			r.Covariance = &types.RegressionCovariance{Type: types.CovarianceHC1}
			return &types.Request{Regressions: []*types.RegressionSpec{r}}
		}, false},
		{"tests beside a regression buffer", func() *types.Request {
			return &types.Request{Regressions: []*types.RegressionSpec{ols()}, Tests: []*types.Test{{Type: types.TEST_T, Field: "x"}}}
		}, false},
		{"empty request buffers", func() *types.Request { return &types.Request{} }, false},
		{"attributes only buffer", func() *types.Request {
			return &types.Request{Attributes: []*types.Attribute{attr()}}
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CanStreamRequest(c.req(), schema); got != c.stream {
				t.Fatalf("CanStreamRequest = %v, want %v", got, c.stream)
			}
			p := NewProcessor(schema)
			streamed, err := p.Process(context.Background(), c.req(), NewSliceIterator(recs))
			if err != nil {
				t.Fatal(err)
			}
			want := map[bool]ProcessPath{true: PathStreaming, false: PathBuffered}[c.stream]
			if p.LastPath() != want {
				t.Fatalf("ran %v, want %v", p.LastPath(), want)
			}
			buffered, err := processBuffered(NewProcessor(schema), c.req(), recs)
			if err != nil {
				t.Fatal(err)
			}
			a, _ := json.Marshal(streamed)
			b, _ := json.Marshal(buffered)
			if string(a) != string(b) {
				t.Fatalf("streaming and buffered differ:\n%s\n%s", a, b)
			}
			if streamed.Data != nil {
				t.Fatalf("a no-aggregation request emitted data rows: %v", streamed.Data)
			}
		})
	}
}
