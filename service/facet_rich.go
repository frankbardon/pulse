package service

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
)

// defaultHistogramBins is applied when IncludeHistogram is true and
// HistogramBins is zero.
const defaultHistogramBins = 20

// maxHistogramBins caps caller-supplied bin counts. A value above this
// cap is rejected with SERVICE_VALIDATION; the cap exists so a runaway
// request cannot allocate an unbounded counter slice per numeric field.
const maxHistogramBins = 256

// FacetSchema is the rich multi-field facet implementation. The
// pulse.FacetSchema facade is a thin wrapper around this method.
//
// Algorithm: one streaming pass over the cohort applies the base
// filterers, then for each requested field updates the appropriate
// accumulator (discrete dictionary count, online numeric stats, online
// histogram bin). Numeric percentiles force a buffered second-stage
// sort over the per-field non-null value slices. Additive contribution
// counts run a parallel discrete accumulator with the additive field's
// own filter clauses stripped from the base filter.
func (s *Service) FacetSchema(ctx context.Context, req *types.FacetRequest) (*types.FacetResult, error) {
	if req == nil {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION, "facet schema requires a request")
	}
	if req.Cohort == nil {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION, "facet schema requires a cohort")
	}
	if len(req.Fields) == 0 {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION, "facet schema requires a non-empty fields list")
	}

	path := resolveCohortPath(req.Cohort)
	cohort, err := s.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	schema := cohort.Schema()

	if err := validateFacetSchemaRequest(req, schema); err != nil {
		return nil, err
	}
	// Inject configured default label bindings (schema-filtered) for the
	// fields being faceted so registered tables render in facet output
	// without per-call bindings.
	if len(s.autoLabels) > 0 {
		faceted := make(map[string]bool, len(req.Fields))
		for _, f := range req.Fields {
			faceted[f] = true
		}
		s.applyAutoLabels(&req.Labels, schema, nil, faceted)
	}
	if err := s.validateFacetLabels(req, schema); err != nil {
		return nil, err
	}

	bins, err := resolveHistogramBins(req)
	if err != nil {
		return nil, err
	}

	// Build accumulators per requested field.
	fields, err := buildFieldAccumulators(req.Fields, schema, req, bins)
	if err != nil {
		return nil, err
	}

	// Build additive accumulators with the field's own clauses stripped.
	additive, additiveFilters, err := buildAdditiveAccumulators(req, schema)
	if err != nil {
		return nil, err
	}

	baseFilterFns, err := processing.BuildFilters(req.Filterers, schema, s.extensions)
	if err != nil {
		return nil, err
	}

	// Route through newScanIter so the same accumulator loop runs over
	// the union of shards for archive cohorts (sharding design contract
	// §5.4). Welford / count / null tallies / histogram accumulators are
	// online-mergeable — feeding every shard's rows through one
	// accumulator yields the same result as a concatenated single-file
	// equivalent. NumericPercentiles force buffered materialization
	// across the union via the per-field values slice in
	// numericAccumulator; that is mathematically required for global
	// percentile semantics.
	iter := s.newScanIter(cohort, path)
	defer iter.Close()

	var totalRows, filteredRows int64
	for iter.Next() {
		totalRows++
		r := iter.Record()

		basePass := allPass(baseFilterFns, r)
		if basePass {
			filteredRows++
			for _, fa := range fields {
				if err := fa.update(r); err != nil {
					return nil, err
				}
			}
		}

		for _, ae := range additive {
			scopePass := allPass(additiveFilters[ae.field], r)
			if !scopePass {
				continue
			}
			if err := ae.acc.update(r); err != nil {
				return nil, err
			}
		}
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}

	result := &types.FacetResult{
		Fields:          make(map[string]*types.FacetField, len(fields)),
		TotalRecords:    totalRows,
		FilteredRecords: filteredRows,
	}

	for _, fa := range fields {
		ff, warning, err := fa.finalize(req)
		if err != nil {
			return nil, err
		}
		result.Fields[fa.name] = ff
		if warning != "" {
			result.Warnings = append(result.Warnings, warning)
		}
	}

	if len(additive) > 0 {
		result.Additive = make(map[string]*types.FacetField, len(additive))
		for _, ae := range additive {
			ff, warning, err := ae.acc.finalize(req)
			if err != nil {
				return nil, err
			}
			result.Additive[ae.field] = ff
			if warning != "" {
				result.Warnings = append(result.Warnings, warning)
			}
		}
	}

	if err := s.applyFacetLabels(req, result); err != nil {
		return nil, err
	}

	return result, nil
}

