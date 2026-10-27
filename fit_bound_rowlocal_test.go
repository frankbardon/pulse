package pulse

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"strconv"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// fit_bound_rowlocal_test.go pins U30 E2-S5 at the facade: a fitted
// vector attribute bound to a prebuilt fit (params.fit — a `populations`
// load or a registered population) is ROW-LOCAL. It scores every row to
// the bit of the two-pass fit_on run over the same fit rows, grouped or
// not, through Process, ProcessStream, the shard reducer (its fit
// warnings raised once) and a ProcessChain stage (admitted, where the
// fit_on twin is PULSE_CHAIN_NOT_MERGEABLE on predict and runtime
// alike); a Compose vector fit whose battery is a bound attribute
// captures once however many shards run it.

const (
	fbAttrPop = "pops/attr" + model.PopulationSuffix
	fbTinyPop = "pops/tiny" + model.PopulationSuffix
	fbArchive = "vecfit_archive.pulse"
)

var fbColumns = []string{"week", "a", "b", "c", "region"}

// fbRows are vecFacadeFS's rows lo..hi-1 (the same generator), so a
// shard archive of consecutive slices is the facade cohort split.
func fbRows(lo, hi int) [][]string {
	var rows [][]string
	for i := lo; i < hi; i++ {
		a := float64((i*37)%17) + 0.25*float64(i%3)
		b := 0.5*a + float64((i*11)%9)
		c := -0.3*a + 0.7*b + float64((i*7)%5)
		rows = append(rows, []string{strconv.Itoa(i), jsonNumber(a), jsonNumber(b), jsonNumber(c), []string{"north", "south"}[i%2]})
	}
	return rows
}

// fbWeekRange is the fit rows' predicate: weeks lo..hi.
func fbWeekRange(lo, hi int) *types.Filterer {
	return &types.Filterer{Type: types.FILTER_RANGE, Field: "week", Values: []string{strconv.Itoa(lo), strconv.Itoa(hi)}}
}

// fitBoundFS is populationFacadeFS plus a three-shard archive of the
// facade cohort and two populations captured from an ATTR_MAHALANOBIS
// battery over a / b / c: "attr" (weeks 0–44) and "tiny" (weeks 0–2:
// N* = p, so every bound score is null with a fit warning).
func fitBoundFS(t *testing.T) afero.Fs {
	t.Helper()
	fsys := populationFacadeFS(t)
	var shards []string
	for k := 0; k < 3; k++ {
		path := fmt.Sprintf("vecfit_shard%d.pulse", k)
		createTestPulseFile(t, fsys, path, fbColumns, fbRows(k*30, (k+1)*30))
		shards = append(shards, path)
	}
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := p.CreateShardArchive(ctx, fbArchive, shards); err != nil {
		t.Fatal(err)
	}
	battery := func(hi int) *Request {
		r := vecFacadeRequest("")
		r.Filterers = []*types.Filterer{fbWeekRange(0, hi)}
		r.Attributes = []*types.Attribute{{Type: types.ATTR_MAHALANOBIS, Label: "d", Fields: []string{"a", "b", "c"}}}
		r.Aggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "a", Label: "n"}}
		return r
	}
	if _, err := p.Compose(ctx, &ComposedRequest{
		Fits: []types.FitSpec{
			{Name: "attr", Request: battery(44), Save: fbAttrPop},
			{Name: "tiny", Request: battery(2), Save: fbTinyPop},
		},
		Requests: []*Request{vecFacadeMatrix(types.MAT_COVARIANCE, "")},
	}); err != nil {
		t.Fatal(err)
	}
	return fsys
}

// fbOperators are the fitted slots a bound population scores: params
// without the fit source.
var fbOperators = []struct {
	typ    types.AttributeType
	params map[string]any
}{
	{types.ATTR_MAHALANOBIS, map[string]any{}},
	{types.ATTR_MAHALANOBIS, map[string]any{"output": "p"}},
	{types.ATTR_PC_SCORE, map[string]any{"component": 2}},
	{types.ATTR_FACTOR_SCORE, map[string]any{"factor": 1, "factors": 1}},
	{types.ATTR_VEC_DISTANCE, map[string]any{"metric": "mahalanobis", "reference": []float64{8, 9, 10}}},
	{types.ATTR_VEC_DISTANCE, map[string]any{"metric": "std_euclidean", "reference": []float64{8, 9, 10}}},
}

