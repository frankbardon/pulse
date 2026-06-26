package processing

import (
	"fmt"
	"math"
	"strconv"

	"github.com/expr-lang/expr"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

type includeFilterer struct{}

func newIncludeFilterer() FiltererBuilder {
	return &includeFilterer{}
}

func (f *includeFilterer) Build(filter *types.Filterer, schema *encoding.Schema) (FilterFunc, error) {
	field := schema.Field(filter.Field)
	isCategorical := field != nil && field.Type.IsCategorical() && field.Dictionary != nil

	valueSet := make(map[float64]bool, len(filter.Values))
	for _, v := range filter.Values {
		if isCategorical {
			id, ok := field.Dictionary.IDFor(v)
			if !ok {
				return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
					fmt.Sprintf("include value %q not found in dictionary for field %q", v, filter.Field))
			}
			valueSet[float64(id)] = true
			continue
		}
		fv, err := strconv.ParseFloat(v, 64)
		if err != nil {
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

type excludeFilterer struct{}

func newExcludeFilterer() FiltererBuilder {
	return &excludeFilterer{}
}

func (f *excludeFilterer) Build(filter *types.Filterer, schema *encoding.Schema) (FilterFunc, error) {
	field := schema.Field(filter.Field)
	isCategorical := field != nil && field.Type.IsCategorical() && field.Dictionary != nil

	valueSet := make(map[float64]bool, len(filter.Values))
	for _, v := range filter.Values {
		if isCategorical {
			id, ok := field.Dictionary.IDFor(v)
			if !ok {
				return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
					fmt.Sprintf("exclude value %q not found in dictionary for field %q", v, filter.Field))
			}
			valueSet[float64(id)] = true
			continue
		}
		fv, err := strconv.ParseFloat(v, 64)
		if err != nil {
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

// nullFilterer keeps or drops records based on the null state of one
// field. Mode is carried in Filterer.Values[0] as "is_null" /
// "is_not_null" — no struct change required on Filterer for this small
// per-mode parameter.
type nullFilterer struct{}

func newNullFilterer() FiltererBuilder {
	return &nullFilterer{}
}

func (f *nullFilterer) Build(filter *types.Filterer, _ *encoding.Schema) (FilterFunc, error) {
	if filter.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"FILTER_NULL requires a Field")
	}
	if len(filter.Values) != 1 {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"FILTER_NULL requires exactly one Values entry: \"is_null\" or \"is_not_null\"")
	}
	var keepNull bool
	switch filter.Values[0] {
	case "is_null":
		keepNull = true
	case "is_not_null":
		keepNull = false
	default:
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			fmt.Sprintf("FILTER_NULL Values[0]=%q; must be \"is_null\" or \"is_not_null\"", filter.Values[0]))
	}

	field := filter.Field
	return func(record *Record) (bool, error) {
		_, present := record.NumericValue(field)
		if keepNull {
			return !present, nil
		}
		return present, nil
	}, nil
}

// boolFilterer keeps records whose Field is logically true (want=true) or
// logically false (want=false). Default (strict) mode requires Field to
// be a packed_bool column; the predicate matches the raw 0/1 value. Opt
// into JavaScript-style coercion with Values=["truthy"] — any field type
// is then accepted and `Boolean(value)` semantics apply (0, NaN, "",
// null → falsy; everything else → truthy).
type boolFilterer struct {
	want bool
}

func newTrueFilterer() FiltererBuilder  { return &boolFilterer{want: true} }
func newFalseFilterer() FiltererBuilder { return &boolFilterer{want: false} }

func (f *boolFilterer) typeName() types.FiltererType {
	if f.want {
		return types.FILTER_TRUE
	}
	return types.FILTER_FALSE
}

func (f *boolFilterer) Build(filter *types.Filterer, schema *encoding.Schema) (FilterFunc, error) {
	tn := f.typeName()
	if filter.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			fmt.Sprintf("%s requires a Field", tn))
	}

	truthyMode := false
	switch len(filter.Values) {
	case 0:
		// strict mode
	case 1:
		switch filter.Values[0] {
		case "truthy":
			truthyMode = true
		case "strict":
			truthyMode = false
		default:
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("%s Values[0]=%q; must be \"truthy\" or \"strict\" (or omit Values for strict)", tn, filter.Values[0]))
		}
	default:
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			fmt.Sprintf("%s accepts at most one Values entry (\"truthy\" or \"strict\"); got %d", tn, len(filter.Values)))
	}

	field := schema.Field(filter.Field)
	if field == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			fmt.Sprintf("%s field %q not found in schema", tn, filter.Field))
	}
	if !truthyMode && field.Type != encoding.FieldTypePackedBool {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			fmt.Sprintf("%s strict mode requires packed_bool field; %q is %s. Pass Values=[\"truthy\"] to opt into JavaScript-style coercion.",
				tn, filter.Field, field.Type.String()))
	}

	fieldName := filter.Field
	want := f.want

	if !truthyMode {
		return func(record *Record) (bool, error) {
			v, ok := record.NumericValue(fieldName)
			if !ok {
				return false, nil // null → drop in both directions
			}
			isTrue := v != 0
			return isTrue == want, nil
		}, nil
	}

	return func(record *Record) (bool, error) {
		all := record.AllValues()
		v, present := all[fieldName]
		if !present {
			// null / missing — JS coerces to false
			return !want, nil
		}
		return jsTruthy(v) == want, nil
	}, nil
}

// jsTruthy returns whether v would coerce to true under JavaScript's
// Boolean(value) rules. Pulse record values are produced by AllValues,
// so the concrete types in play are nil, bool, string (categorical
// resolved), float64 (numeric/packed_bool/date), and encoding.Decimal128
// (wide map). Anything else falls back to "non-nil → truthy" to mirror
// JS object coercion.
func jsTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0 && !math.IsNaN(x)
	case float32:
		return x != 0 && !math.IsNaN(float64(x))
	case encoding.Decimal128:
		return x.Sign() != 0
	default:
		return true
	}
}