// validateFacetSchemaRequest checks structural rules + schema-references
// before the streaming pass. Failure surfaces as SERVICE_VALIDATION.
func validateFacetSchemaRequest(req *types.FacetRequest, schema *encoding.Schema) error {
	if req.DiscreteTopK < 0 {
		return errors.NewCodedError(errors.SERVICE_VALIDATION, "discrete_top_k must be >= 0")
	}
	for _, p := range req.NumericPercentiles {
		if !(p > 0 && p < 1) || math.IsNaN(p) {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"numeric_percentiles entries must lie in the open interval (0, 1)",
				map[string]any{"value": p})
		}
	}
	if req.IncludeHistogram {
		if req.HistogramRange[0] >= req.HistogramRange[1] {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"histogram_range requires min < max",
				map[string]any{"min": req.HistogramRange[0], "max": req.HistogramRange[1]})
		}
		if req.HistogramBins < 0 {
			return errors.NewCodedError(errors.SERVICE_VALIDATION, "histogram_bins must be >= 0")
		}
		if req.HistogramBins > maxHistogramBins {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				fmt.Sprintf("histogram_bins exceeds cap of %d", maxHistogramBins),
				map[string]any{"requested": req.HistogramBins})
		}
	}

	for _, name := range req.Fields {
		if schema.Field(name) == nil {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				fmt.Sprintf("facet field %q not found in schema", name),
				map[string]any{"field": name})
		}
	}
	for _, name := range req.AdditiveFields {
		if schema.Field(name) == nil {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				fmt.Sprintf("additive field %q not found in schema", name),
				map[string]any{"field": name})
		}
		// Additive scope-stripping cannot semantically remove a clause
		// hidden inside a FILTER_EXPRESSION body. Reject up front rather
		// than silently leaking the additive field's predicate into the
		// scope filter and producing misleading counts.
		for _, f := range req.Filterers {
			if f == nil {
				continue
			}
			if f.Type == types.FILTER_EXPRESSION && filterExpressionMentions(f, name) {
				return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
					fmt.Sprintf("additive field %q referenced inside FILTER_EXPRESSION; express the predicate as discrete filterers instead", name),
					map[string]any{"field": name, "expression": f.Expression})
			}
		}
	}
	return nil
}

func resolveHistogramBins(req *types.FacetRequest) (int, error) {
	if !req.IncludeHistogram {
		return 0, nil
	}
	bins := req.HistogramBins
	if bins == 0 {
		bins = defaultHistogramBins
	}
	return bins, nil
}

// fieldAcc is the per-field accumulator the streaming loop folds rows
// into. Implementations are kindAccumulator instances chosen by
// schema-type at construction.
type fieldAcc struct {
	name string
	kind kindAccumulator
}

func (f *fieldAcc) update(r *processing.Record) error {
	return f.kind.update(r, f.name)
}

func (f *fieldAcc) finalize(req *types.FacetRequest) (*types.FacetField, string, error) {
	return f.kind.finalize(req)
}

// kindAccumulator dispatches discrete vs numeric per-field accumulation.
type kindAccumulator interface {
	update(r *processing.Record, field string) error
	finalize(req *types.FacetRequest) (*types.FacetField, string, error)
}

type additiveEntry struct {
	field string
	acc   *fieldAcc
}

// buildFieldAccumulators chooses discrete vs numeric per-field based on
// the schema type and instantiates the appropriate accumulator.
func buildFieldAccumulators(fields []string, schema *encoding.Schema, req *types.FacetRequest, bins int) ([]*fieldAcc, error) {
	out := make([]*fieldAcc, 0, len(fields))
	for _, name := range fields {
		f := schema.Field(name)
		acc, err := newKindAccumulator(f, req, bins)
		if err != nil {
			return nil, err
		}
		out = append(out, &fieldAcc{name: name, kind: acc})
	}
	return out, nil
}

