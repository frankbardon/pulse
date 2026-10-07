package service

import (
	"bytes"
	"context"
	stderrors "errors"
	"os"
	"path/filepath"
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

// parityHost is the entry a case runs through. Each host has a predict
// surface for some limits and none for the rest ("unknowable at
// predict"): a Request has every rule but RequestTimeout; Compose and
// ProcessChain add their own slot / stage count, which no predict
// surface sees (each slot / stage 0 is predicted as a Request); a rich
// facet has only the memory estimate its pre-flight checks.
type parityHost int

const (
	hostRequest parityHost = iota
	hostCompose
	hostChain
	hostFacet
)

func (h parityHost) unknowable(n limits.Name) bool {
	switch n {
	case limits.RequestTimeout:
		return true
	case limits.MaxComposeSlots:
		return h == hostCompose
	case limits.MaxChainStages:
		return h == hostChain
	}
	return h == hostFacet && n != limits.MaxEstimatedMemory
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
	// requests are what predict sees: the Request, each Compose slot,
	// a chain's stage 0 (with the chain cohort). facet is the facet
	// host's request.
	requests func() []*types.Request
	facet    func() *types.FacetRequest
	run      func(ctx context.Context, svc *Service) error
	limits   func(l *limits.Limits)
	// wantTrip is the limit the runtime must trip ("" = must run).
	wantTrip limits.Name
}

// limitTrip is a PULSE_LIMIT_EXCEEDED raise's comparable part.
type limitTrip struct {
	limit      limits.Name
	configured int64
	observed   int64
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
		return limitTrip{limits.Name(name), configured, observed}, true
	}
	return limitTrip{}, false
}