// fbScoring is a request over cohort scoring typ over a / b / c into
// "d" with params plus source (params.fit, or the fit_on twin), and
// order-free exact aggregations of d (sum included: both arms fold the
// rows in record order).
func fbScoring(cohort string, typ types.AttributeType, params map[string]any, source map[string]any, pops ...types.PopulationRef) *Request {
	all := map[string]any{}
	for k, v := range params {
		all[k] = v
	}
	for k, v := range source {
		all[k] = v
	}
	raw, _ := json.Marshal(all)
	r := &Request{Cohort: &types.Cohort{Filename: cohort}, Populations: pops}
	r.Attributes = []*types.Attribute{{Type: typ, Label: "d", Fields: []string{"a", "b", "c"}, Params: raw}}
	r.Aggregations = []*types.Aggregation{
		{Type: types.AGG_COUNT, Field: "d", Label: "n_d"},
		{Type: types.AGG_MIN, Field: "d", Label: "min_d"},
		{Type: types.AGG_MAX, Field: "d", Label: "max_d"},
		{Type: types.AGG_SUM, Field: "d", Label: "sum_d"},
	}
	return r
}

func fbBound(name string) map[string]any { return map[string]any{"fit": name} }

func fbFitOn() map[string]any {
	return map[string]any{"fit_on": []*types.Filterer{fbWeekRange(0, 44)}}
}

func fbGrouped(r *Request) *Request {
	r.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
	return r
}

