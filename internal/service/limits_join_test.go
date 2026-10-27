package service

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/types"
)

// joinLimitFixture writes a 6-row left cohort (id, seg, v) and a
// 1,000-row right cohort (id, tier) to mem.
func joinLimitFixture(t *testing.T, mem afero.Fs) {
	t.Helper()
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
		{Name: "tier", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 4, CsvColumnIdx: 1},
	}}
	right.Fields[1].Dictionary = tierDict
	var leftRecs, rightRecs [][]uint64
	for i := range 6 {
		leftRecs = append(leftRecs, []uint64{uint64(i), uint64(i % 2), math.Float64bits(float64(i))})
	}
	for i := range 1000 {
		rightRecs = append(rightRecs, []uint64{uint64(i % 6), uint64(i % 2)})
	}
	if err := afero.WriteFile(mem, "left.pulse", writePulseFile(t, left, leftRecs), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(mem, "right.pulse", writePulseFile(t, right, rightRecs), 0o644); err != nil {
		t.Fatal(err)
	}
}

func joinLimitSpec() []*types.JoinSpec {
	return []*types.JoinSpec{{Right: "right.pulse", Kind: "inner", As: "r_", On: []types.OnPair{{LeftField: "id", RightField: "id"}}}}
}

// joinLimitRequests are the two hosts sharing openJoinStream: the
// joined Process and the joined crosstab.
func joinLimitRequests() map[string]func() *types.Request {
	return map[string]func() *types.Request{
		"process": func() *types.Request {
			return &types.Request{
				Cohort:       &types.Cohort{Filename: "left.pulse"},
				Joins:        joinLimitSpec(),
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "v", Label: "s"}},
			}
		},
		"crosstab": func() *types.Request {
			return &types.Request{
				Cohort: &types.Cohort{Filename: "left.pulse"},
				Joins:  joinLimitSpec(),
				Crosstab: &types.CrosstabSpec{
					Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "seg"}},
					Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "r_tier"}},
					Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "v", Label: "c"},
					Shape:   types.CrosstabShapeMatrix,
				},
			}
		},
	}
}

func requireJoinBuildExceeded(t *testing.T, err error, configured, observed int64) {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_LIMIT_EXCEEDED {
		t.Fatalf("err = %v, want PULSE_LIMIT_EXCEEDED", err)
	}
	if ce.Details["limit"] != "max_join_build_rows" || ce.Details["configured"] != configured || ce.Details["observed"] != observed {
		t.Fatalf("details = %v", ce.Details)
	}
}

// TestLimitsPreflight_JoinBuildRowsRefusesBeforeDecode: on both join
// hosts, a right side over MaxJoinBuildRows is refused by the
// header-only count — no handle ever drains the right cohort — while
// the same request at the limit decodes it (the control).
func TestLimitsPreflight_JoinBuildRowsRefusesBeforeDecode(t *testing.T) {
	t.Parallel()

	for name, req := range joinLimitRequests() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			run := func(max int64) (fsCounts, error) {
				mem := afero.NewMemMapFs()
				joinLimitFixture(t, mem)
				wrapped := newCountingFs(mem)
				cfg, err := fs.New(fs.WithFs(wrapped))
				if err != nil {
					t.Fatal(err)
				}
				svc := New(cfg)
				l := limits.Defaults()
				l.MaxJoinBuildRows = max
				svc.SetLimits(l)
				_, perr := svc.Process(context.Background(), req())
				return wrapped.snapshot(), perr
			}

			if snap, err := run(1000); err != nil {
				t.Fatalf("at the limit: %v", err)
			} else if snap.FullReads["right.pulse"] == 0 {
				t.Fatal("control: a run at the limit never drained the right cohort — the read assertion below would be vacuous")
			}

			snap, err := run(999)
			requireJoinBuildExceeded(t, err, 999, 1000)
			if fr := snap.FullReads["right.pulse"]; fr != 0 {
				t.Fatalf("refused run drained the right cohort %d time(s); the pre-flight must refuse before decode", fr)
			}
		})
	}
}

// TestLimitsPreflight_JoinBuildRowsLoopBackstop: when the header count
// under-reports, the build loop refuses as soon as its decode passes
// the limit, on both join hosts.
func TestLimitsPreflight_JoinBuildRowsLoopBackstop(t *testing.T) {
	// serial: swaps the package-level service.joinBuildCount test hook

	orig := joinBuildCount
	joinBuildCount = func(context.Context, *Service, string) (uint64, error) { return 0, nil }
	t.Cleanup(func() { joinBuildCount = orig })

	for name, req := range joinLimitRequests() {
		t.Run(name, func(t *testing.T) {
			mem := afero.NewMemMapFs()
			joinLimitFixture(t, mem)
			cfg, err := fs.New(fs.WithFs(mem))
			if err != nil {
				t.Fatal(err)
			}
			svc := New(cfg)
			l := limits.Defaults()
			l.MaxJoinBuildRows = 999
			svc.SetLimits(l)
			_, perr := svc.Process(context.Background(), req())
			requireJoinBuildExceeded(t, perr, 999, 1000)
		})
	}
}

