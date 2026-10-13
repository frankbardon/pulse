package processing

import (
	stderrors "errors"
	"math"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// Part names OVERLAY_MARKOV writes into OverlayPayload.Parts. The
// per-step parts are MarkovPartStepPrefix / MarkovPartDistributionPrefix
// + the step (step_3, distribution_12).
const (
	// MarkovPartStepPrefix prefixes the Pⁿ matrix parts.
	MarkovPartStepPrefix = "step_"
	// MarkovPartDistributionPrefix prefixes the projected distribution
	// series parts (only with params.initial).
	MarkovPartDistributionPrefix = "distribution_"
	// MarkovPartSteadyState is the stationary distribution series (only
	// for an irreducible aperiodic chain).
	MarkovPartSteadyState = "steady_state"
	// MarkovPartAbsorptionTime is the expected-steps-to-absorption series
	// over the non-absorbing states (only with params.absorbing).
	MarkovPartAbsorptionTime = "absorption_time"
)

const (
	// markovSteadyTolerance stops the power iteration once no share
	// moves by this much in one step.
	markovSteadyTolerance = 1e-12
	// markovSteadyMaxIterations caps the power iteration; a slower
	// chain falls back to the exact linear solve.
	markovSteadyMaxIterations = 10000
)

// applyMarkov is the OVERLAY_MARKOV handler: the host crosstab read as
// a from-state (row) × to-state (column) transition count table.
//
//	states    = the row keys, in row order; the columns are aligned to
//	            them BY KEY (overlayhost.MarkovStateName), never by
//	            position — the two key sets must be equal as sets, save
//	            that a column-only state declared in params.absorbing
//	            gets a synthesized unit row (appended in column order;
//	            one PULSE_OVERLAY_REF_ZERO {kind, axis: row, states,
//	            reason: absorbing_row_synthesized} plus the
//	            synthesized_rows summary parameter)
//	P_ij      = n_ij / Σ_j n_ij    (a zero row → the unit row: absorbing)
//	step_n    = Pⁿ                 (binary powering)
//	dist_n    = π₀ · Pⁿ            (π₀ = params.initial, normalized)
//	π         = lim π_t,  π_{t+1} = π_t · P from the uniform start, until
//	            max |Δ| < 1e-12 (≤ 10,000 steps; past that the exact
//	            solve π (I − P) = 0, Σ π = 1 through linalg.Solve)
//	t         = (I − Q)⁻¹ · 1      (Q = P over the non-absorbing states
//	            that are certain to be absorbed; linalg.FactorLU)
//
// The steady state is emitted only for an irreducible (one
// communicating class, Tarjan) aperiodic (gcd of cycle lengths = 1,
// from BFS levels) chain; otherwise one PULSE_OVERLAY_MARKOV_NOT_ERGODIC
// warning {kind, reason: reducible, classes} / {kind, reason: periodic,
// period} replaces the part. A non-absorbing state that can reach a
// closed class outside params.absorbing may never be absorbed: its
// absorption_time is NaN (null) and one PULSE_OVERLAY_MARKOV_NOT_ERGODIC
// {kind, reason: absorption_not_certain, states} names them. A
// fundamental matrix the LU refuses as numerically singular drops the
// absorption_time part with a PULSE_MATRIX_SINGULAR warning (linalg's
// details plus kind and part) — never a silent NaN.
//
// The table: absent cells are 0; a cell aggregation other than
// AGG_COUNT / AGG_SUM or a negative / non-finite cell is
// PULSE_OVERLAY_NONNEGATIVE_REQUIRED; a different grouper count per
// axis, a row-only key or an undeclared column-only key is
// PULSE_OVERLAY_MATRIX_NOT_SQUARE; fewer
// than 2 states or a zero grand total is
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE; an absorbing / initial key
// naming no state is PULSE_OVERLAY_PARAM_MISSING. Every output is
// scale-invariant (rows are normalized), so a weighted host's Σw table
// is read as is.
//
// Every product, sum and difference below is FMA-free (an explicit
// float64 conversion around each product), so the figures are
// bit-identical on every architecture.
func applyMarkov(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
	return applyMarkovAt(spec, host, -1)
}

// applyMarkovAt is applyMarkov for the spec at index of Request.Overlays
// (-1 unknown): the MATRIX dispatcher passes it so the params, cell,
// arity and state refusals carry the same details — index included — as
// predict's.
func applyMarkovAt(spec *types.OverlaySpec, host *CrosstabHostView, index int) (types.OverlayLayer, []types.OverlayWarning, error) {
	params, cerr := overlayhost.ParseMarkovParams(spec, index)
	if cerr != nil {
		return types.OverlayLayer{}, nil, cerr
	}
	if cerr := overlayhost.NonnegativeCellRefusal(spec.Kind, host.cellType, index); cerr != nil {
		return types.OverlayLayer{}, nil, cerr
	}
	payload := host.Payload()
	if cerr := overlayhost.MarkovArityRefusal(spec.Kind, len(payload.RowHeader.Fields), len(payload.ColumnHeader.Fields), index); cerr != nil {
		return types.OverlayLayer{}, nil, cerr
	}

	// Align the columns to the row states by key.
	hostRows := host.RowCount()
	names := make([]string, hostRows)
	stateIndex := make(map[string]int, hostRows)
	stateKeys := make([]types.AxisKey, hostRows)
	for i := 0; i < hostRows; i++ {
		names[i] = overlayhost.MarkovStateName(payload.RowKeys[i])
		stateIndex[names[i]] = i
		stateKeys[i] = payload.RowKeys[i]
	}
	colOf := make([]int, hostRows) // colOf[state] = host column index
	for i := range colOf {
		colOf[i] = -1
	}
	var rowOnly, columnOnly []string
	var columnOnlyAt []int
	for j := 0; j < host.ColumnCount(); j++ {
		name := overlayhost.MarkovStateName(payload.ColumnKeys[j])
		if i, ok := stateIndex[name]; ok && colOf[i] < 0 {
			colOf[i] = j
		} else {
			columnOnly = append(columnOnly, name)
			columnOnlyAt = append(columnOnlyAt, j)
		}
	}
	for i, j := range colOf {
		if j < 0 {
			rowOnly = append(rowOnly, names[i])
		}
	}
	// A column-only state the caller declared absorbing has a row known
	// by definition (the unit row): synthesize it after the observed
	// rows, in column order. Any other one-axis key is still refused.
	if len(rowOnly) == 0 && len(columnOnly) > 0 && markovAllDeclared(columnOnly, params.Absorbing, stateIndex) {
		for c, name := range columnOnly {
			stateIndex[name] = len(names)
			names = append(names, name)
			stateKeys = append(stateKeys, payload.ColumnKeys[columnOnlyAt[c]])
			colOf = append(colOf, columnOnlyAt[c])
		}
		rowOnly, columnOnly = nil, nil
	}
	if cerr := overlayhost.MarkovKeySetRefusal(spec.Kind, rowOnly, columnOnly, index); cerr != nil {
		return types.OverlayLayer{}, nil, cerr
	}
	k := len(names)
	synthesized := k - hostRows

	// The count table and its row totals.
	counts := make([]float64, k*k)
	rowTotals := make([]float64, k)
	var grand float64
	for i := 0; i < hostRows; i++ {
		for s := 0; s < k; s++ {
			j := colOf[s]
			if i >= len(payload.Cells) || j >= len(payload.Cells[i]) || !payload.Cells[i][j].Present {
				continue // absent cell = 0
			}
			v, ok := host.CellAt(i, j)
			if !ok {
				v = math.NaN()
			}
			if cerr := overlayhost.NonnegativeValueRefusal(spec.Kind, payload.RowKeys[i], payload.ColumnKeys[j], v); cerr != nil {
				return types.OverlayLayer{}, nil, cerr
			}
			counts[i*k+s] = v
			rowTotals[i] += v
		}
		grand += rowTotals[i]
	}
	if k < 2 || !(grand > 0) {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
			"overlay "+string(spec.Kind)+" requires at least 2 states and a non-zero table total",
			map[string]any{"kind": string(spec.Kind), "states": k, "total": grand})
	}

	// Params naming states: every key must be a host state.
	absorbing := make([]bool, k)
	if params.Absorbing != nil {
		var unknown []string
		for _, n := range params.Absorbing {
			if i, ok := stateIndex[n]; ok {
				absorbing[i] = true
			} else {
				unknown = append(unknown, n)
			}
		}
		if cerr := overlayhost.MarkovStateRefusal(spec.Kind, "absorbing", unknown, names, index); cerr != nil {
			return types.OverlayLayer{}, nil, cerr
		}
	}
	var initial []float64
	if params.Initial {
		initial = make([]float64, k)
		if params.InitialMargin {
			for i := range initial {
				initial[i] = rowTotals[i] / grand
			}
		} else {
			var unknown []string
			var sum float64
			for n, v := range params.InitialShares {
				if i, ok := stateIndex[n]; ok {
					initial[i] = v
				} else {
					unknown = append(unknown, n)
				}
			}
			if cerr := overlayhost.MarkovStateRefusal(spec.Kind, "initial", unknown, names, index); cerr != nil {
				return types.OverlayLayer{}, nil, cerr
			}
			for _, v := range initial { // state order: a deterministic sum
				sum += v
			}
			for i := range initial {
				initial[i] /= sum
			}
		}
	}

	// P: each row over its total; a zero row absorbs.
	var warnings []types.OverlayWarning
	P := make([]float64, k*k)
	var zeroRows []int
	var zeroKeys []types.AxisKey
	for i := 0; i < k; i++ {
		if i >= hostRows { // a synthesized declared-absorbing row
			P[i*k+i] = 1
			continue
		}
		if rowTotals[i] > 0 {
			for s := 0; s < k; s++ {
				P[i*k+s] = counts[i*k+s] / rowTotals[i]
			}
			continue
		}
		P[i*k+i] = 1
		zeroRows = append(zeroRows, i)
		zeroKeys = append(zeroKeys, payload.RowKeys[i])
	}
	if synthesized > 0 {
		warnings = append(warnings, types.OverlayWarning{
			Code: string(errors.PULSE_OVERLAY_REF_ZERO),
			Message: "overlay " + string(spec.Kind) + " added a unit row for column-only states declared in params.absorbing: " +
				"no move out of them was observed, and an absorbing state keeps everyone it receives",
			Details: map[string]any{
				"kind":   string(spec.Kind),
				"axis":   "row",
				"states": append([]types.AxisKey(nil), stateKeys[hostRows:]...),
				"reason": "absorbing_row_synthesized",
			},
		})
	}
	if len(zeroRows) > 0 {
		warnings = append(warnings, types.OverlayWarning{
			Code: string(errors.PULSE_OVERLAY_REF_ZERO),
			Message: "overlay " + string(spec.Kind) + " treated states with a zero row as absorbing: " +
				"no move out of them was observed, so the chain keeps them where they are",
			Details: map[string]any{
				"kind":   string(spec.Kind),
				"axis":   "row",
				"rows":   zeroRows,
				"states": zeroKeys,
				"reason": "zero_row_absorbing",
			},
		})
	}

	stateMatrix := func(m []float64, label string) *types.OverlayPayload {
		cells := make([][]types.MatrixCell, k)
		for i := range cells {
			cells[i] = make([]types.MatrixCell, k)
			for s := range cells[i] {
				cells[i][s] = types.MatrixCell{Value: m[i*k+s], Present: true}
			}
		}
		colKeys := make([]types.AxisKey, k)
		for s, j := range colOf {
			colKeys[s] = payload.ColumnKeys[j]
		}
		return &types.OverlayPayload{Shape: types.OverlayShapeMatrix, Matrix: &types.MatrixPayload{
			RowHeader:        payload.RowHeader,
			ColumnHeader:     payload.ColumnHeader,
			RowKeys:          append([]types.AxisKey(nil), stateKeys...),
			ColumnKeys:       colKeys,
			Cells:            cells,
			CellLabel:        label,
			NormalizeApplied: types.CrosstabNormalizeNone,
		}}
	}
	stateSeries := func(values []float64, include func(int) bool) *types.OverlayPayload {
		var es []types.SeriesEntry
		for i, v := range values {
			if include != nil && !include(i) {
				continue
			}
			v := v
			es = append(es, types.SeriesEntry{Key: stateKeys[i], Summary: types.OverlaySummary{Statistic: &v}})
		}
		if es == nil {
			es = []types.SeriesEntry{}
		}
		return &types.OverlayPayload{Shape: types.OverlayShapeSeries, Series: &types.SeriesPayload{Entries: es}}
	}

	parts := map[string]*types.OverlayPayload{}
	for _, n := range params.Steps {
		Pn := markovPower(P, k, n)
		suffix := strconv.Itoa(n)
		parts[MarkovPartStepPrefix+suffix] = stateMatrix(Pn, MarkovPartStepPrefix+suffix)
		if initial != nil {
			parts[MarkovPartDistributionPrefix+suffix] = stateSeries(markovRowTimes(initial, Pn, k), nil)
		}
	}

	// Ergodicity: communicating classes and the period.
	classes := markovClasses(P, k)
	summary := map[string]float64{
		"states":  float64(k),
		"classes": float64(len(classes)),
	}
	if synthesized > 0 {
		summary["synthesized_rows"] = float64(synthesized)
	}
	switch {
	case len(classes) > 1:
		summary["irreducible"] = 0
		named := make([][]types.AxisKey, len(classes))
		for c, members := range classes {
			for _, i := range members {
				named[c] = append(named[c], stateKeys[i])
			}
		}
		warnings = append(warnings, types.OverlayWarning{
			Code: string(errors.PULSE_OVERLAY_MARKOV_NOT_ERGODIC),
			Message: "overlay " + string(spec.Kind) + " chain is reducible (" + strconv.Itoa(len(classes)) +
				" communicating classes): it has no single long-run distribution, so steady_state is omitted",
			Details: map[string]any{"kind": string(spec.Kind), "reason": "reducible", "classes": named},
		})
	default:
		summary["irreducible"] = 1
		period := markovPeriod(P, k)
		summary["period"] = float64(period)
		if period > 1 {
			warnings = append(warnings, types.OverlayWarning{
				Code: string(errors.PULSE_OVERLAY_MARKOV_NOT_ERGODIC),
				Message: "overlay " + string(spec.Kind) + " chain is periodic (period " + strconv.Itoa(period) +
					"): it cycles instead of settling, so steady_state is omitted",
				Details: map[string]any{"kind": string(spec.Kind), "reason": "periodic", "period": period},
			})
			break
		}
		pi, iterations, converged, err := markovSteadyState(P, k)
		if err != nil {
			return types.OverlayLayer{}, warnings, err
		}
		summary["steady_state_iterations"] = float64(iterations)
		summary["steady_state_converged"] = 0
		if converged {
			summary["steady_state_converged"] = 1
		}
		parts[MarkovPartSteadyState] = stateSeries(pi, nil)
	}

	if params.Absorbing != nil {
		times, uncertain, err := markovAbsorptionTimes(P, k, absorbing)
		var lerr *errors.CodedError
		switch {
		case err != nil && stderrors.As(err, &lerr):
			details := map[string]any{}
			for key, v := range lerr.Details {
				details[key] = v
			}
			details["kind"] = string(spec.Kind)
			details["part"] = MarkovPartAbsorptionTime
			warnings = append(warnings, types.OverlayWarning{
				Code: string(lerr.Code),
				Message: "overlay " + string(spec.Kind) + " could not solve the fundamental matrix (I - Q) for the absorption times " +
					"(numerically singular); absorption_time is omitted",
				Details: details,
			})
		case err != nil:
			return types.OverlayLayer{}, warnings, err
		default:
			if len(uncertain) > 0 {
				keys := make([]types.AxisKey, len(uncertain))
				for u, i := range uncertain {
					keys[u] = stateKeys[i]
				}
				warnings = append(warnings, types.OverlayWarning{
					Code: string(errors.PULSE_OVERLAY_MARKOV_NOT_ERGODIC),
					Message: "overlay " + string(spec.Kind) + " has states that may never reach an absorbing state " +
						"(they can enter a closed group outside params.absorbing): their absorption_time is null",
					Details: map[string]any{"kind": string(spec.Kind), "reason": "absorption_not_certain", "states": keys},
				})
			}
			parts[MarkovPartAbsorptionTime] = stateSeries(times, func(i int) bool { return !absorbing[i] })
		}
	}

	primary := stateMatrix(P, "transition")
	layer := types.OverlayLayer{
		Name:  overlayLayerName(spec),
		Kind:  spec.Kind,
		Scope: spec.Scope,
		Ref:   spec.Ref,
		Payload: types.OverlayPayload{
			Shape:  types.OverlayShapeMatrix,
			Matrix: primary.Matrix,
			Parts:  parts,
		},
		Summary: &types.OverlaySummary{Parameters: summary},
	}
	return layer, warnings, nil
}