// fbWire is a response's data and warnings — what must not move. A
// bound fit's warnings name it (details.fit); the fit_on twin's cannot,
// so that one key is dropped before comparing.
func fbWire(t *testing.T, resp *Response) string {
	t.Helper()
	warns := make([]map[string]any, len(resp.Warnings))
	for i, w := range resp.Warnings {
		d := map[string]any{}
		for k, v := range w.Details {
			if k != "fit" {
				d[k] = v
			}
		}
		warns[i] = map[string]any{"code": w.Code, "message": w.Message, "details": d}
	}
	b, err := types.MarshalFinite(map[string]any{"data": resp.Data, "warnings": warns})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestFitBound_MatchesTwoPassFit: every fitted operator bound to a
// population (a `populations` load, or the same file registered) scores
// to the bit of its two-pass fit_on twin over the same fit rows — the
// aggregations of every row's score and the warnings byte-identical —
// ungrouped and grouped (where the fit_on twin runs buffered and the
// bound slot streams), through Process and ProcessStream; predict calls
// the bound request streamable on both shapes and the fit_on twin
// buffered when grouped.
func TestFitBound_MatchesTwoPassFit(t *testing.T) {
	t.Parallel()

	fsys := fitBoundFS(t)
	ctx := context.Background()
	plain, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := afero.ReadFile(fsys, fbAttrPop)
	if err != nil {
		t.Fatal(err)
	}
	registered, err := New(Options{FS: fsys, Extensions: Extensions{Populations: map[string]Population{"attr": {File: raw}}}})
	if err != nil {
		t.Fatal(err)
	}
	cohort, err := afero.ReadFile(fsys, vecFacadeCohort)
	if err != nil {
		t.Fatal(err)
	}
	load := popRef("attr", fbAttrPop)
	for _, op := range fbOperators {
		for _, grouped := range []bool{false, true} {
			shape := func(r *Request) *Request {
				if grouped {
					return fbGrouped(r)
				}
				return r
			}
			name := fmt.Sprintf("%s/%v/grouped=%v", op.typ, op.params, grouped)
			t.Run(name, func(t *testing.T) {
				twin, err := plain.Process(ctx, shape(fbScoring(vecFacadeCohort, op.typ, op.params, fbFitOn())))
				if err != nil {
					t.Fatal(err)
				}
				want := fbWire(t, twin)
				arms := map[string]func() (*Response, error){
					"loaded": func() (*Response, error) {
						return plain.Process(ctx, shape(fbScoring(vecFacadeCohort, op.typ, op.params, fbBound("attr"), load)))
					},
					"registered": func() (*Response, error) {
						return registered.Process(ctx, shape(fbScoring(vecFacadeCohort, op.typ, op.params, fbBound("attr"))))
					},
				}
				for arm, run := range arms {
					got, err := run()
					if err != nil {
						t.Fatalf("%s: %v", arm, err)
					}
					if g := fbWire(t, got); g != want {
						t.Fatalf("%s: bound scores differ from the two-pass fit_on run\n got %s\nwant %s", arm, g, want)
					}
				}
				// ProcessStream binds the same models (request populations
				// and registered ones) and yields the same rows.
				for arm, stream := range map[string]func() (RowIter, error){
					"loaded": func() (RowIter, error) {
						return plain.ProcessStream(ctx, shape(fbScoring(vecFacadeCohort, op.typ, op.params, fbBound("attr"), load)))
					},
					"registered": func() (RowIter, error) {
						return registered.ProcessStream(ctx, shape(fbScoring(vecFacadeCohort, op.typ, op.params, fbBound("attr"))))
					},
				} {
					iter, err := stream()
					if err != nil {
						t.Fatalf("stream %s: %v", arm, err)
					}
					var rows []Row
					for {
						row, ok, err := iter.Next(ctx)
						if err != nil {
							t.Fatal(err)
						}
						if !ok {
							break
						}
						rows = append(rows, row)
					}
					_ = iter.Close()
					got, _ := json.Marshal(rows)
					wantRows, _ := json.Marshal(twin.Data)
					if !bytes.Equal(got, wantRows) {
						t.Fatalf("stream %s rows differ from the two-pass run\n got %s\nwant %s", arm, got, wantRows)
					}
				}
				env, err := plain.PredictBytes(ctx, cohort, shape(fbScoring(vecFacadeCohort, op.typ, op.params, fbBound("attr"), load)))
				if err != nil {
					t.Fatal(err)
				}
				if len(env.Errors) != 0 || !env.Data.(*descriptor.PredictResult).Streamable {
					t.Fatalf("predict on the bound request: errors %+v, streamable %v", env.Errors, env.Data.(*descriptor.PredictResult).Streamable)
				}
				env, err = plain.PredictBytes(ctx, cohort, shape(fbScoring(vecFacadeCohort, op.typ, op.params, fbFitOn())))
				if err != nil {
					t.Fatal(err)
				}
				if got := env.Data.(*descriptor.PredictResult).Streamable; got == grouped {
					t.Fatalf("predict on the fit_on twin: streamable %v (grouped %v)", got, grouped)
				}
			})
		}
	}
}

// TestFitBound_ShardReducer: a bound fitted attribute merges, so a shard
// archive runs it on the per-shard reducer (ShardWorkers 3) to exactly
// the serial archive's result (ShardWorkers 1) — its vector-input slot
// resolved there like every other arm, and a bound fit's warning raised
// ONCE, not once per shard.
func TestFitBound_ShardReducer(t *testing.T) {
	t.Parallel()

	fsys := fitBoundFS(t)
	ctx := context.Background()
	serial, err := New(Options{FS: fsys, ShardWorkers: 1})
	if err != nil {
		t.Fatal(err)
	}
	parallel, err := New(Options{FS: fsys, ShardWorkers: 3})
	if err != nil {
		t.Fatal(err)
	}
	exact := func(r *Request) *Request {
		r.Aggregations = r.Aggregations[:3] // count / min / max: exact under any merge
		return r
	}
	warned := 0
	for _, pop := range []string{"attr", "tiny"} {
		for _, grouped := range []bool{false, true} {
			for _, viaVector := range []bool{false, true} {
				req := func() *Request {
					r := exact(fbScoring(fbArchive, types.ATTR_MAHALANOBIS, nil, fbBound(pop), popRef(pop, "pops/"+pop+model.PopulationSuffix)))
					if viaVector {
						r.Vectors = []types.VectorSpec{{Name: "battery", Fields: []string{"a", "b", "c"}}}
						r.Attributes[0].Fields, r.Attributes[0].Vector = nil, "battery"
					}
					if grouped {
						return fbGrouped(r)
					}
					return r
				}
				want, err := serial.Process(ctx, req())
				if err != nil {
					t.Fatal(err)
				}
				got, err := parallel.Process(ctx, req())
				if err != nil {
					t.Fatalf("%s grouped=%v vector=%v: %v", pop, grouped, viaVector, err)
				}
				if g, w := fbWire(t, got), fbWire(t, want); g != w {
					t.Fatalf("%s grouped=%v vector=%v: shard reducer differs from serial\n got %s\nwant %s", pop, grouped, viaVector, g, w)
				}
				if pop == "tiny" {
					if len(got.Warnings) != 1 || got.Warnings[0].Code != string(perr.PULSE_MATRIX_INSUFFICIENT_N) {
						t.Fatalf("tiny fit: warnings %+v, want one PULSE_MATRIX_INSUFFICIENT_N", got.Warnings)
					}
					warned++
				}
			}
		}
	}
	if warned == 0 {
		t.Fatal("vacuous: no bound fit raised a warning")
	}
}

// TestFitBound_ComposeFitOnShards: a Compose vector fit whose battery is
// a bound fitted attribute, run over a shard archive on the per-shard
// reducer, captures its state exactly once (the reducer's per-shard
// builds never offer), so the batch equals its serial run byte for byte.
func TestFitBound_ComposeFitOnShards(t *testing.T) {
	t.Parallel()

	fsys := fitBoundFS(t)
	ctx := context.Background()
	run := func(workers int) []byte {
		p, err := New(Options{FS: fsys, ShardWorkers: workers})
		if err != nil {
			t.Fatal(err)
		}
		battery := exactBattery()
		c := &ComposedRequest{
			Fits:     []types.FitSpec{{Name: "again", Request: battery}},
			Requests: []*Request{vecFacadeMatrix(types.MAT_COVARIANCE, `{"fit":"again"}`)},
		}
		out, err := p.Compose(ctx, c)
		if err != nil {
			t.Fatalf("ShardWorkers %d: %v", workers, err)
		}
		if out.Fits["again"] == nil || out.Fits["again"].Vector == nil {
			t.Fatalf("ShardWorkers %d: no vector fit bound", workers)
		}
		return responseBytes(t, out)
	}
	if got, want := run(3), run(1); !bytes.Equal(got, want) {
		t.Fatalf("parallel compose differs from serial\n got %s\nwant %s", got, want)
	}
}

// exactBattery is a fit request over the archive whose one battery is
// an ATTR_MAHALANOBIS bound to the "attr" population, mergeable (one
// AGG_COUNT).
func exactBattery() *Request {
	r := fbScoring(fbArchive, types.ATTR_MAHALANOBIS, nil, fbBound("attr"), popRef("attr", fbAttrPop))
	r.Aggregations = r.Aggregations[:1]
	return r
}

// TestFitBound_ChainStage: a ProcessChain stage carrying a bound fitted
// attribute is admitted (row-local) and its stage-0 rows are the plain
// Process rows; predict agrees. The same stage folding its own fit
// (fit_on) is PULSE_CHAIN_NOT_MERGEABLE on predict and runtime alike.
func TestFitBound_ChainStage(t *testing.T) {
	t.Parallel()

	fsys := fitBoundFS(t)
	ctx := context.Background()
	raw, err := afero.ReadFile(fsys, fbAttrPop)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	registered, err := New(Options{FS: fsys, Extensions: Extensions{Populations: map[string]Population{"attr": {File: raw}}}})
	if err != nil {
		t.Fatal(err)
	}
	chain := func(source map[string]any, pops ...types.PopulationRef) *ChainRequest {
		s0 := fbGrouped(fbScoring(vecFacadeCohort, types.ATTR_PC_SCORE, map[string]any{"component": 1}, source, pops...))
		s0.Cohort = nil
		s1 := &Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n_d", Label: "rows"}}}
		return &ChainRequest{
			Cohort: &types.Cohort{Filename: vecFacadeCohort},
			Stages: []*types.ChainStage{{Name: "s0", Request: s0}, {Name: "s1", Request: s1}},
		}
	}
	want, err := plain.Process(ctx, fbGrouped(fbScoring(vecFacadeCohort, types.ATTR_PC_SCORE, map[string]any{"component": 1}, fbBound("attr"), popRef("attr", fbAttrPop))))
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		p *Pulse
		c *ChainRequest
	}{
		"loaded":     {plain, chain(fbBound("attr"), popRef("attr", fbAttrPop))},
		"registered": {registered, chain(fbBound("attr"))},
	} {
		env, err := tc.p.PredictChain(ctx, tc.c)
		if err != nil || len(env.Errors) != 0 {
			t.Fatalf("%s: predict chain %v %+v", name, err, env)
		}
		got, err := tc.p.ProcessChain(ctx, tc.c)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		g, _ := json.Marshal(got.Stages[0].Data)
		w, _ := json.Marshal(want.Data)
		if !bytes.Equal(g, w) {
			t.Fatalf("%s: stage 0 differs from Process\n got %s\nwant %s", name, g, w)
		}
	}
	fitOn := chain(fbFitOn())
	env, err := plain.PredictChain(ctx, fitOn)
	if err != nil {
		t.Fatal(err)
	}
	pe := findEnvError(t, env, perr.PULSE_CHAIN_NOT_MERGEABLE)
	_, rerr := plain.ProcessChain(ctx, fitOn)
	var ce *perr.CodedError
	if !stderrors.As(rerr, &ce) {
		t.Fatalf("fit_on chain: uncoded %v", rerr)
	}
	assertSameRefusal(t, "ProcessChain", rerr, pe)
}

