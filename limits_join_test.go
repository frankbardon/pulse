package pulse_test

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/mcp"
	"github.com/frankbardon/pulse/types"
)

// readCountingFs counts the bytes read through every handle per path,
// so a test can tell a header-only read from a record decode.
type readCountingFs struct {
	afero.Fs
	mu    sync.Mutex
	bytes map[string]int64
}

func newReadCountingFs(inner afero.Fs) *readCountingFs {
	return &readCountingFs{Fs: inner, bytes: map[string]int64{}}
}

func (c *readCountingFs) Open(name string) (afero.File, error) {
	f, err := c.Fs.Open(name)
	if err != nil {
		return nil, err
	}
	return &readCountingFile{File: f, fs: c, name: name}, nil
}

func (c *readCountingFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	f, err := c.Fs.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}
	return &readCountingFile{File: f, fs: c, name: name}, nil
}

func (c *readCountingFs) add(name string, n int) {
	c.mu.Lock()
	c.bytes[name] += int64(n)
	c.mu.Unlock()
}

func (c *readCountingFs) read(name string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bytes[name]
}

func (c *readCountingFs) reset() {
	c.mu.Lock()
	c.bytes = map[string]int64{}
	c.mu.Unlock()
}

type readCountingFile struct {
	afero.File
	fs   *readCountingFs
	name string
}

func (f *readCountingFile) Read(p []byte) (int, error) {
	n, err := f.File.Read(p)
	f.fs.add(f.name, n)
	return n, err
}

func (f *readCountingFile) ReadAt(p []byte, off int64) (int, error) {
	n, err := f.File.ReadAt(p, off)
	f.fs.add(f.name, n)
	return n, err
}

// joinLimitCohorts imports a 30-row left cohort (id, seg, v) and a
// 1,000-row right cohort (id, tier) whose ids repeat the left's, and
// returns the fs and both paths.
func joinLimitCohorts(t *testing.T) (afero.Fs, string, string) {
	t.Helper()
	fs := afero.NewMemMapFs()
	var left, right strings.Builder
	left.WriteString("id,seg,v\n")
	for i := range 30 {
		fmt.Fprintf(&left, "%d,%s,%d\n", i, []string{"x", "y"}[i%2], i*3)
	}
	right.WriteString("id,tier\n")
	for i := range 1000 {
		fmt.Fprintf(&right, "%d,%s\n", i%30, []string{"gold", "silver", "bronze"}[i%3])
	}
	if err := afero.WriteFile(fs, "left.csv", []byte(left.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fs, "right.csv", []byte(right.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	l, err := p.ImportFile(context.Background(), pulse.ImportSpec{SourcePath: "left.csv"})
	if err != nil {
		t.Fatalf("ImportFile left: %v", err)
	}
	r, err := p.ImportFile(context.Background(), pulse.ImportSpec{SourcePath: "right.csv"})
	if err != nil {
		t.Fatalf("ImportFile right: %v", err)
	}
	return fs, l.Path, r.Path
}

func joinOn(right string) []*types.JoinSpec {
	return []*types.JoinSpec{{Right: right, Kind: "inner", As: "r_", On: []types.OnPair{{LeftField: "id", RightField: "id"}}}}
}

func joinedSum(left, right string) *types.Request {
	return &types.Request{
		Cohort:       &types.Cohort{Filename: left},
		Joins:        joinOn(right),
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "v", Label: "s"}},
	}
}

func joinedCrosstab(left, right string) *types.Request {
	return &types.Request{
		Cohort: &types.Cohort{Filename: left},
		Joins:  joinOn(right),
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "seg"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "r_tier"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "v", Label: "c"},
			Shape:   types.CrosstabShapeMatrix,
		},
	}
}

