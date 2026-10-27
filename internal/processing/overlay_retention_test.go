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

func retentionSpec(params string) *types.OverlaySpec {
	s := &types.OverlaySpec{Kind: types.OverlayKindRetention, Scope: types.OverlayScopeRow}
	if params != "" {
		s.Params = json.RawMessage(params)
	}
	return s
}

// retentionTiers is the hand-checked fixture: three tiers over the rows;
// the columns name the same tiers in REVERSED order plus the
// column-only churned state, so a positional (not by-key) diagonal
// would read the wrong cell.
//
//	from \ to   churned  bronze  silver  gold   R
//	gold           10       5      25     60   100
//	silver         20      22      50      8   100
//	bronze         10      30       9      1    50
func retentionTiers() *CrosstabHostView {
	return symmetryHost(
		[]string{"gold", "silver", "bronze"},
		[]string{"churned", "bronze", "silver", "gold"},
		[][]float64{{10, 5, 25, 60}, {20, 22, 50, 8}, {10, 30, 9, 1}})
}

type retentionWant struct {
	key                     string
	stay, sw, churn, totalR float64
}

// checkRetentionEntries compares the SERIES entries to want EXACTLY:
// every hand-checked rate is a single correctly rounded division of
// integers, so it equals its decimal literal bit for bit.
func checkRetentionEntries(t *testing.T, layer types.OverlayLayer, want []retentionWant, withChurn bool) {
	t.Helper()
	if layer.Name != "retention" || layer.Payload.Shape != types.OverlayShapeSeries || layer.Payload.Series == nil {
		t.Fatalf("layer %+v", layer)
	}
	es := layer.Payload.Series.Entries
	if len(es) != len(want) {
		t.Fatalf("%d entries, want %d: %+v", len(es), len(want), es)
	}
	for i, w := range want {
		e := es[i]
		if !reflect.DeepEqual(e.Key, types.AxisKey{w.key}) {
			t.Errorf("entry %d key %v, want %s", i, e.Key, w.key)
		}
		if e.Summary.Statistic == nil || *e.Summary.Statistic != w.stay {
			t.Errorf("%s stay %v, want %v", w.key, e.Summary.Statistic, w.stay)
		}
		p := e.Summary.Parameters
		if p["switch"] != w.sw || p["from_total"] != w.totalR {
			t.Errorf("%s parameters %v, want switch %v from_total %v", w.key, p, w.sw, w.totalR)
		}
		if c, ok := p["churn"]; ok != withChurn || (withChurn && c != w.churn) {
			t.Errorf("%s churn %v (present %v), want %v (present %v)", w.key, c, ok, w.churn, withChurn)
		}
		if e.Summary.PValue != nil {
			t.Errorf("%s carries a p-value on a descriptive kind", w.key)
		}
	}
}

// TestRetention_HandOracle: per from-state stay / switch / churn and the
// pooled layer rates equal the hand-computed figures, with churn into
// the named absorbing state; the diagonal aligns by key.
func TestRetention_HandOracle(t *testing.T) {
	t.Parallel()

	layer, ws, err := applyRetention(retentionSpec(`{"absorbing":["churned"]}`), retentionTiers())
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 0 {
		t.Fatalf("warnings %+v", ws)
	}
	// gold:   stay 60/100, switch (5+25)/100, churn 10/100
	// silver: stay 50/100, switch (22+8)/100, churn 20/100
	// bronze: stay 30/50,  switch (9+1)/50,   churn 10/50
	checkRetentionEntries(t, layer, []retentionWant{
		{"gold", 0.6, 0.3, 0.1, 100},
		{"silver", 0.5, 0.3, 0.2, 100},
		{"bronze", 0.6, 0.2, 0.2, 50},
	}, true)
	// Pooled over 250 units: stay 140, switch 70, churn 40.
	want := map[string]float64{"stay": 0.56, "switch": 0.28, "churn": 0.16, "states": 3, "total": 250}
	if !reflect.DeepEqual(layer.Summary.Parameters, want) {
		t.Fatalf("layer parameters %v, want %v", layer.Summary.Parameters, want)
	}
	if layer.Summary.Statistic != nil || layer.Summary.PValue != nil {
		t.Fatalf("layer summary %+v carries a statistic / p", layer.Summary)
	}
}

