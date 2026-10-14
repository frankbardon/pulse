package pulse_test

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Vector aggregators (U27 E2-S2): AGG_VEC_MEAN / AGG_VEC_SUM fold one
// figure per vector member. Every execution mode runs under the default
// projected decode — a member the projection dropped would silently
// read as all-null and move every element — and the merge is
// bit-identical for every DecodeWorkers / ShardWorkers count.

// vaggArray reads an array value as the in-process Response carries it
// ([]float64) or as a JSON round trip would ([]any, null → NaN).
func vaggArray(v any) ([]float64, bool) {
	switch x := v.(type) {
	case []float64:
		return x, true
	case []any:
		out := make([]float64, len(x))
		for i, e := range x {
			if e == nil {
				out[i] = math.NaN()
				continue
			}
			f, ok := vexprFloat(e)
			if !ok {
				return nil, false
			}
			out[i] = f
		}
		return out, true
	}
	return nil, false
}

func vaggInts(v any) []int {
	switch x := v.(type) {
	case []int:
		return x
	case []any:
		out := make([]int, len(x))
		for i, e := range x {
			f, _ := vexprFloat(e)
			out[i] = int(f)
		}
		return out
	}
	return nil
}

// vaggOracle is the expected per-member figures over the rows keep
// admits, for the member indices idx.
type vaggOracle struct {
	mean, sum []float64
	elemN     []int
	n, nNull  int
}

func vaggCompute(rows int, idx []int, keep func(r int) bool) vaggOracle {
	o := vaggOracle{mean: make([]float64, len(idx)), sum: make([]float64, len(idx)), elemN: make([]int, len(idx))}
	for r := 0; r < rows; r++ {
		if keep != nil && !keep(r) {
			continue
		}
		answered := false
		for k, j := range idx {
			if x, ok := vexprMember(r, j); ok {
				o.sum[k] += x
				o.elemN[k]++
				answered = true
			}
		}
		if answered {
			o.n++
		} else {
			o.nNull++
		}
	}
	for k := range idx {
		if o.elemN[k] == 0 {
			o.mean[k] = math.NaN()
		} else {
			o.mean[k] = o.sum[k] / float64(o.elemN[k])
		}
	}
	return o
}

func vaggCheckArray(t *testing.T, what string, got any, want []float64) {
	t.Helper()
	g, ok := vaggArray(got)
	if !ok || len(g) != len(want) {
		t.Errorf("%s = %v (%T), want %v", what, got, got, want)
		return
	}
	for i := range g {
		if !vexprClose(g[i], want[i]) {
			t.Errorf("%s[%d] = %v, want %v", what, i, g[i], want[i])
		}
	}
}

func vaggComponents(resp *types.Response, label string) *types.AggregationComponents {
	if resp.Components == nil {
		return nil
	}
	for i := range resp.Components.Aggregations {
		if resp.Components.Aggregations[i].Label == label {
			return &resp.Components.Aggregations[i]
		}
	}
	return nil
}

func vaggRequest(cohort string) *types.Request {
	return &types.Request{
		Cohort: &types.Cohort{Filename: cohort},
		// b4 is reached ONLY through the inline fields slot, so a
		// projection that skipped them would drop it.
		Vectors: []types.VectorSpec{{Name: "battery", Fields: []string{"b1", "b2", "b3"},
			Labels: []string{"one", "two", "three"}}},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_VEC_MEAN, Vector: "battery", Label: "m"},
			{Type: types.AGG_VEC_SUM, Fields: []string{"b4", "b2"}, Label: "s"},
			// A column read beside the vector keeps the projection
			// narrow (an empty one decodes wide).
			{Type: types.AGG_SUM, Field: "qty", Label: "s_qty"},
		},
	}
}