// markovAllDeclared reports whether every column-only state name is
// declared in params.absorbing and names no row state (a duplicate
// column key is never synthesized).
func markovAllDeclared(columnOnly, absorbing []string, rows map[string]int) bool {
	declared := make(map[string]bool, len(absorbing))
	for _, n := range absorbing {
		declared[n] = true
	}
	seen := make(map[string]bool, len(columnOnly))
	for _, n := range columnOnly {
		if _, isRow := rows[n]; isRow || seen[n] || !declared[n] {
			return false
		}
		seen[n] = true
	}
	return true
}

// markovMul returns the k × k product a·b, FMA-free: each term is a
// rounded product summed in ascending inner index.
func markovMul(a, b []float64, k int) []float64 {
	out := make([]float64, k*k)
	for i := 0; i < k; i++ {
		for j := 0; j < k; j++ {
			var sum float64
			for l := 0; l < k; l++ {
				sum += float64(a[i*k+l] * b[l*k+j])
			}
			out[i*k+j] = sum
		}
	}
	return out
}

// markovPower returns Pⁿ (n ≥ 1) by binary powering: the squares of P
// are multiplied into the result from the lowest set bit of n up.
func markovPower(P []float64, k, n int) []float64 {
	var result []float64
	base := append([]float64(nil), P...)
	for n > 0 {
		if n&1 == 1 {
			if result == nil {
				result = append([]float64(nil), base...)
			} else {
				result = markovMul(result, base, k)
			}
		}
		n >>= 1
		if n > 0 {
			base = markovMul(base, base, k)
		}
	}
	return result
}

