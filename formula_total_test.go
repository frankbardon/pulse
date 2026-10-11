package pulse_test

import (
	"context"
	"math"
	"os"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// End-to-end coverage of ATTR_FORMULA's total(expr) cohort-total term
// (internal/processing/formula_total.go) over the parity fixture
// (extensions_parity_test.go): score = r%50 + 0.5, null when r%7 == 3;
// qty = r%13; region = r%4.

// ftScoreSum is the sequential float64 Σ op(score) over fixture rows
// [0, n) that keep(qty), null scores skipped — the reference a
// total() must equal bit for bit.
func ftScoreSum(n int, keep func(qty int) bool, op func(float64) float64) float64 {
	var s float64
	for r := 0; r < n; r++ {
		if r%7 == 3 || !keep(r%13) {
			continue
		}
		s += op(float64(r%50) + 0.5)
	}
	return s
}

func ftKeep(q int) bool { return q >= 2 }

func ftThird(v float64) float64 { return v / 3 }

func ftReq(path string, attrs ...*types.Attribute) *types.Request {
	return &types.Request{
		Cohort:       &types.Cohort{Filename: path},
		Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "qty >= 2"}},
		Attributes:   attrs,
		Aggregations: []*types.Aggregation{{Type: types.AGG_MAX, Field: "out", Label: "max"}, {Type: types.AGG_MIN, Field: "out", Label: "min"}},
	}
}

func ftBits(t *testing.T, what string, got any, want float64) {
	t.Helper()
	g, ok := got.(float64)
	if !ok || math.Float64bits(g) != math.Float64bits(want) {
		t.Fatalf("%s: %v (%T), want %v bit for bit", what, got, got, want)
	}
}

// TestFormulaTotal_Arms: total(score / 3) equals the sequential sum over
// the post-filter rows on every arm — streaming two-pass, buffered, the
// shard archive (sequential: not mergeable) and the >threshold single
// file (parallel decode would fold partition-local totals; the merge
// gate keeps it serial).
func TestFormulaTotal_Arms(t *testing.T) {
	out := &types.Attribute{Type: types.ATTR_FORMULA, Expression: "total(score / 3)", Label: "out"}
	arms := tpoArms(t, true)
	rows := map[string]int{"single_file": paritySmallRows, "shard_archive": 120, "large_decode_workers": parityLargeRows}
	for _, arm := range arms {
		t.Run(arm.name, func(t *testing.T) {
			p, path := arm.open(t)
			want := ftScoreSum(rows[arm.name], ftKeep, ftThird)
			ctx := context.Background()
			resp, err := p.Process(ctx, ftReq(path, out))
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			ftBits(t, "max", resp.Data[0]["max"], want)
			ftBits(t, "min", resp.Data[0]["min"], want)
			buf, err := p.Process(ctx, ftReq(path, out,
				&types.Attribute{Type: types.ATTR_PERCENTILE, Field: "qty", Label: "pct"}))
			if err != nil {
				t.Fatalf("buffered Process: %v", err)
			}
			ftBits(t, "buffered max", buf.Data[0]["max"], want)
		})
	}
}