// TestJoinBuildRows_UnlimitedSkipsTheCount: an Unlimited
// MaxJoinBuildRows never pays the header count.
func TestJoinBuildRows_UnlimitedSkipsTheCount(t *testing.T) {
	// serial: swaps the package-level service.joinBuildCount test hook

	orig := joinBuildCount
	calls := 0
	joinBuildCount = func(ctx context.Context, s *Service, p string) (uint64, error) {
		calls++
		return orig(ctx, s, p)
	}
	t.Cleanup(func() { joinBuildCount = orig })

	mem := afero.NewMemMapFs()
	joinLimitFixture(t, mem)
	cfg, err := fs.New(fs.WithFs(mem))
	if err != nil {
		t.Fatal(err)
	}
	svc := New(cfg)
	l := limits.Defaults()
	l.MaxJoinBuildRows = limits.Unlimited
	svc.SetLimits(l)
	if _, err := svc.Process(context.Background(), joinLimitRequests()["process"]()); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("unlimited run counted the right side %d time(s)", calls)
	}
	svc.SetLimits(limits.Defaults())
	if _, err := svc.Process(context.Background(), joinLimitRequests()["process"]()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("bounded run counted the right side %d time(s), want 1", calls)
	}
}

// TestJoinFanOutBackstop_RefusesBeforeProbe (#229): on both join hosts
// (a buffered joined Process and the joined crosstab), a
// MaxEstimatedMemory at the pre-flight's estimate (one match per left
// row) passes the pre-flight; once the build has hashed the right side
// — each id 166-167 times — the backstop refuses the estimate on
// left x 167 joined rows with the memory refusal's details plus join,
// join_rows_bound and right_key_multiplicity, before any left row is
// probed (the left cohort never drains). At that figure the run
// succeeds, byte-identical to the Unlimited run.
func TestJoinFanOutBackstop_RefusesBeforeProbe(t *testing.T) {
	t.Parallel()

	hosts := map[string]func() *types.Request{
		"process": func() *types.Request {
			r := joinLimitRequests()["process"]()
			r.Aggregations = []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "v", Label: "m"}}
			return r
		},
		"crosstab": joinLimitRequests()["crosstab"],
	}
	for name, req := range hosts {
		t.Run(name, func(t *testing.T) {
			run := func(max int64) (fsCounts, *types.Response, error) {
				mem := afero.NewMemMapFs()
				joinLimitFixture(t, mem)
				wrapped := newCountingFs(mem)
				cfg, err := fs.New(fs.WithFs(wrapped))
				if err != nil {
					t.Fatal(err)
				}
				svc := New(cfg)
				l := limits.Defaults()
				l.MaxEstimatedMemory = max
				svc.SetLimits(l)
				resp, perr := svc.Process(context.Background(), req())
				return wrapped.snapshot(), resp, perr
			}
			exceeded := func(err error) *errors.CodedError {
				t.Helper()
				var ce *errors.CodedError
				if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_LIMIT_EXCEEDED || ce.Details["limit"] != "max_estimated_memory" {
					t.Fatalf("err = %v, want PULSE_LIMIT_EXCEEDED max_estimated_memory", err)
				}
				return ce
			}
			_, _, err := run(1)
			certain, _ := exceeded(err).Details["observed"].(int64)
			if _, isJoin := exceeded(err).Details["join"]; isJoin {
				t.Fatal("the pre-flight's refusal carries the backstop's join details")
			}

			snap, _, err := run(certain)
			ce := exceeded(err)
			if ce.Details["configured"] != certain || ce.Details["join"] != 0 ||
				ce.Details["right_key_multiplicity"] != int64(167) || ce.Details["join_rows_bound"] != int64(6*167) {
				t.Fatalf("backstop details %v", ce.Details)
			}
			backstop, _ := ce.Details["observed"].(int64)
			if backstop <= certain {
				t.Fatalf("backstop observed %d <= the pre-flight estimate %d", backstop, certain)
			}
			if snap.FullReads["right.pulse"] == 0 {
				t.Fatal("the backstop ran before the build drained the right side")
			}
			if fr := snap.FullReads["left.pulse"]; fr != 0 {
				t.Fatalf("the backstop drained the left side %d time(s); it must refuse before the probe", fr)
			}

			_, capped, err := run(backstop)
			if err != nil {
				t.Fatalf("at the backstop's estimate: %v", err)
			}
			_, free, err := run(limits.Unlimited)
			if err != nil {
				t.Fatal(err)
			}
			a, _ := json.Marshal(capped)
			b, _ := json.Marshal(free)
			if !bytes.Equal(a, b) {
				t.Fatalf("capped run differs from the Unlimited run:\n%s\n%s", a, b)
			}
		})
	}
}
