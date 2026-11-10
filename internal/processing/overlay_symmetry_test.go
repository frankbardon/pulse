package processing

import (
	"encoding/json"
	stderrors "errors"
	"math"
	"reflect"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/statdist"
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
	ConfLevel           float64      `json:"conf_level"`
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

// symmetryBowkerOracleTol holds the exact Bowker p to R's brute-force
// enumeration: both sum the same binomial masses in a different order
// (Pulse's convolution merges equal statistic values first), so a
// relative 1e-10, the statdist oracle tolerance.
const symmetryBowkerOracleTol = 1e-10

func symmetryClose(t *testing.T, where string, got float64, want *float64) {
	t.Helper()
	symmetryCloseTol(t, where, got, want, symmetryOracleTol)
}

func symmetryCloseTol(t *testing.T, where string, got float64, want *float64, tol float64) {
	t.Helper()
	if want == nil {
		if !math.IsNaN(got) {
			t.Errorf("%s = %v, want NaN (null)", where, got)
		}
		return
	}
	if d := math.Abs(got - *want); !(d <= tol*math.Max(1e-300, math.Abs(*want))) && got != *want {
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
	if len(doc.Cases) < 21 {
		t.Fatalf("symmetry.json has %d cases, want at least 21", len(doc.Cases))
	}
	exactCases, levelCases, bowkerCases := 0, 0, 0
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
				if k == 2 {
					exactCases++
				}
				params = params[:len(params)-1] + `,"exact":true}`
				if k == 2 && c.ConfLevel != 0.95 {
					// A non-default level rides params.conf_level; the
					// default cases leave it absent (the 0.95 default).
					levelCases++
					params = params[:len(params)-1] + `,"conf_level":` + strconv.FormatFloat(c.ConfLevel, 'g', -1, 64) + `}`
				}
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
			want := map[string]float64{
				"df": c.DF, "states": float64(k), "discordant": c.Discordant,
				"pairs_dropped": c.PairsDropped, "continuity_corrected": c.ContinuityCorrected,
			}
			got := s.Parameters
			if c.Exact && k > 2 {
				// The exact Bowker arm (R brute-force enumeration): no
				// df, odds ratio or conf_level; continuity_corrected 0.
				bowkerCases++
				symmetryCloseTol(t, "p_value", *s.PValue, c.PValue, symmetryBowkerOracleTol)
				if c.OddsRatio.Set {
					t.Errorf("symmetry.json k x k exact case carries an odds ratio")
				}
				want = map[string]float64{"exact": 1, "states": float64(k), "discordant": c.Discordant, "pairs_dropped": c.PairsDropped,
					"continuity_corrected": c.ContinuityCorrected}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("parameters %v, want %v", got, want)
				}
				return
			}
			symmetryClose(t, "p_value", *s.PValue, c.PValue)
			if c.Exact {
				// The exact arm has no chi-square df; it records the method
				// and the discordant odds ratio with its exact CI.
				delete(want, "df")
				want["exact"] = 1
				want["conf_level"] = c.ConfLevel
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
	if exactCases < 9 || levelCases < 1 || bowkerCases < 5 {
		t.Fatalf("symmetry.json has %d exact 2 x 2 cases (%d at a non-default conf_level) and %d exact k x k, want at least 9 (1) and 5",
			exactCases, levelCases, bowkerCases)
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
		{"conf_level with exact on 3 states", symmetrySpec(`{"exact":true,"conf_level":0.95}`), symmetryHost([]string{"a", "b", "c"}, []string{"a", "b", "c"}, [][]float64{{1, 2, 3}, {3, 4, 5}, {5, 6, 7}}), errors.PULSE_OVERLAY_PARAM_MISSING, true},
		{"exact on 3 states, non-whole cell", symmetrySpec(`{"exact":true}`), symmetryHost([]string{"a", "b", "c"}, []string{"a", "b", "c"}, [][]float64{{1, 2, 3}, {3, 4, 5}, {5, 6.5, 7}}), errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED, true},
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

// TestSymmetry_ExactConfLevel: an absent conf_level and an explicit
// 0.95 emit the same exact layer byte for byte (the default is
// binom.test's), echoing conf_level 0.95; 0.9 narrows the odds-ratio
// interval around the same ratio and p, echoing 0.9; the chi-square
// layer carries no conf_level.
func TestSymmetry_ExactConfLevel(t *testing.T) {
	t.Parallel()

	run := func(params string) types.OverlayLayer {
		t.Helper()
		layer, _, err := applySymmetry(symmetrySpec(params),
			symmetryHost([]string{"yes", "no"}, []string{"yes", "no"}, [][]float64{{10, 9}, {4, 12}}))
		if err != nil {
			t.Fatal(err)
		}
		return layer
	}
	marshal := func(l types.OverlayLayer) string {
		t.Helper()
		b, err := types.MarshalFinite(l)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	def := run(`{"exact":true}`)
	if got, want := marshal(run(`{"exact":true,"conf_level":0.95}`)), marshal(def); got != want {
		t.Fatalf("explicit 0.95:\n got %s\nwant %s", got, want)
	}
	if def.Summary.Parameters["conf_level"] != 0.95 {
		t.Fatalf("default echo %v", def.Summary.Parameters)
	}
	ninety := run(`{"exact":true,"conf_level":0.9}`).Summary
	d := def.Summary
	if ninety.Parameters["conf_level"] != 0.9 || *ninety.PValue != *d.PValue ||
		ninety.Parameters["odds_ratio"] != d.Parameters["odds_ratio"] ||
		!(ninety.Parameters["odds_ratio_ci_lower"] > d.Parameters["odds_ratio_ci_lower"]) ||
		!(ninety.Parameters["odds_ratio_ci_upper"] < d.Parameters["odds_ratio_ci_upper"]) {
		t.Fatalf("0.9 interval %v, want strictly inside the 0.95 one %v", ninety.Parameters, d.Parameters)
	}
	if _, ok := run("").Summary.Parameters["conf_level"]; ok {
		t.Fatal("chi-square arm carries conf_level")
	}
}

// TestSymmetry_BowkerExact: params.exact on a k × k table runs the
// exact Bowker test — the statistic and scalar are the chi-square arm's
// uncorrected statistic bit for bit, the p is the exact one (not the
// chi-square's), the parameters are exactly exact / states / discordant
// / pairs_dropped / continuity_corrected 0 (no df, odds ratio or
// conf_level), correct is inert, and no PULSE_OVERLAY_EXPECTED_LOW is
// raised on a table the chi-square arm warns on. A diagonal table has a
// null statistic and p, every pair dropped.
func TestSymmetry_BowkerExact(t *testing.T) {
	t.Parallel()

	states := []string{"w", "x", "y", "z"}
	counts := [][]float64{{10, 2, 1, 0}, {0, 8, 3, 1}, {2, 1, 12, 2}, {1, 0, 4, 9}}
	chi, ws, err := applySymmetry(symmetrySpec(`{"correct":false}`), symmetryHost(states, states, counts))
	if err != nil || len(ws) != 1 {
		t.Fatalf("chi-square arm: warnings %+v err %v, want one EXPECTED_LOW", ws, err)
	}
	on, wsOn, err := applySymmetry(symmetrySpec(`{"exact":true}`), symmetryHost(states, states, counts))
	if err != nil {
		t.Fatal(err)
	}
	off, wsOff, err := applySymmetry(symmetrySpec(`{"exact":true,"correct":false}`), symmetryHost(states, states, counts))
	if err != nil {
		t.Fatal(err)
	}
	if len(wsOn) != 0 || len(wsOff) != 0 || !reflect.DeepEqual(on, off) {
		t.Fatalf("exact warnings %+v / %+v, or correct moved the layer: %+v vs %+v", wsOn, wsOff, on.Summary, off.Summary)
	}
	s := on.Summary
	if *s.Statistic != *chi.Summary.Statistic || *on.Payload.Scalar != *chi.Payload.Scalar {
		t.Fatalf("exact statistic %v scalar %v, want the chi-square's %v", *s.Statistic, *on.Payload.Scalar, *chi.Summary.Statistic)
	}
	// R brute-force enumeration (symmetry.json small_4x4_exact).
	if math.Abs(*s.PValue-0.51220703125) > 1e-12 || *s.PValue == *chi.Summary.PValue {
		t.Fatalf("exact p %.17g (chi-square %v), want 0.51220703125", *s.PValue, *chi.Summary.PValue)
	}
	want := map[string]float64{"exact": 1, "states": 4, "discordant": 17, "pairs_dropped": 0, "continuity_corrected": 0}
	if !reflect.DeepEqual(s.Parameters, want) {
		t.Fatalf("parameters %v, want %v", s.Parameters, want)
	}

	diag, _, err := applySymmetry(symmetrySpec(`{"exact":true}`), symmetryHost([]string{"a", "b", "c"}, []string{"a", "b", "c"},
		[][]float64{{9, 0, 0}, {0, 4, 0}, {0, 0, 6}}))
	if err != nil || !math.IsNaN(*diag.Summary.Statistic) || !math.IsNaN(*diag.Summary.PValue) || !math.IsNaN(*diag.Payload.Scalar) ||
		diag.Summary.Parameters["pairs_dropped"] != 3 {
		t.Fatalf("diagonal: %+v %v", diag.Summary, err)
	}
}

// TestSymmetry_BowkerExactTwoStatesIsMcNemar: on a 2 × 2 table the
// exact arm stays the binomial McNemar (statistic n_12, the odds ratio),
// and its p equals the exact Bowker test's on the one pair (D34: k = 2
// consistency) to a relative 1e-12.
func TestSymmetry_BowkerExactTwoStatesIsMcNemar(t *testing.T) {
	t.Parallel()

	for _, pr := range [][2]int{{1, 4}, {9, 2}, {12, 12}, {0, 7}, {150, 86}, {6, 7}} {
		host := symmetryHost([]string{"yes", "no"}, []string{"yes", "no"}, [][]float64{{3, float64(pr[0])}, {float64(pr[1]), 5}})
		layer, _, err := applySymmetry(symmetrySpec(`{"exact":true}`), host)
		if err != nil {
			t.Fatal(err)
		}
		if *layer.Summary.Statistic != float64(pr[0]) {
			t.Fatalf("%v: 2 x 2 exact statistic %v, want n_12", pr, *layer.Summary.Statistic)
		}
		if _, ok := layer.Summary.Parameters["odds_ratio"]; !ok {
			t.Fatalf("%v: 2 x 2 exact lost its odds ratio: %v", pr, layer.Summary.Parameters)
		}
		_, bowker, _, ok := statdist.BowkerExactP([][2]int{pr}, statdist.BowkerExactMaxTerms)
		if p := *layer.Summary.PValue; !ok || !(math.Abs(p-bowker) <= 1e-12*bowker) {
			t.Fatalf("%v: McNemar p %.17g, exact Bowker p %.17g", pr, p, bowker)
		}
	}
}

// TestSymmetry_BowkerExactRefusals: on k × k an explicit conf_level
// (even the 0.95 default) is needs_2x2 and an absent one runs; a
// non-whole discordant cell is non_integer naming the pair; a table
// whose convolution outgrows statdist.BowkerExactMaxTerms is
// exact_too_large, while the chi-square arm runs it — each refusal
// carrying the spec index.
func TestSymmetry_BowkerExactRefusals(t *testing.T) {
	t.Parallel()

	three := []string{"a", "b", "c"}
	small := [][]float64{{4, 2, 1}, {1, 5, 3}, {0, 2, 6}}
	refuse := func(params string, states []string, counts [][]float64) *errors.CodedError {
		t.Helper()
		_, _, err := applySymmetryAt(symmetrySpec(params), symmetryHost(states, states, counts), 3)
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Details["index"] != 3 {
			t.Fatalf("%s: err %v, want a coded refusal at index 3", params, err)
		}
		return ce
	}
	for _, params := range []string{`{"exact":true,"conf_level":0.95}`, `{"exact":true,"conf_level":0.9}`} {
		if ce := refuse(params, three, small); ce.Code != errors.PULSE_OVERLAY_PARAM_MISSING || ce.Details["reason"] != "needs_2x2" ||
			ce.Details["param"] != "conf_level" || ce.Details["states"] != 3 {
			t.Fatalf("%s: %v", params, ce)
		}
	}
	if _, _, err := applySymmetry(symmetrySpec(`{"exact":true}`), symmetryHost(three, three, small)); err != nil {
		t.Fatalf("absent conf_level on 3 states refused: %v", err)
	}

	frac := [][]float64{{4, 2, 1}, {1, 5, 3}, {0, 2.5, 6}}
	if ce := refuse(`{"exact":true}`, three, frac); ce.Code != errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED || ce.Details["reason"] != "non_integer" ||
		!reflect.DeepEqual(ce.Details["pair"], []string{"b", "c"}) {
		t.Fatalf("non-whole cell: %v", ce)
	}

	four := []string{"s1", "s2", "s3", "s4"}
	big := [][]float64{{50, 30, 28, 40}, {25, 60, 27, 35}, {31, 22, 55, 29}, {33, 30, 36, 70}}
	ce := refuse(`{"exact":true}`, four, big)
	if ce.Code != errors.PULSE_OVERLAY_PARAM_MISSING || ce.Details["reason"] != "exact_too_large" || ce.Details["param"] != "exact" ||
		ce.Details["limit"] != statdist.BowkerExactMaxTerms || !(ce.Details["terms"].(int) > statdist.BowkerExactMaxTerms) || ce.Details["states"] != 4 {
		t.Fatalf("too large: %v", ce)
	}
	if _, _, err := applySymmetry(symmetrySpec(""), symmetryHost(four, four, big)); err != nil {
		t.Fatalf("the chi-square arm refused the large table: %v", err)
	}
}
