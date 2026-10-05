package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/internal/processing/multiplicity"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// --- unit: the fold over a hand-built plan + response -------------------

func member(slot string, method types.MultiplicityMethod) descx.ResolvedMultiplicity {
	return descx.ResolvedMultiplicity{Slot: slot, Method: method, Family: types.MultiplicityFamilyRequest, Member: true}
}

func nonMember(slot string) descx.ResolvedMultiplicity {
	return descx.ResolvedMultiplicity{Slot: slot, Method: types.MultiplicityMethodNone, Family: types.MultiplicityFamilyRequest}
}

func result(tt types.TestType, p float64) *types.TestResult {
	return &types.TestResult{Type: tt, PValue: p, Alpha: 0.05, RejectNull: p < 0.05}
}

func TestFoldRequestMultiplicity(t *testing.T) {
	holm, bh := types.MultiplicityMethodHolm, types.MultiplicityMethodBH
	nan := math.NaN()
	cases := []struct {
		name      string
		plan      *descx.MultiplicityPlan
		tests     []*types.TestResult
		postTests []*types.TestResult
		// wantP is the expected adjusted p per result (tests then
		// post-tests); a nil entry means the result stays uncorrected.
		wantP []*float64
		wantM int
	}{
		{
			name:  "holm pools three tests",
			plan:  &descx.MultiplicityPlan{Tests: []descx.ResolvedMultiplicity{member("tests[0]", holm), member("tests[1]", holm), member("tests[2]", holm)}},
			tests: []*types.TestResult{result(types.TEST_T, 0.01), result(types.TEST_CHISQ, 0.04), result(types.TEST_PEARSON_R, 0.03)},
			wantP: ptrs(0.03, 0.06, 0.06),
			wantM: 3,
		},
		{
			name: "tests and post-tests share the request family",
			plan: &descx.MultiplicityPlan{
				Tests:     []descx.ResolvedMultiplicity{member("tests[0]", bh)},
				PostTests: []descx.ResolvedMultiplicity{member("post_tests[0]", bh)},
			},
			tests:     []*types.TestResult{result(types.TEST_T, 0.02)},
			postTests: []*types.TestResult{result(types.TEST_TREND, 0.03)},
			wantP:     ptrs(0.03, 0.03),
			wantM:     2,
		},
		{
			name:  "NaN p is excluded from m and stays undefined",
			plan:  &descx.MultiplicityPlan{Tests: []descx.ResolvedMultiplicity{member("tests[0]", holm), member("tests[1]", holm), member("tests[2]", holm)}},
			tests: []*types.TestResult{result(types.TEST_T, 0.01), result(types.TEST_T, nan), result(types.TEST_T, 0.04)},
			wantP: ptrs(0.02, nan, 0.04),
			wantM: 2,
		},
		{
			name: "skipped Tukey HSD never joins",
			plan: &descx.MultiplicityPlan{
				Tests:     []descx.ResolvedMultiplicity{member("tests[0]", holm), member("tests[1]", holm)},
				PostTests: []descx.ResolvedMultiplicity{nonMember("post_tests[0]")},
			},
			tests:     []*types.TestResult{result(types.TEST_T, 0.01), result(types.TEST_T, 0.02)},
			postTests: []*types.TestResult{result(types.TEST_TUKEY_HSD, 0.001)},
			wantP:     []*float64{f(0.02), f(0.02), nil},
			wantM:     2,
		},
		{
			name: "Shapiro headline and Fisher sum count one member each",
			plan: &descx.MultiplicityPlan{Tests: []descx.ResolvedMultiplicity{member("tests[0]", holm), member("tests[1]", holm)}},
			tests: []*types.TestResult{
				{Type: types.TEST_SHAPIRO_WILK, PValue: 0.01, Alpha: 0.05, Details: map[string]any{"groups": []any{map[string]any{"p_value": 0.2}, map[string]any{"p_value": 0.01}}}},
				{Type: types.TEST_FISHER_EXACT, PValue: 0.02, Alpha: 0.05},
			},
			wantP: ptrs(0.02, 0.02),
			wantM: 2,
		},
		{
			name:  "method none is not a member",
			plan:  &descx.MultiplicityPlan{Tests: []descx.ResolvedMultiplicity{nonMember("tests[0]"), nonMember("tests[1]")}},
			tests: []*types.TestResult{result(types.TEST_T, 0.01), result(types.TEST_T, 0.02)},
			wantP: []*float64{nil, nil},
		},
		{
			name:  "nil plan",
			tests: []*types.TestResult{result(types.TEST_T, 0.01)},
			wantP: []*float64{nil},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := &types.Response{Tests: c.tests, PostTests: c.postTests}
			raw := snapshotRaw(append(append([]*types.TestResult{}, c.tests...), c.postTests...))
			if err := foldRequestMultiplicity(c.plan, resp); err != nil {
				t.Fatal(err)
			}
			all := append(append([]*types.TestResult{}, resp.Tests...), resp.PostTests...)
			assertRawUntouched(t, all, raw)
			for i, r := range all {
				want := c.wantP[i]
				if want == nil {
					if r.PAdjusted != nil || r.SignificantAdjusted != nil || r.Multiplicity != nil {
						t.Errorf("result %d corrected, want untouched: %+v", i, r)
					}
					continue
				}
				if r.PAdjusted == nil || r.Multiplicity == nil {
					t.Fatalf("result %d uncorrected", i)
				}
				if !sameFloat(*r.PAdjusted, *want) {
					t.Errorf("result %d p_adjusted = %v, want %v", i, *r.PAdjusted, *want)
				}
				if math.IsNaN(*want) {
					if r.SignificantAdjusted != nil {
						t.Errorf("result %d: undefined p carries significant_adjusted", i)
					}
				} else if r.SignificantAdjusted == nil || *r.SignificantAdjusted != (*want < r.Alpha) {
					t.Errorf("result %d significant_adjusted = %v, want %v", i, r.SignificantAdjusted, *want < r.Alpha)
				}
				if r.Multiplicity.M != c.wantM || r.Multiplicity.Alpha != r.Alpha || r.Multiplicity.Family != types.MultiplicityFamilyRequest {
					t.Errorf("result %d echo = %+v, want m=%d alpha=%v family=request", i, *r.Multiplicity, c.wantM, r.Alpha)
				}
			}
		})
	}
}

