package processing

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/expr-lang/expr"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// --- ZScore Attribute ---

// zscoreAttribute computes (x - mean) / stddev per row using
// population mean/stddev derived in pass 1 via Welford-Pébaÿ. Implements
// TwoPassAttribute so the streaming path can avoid buffering the
// record set.
type zscoreAttribute struct {
	// Welford state (pass 1).
	count uint64
	mean  float64
	m2    float64
	// Locked at Finalize.
	finalMean   float64
	finalStdDev float64
	finalized   bool
}

func newZScoreAttribute(_ *types.Attribute, _ *encoding.Schema) (AttributeComputer, error) {
	return &zscoreAttribute{}, nil
}

func (a *zscoreAttribute) PrePass(r *Record, field string) error {
	v, ok := r.NumericValue(field)
	if !ok {
		return nil
	}
	a.count++
	delta := v - a.mean
	a.mean += delta / float64(a.count)
	a.m2 += delta * (v - a.mean)
	return nil
}

func (a *zscoreAttribute) Finalize() error {
	if a.count == 0 {
		a.finalMean = 0
		a.finalStdDev = 0
	} else {
		a.finalMean = a.mean
		a.finalStdDev = math.Sqrt(a.m2 / float64(a.count))
	}
	a.finalized = true
	return nil
}

func (a *zscoreAttribute) Row(r *Record, field string) (float64, error) {
	v, ok := r.NumericValue(field)
	if !ok || a.finalStdDev == 0 {
		return 0, nil
	}
	return (v - a.finalMean) / a.finalStdDev, nil
}

func (a *zscoreAttribute) Compute(records []*Record, field string) ([]float64, error) {
	if len(records) == 0 {
		return []float64{}, nil
	}
	for _, r := range records {
		if err := a.PrePass(r, field); err != nil {
			return nil, err
		}
	}
	if err := a.Finalize(); err != nil {
		return nil, err
	}
	result := make([]float64, len(records))
	for i, r := range records {
		val, err := a.Row(r, field)
		if err != nil {
			return nil, err
		}
		result[i] = val
	}
	return result, nil
}

// --- TScore Attribute ---

// tscoreAttribute emits z*10+50 per row using population mean/stddev
// from a Welford pass 1. Implements TwoPassAttribute.
type tscoreAttribute struct {
	count       uint64
	mean        float64
	m2          float64
	finalMean   float64
	finalStdDev float64
}

func newTScoreAttribute(_ *types.Attribute, _ *encoding.Schema) (AttributeComputer, error) {
	return &tscoreAttribute{}, nil
}

func (a *tscoreAttribute) PrePass(r *Record, field string) error {
	v, ok := r.NumericValue(field)
	if !ok {
		return nil
	}
	a.count++
	delta := v - a.mean
	a.mean += delta / float64(a.count)
	a.m2 += delta * (v - a.mean)
	return nil
}

func (a *tscoreAttribute) Finalize() error {
	if a.count == 0 {
		a.finalMean = 0
		a.finalStdDev = 0
	} else {
		a.finalMean = a.mean
		a.finalStdDev = math.Sqrt(a.m2 / float64(a.count))
	}
	return nil
}

func (a *tscoreAttribute) Row(r *Record, field string) (float64, error) {
	v, ok := r.NumericValue(field)
	if !ok || a.finalStdDev == 0 {
		return 50, nil // T-score of mean when sd=0 or null input
	}
	return ((v-a.finalMean)/a.finalStdDev)*10 + 50, nil
}

func (a *tscoreAttribute) Compute(records []*Record, field string) ([]float64, error) {
	if len(records) == 0 {
		return []float64{}, nil
	}
	for _, r := range records {
		if err := a.PrePass(r, field); err != nil {
			return nil, err
		}
	}
	if err := a.Finalize(); err != nil {
		return nil, err
	}
	result := make([]float64, len(records))
	for i, r := range records {
		val, err := a.Row(r, field)
		if err != nil {
			return nil, err
		}
		result[i] = val
	}
	return result, nil
}