// TestRetention_NoAbsorbing: without params.absorbing every move out is
// a switch, churn is absent everywhere, and churned is just another
// to-state.
func TestRetention_NoAbsorbing(t *testing.T) {
	t.Parallel()

	layer, _, err := applyRetention(retentionSpec(""), retentionTiers())
	if err != nil {
		t.Fatal(err)
	}
	checkRetentionEntries(t, layer, []retentionWant{
		{"gold", 0.6, 0.4, 0, 100},
		{"silver", 0.5, 0.5, 0, 100},
		{"bronze", 0.6, 0.4, 0, 50},
	}, false)
	want := map[string]float64{"stay": 0.56, "switch": 0.44, "states": 3, "total": 250}
	if !reflect.DeepEqual(layer.Summary.Parameters, want) {
		t.Fatalf("layer parameters %v, want %v", layer.Summary.Parameters, want)
	}
}

// TestRetention_AbsorbingRowNotReported: an observed row for a state
// named in params.absorbing (a reactivation, a stay in churned) is not
// a retention rate — it is left out of the entries and the pooled
// rates; a move from an active state into it is still churn.
func TestRetention_AbsorbingRowNotReported(t *testing.T) {
	t.Parallel()

	host := symmetryHost(
		[]string{"active", "churned"},
		[]string{"active", "churned"},
		[][]float64{{30, 10}, {4, 6}})
	layer, _, err := applyRetention(retentionSpec(`{"absorbing":["churned"]}`), host)
	if err != nil {
		t.Fatal(err)
	}
	checkRetentionEntries(t, layer, []retentionWant{{"active", 0.75, 0, 0.25, 40}}, true)
	if got := layer.Summary.Parameters["states"]; got != 1 {
		t.Fatalf("states %v, want 1", got)
	}
	if got := layer.Summary.Parameters["total"]; got != 40 {
		t.Fatalf("total %v, want 40 (the churned row is not pooled)", got)
	}
}

// TestRetention_RowOnlyStateStaysZero: a from-state with no column of
// its own (nobody ended there) has stay 0 — every unit switched.
func TestRetention_RowOnlyStateStaysZero(t *testing.T) {
	t.Parallel()

	host := symmetryHost([]string{"new", "gold"}, []string{"gold"}, [][]float64{{4}, {6}})
	layer, _, err := applyRetention(retentionSpec(""), host)
	if err != nil {
		t.Fatal(err)
	}
	checkRetentionEntries(t, layer, []retentionWant{{"new", 0, 1, 0, 4}, {"gold", 1, 0, 0, 6}}, false)
}

