package processing

import (
	"encoding/json"
	stderrors "errors"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// markovCase is one markov.json case (scripts/reference/gen_markov.R).
type markovCase struct {
	Name                string                        `json:"name"`
	States              []string                      `json:"states"`
	Counts              [][]float64                   `json:"counts"`
	Transition          [][]float64                   `json:"transition"`
	Steps               []int                         `json:"steps"`
	StepMatrices        map[string][][]float64        `json:"step_matrices"`
	Initial             json.RawMessage               `json:"initial"`
	InitialDistribution []float64                     `json:"initial_distribution"`
	Distributions       map[string][]float64          `json:"distributions"`
	Irreducible         bool                          `json:"irreducible"`
	Period              int                           `json:"period"`
	Classes             [][]string                    `json:"communicating_classes"`
	SteadyState         []float64                     `json:"steady_state"`
	Absorbing           []string                      `json:"absorbing"`
	MeanAbsorptionTime  map[string]float64            `json:"mean_absorption_time"`
	AbsorptionProbs     map[string]map[string]float64 `json:"absorption_probabilities"`
}

// markovHost builds a crosstab host over states whose rows run in state
// order and whose columns run in colOrder (a permutation of the state
// indices) — the alignment is by key, so the permutation must not
// matter. counts is in state × state order.
func markovHost(states []string, colOrder []int, counts [][]float64) *CrosstabHostView {
	mx := &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"from"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"to"}, Types: []string{"GROUP_CATEGORY"}},
		CellLabel:    "n",
	}
	for _, s := range states {
		mx.RowKeys = append(mx.RowKeys, types.AxisKey{s})
	}
	for _, j := range colOrder {
		mx.ColumnKeys = append(mx.ColumnKeys, types.AxisKey{states[j]})
	}
	for i := range states {
		row := make([]types.MatrixCell, len(colOrder))
		for c, j := range colOrder {
			row[c] = types.MatrixCell{Value: counts[i][j], Present: true}
		}
		mx.Cells = append(mx.Cells, row)
	}
	return NewCrosstabHostView(mx)
}

func markovReversed(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = n - 1 - i
	}
	return out
}