// TestMultiplicitySites_ScopeKeysFamilies pins the collector shape
// Compose (E3) builds on: `request` families stay inside their scope,
// `compose` members pool across scopes.
func TestMultiplicitySites_ScopeKeysFamilies(t *testing.T) {
	bonf := types.MultiplicityMethodBonferroni
	plan := func(family types.MultiplicityFamily) *descx.MultiplicityPlan {
		return &descx.MultiplicityPlan{Tests: []descx.ResolvedMultiplicity{
			{Slot: "tests[0]", Method: bonf, Family: family, Member: true},
			{Slot: "tests[1]", Method: bonf, Family: family, Member: true},
		}}
	}
	for _, c := range []struct {
		family types.MultiplicityFamily
		wantM  int
	}{
		{types.MultiplicityFamilyRequest, 2},
		{types.MultiplicityFamilyCompose, 4},
	} {
		t.Run(string(c.family), func(t *testing.T) {
			fams := newMultFamilies()
			a := &types.Response{Tests: []*types.TestResult{result(types.TEST_T, 0.01), result(types.TEST_T, 0.02)}}
			b := &types.Response{Tests: []*types.TestResult{result(types.TEST_T, 0.01), result(types.TEST_T, 0.02)}}
			collectRequestSites(fams, "requests[0]/", plan(c.family), a)
			collectRequestSites(fams, "requests[1]/", plan(c.family), b)
			if err := fams.fold(); err != nil {
				t.Fatal(err)
			}
			for _, r := range append(a.Tests, b.Tests...) {
				if r.Multiplicity == nil || r.Multiplicity.M != c.wantM {
					t.Fatalf("echo %+v, want m=%d", r.Multiplicity, c.wantM)
				}
				if want := math.Min(1, r.PValue*float64(c.wantM)); !sameFloat(*r.PAdjusted, want) {
					t.Errorf("p_adjusted %v, want %v", *r.PAdjusted, want)
				}
			}
		})
	}
}

func f(v float64) *float64 { return &v }

func ptrs(vs ...float64) []*float64 {
	out := make([]*float64, len(vs))
	for i, v := range vs {
		out[i] = f(v)
	}
	return out
}

