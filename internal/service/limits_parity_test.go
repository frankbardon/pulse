package service

import (
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// limitsRows is the FR-31 cohort size: past parallelDecodeRecordThreshold,
// so the per-segment reducer engages on the single file.
const limitsRows = 200_000

// limitsFields keeps buildWideCohort's named fields plus a pad tail
// narrow enough that the ~60 runs below stay a few seconds.
const limitsFields = 24

// limitsFixture is the on-disk (OsFs: the parallel decoder needs a real
// path) cohort set every parity case reads.
type limitsFixture struct {
	cfg    *fs.Config
	schema *encoding.Schema
	// wide is the 200,000-row buildWideCohort file; arch the same rows
	// as a two-shard archive whose shards each carry 10 of the 16
	// brands (A: 0-9, B: 6-15) and whose union carries all 16 — so
	// MaxGroups=12 passes every partition and only the merge can trip;
	// lookup a 16-row cohort keyed on brand.
	wide, arch, lookup string
	// repeat is a 32-row cohort whose brand key repeats (every brand
	// twice — each of the 16 dictionary entries occurs, so the
	// dictionary bound right - D + 1 holds); single a 1-row cohort (a
	// provably unique join key).
	repeat, single string

	joinMu        sync.Mutex
	joinEstimates map[string]int64
}

func writeCohortFile(t testing.TB, path string, schema *encoding.Schema, payload []byte) {
	t.Helper()
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatal(err)
	}
	buf.Write(payload)
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func buildLimitsFixture(t testing.TB, rows, fields int) *limitsFixture {
	t.Helper()
	dir := t.TempDir()
	schema, payload := buildWideCohort(t, fields, rows)
	f := &limitsFixture{
		schema: schema,
		wide:   filepath.Join(dir, "wide.pulse"),
		arch:   filepath.Join(dir, "arch.pulse"),
		lookup: filepath.Join(dir, "lookup.pulse"),
	}
	writeCohortFile(t, f.wide, schema, payload)
	ls, lp := buildWideCohort(t, 10, 16)
	writeCohortFile(t, f.lookup, ls, lp)
	f.repeat, f.single = filepath.Join(dir, "repeat.pulse"), filepath.Join(dir, "single.pulse")
	rs, rp := buildWideCohort(t, 10, 32)
	writeCohortFile(t, f.repeat, rs, rp)
	ss, sp := buildWideCohort(t, 10, 1)
	writeCohortFile(t, f.single, ss, sp)

	// brand is field 0 (byte 0 of every row) and cycles 0..15.
	stride := schema.RecordByteSize()
	var a, b []byte
	for r := 0; r < rows; r++ {
		row := payload[r*stride : (r+1)*stride]
		brand := int(row[0])
		switch {
		case brand < 6:
			a = append(a, row...)
		case brand >= 10:
			b = append(b, row...)
		case r < rows/2:
			a = append(a, row...)
		default:
			b = append(b, row...)
		}
	}
	shardA, shardB := filepath.Join(dir, "a.pulse"), filepath.Join(dir, "b.pulse")
	writeCohortFile(t, shardA, schema, a)
	writeCohortFile(t, shardB, schema, b)

	cfg, err := fs.New(fs.WithFs(afero.NewOsFs()), fs.WithDataDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	f.cfg = cfg
	if _, err := New(cfg).CreateShardArchive(context.Background(), f.arch, []string{shardA, shardB}); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	return f
}

// parityHost is the entry a case runs through, each predicted by its
// own root: a Request by Predict, a Compose by PredictCompose (the slot
// count, then every slot's Request findings), a ProcessChain by
// PredictChain (the stage count, then every stage's) and a rich facet
// by PredictFacet (its group bound and memory estimate). Only
// RequestTimeout is unknowable at predict.
type parityHost int

const (
	hostRequest parityHost = iota
	hostCompose
	hostChain
	hostFacet
)

func (h parityHost) unknowable(n limits.Name) bool {
	return n == limits.RequestTimeout
}

// parityCase is one limit configuration on one runtime site.
type parityCase struct {
	name string
	host parityHost
	// site names the runtime enforcement site the case's trip proves.
	site string
	// setup configures the service arm (workers, fusion, hooks) and
	// asserts the arm is the one the site names.
	setup func(t *testing.T, svc *Service)
	// requests are what predict sees on the Request host. compose,
	// chain and facet are the other hosts' root requests, judged by
	// their own predict root.
	requests func() []*types.Request
	compose  func() *types.ComposedRequest
	chain    func() *types.ChainRequest
	facet    func() *types.FacetRequest
	run      func(ctx context.Context, svc *Service) error
	limits   func(l *limits.Limits)
	// wantTrip is the limit the runtime must trip ("" = must run).
	wantTrip limits.Name
}

// limitTrip is a PULSE_LIMIT_EXCEEDED raise's comparable part; at is
// its location on a multi-request root ("request=1", "stage=0"; "" when
// unlocated).
type limitTrip struct {
	limit      limits.Name
	configured int64
	observed   int64
	at         string
}

// locationOf is the "key=idx" location a Compose slot's or chain
// stage's details carry ("" when none).
func locationOf(details map[string]any) string {
	for _, key := range []string{"request", "stage", "fit"} {
		if v, ok := details[key]; ok {
			return key + "=" + strconv.Itoa(v.(int))
		}
	}
	return ""
}

// tripOf finds the PULSE_LIMIT_EXCEEDED anywhere in err's chain (a
// Compose slot or chain stage may wrap it).
func tripOf(t *testing.T, err error) (limitTrip, bool) {
	t.Helper()
	for e := err; e != nil; e = stderrors.Unwrap(e) {
		ce, ok := e.(*errors.CodedError)
		if !ok || ce.Code != errors.PULSE_LIMIT_EXCEEDED {
			continue
		}
		name, _ := ce.Details["limit"].(string)
		configured, ok1 := ce.Details["configured"].(int64)
		observed, ok2 := ce.Details["observed"].(int64)
		if name == "" || !ok1 || !ok2 {
			t.Fatalf("malformed limit error details %v", ce.Details)
		}
		return limitTrip{limits.Name(name), configured, observed, locationOf(ce.Details)}, true
	}
	return limitTrip{}, false
}

// predictFindings is what predict reports for c under l: the host's
// own predict root (the facade's body — descx.Predict,
// ValidateComposeExpanded, ValidateChainWithOptions,
// ValidateFacetWithOptions) with the instance snapshot carrying l and
// the loaders and counters the facade wires.
func predictFindings(t *testing.T, svc *Service, c parityCase, l limits.Limits) []descriptor.LimitFinding {
	t.Helper()
	ctx := context.Background()
	opts := &descx.PredictOptions{
		Instance:   svc.InstanceSnapshot().WithLimits(l),
		Extensions: svc.ExtensionsSnapshot(),
		SchemaLoader: func(path string) (*encoding.Schema, error) {
			co, err := svc.Open(ctx, path)
			if err != nil {
				return nil, err
			}
			return co.Schema(), nil
		},
		RecordCounter: func(path string) (int64, error) {
			n, err := svc.CountRecords(ctx, path)
			return int64(n), err
		},
		DisableCrosstabFusion: svc.CrosstabFusionDisabled(),
		ShardWorkers:          svc.ShardWorkers(),
	}
	read := func(cohort *types.Cohort) *bytes.Reader {
		data, err := os.ReadFile(resolveCohortPath(cohort))
		if err != nil {
			t.Fatal(err)
		}
		return bytes.NewReader(data)
	}
	switch c.host {
	case hostCompose:
		opts.CohortCounter = func(path string) (descx.CohortCounts, error) { return svc.CohortCounts(ctx, path) }
		env, _ := descx.ValidateComposeExpanded(c.compose(), opts)
		return env.Data.(*descx.ComposeValidationResult).LimitFindings
	case hostChain:
		req := c.chain()
		env := descx.ValidateChainWithOptions(read(req.Cohort), req, opts)
		return env.Data.(*descx.ChainValidationResult).LimitFindings
	case hostFacet:
		req := c.facet()
		env := descx.ValidateFacetWithOptions(read(req.Cohort), req, opts)
		return env.Data.(*descx.FacetValidationResult).LimitFindings
	}
	var out []descriptor.LimitFinding
	for _, req := range c.requests() {
		env := descx.Predict(read(req.Cohort), req, opts)
		res, ok := env.Data.(*descriptor.PredictResult)
		if !ok {
			t.Fatalf("predict returned %T (errors %v)", env.Data, env.Errors)
		}
		out = append(out, res.LimitFindings...)
	}
	return out
}

func wideCohort(f *limitsFixture) *types.Cohort { return &types.Cohort{Filename: f.wide} }

var (
	parityBrand   = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "brand"}}
	paritySum     = []*types.Aggregation{{Type: types.AGG_SUM, Field: "weight", Label: "s"}}
	parityMedian  = []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "weight", Label: "m"}}
	parityCrosstb = func() *types.CrosstabSpec {
		return &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "brand"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cardFeeling"}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "weight", Label: "s"},
			Shape:   types.CrosstabShapeMatrix,
		}
	}
)

