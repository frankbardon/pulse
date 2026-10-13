package processing

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

// The MATRIX_RESULT overlay host: Request.Overlays kinds that read the
// request's finished Response.Matrices (types.MatrixValues, never the
// crosstab MatrixPayload, so no crosstab handler can run here). A spec's
// OverlayRef.Matrix names one Request.Matrices spec by its effective
// name and the handler reads EVERY group bucket of its result.
//
// Ordering: the service applies this pass AFTER the multiplicity fold
// (foldRequestMultiplicity on a process request, foldComposeMultiplicity
// at the Compose barrier for a slot's own overlays), so a handler reads
// each result exactly as the caller receives it — auxiliary.p_adjusted
// / significant_adjusted included. The crosstab and series hooks leave
// a MATRIX_RESULT spec's position a zero layer; this pass fills it, so
// Response.Overlays stays index-aligned with Request.Overlays.

// MatrixResultHostView is the MATRIX_RESULT host one spec reads: the
// Response.Matrices results of the spec its Ref.Matrix names, in
// Response.Matrices order (the grouped run's final bucket order).
type MatrixResultHostView struct {
	// Name is the matrix spec's effective name.
	Name string
	// Results are the spec's results, one per group bucket.
	Results []*types.MatrixResult
}

// matrixResultHandler is one MATRIX_RESULT kind's handler.
type matrixResultHandler func(spec *types.OverlaySpec, host *MatrixResultHostView) (types.OverlayLayer, []types.OverlayWarning, error)

// matrixResultOverlayHandlers is the MATRIX_RESULT host's dispatch
// table; overlayhost's processShapes declares exactly these kinds on
// ShapeMatrixResult (TestOverlayHostTable_MatchesRuntimeHandlers).
var matrixResultOverlayHandlers = map[types.OverlayKind]matrixResultHandler{
	types.OverlayKindMatrixGroupSummary: applyMatrixGroupSummary,
}

// newMatrixResultHostView collects the results of the spec named name.
func newMatrixResultHostView(name string, results []types.MatrixResult) *MatrixResultHostView {
	host := &MatrixResultHostView{Name: name}
	for k := range results {
		if results[k].Name == name {
			host.Results = append(host.Results, &results[k])
		}
	}
	return host
}

// AppliesMatrixResultOverlays reports whether req carries a computed
// MATRIX_RESULT overlay spec — the pass ApplyMatrixResultOverlays runs.
func AppliesMatrixResultOverlays(req *types.Request, exts *ExtensionRegistry, compute ComputePlan) bool {
	return len(matrixResultSpecs(req, exts, compute)) > 0
}

// matrixResultSpecs is the indexes of req's computed MATRIX_RESULT
// specs (by route), in spec order.
func matrixResultSpecs(req *types.Request, exts *ExtensionRegistry, compute ComputePlan) []int {
	if req == nil || len(req.Overlays) == 0 || !compute.Overlays {
		return nil
	}
	keep := compute.overlayKeep()
	var out []int
	for i := range req.Overlays {
		if keep != nil && !keep(i) {
			continue
		}
		if overlayhost.RunsOnMatrixResult(exts.overlayRoute(req.Overlays[i].Kind)) {
			out = append(out, i)
		}
	}
	return out
}

