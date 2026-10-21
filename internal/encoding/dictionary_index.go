package encoding

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// Reasons carried in details.reason of a dictionary-index refusal.
const (
	// ReasonCategoricalIndexOutOfRange: a categorical cell's stored
	// index addresses no entry of its field's dictionary.
	ReasonCategoricalIndexOutOfRange = "categorical_index_out_of_range"
	// ReasonSetMemberOutOfRange: a set cell's mask selects a bit past
	// its field's dictionary.
	ReasonSetMemberOutOfRange = "set_member_out_of_range"
)

// CategoricalIndexFault reports a non-null categorical cell whose
// stored index v addresses no entry of f's dictionary. Every shard of
// an archive decodes against the CANONICAL (widened) dictionary, of
// which each shard's own dictionary is a prefix, so an index at or past
// its length is corrupt record data on every arm — never a category.
// A consumer that keys, labels or renders the cell refuses it with this
// error rather than minting the index as a key (U51 E5-S8).
//
// Returns nil for an in-range index, a field that is not categorical
// or a field with no dictionary (nothing to resolve against).
func CategoricalIndexFault(f *encoding.Field, v float64) error {
	if f == nil || !f.Type.IsCategorical() || f.Dictionary == nil {
		return nil
	}
	n := f.Dictionary.Count()
	if v >= 0 && v < float64(n) && v == math.Trunc(v) {
		return nil
	}
	return dictionaryIndexError(f, ReasonCategoricalIndexOutOfRange, "index", v, n)
}

// SetMemberFault reports a set cell whose mask selects a bit at or
// past the length of f's dictionary: a member no entry names. Same
// contract as CategoricalIndexFault; nil for a non-set field, a field
// with no dictionary, or a mask within the dictionary.
func SetMemberFault(f *encoding.Field, m encoding.SetMask) error {
	if f == nil || !f.Type.IsSet() || f.Dictionary == nil {
		return nil
	}
	n := f.Dictionary.Count()
	hb := m.HighestBit()
	if hb < n {
		return nil
	}
	return dictionaryIndexError(f, ReasonSetMemberOutOfRange, "bit", float64(hb), n)
}

func dictionaryIndexError(f *encoding.Field, reason, key string, v float64, n int) error {
	return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
		fmt.Sprintf("field %q holds %s %v outside its %d-entry dictionary: the record data is corrupt", f.Name, key, v, n),
		map[string]any{
			"field":           f.Name,
			"type":            f.Type.String(),
			key:               v,
			"dictionary_size": n,
			"reason":          reason,
		})
}
