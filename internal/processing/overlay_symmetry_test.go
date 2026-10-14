package processing

import (
	"encoding/json"
	stderrors "errors"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// symmetryCase is one symmetry.json case (scripts/reference/gen_symmetry.R).
type symmetryCase struct {
	Name                string      `json:"name"`
	States              []string    `json:"states"`
	Counts              [][]float64 `json:"counts"`
	Correct             bool        `json:"correct"`
	Statistic           *float64    `json:"statistic"`
	DF                  float64     `json:"df"`
	PValue              *float64    `json:"p_value"`
	Discordant          float64     `json:"discordant"`
	PairsDropped        float64     `json:"pairs_dropped"`
	ContinuityCorrected float64     `json:"continuity_corrected"`
}

// symmetryOracleTol is the relative tolerance the R figures are held to:
// the statistic is a short sum of ratios, but the p-value runs through
// the chi-square survival tail, whose last ulps differ between Pulse's
// incomplete gamma and R's pchisq and between amd64 and arm64 — never
// an exact-bits comparison.
const symmetryOracleTol = 1e-12

func symmetrySpec(params string) *types.OverlaySpec {
	s := &types.OverlaySpec{Kind: types.OverlayKindSymmetry, Scope: types.OverlayScopeMatrix}
	if params != "" {
		s.Params = json.RawMessage(params)
	}
	return s
}

// symmetryHost builds a from × to host: rows are rowStates, columns
// colStates, and counts[i][j] is the count for (rowStates[i],
// colStates[j]).
func symmetryHost(rowStates, colStates []string, counts [][]float64) *CrosstabHostView {
	mx := &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"before"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"after"}, Types: []string{"GROUP_CATEGORY"}},
		CellLabel:    "n",
	}
	for _, s := range rowStates {
		mx.RowKeys = append(mx.RowKeys, types.AxisKey{s})
	}
	for _, s := range colStates {
		mx.ColumnKeys = append(mx.ColumnKeys, types.AxisKey{s})
	}
	for i := range rowStates {
		row := make([]types.MatrixCell, len(colStates))
		for j := range colStates {
			row[j] = types.MatrixCell{Value: counts[i][j], Present: true}
		}
		mx.Cells = append(mx.Cells, row)
	}
	return NewCrosstabHostView(mx)
}

func symmetryClose(t *testing.T, where string, got float64, want *float64) {
	t.Helper()
	if want == nil {
		if !math.IsNaN(got) {
			t.Errorf("%s = %v, want NaN (null)", where, got)
		}
		return
	}
	if d := math.Abs(got - *want); !(d <= symmetryOracleTol*math.Max(1e-300, math.Abs(*want))) && got != *want {
		t.Errorf("%s = %.17g, want %.17g (R)", where, got, *want)
	}
}

// TestSymmetry_MatchesR: McNemar (with and without Edwards' correction)
// and Bowker statistics, df and p equal R's mcnemar.test — the empty-pair
// case the documented drop rule — with the host columns REVERSED against
// the rows, so a positional (not by-key) alignment would fail.
func TestSymmetry_MatchesR(t *testing.T) {
	doc := loadReference[symmetryCase](t, "symmetry")
	if len(doc.Cases) < 7 {
		t.Fatalf("symmetry.json has %d cases, want at least 7", len(doc.Cases))
	}
	for _, c := range doc.Cases {
		t.Run(c.Name, func(t *testing.T) {
			k := len(c.States)
			cols := make([]string, k)
			counts := make([][]float64, k)
			for i := range counts {
				counts[i] = make([]float64, k)
			}
			for j := range cols {
				cols[j] = c.States[k-1-j]
				for i := 0; i < k; i++ {
					counts[i][j] = c.Counts[i][k-1-j]
				}
			}
			params := `{"correct":false}`
			if c.Correct {
				params = `{"correct":true}`
			}
			layer, _, err := applySymmetry(symmetrySpec(params), symmetryHost(c.States, cols, counts))
			if err != nil {
				t.Fatal(err)
			}
			if layer.Name != "symmetry" || layer.Payload.Shape != types.OverlayShapeScalar || layer.Payload.Scalar == nil {
				t.Fatalf("layer %+v", layer)
			}
			s := layer.Summary
			symmetryClose(t, "statistic", *s.Statistic, c.Statistic)
			symmetryClose(t, "scalar", *layer.Payload.Scalar, c.Statistic)
			symmetryClose(t, "p_value", *s.PValue, c.PValue)
			want := map[string]float64{
				"df": c.DF, "states": float64(k), "discordant": c.Discordant,
				"pairs_dropped": c.PairsDropped, "continuity_corrected": c.ContinuityCorrected,
			}
			if !reflect.DeepEqual(s.Parameters, want) {
				t.Errorf("parameters %v, want %v", s.Parameters, want)
			}
		})
	}
}