func processRun(req func() *types.Request) func(ctx context.Context, svc *Service) error {
	return func(ctx context.Context, svc *Service) error {
		resp, err := svc.Process(ctx, req())
		if err != nil && resp != nil {
			return stderrors.New("partial response beside an error")
		}
		return err
	}
}

// discardSink is a MaterializeSink that keeps nothing.
type discardSink struct{}

func (discardSink) Begin(*Cohort, []descx.MaterializeColumn) error { return nil }
func (discardSink) Row(int64, []any) error                         { return nil }

func materializeRun(req func() *types.Request) func(ctx context.Context, svc *Service) error {
	return func(ctx context.Context, svc *Service) error {
		return svc.Materialize(ctx, req(), discardSink{})
	}
}

func one(req func() *types.Request) func() []*types.Request {
	return func() []*types.Request { return []*types.Request{req()} }
}

func serialArm(t *testing.T, svc *Service) {
	svc.SetDecodeWorkers(1)
	svc.SetShardWorkers(1)
}

// joinCertainEstimate is the memory pre-flight's figure for the join
// req (the observed of its refusal at a 1-byte limit: one match per
// left row), cached per right side and aggregation.
func (f *limitsFixture) joinCertainEstimate(req func() *types.Request) int64 {
	r := req()
	f.joinMu.Lock()
	defer f.joinMu.Unlock()
	key := r.Joins[0].Right + "/" + r.Aggregations[0].Label
	if n, ok := f.joinEstimates[key]; ok {
		return n
	}
	svc := New(f.cfg)
	l := limits.Defaults()
	l.MaxEstimatedMemory = 1
	svc.SetLimits(l)
	_, err := svc.Process(context.Background(), r)
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_LIMIT_EXCEEDED {
		panic(fmt.Sprintf("join estimate probe: %v", err))
	}
	if f.joinEstimates == nil {
		f.joinEstimates = map[string]int64{}
	}
	n := ce.Details["observed"].(int64)
	f.joinEstimates[key] = n
	return n
}

