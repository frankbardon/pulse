package processing

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/types"
)

// limitGroupsFixture: 12 records over a 4-entry categorical (keys a..d)
// with two numeric columns for matrices.
func limitGroupsFixture(t *testing.T) (*encoding.Schema, []*Record) {
	t.Helper()
	d := encoding.NewDictionary()
	for _, v := range []string{"a", "b", "c", "d"} {
		if _, err := d.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "cat", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, Dictionary: d},
		{Name: "x", Type: encoding.FieldTypeF64, ByteOffset: 1},
		{Name: "y", Type: encoding.FieldTypeF64, ByteOffset: 9},
	}}
	recs := make([]*Record, 12)
	for i := range recs {
		recs[i] = NewRecord(schema, map[string]float64{"cat": float64(i % 4), "x": float64(i), "y": float64(i * i)})
	}
	return schema, recs
}

// requireMaxGroups asserts err is PULSE_LIMIT_EXCEEDED on max_groups
// at configured, and returns its observed figure.
func requireMaxGroups(t *testing.T, err error, configured int64) int64 {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_LIMIT_EXCEEDED {
		t.Fatalf("err = %v, want PULSE_LIMIT_EXCEEDED", err)
	}
	if ce.Details["limit"] != string(limits.MaxGroups) || ce.Details["configured"] != configured {
		t.Fatalf("details = %v, want limit=max_groups configured=%d", ce.Details, configured)
	}
	obs, _ := ce.Details["observed"].(int64)
	return obs
}

func maxGroups(n int64) limits.Limits {
	l := limits.Defaults()
	l.MaxGroups = n
	return l
}

// TestMaxGroups_ProcessorPaths: the serial streaming-grouped mint and
// the buffered grouped path each refuse a fourth bucket under
// MaxGroups=3 with no partial result, and run at the limit.
func TestMaxGroups_ProcessorPaths(t *testing.T) {
	schema, recs := limitGroupsFixture(t)
	grp := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}}
	for _, c := range []struct {
		name string
		agg  types.AggregationType
		path ProcessPath
	}{
		{"serial streaming", types.AGG_SUM, PathStreaming},
		{"buffered", types.AGG_MEDIAN, PathBuffered},
	} {
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{Groups: grp, Aggregations: []*types.Aggregation{{Type: c.agg, Field: "x", Label: "v"}}}
			run := func(l limits.Limits) (*Processor, *types.Response, error) {
				p := NewProcessor(schema)
				p.SetLimits(l)
				resp, err := p.Process(context.Background(), req, NewSliceIterator(recs))
				return p, resp, err
			}
			p, resp, err := run(maxGroups(4))
			if err != nil || len(resp.Data) != 4 {
				t.Fatalf("at the limit: %v, %v", resp, err)
			}
			if p.LastPath() != c.path {
				t.Fatalf("ran %s, want %s — the test would not cover its site", p.LastPath(), c.path)
			}
			_, resp, err = run(maxGroups(3))
			if resp != nil {
				t.Fatalf("a trip returned a partial result: %+v", resp)
			}
			if obs := requireMaxGroups(t, err, 3); obs != 4 {
				t.Errorf("observed = %d, want 4", obs)
			}
			// A processor built without SetLimits enforces nothing.
			p = NewProcessor(schema)
			if _, err := p.Process(context.Background(), req, NewSliceIterator(recs)); err != nil {
				t.Fatalf("no limits installed: %v", err)
			}
		})
	}
}

// TestMaxGroups_GroupedMatricesMint: the grouped matrix state refuses
// minting a bucket past MaxGroups on its own — the parallel reducers'
// partitions and the buffered fold reach it independently of the
// aggregator bucket map.
func TestMaxGroups_GroupedMatricesMint(t *testing.T) {
	schema, recs := limitGroupsFixture(t)
	req := &types.Request{
		Groups:   []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
		Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Name: "cov", Fields: []string{"x", "y"}}},
	}
	g, err := BuildGroupedMatrices(req, schema, nil, FullComputePlan(), maxGroups(2))
	if err != nil || g == nil {
		t.Fatalf("BuildGroupedMatrices = %v, %v", g, err)
	}
	for i, key := range []string{"a", "b", "a"} {
		recs[i].SetMergePosition(0, i)
		if err := g.UpdateRow(key, recs[i]); err != nil {
			t.Fatalf("UpdateRow(%s) under the limit: %v", key, err)
		}
	}
	recs[3].SetMergePosition(0, 3)
	if obs := requireMaxGroups(t, g.UpdateRow("c", recs[3]), 2); obs != 3 {
		t.Errorf("observed = %d, want 3", obs)
	}
	if _, minted := g.buckets["c"]; minted {
		t.Fatal("the refused bucket was minted")
	}
	if err := g.foldRecords("d", recs[4:5]); err == nil {
		t.Fatal("the buffered fold minted a bucket past the limit")
	}

	// End to end on the buffered grouped path (a matrix-only grouped
	// request): the trip surfaces with no partial result.
	p := NewProcessor(schema)
	p.SetLimits(maxGroups(3))
	resp, err := p.Process(context.Background(), &types.Request{Groups: req.Groups, Matrices: req.Matrices,
		Aggregations: []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "x", Label: "m"}}}, NewSliceIterator(recs))
	if resp != nil {
		t.Fatalf("partial result: %+v", resp)
	}
	requireMaxGroups(t, err, 3)
}