// TestSymmetry_DefaultCorrects: an omitted params.correct is R's default
// correct = TRUE — the corrected McNemar on a 2 x 2.
func TestSymmetry_DefaultCorrects(t *testing.T) {
	host := symmetryHost([]string{"a", "b"}, []string{"a", "b"}, [][]float64{{10, 12}, {4, 9}})
	layer, _, err := applySymmetry(symmetrySpec(""), host)
	if err != nil {
		t.Fatal(err)
	}
	if want := float64(7*7) / 16; *layer.Summary.Statistic != want || layer.Summary.Parameters["continuity_corrected"] != 1 {
		t.Fatalf("statistic %v %v, want %v corrected", *layer.Summary.Statistic, layer.Summary.Parameters, want)
	}
	// k > 2: the correction is inert whatever params.correct says.
	host = symmetryHost([]string{"a", "b", "c"}, []string{"a", "b", "c"}, [][]float64{{5, 4, 1}, {2, 5, 3}, {2, 6, 5}})
	on, _, err := applySymmetry(symmetrySpec(`{"correct":true}`), host)
	if err != nil {
		t.Fatal(err)
	}
	off, _, err := applySymmetry(symmetrySpec(`{"correct":false}`), host)
	if err != nil {
		t.Fatal(err)
	}
	if *on.Summary.Statistic != *off.Summary.Statistic || on.Summary.Parameters["continuity_corrected"] != 0 {
		t.Fatalf("Bowker moved with correct: %v vs %v", on.Summary, off.Summary)
	}
}

// TestSymmetry_CorrectionClampsAtZero: on a non-integer table (a Kish-
// scaled or AGG_SUM host) |n_12 − n_21| can fall below 1; the corrected
// gap is clamped at 0, so the statistic never exceeds the uncorrected
// one (R would square −0.5).
func TestSymmetry_CorrectionClampsAtZero(t *testing.T) {
	host := symmetryHost([]string{"a", "b"}, []string{"a", "b"}, [][]float64{{3, 2.5}, {2, 4}})
	layer, _, err := applySymmetry(symmetrySpec(""), host)
	if err != nil {
		t.Fatal(err)
	}
	if *layer.Summary.Statistic != 0 || *layer.Summary.PValue != 1 || layer.Summary.Parameters["continuity_corrected"] != 1 {
		t.Fatalf("summary %+v, want statistic 0 / p 1, corrected", layer.Summary)
	}
}

// TestSymmetry_OneAxisStateIsZeroRow: a state on one axis only (nobody
// started there) is a row of zeros, never a refusal — the figures equal
// the explicit square table.
func TestSymmetry_OneAxisStateIsZeroRow(t *testing.T) {
	partial := symmetryHost([]string{"a", "b"}, []string{"c", "b", "a"}, [][]float64{{4, 7, 20}, {6, 30, 2}})
	full := symmetryHost([]string{"a", "b", "c"}, []string{"a", "b", "c"}, [][]float64{{20, 7, 4}, {2, 30, 6}, {0, 0, 0}})
	got, _, err := applySymmetry(symmetrySpec(""), partial)
	if err != nil {
		t.Fatal(err)
	}
	want, _, err := applySymmetry(symmetrySpec(""), full)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Summary, want.Summary) || got.Summary.Parameters["states"] != 3 {
		t.Fatalf("one-axis state %+v, want %+v", got.Summary, want.Summary)
	}
}

// TestSymmetry_UndefinedCellIsAbsent: an undefined (NaN) cell is an
// absent one (E4-S6) — the figures equal the table with that cell 0.
func TestSymmetry_UndefinedCellIsAbsent(t *testing.T) {
	nan := symmetryHost([]string{"a", "b", "c"}, []string{"a", "b", "c"}, [][]float64{{5, math.NaN(), 1}, {2, 5, 3}, {2, 6, 5}})
	zero := symmetryHost([]string{"a", "b", "c"}, []string{"a", "b", "c"}, [][]float64{{5, 0, 1}, {2, 5, 3}, {2, 6, 5}})
	got, _, err := applySymmetry(symmetrySpec(""), nan)
	if err != nil {
		t.Fatal(err)
	}
	want, _, err := applySymmetry(symmetrySpec(""), zero)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Summary, want.Summary) {
		t.Fatalf("NaN cell %+v, want the zero-cell %+v", got.Summary, want.Summary)
	}
}

