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
	Name                string       `json:"name"`
	States              []string     `json:"states"`
	Counts              [][]float64  `json:"counts"`
	Correct             bool         `json:"correct"`
	Exact               bool         `json:"exact"`
	Statistic           *float64     `json:"statistic"`
	DF                  float64      `json:"df"`
	PValue              *float64     `json:"p_value"`
	Discordant          float64      `json:"discordant"`
	PairsDropped        float64      `json:"pairs_dropped"`
	ContinuityCorrected float64      `json:"continuity_corrected"`
	OddsRatio           symmetryOdds `json:"odds_ratio"`
	OddsRatioCILower    symmetryOdds `json:"odds_ratio_ci_lower"`
	OddsRatioCIUpper    symmetryOdds `json:"odds_ratio_ci_upper"`
}

// symmetryOdds is a symmetry.json odds figure: null (NaN), the string
// "Inf" (+Inf: n_21 = 0) or a number. Absent (a chi-square case) leaves
// it unset.
type symmetryOdds struct {
	Set bool
	V   float64
}

func (o *symmetryOdds) UnmarshalJSON(b []byte) error {
	o.Set = true
	switch string(b) {
	case "null":
		o.V = math.NaN()
		return nil
	case `"Inf"`:
		o.V = math.Inf(1)
		return nil
	}
	return json.Unmarshal(b, &o.V)
}

// symmetryOddsOracleTol holds the odds-ratio CI to R's binom.test
// conf.int: a root solve (Pulse's on the log-odds, R's qbeta), so the
// statdist oracle tolerance, not the statistic's.
const symmetryOddsOracleTol = 1e-10

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
	t.Parallel()

	doc := loadReference[symmetryCase](t, "symmetry")
	if len(doc.Cases) < 14 {
		t.Fatalf("symmetry.json has %d cases, want at least 15", len(doc.Cases))
	}
	exactCases := 0
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
			if c.Exact {
				exactCases++
				params = params[:len(params)-1] + `,"exact":true}`
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
			got := s.Parameters
			if c.Exact {
				// The exact arm has no chi-square df; it records the method
				// and the discordant odds ratio with its exact CI.
				delete(want, "df")
				want["exact"] = 1
				got = map[string]float64{}
				for key, v := range s.Parameters {
					got[key] = v
				}
				for _, o := range []struct {
					key string
					ref symmetryOdds
					tol float64
				}{
					{"odds_ratio", c.OddsRatio, 0},
					{"odds_ratio_ci_lower", c.OddsRatioCILower, symmetryOddsOracleTol},
					{"odds_ratio_ci_upper", c.OddsRatioCIUpper, symmetryOddsOracleTol},
				} {
					v, ok := got[o.key]
					delete(got, o.key)
					switch {
					case !o.ref.Set:
						t.Errorf("symmetry.json exact case lacks %s", o.key)
					case !ok:
						t.Errorf("parameters lack %s", o.key)
					case math.IsNaN(o.ref.V):
						if !math.IsNaN(v) {
							t.Errorf("%s = %v, want NaN (null)", o.key, v)
						}
					case math.IsInf(o.ref.V, 1) || o.ref.V == 0 || o.tol == 0:
						if v != o.ref.V {
							t.Errorf("%s = %.17g, want %.17g (R)", o.key, v, o.ref.V)
						}
					case !(math.Abs(v-o.ref.V) <= o.tol*math.Abs(o.ref.V)):
						t.Errorf("%s = %.17g, want %.17g (R)", o.key, v, o.ref.V)
					}
				}
			} else if c.OddsRatio.Set {
				t.Errorf("symmetry.json chi-square case carries an odds ratio")
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("parameters %v, want %v", got, want)
			}
		})
	}
	if exactCases < 8 {
		t.Fatalf("symmetry.json has %d exact cases, want at least 8", exactCases)
	}
}

