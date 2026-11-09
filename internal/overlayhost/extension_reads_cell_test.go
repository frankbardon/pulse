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