// TestSymmetry_ExpectedLowWarning: one PULSE_OVERLAY_EXPECTED_LOW names
// the pairs whose expected count under symmetry ((n_ij + n_ji) / 2) is
// below 5, the empty ones among them and the minimum; a table with every
// pair at 5 or more raises none.
func TestSymmetry_ExpectedLowWarning(t *testing.T) {
	// Pairs: (a, b) s = 9 (expected 4.5), (a, c) s = 0, (b, c) s = 32.
	host := symmetryHost([]string{"a", "b", "c"}, []string{"a", "b", "c"}, [][]float64{{9, 4, 0}, {5, 9, 20}, {0, 12, 9}})
	_, ws, err := applySymmetry(symmetrySpec(""), host)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 1 || ws[0].Code != string(errors.PULSE_OVERLAY_EXPECTED_LOW) {
		t.Fatalf("warnings %+v, want one PULSE_OVERLAY_EXPECTED_LOW", ws)
	}
	d := ws[0].Details
	if d["kind"] != "OVERLAY_SYMMETRY" || d["low_expected_pairs"] != 2 || d["zero_pairs"] != 1 || d["expected_min"] != 0.0 {
		t.Fatalf("details %v", d)
	}
	ok := symmetryHost([]string{"a", "b"}, []string{"a", "b"}, [][]float64{{9, 6}, {4, 9}})
	if _, ws, _ := applySymmetry(symmetrySpec(""), ok); len(ws) != 0 {
		t.Fatalf("warnings %+v on a table with expected 5", ws)
	}
}

// TestSymmetry_Refusals: each runtime refusal carries its own code (and
// the spec index through applySymmetryAt), never PROCESSING_INTERNAL.
func TestSymmetry_Refusals(t *testing.T) {
	square := func() *CrosstabHostView {
		return symmetryHost([]string{"a", "b"}, []string{"a", "b"}, [][]float64{{1, 2}, {3, 4}})
	}
	twoCols := square()
	twoCols.payload.ColumnHeader.Fields = []string{"after", "wave"}
	avg := square()
	avg.cellType = types.AGG_AVERAGE
	for _, c := range []struct {
		name   string
		spec   *types.OverlaySpec
		host   *CrosstabHostView
		want   errors.Code
		indexd bool
	}{
		{"unknown param", symmetrySpec(`{"exact":true}`), square(), errors.PULSE_OVERLAY_PARAM_MISSING, true},
		{"non-boolean correct", symmetrySpec(`{"correct":"yes"}`), square(), errors.PULSE_OVERLAY_PARAM_MISSING, true},
		{"average cell", symmetrySpec(""), avg, errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED, true},
		{"grouper counts differ", symmetrySpec(""), twoCols, errors.PULSE_OVERLAY_MATRIX_NOT_SQUARE, true},
		{"negative cell", symmetrySpec(""), symmetryHost([]string{"a", "b"}, []string{"a", "b"}, [][]float64{{1, -2}, {3, 4}}), errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED, false},
		{"one state", symmetrySpec(""), symmetryHost([]string{"a"}, []string{"a"}, [][]float64{{4}}), errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, false},
		{"zero total", symmetrySpec(""), symmetryHost([]string{"a", "b"}, []string{"a", "b"}, [][]float64{{0, 0}, {0, 0}}), errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := applySymmetryAt(c.spec, c.host, 3)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != c.want {
				t.Fatalf("err %v, want %s", err, c.want)
			}
			if c.indexd && ce.Details["index"] != 3 {
				t.Fatalf("details %v lack the spec index", ce.Details)
			}
		})
	}
}

// TestSymmetry_DoesNotMutateHost: the overlay is a read-only sibling —
// the host payload is byte-identical after the handler runs.
func TestSymmetry_DoesNotMutateHost(t *testing.T) {
	host := symmetryHost([]string{"a", "b"}, []string{"b", "a"}, [][]float64{{3, 10}, {9, 2}})
	before, _ := json.Marshal(host.Payload())
	if _, _, err := applySymmetry(symmetrySpec(""), host); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(host.Payload())
	if string(before) != string(after) {
		t.Fatalf("host moved:\n%s\n%s", before, after)
	}
}