// TestSymmetry_ExactDefaultByteIdentical: an absent exact and an
// explicit exact false emit the same layer and warnings, byte for byte,
// as the params-free spec — the χ² arm never sees the new param.
func TestSymmetry_ExactDefaultByteIdentical(t *testing.T) {
	t.Parallel()

	host := func() *CrosstabHostView {
		return symmetryHost([]string{"yes", "no"}, []string{"yes", "no"}, [][]float64{{10, 1}, {4, 12}})
	}
	marshal := func(params string) string {
		t.Helper()
		layer, ws, err := applySymmetry(symmetrySpec(params), host())
		if err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(struct {
			L types.OverlayLayer
			W []types.OverlayWarning
		}{layer, ws})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	base := marshal("")
	for _, params := range []string{`{"exact":false}`, `{"correct":true,"exact":false}`} {
		if got := marshal(params); got != base {
			t.Errorf("%s:\n got %s\nwant %s", params, got, base)
		}
	}
	if marshal(`{"exact":true}`) == base {
		t.Fatal("exact true emitted the chi-square layer")
	}
}

// TestSymmetry_ExactIgnoresCorrectAndLowExpected: under exact the
// continuity correction is inert (correct true and false give the same
// layer, continuity_corrected 0) and no PULSE_OVERLAY_EXPECTED_LOW is
// raised on a table the chi-square arm would warn on.
func TestSymmetry_ExactIgnoresCorrectAndLowExpected(t *testing.T) {
	t.Parallel()

	host := func() *CrosstabHostView {
		return symmetryHost([]string{"yes", "no"}, []string{"yes", "no"}, [][]float64{{10, 1}, {4, 12}})
	}
	if _, ws, err := applySymmetry(symmetrySpec(""), host()); err != nil || len(ws) != 1 {
		t.Fatalf("chi-square arm: warnings %+v err %v, want one EXPECTED_LOW", ws, err)
	}
	on, wsOn, err := applySymmetry(symmetrySpec(`{"exact":true,"correct":true}`), host())
	if err != nil {
		t.Fatal(err)
	}
	off, wsOff, err := applySymmetry(symmetrySpec(`{"exact":true,"correct":false}`), host())
	if err != nil {
		t.Fatal(err)
	}
	if len(wsOn) != 0 || len(wsOff) != 0 {
		t.Fatalf("exact warnings %+v / %+v, want none", wsOn, wsOff)
	}
	if !reflect.DeepEqual(on, off) || on.Summary.Parameters["continuity_corrected"] != 0 {
		t.Fatalf("correct moved the exact layer: %+v vs %+v", on.Summary, off.Summary)
	}
	if *on.Summary.Statistic != 1 || math.Abs(*on.Summary.PValue-0.375) > 1e-15 {
		t.Fatalf("exact statistic %v p %v, want 1 and 0.375", *on.Summary.Statistic, *on.Summary.PValue)
	}
}

// TestSymmetry_ExactCounts: a frequency-weighted (Σw) table of whole
// counts runs exact as is, stamping the weighted floor; a fractional
// discordant pair (an AGG_SUM host) is refused, a near-whole one
// (within binom.test's 1e-7) rounded.
func TestSymmetry_ExactCounts(t *testing.T) {
	t.Parallel()

	near := symmetryHost([]string{"yes", "no"}, []string{"yes", "no"}, [][]float64{{10, 1.00000001}, {4, 12}})
	layer, _, err := applySymmetry(symmetrySpec(`{"exact":true}`), near)
	if err != nil || *layer.Summary.Statistic != 1 || layer.Summary.Parameters["discordant"] != 5 {
		t.Fatalf("near-whole pair: %+v %v", layer.Summary, err)
	}
	frac := symmetryHost([]string{"yes", "no"}, []string{"yes", "no"}, [][]float64{{10, 1.5}, {4, 12}})
	_, _, err = applySymmetryAt(symmetrySpec(`{"exact":true}`), frac, 2)
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED || ce.Details["reason"] != "non_integer" || ce.Details["index"] != 2 {
		t.Fatalf("fractional pair: err %v", err)
	}
	if _, _, err := applySymmetry(symmetrySpec(""), frac); err != nil {
		t.Fatalf("chi-square arm refused a fractional table: %v", err)
	}
}

// TestSymmetry_ExactOddsRatioEdges: the discordant odds ratio's zero
// edges. n_21 = 0 is +Inf (ratio and upper bound) in Go and null on
// the wire (types.MarshalFinite), the lower bound finite; n_12 = 0 is
// 0 (ratio and lower bound); no discordant unit is NaN (null) on all
// three. The chi-square arm on the same table carries none of the keys.
func TestSymmetry_ExactOddsRatioEdges(t *testing.T) {
	t.Parallel()

	run := func(params string, n12, n21 float64) types.OverlayLayer {
		t.Helper()
		layer, _, err := applySymmetry(symmetrySpec(params),
			symmetryHost([]string{"yes", "no"}, []string{"yes", "no"}, [][]float64{{5, n12}, {n21, 5}}))
		if err != nil {
			t.Fatal(err)
		}
		return layer
	}
	keys := []string{"odds_ratio", "odds_ratio_ci_lower", "odds_ratio_ci_upper"}

	up := run(`{"exact":true}`, 7, 0).Summary.Parameters
	if !math.IsInf(up["odds_ratio"], 1) || !math.IsInf(up["odds_ratio_ci_upper"], 1) ||
		!(up["odds_ratio_ci_lower"] > 0) || math.IsInf(up["odds_ratio_ci_lower"], 0) {
		t.Errorf("n_21 = 0: %v, want +Inf ratio and upper, a finite positive lower", up)
	}
	wire, err := types.MarshalFinite(run(`{"exact":true}`, 7, 0).Summary)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Parameters map[string]any `json:"parameters"`
	}
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"odds_ratio", "odds_ratio_ci_upper"} {
		if v, ok := decoded.Parameters[k]; !ok || v != nil {
			t.Errorf("wire %s = %v (present %v), want null with the key kept: %s", k, v, ok, wire)
		}
	}

	down := run(`{"exact":true}`, 0, 7).Summary.Parameters
	if down["odds_ratio"] != 0 || down["odds_ratio_ci_lower"] != 0 || !(down["odds_ratio_ci_upper"] > 0) {
		t.Errorf("n_12 = 0: %v, want ratio and lower 0, a positive upper", down)
	}

	none := run(`{"exact":true}`, 0, 0).Summary.Parameters
	for _, k := range keys {
		if v, ok := none[k]; !ok || !math.IsNaN(v) {
			t.Errorf("no discordant unit: %s = %v (present %v), want NaN", k, v, ok)
		}
	}

	chi := run("", 7, 3).Summary.Parameters
	for _, k := range keys {
		if _, ok := chi[k]; ok {
			t.Errorf("chi-square arm carries %s: %v", k, chi)
		}
	}
}

// TestSymmetry_DefaultCorrects: an omitted params.correct is R's default
// correct = TRUE — the corrected McNemar on a 2 x 2.
func TestSymmetry_DefaultCorrects(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
		{"unknown param", symmetrySpec(`{"mid_p":true}`), square(), errors.PULSE_OVERLAY_PARAM_MISSING, true},
		{"non-boolean exact", symmetrySpec(`{"exact":1}`), square(), errors.PULSE_OVERLAY_PARAM_MISSING, true},
		{"exact on 3 states", symmetrySpec(`{"exact":true}`), symmetryHost([]string{"a", "b", "c"}, []string{"a", "b", "c"}, [][]float64{{1, 2, 3}, {3, 4, 5}, {5, 6, 7}}), errors.PULSE_OVERLAY_PARAM_MISSING, true},
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
	t.Parallel()

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