func sameFloat(a, b float64) bool {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.IsNaN(a) && math.IsNaN(b)
	}
	return math.Abs(a-b) <= 1e-12
}

type rawTest struct {
	p      float64
	reject bool
	alpha  float64
}

func snapshotRaw(rs []*types.TestResult) []rawTest {
	out := make([]rawTest, len(rs))
	for i, r := range rs {
		out[i] = rawTest{r.PValue, r.RejectNull, r.Alpha}
	}
	return out
}

func assertRawUntouched(t *testing.T, rs []*types.TestResult, raw []rawTest) {
	t.Helper()
	for i, r := range rs {
		if !sameFloat(r.PValue, raw[i].p) || r.RejectNull != raw[i].reject || r.Alpha != raw[i].alpha {
			t.Errorf("result %d raw figures changed: p=%v reject=%v alpha=%v, was %+v", i, r.PValue, r.RejectNull, r.Alpha, raw[i])
		}
	}
}

// --- per-arm: the Service.Process post-hook ------------------------------

func multSchema() *encoding.Schema {
	g := encoding.NewDictionary()
	g.Add("a")
	g.Add("b")
	h := encoding.NewDictionary()
	h.Add("u")
	h.Add("v")
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "g", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 4, CsvColumnIdx: 1, Dictionary: g},
		{Name: "h", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 5, CsvColumnIdx: 2, Dictionary: h},
		{Name: "x", Type: encoding.FieldTypeF64, ByteOffset: 6, CsvColumnIdx: 3},
		{Name: "y", Type: encoding.FieldTypeF64, ByteOffset: 14, CsvColumnIdx: 4},
	}}
}

// multRows: g alternates, h leans on g (so g×h is associated), x shifts
// with g, y correlates with x.
func multRows(n, offset int) [][]uint64 {
	recs := make([][]uint64, n)
	for i := range recs {
		k := offset + i
		g := uint64(k % 2)
		h := g
		if k%5 == 0 {
			h = 1 - g
		}
		x := 5 + 2*math.Sin(float64(k)) + 0.6*float64(g)
		y := 0.4*x + math.Cos(1.7*float64(k))
		recs[i] = []uint64{uint64(k), g, h, math.Float64bits(x), math.Float64bits(y)}
	}
	return recs
}

func multTests(streamableOnly bool) []*types.Test {
	ts := []*types.Test{
		{Type: types.TEST_T, Field: "x", Params: json.RawMessage(`{"mu":5.2}`), Label: "one"},
		{Type: types.TEST_T, Field: "x", SplitBy: "g", Label: "two"},
		{Type: types.TEST_PEARSON_R, Field: "x", Field2: "y", Label: "r"},
	}
	if !streamableOnly {
		ts = append(ts,
			&types.Test{Type: types.TEST_SHAPIRO_WILK, Field: "x", SplitBy: "g", Label: "sw"},
			&types.Test{Type: types.TEST_FISHER_EXACT, Rows: "g", Cols: "h", Label: "fisher"},
		)
	}
	return ts
}

// assertProcessFold runs mk() twice — without a block (baseline) and
// with a request-level holm block — and checks the corrected run against
// the core applied to the baseline's raw p-values.
func assertProcessFold(t *testing.T, svc *Service, mk func() *types.Request) {
	t.Helper()
	ctx := context.Background()
	base, err := svc.Process(ctx, mk())
	if err != nil {
		t.Fatal(err)
	}
	req := mk()
	req.Multiplicity = &types.Multiplicity{Method: types.MultiplicityMethodHolm}
	got, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	all := append(append([]*types.TestResult{}, got.Tests...), got.PostTests...)
	baseAll := append(append([]*types.TestResult{}, base.Tests...), base.PostTests...)
	if len(all) < 2 || len(all) != len(baseAll) {
		t.Fatalf("fixture yields %d results (baseline %d); a family needs ≥2", len(all), len(baseAll))
	}
	assertRawUntouched(t, all, snapshotRaw(baseAll))
	ps := make([]float64, len(baseAll))
	for i, r := range baseAll {
		ps[i] = r.PValue
		if r.PAdjusted != nil || r.Multiplicity != nil {
			t.Fatalf("baseline result %d corrected without a block", i)
		}
	}
	want, err := multiplicity.Adjust(multiplicity.MethodHolm, ps)
	if err != nil {
		t.Fatal(err)
	}
	m := multiplicity.FamilySize(ps)
	moved := false
	for i, r := range all {
		if r.PAdjusted == nil || r.Multiplicity == nil {
			t.Fatalf("result %d (%s) uncorrected", i, r.Type)
		}
		if !sameFloat(*r.PAdjusted, want[i]) {
			t.Errorf("result %d (%s) p_adjusted %v, want %v", i, r.Type, *r.PAdjusted, want[i])
		}
		if r.Multiplicity.M != m || r.Multiplicity.Method != types.MultiplicityMethodHolm {
			t.Errorf("result %d echo %+v, want holm m=%d", i, *r.Multiplicity, m)
		}
		moved = moved || !sameFloat(*r.PAdjusted, r.PValue)
	}
	if !moved {
		t.Fatal("no adjusted p differs from its raw one; the fixture proves nothing")
	}
}

