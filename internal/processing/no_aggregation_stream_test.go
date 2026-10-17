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
		// U48 E3-S3 fix: tests beside a regression fold in the same
		// ungrouped loop — one readout request, no Compose batch.
		{"tests beside a regression", func() *types.Request {
			return &types.Request{Regressions: []*types.RegressionSpec{ols()}, Tests: []*types.Test{{Type: types.TEST_T, Field: "x"}}}
		}, true},
		{"weighted tests beside a weighted regression and a filter", func() *types.Request {
			return &types.Request{Weight: weight, Filterers: []*types.Filterer{filter()},
				Regressions: []*types.RegressionSpec{ols()},
				Tests: []*types.Test{
					{Type: types.TEST_ANOVA_F, Field: "x", SplitBy: "g"},
					{Type: types.TEST_PEARSON_R, Field: "x", Field2: "y"},
					{Type: types.TEST_T, Field: "y", Params: json.RawMessage(`{"mu":1}`)},
				}}
		}, true},
		{"tests beside a regression and an aggregation", func() *types.Request {
			return &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "y"}},
				Regressions: []*types.RegressionSpec{ols()}, Tests: []*types.Test{{Type: types.TEST_PEARSON_R, Field: "x", Field2: "y"}}}
		}, true},
		{"tests beside a robust regression buffer", func() *types.Request {
			r := ols()
			r.Covariance = &types.RegressionCovariance{Type: types.CovarianceHC1}
			return &types.Request{Regressions: []*types.RegressionSpec{r}, Tests: []*types.Test{{Type: types.TEST_T, Field: "x"}}}
		}, false},
		{"grouped tests beside a regression buffer", func() *types.Request {
			return &types.Request{Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "y"}},
				Regressions:  []*types.RegressionSpec{ols()}, Tests: []*types.Test{{Type: types.TEST_T, Field: "x"}}}
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
			if streamed.Data != nil && len(c.req().Aggregations) == 0 {
				t.Fatalf("a no-aggregation request emitted data rows: %v", streamed.Data)
			}
		})
	}
}

// TestPostTestWarnings_StreamAndMatchBuffered: a streamable request
// carrying post-tests answers byte-identically streamed and forced-
// buffered, WARNINGS included (U48 E3-S3). Post-test warnings are the
// partitioned run's sparse-partition warnings, so only a grouped request
// raises them (ungrouped, the post-test runs once over the one data row
// and runPostTests returns no warning); on the grouped stream they ride
// the shared grouped tail. The ungrouped streaming arms and the crosstab
// arm keep runPostTests' warnings anyway, in the buffered arm's order.
func TestPostTestWarnings_StreamAndMatchBuffered(t *testing.T) {
	schema := weightedTestSchema()
	var recs []*Record
	for _, r := range weightedTestFixture() {
		recs = append(recs, NewRecord(schema, map[string]float64{"x": r.x, "y": r.y, "w": r.w, "g": float64(dictIDOrAdd(schema, "g", r.g))}))
	}
	aggs := func() []*types.Aggregation {
		return []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "x", Label: "n"},
			{Type: types.AGG_AVERAGE, Field: "y", Label: "my"},
		}
	}
	pearson := func() []*types.Test {
		return []*types.Test{{Type: types.TEST_PEARSON_R, Field: "n", Field2: "my", Label: "r"}}
	}
	cases := []struct {
		name      string
		req       func() *types.Request
		wantWarns bool
	}{
		// Two groupers: the post-test runs once per g partition; each
		// partition's handful of (g, round(x)) rows is too few for a
		// correlation, so every partition warns.
		{"grouped sparse partitions", func() *types.Request {
			return &types.Request{Groups: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "g"},
				{Type: types.GROUP_ROUNDED, Field: "x", Interval: 2},
			}, Aggregations: aggs(), PostTests: pearson()}
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := NewProcessor(schema)
			streamed, err := p.Process(context.Background(), c.req(), NewSliceIterator(recs))
			if err != nil {
				t.Fatal(err)
			}
			if p.LastPath() != PathStreaming {
				t.Fatalf("ran %v, want streaming", p.LastPath())
			}
			buffered, err := processBuffered(NewProcessor(schema), c.req(), recs)
			if err != nil {
				t.Fatal(err)
			}
			if c.wantWarns && len(buffered.Warnings) == 0 {
				t.Fatal("fixture raised no post-test warning on the buffered arm")
			}
			a, _ := json.Marshal(streamed)
			b, _ := json.Marshal(buffered)
			if string(a) != string(b) {
				t.Fatalf("streaming and buffered differ:\n%s\n%s", a, b)
			}
		})
	}
}