// ApplyMatrixResultOverlays runs every computed MATRIX_RESULT spec of
// req.Overlays against resp.Matrices and writes its layer at the spec's
// own index of resp.Overlays (allocating index-aligned zero layers when
// no other hook did); handler warnings join resp.Warnings. A request
// with no such spec is a no-op, so resp stays byte-identical. Every
// refusal is the shared one predict raises: the slot and shape
// refusals, then overlayhost.MatrixResultRefusal; a handler's parts are
// judged by overlayhost.PartsRefusal. The results are read, never
// written.
func ApplyMatrixResultOverlays(req *types.Request, resp *types.Response, exts *ExtensionRegistry, compute ComputePlan) error {
	idx := matrixResultSpecs(req, exts, compute)
	if len(idx) == 0 || resp == nil {
		return nil
	}
	authored := AuthoredHostShape(req)
	matrices := len(req.Matrices) > 0
	layers := resp.Overlays
	if len(layers) != len(req.Overlays) {
		layers = make([]types.OverlayLayer, len(req.Overlays))
		copy(layers, resp.Overlays)
	}
	var warnings []types.OverlayWarning
	for _, i := range idx {
		workOverlayLayerRuns.Add(1)
		spec := &req.Overlays[i]
		route := exts.overlayRoute(spec.Kind)
		if cerr := overlayhost.Refusal(overlayhost.Request, spec.Kind, route, i); cerr != nil {
			return cerr
		}
		if cerr := overlayhost.ShapeRefusal(overlayhost.JudgedShape(authored, matrices, route), spec.Kind, route, i); cerr != nil {
			return cerr
		}
		if cerr := overlayhost.MatrixResultRefusal(req, spec, route, i); cerr != nil {
			return cerr
		}
		handler, ok := matrixResultOverlayHandlers[route]
		if !ok {
			return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_KIND_UNKNOWN,
				"overlay kind has no runtime handler: "+string(spec.Kind),
				map[string]any{"index": i, "kind": string(spec.Kind)})
		}
		layer, ws, err := handler(spec, newMatrixResultHostView(spec.Ref.Matrix.Name, resp.Matrices))
		if err != nil {
			return err
		}
		if cerr := overlayhost.PartsRefusal(&layer, i); cerr != nil {
			return cerr
		}
		layers[i] = layer
		warnings = append(warnings, ws...)
	}
	resp.Overlays = layers
	for _, w := range warnings {
		resp.Warnings = append(resp.Warnings, &types.ResponseWarning{Code: w.Code, Message: w.Message, Details: w.Details})
	}
	return nil
}

// matrixResultRowHeader / matrixResultColumnHeader label a matrix
// result's member axes on an overlay MATRIX payload: each key is a
// one-element AxisKey holding the member (or, on a rectangular result,
// column) name.
var (
	matrixResultRowHeader    = types.AxisHeader{Fields: []string{"row"}, Types: []string{"matrix_key"}}
	matrixResultColumnHeader = types.AxisHeader{Fields: []string{"column"}, Types: []string{"matrix_key"}}
)

// ExpandMatrixValues returns v's cells as a dense rows × columns grid:
// a "full" matrix as is (copied), an "upper" square_symmetric one with
// its lower triangle mirrored from the upper (row r of an upper
// encoding holds columns r … p-1). A short or ragged row leaves NaN.
func ExpandMatrixValues(v *types.MatrixValues) [][]float64 {
	if v == nil {
		return nil
	}
	nr, nc := len(v.RowKeys), len(v.ColumnKeys)
	out := make([][]float64, nr)
	for r := range out {
		out[r] = make([]float64, nc)
		for c := range out[r] {
			out[r][c] = math.NaN()
		}
	}
	upper := v.Encoding == types.MatrixEncodingUpper && v.Kind == types.MatrixKindSquareSymmetric
	for r := 0; r < nr && r < len(v.Values); r++ {
		for k, x := range v.Values[r] {
			c := k
			if upper {
				c = r + k
			}
			if c >= nc {
				break
			}
			out[r][c] = x
			if upper && c < nr && r < nc {
				out[c][r] = x
			}
		}
	}
	return out
}