// --- Normalized Attribute ---

// normalizedAttribute emits (x-min)/(max-min) per row using population
// min/max from a Welford-shaped pass 1. Implements TwoPassAttribute.
type normalizedAttribute struct {
	seen     bool
	min, max float64
	rng      float64
}

func newNormalizedAttribute(_ *types.Attribute, _ *encoding.Schema) (AttributeComputer, error) {
	return &normalizedAttribute{}, nil
}

func (a *normalizedAttribute) PrePass(r *Record, field string) error {
	v, ok := r.NumericValue(field)
	if !ok {
		return nil
	}
	if !a.seen {
		a.min, a.max = v, v
		a.seen = true
		return nil
	}
	if v < a.min {
		a.min = v
	}
	if v > a.max {
		a.max = v
	}
	return nil
}

func (a *normalizedAttribute) Finalize() error {
	a.rng = a.max - a.min
	return nil
}

func (a *normalizedAttribute) Row(r *Record, field string) (float64, error) {
	v, ok := r.NumericValue(field)
	if !ok || a.rng == 0 {
		return 0, nil
	}
	return (v - a.min) / a.rng, nil
}

func (a *normalizedAttribute) Compute(records []*Record, field string) ([]float64, error) {
	if len(records) == 0 {
		return []float64{}, nil
	}
	for _, r := range records {
		if err := a.PrePass(r, field); err != nil {
			return nil, err
		}
	}
	if err := a.Finalize(); err != nil {
		return nil, err
	}
	result := make([]float64, len(records))
	for i, r := range records {
		val, err := a.Row(r, field)
		if err != nil {
			return nil, err
		}
		result[i] = val
	}
	return result, nil
}

// --- Formula Attribute ---

type formulaAttribute struct {
	expression string
	schema     *encoding.Schema
	exts       *ExtensionRegistry
}

func newFormulaAttribute(attr *types.Attribute, schema *encoding.Schema) (AttributeComputer, error) {
	if attr.Expression == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG, "formula attribute requires an expression")
	}
	return &formulaAttribute{
		expression: attr.Expression,
		schema:     schema,
	}, nil
}

// SetExtensions implements ExtensionAware so the Processor can inject
// the live registry after construction. Custom expr functions and
// lookup tables registered by the embedder are then visible to the
// formula expression at compile / evaluation time.
func (a *formulaAttribute) SetExtensions(r *ExtensionRegistry) {
	a.exts = r
}

func (a *formulaAttribute) Compute(records []*Record, field string) ([]float64, error) {
	if len(records) == 0 {
		return []float64{}, nil
	}

	result := make([]float64, len(records))
	for i, r := range records {
		v, err := a.Row(r, field)
		if err != nil {
			return nil, err
		}
		result[i] = v
	}
	return result, nil
}

// Row evaluates the formula expression against a single record. Each
// invocation re-compiles the expression because the env shape may change
// when records have different sparse-null populations; predict guarantees
// the expression typechecks against the schema, so compile failures here
// are user-visible PROCESSING_RUNTIME errors.
func (a *formulaAttribute) Row(r *Record, _ string) (float64, error) {
	env := r.AllValues()
	opts := []expr.Option{expr.Env(env)}
	opts = append(opts, a.exts.ExprOptions()...)
	program, err := expr.Compile(a.expression, opts...)
	if err != nil {
		return 0, errors.WrapCodedError(err, errors.PROCESSING_RUNTIME,
			fmt.Sprintf("compiling formula expression: %s", a.expression))
	}
	output, err := expr.Run(program, env)
	if err != nil {
		return 0, errors.WrapCodedError(err, errors.PROCESSING_RUNTIME,
			fmt.Sprintf("evaluating formula expression: %s", a.expression))
	}
	switch v := output.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case bool:
		if v {
			return 1.0, nil
		}
		return 0.0, nil
	default:
		return 0, errors.NewCodedError(errors.PROCESSING_RUNTIME,
			fmt.Sprintf("formula expression returned unsupported type %T", output))
	}
}