// parityCases is the site matrix: every runtime enforcement site
// (FR-31) with a limit tight enough to trip it, plus no-trip controls.
func parityCases(f *limitsFixture) []parityCase {
	grouped := func(aggs []*types.Aggregation, cohort string) func() *types.Request {
		return func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Groups: parityBrand, Aggregations: aggs}
		}
	}
	maxGroups := func(n int64) func(*limits.Limits) { return func(l *limits.Limits) { l.MaxGroups = n } }
	crosstab := func() *types.Request { return &types.Request{Cohort: wideCohort(f), Crosstab: parityCrosstb()} }
	filteredCrosstab := func() *types.Request {
		r := crosstab()
		r.Filterers = []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "brand", Values: []string{"brand_00", "brand_01", "brand_02", "brand_03"}}}
		return r
	}
	join := func() *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: f.lookup}, Aggregations: paritySum,
			Joins: []*types.JoinSpec{{Right: f.wide, Kind: "inner", As: "r_", On: []types.OnPair{{LeftField: "brand", RightField: "brand"}}}}}
	}
	joinOn := func(right string, aggs []*types.Aggregation) func() *types.Request {
		return func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: f.lookup}, Aggregations: aggs,
				Joins: []*types.JoinSpec{{Right: right, Kind: "inner", As: "r_", On: []types.OnPair{{LeftField: "brand", RightField: "brand"}}}}}
		}
	}
	joinFanOut, joinUnique := joinOn(f.repeat, parityMedian), joinOn(f.single, parityMedian)
	// A chain stage must be mergeable: a distinct count's per-value
	// state scales with the joined rows instead of the buffered records.
	joinFanOutDistinct := joinOn(f.repeat, []*types.Aggregation{{Type: types.AGG_DISTINCT_COUNT, Field: "weight", Label: "d"}})
	// memAt sets MaxEstimatedMemory to the join req's certain estimate
	// (the pre-flight's figure, one match per left row) plus delta.
	memAt := func(req func() *types.Request, delta int64) func(*limits.Limits) {
		return func(l *limits.Limits) { l.MaxEstimatedMemory = f.joinCertainEstimate(req) + delta }
	}
	matrix := func() *types.Request {
		return &types.Request{Cohort: wideCohort(f), Aggregations: paritySum,
			Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Name: "c", Fields: []string{"weight", "pad_014", "pad_015"}}}}
	}
	groupedMatrix := func() *types.Request {
		r := matrix()
		r.Groups = parityBrand
		return r
	}
	shardFanOut := func(t *testing.T, svc *Service) {
		svc.SetShardWorkers(2)
		cohort, err := svc.Open(context.Background(), f.arch)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := svc.shouldFanOut(grouped(paritySum, f.arch)(), cohort); !ok {
			t.Fatal("the shard reducer would not fan out")
		}
	}
	two := func(second func() *types.Request) func() []*types.Request {
		return func() []*types.Request {
			return []*types.Request{{Cohort: wideCohort(f), Aggregations: paritySum}, second()}
		}
	}
	composed := func(reqs func() []*types.Request) *types.ComposedRequest {
		return &types.ComposedRequest{Requests: reqs()}
	}
	chain := func(stages int, stage0 func() *types.Request) *types.ChainRequest {
		s0 := stage0()
		out := &types.ChainRequest{Cohort: s0.Cohort, Stages: []*types.ChainStage{{Request: s0}}}
		for i := 1; i < stages; i++ {
			out.Stages = append(out.Stages, &types.ChainStage{Request: &types.Request{
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "brand"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "s", Label: "s"}},
			}})
		}
		return out
	}
	// Three derived columns from ONE for_each item: the count the
	// expansion's preflight checks before rendering.
	fanOut := func() *types.Request {
		return &types.Request{Cohort: wideCohort(f), Aggregations: paritySum,
			Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Field: "weight", Label: "w{{k}}", Expression: "weight * 2",
				ForEach: &types.ForEach{Axes: []types.SweepAxis{{Name: "k", Values: []any{1, 2, 3}}}}}}}
	}
	// A sorted materialize: the pre-flight predict reports is the one
	// it trips, before the sink opens anything.
	materializeSorted := func() *types.Request {
		return &types.Request{Cohort: wideCohort(f),
			Features: []*types.Feature{{Type: types.FEAT_LOG, Field: "weight"}},
			Sort:     []types.OrderKey{{Field: "brand", Desc: true}, {Field: "LOG_weight"}}}
	}
	facet := func() *types.FacetRequest {
		return &types.FacetRequest{Cohort: wideCohort(f), Fields: []string{"brand", "weight"}}
	}

	return []parityCase{
		{name: "serial grouped", site: "serial", setup: serialArm,
			requests: one(grouped(paritySum, f.wide)), run: processRun(grouped(paritySum, f.wide)),
			limits: maxGroups(8), wantTrip: limits.MaxGroups},
		{name: "buffered grouped", site: "buffered", setup: serialArm,
			requests: one(grouped(parityMedian, f.wide)), run: processRun(grouped(parityMedian, f.wide)),
			limits: maxGroups(8), wantTrip: limits.MaxGroups},
		{name: "parallel decode", site: "parallel decode reducer", setup: func(t *testing.T, svc *Service) {
			svc.SetDecodeWorkers(4)
			if !processing.CanMergeRequest(grouped(paritySum, f.wide)(), f.schema) {
				t.Fatal("request not mergeable: the per-segment reducer would not engage")
			}
			if _, ok := shouldFanOutDecode(4, limitsRows); !ok {
				t.Fatal("shouldFanOutDecode refused the fan-out")
			}
		}, requests: one(grouped(paritySum, f.wide)), run: processRun(grouped(paritySum, f.wide)),
			limits: maxGroups(8), wantTrip: limits.MaxGroups},
		{name: "parallel shards partition", site: "parallel shard reducer", setup: shardFanOut,
			requests: one(grouped(paritySum, f.arch)), run: processRun(grouped(paritySum, f.arch)),
			limits: maxGroups(8), wantTrip: limits.MaxGroups},
		{name: "parallel shards merge", site: "merge", setup: shardFanOut,
			requests: one(grouped(paritySum, f.arch)), run: processRun(grouped(paritySum, f.arch)),
			limits: maxGroups(12), wantTrip: limits.MaxGroups},
		{name: "serial shards", site: "serial (archive)", setup: serialArm,
			requests: one(grouped(paritySum, f.arch)), run: processRun(grouped(paritySum, f.arch)),
			limits: maxGroups(12), wantTrip: limits.MaxGroups},
		{name: "fused crosstab", site: "fused crosstab", setup: func(t *testing.T, svc *Service) {
			serialArm(t, svc)
			if fused, reasons := descx.CrosstabFusion(crosstab(), f.schema, nil, nil); !fused {
				t.Fatalf("crosstab does not fuse: %v", reasons)
			}
		}, requests: one(crosstab), run: processRun(crosstab),
			limits: func(l *limits.Limits) { l.MaxCrosstabCells = 10 }, wantTrip: limits.MaxCrosstabCells},
		{name: "buffered crosstab", site: "buffered crosstab", setup: func(t *testing.T, svc *Service) {
			serialArm(t, svc)
			svc.SetDisableCrosstabFusion(true)
		}, requests: one(crosstab), run: processRun(crosstab),
			limits: func(l *limits.Limits) { l.MaxCrosstabCells = 10 }, wantTrip: limits.MaxCrosstabCells},
		// The 16 x 8 dictionary product is the filter-blind bound; four
		// brands reach a 4 x 4 grid, so a POSSIBLE finding that never
		// trips.
		{name: "crosstab upper bound", site: "fused crosstab", setup: serialArm,
			requests: one(filteredCrosstab), run: processRun(filteredCrosstab),
			limits: func(l *limits.Limits) { l.MaxCrosstabCells = 100 }},
		{name: "join build", site: "join", setup: serialArm,
			requests: one(join), run: processRun(join),
			limits: func(l *limits.Limits) { l.MaxJoinBuildRows = limitsRows / 2 }, wantTrip: limits.MaxJoinBuildRows},
		{name: "matrix dim", site: "matrix", setup: serialArm,
			requests: one(matrix), run: processRun(matrix),
			limits: func(l *limits.Limits) { l.MaxMatrixDim = 2 }, wantTrip: limits.MaxMatrixDim},
		{name: "grouped matrix", site: "matrix (grouped)", setup: serialArm,
			requests: one(groupedMatrix), run: processRun(groupedMatrix),
			limits: maxGroups(8), wantTrip: limits.MaxGroups},
		{name: "memory buffered", site: "buffered (memory pre-flight)", setup: serialArm,
			requests: one(grouped(parityMedian, f.wide)), run: processRun(grouped(parityMedian, f.wide)),
			limits: func(l *limits.Limits) { l.MaxEstimatedMemory = 1 << 20 }, wantTrip: limits.MaxEstimatedMemory},
		{name: "memory materialize sorted", site: "materialize (memory pre-flight)", setup: serialArm,
			requests: one(materializeSorted), run: materializeRun(materializeSorted),
			limits: func(l *limits.Limits) { l.MaxEstimatedMemory = 1 << 20 }, wantTrip: limits.MaxEstimatedMemory},
		{name: "memory join", site: "join (memory pre-flight)", setup: serialArm,
			requests: one(join), run: processRun(join),
			limits: func(l *limits.Limits) { l.MaxEstimatedMemory = 1 << 20 }, wantTrip: limits.MaxEstimatedMemory},
		// #229: the 16-row lookup joined to repeat (each brand twice).
		// At the certain estimate (one match per left row) predict
		// reports the POSSIBLE fan-out finding (16 x (32 - 16 + 1)
		// joined rows on the dictionary bound) and the run is refused
		// by the build-side backstop on 16 x 2 measured rows; at the
		// possible estimate neither.
		{name: "memory join fan-out", site: "join (build-side memory backstop)", setup: serialArm,
			requests: one(joinFanOut), run: processRun(joinFanOut),
			limits: memAt(joinFanOut, 0), wantTrip: limits.MaxEstimatedMemory},
		{name: "memory join fan-out under the cap", site: "join (build-side memory backstop)", setup: serialArm,
			requests: one(joinFanOut), run: processRun(joinFanOut),
			limits: func(l *limits.Limits) { l.MaxEstimatedMemory = 1 << 40 }},
		// A one-row right side is provably unique: the certain estimate
		// is the whole story, predict and the pre-flight agree.
		{name: "memory join unique", site: "join (memory pre-flight)", setup: serialArm,
			requests: one(joinUnique), run: processRun(joinUnique),
			limits: memAt(joinUnique, -1), wantTrip: limits.MaxEstimatedMemory},
		{name: "compose slot join fan-out", host: hostCompose, site: "compose (slot join backstop)", setup: serialArm,
			compose: func() *types.ComposedRequest { return composed(two(joinFanOut)) },
			run: func(ctx context.Context, svc *Service) error {
				_, err := svc.Compose(ctx, composed(two(joinFanOut)))
				return err
			},
			limits: memAt(joinFanOut, 0), wantTrip: limits.MaxEstimatedMemory},
		{name: "chain stage join fan-out", host: hostChain, site: "chain (stage 0 join backstop)", setup: serialArm,
			chain: func() *types.ChainRequest { return chain(1, joinFanOutDistinct) },
			run: func(ctx context.Context, svc *Service) error {
				_, err := svc.ProcessChain(ctx, chain(1, joinFanOutDistinct))
				return err
			},
			limits: memAt(joinFanOutDistinct, 0), wantTrip: limits.MaxEstimatedMemory},
		{name: "compose slots", host: hostCompose, site: "compose", setup: serialArm,
			compose: func() *types.ComposedRequest { return composed(two(grouped(paritySum, f.wide))) },
			run: func(ctx context.Context, svc *Service) error {
				_, err := svc.Compose(ctx, composed(two(grouped(paritySum, f.wide))))
				return err
			},
			limits: func(l *limits.Limits) { l.MaxComposeSlots = 1 }, wantTrip: limits.MaxComposeSlots},
		{name: "compose slot groups", host: hostCompose, site: "compose (slot)", setup: serialArm,
			compose: func() *types.ComposedRequest { return composed(two(grouped(paritySum, f.wide))) },
			run: func(ctx context.Context, svc *Service) error {
				_, err := svc.Compose(ctx, composed(two(grouped(paritySum, f.wide))))
				return err
			},
			limits: maxGroups(8), wantTrip: limits.MaxGroups},
		{name: "compose parallel slots", host: hostCompose, site: "compose (parallel)", setup: serialArm,
			compose: func() *types.ComposedRequest { return composed(two(grouped(paritySum, f.wide))) },
			run: func(ctx context.Context, svc *Service) error {
				_, err := svc.ComposeParallel(ctx, composed(two(grouped(paritySum, f.wide))), ComposeOptions{FailFast: true})
				return err
			},
			limits: func(l *limits.Limits) { l.MaxComposeSlots = 1 }, wantTrip: limits.MaxComposeSlots},
		{name: "compose parallel slot groups", host: hostCompose, site: "compose (parallel slot)", setup: serialArm,
			compose: func() *types.ComposedRequest { return composed(two(grouped(paritySum, f.wide))) },
			run: func(ctx context.Context, svc *Service) error {
				_, err := svc.ComposeParallel(ctx, composed(two(grouped(paritySum, f.wide))), ComposeOptions{FailFast: true})
				return err
			},
			limits: maxGroups(8), wantTrip: limits.MaxGroups},
		{name: "chain stages", host: hostChain, site: "chain", setup: serialArm,
			chain: func() *types.ChainRequest { return chain(2, grouped(paritySum, f.wide)) },
			run: func(ctx context.Context, svc *Service) error {
				_, err := svc.ProcessChain(ctx, chain(2, grouped(paritySum, f.wide)))
				return err
			},
			limits: func(l *limits.Limits) { l.MaxChainStages = 1 }, wantTrip: limits.MaxChainStages},
		{name: "chain stage groups", host: hostChain, site: "chain (stage 0)", setup: serialArm,
			chain: func() *types.ChainRequest { return chain(2, grouped(paritySum, f.wide)) },
			run: func(ctx context.Context, svc *Service) error {
				_, err := svc.ProcessChain(ctx, chain(2, grouped(paritySum, f.wide)))
				return err
			},
			limits: maxGroups(8), wantTrip: limits.MaxGroups},
		// A slot's / stage 0's memory estimate is certain on its
		// cohort's header count: PredictCompose / PredictChain report
		// the exact figure the slot's (stage's) Process refuses with.
		{name: "compose slot memory", host: hostCompose, site: "compose (slot memory pre-flight)", setup: serialArm,
			compose: func() *types.ComposedRequest { return composed(two(grouped(parityMedian, f.wide))) },
			run: func(ctx context.Context, svc *Service) error {
				_, err := svc.Compose(ctx, composed(two(grouped(parityMedian, f.wide))))
				return err
			},
			limits: func(l *limits.Limits) { l.MaxEstimatedMemory = 1 << 20 }, wantTrip: limits.MaxEstimatedMemory},
		{name: "compose archive slot memory", host: hostCompose, site: "compose (archive slot memory pre-flight)", setup: serialArm,
			compose: func() *types.ComposedRequest {
				return composed(func() []*types.Request { return []*types.Request{grouped(parityMedian, f.arch)()} })
			},
			run: func(ctx context.Context, svc *Service) error {
				_, err := svc.Compose(ctx, composed(func() []*types.Request { return []*types.Request{grouped(parityMedian, f.arch)()} }))
				return err
			},
			limits: func(l *limits.Limits) { l.MaxEstimatedMemory = 1 << 20 }, wantTrip: limits.MaxEstimatedMemory},
		{name: "chain stage memory", host: hostChain, site: "chain (stage 0 memory pre-flight)", setup: serialArm,
			chain: func() *types.ChainRequest { return chain(2, grouped(paritySum, f.wide)) },
			run: func(ctx context.Context, svc *Service) error {
				_, err := svc.ProcessChain(ctx, chain(2, grouped(paritySum, f.wide)))
				return err
			},
			limits: func(l *limits.Limits) { l.MaxEstimatedMemory = 1 << 20 }, wantTrip: limits.MaxEstimatedMemory},
		{name: "derived columns", site: "derived columns", setup: serialArm,
			requests: one(fanOut), run: processRun(fanOut),
			limits: func(l *limits.Limits) { l.MaxDerivedColumns = 2 }, wantTrip: limits.MaxDerivedColumns},
		{name: "facet groups", host: hostFacet, site: "facet", setup: serialArm, facet: facet,
			run: func(ctx context.Context, svc *Service) error {
				_, err := svc.FacetSchema(ctx, facet())
				return err
			},
			limits: maxGroups(8), wantTrip: limits.MaxGroups},
		{name: "facet memory", host: hostFacet, site: "facet (memory pre-flight)", setup: serialArm, facet: facet,
			run: func(ctx context.Context, svc *Service) error {
				_, err := svc.FacetSchema(ctx, facet())
				return err
			},
			limits: func(l *limits.Limits) { l.MaxEstimatedMemory = 1 << 10 }, wantTrip: limits.MaxEstimatedMemory},
		{name: "timeout", site: "timeout", setup: func(t *testing.T, svc *Service) {
			serialArm(t, svc)
			svc.entryHook = waitOutShortDeadlines
		}, requests: one(grouped(paritySum, f.wide)), run: processRun(grouped(paritySum, f.wide)),
			limits: func(l *limits.Limits) { l.RequestTimeout = time.Millisecond }, wantTrip: limits.RequestTimeout},
	}
}