// TestFitBound_CrosstabFuses: a crosstab over a bound fitted attribute
// takes the fused in-decode arm (predict says so) and its cells and
// warnings equal the fit_on twin's, which declines fusion and runs
// buffered.
func TestFitBound_CrosstabFuses(t *testing.T) {
	t.Parallel()

	fsys := fitBoundFS(t)
	ctx := context.Background()
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	cohort, err := afero.ReadFile(fsys, vecFacadeCohort)
	if err != nil {
		t.Fatal(err)
	}
	xtab := func(source map[string]any, pops ...types.PopulationRef) *Request {
		r := fbScoring(vecFacadeCohort, types.ATTR_MAHALANOBIS, nil, source, pops...)
		r.Aggregations = nil
		region := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
		r.Crosstab = &types.CrosstabSpec{Rows: region, Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Cell: &types.Aggregation{Type: types.AGG_SUM, Field: "d", Label: "sum_d"}}
		return r
	}
	bound := xtab(fbBound("attr"), popRef("attr", fbAttrPop))
	for req, fusable := range map[*Request]bool{bound: true, xtab(fbFitOn()): false} {
		env, err := p.PredictBytes(ctx, cohort, req)
		if err != nil {
			t.Fatal(err)
		}
		pr := env.Data.(*descriptor.PredictResult)
		if len(env.Errors) != 0 || pr.CrosstabFusable == nil || *pr.CrosstabFusable != fusable {
			t.Fatalf("predict: errors %+v, fusable %v (reasons %q), want %v", env.Errors, pr.CrosstabFusable, pr.CrosstabFusionReasons, fusable)
		}
	}
	want, err := p.Process(ctx, xtab(fbFitOn()))
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Process(ctx, bound)
	if err != nil {
		t.Fatal(err)
	}
	g, _ := json.Marshal(got.Crosstab)
	w, _ := json.Marshal(want.Crosstab)
	if !bytes.Equal(g, w) || len(got.Warnings) != len(want.Warnings) {
		t.Fatalf("fused bound crosstab differs from the buffered fit_on twin\n got %s %+v\nwant %s %+v", g, got.Warnings, w, want.Warnings)
	}
}
