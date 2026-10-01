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
// encoding.SetMask query mask; per-row work is then one bitwise op
// against the row's own mask. There is ONE body per filterer covering
// every set rung from set_u8 to set_u256 — no narrow arm and no wide
// arm, because a duplicated wide arm is another place for the silent
// "wide mask reads as no selection" bug to reappear.
//
// Null rows fail the CONTAINS_ANY / CONTAINS_ALL / EQUALS filters and
// pass CONTAINS_NONE, matching FILTER_INCLUDE / FILTER_EXCLUDE
// conventions for nulls. An EMPTY mask is not a null: it is a present
// value that simply carries no members, so it takes the predicate's
// ordinary verdict (fails ANY, fails a non-empty ALL, passes NONE,
// equals only an empty query).

// buildSetQueryMask resolves Values against the field's dictionary and
// returns the corresponding encoding.SetMask. Empty Values returns the
// empty mask. Unknown labels raise PROCESSING_CONFIG so the predict path
// can surface them before the orchestrator runs.
//
// A label whose dictionary ID lies beyond the DECLARED RUNG's capacity
// is also PROCESSING_CONFIG: no row of that field can ever carry the
// bit, so the request is unsatisfiable and saying so beats answering
// "no rows matched". The guard is against the rung's own capacity, not
// against 64 — the former 64-bit ceiling rejected labels that a
// set_u128 / set_u256 column legitimately holds.
func buildSetQueryMask(filter *types.Filterer, schema *encoding.Schema) (encoding.SetMask, error) {
	var zero encoding.SetMask
	if schema == nil {
		return zero, errors.NewCodedError(errors.PROCESSING_CONFIG,
			string(filter.Type)+" requires schema")
	}
	if filter.Field == "" {
		return zero, errors.NewCodedError(errors.PROCESSING_CONFIG,
			string(filter.Type)+" requires field")
	}
	f := schema.Field(filter.Field)
	if f == nil {
		return zero, errors.NewCodedError(errors.PROCESSING_CONFIG,
			string(filter.Type)+": unknown field "+filter.Field)
	}
	if !f.Type.IsSet() {
		return zero, errors.NewCodedError(errors.PROCESSING_CONFIG,
			string(filter.Type)+": field "+filter.Field+" is not a set type")
	}
	if f.Dictionary == nil {
		return zero, errors.NewCodedError(errors.PROCESSING_CONFIG,
			string(filter.Type)+": field "+filter.Field+" has no dictionary")
	}
	capacity := int(f.Type.MaxSetEntries())
	var mask encoding.SetMask
	for _, label := range filter.Values {
		id, ok := f.Dictionary.IDFor(label)
		if !ok {
			return zero, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("%s value %q not found in dictionary for field %q",
					filter.Type, label, filter.Field))
		}
		if int(id) >= capacity {
			return zero, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				fmt.Sprintf("%s value %q maps to bit %d, beyond the %d-member capacity of %s field %q",
					filter.Type, label, id, capacity, f.Type, filter.Field),
				map[string]any{
					"field":    filter.Field,
					"type":     f.Type.String(),
					"capacity": capacity,
					"bit":      int(id),
					"value":    label,
				})
		}
		mask = mask.WithBit(int(id))
	}
	return mask, nil
}

// rejectSetFieldForNumericFilter refuses a set-typed field for a
// filterer that reads its value through Record.NumericValue. The float64
// echo of a set column is the LOW 64 BITS of the bitmask reinterpreted
// as a number, so such a filterer answers with a plausible wrong verdict
// rather than an error — and for a wide rung it cannot even see the
// members above bit 63. A nil schema or an unknown field is left alone;
// those paths have their own handling and are not this guard's business.
func rejectSetFieldForNumericFilter(filter *types.Filterer, schema *encoding.Schema) error {
	if schema == nil || filter == nil || filter.Field == "" {
		return nil
	}
	f := schema.Field(filter.Field)
	if f == nil || !f.Type.IsSet() {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
		fmt.Sprintf("%s cannot read set field %q (%s): its numeric value is the low 64 bits of the membership bitmask, not a quantity. Use FILTER_SET_CONTAINS_ANY / _ALL / _NONE / _EQUALS, or ATTR_SET_POPCOUNT for set size.",
			filter.Type, filter.Field, f.Type),
		map[string]any{"field": filter.Field, "type": f.Type.String()})
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
	// Empty query: nothing can intersect it. This is a pure shortcut —
	// the general body below already answers false for every record state
	// when query is empty (an intersection with the empty mask is empty),
	// so removing it changes no verdict, only the per-row cost. Do not
	// hang behaviour off it.
	if query.IsEmpty() {
		return func(record *Record) (bool, error) { return false, nil }, nil
	}
	return func(record *Record) (bool, error) {
		m, ok := record.SetMaskValue(field)
		if !ok {
			return false, nil
		}
		return !m.Intersect(query).IsEmpty(), nil
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
		m, ok := record.SetMaskValue(field)
		if !ok {
			return false, nil
		}
		return m.Intersect(query).Equal(query), nil
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
		m, ok := record.SetMaskValue(field)
		if !ok {
			return true, nil
		}
		return m.Intersect(query).IsEmpty(), nil
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
		m, ok := record.SetMaskValue(field)
		if !ok {
			return false, nil
		}
		return m.Equal(query), nil
	}, nil
}