// checkParity runs c under l and asserts the FR-31 rules, returning
// the findings and the trip (nil when the run succeeded).
func checkParity(t *testing.T, f *limitsFixture, c parityCase, l limits.Limits) ([]descriptor.LimitFinding, *limitTrip) {
	t.Helper()
	svc := New(f.cfg)
	c.setup(t, svc)
	svc.SetLimits(l)
	findings := predictFindings(t, svc, c, l)

	err := c.run(context.Background(), svc)
	trip, tripped := tripOf(t, err)
	if err != nil && !tripped {
		t.Fatalf("run failed without a limit trip: %v", err)
	}

	var certain []descriptor.LimitFinding
	for _, fd := range findings {
		if fd.Grade == descriptor.LimitGradeCertain {
			certain = append(certain, fd)
		}
	}
	// Every certain finding trips — with one of the certain figures, at
	// the same location (a Compose slot's details.request, a chain
	// stage's details.stage).
	if len(certain) > 0 {
		if !tripped {
			t.Fatalf("predict reported certain %+v but the run succeeded", certain)
		}
		matched := false
		for _, fd := range certain {
			if string(trip.limit) == fd.Limit && trip.configured == fd.Configured && trip.at == locationOf(fd.Details) {
				matched = true
				if trip.observed != fd.Estimated {
					t.Fatalf("certain %s: runtime observed %d, predict estimated %d", fd.Limit, trip.observed, fd.Estimated)
				}
			}
		}
		if !matched {
			t.Fatalf("runtime tripped %+v; predict's certain findings were %+v", trip, certain)
		}
	}
	if !tripped {
		return findings, nil
	}
	// Every trip was certain, possible or unknowable at predict.
	if c.host.unknowable(trip.limit) {
		return findings, &trip
	}
	for _, fd := range findings {
		if string(trip.limit) == fd.Limit && trip.configured == fd.Configured {
			return findings, &trip
		}
	}
	// No matching finding: a predictable limit tripped behind
	// predict's back (and with no findings at all, a request predict
	// cleared tripped).
	t.Fatalf("runtime tripped predictable %+v that predict never reported (findings %+v)", trip, findings)
	return nil, nil
}

