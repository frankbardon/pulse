package service

import (
	"context"
	stderrors "errors"
	"math"
	"runtime"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// ctxPollBuildRows is the right side of the join-build fixture: far
// past processing.CtxPollInterval, so a build loop that ignores ctx
// shows up as hundreds of thousands of allocations.
const ctxPollBuildRows = 100_000

// ctxPollFixture writes a left cohort of leftRows (id, seg, v) rows and
// a right cohort of rightRows (id, tier) rows whose ids cover the
// left's, so every left row joins.
func ctxPollFixture(t *testing.T, leftRows, rightRows int) *Service {
	t.Helper()
	mem := afero.NewMemMapFs()
	segDict := encoding.NewDictionary()
	segDict.Add("x")
	segDict.Add("y")
	left := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "seg", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 4, CsvColumnIdx: 1, Dictionary: segDict},
		{Name: "v", Type: encoding.FieldTypeF64, ByteOffset: 5, CsvColumnIdx: 2},
	}}
	tierDict := encoding.NewDictionary()
	tierDict.Add("gold")
	tierDict.Add("silver")
	right := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "tier", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 4, CsvColumnIdx: 1, Dictionary: tierDict},
	}}
	leftRecs := make([][]uint64, leftRows)
	for i := range leftRecs {
		leftRecs[i] = []uint64{uint64(i % rightRows), uint64(i % 2), math.Float64bits(float64(i))}
	}
	rightRecs := make([][]uint64, rightRows)
	for i := range rightRecs {
		rightRecs[i] = []uint64{uint64(i), uint64(i % 2)}
	}
	if err := afero.WriteFile(mem, "left.pulse", writePulseFile(t, left, leftRecs), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(mem, "right.pulse", writePulseFile(t, right, rightRecs), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := fs.New(fs.WithFs(mem))
	if err != nil {
		t.Fatal(err)
	}
	return New(cfg)
}

func ctxPollJoin() []*types.JoinSpec {
	return []*types.JoinSpec{{Right: "right.pulse", Kind: "inner", As: "r_", On: []types.OnPair{{LeftField: "id", RightField: "id"}}}}
}

// ctxPollBufferedCrosstab is a seg x tier crosstab whose MEDIAN cell
// keeps it off the fused arm, so the records are materialized first.
func ctxPollBufferedCrosstab(join bool) *types.Request {
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "left.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "seg"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "seg"}},
			Cell:    &types.Aggregation{Type: types.AGG_MEDIAN, Field: "v", Label: "m"},
			Shape:   types.CrosstabShapeMatrix,
		},
	}
	if join {
		req.Joins = ctxPollJoin()
		req.Crosstab.Columns[0].Field = "r_tier"
	}
	return req
}

// sliceCancelIter cancels its ctx as it yields row cancelAt.
type sliceCancelIter struct {
	*processing.SliceIterator
	cancel   context.CancelFunc
	cancelAt int
	yielded  int
}

func (c *sliceCancelIter) Next() bool {
	if !c.SliceIterator.Next() {
		return false
	}
	c.yielded++
	if c.yielded == c.cancelAt {
		c.cancel()
	}
	return true
}

// TestCtxPoll_MaterializeRecords: the buffered crosstab drain — serial
// and joined alike — stops within CtxPollInterval rows of a mid-run
// cancel and returns the caller's ctx error unchanged.
func TestCtxPoll_MaterializeRecords(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{{Name: "v", Type: encoding.FieldTypeF64}}}
	recs := make([]*processing.Record, 40_000)
	for i := range recs {
		recs[i] = processing.NewRecord(schema, map[string]float64{"v": float64(i)})
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	it := &sliceCancelIter{SliceIterator: processing.NewSliceIterator(recs), cancel: cancel, cancelAt: 5_000}
	out, err := materializeRecords(ctx, it)
	if err != context.Canceled {
		t.Fatalf("err = %v (%d records), want context.Canceled unchanged", err, len(out))
	}
	if over := it.yielded - it.cancelAt; over > processing.CtxPollInterval {
		t.Fatalf("drain ran %d rows past the cancel, want <= %d", over, processing.CtxPollInterval)
	}
}

// TestCtxPoll_BufferedCrosstabCancelled: a cancelled ctx stops the
// buffered crosstab — plain and joined — with the caller's error. The
// buffered crosstab polls nowhere else, so a run that ignored ctx
// would succeed.
func TestCtxPoll_BufferedCrosstabCancelled(t *testing.T) {
	for _, join := range []bool{false, true} {
		name := "plain"
		if join {
			name = "joined"
		}
		t.Run(name, func(t *testing.T) {
			svc := ctxPollFixture(t, 20_000, 1_000)
			if _, err := svc.Process(context.Background(), ctxPollBufferedCrosstab(join)); err != nil {
				t.Fatalf("uncancelled run: %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := svc.Process(ctx, ctxPollBufferedCrosstab(join))
			if !stderrors.Is(err, context.Canceled) {
				t.Fatalf("err = %v, want context.Canceled", err)
			}
		})
	}
}

// TestCtxPoll_JoinBuildCancelled: a cancelled ctx stops the join build
// on its first row — the right side is not materialized (its per-row
// record allocations never happen) — and the joined Process returns the
// caller's error unchanged.
func TestCtxPoll_JoinBuildCancelled(t *testing.T) {
	svc := ctxPollFixture(t, 10, ctxPollBuildRows)
	req := func() *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: "left.pulse"},
			Joins:        ctxPollJoin(),
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "v", Label: "s"}},
		}
	}
	if _, err := svc.Process(context.Background(), req()); err != nil {
		t.Fatalf("uncancelled run: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := svc.Process(ctx, req())
	runtime.ReadMemStats(&after)
	if err != context.Canceled {
		t.Fatalf("err = %v, want context.Canceled unchanged", err)
	}
	// A full build allocates several objects per right row; a build
	// stopped on its first row allocates a bounded handful.
	if mallocs := after.Mallocs - before.Mallocs; mallocs > ctxPollBuildRows/4 {
		t.Fatalf("cancelled join allocated %d objects, want <= %d (build side materialized)", mallocs, ctxPollBuildRows/4)
	}
}

// TestFailFastWinner: a FailFast Compose reports the lowest-index
// failure that is not a sibling cancellation — a slot cancelled by
// another slot's error never hides that error — while a caller-done ctx
// keeps the lowest-index failure, cancellation included.
func TestFailFastWinner(t *testing.T) {
	refusal := stderrors.New("refused")
	errs := []error{context.Canceled, refusal, context.Canceled}
	failed := []int{0, 1, 2}
	if w := failFastWinner(context.Background(), errs, failed); w != 1 {
		t.Fatalf("winner = %d, want 1 (the refusal, not the sibling cancel)", w)
	}
	if w := failFastWinner(context.Background(), []error{context.Canceled, context.Canceled}, []int{0, 1}); w != 0 {
		t.Fatalf("all-cancelled winner = %d, want 0", w)
	}
	done, cancel := context.WithCancel(context.Background())
	cancel()
	if w := failFastWinner(done, errs, failed); w != 0 {
		t.Fatalf("caller-cancelled winner = %d, want 0", w)
	}
}