// predictFindings is what predict reports for c under l: descx.Predict
// (the facade's Pulse.Predict body) on each request with the instance
// snapshot carrying l, the header-only record counter and the schema
// loader the facade wires; for a facet, the memory estimate its
// pre-flight checks.
func predictFindings(t *testing.T, svc *Service, c parityCase, l limits.Limits) []descriptor.LimitFinding {
	t.Helper()
	ctx := context.Background()
	if c.host == hostFacet {
		req := c.facet()
		cohort, err := svc.Open(ctx, resolveCohortPath(req.Cohort))
		if err != nil {
			t.Fatal(err)
		}
		in, err := svc.limitInputs(ctx, cohort, resolveCohortPath(req.Cohort))
		if err != nil {
			t.Fatal(err)
		}
		if limits.IsUnlimited(l.MaxEstimatedMemory) {
			return nil
		}
		n, ok := descx.EstimateFacetMemory(req, cohort.Schema(), in)
		if !ok {
			return nil
		}
		if f, ok := limits.Evaluate(l, limits.MaxEstimatedMemory, n, limits.Certain); ok {
			return []descriptor.LimitFinding{{Limit: string(f.Limit), Configured: f.Configured, Estimated: f.Estimated, Grade: descriptor.LimitGrade(f.Grade)}}
		}
		return nil
	}
	var out []descriptor.LimitFinding
	for _, req := range c.requests() {
		data, err := os.ReadFile(resolveCohortPath(req.Cohort))
		if err != nil {
			t.Fatal(err)
		}
		env := descx.Predict(bytes.NewReader(data), req, &descx.PredictOptions{
			Instance: svc.InstanceSnapshot().WithLimits(l),
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
		})
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

func one(req func() *types.Request) func() []*types.Request {
	return func() []*types.Request { return []*types.Request{req()} }
}

func serialArm(t *testing.T, svc *Service) {
	svc.SetDecodeWorkers(1)
	svc.SetShardWorkers(1)
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
	chainStage0 := func(stage0 func() *types.Request) func() []*types.Request {
		return func() []*types.Request { return []*types.Request{stage0()} }
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
		{name: "memory join", site: "join (memory pre-flight)", setup: serialArm,
			requests: one(join), run: processRun(join),
			limits: func(l *limits.Limits) { l.MaxEstimatedMemory = 1 << 20 }, wantTrip: limits.MaxEstimatedMemory},
		{name: "compose slots", host: hostCompose, site: "compose", setup: serialArm,
			requests: two(grouped(paritySum, f.wide)),
			run: func(ctx context.Context, svc *Service) error {
				_, err := svc.Compose(ctx, composed(two(grouped(paritySum, f.wide))))
				return err
			},
			limits: func(l *limits.Limits) { l.MaxComposeSlots = 1 }, wantTrip: limits.MaxComposeSlots},
		{name: "compose slot groups", host: hostCompose, site: "compose (slot)", setup: serialArm,
			requests: two(grouped(paritySum, f.wide)),
			run: func(ctx context.Context, svc *Service) error {
				_, err := svc.Compose(ctx, composed(two(grouped(paritySum, f.wide))))
				return err
			},
			limits: maxGroups(8), wantTrip: limits.MaxGroups},
		{name: "compose parallel slots", host: hostCompose, site: "compose (parallel)", setup: serialArm,
			requests: two(grouped(paritySum, f.wide)),
			run: func(ctx context.Context, svc *Service) error {
				_, err := svc.ComposeParallel(ctx, composed(two(grouped(paritySum, f.wide))), ComposeOptions{FailFast: true})
				return err
			},
			limits: func(l *limits.Limits) { l.MaxComposeSlots = 1 }, wantTrip: limits.MaxComposeSlots},
		{name: "compose parallel slot groups", host: hostCompose, site: "compose (parallel slot)", setup: serialArm,
			requests: two(grouped(paritySum, f.wide)),
			run: func(ctx context.Context, svc *Service) error {
				_, err := svc.ComposeParallel(ctx, composed(two(grouped(paritySum, f.wide))), ComposeOptions{FailFast: true})
				return err
			},
			limits: maxGroups(8), wantTrip: limits.MaxGroups},
		{name: "chain stages", host: hostChain, site: "chain", setup: serialArm,
			requests: chainStage0(grouped(paritySum, f.wide)),
			run: func(ctx context.Context, svc *Service) error {
				_, err := svc.ProcessChain(ctx, chain(2, grouped(paritySum, f.wide)))
				return err
			},
			limits: func(l *limits.Limits) { l.MaxChainStages = 1 }, wantTrip: limits.MaxChainStages},
		{name: "chain stage groups", host: hostChain, site: "chain (stage 0)", setup: serialArm,
			requests: chainStage0(grouped(paritySum, f.wide)),
			run: func(ctx context.Context, svc *Service) error {
				_, err := svc.ProcessChain(ctx, chain(2, grouped(paritySum, f.wide)))
				return err
			},
			limits: maxGroups(8), wantTrip: limits.MaxGroups},
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
	// Every certain finding trips — with one of the certain figures.
	if len(certain) > 0 {
		if !tripped {
			t.Fatalf("predict reported certain %+v but the run succeeded", certain)
		}
		matched := false
		for _, fd := range certain {
			if string(trip.limit) == fd.Limit && trip.configured == fd.Configured {
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

// TestLimitsPredictRuntimeParity is the FR-31 gate on a 200,000-row
// buildWideCohort cohort: on every runtime enforcement site — serial,
// buffered, the parallel decode and shard reducers, the shard merge,
// the buffered and fused crosstab, join, matrix, Compose (serial and
// parallel), ProcessChain, facet and RequestTimeout — a tight limit
// trips exactly the limit the case names, and
//
//   - every certain finding trips at runtime, with the same {code,
//     limit, configured} (and, for a certain figure, the same
//     observed);
//   - every runtime trip was certain, possible or unknowable at
//     predict (RequestTimeout; the slot / stage count no predict
//     surface sees; a facet's group count);
//   - a request predict clears never trips a predictable limit: each
//     finding re-run with the limit raised to its estimate — the upper
//     bound — no longer trips that limit, and every case under the
//     built-in defaults has no finding and runs clean.
func TestLimitsPredictRuntimeParity(t *testing.T) {
	f := buildLimitsFixture(t, limitsRows, limitsFields)
	sites := map[string]bool{}
	for _, c := range parityCases(f) {
		t.Run(c.name, func(t *testing.T) {
			l := limits.Defaults()
			c.limits(&l)
			findings, trip := checkParity(t, f, c, l)
			switch {
			case c.wantTrip == "" && trip != nil:
				t.Fatalf("site %s: tripped %+v, want a clean run", c.site, *trip)
			case c.wantTrip != "" && trip == nil:
				t.Fatalf("site %s: no trip, want %s", c.site, c.wantTrip)
			case trip != nil && trip.limit != c.wantTrip:
				t.Fatalf("site %s: tripped %s, want %s", c.site, trip.limit, c.wantTrip)
			}
			if trip != nil {
				if got := limits.Value(l, trip.limit); trip.configured != got {
					t.Fatalf("site %s: configured %d, instance limit %d", c.site, trip.configured, got)
				}
				sites[c.site] = true
			}

			// Raised to its estimate, each finding's limit clears.
			for _, fd := range findings {
				raised := l
				limits.Set(&raised, limits.Name(fd.Limit), fd.Estimated)
				again, trip := checkParity(t, f, c, raised)
				for _, g := range again {
					if g.Limit == fd.Limit {
						t.Fatalf("%s at its estimate %d still reported %+v", fd.Limit, fd.Estimated, g)
					}
				}
				if trip != nil && string(trip.limit) == fd.Limit {
					t.Fatalf("%s at its estimate %d still tripped: %+v", fd.Limit, fd.Estimated, *trip)
				}
			}

			// The built-in defaults: no finding, no trip, on 200,000 rows.
			if c.site != "timeout" {
				if fs, trip := checkParity(t, f, c, limits.Defaults()); len(fs) != 0 || trip != nil {
					t.Fatalf("defaults: findings %+v, trip %v", fs, trip)
				}
			}
		})
	}
	for _, s := range []string{"serial", "buffered", "parallel decode reducer", "parallel shard reducer", "merge",
		"buffered crosstab", "fused crosstab", "join", "matrix", "compose", "compose (parallel)", "chain", "facet", "timeout"} {
		if !sites[s] {
			t.Errorf("runtime site %q never tripped", s)
		}
	}
}

// TestLimitsReleaseMemory is the FR-30 gate: a run a limit trips
// AFTER it built buffered state — the materialized records of the
// buffered grouped and crosstab arms, the fused grid, the shard and
// segment partials, a rich facet's accumulators — retains none of it.
// Each pass holds the returned error (and response) the way a caller
// would; retainedPerRecord (post-GC heap growth over the held results,
// per scanned record — never B/op) must stay under maxRetained, while
// the control that holds one record-sized byte per row reads well
// above it.
func TestLimitsReleaseMemory(t *testing.T) {
	const (
		passes      = 2
		maxRetained = 2.0 // B/record; a leaked materialization is ~100+
	)
	f := buildLimitsFixture(t, limitsRows, limitsFields)
	stride := f.schema.RecordByteSize()
	byName := map[string]parityCase{}
	for _, c := range parityCases(f) {
		byName[c.name] = c
	}
	for _, name := range []string{
		"buffered grouped", "buffered crosstab", "fused crosstab",
		"parallel decode", "parallel shards merge", "facet groups",
	} {
		c, ok := byName[name]
		if !ok {
			t.Fatalf("no parity case %q", name)
		}
		t.Run(name, func(t *testing.T) {
			svc := New(f.cfg)
			c.setup(t, svc)
			l := limits.Defaults()
			c.limits(&l)
			svc.SetLimits(l)
			trip := func() (any, int, error) {
				err := c.run(context.Background(), svc)
				if tr, ok := tripOf(t, err); !ok || tr.limit != c.wantTrip {
					return nil, 0, stderrors.Join(stderrors.New("want a "+string(c.wantTrip)+" trip"), err)
				}
				return err, limitsRows, nil
			}
			// Warm the service's cohort handles outside the measurement.
			if _, _, err := trip(); err != nil {
				t.Fatal(err)
			}
			per := retainedPerRecord(t, passes, trip)
			t.Logf("retained %.3f B/record after a %s trip", per, c.wantTrip)
			if per > maxRetained {
				t.Fatalf("a tripped run retains %.1f B/record (> %.1f): the buffered state outlived the trip", per, maxRetained)
			}
		})
	}
	t.Run("control", func(t *testing.T) {
		per := retainedPerRecord(t, passes, func() (any, int, error) {
			return make([]byte, stride*limitsRows), limitsRows, nil
		})
		if per < float64(stride)/2 {
			t.Fatalf("control retained %.1f B/record, want ~%d: the measurement cannot see a leak", per, stride)
		}
	})
}
