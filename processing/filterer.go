package processing

import (
	"fmt"
	"strconv"

	"github.com/expr-lang/expr"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// --- Include Filter ---

type includeFilterer struct{}

func newIncludeFilterer() FiltererBuilder {
	return &includeFilterer{}
}

func (f *includeFilterer) Build(filter *types.Filterer, schema *encoding.Schema) (FilterFunc, error) {
	valueSet := make(map[float64]bool, len(filter.Values))
	for _, v := range filter.Values {
		fv, err := strconv.ParseFloat(v, 64)
		if err != nil {
			// For categorical fields, try dictionary lookup
			field := schema.Field(filter.Field)
			if field != nil && field.Type.IsCategorical() && field.Dictionary != nil {
				id, ok := field.Dictionary.IDFor(v)
				if ok {
					valueSet[float64(id)] = true
					continue
				}
			}
			return nil, errors.WrapCodedError(err, errors.PROCESSING_CONFIG,
				fmt.Sprintf("parsing include value %q", v))
		}
		valueSet[fv] = true
	}

	return func(record *Record) (bool, error) {
		v, ok := record.NumericValue(filter.Field)
		if !ok {
			return false, nil
		}
		return valueSet[v], nil
	}, nil
}

// --- Exclude Filter ---

type excludeFilterer struct{}

func newExcludeFilterer() FiltererBuilder {
	return &excludeFilterer{}
}

func (f *excludeFilterer) Build(filter *types.Filterer, schema *encoding.Schema) (FilterFunc, error) {
	valueSet := make(map[float64]bool, len(filter.Values))
	for _, v := range filter.Values {
		fv, err := strconv.ParseFloat(v, 64)
		if err != nil {
			field := schema.Field(filter.Field)
			if field != nil && field.Type.IsCategorical() && field.Dictionary != nil {
				id, ok := field.Dictionary.IDFor(v)
				if ok {
					valueSet[float64(id)] = true
					continue
				}
			}
			return nil, errors.WrapCodedError(err, errors.PROCESSING_CONFIG,
				fmt.Sprintf("parsing exclude value %q", v))
		}
		valueSet[fv] = true
	}

	return func(record *Record) (bool, error) {
		v, ok := record.NumericValue(filter.Field)
		if !ok {
			return true, nil // null values pass exclude filter
		}
		return !valueSet[v], nil
	}, nil
}

// --- Range Filter ---

type rangeFilterer struct{}

func newRangeFilterer() FiltererBuilder {
	return &rangeFilterer{}
}

func (f *rangeFilterer) Build(filter *types.Filterer, schema *encoding.Schema) (FilterFunc, error) {
	if len(filter.Values) != 2 {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"range filter requires exactly 2 values (min, max)")
	}

	minVal, err := strconv.ParseFloat(filter.Values[0], 64)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.PROCESSING_CONFIG,
			fmt.Sprintf("parsing range min value %q", filter.Values[0]))
	}
	maxVal, err := strconv.ParseFloat(filter.Values[1], 64)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.PROCESSING_CONFIG,
			fmt.Sprintf("parsing range max value %q", filter.Values[1]))
	}

	return func(record *Record) (bool, error) {
		v, ok := record.NumericValue(filter.Field)
		if !ok {
			return false, nil
		}
		return v >= minVal && v <= maxVal, nil
	}, nil
}

// --- Expression Filter ---

type expressionFilterer struct {
	exts *ExtensionRegistry
}

func newExpressionFilterer() FiltererBuilder {
	return &expressionFilterer{}
}

// SetExtensions implements ExtensionAware so the Processor can inject
// the live registry after construction. Custom expr functions and
// lookup tables become visible to the filter expression.
func (f *expressionFilterer) SetExtensions(r *ExtensionRegistry) {
	f.exts = r
}

func (f *expressionFilterer) Build(filter *types.Filterer, schema *encoding.Schema) (FilterFunc, error) {
	if filter.Expression == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"expression filter requires a non-empty expression")
	}

	exts := f.exts
	return func(record *Record) (bool, error) {
		env := record.AllValues()
		opts := []expr.Option{expr.Env(env)}
		opts = append(opts, exts.ExprOptions()...)
		program, err := expr.Compile(filter.Expression, opts...)
		if err != nil {
			return false, errors.WrapCodedError(err, errors.PROCESSING_RUNTIME,
				fmt.Sprintf("compiling filter expression: %s", filter.Expression))
		}
		output, err := expr.Run(program, env)
		if err != nil {
			return false, errors.WrapCodedError(err, errors.PROCESSING_RUNTIME,
				fmt.Sprintf("evaluating filter expression: %s", filter.Expression))
		}
		b, ok := output.(bool)
		if !ok {
			return false, errors.NewCodedError(errors.PROCESSING_RUNTIME,
				fmt.Sprintf("filter expression must return bool, got %T", output))
		}
		return b, nil
	}, nil
}
