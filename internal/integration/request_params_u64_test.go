package integration_test

import (
	"context"
	"encoding/json"
	"math"
	"math/big"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/processing"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
)

// request_params_u64_test.go — a u64 FIELD compares exactly over the
// full 0..2^64−1 range against a param, an integer literal and another
// u64 field (U58 E3-S1 fix): on the streaming arm and the parallel
// decode arm alike (DecodeWorkers 1 ≡ 4), on a null-input row's untyped
// twin, through the grouped filter precompute, with predict refusing
// exactly what the run refuses.

// u64Rows is the parallel-decode fixture's row count: at the decode
// threshold, so DecodeWorkers > 1 fans out.
const u64Rows = 100_000

// u64RowID is row i's id: half the rows straddle 2^53, the rest end at
// 2^64 − 1 (straddling nothing a float64 can tell apart).
func u64RowID(i int) uint64 {
	if i < u64Rows/2 {
		return 1<<53 - 25_000 + uint64(i)
	}
	return math.MaxUint64 - uint64(u64Rows-1-i)
}

// u64RowID2 is row i's id2: id − 1, id or id + 1 (saturating).
func u64RowID2(i int) uint64 {
	id := u64RowID(i)
	switch i % 3 {
	case 0:
		if id > 0 {
			return id - 1
		}
	case 1:
		if id < math.MaxUint64 {
			return id + 1
		}
	}
	return id
}

// u64ParamsDir writes u64.pulse (id u64, id2 u64, y f64 nullable — null
// on every third row) into a fresh t.TempDir. Per call, never cached in
// a package-level sync.Once: a cached TempDir dies with the first test
// that made it, so a later caller (or a -count=N rerun) read a deleted
// directory.
func u64ParamsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p, err := pulse.New(pulse.Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	s := encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU64},
		{Name: "id2", Type: encoding.FieldTypeU64},
		{Name: "y", Type: encoding.FieldTypeF64, Nullable: true},
	}}
	b, err := p.NewCohortBuilder(context.Background(), "u64.pulse", s, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < u64Rows; i++ {
		var y any = float64(i % 7)
		if i%3 == 2 {
			y = nil
		}
		if err := b.Append(pulse.CohortRow{u64RowID(i), u64RowID2(i), y}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	return dir
}

func u64Exact(op string, a, b *big.Int) bool {
	c := a.Cmp(b)
	switch op {
	case "==":
		return c == 0
	case "!=":
		return c != 0
	case "<":
		return c < 0
	case "<=":
		return c <= 0
	case ">":
		return c > 0
	}
	return c >= 0
}

// TestParams_U64FieldPredictMatchesRuntime: an ill-typed expression
// over a u64 field is refused by predict with the run's code and
// message — the float64 echo's own compile error, as before.
func TestParams_U64FieldPredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatal(err)
	}
	s := encoding.Schema{Fields: []encoding.Field{{Name: "id", Type: encoding.FieldTypeU64}, {Name: "x", Type: encoding.FieldTypeF64}}}
	b, err := p.NewCohortBuilder(ctx, "u.pulse", s, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if err := b.Append(pulse.CohortRow{uint64(1<<53 + i), float64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	filter := func(e string) *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: "u.pulse"},
			Params:       types.Params{"k": json.Number("9007199254740993")},
			Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: e}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "c"}},
		}
	}
	formula := func(e string) *types.Request {
		r := filter("true")
		r.Filterers = nil
		r.Attributes = []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "f", Expression: e}}
		r.Aggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "f", Label: "s"}}
		return r
	}
	for _, req := range []*types.Request{
		filter(`id == "a"`), filter(`id < "a"`), filter(`id`), filter(`id + 1`), filter(`id == x + "a"`),
		filter(`id in "abc"`), filter(`id < nil`), formula(`id + "a"`), formula(`len(id)`), formula(`id[0]`),
		formula(`id == params.k + "a"`),
	} {
		var name string
		if len(req.Filterers) > 0 {
			name = req.Filterers[0].Expression
		} else {
			name = req.Attributes[0].Expression
		}
		t.Run(name, func(t *testing.T) {
			env, err := p.PredictEnvelope(ctx, req)
			if err != nil {
				t.Fatalf("PredictEnvelope: %v", err)
			}
			_, runErr := p.Process(ctx, req)
			if runErr == nil {
				t.Fatalf("the run accepted %q", name)
			}
			ce := requireCodedError(t, runErr, "PULSE_FORMULA_COMPILE_ERROR")
			found := false
			for _, e := range env.Errors {
				found = found || (e.Code == string(ce.Code) && e.Message == ce.Message)
			}
			if !found {
				t.Fatalf("predict errors %+v lack the run's %s %q", env.Errors, ce.Code, ce.Message)
			}
		})
	}
	// And a valid one stays valid on both sides.
	env, err := p.PredictEnvelope(ctx, filter("id == params.k"))
	if err != nil || len(env.Errors) != 0 {
		t.Fatalf("predict: %v %+v", err, env.Errors)
	}
	resp, err := p.Process(ctx, filter("id == params.k"))
	if err != nil {
		t.Fatal(err)
	}
	if got := paramsFloat(t, resp, "c"); got != 1 {
		t.Fatalf("id == params.k kept %v rows, want 1", got)
	}
}