// buildAdditiveAccumulators constructs one fieldAcc per AdditiveFields
// entry plus the per-additive-field scope-filter set.
func buildAdditiveAccumulators(req *types.FacetRequest, schema *encoding.Schema) ([]*additiveEntry, map[string][]processing.FilterFunc, error) {
	if len(req.AdditiveFields) == 0 {
		return nil, nil, nil
	}
	entries := make([]*additiveEntry, 0, len(req.AdditiveFields))
	scopes := make(map[string][]processing.FilterFunc, len(req.AdditiveFields))
	for _, name := range req.AdditiveFields {
		f := schema.Field(name)
		acc, err := newKindAccumulator(f, req, 0)
		if err != nil {
			return nil, nil, err
		}
		entries = append(entries, &additiveEntry{field: name, acc: &fieldAcc{name: name, kind: acc}})
		scopeFilters := stripFieldFromFilterers(req.Filterers, name)
		fns, err := processing.BuildFilters(scopeFilters, schema, nil)
		if err != nil {
			return nil, nil, err
		}
		scopes[name] = fns
	}
	return entries, scopes, nil
}

// newKindAccumulator constructs the right accumulator for the field's
// schema type. Numeric types (u*, f*, decimal*, date) are summarised
// numerically; categorical / boolean types are summarised discretely.
func newKindAccumulator(f *encoding.Field, req *types.FacetRequest, bins int) (kindAccumulator, error) {
	switch {
	case f.Type.IsCategorical():
		return newCategoricalAccumulator(f), nil
	case f.Type == encoding.FieldTypePackedBool:
		return newBoolAccumulator(f), nil
	case isFacetNumericType(f.Type):
		wantPercentiles := len(req.NumericPercentiles) > 0
		return newNumericAccumulator(f, wantPercentiles, req.IncludeHistogram, bins, req.HistogramRange), nil
	default:
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("facet does not support field type %s", f.Type.String()),
			map[string]any{"field": f.Name, "type": f.Type.String()})
	}
}

func isFacetNumericType(t encoding.FieldType) bool {
	switch t {
	case encoding.FieldTypeU4,
		encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32, encoding.FieldTypeU64,
		encoding.FieldTypeF32, encoding.FieldTypeF64,
		encoding.FieldTypeDate,
		encoding.FieldTypeDecimal128:
		return true
	}
	return false
}

// allPass returns true when every filter accepts the record. An empty
// slice trivially passes. Errors propagate up so the caller can surface
// them as PROCESSING_RUNTIME.
func allPass(fns []processing.FilterFunc, r *processing.Record) bool {
	for _, fn := range fns {
		ok, err := fn(r)
		if err != nil || !ok {
			return false
		}
	}
	return true
}

// --- Categorical accumulator (dictionary fast path) ---

type categoricalAccumulator struct {
	field    *encoding.Field
	counts   map[uint32]int64
	nulls    int64
	distinct int64
}

func newCategoricalAccumulator(f *encoding.Field) *categoricalAccumulator {
	return &categoricalAccumulator{field: f, counts: make(map[uint32]int64)}
}

func (a *categoricalAccumulator) update(r *processing.Record, field string) error {
	v, ok := r.NumericValue(field)
	if !ok {
		a.nulls++
		return nil
	}
	id := uint32(v)
	if _, seen := a.counts[id]; !seen {
		a.distinct++
	}
	a.counts[id]++
	return nil
}

func (a *categoricalAccumulator) finalize(req *types.FacetRequest) (*types.FacetField, string, error) {
	values := make([]types.FacetValueCount, 0, len(a.counts))
	for id, c := range a.counts {
		val := strconv.FormatUint(uint64(id), 10)
		if a.field.Dictionary != nil {
			if resolved := a.field.Dictionary.Resolve(id); resolved != "" {
				val = resolved
			}
		}
		values = append(values, types.FacetValueCount{Value: val, Count: c})
	}
	values, truncatedAt, warning := sortAndTruncateDiscrete(values, a.field.Name, a.distinct, req.DiscreteTopK)
	return &types.FacetField{
		Kind:        "discrete",
		TypeName:    a.field.Type.String(),
		Description: a.field.Description,
		NullCount:   a.nulls,
		Discrete: &types.FacetDiscrete{
			Values:        values,
			DistinctCount: a.distinct,
			TruncatedAt:   truncatedAt,
		},
	}, warning, nil
}