// applyMatrixGroupSummary is the OVERLAY_MATRIX_GROUP_SUMMARY handler:
// a grouped matrix spec summarized cell by cell across its group
// buckets.
//
//	keys      = the union of every bucket's row keys / column keys, in
//	            first-seen order (bucket order, then axis order)
//	x_b(i,j)  = bucket b's Primary cell, its "upper" encoding expanded
//	G(i,j)    = the buckets whose cell (i, j) exists and is finite
//	mean      = Σ_{b ∈ G} x_b / |G|   (unweighted, bucket order)
//	min / max = over G; range = max − min; n_groups = |G|
//
// A NaN (null) cell, or a key a bucket lacks, contributes nothing; a
// cell with no contributing bucket is NaN (null) in mean / min / max /
// range and 0 in n_groups. Primary MATRIX: the mean; parts min, max,
// range, n_groups (MATRIX each), every cell present. Descriptive: no
// p-value, no warning.
func applyMatrixGroupSummary(spec *types.OverlaySpec, host *MatrixResultHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
	var rowKeys, colKeys []string
	rowAt, colAt := map[string]int{}, map[string]int{}
	type bucket struct {
		rows, cols []int
		grid       [][]float64
	}
	buckets := make([]bucket, 0, len(host.Results))
	for _, res := range host.Results {
		if res == nil || res.Primary == nil {
			continue
		}
		b := bucket{grid: ExpandMatrixValues(res.Primary)}
		for _, k := range res.Primary.RowKeys {
			at, ok := rowAt[k]
			if !ok {
				at = len(rowKeys)
				rowAt[k] = at
				rowKeys = append(rowKeys, k)
			}
			b.rows = append(b.rows, at)
		}
		for _, k := range res.Primary.ColumnKeys {
			at, ok := colAt[k]
			if !ok {
				at = len(colKeys)
				colAt[k] = at
				colKeys = append(colKeys, k)
			}
			b.cols = append(b.cols, at)
		}
		buckets = append(buckets, b)
	}
	nr, nc := len(rowKeys), len(colKeys)
	grid := func(fill float64) [][]float64 {
		g := make([][]float64, nr)
		for i := range g {
			g[i] = make([]float64, nc)
			for j := range g[i] {
				g[i][j] = fill
			}
		}
		return g
	}
	sum, count := grid(0), grid(0)
	lo, hi := grid(math.Inf(1)), grid(math.Inf(-1))
	for _, b := range buckets {
		for r, i := range b.rows {
			for c, j := range b.cols {
				x := b.grid[r][c]
				if math.IsNaN(x) || math.IsInf(x, 0) {
					continue
				}
				sum[i][j] += x
				count[i][j]++
				lo[i][j] = math.Min(lo[i][j], x)
				hi[i][j] = math.Max(hi[i][j], x)
			}
		}
	}
	mean, rng := grid(math.NaN()), grid(math.NaN())
	for i := range mean {
		for j := range mean[i] {
			if count[i][j] == 0 {
				lo[i][j], hi[i][j] = math.NaN(), math.NaN()
				continue
			}
			mean[i][j] = sum[i][j] / count[i][j]
			rng[i][j] = hi[i][j] - lo[i][j]
		}
	}
	rowAxis := make([]types.AxisKey, nr)
	for i, k := range rowKeys {
		rowAxis[i] = types.AxisKey{k}
	}
	colAxis := make([]types.AxisKey, nc)
	for j, k := range colKeys {
		colAxis[j] = types.AxisKey{k}
	}
	table := func(values [][]float64, label string) *types.MatrixPayload {
		cells := make([][]types.MatrixCell, nr)
		for i := range cells {
			cells[i] = make([]types.MatrixCell, nc)
			for j := range cells[i] {
				cells[i][j] = types.MatrixCell{Value: values[i][j], Present: true}
			}
		}
		return &types.MatrixPayload{
			RowHeader:        matrixResultRowHeader,
			ColumnHeader:     matrixResultColumnHeader,
			RowKeys:          append([]types.AxisKey{}, rowAxis...),
			ColumnKeys:       append([]types.AxisKey{}, colAxis...),
			Cells:            cells,
			CellLabel:        label,
			NormalizeApplied: types.CrosstabNormalizeNone,
		}
	}
	part := func(values [][]float64, label string) *types.OverlayPayload {
		return &types.OverlayPayload{Shape: types.OverlayShapeMatrix, Matrix: table(values, label)}
	}
	layer := types.OverlayLayer{
		Name:  overlayLayerName(spec),
		Kind:  spec.Kind,
		Scope: spec.Scope,
		Ref:   spec.Ref,
		Payload: types.OverlayPayload{
			Shape:  types.OverlayShapeMatrix,
			Matrix: table(mean, "mean"),
			Parts: map[string]*types.OverlayPayload{
				overlayhost.GroupSummaryPartMin:     part(lo, overlayhost.GroupSummaryPartMin),
				overlayhost.GroupSummaryPartMax:     part(hi, overlayhost.GroupSummaryPartMax),
				overlayhost.GroupSummaryPartRange:   part(rng, overlayhost.GroupSummaryPartRange),
				overlayhost.GroupSummaryPartNGroups: part(count, overlayhost.GroupSummaryPartNGroups),
			},
		},
	}
	return layer, nil, nil
}