// TestLimits_JoinFanOut_PossibleAndBackstop pins the #229 join cases of
// the parity matrix beyond limit + configured: at the certain estimate
// predict's only finding is POSSIBLE with the dictionary bound
// (16 x (32 - 16 + 1) joined rows) in its details — beside
// details.request / details.stage on Compose and the chain — and the
// runtime refusal carries the measured multiplicity (each brand twice)
// at the same location.
func TestLimits_JoinFanOut_PossibleAndBackstop(t *testing.T) {
	t.Parallel()

	f := buildLimitsFixture(t, 16, limitsFields)
	byName := map[string]parityCase{}
	for _, c := range parityCases(f) {
		byName[c.name] = c
	}
	for name, at := range map[string]string{
		"memory join fan-out":       "",
		"compose slot join fan-out": "request=1",
		"chain stage join fan-out":  "stage=0",
	} {
		t.Run(name, func(t *testing.T) {
			c := byName[name]
			svc := New(f.cfg)
			c.setup(t, svc)
			l := limits.Defaults()
			c.limits(&l)
			svc.SetLimits(l)
			fs := predictFindings(t, svc, c, l)
			if len(fs) != 1 {
				t.Fatalf("findings %+v, want one", fs)
			}
			fd := fs[0]
			if fd.Grade != descriptor.LimitGradePossible || fd.Limit != string(limits.MaxEstimatedMemory) || locationOf(fd.Details) != at ||
				fd.Details["join"] != 0 || fd.Details["join_bound"] != limits.JoinBoundDictionary || fd.Details["join_rows_bound"] != int64(16*17) {
				t.Fatalf("finding %+v", fd)
			}
			err := c.run(context.Background(), svc)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_LIMIT_EXCEEDED {
				t.Fatalf("run: %v", err)
			}
			if locationOf(ce.Details) != at || ce.Details["join"] != 0 || ce.Details["right_key_multiplicity"] != int64(2) ||
				ce.Details["join_rows_bound"] != int64(32) || ce.Details["configured"] != fd.Configured {
				t.Fatalf("refusal details %v", ce.Details)
			}
			if obs := ce.Details["observed"].(int64); obs <= fd.Configured || obs > fd.Estimated {
				t.Fatalf("backstop observed %d outside (configured %d, possible estimate %d]", obs, fd.Configured, fd.Estimated)
			}
		})
	}
}
