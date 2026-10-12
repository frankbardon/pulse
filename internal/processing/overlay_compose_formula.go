package processing

import (
	"fmt"
	"math"

	"github.com/expr-lang/expr"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/formulaident"
	"github.com/frankbardon/pulse/types"
)

// Compose-host OVERLAY_FORMULA (U46 E4-S15). Before, a FORMULA on
// ComposedRequest.Overlays passed predict-compose and then fell through
// to the chassis stub: an EMPTY layer. It now evaluates the per-shape
// namespace types.OverlayKindFormula documents for a Compose host.
//
//   - Host: the FIRST target slot (every single-layer Compose kind's
//     rule); the layer's coordinates are its cells / rows.
//   - Shape: the reference slot's authored shape (the gates already
//     refused a target of another shape). MATRIX binds cell, margins
//     and SDs off the target crosstab plus ref_cell; SERIES binds value,
//     total (sum of the present target values) and prior (lag-1 present
//     row, in Data order) plus ref_value; SCALAR binds value (the
//     target's one aggregate row) plus ref.
//   - slot.<label>.cell (MATRIX) / slot.<label>.value: every slot the
//     spec names (reference and targets), aligned to the host
//     coordinate by key — the key-set gate already proved the sets
//     equal.
//   - A reference or slot value with no figure at the host coordinate
//     (an absent cell, a row with no numeric value) binds NaN, so the
//     result is undefined (JSON null), never a silent 0.
//
// The identifier gate (formulaident.ComposeRefusal) runs first, on the
// same inputs predict-compose hands it, so an out-of-namespace name is
// refused with the same code and Details on both sides.

// applyComposeFormula is the composeOverlayHandlers entry: the
// handler-signature form with no registry and the slot shapes probed
// off the responses (the bare ApplyComposeOverlays path). The
// dispatcher calls applyComposeFormulaWith instead, with each slot's
// authored shape and the instance's ExprFunctions.
func applyComposeFormula(spec *types.ComposeOverlaySpec, reference *types.Response, targets []*types.Response, refIdx int, targetIdxs []int) (types.OverlayLayer, []types.OverlayWarning, error) {
	return applyComposeFormulaWith(spec, -1, responseSlotShapes(reference, targets), reference, targets, nil)
}

// applyComposeFormulaWith evaluates the Compose FORMULA spec at index
// with each slot judged by shapes (reference, then targets parallel to
// targets) and exts' ExprFunctions / LookupTables in reach.
func applyComposeFormulaWith(spec *types.ComposeOverlaySpec, index int, shapes composeSlotShapes, reference *types.Response, targets []*types.Response, exts *ExtensionRegistry) (types.OverlayLayer, []types.OverlayWarning, error) {
	slots := composeFormulaSlots(spec, shapes)
	if cerr := formulaident.ComposeRefusal(spec.Kind, spec.Params, index, shapes.ref, slots, exts.overlayFormulaFuncs()); cerr != nil {
		return types.OverlayLayer{}, nil, cerr
	}
	formula, _ := formulaident.ComposeFormula(spec.Params)
	target, _ := composeFirstTarget(targets, nil)
	if reference == nil || target == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_TARGET_UNKNOWN,
			"compose overlay spec must declare at least one target slot label",
			map[string]any{"index": index, "kind": string(spec.Kind), "which": "targets"})
	}
	// The slot responses parallel to slots: the reference, then each
	// target (a duplicate label keeps its first response).
	slotResps := make([]*types.Response, 0, len(slots))
	seen := map[string]bool{}
	for i, label := range append([]string{spec.Reference}, spec.Targets...) {
		if seen[label] {
			continue
		}
		seen[label] = true
		if i == 0 {
			slotResps = append(slotResps, reference)
		} else if i-1 < len(targets) {
			slotResps = append(slotResps, targets[i-1])
		}
	}
	ospec := &types.OverlaySpec{Kind: spec.Kind, Name: spec.Name, Scope: spec.Scope}
	var (
		layer types.OverlayLayer
		err   error
	)
	switch shapes.ref {
	case types.OverlayShapeMatrix:
		layer, err = composeFormulaMatrix(spec, ospec, formula, reference, target, slots, slotResps, exts)
	case types.OverlayShapeSeries:
		layer, err = composeFormulaSeries(ospec, formula, reference, target, slots, slotResps, exts)
	default:
		layer, err = composeFormulaScalar(ospec, formula, reference, target, slots, slotResps, exts)
	}
	if err != nil {
		return types.OverlayLayer{}, nil, err
	}
	layer.Name = composeOverlayLayerName(spec)
	layer.Ref = types.OverlayRef{}
	return layer, nil, nil
}