// --- Percentile Attribute ---

type percentileAttribute struct{}

func newPercentileAttribute(_ *types.Attribute, _ *encoding.Schema) (AttributeComputer, error) {
	return &percentileAttribute{}, nil
}

func (a *percentileAttribute) Compute(records []*Record, field string) ([]float64, error) {
	if len(records) == 0 {
		return []float64{}, nil
	}

	// Collect values with their original indices
	type indexedVal struct {
		idx int
		val float64
	}
	var indexed []indexedVal
	for i, r := range records {
		v, ok := r.NumericValue(field)
		if ok {
			indexed = append(indexed, indexedVal{idx: i, val: v})
		}
	}

	if len(indexed) == 0 {
		return make([]float64, len(records)), nil
	}

	// Sort by value
	sort.Slice(indexed, func(i, j int) bool {
		return indexed[i].val < indexed[j].val
	})

	// Compute percentile rank for each position
	result := make([]float64, len(records))
	n := float64(len(indexed))
	for rank, iv := range indexed {
		// Percentile = (rank + 1) / n * 100
		result[iv.idx] = float64(rank+1) / n * 100.0
	}
	return result, nil
}

// --- DatePart Attribute ---

// datePartParams holds the configuration for ATTR_DATE_PART.
type datePartParams struct {
	Part string `json:"part"`
}

var validDateParts = map[string]bool{
	"year":           true,
	"month":          true,
	"day":            true,
	"year_month":     true,
	"year_month_day": true,
	"month_day":      true,
}

type datePartAttribute struct {
	part string
}

func newDatePartAttribute(attr *types.Attribute, schema *encoding.Schema) (AttributeComputer, error) {
	if len(attr.Params) == 0 {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG, "date_part attribute requires params with a \"part\" field")
	}

	var params datePartParams
	if err := json.Unmarshal(attr.Params, &params); err != nil {
		return nil, errors.WrapCodedError(err, errors.PROCESSING_CONFIG, "parsing date_part params")
	}

	if params.Part == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG, "date_part attribute requires a \"part\" field in params")
	}

	if !validDateParts[params.Part] {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			fmt.Sprintf("invalid date part %q: must be one of year, month, day, year_month, year_month_day, month_day", params.Part))
	}

	f := schema.Field(attr.Field)
	if f == nil || f.Type != encoding.FieldTypeDate {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			fmt.Sprintf("date_part attribute requires a date field, got %q", attr.Field))
	}

	return &datePartAttribute{part: params.Part}, nil
}

func (a *datePartAttribute) Compute(records []*Record, field string) ([]float64, error) {
	if len(records) == 0 {
		return []float64{}, nil
	}

	result := make([]float64, len(records))
	for i, r := range records {
		v, err := a.Row(r, field)
		if err != nil {
			return nil, err
		}
		result[i] = v
	}
	return result, nil
}

// Row extracts the configured date part from a single record. Null date
// values produce 0 (matches buffered Compute semantics).
func (a *datePartAttribute) Row(r *Record, field string) (float64, error) {
	v, ok := r.NumericValue(field)
	if !ok {
		return 0, nil
	}
	t := time.Unix(int64(v)*86400, 0).UTC()
	year, month, day := t.Date()
	switch a.part {
	case "year":
		return float64(year), nil
	case "month":
		return float64(month), nil
	case "day":
		return float64(day), nil
	case "year_month":
		return float64(year*100 + int(month)), nil
	case "year_month_day":
		return float64(year*10000 + int(month)*100 + day), nil
	case "month_day":
		return float64(int(month)*100 + day), nil
	}
	return 0, nil
}
