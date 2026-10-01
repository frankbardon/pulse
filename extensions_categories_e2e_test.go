package pulse_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// End-to-end proof that attributes, features, tests and windows
// authored purely against extend run through pulse.Process. Each
// extension doubles (or counts) a figure a built-in computes, so the
// expected value is a built-in result, not a hand-typed constant. The
// fuller cohort × execution-mode parity matrix lives in
// extensions_parity_test.go.

// e2eCalls counts which engine path reached each extension.
type e2eCalls struct {
	compute, row, prePass, finalize, emit, update, run atomic.Int64
}

func openE2E(t *testing.T, ext pulse.Extensions) (*pulse.Pulse, *types.Cohort) {
	t.Helper()
	fsys := afero.NewMemMapFs()
	writeParityCohort(t, fsys, "e2e.pulse", paritySchema(t), 0, paritySmallRows)
	p, err := pulse.New(pulse.Options{FS: fsys, Extensions: ext})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	return p, &types.Cohort{Filename: "e2e.pulse"}
}

func processE2E(t *testing.T, p *pulse.Pulse, req *types.Request) *types.Response {
	t.Helper()
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	return resp
}

// builtinScoreSum is AGG_SUM(score) from the built-in path.
func builtinScoreSum(t *testing.T, p *pulse.Pulse, cohort *types.Cohort) float64 {
	t.Helper()
	resp := processE2E(t, p, &types.Request{
		Cohort:       cohort,
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "s"}},
	})
	return e2eFloat(t, resp.Data[0]["s"])
}

func e2eFloat(t *testing.T, v any) float64 {
	t.Helper()
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("value %v (%T) is not float64", v, v)
	}
	return f
}

// ---- attributes --------------------------------------------------------

type e2eAttr struct{ calls *e2eCalls }

func (a e2eAttr) Compute(rows extend.Rows, field string) ([]float64, error) {
	a.calls.compute.Add(1)
	out := make([]float64, rows.Len())
	for i := range out {
		v, _ := rows.At(i).NumericValue(field)
		out[i] = 2 * v
	}
	return out, nil
}

type e2eAttrRow struct{ e2eAttr }

func (a e2eAttrRow) Row(rec extend.Record, field string) (float64, error) {
	a.calls.row.Add(1)
	v, _ := rec.NumericValue(field)
	return 2 * v, nil
}

// e2eAttrTwoPass refuses Row before Finalize, so an adapter that
// dropped the two-pass siblings cannot pass by luck.
type e2eAttrTwoPass struct {
	e2eAttrRow
	finalized *bool
}

func (a e2eAttrTwoPass) PrePass(extend.Record, string) error { a.calls.prePass.Add(1); return nil }
func (a e2eAttrTwoPass) Finalize() error {
	a.calls.finalize.Add(1)
	*a.finalized = true
	return nil
}
func (a e2eAttrTwoPass) Row(rec extend.Record, field string) (float64, error) {
	if !*a.finalized {
		return 0, fmt.Errorf("Row before Finalize")
	}
	return a.e2eAttrRow.Row(rec, field)
}

// TestExtensions_AttributesAuthoredAgainstExtend runs one extension
// attribute per mode and checks AGG_SUM over its output equals twice
// the built-in AGG_SUM(score), on both Process and ProcessStream.
func TestExtensions_AttributesAuthoredAgainstExtend(t *testing.T) {
	for _, mode := range []pulse.AttributeMode{pulse.AttributeModeBuffered, pulse.AttributeModeRowLocal, pulse.AttributeModeTwoPass} {
		t.Run(string(mode), func(t *testing.T) {
			calls := &e2eCalls{}
			factory := func(*types.Attribute, *encoding.Schema) (extend.AttributeComputer, error) {
				base := e2eAttr{calls: calls}
				switch mode {
				case pulse.AttributeModeRowLocal:
					return e2eAttrRow{base}, nil
				case pulse.AttributeModeTwoPass:
					return e2eAttrTwoPass{e2eAttrRow{base}, new(bool)}, nil
				}
				return base, nil
			}
			p, cohort := openE2E(t, pulse.Extensions{Attributes: []pulse.AttributeRegistration{{
				Name: "ATTR_ACME_E2E_DOUBLE", Factory: factory, Mode: mode,
			}}})
			want := 2 * builtinScoreSum(t, p, cohort)
			req := &types.Request{
				Cohort:       cohort,
				Attributes:   []*types.Attribute{{Type: "ATTR_ACME_E2E_DOUBLE", Field: "score", Label: "dbl"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "dbl", Label: "s"}},
			}
			if got := e2eFloat(t, processE2E(t, p, req).Data[0]["s"]); got != want {
				t.Errorf("Process: sum(dbl) = %v, want %v", got, want)
			}
			if got := e2eFloat(t, streamE2E(t, p, req)["s"]); got != want {
				t.Errorf("ProcessStream: sum(dbl) = %v, want %v", got, want)
			}
			if mode == pulse.AttributeModeTwoPass && calls.row.Load() > 0 &&
				(calls.prePass.Load() == 0 || calls.finalize.Load() == 0) {
				t.Errorf("two_pass Row driven without PrePass/Finalize: %+v", calls)
			}
			// Process gates attribute streaming on the built-in
			// AttributeType.Streamable(), so an extension attribute runs
			// buffered there today whatever its Mode; the Row / PrePass
			// forwarding is pinned at the adapter seam instead
			// (TestAdaptAttribute_ForwardsExactlyTheImplementedTier).
			if calls.compute.Load()+calls.row.Load() == 0 {
				t.Errorf("%s attribute never driven", mode)
			}
		})
	}
}