// composeFormulaSlots lists the slots spec names, reference first, each
// with its judged shape; a repeated label is listed once.
func composeFormulaSlots(spec *types.ComposeOverlaySpec, shapes composeSlotShapes) []formulaident.Slot {
	out := []formulaident.Slot{{Label: spec.Reference, Shape: shapes.ref}}
	seen := map[string]bool{spec.Reference: true}
	for i, label := range spec.Targets {
		if seen[label] {
			continue
		}
		seen[label] = true
		shape := shapes.ref
		if i < len(shapes.targets) {
			shape = shapes.targets[i]
		}
		out = append(out, formulaident.Slot{Label: label, Shape: shape})
	}
	return out
}

// overlayFormulaFuncs is the function set an OVERLAY_FORMULA may call on
// this instance (formulaident.AllowedFuncs over the registry's
// ExprFunctions and LookupTables). Nil-receiver-safe.
func (r *ExtensionRegistry) overlayFormulaFuncs() map[string]struct{} {
	if r == nil {
		return formulaident.AllowedFuncs(nil, false)
	}
	names := make([]string, 0, len(r.ExprFunctions))
	for _, fn := range r.ExprFunctions {
		names = append(names, fn.Name)
	}
	return formulaident.AllowedFuncs(names, len(r.LookupTables) > 0)
}

// composeFormulaMatrix is the MATRIX arm: the target crosstab is the
// host; ref_cell and each matrix slot are aligned to its (row, column)
// keys.
func composeFormulaMatrix(spec *types.ComposeOverlaySpec, ospec *types.OverlaySpec, formula string, reference, target *types.Response, slots []formulaident.Slot, slotResps []*types.Response, exts *ExtensionRegistry) (types.OverlayLayer, error) {
	targetMx, refMx := readMatrix(target), readMatrix(reference)
	if targetMx == nil || refMx == nil {
		return types.OverlayLayer{}, errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_SLOT_NOT_CROSSTAB,
			"overlay "+string(spec.Kind)+" over a MATRIX-shape Compose host needs a crosstab matrix on the reference and target",
			map[string]any{"kind": string(spec.Kind), "reference": spec.Reference, "target_label": composeFirstTargetLabel(spec)})
	}
	var comps *types.CrosstabComponents
	if target.Components != nil {
		comps = target.Components.Crosstab
	}
	host := newCrosstabHostViewWithComponents(targetMx, comps)
	needRowSD, needColSD, needGrandSD := detectFormulaSDIdentifiers(formula)
	rowSDs, colSDs, grandSD := computeMatrixWelfordSDs(host, needRowSD, needColSD, needGrandSD)
	ctx := &FormulaContext{
		Shape:      types.OverlayShapeMatrix,
		MatrixHost: host,
		RowSDs:     rowSDs,
		ColumnSDs:  colSDs,
		GrandSD:    grandSD,
		RefMatrix:  alignMatrixCells(refMx, targetMx),
	}
	for k, s := range slots {
		if k < len(slotResps) {
			ctx.Slots = append(ctx.Slots, formulaSlotBinding{Label: s.Label, MatrixCells: alignMatrixCells(readMatrix(slotResps[k]), targetMx)})
		}
	}
	return runFormulaMatrix(ospec, formula, ctx, exts)
}