// --- Boolean accumulator (true/false counts) ---

type boolAccumulator struct {
	field    *encoding.Field
	t, fcnt  int64
	nulls    int64
	distinct int64
}

func newBoolAccumulator(f *encoding.Field) *boolAccumulator { return &boolAccumulator{field: f} }

func (a *boolAccumulator) update(r *processing.Record, field string) error {
	v, ok := r.NumericValue(field)
	if !ok {
		a.nulls++
		return nil
	}
	if v != 0 {
		if a.t == 0 {
			a.distinct++
		}
		a.t++
	} else {
		if a.fcnt == 0 {
			a.distinct++
		}
		a.fcnt++
	}
	return nil
}

func (a *boolAccumulator) finalize(req *types.FacetRequest) (*types.FacetField, string, error) {
	var values []types.FacetValueCount
	if a.t > 0 {
		values = append(values, types.FacetValueCount{Value: "true", Count: a.t})
	}
	if a.fcnt > 0 {
		values = append(values, types.FacetValueCount{Value: "false", Count: a.fcnt})
	}
	values, truncatedAt, warning := sortAndTruncateDiscrete(values, a.field.Name, a.distinct, req.DiscreteTopK)
	return &types.FacetField{
		Kind:        "discrete",
		TypeName:    a.field.Type.String(),
		Description: a.field.Description,
		NullCount:   a.nulls,
		Discrete: &types.FacetDiscrete{
			Values:        values,
			DistinctCount: a.distinct,
			TruncatedAt:   truncatedAt,
		},
	}, warning, nil
}

// --- Numeric accumulator (Welford online + optional histogram + buffered percentiles) ---

type numericAccumulator struct {
	field          *encoding.Field
	wantPercentile bool
	includeHist    bool
	bins           int
	histRange      [2]float64

	n     int64
	min   float64
	max   float64
	mean  float64
	m2    float64
	sum   float64
	nulls int64

	histCounts []int64

	// values is populated only when wantPercentile is true.
	values []float64
}

func newNumericAccumulator(f *encoding.Field, wantPercentiles, includeHist bool, bins int, hr [2]float64) *numericAccumulator {
	a := &numericAccumulator{
		field:          f,
		wantPercentile: wantPercentiles,
		includeHist:    includeHist,
		bins:           bins,
		histRange:      hr,
		min:            math.Inf(1),
		max:            math.Inf(-1),
	}
	if includeHist {
		a.histCounts = make([]int64, bins)
	}
	return a
}

func (a *numericAccumulator) update(r *processing.Record, field string) error {
	v, ok := r.NumericValue(field)
	if !ok {
		// decimal128 may live in wide; convert via Float64 when present.
		if wv, ok := r.WideValue(field); ok {
			if d, dok := wv.(encoding.Decimal128); dok {
				v = d.Float64(a.field.Scale)
			} else {
				a.nulls++
				return nil
			}
		} else {
			a.nulls++
			return nil
		}
	}
	a.n++
	a.sum += v
	if v < a.min {
		a.min = v
	}
	if v > a.max {
		a.max = v
	}
	// Welford recurrence for running mean + M2.
	delta := v - a.mean
	a.mean += delta / float64(a.n)
	delta2 := v - a.mean
	a.m2 += delta * delta2

	if a.includeHist {
		idx := histogramBin(v, a.histRange[0], a.histRange[1], a.bins)
		if idx >= 0 {
			a.histCounts[idx]++
		}
	}
	if a.wantPercentile {
		a.values = append(a.values, v)
	}
	return nil
}

func (a *numericAccumulator) finalize(req *types.FacetRequest) (*types.FacetField, string, error) {
	num := &types.FacetNumeric{
		Count: a.n,
		Sum:   a.sum,
	}
	if a.n == 0 {
		num.Min = 0
		num.Max = 0
	} else {
		num.Min = a.min
		num.Max = a.max
		num.Mean = a.mean
		if a.n > 1 {
			num.StdDev = math.Sqrt(a.m2 / float64(a.n-1))
		}
	}

	if a.includeHist {
		num.Histogram = &types.FacetHistogram{
			Min:  a.histRange[0],
			Max:  a.histRange[1],
			Bins: a.histCounts,
		}
	}

	if a.wantPercentile && a.n > 0 {
		sort.Float64s(a.values)
		num.Percentiles = make(map[string]float64, len(req.NumericPercentiles))
		for _, p := range req.NumericPercentiles {
			num.Percentiles[percentileLabel(p)] = linearPercentile(a.values, p)
		}
	}

	return &types.FacetField{
		Kind:        "numeric",
		TypeName:    a.field.Type.String(),
		Description: a.field.Description,
		NullCount:   a.nulls,
		Numeric:     num,
	}, "", nil
}