// TestFormulaTotal_GroupedWeightedProjected: a grouped request reads
// the cohort-wide total on every group row; a request weight does not
// reach it (unweighted); the projected buffered decode loads the
// total()'s argument columns, equal to an unprojected run.
func TestFormulaTotal_GroupedWeightedProjected(t *testing.T) {
	fsys := afero.NewMemMapFs()
	writeParityCohort(t, fsys, "ft.pulse", paritySchema(t), 0, paritySmallRows)
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	noProj, err := pulse.New(pulse.Options{FS: fsys, DisableProjection: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	want := ftScoreSum(paritySmallRows, ftKeep, ftThird)
	out := &types.Attribute{Type: types.ATTR_FORMULA, Expression: "total(score / 3)", Label: "out"}

	grouped := ftReq("ft.pulse", out)
	grouped.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
	resp, err := p.Process(ctx, grouped)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 4 {
		t.Fatalf("%d group rows, want 4", len(resp.Data))
	}
	for _, row := range resp.Data {
		ftBits(t, "group "+row["region"].(string), row["max"], want)
	}

	// A weight-aware aggregation under a request weight: its weighted
	// mean of the constant column is the unweighted total, while a
	// weighted Σ qty·score/3 would be several times larger.
	weighted := ftReq("ft.pulse", out)
	weighted.Weight = &types.WeightSpec{Field: "qty"}
	weighted.Aggregations = []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "out", Label: "mean"}}
	wresp, err := p.Process(ctx, weighted)
	if err != nil {
		t.Fatal(err)
	}
	if wsum := ftScoreSum(paritySmallRows, ftKeep, ftThird); math.Abs(wresp.Data[0]["mean"].(float64)-wsum) > 1e-12*wsum {
		t.Fatalf("weighted request: total %v, want the unweighted %v", wresp.Data[0]["mean"], wsum)
	}

	// Buffered (percentile) so projection applies; score is read only
	// inside total().
	proj := ftReq("ft.pulse", out, &types.Attribute{Type: types.ATTR_PERCENTILE, Field: "qty", Label: "pct"})
	a, err := p.Process(ctx, proj)
	if err != nil {
		t.Fatal(err)
	}
	b, err := noProj.Process(ctx, proj)
	if err != nil {
		t.Fatal(err)
	}
	ftBits(t, "projected", a.Data[0]["max"], want)
	ftBits(t, "unprojected", b.Data[0]["max"], want)
}

// TestFormulaTotal_UndefinedIsNull: a total over zero non-null rows is
// NaN in Go and null on the wire; a zero total used as a divisor is
// non-finite and null on the wire. Never 0.
func TestFormulaTotal_UndefinedIsNull(t *testing.T) {
	fsys := afero.NewMemMapFs()
	writeParityCohort(t, fsys, "ft.pulse", paritySchema(t), 0, paritySmallRows)
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	empty := ftReq("ft.pulse", &types.Attribute{Type: types.ATTR_FORMULA, Expression: "qty + total(score)", Label: "out"})
	empty.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "score == nil"}}
	zero := ftReq("ft.pulse", &types.Attribute{Type: types.ATTR_FORMULA, Expression: "(qty + 1) / total(qty * 0)", Label: "out"})
	for name, tc := range map[string]struct {
		req  *types.Request
		isNF func(float64) bool
	}{
		"empty": {empty, math.IsNaN},
		"zero":  {zero, func(v float64) bool { return math.IsInf(v, 1) }},
	} {
		resp, err := p.Process(ctx, tc.req)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		v, _ := resp.Data[0]["max"].(float64)
		if !tc.isNF(v) {
			t.Fatalf("%s: max %v, want non-finite", name, resp.Data[0]["max"])
		}
		b, err := types.MarshalFinite(map[string]any{"max": resp.Data[0]["max"]})
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != `{"max":null}` {
			t.Fatalf("%s: wire %s, want null", name, b)
		}
	}
}