// TestLimits_JoinBuildRows_PredictCertainAndProcessRefuses: a join
// whose right side holds 1,000 records under MaxJoinBuildRows=999 is a
// certain predict finding (Predict, PredictBytes, MCP pulse_predict)
// and the runtime refuses it with the error predict reports — on the
// joined Process and the joined crosstab alike. At 1,000 both run.
func TestLimits_JoinBuildRows_PredictCertainAndProcessRefuses(t *testing.T) {
	fs, left, right := joinLimitCohorts(t)
	ctx := context.Background()
	want := []descriptor.LimitFinding{{Limit: "max_join_build_rows", Configured: 999, Estimated: 1000, Grade: descriptor.LimitGradeCertain}}

	for name, req := range map[string]func(string, string) *types.Request{"process": joinedSum, "crosstab": joinedCrosstab} {
		t.Run(name, func(t *testing.T) {
			p := limitsPulse(t, fs, pulse.Limits{MaxJoinBuildRows: 999})
			res, err := p.Predict(ctx, req(left, right))
			if err != nil {
				t.Fatal(err)
			}
			if res.Valid || !reflect.DeepEqual(res.LimitFindings, want) {
				t.Fatalf("Predict valid=%v findings=%+v, want false %+v", res.Valid, res.LimitFindings, want)
			}
			out, err := mcp.HandlePredict(ctx, p, *req(left, right))
			if err != nil {
				t.Fatal(err)
			}
			if out.Valid || !reflect.DeepEqual(out.LimitFindings, want) {
				t.Fatalf("pulse_predict valid=%v findings=%+v", out.Valid, out.LimitFindings)
			}
			data, err := afero.ReadFile(fs, left)
			if err != nil {
				t.Fatal(err)
			}
			env, err := p.PredictBytes(ctx, data, req(left, right))
			if err != nil {
				t.Fatal(err)
			}
			if pr := env.Data.(*descriptor.PredictResult); pr.Valid || !reflect.DeepEqual(pr.LimitFindings, want) {
				t.Fatalf("PredictBytes valid=%v findings=%+v", pr.Valid, pr.LimitFindings)
			}

			_, perr := p.Process(ctx, req(left, right))
			d := requireLimitExceeded(t, perr, "max_join_build_rows", 999, "Options.Limits.MaxJoinBuildRows")
			if d["observed"] != int64(1000) {
				t.Fatalf("observed = %v, want 1000", d["observed"])
			}
			sameEntry(t, env, perr)

			at := limitsPulse(t, fs, pulse.Limits{MaxJoinBuildRows: 1000})
			res, err = at.Predict(ctx, req(left, right))
			if err != nil {
				t.Fatal(err)
			}
			if !res.Valid || res.LimitFindings != nil {
				t.Fatalf("at the limit: valid=%v findings=%+v", res.Valid, res.LimitFindings)
			}
			if _, err := at.Process(ctx, req(left, right)); err != nil {
				t.Fatalf("at the limit: %v", err)
			}
		})
	}
}

// TestLimits_JoinBuildRows_PredictReadsRightHeaderOnly: predicting a
// join reads the right cohort's header + schema only — never a record
// — while the joined run decodes it (the control that keeps the
// assertion from being vacuous).
func TestLimits_JoinBuildRows_PredictReadsRightHeaderOnly(t *testing.T) {
	inner, left, right := joinLimitCohorts(t)
	fs := newReadCountingFs(inner)
	ctx := context.Background()
	fi, err := inner.Stat(right)
	if err != nil {
		t.Fatal(err)
	}
	p := limitsPulse(t, fs, pulse.Limits{MaxJoinBuildRows: 999})

	res, err := p.Predict(ctx, joinedSum(left, right))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.LimitFindings) != 1 {
		t.Fatalf("findings = %+v, want the join-build finding", res.LimitFindings)
	}
	t.Logf("predict read %d of %d right-cohort bytes", fs.read(right), fi.Size())
	if got := fs.read(right); got == 0 || got >= fi.Size() {
		t.Fatalf("predict read %d of the right cohort's %d bytes; want header + schema only", got, fi.Size())
	}

	fs.reset()
	free := limitsPulse(t, fs, pulse.Limits{})
	if _, err := free.Process(ctx, joinedSum(left, right)); err != nil {
		t.Fatal(err)
	}
	if got := fs.read(right); got < fi.Size() {
		t.Fatalf("control: the joined run read %d of %d right bytes — the header-only assertion would be vacuous", got, fi.Size())
	}
}