// streamE2E returns the last row of a ProcessStreamResult run (the
// only row for the ungrouped requests here).
func streamE2E(t *testing.T, p *pulse.Pulse, req *types.Request) map[string]any {
	t.Helper()
	sr, err := p.ProcessStreamResult(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessStreamResult: %v", err)
	}
	var last map[string]any
	n := 0
	for c := range sr.Chunks {
		last, n = c.Data, n+1
	}
	if done := <-sr.Done; done.Status != pulse.StreamCompleted {
		t.Fatalf("stream status = %v, err = %v", done.Status, done.Error)
	}
	if n == 0 || last == nil {
		t.Fatal("stream produced no rows")
	}
	return last
}

// ---- features ----------------------------------------------------------

type e2eFeat struct{ calls *e2eCalls }

func (f e2eFeat) Compute(rows extend.Rows, field string) (map[string]extend.FeatureOutput, error) {
	f.calls.compute.Add(1)
	out := extend.FeatureOutput{Values: make([]float64, rows.Len()), Nulls: make([]bool, rows.Len())}
	for i := 0; i < rows.Len(); i++ {
		v, ok := rows.At(i).NumericValue(field)
		out.Values[i], out.Nulls[i] = 2*v, !ok
	}
	return map[string]extend.FeatureOutput{"fdbl": out}, nil
}

type e2eFeatStreaming struct{ e2eFeat }

func (f e2eFeatStreaming) PrePass(extend.Record, string) error { f.calls.prePass.Add(1); return nil }
func (f e2eFeatStreaming) Finalize() error                     { f.calls.finalize.Add(1); return nil }
func (f e2eFeatStreaming) EmitRow(rec extend.Record, field string) (map[string]extend.FeatureOutput, error) {
	f.calls.emit.Add(1)
	v, ok := rec.NumericValue(field)
	if !ok {
		return map[string]extend.FeatureOutput{"fdbl": {Nulls: []bool{true}}}, nil
	}
	return map[string]extend.FeatureOutput{"fdbl": {Values: []float64{2 * v}}}, nil
}

// TestExtensions_FeaturesAuthoredAgainstExtend runs a buffered and a
// streaming extension feature; AGG_SUM over its column must equal twice
// the built-in AGG_SUM(score), and the streaming variant must actually
// be driven through EmitRow on the streaming run.
func TestExtensions_FeaturesAuthoredAgainstExtend(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("streaming=%v", streaming), func(t *testing.T) {
			calls := &e2eCalls{}
			factory := func(*types.Feature, *encoding.Schema) (extend.FeatureComputer, error) {
				if streaming {
					return e2eFeatStreaming{e2eFeat{calls}}, nil
				}
				return e2eFeat{calls}, nil
			}
			p, cohort := openE2E(t, pulse.Extensions{Features: []pulse.FeatureRegistration{{
				Name: "FEAT_ACME_E2E_DOUBLE", Factory: factory, Streamable: streaming,
			}}})
			want := 2 * builtinScoreSum(t, p, cohort)
			req := &types.Request{
				Cohort:       cohort,
				Features:     []*types.Feature{{Type: "FEAT_ACME_E2E_DOUBLE", Field: "score", Label: "fdbl"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "fdbl", Label: "s"}},
			}
			if got := e2eFloat(t, processE2E(t, p, req).Data[0]["s"]); got != want {
				t.Errorf("Process: sum(fdbl) = %v, want %v", got, want)
			}
			if got := e2eFloat(t, streamE2E(t, p, req)["s"]); got != want {
				t.Errorf("ProcessStream: sum(fdbl) = %v, want %v", got, want)
			}
			if streaming && calls.emit.Load() == 0 {
				t.Errorf("streaming feature never driven through EmitRow (compute=%d)", calls.compute.Load())
			}
			if !streaming && calls.emit.Load() != 0 {
				t.Error("buffered feature reported EmitRow calls")
			}
		})
	}
}

// ---- tests -------------------------------------------------------------

type e2eRowTest struct {
	calls *e2eCalls
	field string
	sum   *float64
}