// TestParams_U64GroupMemberPrecompute: a filter over a u64 parent-group
// member is exact through the grouped filter precompute, per-row and on
// the ungrouped twin alike.
func TestParams_U64GroupMemberPrecompute(t *testing.T) {
	// serial: processing.SetFilterPrecompute flips a process-wide engine switch
	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	s := encoding.Schema{Fields: []encoding.Field{
		{Name: "cust", Type: encoding.FieldTypeU32},
		{Name: "cid", Type: encoding.FieldTypeU64},
		{Name: "amount", Type: encoding.FieldTypeF64},
	}}
	for _, name := range []string{"flat.pulse", "grouped.pulse"} {
		opts := pulse.CohortBuilderOptions{}
		if name == "grouped.pulse" {
			opts.Groups = []pio.GroupDecl{{Key: []string{"cust"}, Members: []string{"cid"}}}
		}
		b, err := p.NewCohortBuilder(ctx, name, s, opts)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 600; i++ {
			c := i % 20
			if err := b.Append(pulse.CohortRow{uint64(c), uint64(1<<53 + c), float64(i)}); err != nil {
				t.Fatal(err)
			}
		}
		res, err := b.Close()
		if err != nil {
			t.Fatal(err)
		}
		if name == "grouped.pulse" && len(res.Groups) != 1 {
			t.Fatalf("grouped twin wrote %d groups", len(res.Groups))
		}
	}
	req := func(cohort string) *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: cohort},
			Params:       types.Params{"k": json.Number("9007199254740993")}, // 2^53 + 1: cust 1 only
			Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "cid == params.k"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "amount", Label: "c"}},
		}
	}
	for _, arm := range []struct {
		cohort     string
		precompute bool
	}{{"flat.pulse", true}, {"grouped.pulse", true}, {"grouped.pulse", false}} {
		prev := processing.SetFilterPrecompute(arm.precompute)
		before := processing.FilterPrecomputeStats().EntryEvaluations
		resp, err := p.Process(ctx, req(arm.cohort))
		evals := processing.FilterPrecomputeStats().EntryEvaluations - before
		processing.SetFilterPrecompute(prev)
		if err != nil {
			t.Fatal(err)
		}
		if got := paramsFloat(t, resp, "c"); got != 30 {
			t.Errorf("%s precompute=%v: kept %v rows, want 30", arm.cohort, arm.precompute, got)
		}
		if wantEvals := int64(0); arm.cohort == "grouped.pulse" && arm.precompute {
			if evals != 20 {
				t.Errorf("grouped precompute evaluated %d entries, want 20", evals)
			}
		} else if evals != wantEvals {
			t.Errorf("%s precompute=%v: %d entry evaluations, want none", arm.cohort, arm.precompute, evals)
		}
	}
}
