package processing

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/expr-lang/expr"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// --- ZScore Attribute ---

type zscoreAttribute struct{}

func newZScoreAttribute(_ *types.Attribute, _ *encoding.Schema) (AttributeComputer, error) {
	return &zscoreAttribute{}, nil
}

func (a *zscoreAttribute) Compute(records []*Record, field string) ([]float64, error) {
	if len(records) == 0 {
		return []float64{}, nil
	}
	vals := collectValues(records, field)
	m := mean(vals)
	sd := populationStdDev(vals)

	result := make([]float64, len(records))
	for i, r := range records {
		v, ok := r.NumericValue(field)
		if !ok || sd == 0 {
			result[i] = 0
			continue
		}
		result[i] = (v - m) / sd
	}
	return result, nil
}

// --- TScore Attribute ---

type tscoreAttribute struct{}

func newTScoreAttribute(_ *types.Attribute, _ *encoding.Schema) (AttributeComputer, error) {
	return &tscoreAttribute{}, nil
}

func (a *tscoreAttribute) Compute(records []*Record, field string) ([]float64, error) {
	if len(records) == 0 {
		return []float64{}, nil
	}
	vals := collectValues(records, field)
	m := mean(vals)
	sd := populationStdDev(vals)

	result := make([]float64, len(records))
	for i, r := range records {
		v, ok := r.NumericValue(field)
		if !ok || sd == 0 {
			result[i] = 50 // T-score of mean when sd=0
			continue
		}
		z := (v - m) / sd
		result[i] = z*10 + 50
	}
	return result, nil
}

// --- Normalized Attribute ---

type normalizedAttribute struct{}

func newNormalizedAttribute(_ *types.Attribute, _ *encoding.Schema) (AttributeComputer, error) {
	return &normalizedAttribute{}, nil
}

func (a *normalizedAttribute) Compute(records []*Record, field string) ([]float64, error) {
	if len(records) == 0 {
		return []float64{}, nil
	}
	vals := collectValues(records, field)
	if len(vals) == 0 {
		return make([]float64, len(records)), nil
	}

	minV, maxV := vals[0], vals[0]
	for _, v := range vals[1:] {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}

	rng := maxV - minV
	result := make([]float64, len(records))
	for i, r := range records {
		v, ok := r.NumericValue(field)
		if !ok || rng == 0 {
			result[i] = 0
			continue
		}
		result[i] = (v - minV) / rng
	}
	return result, nil
}

// --- Formula Attribute ---

type formulaAttribute struct {
	expression string
	schema     *encoding.Schema
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

func (a *formulaAttribute) Compute(records []*Record, field string) ([]float64, error) {
	if len(records) == 0 {
		return []float64{}, nil
	}

	result := make([]float64, len(records))
	for i, r := range records {
		env := r.AllValues()
		program, err := expr.Compile(a.expression, expr.Env(env))
		if err != nil {
			return nil, errors.WrapCodedError(err, errors.PROCESSING_RUNTIME,
				fmt.Sprintf("compiling formula expression: %s", a.expression))
		}

		output, err := expr.Run(program, env)
		if err != nil {
			return nil, errors.WrapCodedError(err, errors.PROCESSING_RUNTIME,
				fmt.Sprintf("evaluating formula expression: %s", a.expression))
		}

		switch v := output.(type) {
		case float64:
			result[i] = v
		case float32:
			result[i] = float64(v)
		case int:
			result[i] = float64(v)
		case int64:
			result[i] = float64(v)
		case bool:
			if v {
				result[i] = 1.0
			} else {
				result[i] = 0.0
			}
		default:
			return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
				fmt.Sprintf("formula expression returned unsupported type %T", output))
		}
	}
	return result, nil
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

// --- Rank Attribute ---

type rankAttribute struct{}

func newRankAttribute(_ *types.Attribute, _ *encoding.Schema) (AttributeComputer, error) {
	return &rankAttribute{}, nil
}

func (a *rankAttribute) Compute(records []*Record, field string) ([]float64, error) {
	if len(records) == 0 {
		return []float64{}, nil
	}

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

	// Sort by value (ascending)
	sort.Slice(indexed, func(i, j int) bool {
		return indexed[i].val < indexed[j].val
	})

	// Assign ranks (1-based, ascending)
	result := make([]float64, len(records))
	for rank, iv := range indexed {
		result[iv.idx] = float64(rank + 1)
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
		v, ok := r.NumericValue(field)
		if !ok {
			result[i] = 0
			continue
		}

		t := time.Unix(int64(v)*86400, 0).UTC()
		year, month, day := t.Date()

		switch a.part {
		case "year":
			result[i] = float64(year)
		case "month":
			result[i] = float64(month)
		case "day":
			result[i] = float64(day)
		case "year_month":
			result[i] = float64(year*100 + int(month))
		case "year_month_day":
			result[i] = float64(year*10000 + int(month)*100 + day)
		case "month_day":
			result[i] = float64(int(month)*100 + day)
		}
	}
	return result, nil
}
