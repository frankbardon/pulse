package overlayhost

import (
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestExtensionReadsCellValue pins the extension half of the
// categorical-code rule's cell classification (U66 #447): an extension
// kind reads a crosstab cell's VALUE on the Request slot iff it has a
// MATRIX Process handler, on the Compose slot iff it has a Compose
// handler — unless its registration declares ReadsCellCountsOnly; it
// never reads one on a slot an extension kind cannot run on.
func TestExtensionReadsCellValue(t *testing.T) {
	t.Parallel()

	matrix := []types.OverlayShape{types.OverlayShapeMatrix}
	series := []types.OverlayShape{types.OverlayShapeSeries}
	cases := []struct {
		name                      string
		decl                      *ExtensionDecl
		request, compose, nilDecl bool
	}{
		{name: "matrix_handler", decl: &ExtensionDecl{HostShapes: matrix}, request: true},
		{name: "matrix_counts_only", decl: &ExtensionDecl{HostShapes: matrix, ReadsCellCountsOnly: true}},
		{name: "series_handler", decl: &ExtensionDecl{HostShapes: series}},
		{name: "compose_handler", decl: &ExtensionDecl{Compose: true}, compose: true},
		{name: "compose_counts_only", decl: &ExtensionDecl{Compose: true, ReadsCellCountsOnly: true}},
		{name: "every_handler", decl: &ExtensionDecl{HostShapes: append(matrix, series...), Compose: true}, request: true, compose: true},
		{name: "every_handler_counts_only", decl: &ExtensionDecl{HostShapes: append(matrix, series...), Compose: true, ReadsCellCountsOnly: true}},
		{name: "nil", nilDecl: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := tc.decl
			if tc.nilDecl {
				d = nil
			}
			for host, want := range map[Host]bool{Request: tc.request, Compose: tc.compose, Chain: false, Facet: false} {
				if got := ExtensionReadsCellValue(d, host); got != want {
					t.Errorf("ExtensionReadsCellValue(%s) = %v, want %v", host, got, want)
				}
			}
		})
	}
}

// TestExtensionHonoursSeriesParams pins the opt-in series params (U66
// #713): params.field on the Request slot needs HonoursField and a
// SERIES handler, on the Compose slot HonoursField and a Compose
// handler; params.partition_by needs HonoursPartitionBy and a SERIES
// handler. Unset flags opt nothing in, whatever the handlers.
func TestExtensionHonoursSeriesParams(t *testing.T) {
	t.Parallel()

	series := []types.OverlayShape{types.OverlayShapeSeries}
	matrix := []types.OverlayShape{types.OverlayShapeMatrix}
	cases := []struct {
		name                        string
		decl                        *ExtensionDecl
		fieldReq, fieldComp, partBy bool
	}{
		{name: "nil"},
		{name: "unset", decl: &ExtensionDecl{HostShapes: series, Compose: true}},
		{name: "field_series", decl: &ExtensionDecl{HostShapes: series, HonoursField: true}, fieldReq: true},
		{name: "field_compose", decl: &ExtensionDecl{Compose: true, HonoursField: true}, fieldComp: true},
		{name: "field_matrix_only", decl: &ExtensionDecl{HostShapes: matrix, HonoursField: true}},
		{name: "partition_series", decl: &ExtensionDecl{HostShapes: series, HonoursPartitionBy: true}, partBy: true},
		{name: "partition_compose_only", decl: &ExtensionDecl{Compose: true, HonoursPartitionBy: true}},
		{name: "both_everywhere", decl: &ExtensionDecl{HostShapes: append(matrix, series...), Compose: true, HonoursField: true, HonoursPartitionBy: true}, fieldReq: true, fieldComp: true, partBy: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ExtensionHonoursField(tc.decl, Request); got != tc.fieldReq {
				t.Errorf("field on request = %v, want %v", got, tc.fieldReq)
			}
			if got := ExtensionHonoursField(tc.decl, Compose); got != tc.fieldComp {
				t.Errorf("field on compose = %v, want %v", got, tc.fieldComp)
			}
			if ExtensionHonoursField(tc.decl, Chain) || ExtensionHonoursField(tc.decl, Facet) {
				t.Error("field honoured on a slot no extension kind runs on")
			}
			if got := ExtensionHonoursPartitionBy(tc.decl); got != tc.partBy {
				t.Errorf("partition_by = %v, want %v", got, tc.partBy)
			}
		})
	}
}

// TestExtensionPartitionOutputRefusal: a partition's output must be a
// SERIES payload with one entry per partition row.
func TestExtensionPartitionOutputRefusal(t *testing.T) {
	t.Parallel()

	d := &ExtensionDecl{Kind: "OVERLAY_ACME_TREND", Shapes: []types.OverlayShape{types.OverlayShapeSeries, types.OverlayShapeScalar}}
	two := &types.OverlayPayload{Shape: types.OverlayShapeSeries, Series: &types.SeriesPayload{Entries: make([]types.SeriesEntry, 2)}}
	if cerr := ExtensionPartitionOutputRefusal(d, two, 2, 0); cerr != nil {
		t.Fatalf("aligned output refused: %v", cerr)
	}
	x := 1.0
	for name, c := range map[string]struct {
		p       *types.OverlayPayload
		entries int
	}{
		"short":  {two, 2},
		"scalar": {&types.OverlayPayload{Shape: types.OverlayShapeScalar, Scalar: &x}, -1},
	} {
		rows := 3
		cerr := ExtensionPartitionOutputRefusal(d, c.p, rows, 4)
		if cerr == nil || cerr.Code != "PULSE_EXTENSION_OVERLAY_OUTPUT_MISMATCH" || cerr.Details["reason"] != "partition_misaligned" ||
			cerr.Details["rows"] != rows || cerr.Details["entries"] != c.entries || cerr.Details["index"] != 4 {
			t.Errorf("%s: %v", name, cerr)
		}
	}
}
