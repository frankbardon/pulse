// Package valuematch is the one rule that resolves a request-supplied
// value string to the float64 key a record's numeric value carries —
// the rule FILTER_INCLUDE, FILTER_EXCLUDE, AGG_FREQUENCY and
// ATTR_CODE_IN share. It is a leaf (public encoding / errors / types
// only) so the no-execute predict layer applies the same rule as the
// runtime constructors, with the same code, message and details.
package valuematch

import (
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Key resolves one value string. A categorical field (with a
// dictionary) resolves the string as a dictionary LABEL to its ID;
// found is false when no entry carries it, and each caller decides what
// that means (the filters refuse, AGG_FREQUENCY counts zero). Every
// other field parses the string as a number: a date in epoch days, a
// datetime in epoch seconds, packed_bool as 1 / 0, decimal128 against
// its float echo. A parse failure is returned raw for the caller to
// wrap.
func Key(field *encoding.Field, v string) (key float64, found bool, err error) {
	if field != nil && field.Type.IsCategorical() && field.Dictionary != nil {
		id, ok := field.Dictionary.IDFor(v)
		return float64(id), ok, nil
	}
	fv, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, false, err
	}
	return fv, true, nil
}

// FilterKeys resolves a FILTER_INCLUDE / FILTER_EXCLUDE values list
// against field (nil: a column the schema does not carry, parsed as a
// number) and returns the key set. The first value that fails is
// refused with PROCESSING_CONFIG: an unparseable number, or a label the
// categorical field's dictionary does not hold. The dictionary is the
// full one the schema block carries — on a shard archive the canonical
// schema's, whose dictionaries are the union of every shard's.
func FilterKeys(filterType types.FiltererType, fieldName string, field *encoding.Field, values []string) (map[float64]bool, error) {
	word := "include"
	if filterType == types.FILTER_EXCLUDE {
		word = "exclude"
	}
	set := make(map[float64]bool, len(values))
	for _, v := range values {
		key, found, err := Key(field, v)
		if err != nil {
			return nil, errors.WrapCodedError(err, errors.PROCESSING_CONFIG,
				"parsing "+word+" value "+strconv.Quote(v))
		}
		if !found {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				word+" value "+strconv.Quote(v)+" not found in dictionary for field "+strconv.Quote(fieldName),
				map[string]any{"filterer": string(filterType), "field": fieldName, "value": v})
		}
		set[key] = true
	}
	return set, nil
}