// TestRetention_ZeroRowIsNull: a from-state whose row totals 0 (an
// AGG_SUM host) has null rates — undefined, never 0 — and one
// PULSE_OVERLAY_REF_ZERO naming it; it adds nothing to the pooled
// rates. Every figure null when no row has a total.
func TestRetention_ZeroRowIsNull(t *testing.T) {
	t.Parallel()

	host := symmetryHost([]string{"a", "b"}, []string{"a", "b"}, [][]float64{{3, 1}, {0, 0}})
	layer, ws, err := applyRetention(retentionSpec(""), host)
	if err != nil {
		t.Fatal(err)
	}
	es := layer.Payload.Series.Entries
	if len(es) != 2 || !math.IsNaN(*es[1].Summary.Statistic) || !math.IsNaN(es[1].Summary.Parameters["switch"]) || es[1].Summary.Parameters["from_total"] != 0 {
		t.Fatalf("zero row entry %+v", es)
	}
	if *es[0].Summary.Statistic != 0.75 {
		t.Fatalf("defined row stay %v", *es[0].Summary.Statistic)
	}
	if len(ws) != 1 || ws[0].Code != string(errors.PULSE_OVERLAY_REF_ZERO) || ws[0].Details["reason"] != "zero_row" ||
		!reflect.DeepEqual(ws[0].Details["states"], []types.AxisKey{{"b"}}) {
		t.Fatalf("warnings %+v", ws)
	}
	if p := layer.Summary.Parameters; p["stay"] != 0.75 || p["total"] != 4 || p["states"] != 2 {
		t.Fatalf("pooled %v", p)
	}
	out, err := json.Marshal(layer)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var wire struct {
		Payload struct {
			Series struct {
				Entries []map[string]any `json:"entries"`
			} `json:"series"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(out, &wire); err != nil {
		t.Fatal(err)
	}
	if s := wire.Payload.Series.Entries[1]["summary"].(map[string]any); s["statistic"] != nil {
		t.Fatalf("zero-row stay on the wire %v, want null", s["statistic"])
	}

	empty := symmetryHost([]string{"a"}, []string{"a"}, [][]float64{{0}})
	layer, ws, err = applyRetention(retentionSpec(""), empty)
	if err != nil {
		t.Fatal(err)
	}
	if p := layer.Summary.Parameters; !math.IsNaN(p["stay"]) || !math.IsNaN(p["switch"]) || p["total"] != 0 || len(ws) != 1 {
		t.Fatalf("all-zero host: %v %+v", p, ws)
	}
}

// TestRetention_UndefinedCellIsAbsent: an undefined (NaN) cell is read
// as an absent one (E4-S6) — the same layer as a 0 there.
func TestRetention_UndefinedCellIsAbsent(t *testing.T) {
	t.Parallel()

	nan := symmetryHost([]string{"a", "b"}, []string{"a", "b"}, [][]float64{{5, math.NaN()}, {2, 6}})
	zero := symmetryHost([]string{"a", "b"}, []string{"a", "b"}, [][]float64{{5, 0}, {2, 6}})
	got, _, err := applyRetention(retentionSpec(""), nan)
	if err != nil {
		t.Fatal(err)
	}
	want, _, err := applyRetention(retentionSpec(""), zero)
	if err != nil {
		t.Fatal(err)
	}
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	if string(g) != string(w) {
		t.Fatalf("NaN cell\n%s\nwant the zero-cell\n%s", g, w)
	}
}

// TestRetention_ScaleInvariant: a Σw table (every cell times a common
// weight) gives the same rates as the count table — the reason the kind
// carries no weight class.
func TestRetention_ScaleInvariant(t *testing.T) {
	t.Parallel()

	base, _, err := applyRetention(retentionSpec(`{"absorbing":["churned"]}`), retentionTiers())
	if err != nil {
		t.Fatal(err)
	}
	scaled := symmetryHost(
		[]string{"gold", "silver", "bronze"},
		[]string{"churned", "bronze", "silver", "gold"},
		[][]float64{{20, 10, 50, 120}, {40, 44, 100, 16}, {20, 60, 18, 2}})
	got, _, err := applyRetention(retentionSpec(`{"absorbing":["churned"]}`), scaled)
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range got.Payload.Series.Entries {
		b := base.Payload.Series.Entries[i]
		if *e.Summary.Statistic != *b.Summary.Statistic || e.Summary.Parameters["switch"] != b.Summary.Parameters["switch"] ||
			e.Summary.Parameters["churn"] != b.Summary.Parameters["churn"] || e.Summary.Parameters["from_total"] != 2*b.Summary.Parameters["from_total"] {
			t.Fatalf("entry %d: doubled table %+v, base %+v", i, e.Summary, b.Summary)
		}
	}
}

// TestRetention_Refusals: each runtime refusal carries its own code (and
// the spec index through applyRetentionAt), never PROCESSING_INTERNAL.
func TestRetention_Refusals(t *testing.T) {
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
		{"unknown param", retentionSpec(`{"steps":[1]}`), square(), errors.PULSE_OVERLAY_PARAM_MISSING, true},
		{"empty absorbing", retentionSpec(`{"absorbing":[]}`), square(), errors.PULSE_OVERLAY_PARAM_MISSING, true},
		{"unknown absorbing state", retentionSpec(`{"absorbing":["lapsed"]}`), square(), errors.PULSE_OVERLAY_PARAM_MISSING, true},
		{"average cell", retentionSpec(""), avg, errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED, true},
		{"grouper counts differ", retentionSpec(""), twoCols, errors.PULSE_OVERLAY_MATRIX_NOT_SQUARE, true},
		{"negative cell", retentionSpec(""), symmetryHost([]string{"a", "b"}, []string{"a", "b"}, [][]float64{{1, -2}, {3, 4}}), errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := applyRetentionAt(c.spec, c.host, 3)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != c.want {
				t.Fatalf("err %v, want %s", err, c.want)
			}
			if c.indexd && ce.Details["index"] != 3 {
				t.Fatalf("details %v lack the spec index", ce.Details)
			}
		})
	}
	// The unknown-state refusal names the unknown key and the host's
	// states (rows, then column-only keys).
	host := symmetryHost([]string{"a"}, []string{"a", "z"}, [][]float64{{1, 2}})
	_, _, err := applyRetentionAt(retentionSpec(`{"absorbing":["z","q"]}`), host, 0)
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || !reflect.DeepEqual(ce.Details["unknown"], []string{"q"}) ||
		!reflect.DeepEqual(ce.Details["states"], []string{"a", "z"}) || ce.Details["param"] != "absorbing" {
		t.Fatalf("unknown-state refusal %v", err)
	}
}

// TestRetention_DoesNotMutateHost: the overlay is a read-only sibling —
// the host payload is byte-identical after the handler runs.
func TestRetention_DoesNotMutateHost(t *testing.T) {
	t.Parallel()

	host := retentionTiers()
	before, _ := json.Marshal(host.Payload())
	if _, _, err := applyRetention(retentionSpec(`{"absorbing":["churned"]}`), host); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(host.Payload())
	if string(before) != string(after) {
		t.Fatalf("host moved:\n%s\n%s", before, after)
	}
}
