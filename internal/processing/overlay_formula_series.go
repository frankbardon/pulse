package processing

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/formulaident"
	"github.com/frankbardon/pulse/types"
)

// Request-host SERIES OVERLAY_FORMULA (U35 #432). A FORMULA on the
// Request.Overlays of a grouped Process (groups + aggregations, no
// crosstab) evaluates once per host row against the SERIES namespace:
//
//   - value    — the row's scored aggregation (params.field, else
//     Request.Aggregations[0]; internal/seriesfield);
//   - total    — the sum of the present values of the row's partition;
//   - prior    — the nearest earlier PRESENT row's value in host order,
//     NaN on the first (the Compose SERIES arm's lag-1 read);
//   - baseline — opt-in: the value at row params.baseline_position of
//     the partition (0-based), NaN when that row has no value; a
//     position past the partition is PULSE_OVERLAY_REF_UNKNOWN.
//
// With two or more groupers it runs once per partition like every
// ordered series kind (internal/seriespartition: partition_by, default
// every key but the last), so total / prior / baseline never cross a
// partition. A row with no value emits an entry with no statistic; a
// non-finite result stays in the payload (JSON null) and out of the
// summary. The identifier gate (formulaident.RequestSpecRefusal) runs
// in the dispatcher before this handler, as in predict.

// applyFormulaSeries is the seriesOverlayHandlers entry: no registry
// (Pulse.ApplySeriesOverlays, the bare path). The Process dispatcher
// calls applyFormulaSeriesWith with the instance's ExprFunctions.
func applyFormulaSeries(spec *types.OverlaySpec, host *SeriesHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
	return applyFormulaSeriesWith(spec, host, nil)
}

// applyFormulaSeriesWith evaluates spec over host with exts' functions
// and LookupTables in reach.
func applyFormulaSeriesWith(spec *types.OverlaySpec, host *SeriesHostView, exts *ExtensionRegistry) (types.OverlayLayer, []types.OverlayWarning, error) {
	if host == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
			"overlay "+string(spec.Kind)+" requires a non-nil SeriesHostView",
			map[string]any{"kind": string(spec.Kind)})
	}
	formula, err := extractFormulaParam(spec)
	if err != nil {
		return types.OverlayLayer{}, nil, err
	}
	total, _, _ := computeSeriesGrandTotal(host)
	ctx := &FormulaContext{Shape: types.OverlayShapeSeries, SeriesHost: host, SeriesTotal: total}
	if pos, set := formulaident.SpecBaselinePosition(spec); set {
		if pos >= host.GroupCount() {
			return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
				errors.PULSE_OVERLAY_REF_UNKNOWN,
				"overlay "+string(spec.Kind)+" params.baseline_position is out of range for the host series",
				map[string]any{
					"kind":                     string(spec.Kind),
					formulaident.BaselineParam: pos,
					"series_length":            host.GroupCount(),
				})
		}
		v, ok := host.ValueAt(pos)
		if !ok {
			v = math.NaN()
		}
		ctx.BaselineValue, ctx.BaselineValuePresent = v, true
	}
	program, err := compileFormulaProgramWithExtensions(spec, formula, buildFormulaPrototypeEnvSeries(ctx), exts)
	if err != nil {
		return types.OverlayLayer{}, nil, err
	}
	env := buildFormulaPrototypeEnvSeries(ctx)
	n := host.GroupCount()
	entries := make([]types.SeriesEntry, n)
	var acc formulaSummary
	for i := 0; i < n; i++ {
		if key, ok := host.GroupKey(i); ok && key != nil {
			entries[i].Key = append(types.AxisKey(nil), key...)
		}
		if _, ok := bindFormulaEnvSeries(env, ctx, i); !ok {
			continue
		}
		stat, err := evalFormula(spec, program, env, map[string]any{"row_index": i})
		if err != nil {
			return types.OverlayLayer{}, nil, err
		}
		acc.add(stat)
		entries[i].Summary.Statistic = &stat
	}
	return types.OverlayLayer{
		Name:    overlayLayerName(spec),
		Kind:    spec.Kind,
		Scope:   spec.Scope,
		Ref:     spec.Ref,
		Payload: types.OverlayPayload{Shape: types.OverlayShapeSeries, Series: &types.SeriesPayload{Entries: entries}},
		Summary: acc.summary(),
	}, nil, nil
}
