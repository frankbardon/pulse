package processing

import (
	"encoding/json"
	stderrors "errors"
	"math"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// correspondenceCase is one correspondence.json case
// (scripts/reference/gen_correspondence.R). Signs are ca's own.
type correspondenceCase struct {
	Name         string                                      `json:"name"`
	Dimensions   int                                         `json:"dimensions"`
	Observed     [][]float64                                 `json:"observed"`
	KeptRows     []int                                       `json:"kept_rows"`
	KeptCols     []int                                       `json:"kept_cols"`
	Inertia      []float64                                   `json:"inertia"`
	TotalInertia float64                                     `json:"total_inertia"`
	RowMass      []float64                                   `json:"row_mass"`
	ColMass      []float64                                   `json:"col_mass"`
	ColStandard  [][]float64                                 `json:"col_standard"`
	Coordinates  map[string]struct{ Rows, Cols [][]float64 } `json:"coordinates"`
	RowQuality   []float64                                   `json:"row_quality"`
	ColQuality   []float64                                   `json:"col_quality"`
	RowContrib   [][]float64                                 `json:"row_contribution"`
	ColContrib   [][]float64                                 `json:"col_contribution"`
}

// linalgSigns returns, per dimension, the factor (±1) that maps ca's
// sign onto the linalg.SVD convention: the dominant component of the
// column singular vector V_k = sqrt(colmass) · standard column
// coordinate is positive (ties within DominanceTolerance go to the
// lowest index).
func linalgSigns(c correspondenceCase) []float64 {
	k := len(c.ColStandard[0])
	signs := make([]float64, k)
	for d := 0; d < k; d++ {
		v := make([]float64, len(c.ColMass))
		maxAbs := 0.0
		for j := range v {
			v[j] = math.Sqrt(c.ColMass[j]) * c.ColStandard[j][d]
			maxAbs = math.Max(maxAbs, math.Abs(v[j]))
		}
		signs[d] = 1
		for j := range v {
			if math.Abs(v[j]) >= (1-linalg.DominanceTolerance)*maxAbs {
				if v[j] < 0 {
					signs[d] = -1
				}
				break
			}
		}
	}
	return signs
}

func correspondenceSpec(params string) *types.OverlaySpec {
	s := &types.OverlaySpec{Kind: types.OverlayKindCorrespondence, Scope: types.OverlayScopeMatrix}
	if params != "" {
		s.Params = json.RawMessage(params)
	}
	return s
}

func cellFloat(t *testing.T, where string, c types.MatrixCell) float64 {
	t.Helper()
	v, ok := c.Value.(float64)
	if !c.Present || !ok {
		t.Fatalf("%s: cell %+v, want a present float64", where, c)
	}
	return v
}

func caClose(t *testing.T, where string, got, want, tol float64) {
	t.Helper()
	if d := math.Abs(got - want); d > tol*math.Max(1, math.Abs(want)) {
		t.Errorf("%s = %.17g, want %.17g (R)", where, got, want)
	}
}

// assertSignedMatrix compares a part matrix to R's (signs normalized
// per dimension column).
func assertSignedMatrix(t *testing.T, where string, got *types.OverlayPayload, want [][]float64, signs []float64, tol float64) {
	t.Helper()
	if got == nil || got.Shape != types.OverlayShapeMatrix || got.Matrix == nil || len(got.Matrix.Cells) != len(want) {
		t.Fatalf("%s: part %+v, want a %d-row MATRIX", where, got, len(want))
	}
	for i := range want {
		if len(got.Matrix.Cells[i]) != len(want[i]) {
			t.Fatalf("%s: row %d has %d dims, want %d", where, i, len(got.Matrix.Cells[i]), len(want[i]))
		}
		for k, w := range want[i] {
			caClose(t, where+"["+strconv.Itoa(i)+"]["+strconv.Itoa(k)+"]", cellFloat(t, where, got.Matrix.Cells[i][k]), signs[k]*w, tol)
		}
	}
}

func assertQualitySeries(t *testing.T, where string, got *types.OverlayPayload, want, mass []float64) {
	t.Helper()
	if got == nil || got.Shape != types.OverlayShapeSeries || got.Series == nil || len(got.Series.Entries) != len(want) {
		t.Fatalf("%s: part %+v, want a %d-entry SERIES", where, got, len(want))
	}
	for i, w := range want {
		e := got.Series.Entries[i]
		caClose(t, where+"["+strconv.Itoa(i)+"]", *e.Summary.Statistic, w, 1e-10)
		caClose(t, where+" mass", e.Summary.Parameters["mass"], mass[i], 1e-12)
	}
}

// TestCorrespondence_MatchesR: inertias, total inertia, masses, map
// coordinates under every normalization, quality and contribution equal
// ca::ca's after sign-normalizing each of ca's dimensions to the
// linalg.SVD convention — including a table with a zero row and a zero
// column, which Pulse drops (R sees the remainder).
func TestCorrespondence_MatchesR(t *testing.T) {
	doc := loadReference[correspondenceCase](t, "correspondence")
	norms := []string{"symmetric", "row_principal", "column_principal"}
	seen := 0
	for _, c := range doc.Cases {
		signs := linalgSigns(c)
		for _, norm := range norms {
			t.Run(c.Name+"/"+norm, func(t *testing.T) {
				want, ok := c.Coordinates[norm]
				if !ok {
					t.Fatalf("fixture has no %s coordinates", norm)
				}
				spec := correspondenceSpec(`{"dimensions":` + strconv.Itoa(c.Dimensions) + `,"normalization":"` + norm + `"}`)
				layer, ws, err := applyCorrespondence(spec, countHost(c.Observed))
				if err != nil {
					t.Fatal(err)
				}
				dropped := len(c.Observed) - len(c.KeptRows) + len(c.Observed[0]) - len(c.KeptCols)
				if (dropped > 0) != (len(ws) > 0) {
					t.Fatalf("warnings %+v with %d dropped points", ws, dropped)
				}
				entries := layer.Payload.Series.Entries
				if len(entries) != len(c.Inertia) {
					t.Fatalf("%d inertia entries, want %d", len(entries), len(c.Inertia))
				}
				for k, w := range c.Inertia {
					caClose(t, "inertia["+strconv.Itoa(k)+"]", *entries[k].Summary.Statistic, w, 1e-10)
				}
				caClose(t, "total inertia", *layer.Summary.Statistic, c.TotalInertia, 1e-12)
				p := layer.Payload.Parts
				assertSignedMatrix(t, "row_coordinates", p[CorrespondencePartRowCoordinates], want.Rows, signs, 1e-9)
				assertSignedMatrix(t, "column_coordinates", p[CorrespondencePartColumnCoordinates], want.Cols, signs, 1e-9)
				ones := make([]float64, len(signs))
				for k := range ones {
					ones[k] = 1
				}
				assertSignedMatrix(t, "row_contribution", p[CorrespondencePartRowContribution], c.RowContrib, ones, 1e-10)
				assertSignedMatrix(t, "column_contribution", p[CorrespondencePartColumnContribution], c.ColContrib, ones, 1e-10)
				assertQualitySeries(t, "row_quality", p[CorrespondencePartRowQuality], c.RowQuality, c.RowMass)
				assertQualitySeries(t, "column_quality", p[CorrespondencePartColumnQuality], c.ColQuality, c.ColMass)
			})
			seen++
		}
	}
	if seen < 9 {
		t.Fatalf("correspondence.json yields %d case × normalization runs, want at least 9", seen)
	}
}

// TestCorrespondence_GreenacreWorkedExample: the published principal
// inertias of Greenacre's smoking data (Greenacre, Correspondence
// Analysis in Practice, 2nd ed., 2007, Exhibit 4.x / the ca::smoke
// table) — 0.0748, 0.0100, 0.0004, total 0.0852 — and the first two
// dimensions' shares of 87.8% and 11.8%.
func TestCorrespondence_GreenacreWorkedExample(t *testing.T) {
	smoke := [][]float64{{4, 2, 3, 2}, {4, 3, 7, 4}, {25, 10, 12, 4}, {18, 24, 33, 13}, {10, 6, 7, 2}}
	layer, ws, err := applyCorrespondence(correspondenceSpec(""), countHost(smoke))
	if err != nil || len(ws) != 0 {
		t.Fatalf("err %v warnings %+v", err, ws)
	}
	for k, want := range []float64{0.0748, 0.0100, 0.0004} {
		if got := *layer.Payload.Series.Entries[k].Summary.Statistic; math.Abs(got-want) > 0.00005 {
			t.Errorf("inertia dim %d = %.5f, published %.4f", k+1, got, want)
		}
	}
	if got := *layer.Summary.Statistic; math.Abs(got-0.0852) > 0.00005 {
		t.Errorf("total inertia %.5f, published 0.0852", got)
	}
	for k, want := range []float64{0.878, 0.118} {
		if got := layer.Payload.Series.Entries[k].Summary.Parameters["share"]; math.Abs(got-want) > 0.0005 {
			t.Errorf("share dim %d = %.4f, published %.3f", k+1, got, want)
		}
	}
}

// TestCorrespondence_Layer pins the layer's structure: a SERIES
// primary of K = min(r, c) − 1 inertias keyed dim_k with share /
// cumulative_share / singular_value; six parts whose matrices key their
// columns dim_1 … dim_d and rows by the host keys; the summary's total
// inertia, dimensions, retained_share and share_dim_<k>.
func TestCorrespondence_Layer(t *testing.T) {
	host := countHost([][]float64{{20, 5, 3, 9}, {4, 18, 6, 2}, {7, 6, 21, 3}, {9, 2, 4, 15}, {3, 11, 8, 6}})
	layer, _, err := applyCorrespondence(correspondenceSpec(""), host)
	if err != nil {
		t.Fatal(err)
	}
	if layer.Name != "correspondence" || layer.Kind != types.OverlayKindCorrespondence || layer.Scope != types.OverlayScopeMatrix {
		t.Fatalf("layer identity %q %q %q", layer.Name, layer.Kind, layer.Scope)
	}
	if layer.Payload.Shape != types.OverlayShapeSeries || layer.Payload.Matrix != nil || len(layer.Payload.Series.Entries) != 3 {
		t.Fatalf("primary %+v, want a 3-entry SERIES", layer.Payload)
	}
	var cum float64
	for k, e := range layer.Payload.Series.Entries {
		if e.Key[0] != "dim_"+strconv.Itoa(k+1) {
			t.Errorf("entry %d key %v", k, e.Key)
		}
		cum += e.Summary.Parameters["share"]
		caClose(t, "cumulative_share", e.Summary.Parameters["cumulative_share"], cum, 1e-12)
		caClose(t, "singular_value²", e.Summary.Parameters["singular_value"]*e.Summary.Parameters["singular_value"], *e.Summary.Statistic, 1e-12)
	}
	caClose(t, "shares sum", cum, 1, 1e-12)
	s := layer.Summary
	if s.Parameters["dimensions"] != 2 || s.Parameters["share_dim_3"] <= 0 {
		t.Fatalf("summary parameters %v", s.Parameters)
	}
	caClose(t, "retained_share", s.Parameters["retained_share"], s.Parameters["share_dim_1"]+s.Parameters["share_dim_2"], 1e-12)
	if len(layer.Payload.Parts) != 6 {
		t.Fatalf("%d parts, want 6", len(layer.Payload.Parts))
	}
	hostMx := host.Payload()
	for _, name := range []string{CorrespondencePartRowCoordinates, CorrespondencePartRowContribution, CorrespondencePartColumnCoordinates, CorrespondencePartColumnContribution} {
		m := layer.Payload.Parts[name].Matrix
		wantKeys := hostMx.RowKeys
		if name == CorrespondencePartColumnCoordinates || name == CorrespondencePartColumnContribution {
			wantKeys = hostMx.ColumnKeys
		}
		if len(m.RowKeys) != len(wantKeys) || m.RowKeys[1][0] != wantKeys[1][0] || len(m.ColumnKeys) != 2 || m.ColumnKeys[1][0] != "dim_2" {
			t.Errorf("%s keys %v / %v", name, m.RowKeys, m.ColumnKeys)
		}
	}
	// Each dimension's contributions sum to 1 over the points.
	for _, name := range []string{CorrespondencePartRowContribution, CorrespondencePartColumnContribution} {
		for k := 0; k < 2; k++ {
			var sum float64
			for _, row := range layer.Payload.Parts[name].Matrix.Cells {
				sum += row[k].Value.(float64)
			}
			caClose(t, name+" column sum", sum, 1, 1e-12)
		}
	}
	for _, name := range []string{CorrespondencePartRowQuality, CorrespondencePartColumnQuality} {
		for _, e := range layer.Payload.Parts[name].Series.Entries {
			if q := *e.Summary.Statistic; q < 0 || q > 1+1e-12 {
				t.Errorf("%s %v = %v, want within [0, 1]", name, e.Key, q)
			}
		}
	}
}

// TestCorrespondence_ScaleInvariant: CA reads only profiles and masses,
// so multiplying every cell by a constant (a probability weight's
// arbitrary scale, a Σw table vs its count twin) leaves every
// coordinate, inertia, quality and contribution unchanged; and a
// weighted host reads the Σw table as is under either weight kind.
func TestCorrespondence_ScaleInvariant(t *testing.T) {
	base := [][]float64{{20, 5, 3}, {4, 18, 6}, {7, 6, 21}, {9, 2, 4}}
	ref, _, err := applyCorrespondence(correspondenceSpec(""), countHost(base))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []float64{3.7, 1e-3, 1e6} {
		scaled := make([][]float64, len(base))
		for i, row := range base {
			scaled[i] = make([]float64, len(row))
			for j, v := range row {
				scaled[i][j] = v * f
			}
		}
		got, _, err := applyCorrespondence(correspondenceSpec(""), countHost(scaled))
		if err != nil {
			t.Fatal(err)
		}
		assertLayersClose(t, "×"+strconv.FormatFloat(f, 'g', -1, 64), got, ref, 1e-10)
	}
	freq, _, err := applyCorrespondence(correspondenceSpec(""), weightedCountHost(ctFixture, 2, 3, weighting.Frequency))
	if err != nil {
		t.Fatal(err)
	}
	prob, _, err := applyCorrespondence(correspondenceSpec(""), weightedCountHost(ctFixture, 2, 3, weighting.Probability))
	if err != nil {
		t.Fatal(err)
	}
	assertLayersClose(t, "probability vs frequency", prob, freq, 0)
	if prob.Summary.Parameters["n_eff"] != 0 || prob.Summary.Parameters["sum_weights"] != 0 {
		t.Errorf("descriptive kind stamped a weighted floor: %v", prob.Summary.Parameters)
	}
}

// assertLayersClose compares two correspondence layers figure by
// figure (NaN equals NaN).
func assertLayersClose(t *testing.T, where string, got, want types.OverlayLayer, tol float64) {
	t.Helper()
	same := func(what string, a, b float64) {
		if math.IsNaN(a) && math.IsNaN(b) {
			return
		}
		if math.Abs(a-b) > tol*math.Max(1, math.Abs(b)) {
			t.Errorf("%s %s: %.17g vs %.17g", where, what, a, b)
		}
	}
	same("total", *got.Summary.Statistic, *want.Summary.Statistic)
	for k, e := range want.Payload.Series.Entries {
		same("inertia", *got.Payload.Series.Entries[k].Summary.Statistic, *e.Summary.Statistic)
	}
	for name, part := range want.Payload.Parts {
		g := got.Payload.Parts[name]
		switch part.Shape {
		case types.OverlayShapeMatrix:
			for i, row := range part.Matrix.Cells {
				for k, c := range row {
					same(name, g.Matrix.Cells[i][k].Value.(float64), c.Value.(float64))
				}
			}
		case types.OverlayShapeSeries:
			for i, e := range part.Series.Entries {
				same(name, *g.Series.Entries[i].Summary.Statistic, *e.Summary.Statistic)
			}
		}
	}
}

// TestCorrespondence_Params: dimensions defaults to 2 capped at
// min(r, c) − 1, an explicit value above the cap and every malformed or
// unknown param is PULSE_OVERLAY_PARAM_MISSING; the normalization picks
// principal vs standard coordinates per axis.
func TestCorrespondence_Params(t *testing.T) {
	table := [][]float64{{20, 5, 3}, {4, 18, 6}, {7, 6, 21}, {9, 2, 4}} // 4 × 3: K = 2
	for _, c := range []struct {
		name, params string
		host         [][]float64
		dims         int  // retained, when ok
		refused      bool // PULSE_OVERLAY_PARAM_MISSING
	}{
		{"default", "", table, 2, false},
		{"null", "null", table, 2, false},
		{"default capped", "", [][]float64{{3, 4, 5}, {6, 2, 9}}, 1, false},
		{"explicit 1", `{"dimensions":1}`, table, 1, false},
		{"explicit at cap", `{"dimensions":2}`, table, 2, false},
		{"over cap", `{"dimensions":3}`, table, 0, true},
		{"explicit over cap on 2x3", `{"dimensions":2}`, [][]float64{{3, 4, 5}, {6, 2, 9}}, 0, true},
		{"zero", `{"dimensions":0}`, table, 0, true},
		{"negative", `{"dimensions":-1}`, table, 0, true},
		{"fraction", `{"dimensions":1.5}`, table, 0, true},
		{"string", `{"dimensions":"2"}`, table, 0, true},
		{"null dimensions", `{"dimensions":null}`, table, 0, true},
		{"exponent", `{"dimensions":2e0}`, table, 0, true},
		{"unknown normalization", `{"normalization":"principal"}`, table, 0, true},
		{"unknown key", `{"dims":2}`, table, 0, true},
		{"not an object", `[2]`, table, 0, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			layer, _, err := applyCorrespondence(correspondenceSpec(c.params), countHost(c.host))
			if c.refused {
				var ce *errors.CodedError
				if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_OVERLAY_PARAM_MISSING {
					t.Fatalf("err %v, want PULSE_OVERLAY_PARAM_MISSING", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := len(layer.Payload.Parts[CorrespondencePartRowCoordinates].Matrix.ColumnKeys); got != c.dims {
				t.Fatalf("%d retained dims, want %d", got, c.dims)
			}
		})
	}

	// Principal = standard × singular value, per axis and normalization.
	run := func(norm string) types.OverlayLayer {
		l, _, err := applyCorrespondence(correspondenceSpec(`{"normalization":"`+norm+`"}`), countHost(table))
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	sym, rowP, colP := run("symmetric"), run("row_principal"), run("column_principal")
	coord := func(l types.OverlayLayer, part string, i, k int) float64 {
		return l.Payload.Parts[part].Matrix.Cells[i][k].Value.(float64)
	}
	for k := 0; k < 2; k++ {
		sv := sym.Payload.Series.Entries[k].Summary.Parameters["singular_value"]
		caClose(t, "row_principal rows", coord(rowP, CorrespondencePartRowCoordinates, 1, k), coord(sym, CorrespondencePartRowCoordinates, 1, k), 1e-14)
		caClose(t, "row_principal cols", coord(rowP, CorrespondencePartColumnCoordinates, 1, k)*sv, coord(sym, CorrespondencePartColumnCoordinates, 1, k), 1e-12)
		caClose(t, "column_principal rows", coord(colP, CorrespondencePartRowCoordinates, 1, k)*sv, coord(sym, CorrespondencePartRowCoordinates, 1, k), 1e-12)
		caClose(t, "column_principal cols", coord(colP, CorrespondencePartColumnCoordinates, 1, k), coord(sym, CorrespondencePartColumnCoordinates, 1, k), 1e-14)
	}
}

// TestCorrespondence_NonnegativeRequired: a negative or non-finite
// cell, or a known cell aggregation other than AGG_COUNT / AGG_SUM, is
// PULSE_OVERLAY_NONNEGATIVE_REQUIRED; AGG_COUNT and AGG_SUM run.
func TestCorrespondence_NonnegativeRequired(t *testing.T) {
	table := [][]float64{{20, 5, 3}, {4, 18, 6}, {7, 6, 21}}
	for _, c := range []struct {
		name    string
		edit    func(h *CrosstabHostView)
		refused bool
	}{
		{"count cell", func(h *CrosstabHostView) { h.cellType = types.AGG_COUNT }, false},
		{"sum cell", func(h *CrosstabHostView) { h.cellType = types.AGG_SUM }, false},
		{"average cell", func(h *CrosstabHostView) { h.cellType = types.AGG_AVERAGE }, true},
		{"mode cell", func(h *CrosstabHostView) { h.cellType = types.AGG_MODE }, true},
		{"negative cell", func(h *CrosstabHostView) { h.payload.Cells[1][2].Value = -0.5 }, true},
		{"NaN cell", func(h *CrosstabHostView) { h.payload.Cells[0][0].Value = math.NaN() }, true},
		{"+Inf cell", func(h *CrosstabHostView) { h.payload.Cells[0][0].Value = math.Inf(1) }, true},
		{"non-scalar cell", func(h *CrosstabHostView) { h.payload.Cells[0][0].Value = []float64{1, 2} }, true},
		{"absent cell is 0", func(h *CrosstabHostView) { h.payload.Cells[0][0] = types.MatrixCell{} }, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			host := countHost(table)
			c.edit(host)
			_, _, err := applyCorrespondence(correspondenceSpec(""), host)
			var ce *errors.CodedError
			got := stderrors.As(err, &ce) && ce.Code == errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED
			if got != c.refused || (!c.refused && err != nil) {
				t.Fatalf("err %v, refused want %v", err, c.refused)
			}
		})
	}
}

// TestCorrespondence_ZeroMargins: a zero row and a zero column are
// dropped — one PULSE_OVERLAY_REF_ZERO per axis naming them — and the
// remaining points key the parts; a table that leaves fewer than 2 × 2
// points is PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
func TestCorrespondence_ZeroMargins(t *testing.T) {
	host := countHost([][]float64{{12, 0, 7, 30}, {0, 0, 0, 0}, {25, 0, 4, 9}, {8, 0, 16, 11}})
	layer, ws, err := applyCorrespondence(correspondenceSpec(""), host)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 2 {
		t.Fatalf("warnings %+v, want one per axis", ws)
	}
	for i, axis := range []string{"row", "column"} {
		w := ws[i]
		dropped, _ := w.Details["dropped"].([]int)
		if w.Code != string(errors.PULSE_OVERLAY_REF_ZERO) || w.Details["axis"] != axis || len(dropped) != 1 || dropped[0] != 1 || w.Details["reason"] != "zero_margin" {
			t.Errorf("warning %d %+v", i, w)
		}
	}
	rows := layer.Payload.Parts[CorrespondencePartRowCoordinates].Matrix.RowKeys
	cols := layer.Payload.Parts[CorrespondencePartColumnQuality].Series.Entries
	if len(rows) != 3 || rows[1][0] != "C" || len(cols) != 3 || cols[1].Key[0] != "z" {
		t.Fatalf("kept rows %v / columns %v", rows, cols)
	}

	for name, table := range map[string][][]float64{
		"one row left":    {{1, 2, 3}, {0, 0, 0}},
		"one column left": {{1, 0}, {2, 0}},
		"one row":         {{1, 2, 3}},
		"all zero":        {{0, 0}, {0, 0}},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := applyCorrespondence(correspondenceSpec(""), countHost(table))
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE {
				t.Fatalf("err %v, want PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE", err)
			}
		})
	}
}

// TestCorrespondence_DegenerateDimensions: an exactly independent table
// has zero inertia — every dimension undefined: coordinates and
// contributions NaN, shares NaN, qualities NaN (each profile sits on
// the centroid).
func TestCorrespondence_DegenerateDimensions(t *testing.T) {
	layer, _, err := applyCorrespondence(correspondenceSpec(""), countHost([][]float64{{1, 2, 3}, {2, 4, 6}, {3, 6, 9}}))
	if err != nil {
		t.Fatal(err)
	}
	if got := *layer.Summary.Statistic; got > 1e-28 {
		t.Fatalf("total inertia %v, want 0", got)
	}
	for _, part := range []string{CorrespondencePartRowCoordinates, CorrespondencePartColumnContribution} {
		for _, row := range layer.Payload.Parts[part].Matrix.Cells {
			for _, c := range row {
				if v := c.Value.(float64); !math.IsNaN(v) {
					t.Errorf("%s cell %v, want NaN on an undefined dimension", part, v)
				}
			}
		}
	}
	if q := *layer.Payload.Parts[CorrespondencePartRowQuality].Series.Entries[0].Summary.Statistic; !math.IsNaN(q) {
		t.Errorf("row quality %v, want NaN (profile on the centroid)", q)
	}
}

// TestCorrespondence_DispatchGates: through the MATRIX dispatcher the
// kind runs (one layer, parts intact past PartsRefusal) and a non-zero
// Level / Within is PULSE_OVERLAY_LEVEL_OUT_OF_RANGE — the runtime twin
// of descriptor.ValidateOverlays.
func TestCorrespondence_DispatchGates(t *testing.T) {
	host := countHost([][]float64{{10, 20, 5}, {20, 10, 8}, {3, 9, 14}})
	layers, _, err := applyOverlays([]types.OverlaySpec{*correspondenceSpec("")}, host)
	if err != nil || len(layers) != 1 || len(layers[0].Payload.Parts) != 6 {
		t.Fatalf("dispatch: layers %+v err %v", layers, err)
	}
	for _, edit := range []func(*types.OverlaySpec){
		func(s *types.OverlaySpec) { s.Level = 1 },
		func(s *types.OverlaySpec) { s.Within = 1 },
	} {
		spec := *correspondenceSpec("")
		edit(&spec)
		_, _, err := applyOverlays([]types.OverlaySpec{spec}, host)
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE {
			t.Fatalf("level %d within %d: err %v, want PULSE_OVERLAY_LEVEL_OUT_OF_RANGE", spec.Level, spec.Within, err)
		}
	}
}