func (r e2eRowTest) UpdateRow(rec extend.Record) error {
	r.calls.update.Add(1)
	if v, ok := rec.NumericValue(r.field); ok {
		*r.sum += v
	}
	return nil
}

func (r e2eRowTest) Finalize() (*types.TestResult, error) {
	return &types.TestResult{Statistic: *r.sum, PValue: 1, Alpha: 0.05}, nil
}

type e2ePostTest struct{ calls *e2eCalls }

func (r e2ePostTest) Run(rows []map[string]any) (*types.TestResult, error) {
	r.calls.run.Add(1)
	return &types.TestResult{Statistic: float64(len(rows)), PValue: 1, Alpha: 0.05}, nil
}

// TestExtensions_TestsAuthoredAgainstExtend runs a tier-1 test whose
// statistic is the score sum (must equal built-in AGG_SUM) and a tier-2
// test whose statistic is the number of result rows (four regions).
func TestExtensions_TestsAuthoredAgainstExtend(t *testing.T) {
	calls := &e2eCalls{}
	p, cohort := openE2E(t, pulse.Extensions{Tests: []pulse.TestRegistration{
		{
			Name: "TEST_ACME_E2E_SUM", Tier: pulse.TestTierRow, Streamable: true,
			RowFactory: func(spec *types.Test, _ *encoding.Schema) (extend.RowTest, error) {
				return e2eRowTest{calls: calls, field: spec.Field, sum: new(float64)}, nil
			},
		},
		{
			Name: "TEST_ACME_E2E_ROWS", Tier: pulse.TestTierPost,
			PostFactory: func(*types.Test, *encoding.Schema) (extend.PostTest, error) {
				return e2ePostTest{calls}, nil
			},
		},
	}})
	want := builtinScoreSum(t, p, cohort)
	resp := processE2E(t, p, &types.Request{
		Cohort:       cohort,
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "s"}},
		Tests:        []*types.Test{{Type: "TEST_ACME_E2E_SUM", Field: "score", Label: "sum"}},
		PostTests:    []*types.Test{{Type: "TEST_ACME_E2E_ROWS", Field: "s", Label: "rows"}},
	})
	if len(resp.Tests) != 1 || resp.Tests[0].Statistic != want {
		t.Errorf("tier-1 tests = %+v; want statistic %v", resp.Tests, want)
	}
	if len(resp.PostTests) != 1 || resp.PostTests[0].Statistic != float64(len(resp.Data)) || len(resp.Data) != 4 {
		t.Errorf("tier-2 tests = %+v over %d rows; want statistic 4", resp.PostTests, len(resp.Data))
	}
	if calls.update.Load() != paritySmallRows || calls.run.Load() != 1 {
		t.Errorf("UpdateRow calls = %d (want %d), Run calls = %d (want 1)", calls.update.Load(), paritySmallRows, calls.run.Load())
	}
}

// ---- windows -----------------------------------------------------------

type e2eWindow struct{}

func (e2eWindow) Compute(rows []map[string]any, partitions [][]int, label string) error {
	for _, part := range partitions {
		for pos, i := range part {
			rows[i][label] = float64(pos)
		}
	}
	return nil
}

// TestExtensions_WindowsAuthoredAgainstExtend runs an extension window
// that writes each row's ordinal within its partition; over one
// partition ordered by the group sum, the ordinals are 0..n-1 in sum
// order.
func TestExtensions_WindowsAuthoredAgainstExtend(t *testing.T) {
	var opts *extend.WindowOptions
	p, cohort := openE2E(t, pulse.Extensions{Windows: []pulse.WindowRegistration{{
		Name: "WIN_ACME_E2E_ORDINAL",
		Factory: func(_ *types.Window, o extend.WindowOptions) (extend.WindowComputer, error) {
			opts = &o
			return e2eWindow{}, nil
		},
	}}})
	resp := processE2E(t, p, &types.Request{
		Cohort:       cohort,
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "s"}},
		Windows: []*types.Window{{
			Type: "WIN_ACME_E2E_ORDINAL", Field: "s", Label: "ord",
			OrderBy: []types.OrderKey{{Field: "s"}},
		}},
	})
	if opts == nil {
		t.Fatal("window factory never called")
	}
	if len(resp.Data) != 4 {
		t.Fatalf("rows = %d, want 4", len(resp.Data))
	}
	seen := map[float64]float64{}
	for _, row := range resp.Data {
		ord, ok := row["ord"].(float64)
		if !ok {
			t.Fatalf("row %v missing ord", row)
		}
		seen[ord] = e2eFloat(t, row["s"])
	}
	for i := 0; i < 4; i++ {
		if _, ok := seen[float64(i)]; !ok {
			t.Fatalf("ordinals = %v; want 0..3", seen)
		}
		if i > 0 && seen[float64(i)] < seen[float64(i-1)] {
			t.Errorf("ordinal %d has sum %v below ordinal %d's %v", i, seen[float64(i)], i-1, seen[float64(i-1)])
		}
	}
}