// TestVecAgg_EveryModeUnderProjection: every execution mode, ungrouped
// and grouped, the default projected decode — the array values, the
// per-element n, the labels and the floor equal the oracle, and predict
// accepts the request.
func TestVecAgg_EveryModeUnderProjection(t *testing.T) {
	ctx := context.Background()
	all := []int{0, 1, 2}
	pair := []int{3, 1}
	for _, m := range vexprModes() {
		t.Run(m.name, func(t *testing.T) {
			p, cohort := m.open(t)
			for _, grouped := range []bool{false, true} {
				t.Run(fmt.Sprintf("grouped=%v", grouped), func(t *testing.T) {
					req := vaggRequest(cohort)
					if grouped {
						req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "qty"}}
					}
					if m.buffered {
						req.Attributes = append(req.Attributes,
							&types.Attribute{Type: types.ATTR_PERCENTILE, Field: "score", Label: "score_pct"})
					}
					resp, err := p.Process(ctx, req)
					if err != nil {
						t.Fatalf("Process: %v", err)
					}
					if !grouped {
						om, os := vaggCompute(m.rows, all, nil), vaggCompute(m.rows, pair, nil)
						vaggCheckArray(t, "m", resp.Data[0]["m"], om.mean)
						vaggCheckArray(t, "s", resp.Data[0]["s"], os.sum)
						for label, o := range map[string]vaggOracle{"m": om, "s": os} {
							c := vaggComponents(resp, label)
							if c == nil {
								t.Fatalf("no components for %s", label)
							}
							if c.N != o.n || c.NNull != o.nNull {
								t.Errorf("%s floor = {%d,%d}, want {%d,%d}", label, c.N, c.NNull, o.n, o.nNull)
							}
							if got := vaggInts(c.Operator["element_n"]); !reflect.DeepEqual(got, o.elemN) {
								t.Errorf("%s element_n = %v, want %v", label, got, o.elemN)
							}
						}
						if got := vaggComponents(resp, "m").Operator["labels"]; !reflect.DeepEqual(fmt.Sprint(got), "[one two three]") {
							t.Errorf("m labels = %v", got)
						}
						if got := vaggComponents(resp, "s").Operator["labels"]; !reflect.DeepEqual(fmt.Sprint(got), "[b4 b2]") {
							t.Errorf("s labels = %v", got)
						}
					} else {
						if len(resp.Data) != 7 {
							t.Fatalf("grouped rows = %d, want 7", len(resp.Data))
						}
						for _, row := range resp.Data {
							key := fmt.Sprint(row["qty"])
							keep := func(r int) bool { return fmt.Sprint(r%7) == key }
							vaggCheckArray(t, "m["+key+"]", row["m"], vaggCompute(m.rows, all, keep).mean)
							vaggCheckArray(t, "s["+key+"]", row["s"], vaggCompute(m.rows, pair, keep).sum)
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

// vaggBitsMember is row r's member j as a non-dyadic float (so the
// summation order reaches the last bits); ~1 in 9 is null.
func vaggBitsMember(r, j int) (float64, bool) {
	z := uint64(r*vexprMembers+j) + 0x9e3779b97f4a7c15
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	z ^= z >> 31
	if z%9 == 0 {
		return 0, false
	}
	return (float64(z>>11)/float64(1<<53))*2000 - 1000 + 1/3.0, true
}

func writeVaggBitsCohort(t testing.TB, fsys afero.Fs, path string, from, to int) {
	t.Helper()
	s := vexprSchema()
	stride, bm := s.RecordByteSize(), s.BitmapByteSize()
	var buf []byte
	w := &byteSink{b: &buf}
	if err := encoding.WriteHeader(w); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(w, s); err != nil {
		t.Fatal(err)
	}
	members := []int{0, 2, 3, 5}
	for r := from; r < to; r++ {
		rec := make([]byte, stride)
		put := func(off int, v uint64, n int) {
			for i := 0; i < n; i++ {
				rec[off+i] = byte(v >> (8 * i))
			}
		}
		for j, fi := range members {
			if v, ok := vaggBitsMember(r, j); ok {
				put(s.Fields[fi].ByteOffset, math.Float64bits(v), 8)
			} else {
				encoding.BitmapSetNull(rec[stride-bm:], fi)
			}
		}
		put(s.Fields[1].ByteOffset, math.Float64bits(1+float64(r%3)/7), 8)
		put(s.Fields[4].ByteOffset, uint64(r%7), 4)
		buf = append(buf, rec...)
	}
	if err := afero.WriteFile(fsys, path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

// vaggBits runs the request and returns every array element's bits,
// row by row, weighted and unweighted slots alike, then every
// covariance entry's bits (the BlockCoMoments arm of the blocked
// merge; per group on a grouped request).
func vaggBits(t *testing.T, p *pulse.Pulse, cohort string, grouped bool) []uint64 {
	t.Helper()
	req := &types.Request{
		Cohort: &types.Cohort{Filename: cohort},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_VEC_MEAN, Fields: []string{"b*"}, Label: "m"},
			{Type: types.AGG_VEC_SUM, Fields: []string{"b*"}, Label: "s"},
			{Type: types.AGG_VEC_MEAN, Fields: []string{"b*"}, Label: "wm", Weight: types.SlotWeightField("score")},
			{Type: types.AGG_VEC_SD, Fields: []string{"b*"}, Label: "sd"},
			{Type: types.AGG_VEC_SD, Fields: []string{"b*"}, Label: "wsd", Weight: types.SlotWeightField("score")},
		},
		Matrices: []types.MatrixSpec{
			{Type: types.MAT_COVARIANCE, Fields: []string{"b*"}, Name: "cov"},
			{Type: types.MAT_COVARIANCE, Fields: []string{"b*"}, Name: "wcov", Weight: types.SlotWeightField("score")},
		},
	}
	if grouped {
		req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "qty"}}
		req.Sort = []types.OrderKey{{Field: "qty"}}
	}
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	var out []uint64
	for _, row := range resp.Data {
		for _, label := range []string{"m", "s", "wm", "sd", "wsd"} {
			v, ok := vaggArray(row[label])
			if !ok {
				t.Fatalf("%s = %T", label, row[label])
			}
			for _, x := range v {
				out = append(out, math.Float64bits(x))
			}
		}
	}
	if len(resp.Matrices) == 0 {
		t.Fatal("no matrix results")
	}
	for _, m := range resp.Matrices {
		for _, r := range m.Primary.Values {
			for _, x := range r {
				out = append(out, math.Float64bits(x))
			}
		}
	}
	return out
}

// TestVecAgg_BitExactAcrossWorkers: a single file answers the same bits
// serially and under every DecodeWorkers count, and a shard archive
// holding the same rows — split unevenly, with shard edges inside merge
// blocks, 1-row shards and an empty shard — answers THOSE bits under
// every ShardWorkers × DecodeWorkers count: blocks key by the global
// record ordinal, so the twin and the archive fold the same blocks in
// the same tree. Grouped and ungrouped; weighted and unweighted vector
// aggregates (AGG_VEC_SD's Chan-combined moments included) and
// covariance matrices.
func TestVecAgg_BitExactAcrossWorkers(t *testing.T) {
	const rows = 5*4096 + 1234
	fsys := afero.NewMemMapFs()
	writeVaggBitsCohort(t, fsys, "single.pulse", 0, rows)
	bounds := []int{0, 1, 2, 4097, 4097, 9000, 9001, 14000, 14000 + 4096, rows}
	var shards []string
	for i := 0; i+1 < len(bounds); i++ {
		path := fmt.Sprintf("s%d.pulse", i)
		writeVaggBitsCohort(t, fsys, path, bounds[i], bounds[i+1])
		shards = append(shards, path)
	}
	builder, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := builder.CreateShardArchive(context.Background(), "archive.pulse", shards); err != nil {
		t.Fatal(err)
	}
	for _, grouped := range []bool{false, true} {
		t.Run(fmt.Sprintf("grouped=%v", grouped), func(t *testing.T) {
			serial, err := pulse.New(pulse.Options{FS: fsys})
			if err != nil {
				t.Fatal(err)
			}
			want := vaggBits(t, serial, "single.pulse", grouped)
			for _, dw := range []int{1, 2, 3, 7} {
				p, err := pulse.New(pulse.Options{FS: fsys, DecodeWorkers: dw})
				if err != nil {
					t.Fatal(err)
				}
				if got := vaggBits(t, p, "single.pulse", grouped); !reflect.DeepEqual(got, want) {
					t.Errorf("single file DecodeWorkers=%d: bits differ from serial", dw)
				}
				for _, sw := range []int{1, 2, 3, 4} {
					p, err := pulse.New(pulse.Options{FS: fsys, ShardWorkers: sw, DecodeWorkers: dw})
					if err != nil {
						t.Fatal(err)
					}
					if got := vaggBits(t, p, "archive.pulse", grouped); !reflect.DeepEqual(got, want) {
						t.Errorf("archive ShardWorkers=%d DecodeWorkers=%d: bits differ from the single-file twin", sw, dw)
					}
				}
			}
		})
	}
}

// TestVecAgg_RefusalParity: Process and predict refuse the same
// requests with the same code and reason — the array-valued hosts
// (crosstab cell / margin, Sort, overlays, a chain feed stage) and the
// slot shape rules.
func TestVecAgg_RefusalParity(t *testing.T) {
	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	writeVexprCohort(t, fsys, "v.pulse", 0, 40)
	data, err := afero.ReadFile(fsys, "v.pulse")
	if err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	vec := func() *types.Aggregation {
		return &types.Aggregation{Type: types.AGG_VEC_MEAN, Fields: []string{"b*"}, Label: "m"}
	}
	qty := func() []*types.Group { return []*types.Group{{Type: types.GROUP_CATEGORY, Field: "qty"}} }
	base := func(mut func(r *types.Request)) func() *types.Request {
		return func() *types.Request {
			r := &types.Request{Cohort: &types.Cohort{Filename: "v.pulse"}}
			mut(r)
			return r
		}
	}
	cases := []struct {
		name   string
		req    func() *types.Request
		code   perr.Code
		reason string
	}{
		{"crosstab cell", base(func(r *types.Request) {
			r.Crosstab = &types.CrosstabSpec{Rows: qty(), Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "b1"}}, Cell: vec()}
		}), perr.PROCESSING_CONFIG, "array_valued"},
		{"crosstab margin", base(func(r *types.Request) {
			r.Crosstab = &types.CrosstabSpec{Rows: qty(), Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "b1"}},
				Cell:               &types.Aggregation{Type: types.AGG_COUNT, Field: "qty"},
				MarginAggregations: []*types.Aggregation{vec()}}
		}), perr.PROCESSING_CONFIG, "array_valued"},
		{"sort", base(func(r *types.Request) {
			r.Groups, r.Aggregations, r.Sort = qty(), []*types.Aggregation{vec()}, []types.OrderKey{{Field: "m"}}
		}), perr.PROCESSING_CONFIG, "array_valued"},
		{"overlay host", base(func(r *types.Request) {
			r.Groups, r.Aggregations = qty(), []*types.Aggregation{vec()}
			r.Overlays = []types.OverlaySpec{{Kind: types.OverlayKindPairwisePropZ}}
		}), perr.PROCESSING_CONFIG, "array_valued"},
		{"field and fields", base(func(r *types.Request) {
			a := vec()
			a.Field = "b1"
			r.Aggregations = []*types.Aggregation{a}
		}), perr.SERVICE_VALIDATION, "field_on_vector_aggregation"},
		{"vector on scalar aggregator", base(func(r *types.Request) {
			r.Aggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "b1", Fields: []string{"b2"}}}
		}), perr.SERVICE_VALIDATION, "vector_not_accepted"},
		{"unknown vector", base(func(r *types.Request) {
			r.Aggregations = []*types.Aggregation{{Type: types.AGG_VEC_SUM, Vector: "nope"}}
		}), perr.PULSE_VECTOR_UNKNOWN, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, perrRun := p.Process(ctx, c.req())
			ce := codeOf(t, perrRun)
			if ce.Code != c.code || (c.reason != "" && ce.Details["reason"] != c.reason) {
				t.Fatalf("Process = %s %v, want %s %s", ce.Code, ce.Details, c.code, c.reason)
			}
			env, err := p.PredictBytes(ctx, data, c.req())
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(c.code) ||
				(c.reason != "" && env.Errors[0].Details["reason"] != c.reason) {
				t.Fatalf("predict = %+v, want %s %s", env.Errors, c.code, c.reason)
			}
		})
	}

	// A chain stage feeding another: its aggregate column would be an
	// array the next stage cannot read (total() / a ratio over it).
	chain := func(final bool) *pulse.ChainRequest {
		stages := []*types.ChainStage{{Name: "profile", Request: &types.Request{Groups: qty(), Aggregations: []*types.Aggregation{vec()}}}}
		if !final {
			stages = append(stages, &types.ChainStage{Name: "count", Request: &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "qty"}}}})
		}
		return &pulse.ChainRequest{Cohort: &types.Cohort{Filename: "v.pulse"}, Stages: stages}
	}
	_, err = p.ProcessChain(ctx, chain(false))
	if ce := codeOf(t, err); ce.Code != perr.PROCESSING_CONFIG || ce.Details["reason"] != "array_valued" {
		t.Fatalf("ProcessChain = %s %v", ce.Code, ce.Details)
	}
	cenv, err := p.PredictChain(ctx, chain(false))
	if err != nil || len(cenv.Errors) == 0 || cenv.Errors[0].Code != string(perr.PROCESSING_CONFIG) || cenv.Errors[0].Details["reason"] != "array_valued" {
		t.Fatalf("PredictChain = %v %+v", err, cenv.Errors)
	}
	// The final stage is the result, not a feed: it runs.
	if _, err := p.ProcessChain(ctx, chain(true)); err != nil {
		t.Fatalf("final-stage vector aggregator refused: %v", err)
	}
	if cenv, err := p.PredictChain(ctx, chain(true)); err != nil || len(cenv.Errors) != 0 {
		t.Fatalf("PredictChain final stage = %v %+v", err, cenv.Errors)
	}
}
