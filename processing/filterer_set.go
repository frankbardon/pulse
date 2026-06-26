package processing

import (
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Set-typed filterers — FILTER_SET_CONTAINS_ANY, FILTER_SET_CONTAINS_ALL,
// FILTER_SET_CONTAINS_NONE, FILTER_SET_EQUALS.
//
// Each accepts Filterer.Values as the label list. At build time the
// labels are resolved through the field's dictionary into a single
// query mask; per-row work is then one bitwise op. Null rows fail the
// CONTAINS_ANY / CONTAINS_ALL / EQUALS filters and pass CONTAINS_NONE,
// matching FILTER_INCLUDE / FILTER_EXCLUDE conventions for nulls.

// buildSetQueryMask resolves Values against the field's dictionary and
// returns the corresponding bitmask. Empty Values returns 0. Unknown
// labels raise PROCESSING_CONFIG so the predict path can surface them
// before the orchestrator runs.
func buildSetQueryMask(filter *types.Filterer, schema *encoding.Schema) (uint64, error) {
	if schema == nil {
		return 0, errors.NewCodedError(errors.PROCESSING_CONFIG,
			string(filter.Type)+" requires schema")
	}
	if filter.Field == "" {
		return 0, errors.NewCodedError(errors.PROCESSING_CONFIG,
			string(filter.Type)+" requires field")
	}
	f := schema.Field(filter.Field)
	if f == nil {
		return 0, errors.NewCodedError(errors.PROCESSING_CONFIG,
			string(filter.Type)+": unknown field "+filter.Field)
	}
	if !f.Type.IsSet() {
		return 0, errors.NewCodedError(errors.PROCESSING_CONFIG,
			string(filter.Type)+": field "+filter.Field+" is not a set type")
	}
	if f.Dictionary == nil {
		return 0, errors.NewCodedError(errors.PROCESSING_CONFIG,
			string(filter.Type)+": field "+filter.Field+" has no dictionary")
	}
	var mask uint64
	for _, label := range filter.Values {
		id, ok := f.Dictionary.IDFor(label)
		if !ok {
			return 0, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("%s value %q not found in dictionary for field %q",
					filter.Type, label, filter.Field))
		}
		if id >= 64 {
			return 0, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("%s value %q maps to bit %d which exceeds set width",
					filter.Type, label, id))
		}
		mask |= uint64(1) << uint(id)
	}
	return mask, nil
}

// Keep rows where the set shares at least one bit with the query mask.

type setContainsAnyFilterer struct{}

func newSetContainsAnyFilterer() FiltererBuilder { return &setContainsAnyFilterer{} }

func (f *setContainsAnyFilterer) Build(filter *types.Filterer, schema *encoding.Schema) (FilterFunc, error) {
	query, err := buildSetQueryMask(filter, schema)
	if err != nil {
		return nil, err
	}
	field := filter.Field
	if query == 0 {
		return func(record *Record) (bool, error) { return false, nil }, nil
	}
	return func(record *Record) (bool, error) {
		m, ok := record.SetValue(field)
		if !ok {
			return false, nil
		}
		return m&query != 0, nil
	}, nil
}

// Keep rows where every bit in the query mask is also set in the row.

type setContainsAllFilterer struct{}

func newSetContainsAllFilterer() FiltererBuilder { return &setContainsAllFilterer{} }

func (f *setContainsAllFilterer) Build(filter *types.Filterer, schema *encoding.Schema) (FilterFunc, error) {
	query, err := buildSetQueryMask(filter, schema)
	if err != nil {
		return nil, err
	}
	field := filter.Field
	return func(record *Record) (bool, error) {
		m, ok := record.SetValue(field)
		if !ok {
			return false, nil
		}
		return m&query == query, nil
	}, nil
}

// Keep rows where the row shares no bits with the query mask. Null
// rows pass (consistent with FILTER_EXCLUDE).

type setContainsNoneFilterer struct{}

func newSetContainsNoneFilterer() FiltererBuilder { return &setContainsNoneFilterer{} }

func (f *setContainsNoneFilterer) Build(filter *types.Filterer, schema *encoding.Schema) (FilterFunc, error) {
	query, err := buildSetQueryMask(filter, schema)
	if err != nil {
		return nil, err
	}
	field := filter.Field
	return func(record *Record) (bool, error) {
		m, ok := record.SetValue(field)
		if !ok {
			return true, nil
		}
		return m&query == 0, nil
	}, nil
}

// Keep rows whose mask exactly matches the query mask. Useful with
// GROUP_SET_VALUE for atomic-combination filtering.

type setEqualsFilterer struct{}

func newSetEqualsFilterer() FiltererBuilder { return &setEqualsFilterer{} }

func (f *setEqualsFilterer) Build(filter *types.Filterer, schema *encoding.Schema) (FilterFunc, error) {
	query, err := buildSetQueryMask(filter, schema)
	if err != nil {
		return nil, err
	}
	field := filter.Field
	return func(record *Record) (bool, error) {
		m, ok := record.SetValue(field)
		if !ok {
			return false, nil
		}
		return m == query, nil
	}, nil
}