// markovRowTimes returns the row vector v·M (M k × k), FMA-free.
func markovRowTimes(v, M []float64, k int) []float64 {
	out := make([]float64, k)
	for j := 0; j < k; j++ {
		var sum float64
		for i := 0; i < k; i++ {
			sum += float64(v[i] * M[i*k+j])
		}
		out[j] = sum
	}
	return out
}

// markovClasses returns the communicating classes of the chain (the
// strongly connected components of the graph i → j iff P_ij > 0,
// Tarjan), each listed in state order, the classes ordered by their
// first state.
func markovClasses(P []float64, k int) [][]int {
	idx := make([]int, k)
	low := make([]int, k)
	onStack := make([]bool, k)
	for i := range idx {
		idx[i] = -1
	}
	comp := make([]int, k)
	var stack []int
	next, ncomp := 0, 0
	var visit func(v int)
	visit = func(v int) {
		idx[v], low[v] = next, next
		next++
		stack = append(stack, v)
		onStack[v] = true
		for w := 0; w < k; w++ {
			if !(P[v*k+w] > 0) {
				continue
			}
			if idx[w] < 0 {
				visit(w)
				low[v] = min(low[v], low[w])
			} else if onStack[w] {
				low[v] = min(low[v], idx[w])
			}
		}
		if low[v] == idx[v] {
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp[w] = ncomp
				if w == v {
					break
				}
			}
			ncomp++
		}
	}
	for v := 0; v < k; v++ {
		if idx[v] < 0 {
			visit(v)
		}
	}
	// Order the classes by their lowest state.
	order := map[int]int{}
	var classes [][]int
	for v := 0; v < k; v++ {
		c, ok := order[comp[v]]
		if !ok {
			c = len(classes)
			order[comp[v]] = c
			classes = append(classes, nil)
		}
		classes[c] = append(classes[c], v)
	}
	return classes
}