// alignMatrixCells lays src's scalar cells over host's (row, column)
// key grid, NaN where src has no figure at the coordinate.
func alignMatrixCells(src, host *types.MatrixPayload) [][]float64 {
	lookup := buildMatrixCellLookup(src)
	out := make([][]float64, len(host.RowKeys))
	for i := range host.RowKeys {
		row := make([]float64, len(host.ColumnKeys))
		rk := axisKeyToString(host.RowKeys[i])
		for j := range host.ColumnKeys {
			v, ok := lookup[matrixCellLookupKey{row: rk, col: axisKeyToString(host.ColumnKeys[j])}]
			if !ok {
				v = math.NaN()
			}
			row[j] = v
		}
		out[i] = row
	}
	return out
}

// composeFormulaSeries is the SERIES arm: the target's Data rows (in
// order) are the host; ref_value and each slot are aligned by row key.
func composeFormulaSeries(ospec *types.OverlaySpec, formula string, reference, target *types.Response, slots []formulaident.Slot, slotResps []*types.Response, exts *ExtensionRegistry) (types.OverlayLayer, error) {
	keys := make([]string, len(target.Data))
	axis := make([]types.AxisKey, len(target.Data))
	values := make([]float64, len(target.Data))
	present := make([]bool, len(target.Data))
	var total float64
	for i, row := range target.Data {
		k, _, v, ok := encodeSeriesRow(row)
		keys[i], axis[i], values[i], present[i] = k, types.AxisKey{k}, v, ok
		if ok {
			total += v
		}
	}
	host := NewSeriesHostView(axis, func(i int) (float64, bool) {
		if i < 0 || i >= len(values) {
			return 0, false
		}
		return values[i], present[i]
	})
	ctx := &FormulaContext{
		Shape:       types.OverlayShapeSeries,
		SeriesHost:  host,
		SeriesTotal: total,
		RefSeries:   alignSeriesValues(reference, keys),
	}
	for k, s := range slots {
		if k < len(slotResps) {
			ctx.Slots = append(ctx.Slots, formulaSlotBinding{Label: s.Label, SeriesValues: alignSeriesValues(slotResps[k], keys)})
		}
	}
	program, err := compileFormulaProgramWithExtensions(ospec, formula, buildFormulaPrototypeEnvSeries(ctx), exts)
	if err != nil {
		return types.OverlayLayer{}, err
	}
	env := buildFormulaPrototypeEnvSeries(ctx)
	entries := make([]types.SeriesEntry, 0, len(keys))
	var acc formulaSummary
	for i := range keys {
		if _, ok := bindFormulaEnvSeries(env, ctx, i); !ok {
			continue
		}
		stat, err := evalFormula(ospec, program, env, map[string]any{"row_index": i, "row_key": keys[i]})
		if err != nil {
			return types.OverlayLayer{}, err
		}
		acc.add(stat)
		entries = append(entries, types.SeriesEntry{Key: axis[i], Summary: types.OverlaySummary{Statistic: &stat}})
	}
	return types.OverlayLayer{
		Kind:    ospec.Kind,
		Scope:   ospec.Scope,
		Payload: types.OverlayPayload{Shape: types.OverlayShapeSeries, Series: &types.SeriesPayload{Entries: entries}},
		Summary: acc.summary(),
	}, nil
}

// alignSeriesValues reads resp's Data value per host row key, NaN where
// resp has no figure for the key.
func alignSeriesValues(resp *types.Response, keys []string) []float64 {
	var lookup map[string]float64
	if resp != nil {
		lookup, _ = buildSeriesRowLookup(resp.Data)
	}
	out := make([]float64, len(keys))
	for i, k := range keys {
		v, ok := lookup[k]
		if !ok {
			v = math.NaN()
		}
		out[i] = v
	}
	return out
}

