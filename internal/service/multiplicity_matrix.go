package service

import (
	"math"
	"strconv"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// multiplicity_matrix.go is the matrix half of the multiple-comparison
// fold (U28 E3-S4): a matrix spec under params.inference carries its
// raw p-values in each result's auxiliary.p (a types.MatrixValues — the
// dedicated matrix type, never the crosstab MatrixPayload the overlay
// writers read), and a member spec gains auxiliary.p_adjusted and
// auxiliary.significant_adjusted beside it plus the
// MatrixResult.Multiplicity echo. auxiliary.p is never written.
//
// The hypotheses of a square_symmetric result are its off-diagonal
// UPPER-triangle cells: cell (r, c) and its mirror (c, r) are one test
// and the diagonal is no test at all, so p members give m = p(p-1)/2
// (R: p.adjust over the cor.test p-values of the distinct pairs). The
// adjusted figure is written to both triangles under the `full`
// encoding; the diagonal stays NaN (null). A rectangular result (none
// of today's inferential operators emits one) files every cell.
//
// Families: `matrix` keys one family per result — per spec and per
// group bucket (Response.Matrices index) — inside the request's scope;
// `request` shares multFamilyKey with the tests, post-tests and
// request-family overlays, so every bucket of the spec pools with them.
//
// Ordering: collectRequestSites files matrices LAST (after tests,
// post-tests and overlay layers), so a pooled `request` family lists
// its members in that order. The fold reads Response.Matrices as the
// dispatch arm finished it — before any overlay that reads matrix
// results is applied: the MATRIX_RESULT overlay pass runs after
// foldRequestMultiplicity (matrix_result_overlay.go), so it reads
// p_adjusted.

// Auxiliary keys the matrix fold writes beside auxiliary.p.
const (
	matrixAuxP                   = "p"
	matrixAuxPAdjusted           = "p_adjusted"
	matrixAuxSignificantAdjusted = "significant_adjusted"
)

// collectMatrixResultSites files every member matrix spec's p-values under
// its family. A result pairs with its slot by name (one result per
// spec, or one per spec per group bucket); a result without
// auxiliary.p (none computed) or without an off-diagonal cell
// contributes nothing and gains no twin.
func collectMatrixResultSites(fams *multFamilies, scope string, slots []descx.ResolvedMultiplicity, results []types.MatrixResult) {
	if len(slots) == 0 || len(results) == 0 {
		return
	}
	byName := make(map[string]int, len(slots))
	for i, rm := range slots {
		if rm.Member {
			byName[rm.Name] = i
		}
	}
	if len(byName) == 0 {
		return
	}
	for k := range results {
		res := &results[k]
		i, ok := byName[res.Name]
		if !ok {
			continue
		}
		rm := slots[i]
		raw := res.Auxiliary[matrixAuxP]
		cells := matrixPCells(raw)
		if len(cells) == 0 {
			continue
		}
		var key string
		switch rm.Family {
		case types.MultiplicityFamilyMatrix:
			key = scope + rm.Slot + "#" + string(rm.Family) + ":" + strconv.Itoa(k)
		default:
			key = multFamilyKey(scope, rm.Family)
		}
		adj, sig := nanTwin(raw), nanTwin(raw)
		res.Auxiliary[matrixAuxPAdjusted] = adj
		res.Auxiliary[matrixAuxSignificantAdjusted] = sig
		method, family, alpha := rm.Method, rm.Family, rm.Alpha
		for _, c := range cells {
			fams.add(key, family, method, multSite{
				p: c.p,
				write: func(adjusted float64, m, excluded int) {
					flag := math.NaN()
					if !math.IsNaN(adjusted) {
						flag = 0
						if adjusted < alpha {
							flag = 1
						}
					}
					setMatrixCell(adj, c.r, c.c, adjusted)
					setMatrixCell(sig, c.r, c.c, flag)
					res.Multiplicity = &types.AppliedMultiplicity{Method: method, Family: family, Alpha: alpha, M: m, Excluded: excluded}
				},
			})
		}
	}
}

// matrixPCell is one hypothesis of a p matrix: its logical (row,
// column) coordinate and raw p.
type matrixPCell struct {
	r, c int
	p    float64
}

// matrixPCells lists the hypotheses of p in row-major order: the
// off-diagonal upper triangle of a square_symmetric matrix (whatever
// its encoding), every cell of a rectangular one. Nil for a nil matrix.
func matrixPCells(p *types.MatrixValues) []matrixPCell {
	if p == nil {
		return nil
	}
	var out []matrixPCell
	if p.Kind != types.MatrixKindSquareSymmetric {
		for r, row := range p.Values {
			for c, v := range row {
				out = append(out, matrixPCell{r: r, c: c, p: v})
			}
		}
		return out
	}
	n := len(p.RowKeys)
	for r := 0; r < n; r++ {
		for c := r + 1; c < n; c++ {
			v, ok := matrixCell(p, r, c)
			if !ok {
				continue
			}
			out = append(out, matrixPCell{r: r, c: c, p: v})
		}
	}
	return out
}

// matrixCell reads logical cell (r, c) of m: Values[r][c] under the
// full encoding (and on a rectangular matrix), Values[r][c-r] under the
// upper one (c >= r only). ok is false when the cell is not stored.
func matrixCell(m *types.MatrixValues, r, c int) (float64, bool) {
	if m.Kind == types.MatrixKindSquareSymmetric && m.Encoding == types.MatrixEncodingUpper {
		c -= r
	}
	if r < 0 || r >= len(m.Values) || c < 0 || c >= len(m.Values[r]) {
		return 0, false
	}
	return m.Values[r][c], true
}

// setMatrixCell writes v at logical cell (r, c) of m and, on a full
// square_symmetric matrix, at its mirror (c, r) too.
func setMatrixCell(m *types.MatrixValues, r, c int, v float64) {
	put := func(r, c int) {
		if m.Kind == types.MatrixKindSquareSymmetric && m.Encoding == types.MatrixEncodingUpper {
			c -= r
		}
		if r >= 0 && r < len(m.Values) && c >= 0 && c < len(m.Values[r]) {
			m.Values[r][c] = v
		}
	}
	put(r, c)
	if m.Kind == types.MatrixKindSquareSymmetric && m.Encoding != types.MatrixEncodingUpper && r != c {
		put(c, r)
	}
}

// nanTwin is a matrix of p's shape, kind, encoding and keys with every
// cell NaN (null): the fold fills the hypotheses, the diagonal and any
// unfilled cell stay null.
func nanTwin(p *types.MatrixValues) *types.MatrixValues {
	out := &types.MatrixValues{
		Kind:       p.Kind,
		Encoding:   p.Encoding,
		RowKeys:    append([]string(nil), p.RowKeys...),
		ColumnKeys: append([]string(nil), p.ColumnKeys...),
		Values:     make([][]float64, len(p.Values)),
	}
	if p.Labels != nil {
		out.Labels = append([]string(nil), p.Labels...)
	}
	for i, row := range p.Values {
		out.Values[i] = make([]float64, len(row))
		for j := range row {
			out.Values[i][j] = math.NaN()
		}
	}
	return out
}