// TestFormulaTotal_Materialize: api materialize writes the total-bearing
// column with every row's pre-pass total.
func TestFormulaTotal_Materialize(t *testing.T) {
	fsys := afero.NewMemMapFs()
	writeParityCohort(t, fsys, "ft.pulse", paritySchema(t), 0, paritySmallRows)
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	req := &types.Request{
		Cohort:     &types.Cohort{Filename: "ft.pulse"},
		Filterers:  []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "qty >= 2"}},
		Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "total(score / 3)", Label: "out"}},
	}
	if _, err := p.Materialize(ctx, req, "mat.pulse", pulse.MaterializeOptions{}); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	resp, err := p.Process(ctx, &types.Request{
		Cohort:       &types.Cohort{Filename: "mat.pulse"},
		Aggregations: []*types.Aggregation{{Type: types.AGG_MAX, Field: "out", Label: "max"}, {Type: types.AGG_MIN, Field: "out", Label: "min"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := ftScoreSum(paritySmallRows, ftKeep, ftThird)
	ftBits(t, "materialized max", resp.Data[0]["max"], want)
	ftBits(t, "materialized min", resp.Data[0]["min"], want)
}

// TestFormulaTotal_Refusals: predict and Process refuse the same
// total() misuse with the same code; an embedder may not register an
// expression function named total.
func TestFormulaTotal_Refusals(t *testing.T) {
	fsys := afero.NewMemMapFs()
	writeParityCohort(t, fsys, "ft.pulse", paritySchema(t), 0, paritySmallRows)
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	formula := func(expr string) *types.Request {
		return ftReq("ft.pulse", &types.Attribute{Type: types.ATTR_FORMULA, Expression: expr, Label: "out"})
	}
	filter := ftReq("ft.pulse", &types.Attribute{Type: types.ATTR_FORMULA, Expression: "qty", Label: "out"})
	filter.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "qty > total(qty) / 100"}}
	overlay := &types.Request{
		Cohort: &types.Cohort{Filename: "ft.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "qty"},
		},
		Overlays: []types.OverlaySpec{{Kind: types.OverlayKindFormula, Params: []byte(`{"formula":"cell / total(cell)"}`)}},
	}
	for name, tc := range map[string]struct {
		req  *types.Request
		code errors.Code
	}{
		"nested":  {formula("total(qty * total(score))"), errors.PULSE_FORMULA_TOTAL_NESTED},
		"arity":   {formula("total(qty, score)"), errors.PULSE_FORMULA_TOTAL_ARITY},
		"filter":  {filter, errors.PULSE_FORMULA_TOTAL_SITE},
		"overlay": {overlay, errors.PULSE_FORMULA_TOTAL_SITE},
	} {
		t.Run(name, func(t *testing.T) {
			cohort, err := afero.ReadFile(fsys, "ft.pulse")
			if err != nil {
				t.Fatal(err)
			}
			env, err := p.PredictBytes(ctx, cohort, tc.req)
			if err != nil {
				t.Fatalf("Predict: %v", err)
			}
			found := false
			for _, e := range env.Errors {
				if e.Code == string(tc.code) {
					found = true
				}
			}
			if !found {
				t.Errorf("predict errors %+v, want %s", env.Errors, tc.code)
			}
			if _, err := p.Process(ctx, tc.req); !errors.HasCode(err, tc.code) {
				t.Errorf("Process: %v, want %s", err, tc.code)
			}
		})
	}

	_, err = pulse.New(pulse.Options{FS: fsys, Extensions: pulse.Extensions{
		ExprFunctions: []pulse.ExprFunction{{Name: "total", Fn: func(x float64) float64 { return x }}},
	}})
	if !errors.HasCode(err, errors.PULSE_EXTENSION_NAME_RESERVED) {
		t.Fatalf("embedder total(): %v, want PULSE_EXTENSION_NAME_RESERVED", err)
	}
}

// TestFormulaTotal_MMMSubsetFactor: the MMM panel's conditional-subset
// scenario factor — 1 - 1000 / (TV spend over the second year) — is
// 0.80866 to five places, and the scenario column is spend × factor.
func TestFormulaTotal_MMMSubsetFactor(t *testing.T) {
	csv, err := os.ReadFile("docs/roadmap/fixtures/mmm-harbor-pine/harbor_pine_mmm.csv")
	if err != nil {
		t.Fatal(err)
	}
	fsys := afero.NewMemMapFs()
	if err := afero.WriteFile(fsys, "mmm.csv", csv, 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	res, err := p.ImportFile(ctx, pulse.ImportSpec{SourcePath: "mmm.csv"})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	resp, err := p.Process(ctx, &types.Request{
		Cohort: &types.Cohort{Filename: res.Path},
		Attributes: []*types.Attribute{
			{Type: types.ATTR_FORMULA, Expression: "1 - 1000 / total(week_index >= 52 ? tv_spend_k : 0)", Label: "factor"},
			{Type: types.ATTR_FORMULA, Expression: "tv_spend_k * (1 - 1000 / total(week_index >= 52 ? tv_spend_k : 0))", Label: "tv_scn"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_MAX, Field: "factor", Label: "factor"},
			{Type: types.AGG_SUM, Field: "tv_scn", Label: "tv_scn"},
			{Type: types.AGG_SUM, Field: "tv_spend_k", Label: "tv"},
		},
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	f := resp.Data[0]["factor"].(float64)
	if got := math.Round(f*1e5) / 1e5; got != 0.80866 {
		t.Fatalf("factor %v (%.5f), want 0.80866", f, f)
	}
	scn, tv := resp.Data[0]["tv_scn"].(float64), resp.Data[0]["tv"].(float64)
	if math.Abs(scn-tv*f) > 1e-9*tv {
		t.Fatalf("scenario Σ %v, want Σspend × factor = %v", scn, tv*f)
	}
}

// TestFormulaTotal_PartitionedRouting: on each parallel arm — the
// per-shard reducer (shard archive, ShardWorkers > 1) and the parallel
// buffered decode (single file over the threshold, DecodeWorkers > 1)
// — a plain formula still takes the parallel arm, while a total()
// formula falls back to the sequential arm and equals the sequential
// single-file run bit for bit (a partition-local total would differ).
// Each arm is falsified on its own gate: shard_reduce.go's and
// parallel_decode.go's CanMergeRequestWithExtensions checks.
func TestFormulaTotal_PartitionedRouting(t *testing.T) {
	large := parityLargeCohort(t.TempDir())
	type arm struct {
		name     string
		parallel observe.Arm
		rows     int
		// open returns the partitioned instance and the sequential
		// single-file reference (same rows), plus their cohort paths.
		open func(t *testing.T, rec *execRecorder) (p *pulse.Pulse, path string, ref *pulse.Pulse, refPath string)
	}
	arms := []arm{
		{"shard_archive", observe.ArmShardParallel, 120, func(t *testing.T, rec *execRecorder) (*pulse.Pulse, string, *pulse.Pulse, string) {
			fsys := afero.NewMemMapFs()
			s := paritySchema(t)
			shards := []string{"s0.pulse", "s1.pulse", "s2.pulse"}
			for i, path := range shards {
				writeParityCohort(t, fsys, path, s, i*40, (i+1)*40)
			}
			writeParityCohort(t, fsys, "whole.pulse", s, 0, 120)
			p := execNew(t, pulse.Options{FS: fsys, ShardWorkers: 3}, rec)
			if _, err := p.CreateShardArchive(context.Background(), "archive.pulse", shards); err != nil {
				t.Fatalf("CreateShardArchive: %v", err)
			}
			ref, err := pulse.New(pulse.Options{FS: fsys, ShardWorkers: 1, DecodeWorkers: 1})
			if err != nil {
				t.Fatal(err)
			}
			return p, "archive.pulse", ref, "whole.pulse"
		}},
		{"parallel_decode", observe.ArmParallelDecode, parityLargeRows, func(t *testing.T, rec *execRecorder) (*pulse.Pulse, string, *pulse.Pulse, string) {
			dir := large(t)
			ref, err := pulse.New(pulse.Options{DataDir: dir, ShardWorkers: 1, DecodeWorkers: 1})
			if err != nil {
				t.Fatal(err)
			}
			return execNew(t, pulse.Options{DataDir: dir, DecodeWorkers: 4}, rec), "large.pulse", ref, "large.pulse"
		}},
	}
	ctx := context.Background()
	for _, a := range arms {
		t.Run(a.name, func(t *testing.T) {
			rec := &execRecorder{}
			p, path, ref, refPath := a.open(t, rec)
			lastArm := func() observe.Arm {
				rec.mu.Lock()
				defer rec.mu.Unlock()
				if len(rec.ends) == 0 {
					t.Fatal("no operation recorded")
				}
				return rec.ends[len(rec.ends)-1].Arm
			}

			plain := ftReq(path, &types.Attribute{Type: types.ATTR_FORMULA, Expression: "(score ?? 0) / 3", Label: "out"})
			if _, err := p.Process(ctx, plain); err != nil {
				t.Fatalf("plain Process: %v", err)
			}
			if got := lastArm(); got != a.parallel {
				t.Fatalf("plain formula took arm %q, want %q (still mergeable)", got, a.parallel)
			}

			total := &types.Attribute{Type: types.ATTR_FORMULA, Expression: "total(score / 3)", Label: "out"}
			resp, err := p.Process(ctx, ftReq(path, total))
			if err != nil {
				t.Fatalf("total Process: %v", err)
			}
			if got := lastArm(); got == a.parallel {
				t.Fatalf("total() formula took the parallel arm %q", got)
			}
			want, err := ref.Process(ctx, ftReq(refPath, total))
			if err != nil {
				t.Fatalf("reference Process: %v", err)
			}
			wv, _ := want.Data[0]["max"].(float64)
			ftBits(t, "reference max", wv, ftScoreSum(a.rows, ftKeep, ftThird))
			ftBits(t, "partitioned max", resp.Data[0]["max"], wv)
			ftBits(t, "partitioned min", resp.Data[0]["min"], wv)
		})
	}
}
