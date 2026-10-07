package pulse

import (
	"bytes"
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// benchDefaultReturnRowCount keeps the cohort SMALL on purpose: the
// fixed per-request cost of resolving a `return` block (the reflective
// type walk in descx.ResolveReturn) is measured against the cheapest
// realistic Process, so its share is an upper bound.
const benchDefaultReturnRowCount = 1_000

func writeBenchDefaultReturnCohort(b *testing.B, memFs afero.Fs) string {
	b.Helper()
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 0, CsvColumnIdx: 0},
		},
	}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		b.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		b.Fatalf("WriteSchema: %v", err)
	}
	for i := 0; i < benchDefaultReturnRowCount; i++ {
		v := float64((i*37)%1009) + 0.5
		if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeF64, math.Float64bits(v)); err != nil {
			b.Fatalf("WriteFieldValue: %v", err)
		}
	}
	const path = "bench_default_return.pulse"
	if err := afero.WriteFile(memFs, path, buf.Bytes(), 0o644); err != nil {
		b.Fatalf("WriteFile: %v", err)
	}
	return path
}

func benchDefaultReturnRequest(path string) *Request {
	return &Request{
		Cohort: &types.Cohort{Filename: path},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "sum_score"},
			{Type: types.AGG_AVERAGE, Field: "score", Label: "avg_score"},
		},
	}
}

// BenchmarkProcessDefaultReturn measures the cost of an instance
// `return` default (PRD FR-42, #219): a small Process with no default
// ("unset"), with a `full` default (resolution runs, nothing is
// skipped — the pure overhead), with the `standard` default MCP injects
// (resolution plus whatever the selection skips), and the resolution
// alone (descx.ResolveReturn against a snapshot carrying the default,
// the once-per-request walk Service.process performs). The resolve
// share of "unset" is the figure that decides whether a per-instance
// plan cache is worth building.
func BenchmarkProcessDefaultReturn(b *testing.B) {
	memFs := afero.NewMemMapFs()
	path := writeBenchDefaultReturnCohort(b, memFs)
	ctx := context.Background()

	cases := []struct {
		name string
		def  *types.Return
	}{
		{"unset", nil},
		{"full", &types.Return{Preset: types.ReturnPresetFull}},
		{"standard", &types.Return{Preset: types.ReturnPresetStandard}},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			p, err := New(Options{FS: memFs, DefaultReturn: tc.def})
			if err != nil {
				b.Fatalf("pulse.New: %v", err)
			}
			req := benchDefaultReturnRequest(path)
			b.ReportAllocs()
			for b.Loop() {
				if _, err := p.Process(ctx, req); err != nil {
					b.Fatalf("Process: %v", err)
				}
			}
		})
	}

	for _, preset := range []types.ReturnPreset{types.ReturnPresetFull, types.ReturnPresetStandard} {
		b.Run("resolve_"+string(preset), func(b *testing.B) {
			snap := (*descx.InstanceSnapshot)(nil).WithDefaultReturn(&types.Return{Preset: preset})
			req := benchDefaultReturnRequest(path)
			b.ReportAllocs()
			for b.Loop() {
				plan, err := descx.ResolveReturn(req, snap)
				if err != nil || plan == nil {
					b.Fatalf("ResolveReturn: plan=%v err=%v", plan, err)
				}
			}
		})
		b.Run("resolve_uncached_"+string(preset), func(b *testing.B) {
			req := benchDefaultReturnRequest(path)
			req.Return = &types.Return{Preset: preset}
			b.ReportAllocs()
			for b.Loop() {
				plan, err := descx.ResolveReturn(req, nil)
				if err != nil || plan == nil {
					b.Fatalf("ResolveReturn: plan=%v err=%v", plan, err)
				}
			}
		})
	}
}