// assertNoTestArmIdentical: a mergeable (test-free) request carrying a
// block answers byte-identically to the same request without one — the
// parallel arms exclude tests, so the fold has nothing to correct there.
func assertNoTestArmIdentical(t *testing.T, svc *Service, mk func() *types.Request) {
	t.Helper()
	ctx := context.Background()
	base, err := svc.Process(ctx, mk())
	if err != nil {
		t.Fatal(err)
	}
	req := mk()
	req.Multiplicity = &types.Multiplicity{Method: types.MultiplicityMethodHolm}
	got, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(base)
	b, _ := json.Marshal(got)
	if !bytes.Equal(a, b) {
		t.Errorf("test-free parallel response changed under a block:\n%s\n%s", a, b)
	}
}

func multArchive(t testing.TB, shardRows []int) []byte {
	t.Helper()
	schema := multSchema()
	var total uint64
	var doc, buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	payloads := make([][]byte, len(shardRows))
	offset := 0
	for i, n := range shardRows {
		payloads[i] = writeNullablePulse(t, schema, multRows(n, offset), nil)
		total += uint64(n)
		offset += n
	}
	if err := encx.WriteSchemaDoc(&doc, schema, total, uint16(len(shardRows))); err != nil {
		t.Fatal(err)
	}
	write := func(name string, b []byte) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	write(encx.ReservedSchemaName, doc.Bytes())
	for i := range payloads {
		write(fmt.Sprintf("s%d.pulse", i), payloads[i])
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestMultiplicityFold_ProcessArms(t *testing.T) {
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "m.pulse", writeNullablePulse(t, multSchema(), multRows(80, 0), nil), 0o644); err != nil {
		t.Fatal(err)
	}
	right := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "z", Type: encoding.FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1},
	}}
	rightRecs := make([][]uint64, 80)
	for i := range rightRecs {
		rightRecs[i] = []uint64{uint64(i), math.Float64bits(float64(i%7) + 0.1*math.Sin(float64(i)))}
	}
	if err := afero.WriteFile(cfg.Fs(), "r.pulse", writePulseFile(t, right, rightRecs), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(cfg.Fs(), "arch.pulse", multArchive(t, []int{30, 27, 33}), 0o644); err != nil {
		t.Fatal(err)
	}
	base := func(cohort string) *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: cohort},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
		}
	}

	t.Run("serial streaming", func(t *testing.T) {
		mk := func() *types.Request { r := base("m.pulse"); r.Tests = multTests(true); return r }
		if !processing.CanStreamRequest(mk(), multSchema()) {
			t.Fatal("fixture is not streamable; the arm would not be exercised")
		}
		assertProcessFold(t, New(cfg), mk)
	})
	t.Run("serial buffered", func(t *testing.T) {
		mk := func() *types.Request { r := base("m.pulse"); r.Tests = multTests(false); return r }
		if processing.CanStreamRequest(mk(), multSchema()) {
			t.Fatal("fixture streams; the buffered arm would not be exercised")
		}
		assertProcessFold(t, New(cfg), mk)
	})
	t.Run("crosstab", func(t *testing.T) {
		mk := func() *types.Request {
			return &types.Request{
				Cohort: &types.Cohort{Filename: "m.pulse"},
				Crosstab: &types.CrosstabSpec{
					Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
					Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "h"}},
					Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "x", Label: "n"},
				},
				Tests: multTests(false),
			}
		}
		assertProcessFold(t, New(cfg), mk)
	})
	t.Run("join", func(t *testing.T) {
		mk := func() *types.Request {
			r := base("m.pulse")
			r.Joins = []*types.JoinSpec{{Right: "r.pulse", Kind: "inner", As: "r_", On: []types.OnPair{{LeftField: "id", RightField: "id"}}}}
			r.Tests = []*types.Test{
				{Type: types.TEST_PEARSON_R, Field: "x", Field2: "r_z", Label: "xz"},
				{Type: types.TEST_T, Field: "r_z", Params: json.RawMessage(`{"mu":3.5}`), Label: "z"},
				{Type: types.TEST_T, Field: "x", SplitBy: "g", Label: "two"},
			}
			return r
		}
		assertProcessFold(t, New(cfg), mk)
	})
	t.Run("shard archive", func(t *testing.T) {
		svc := New(cfg)
		svc.SetShardWorkers(2)
		// Tests exclude the per-shard parallel arm, so the serial
		// shard iterator answers and the post-hook corrects it.
		mk := func() *types.Request { r := base("arch.pulse"); r.Tests = multTests(false); return r }
		assertProcessFold(t, svc, mk)
	})
	t.Run("shard parallel", func(t *testing.T) {
		svc := New(cfg)
		svc.SetShardWorkers(2)
		mk := func() *types.Request { return base("arch.pulse") }
		cohort, err := svc.Open(context.Background(), "arch.pulse")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := svc.shouldFanOut(mk(), cohort); !ok {
			t.Fatal("shouldFanOut refused; the parallel arm would not be exercised")
		}
		assertNoTestArmIdentical(t, svc, mk)
	})
	t.Run("process stream", func(t *testing.T) {
		// ProcessStream drains a Process response: same gates, same
		// fold. Its iterator surfaces rows only, which stay identical.
		svc := New(cfg)
		req := base("m.pulse")
		req.Tests = multTests(true)
		req.Multiplicity = &types.Multiplicity{Method: types.MultiplicityMethodHolm}
		it, err := svc.ProcessStream(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		defer it.Close()
		row, ok, err := it.Next(context.Background())
		if err != nil || !ok || row["n"] == nil {
			t.Fatalf("stream row = %v ok=%v err=%v", row, ok, err)
		}
	})
}