func markovIdentity(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

func markovSpec(params string) *types.OverlaySpec {
	s := &types.OverlaySpec{Kind: types.OverlayKindMarkov, Scope: types.OverlayScopeMatrix}
	if params != "" {
		s.Params = json.RawMessage(params)
	}
	return s
}

func mkClose(t *testing.T, where string, got, want, tol float64) {
	t.Helper()
	if d := math.Abs(got - want); !(d <= tol*math.Max(1, math.Abs(want))) {
		t.Errorf("%s = %.17g, want %.17g (R)", where, got, want)
	}
}

func assertStateMatrix(t *testing.T, where string, got *types.OverlayPayload, states []string, want [][]float64, tol float64) {
	t.Helper()
	if got == nil || got.Shape != types.OverlayShapeMatrix || got.Matrix == nil || len(got.Matrix.Cells) != len(want) {
		t.Fatalf("%s: %+v, want a %d-state MATRIX", where, got, len(want))
	}
	for i := range want {
		if got.Matrix.RowKeys[i][0] != states[i] || got.Matrix.ColumnKeys[i][0] != states[i] {
			t.Fatalf("%s: keys %v / %v not aligned to the states %v", where, got.Matrix.RowKeys, got.Matrix.ColumnKeys, states)
		}
		for j, w := range want[i] {
			mkClose(t, where+"["+strconv.Itoa(i)+"]["+strconv.Itoa(j)+"]", cellFloat(t, where, got.Matrix.Cells[i][j]), w, tol)
		}
	}
}

func assertStateSeries(t *testing.T, where string, got *types.OverlayPayload, states []string, want []float64, tol float64) {
	t.Helper()
	if got == nil || got.Shape != types.OverlayShapeSeries || got.Series == nil || len(got.Series.Entries) != len(want) {
		t.Fatalf("%s: %+v, want a %d-entry SERIES", where, got, len(want))
	}
	for i, w := range want {
		e := got.Series.Entries[i]
		if e.Key[0] != states[i] {
			t.Fatalf("%s: entry %d key %v, want %s", where, i, e.Key, states[i])
		}
		mkClose(t, where+"["+states[i]+"]", *e.Summary.Statistic, w, tol)
	}
}

func warningsWith(ws []types.OverlayWarning, code errors.Code, reason string) []types.OverlayWarning {
	var out []types.OverlayWarning
	for _, w := range ws {
		if w.Code == string(code) && (reason == "" || w.Details["reason"] == reason) {
			out = append(out, w)
		}
	}
	return out
}

// TestMarkov_MatchesR: the transition matrix, every Pⁿ, the projected
// distributions, irreducibility / period / class count, the steady
// state (when ergodic) and the mean absorption times equal
// markovchain's — with the host columns REVERSED against the rows, so
// a positional (not by-key) alignment would fail.
func TestMarkov_MatchesR(t *testing.T) {
	doc := loadReference[markovCase](t, "markov")
	if len(doc.Cases) < 6 {
		t.Fatalf("markov.json has %d cases, want at least 6", len(doc.Cases))
	}
	for _, c := range doc.Cases {
		t.Run(c.Name, func(t *testing.T) {
			params := map[string]any{"steps": c.Steps, "initial": c.Initial}
			if len(c.Absorbing) > 0 {
				params["absorbing"] = c.Absorbing
			}
			raw, _ := json.Marshal(params)
			layer, ws, err := applyMarkov(markovSpec(string(raw)), markovHost(c.States, markovReversed(len(c.States)), c.Counts))
			if err != nil {
				t.Fatal(err)
			}
			if layer.Payload.Shape != types.OverlayShapeMatrix {
				t.Fatalf("primary shape %s", layer.Payload.Shape)
			}
			assertStateMatrix(t, "transition", &layer.Payload, c.States, c.Transition, 1e-15)
			for _, n := range c.Steps {
				key := strconv.Itoa(n)
				assertStateMatrix(t, "step_"+key, layer.Payload.Parts["step_"+key], c.States, c.StepMatrices[key], 1e-11)
				assertStateSeries(t, "distribution_"+key, layer.Payload.Parts["distribution_"+key], c.States, c.Distributions[key], 1e-11)
			}
			s := layer.Summary.Parameters
			if (s["irreducible"] == 1) != c.Irreducible || s["classes"] != float64(len(c.Classes)) {
				t.Errorf("summary %v, R irreducible %v with %d classes", s, c.Irreducible, len(c.Classes))
			}
			if c.Irreducible && s["period"] != float64(c.Period) {
				t.Errorf("period %v, R %d", s["period"], c.Period)
			}
			notErgodic := warningsWith(ws, errors.PULSE_OVERLAY_MARKOV_NOT_ERGODIC, "")
			if c.SteadyState != nil {
				assertStateSeries(t, "steady_state", layer.Payload.Parts[MarkovPartSteadyState], c.States, c.SteadyState, 1e-10)
				if len(notErgodic) != 0 {
					t.Errorf("ergodic chain warned %+v", notErgodic)
				}
			} else {
				if _, ok := layer.Payload.Parts[MarkovPartSteadyState]; ok {
					t.Errorf("non-ergodic chain emitted a steady state")
				}
				want := "reducible"
				if c.Irreducible {
					want = "periodic"
				}
				if len(warningsWith(ws, errors.PULSE_OVERLAY_MARKOV_NOT_ERGODIC, want)) != 1 {
					t.Errorf("warnings %+v, want one %s PULSE_OVERLAY_MARKOV_NOT_ERGODIC", ws, want)
				}
			}
			if c.MeanAbsorptionTime == nil {
				if _, ok := layer.Payload.Parts[MarkovPartAbsorptionTime]; ok {
					t.Errorf("absorption_time emitted without params.absorbing")
				}
				return
			}
			part := layer.Payload.Parts[MarkovPartAbsorptionTime]
			if part == nil || len(part.Series.Entries) != len(c.MeanAbsorptionTime) {
				t.Fatalf("absorption_time %+v, want %d entries", part, len(c.MeanAbsorptionTime))
			}
			for _, e := range part.Series.Entries {
				name := e.Key[0].(string)
				mkClose(t, "absorption_time["+name+"]", *e.Summary.Statistic, c.MeanAbsorptionTime[name], 1e-10)
				var total float64
				for _, p := range c.AbsorptionProbs[name] {
					total += p
				}
				mkClose(t, "R absorption probability of "+name, total, 1, 1e-12) // certain in every fixture
			}
		})
	}
}

// TestMarkov_PowerIterationSelfCheck: on every ergodic fixture the
// steady state is a fixed point of P (π P = π within 1e-12) and sums to
// 1; a chain too sticky for 10,000 iterations falls back to the exact
// solve and still matches the closed form (b / (a + b), a / (a + b)).
func TestMarkov_PowerIterationSelfCheck(t *testing.T) {
	doc := loadReference[markovCase](t, "markov")
	checked := 0
	for _, c := range doc.Cases {
		if c.SteadyState == nil {
			continue
		}
		layer, _, err := applyMarkov(markovSpec(""), markovHost(c.States, markovIdentity(len(c.States)), c.Counts))
		if err != nil {
			t.Fatal(err)
		}
		k := len(c.States)
		pi := make([]float64, k)
		var sum float64
		for i, e := range layer.Payload.Parts[MarkovPartSteadyState].Series.Entries {
			pi[i] = *e.Summary.Statistic
			sum += pi[i]
		}
		mkClose(t, c.Name+" Σπ", sum, 1, 1e-12)
		P := make([]float64, k*k)
		for i := range c.Transition {
			copy(P[i*k:], c.Transition[i])
		}
		for j, v := range markovRowTimes(pi, P, k) {
			mkClose(t, c.Name+" (πP)_"+strconv.Itoa(j), v, pi[j], 1e-12)
		}
		if layer.Summary.Parameters["steady_state_converged"] != 1 || layer.Summary.Parameters["steady_state_iterations"] < 1 {
			t.Errorf("%s: summary %v, want a converged power iteration", c.Name, layer.Summary.Parameters)
		}
		checked++
	}
	if checked < 3 {
		t.Fatalf("%d ergodic fixtures checked, want at least 3", checked)
	}

	// a = P(x→y), b = P(y→x): π = (b, a) / (a + b). λ₂ = 1 − a − b =
	// 0.99985 needs ~1.8e5 steps to reach 1e-12 from the uniform start:
	// the exact solve answers.
	a, b := 0.0001, 0.00005
	host := markovHost([]string{"x", "y"}, markovIdentity(2), [][]float64{{(1 - a) * 1e6, a * 1e6}, {b * 1e6, (1 - b) * 1e6}})
	layer, _, err := applyMarkov(markovSpec(""), host)
	if err != nil {
		t.Fatal(err)
	}
	if s := layer.Summary.Parameters; s["steady_state_converged"] != 0 || s["steady_state_iterations"] != markovSteadyMaxIterations {
		t.Fatalf("summary %v, want the exact-solve fallback after %d iterations", s, markovSteadyMaxIterations)
	}
	assertStateSeries(t, "sticky steady_state", layer.Payload.Parts[MarkovPartSteadyState], []string{"x", "y"}, []float64{b / (a + b), a / (a + b)}, 1e-12)
}

// TestMarkov_Layer pins the layer's structure: name / kind / scope, the
// primary transition MATRIX on the row order, step_<n> parts for the
// default steps, distribution_<n> only with params.initial,
// absorption_time only with params.absorbing (over the non-absorbing
// states), the summary parameters.
func TestMarkov_Layer(t *testing.T) {
	states := []string{"a", "b", "c"}
	counts := [][]float64{{5, 3, 2}, {1, 6, 3}, {2, 2, 6}}
	layer, ws, err := applyMarkov(markovSpec(""), markovHost(states, []int{2, 0, 1}, counts))
	if err != nil || len(ws) != 0 {
		t.Fatalf("err %v warnings %+v", err, ws)
	}
	if layer.Name != "markov" || layer.Kind != types.OverlayKindMarkov || layer.Scope != types.OverlayScopeMatrix {
		t.Fatalf("layer identity %q %q %q", layer.Name, layer.Kind, layer.Scope)
	}
	want := []string{"steady_state", "step_1", "step_12", "step_3", "step_6"}
	var got []string
	for name := range layer.Payload.Parts {
		got = append(got, name)
	}
	if strings.Join(sortedStrings(got), ",") != strings.Join(want, ",") {
		t.Fatalf("parts %v, want %v", sortedStrings(got), want)
	}
	assertStateMatrix(t, "transition", &layer.Payload, states, [][]float64{{0.5, 0.3, 0.2}, {0.1, 0.6, 0.3}, {0.2, 0.2, 0.6}}, 1e-15)
	assertStateMatrix(t, "step_1", layer.Payload.Parts["step_1"], states, [][]float64{{0.5, 0.3, 0.2}, {0.1, 0.6, 0.3}, {0.2, 0.2, 0.6}}, 1e-15)
	s := layer.Summary.Parameters
	if s["states"] != 3 || s["classes"] != 1 || s["irreducible"] != 1 || s["period"] != 1 {
		t.Fatalf("summary %v", s)
	}

	layer, _, err = applyMarkov(markovSpec(`{"steps":[2],"initial":{"a":1},"absorbing":["c"]}`), markovHost(states, markovIdentity(3), counts))
	if err != nil {
		t.Fatal(err)
	}
	if len(layer.Payload.Parts) != 4 || layer.Payload.Parts["distribution_2"] == nil || layer.Payload.Parts["step_2"] == nil {
		t.Fatalf("parts %v", layer.Payload.Parts)
	}
	// P² row a = (0.5·0.5 + 0.3·0.1 + 0.2·0.2, …) = (0.32, 0.37, 0.31).
	assertStateSeries(t, "distribution_2", layer.Payload.Parts["distribution_2"], states, []float64{0.32, 0.37, 0.31}, 1e-15)
	at := layer.Payload.Parts[MarkovPartAbsorptionTime]
	if at == nil || len(at.Series.Entries) != 2 || at.Series.Entries[0].Key[0] != "a" || at.Series.Entries[1].Key[0] != "b" {
		t.Fatalf("absorption_time %+v, want entries a, b", at)
	}
	// (I − Q) t = 1 with Q = [[.5,.3],[.1,.6]]: det 0.17, t = (0.7, 0.6) / 0.17.
	mkClose(t, "t_a", *at.Series.Entries[0].Summary.Statistic, 0.7/0.17, 1e-14)
	mkClose(t, "t_b", *at.Series.Entries[1].Summary.Statistic, 0.6/0.17, 1e-14)
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// TestMarkov_Params: every malformed or unknown param — and a state key
// the host lacks — is PULSE_OVERLAY_PARAM_MISSING; the accepted forms
// run.
func TestMarkov_Params(t *testing.T) {
	states := []string{"a", "b", "c"}
	counts := [][]float64{{5, 3, 2}, {1, 6, 3}, {2, 2, 6}}
	many := make([]string, 65)
	for i := range many {
		many[i] = strconv.Itoa(i + 1)
	}
	for _, c := range []struct {
		name, params string
		refused      bool
		param        string
	}{
		{"default", "", false, ""},
		{"null", "null", false, ""},
		{"steps one", `{"steps":[1]}`, false, ""},
		{"steps max", `{"steps":[1000]}`, false, ""},
		{"steps 0", `{"steps":[0]}`, true, "steps"},
		{"steps 1001", `{"steps":[1001]}`, true, "steps"},
		{"steps fraction", `{"steps":[1.5]}`, true, "steps"},
		{"steps string", `{"steps":["3"]}`, true, "steps"},
		{"steps repeated", `{"steps":[3,3]}`, true, "steps"},
		{"steps empty", `{"steps":[]}`, true, "steps"},
		{"steps scalar", `{"steps":3}`, true, "steps"},
		{"steps too many", `{"steps":[` + strings.Join(many, ",") + `]}`, true, "steps"},
		{"absorbing ok", `{"absorbing":["c"]}`, false, ""},
		{"absorbing empty", `{"absorbing":[]}`, true, "absorbing"},
		{"absorbing repeated", `{"absorbing":["c","c"]}`, true, "absorbing"},
		{"absorbing blank", `{"absorbing":[""]}`, true, "absorbing"},
		{"absorbing string", `{"absorbing":"c"}`, true, "absorbing"},
		{"absorbing unknown state", `{"absorbing":["z"]}`, true, "absorbing"},
		{"initial margin", `{"initial":"margin"}`, false, ""},
		{"initial map", `{"initial":{"a":2,"b":0}}`, false, ""},
		{"initial other string", `{"initial":"uniform"}`, true, "initial"},
		{"initial empty map", `{"initial":{}}`, true, "initial"},
		{"initial negative", `{"initial":{"a":-1,"b":2}}`, true, "initial"},
		{"initial zero sum", `{"initial":{"a":0}}`, true, "initial"},
		{"initial number", `{"initial":1}`, true, "initial"},
		{"initial unknown state", `{"initial":{"a":1,"z":1}}`, true, "initial"},
		{"unknown key", `{"horizon":12}`, true, ""},
		{"not an object", `[1]`, true, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := applyMarkovAt(markovSpec(c.params), markovHost(states, markovIdentity(3), counts), 4)
			if !c.refused {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_OVERLAY_PARAM_MISSING {
				t.Fatalf("err %v, want PULSE_OVERLAY_PARAM_MISSING", err)
			}
			if ce.Details["index"] != 4 || ce.Details["kind"] != string(types.OverlayKindMarkov) {
				t.Errorf("details %v lack index / kind", ce.Details)
			}
			if c.param != "" && ce.Details["param"] != c.param {
				t.Errorf("details param %v, want %s", ce.Details["param"], c.param)
			}
		})
	}
}

// TestMarkov_HostRefusals: different grouper counts or key sets per
// axis are PULSE_OVERLAY_MATRIX_NOT_SQUARE; a non-count cell or a
// negative / non-finite cell is PULSE_OVERLAY_NONNEGATIVE_REQUIRED;
// fewer than 2 states or a zero total is
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
func TestMarkov_HostRefusals(t *testing.T) {
	states := []string{"a", "b", "c"}
	counts := [][]float64{{5, 3, 2}, {1, 6, 3}, {2, 2, 6}}
	for _, c := range []struct {
		name string
		host func() *CrosstabHostView
		want errors.Code
	}{
		{"arity", func() *CrosstabHostView {
			h := markovHost(states, markovIdentity(3), counts)
			h.payload.ColumnHeader = types.AxisHeader{Fields: []string{"to", "region"}, Types: []string{"GROUP_CATEGORY", "GROUP_CATEGORY"}}
			return h
		}, errors.PULSE_OVERLAY_MATRIX_NOT_SQUARE},
		{"column-only key", func() *CrosstabHostView {
			h := markovHost(states, markovIdentity(3), counts)
			h.payload.ColumnKeys[2] = types.AxisKey{"churned"}
			return h
		}, errors.PULSE_OVERLAY_MATRIX_NOT_SQUARE},
		{"missing column", func() *CrosstabHostView {
			h := markovHost(states, markovIdentity(3), counts)
			h.payload.ColumnKeys = h.payload.ColumnKeys[:2]
			for i := range h.payload.Cells {
				h.payload.Cells[i] = h.payload.Cells[i][:2]
			}
			return h
		}, errors.PULSE_OVERLAY_MATRIX_NOT_SQUARE},
		{"average cell", func() *CrosstabHostView {
			h := markovHost(states, markovIdentity(3), counts)
			h.cellType = types.AGG_AVERAGE
			return h
		}, errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED},
		{"negative cell", func() *CrosstabHostView {
			h := markovHost(states, markovIdentity(3), counts)
			h.payload.Cells[1][2].Value = -1.0
			return h
		}, errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED},
		{"NaN cell", func() *CrosstabHostView {
			h := markovHost(states, markovIdentity(3), counts)
			h.payload.Cells[0][0].Value = math.NaN()
			return h
		}, errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED},
		{"one state", func() *CrosstabHostView {
			return markovHost([]string{"a"}, markovIdentity(1), [][]float64{{4}})
		}, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"zero total", func() *CrosstabHostView {
			return markovHost([]string{"a", "b"}, markovIdentity(2), [][]float64{{0, 0}, {0, 0}})
		}, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := applyMarkovAt(markovSpec(""), c.host(), 2)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != c.want {
				t.Fatalf("err %v, want %s", err, c.want)
			}
		})
	}

	// The key-set refusal names the one-axis states, sorted.
	h := markovHost(states, markovIdentity(3), counts)
	h.payload.ColumnKeys[2] = types.AxisKey{"churned"}
	_, _, err := applyMarkovAt(markovSpec(""), h, 0)
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatal(err)
	}
	if rows, cols := ce.Details["row_only"].([]string), ce.Details["column_only"].([]string); len(rows) != 1 || rows[0] != "c" || len(cols) != 1 || cols[0] != "churned" || ce.Details["index"] != 0 {
		t.Fatalf("details %v", ce.Details)
	}

	// A count or sum cell, and an absent cell (= 0), run.
	for _, edit := range []func(*CrosstabHostView){
		func(h *CrosstabHostView) { h.cellType = types.AGG_COUNT },
		func(h *CrosstabHostView) { h.cellType = types.AGG_SUM },
		func(h *CrosstabHostView) { h.payload.Cells[0][1] = types.MatrixCell{} },
	} {
		h := markovHost(states, markovIdentity(3), counts)
		edit(h)
		if _, _, err := applyMarkov(markovSpec(""), h); err != nil {
			t.Fatal(err)
		}
	}
}