// markovPeriod returns the period of an IRREDUCIBLE chain: the gcd over
// every edge u → v of level(u) + 1 − level(v), levels from a BFS out of
// state 0 (the gcd of all cycle lengths).
func markovPeriod(P []float64, k int) int {
	level := make([]int, k)
	for i := range level {
		level[i] = -1
	}
	level[0] = 0
	queue := []int{0}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for v := 0; v < k; v++ {
			if P[u*k+v] > 0 && level[v] < 0 {
				level[v] = level[u] + 1
				queue = append(queue, v)
			}
		}
	}
	g := 0
	for u := 0; u < k; u++ {
		for v := 0; v < k; v++ {
			if P[u*k+v] > 0 {
				d := level[u] + 1 - level[v]
				if d < 0 {
					d = -d
				}
				g = markovGCD(g, d)
			}
		}
	}
	return g
}

func markovGCD(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// markovSteadyState returns the stationary distribution of an
// irreducible aperiodic chain by power iteration from the uniform
// distribution (stop when no share moves by markovSteadyTolerance;
// converged = true), or — when markovSteadyMaxIterations pass first —
// by the exact solve of π (I − P) = 0 with Σ π = 1 (the last balance
// equation replaced by the normalization; converged = false).
func markovSteadyState(P []float64, k int) (pi []float64, iterations int, converged bool, err error) {
	pi = make([]float64, k)
	for i := range pi {
		pi[i] = 1 / float64(k)
	}
	for iterations < markovSteadyMaxIterations {
		next := markovRowTimes(pi, P, k)
		iterations++
		var delta float64
		for i := range next {
			delta = math.Max(delta, math.Abs(next[i]-pi[i]))
		}
		pi = next
		if delta < markovSteadyTolerance {
			return pi, iterations, true, nil
		}
	}
	// (I − P)ᵀ π = 0 over the first k − 1 balance equations, then Σ π = 1.
	a := make([]float64, k*k)
	for j := 0; j < k-1; j++ {
		for i := 0; i < k; i++ {
			v := -P[i*k+j]
			if i == j {
				v = 1 - P[i*k+j]
			}
			a[j*k+i] = v
		}
	}
	for i := 0; i < k; i++ {
		a[(k-1)*k+i] = 1
	}
	b := make([]float64, k)
	b[k-1] = 1
	m, err := linalg.NewMatrix(k, k, a)
	if err != nil {
		return nil, iterations, false, err
	}
	x, err := linalg.Solve(m, linalg.NewVec(b))
	if err != nil {
		return nil, iterations, false, err
	}
	for i := range pi {
		pi[i] = x.At(i)
	}
	return pi, iterations, false, nil
}

// markovAbsorptionTimes returns, per state, the expected number of
// steps to first reach a state in absorbing (NaN for an absorbing state
// itself — callers drop those — and for a state that may never get
// there), plus the non-absorbing states whose absorption is not
// certain. A state is certain when it cannot reach a state from which
// the absorbing set is unreachable; the certain states' times solve
// (I − Q) t = 1 over Q = P restricted to them, through linalg.FactorLU.
// A coded linalg error (a numerically singular I − Q) is returned as
// is.
func markovAbsorptionTimes(P []float64, k int, absorbing []bool) ([]float64, []int, error) {
	// reaches[v] = v can reach the absorbing set.
	reaches := markovReachBack(P, k, absorbing)
	// stuck = non-absorbing states that can never reach it; doomed =
	// states that can reach a stuck one (absorption not certain).
	stuck := make([]bool, k)
	for v := 0; v < k; v++ {
		stuck[v] = !absorbing[v] && !reaches[v]
	}
	doomed := markovReachBack(P, k, stuck)
	times := make([]float64, k)
	var certain, uncertain []int
	for v := 0; v < k; v++ {
		switch {
		case absorbing[v]:
			times[v] = math.NaN()
		case doomed[v]:
			times[v] = math.NaN()
			uncertain = append(uncertain, v)
		default:
			certain = append(certain, v)
		}
	}
	if len(certain) == 0 {
		return times, uncertain, nil
	}
	m := len(certain)
	a := make([]float64, m*m)
	for r, i := range certain {
		for c, j := range certain {
			v := -P[i*k+j]
			if r == c {
				v = 1 - P[i*k+j]
			}
			a[r*m+c] = v
		}
	}
	ones := make([]float64, m)
	for i := range ones {
		ones[i] = 1
	}
	mat, err := linalg.NewMatrix(m, m, a)
	if err != nil {
		return nil, nil, err
	}
	lu, err := linalg.FactorLU(mat)
	if err != nil {
		return nil, nil, err
	}
	t, err := lu.Solve(linalg.NewVec(ones))
	if err != nil {
		return nil, nil, err
	}
	for r, i := range certain {
		times[i] = t.At(r)
	}
	return times, uncertain, nil
}

// markovReachBack returns, per state, whether it can reach (in zero or
// more steps along P_ij > 0) a state marked in target.
func markovReachBack(P []float64, k int, target []bool) []bool {
	out := append([]bool(nil), target...)
	var queue []int
	for v, t := range target {
		if t {
			queue = append(queue, v)
		}
	}
	for len(queue) > 0 {
		w := queue[0]
		queue = queue[1:]
		for v := 0; v < k; v++ {
			if !out[v] && P[v*k+w] > 0 {
				out[v] = true
				queue = append(queue, v)
			}
		}
	}
	return out
}