func TestMultiplicityFold_ParallelDecodeArm(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping parallel-decode multiplicity arm in -short mode")
	}
	const rowCount = parallelDecodeRecordThreshold + 4096
	dir := t.TempDir()
	osFs := afero.NewOsFs()
	path := dir + "/m.pulse"
	if err := afero.WriteFile(osFs, path, writeNullablePulse(t, multSchema(), multRows(rowCount, 0), nil), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	svc := New(cfg)
	svc.SetDecodeWorkers(4)
	mergeable := func() *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: path},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}},
		}
	}
	cohort, err := svc.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, perr := svc.processSingleFileParallelMaybe(context.Background(), mergeable(), cohort, path); perr != nil || !ok {
		t.Fatalf("parallel decode did not engage (ok=%v err=%v)", ok, perr)
	}
	t.Run("test-free parallel identical", func(t *testing.T) {
		assertNoTestArmIdentical(t, svc, mergeable)
	})
	t.Run("tests fall back and are corrected", func(t *testing.T) {
		// At this n every fixture p is ~0; aim the one-sample tests
		// just off the observed mean so the family holds moderate
		// p-values the correction visibly moves.
		avg := mergeable()
		avg.Aggregations = []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "x", Label: "m"}}
		resp, err := svc.Process(context.Background(), avg)
		if err != nil {
			t.Fatal(err)
		}
		mean := resp.Data[0]["m"].(float64)
		assertProcessFold(t, svc, func() *types.Request {
			r := mergeable()
			r.Tests = []*types.Test{
				{Type: types.TEST_T, Field: "x", Params: json.RawMessage(fmt.Sprintf(`{"mu":%v}`, mean+0.009)), Label: "a"},
				{Type: types.TEST_T, Field: "x", Params: json.RawMessage(fmt.Sprintf(`{"mu":%v}`, mean+0.005)), Label: "b"},
				{Type: types.TEST_PEARSON_R, Field: "x", Field2: "y", Label: "r"},
			}
			return r
		})
	})
}