// TestMarkov_ZeroRowAbsorbs: a state with a zero row becomes the unit
// row (it absorbs) with one PULSE_OVERLAY_REF_ZERO warning naming it.
func TestMarkov_ZeroRowAbsorbs(t *testing.T) {
	states := []string{"a", "b", "c"}
	layer, ws, err := applyMarkov(markovSpec(""), markovHost(states, markovIdentity(3), [][]float64{{5, 3, 2}, {1, 6, 3}, {0, 0, 0}}))
	if err != nil {
		t.Fatal(err)
	}
	row := layer.Payload.Matrix.Cells[2]
	if row[0].Value != 0.0 || row[1].Value != 0.0 || row[2].Value != 1.0 {
		t.Fatalf("zero row → %v, want the unit row", row)
	}
	zero := warningsWith(ws, errors.PULSE_OVERLAY_REF_ZERO, "zero_row_absorbing")
	if len(zero) != 1 {
		t.Fatalf("warnings %+v, want one zero_row_absorbing", ws)
	}
	if keys := zero[0].Details["states"].([]types.AxisKey); len(keys) != 1 || keys[0][0] != "c" {
		t.Fatalf("zero-row warning states %v", zero[0].Details["states"])
	}
}

// TestMarkov_AbsorptionNotCertain: a state that can fall into a closed
// class outside params.absorbing (d ↔ e here) has a null time and one
// absorption_not_certain warning; a state that can only reach the
// absorbing set keeps its exact time.
func TestMarkov_AbsorptionNotCertain(t *testing.T) {
	states := []string{"a", "b", "z", "d", "e"}
	counts := [][]float64{
		{1, 1, 2, 0, 0}, // a → a, b, z: certain
		{0, 2, 1, 1, 0}, // b → d: may never be absorbed
		{0, 0, 1, 0, 0}, // z absorbs
		{0, 0, 0, 1, 1}, // d ↔ e: a closed class without z
		{0, 0, 0, 1, 1},
	}
	layer, ws, err := applyMarkov(markovSpec(`{"absorbing":["z"]}`), markovHost(states, markovIdentity(5), counts))
	if err != nil {
		t.Fatal(err)
	}
	at := layer.Payload.Parts[MarkovPartAbsorptionTime].Series.Entries
	if len(at) != 4 {
		t.Fatalf("absorption_time %+v, want a, b, d, e", at)
	}
	// a can reach d through b, so its absorption is not certain either.
	for _, e := range at {
		if !math.IsNaN(*e.Summary.Statistic) {
			t.Errorf("%v: %v, want NaN (absorption not certain)", e.Key, *e.Summary.Statistic)
		}
	}
	nc := warningsWith(ws, errors.PULSE_OVERLAY_MARKOV_NOT_ERGODIC, "absorption_not_certain")
	if len(nc) != 1 || len(nc[0].Details["states"].([]types.AxisKey)) != 4 {
		t.Fatalf("warnings %+v, want one absorption_not_certain over a, b, d, e", ws)
	}

	// Cut a → b: a reaches only itself and z, so its time is exact
	// (1 / P(a → z) = 1 / (2/3) = 1.5).
	counts[0] = []float64{1, 0, 2, 0, 0}
	layer, ws, err = applyMarkov(markovSpec(`{"absorbing":["z"]}`), markovHost(states, markovIdentity(5), counts))
	if err != nil {
		t.Fatal(err)
	}
	at = layer.Payload.Parts[MarkovPartAbsorptionTime].Series.Entries
	mkClose(t, "t_a", *at[0].Summary.Statistic, 1.5, 1e-15)
	if nc := warningsWith(ws, errors.PULSE_OVERLAY_MARKOV_NOT_ERGODIC, "absorption_not_certain"); len(nc) != 1 || len(nc[0].Details["states"].([]types.AxisKey)) != 3 {
		t.Fatalf("warnings %+v, want b, d, e uncertain", ws)
	}
}