// composeFormulaScalar is the SCALAR arm: one evaluation over the
// target's single aggregate row, ref and each slot its own row.
func composeFormulaScalar(ospec *types.OverlaySpec, formula string, reference, target *types.Response, slots []formulaident.Slot, slotResps []*types.Response, exts *ExtensionRegistry) (types.OverlayLayer, error) {
	value, ok := scalarSlotValue(target)
	refValue, _ := scalarSlotValue(reference)
	ctx := &FormulaContext{
		Shape:            types.OverlayShapeScalar,
		ScalarValue:      value,
		ScalarPresent:    ok,
		RefScalar:        refValue,
		RefScalarPresent: true,
	}
	for k, s := range slots {
		if k < len(slotResps) {
			v, _ := scalarSlotValue(slotResps[k])
			ctx.Slots = append(ctx.Slots, formulaSlotBinding{Label: s.Label, Scalar: v, ScalarPresent: true})
		}
	}
	program, err := compileFormulaProgramWithExtensions(ospec, formula, buildFormulaPrototypeEnvScalar(ctx), exts)
	if err != nil {
		return types.OverlayLayer{}, err
	}
	layer := types.OverlayLayer{Kind: ospec.Kind, Scope: ospec.Scope, Payload: types.OverlayPayload{Shape: types.OverlayShapeScalar}}
	var acc formulaSummary
	env := buildFormulaPrototypeEnvScalar(ctx)
	if _, ok := bindFormulaEnvScalar(env, ctx); ok {
		stat, err := evalFormula(ospec, program, env, nil)
		if err != nil {
			return types.OverlayLayer{}, err
		}
		acc.add(stat)
		layer.Payload.Scalar = &stat
	}
	layer.Summary = acc.summary()
	return layer, nil
}

// scalarSlotValue is a SCALAR slot's figure: its one aggregate row's
// value, NaN and false when it has none.
func scalarSlotValue(resp *types.Response) (float64, bool) {
	if resp == nil || len(resp.Data) == 0 {
		return math.NaN(), false
	}
	_, _, v, ok := encodeSeriesRow(resp.Data[0])
	if !ok {
		return math.NaN(), false
	}
	return v, true
}

// evalFormula runs program over env and coerces the result to float64,
// PULSE_OVERLAY_FORMULA_TYPE_MISMATCH (Details + at) when it fails or
// is not numeric — the request-host matrix loop's two refusals.
func evalFormula(spec *types.OverlaySpec, program *formulaProgram, env map[string]any, at map[string]any) (float64, error) {
	details := func(extra map[string]any) map[string]any {
		d := map[string]any{"kind": string(spec.Kind), "formula": program.formula}
		for k, v := range at {
			d[k] = v
		}
		for k, v := range extra {
			d[k] = v
		}
		return d
	}
	out, err := expr.Run(program.program, env)
	if err != nil {
		return 0, errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_FORMULA_TYPE_MISMATCH,
			"overlay "+string(spec.Kind)+" expression evaluation failed",
			details(map[string]any{"runtime_err": err.Error()}))
	}
	stat, ok := coerceFormulaResult(out)
	if !ok {
		return 0, errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_FORMULA_TYPE_MISMATCH,
			"overlay "+string(spec.Kind)+" expression returned a non-numeric value",
			details(map[string]any{"returned_type": fmt.Sprintf("%T", out)}))
	}
	return stat, nil
}

// formulaSummary tracks min / max / count over finite results — the
// request-host matrix loop's rule (non-finite figures are kept in the
// payload but not summarised). FORMULA has no Baseline.
type formulaSummary struct {
	minV, maxV float64
	seen       int
}

func (s *formulaSummary) add(v float64) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return
	}
	updateMinMaxCount(v, &s.seen, &s.minV, &s.maxV)
}

func (s *formulaSummary) summary() *types.OverlaySummary {
	out := &types.OverlaySummary{}
	count := s.seen
	out.Count = &count
	if s.seen > 0 {
		mn, mx := s.minV, s.maxV
		out.Min, out.Max = &mn, &mx
	}
	return out
}