// histogramBin returns the bin index for value v over [lo, hi] with
// nBins equal-width buckets. Values outside [lo, hi] return -1 so the
// histogram does not double-count out-of-range data; values exactly
// equal to hi land in the last bin.
func histogramBin(v, lo, hi float64, nBins int) int {
	if v < lo || v > hi {
		return -1
	}
	if v == hi {
		return nBins - 1
	}
	step := (hi - lo) / float64(nBins)
	idx := int((v - lo) / step)
	if idx >= nBins {
		idx = nBins - 1
	}
	return idx
}

// linearPercentile returns the linearly-interpolated percentile p over
// a pre-sorted ascending slice. Mirrors the AGG_PERCENTILE
// implementation so the two surfaces stay numerically aligned.
func linearPercentile(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return sorted[0]
	}
	rank := p * float64(n-1)
	lo := int(math.Floor(rank))
	hi := int(math.Ceil(rank))
	if lo == hi {
		return sorted[lo]
	}
	frac := rank - float64(lo)
	return sorted[lo] + frac*(sorted[hi]-sorted[lo])
}

// percentileLabel formats a percentile value as "p<integer>" when the
// fraction maps cleanly to an integer percent (the common case
// p=0.5 → "p50"), and as a fixed-precision form otherwise so requests
// with exotic fractions remain key-stable.
func percentileLabel(p float64) string {
	pct := p * 100
	if math.Abs(pct-math.Round(pct)) < 1e-9 {
		return fmt.Sprintf("p%d", int(math.Round(pct)))
	}
	return fmt.Sprintf("p%g", pct)
}

// sortAndTruncateDiscrete sorts the value/count slice descending by
// count (ties resolved ascending by value), then truncates to
// DiscreteTopK when set. Returns the trimmed slice, the count of
// values dropped, and a warning string when truncation occurred.
func sortAndTruncateDiscrete(values []types.FacetValueCount, fieldName string, distinct int64, topK int) ([]types.FacetValueCount, int, string) {
	sort.SliceStable(values, func(i, j int) bool {
		if values[i].Count != values[j].Count {
			return values[i].Count > values[j].Count
		}
		return values[i].Value < values[j].Value
	})
	if topK > 0 && len(values) > topK {
		dropped := len(values) - topK
		values = values[:topK]
		return values, dropped, fmt.Sprintf("field %q truncated to top %d of %d distinct values", fieldName, topK, distinct)
	}
	return values, 0, ""
}

// filterExpressionMentions returns true when the FILTER_EXPRESSION
// body references field as an identifier. We search for the field
// surrounded by non-identifier characters (or string boundaries) so a
// substring match doesn't trigger on an unrelated identifier that
// happens to contain the additive field's name.
func filterExpressionMentions(f *types.Filterer, field string) bool {
	if f == nil || f.Expression == "" || field == "" {
		return false
	}
	expr := f.Expression
	for i := 0; i+len(field) <= len(expr); i++ {
		if expr[i:i+len(field)] != field {
			continue
		}
		left := i == 0 || !isIdentChar(expr[i-1])
		right := i+len(field) == len(expr) || !isIdentChar(expr[i+len(field)])
		if left && right {
			return true
		}
	}
	return false
}

func isIdentChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

// stripFieldFromFilterers returns a copy of filterers with every
// clause that targets field removed. FILTER_EXPRESSION clauses are
// passed through untouched — the validator rejects expressions that
// reference an additive field up front so we never reach this point
// with one to strip.
func stripFieldFromFilterers(in []*types.Filterer, field string) []*types.Filterer {
	if len(in) == 0 {
		return nil
	}
	out := make([]*types.Filterer, 0, len(in))
	for _, f := range in {
		if f == nil {
			continue
		}
		if f.Field == field {
			continue
		}
		out = append(out, f)
	}
	return out
}