// TestMarkov_SingularFundamentalMatrix: an I − Q the LU refuses (here a
// leak so small that 1 − P_bb rounds to 0) drops the absorption_time
// part with a coded PULSE_MATRIX_SINGULAR warning — never a NaN time.
func TestMarkov_SingularFundamentalMatrix(t *testing.T) {
	states := []string{"a", "b", "z"}
	counts := [][]float64{{1, 1, 0}, {0, 1e17, 1}, {0, 0, 1}}
	layer, ws, err := applyMarkov(markovSpec(`{"absorbing":["z"]}`), markovHost(states, markovIdentity(3), counts))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := layer.Payload.Parts[MarkovPartAbsorptionTime]; ok {
		t.Fatal("absorption_time emitted over a singular fundamental matrix")
	}
	sing := warningsWith(ws, errors.PULSE_MATRIX_SINGULAR, "")
	if len(sing) != 1 || sing[0].Details["kind"] != string(types.OverlayKindMarkov) || sing[0].Details["part"] != MarkovPartAbsorptionTime {
		t.Fatalf("warnings %+v, want one PULSE_MATRIX_SINGULAR on absorption_time", ws)
	}
}

// TestMarkov_ScaleInvariant: scaling every cell (a weighted host's Σw
// table) moves no figure.
func TestMarkov_ScaleInvariant(t *testing.T) {
	states := []string{"a", "b", "c"}
	counts := [][]float64{{5, 3, 2}, {1, 6, 3}, {2, 2, 6}}
	scaled := make([][]float64, 3)
	for i := range counts {
		for _, v := range counts[i] {
			scaled[i] = append(scaled[i], v*0.37)
		}
	}
	spec := markovSpec(`{"initial":"margin","absorbing":["c"]}`)
	a, _, err := applyMarkov(spec, markovHost(states, markovIdentity(3), counts))
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := applyMarkov(spec, markovHost(states, markovIdentity(3), scaled))
	if err != nil {
		t.Fatal(err)
	}
	for name, part := range a.Payload.Parts {
		other := b.Payload.Parts[name]
		switch part.Shape {
		case types.OverlayShapeMatrix:
			for i, row := range part.Matrix.Cells {
				for j, c := range row {
					mkClose(t, name, other.Matrix.Cells[i][j].Value.(float64), c.Value.(float64), 1e-14)
				}
			}
		case types.OverlayShapeSeries:
			for i, e := range part.Series.Entries {
				mkClose(t, name, *other.Series.Entries[i].Summary.Statistic, *e.Summary.Statistic, 1e-12)
			}
		}
	}
}
