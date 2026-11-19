// Package labelcodes is the one integer-coded label-binding rule every
// surface shares: which field types bind as integer codes (u4 / u8 /
// u16 / u32, the decimal spellings of their stored integers being the
// table keys) and the key check that refuses a table key no stored
// integer could match (PULSE_LABEL_KEY_INVALID). The export and convert
// row writers, the `.sav` writer (internal/io) and the response
// surfaces' binding validator (internal/descriptor.ValidateLabels —
// process, sample, facet, chain and compose, predict included) all call
// it, so one binding means one thing on every target. It reads only
// field types and key strings: no record, no engine.
package labelcodes

import (
	"sort"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// CodeMax reports whether ft is an integer-coded type a value-label
// binding may name, and the largest value it stores. u64 is not one:
// its values past 2^53 have no exact SPSS (double) code, and the
// response surfaces carry it as a float echo.
func CodeMax(ft encoding.FieldType) (uint64, bool) {
	switch ft {
	case encoding.FieldTypeU4:
		return 15, true
	case encoding.FieldTypeU8:
		return 255, true
	case encoding.FieldTypeU16:
		return 65535, true
	case encoding.FieldTypeU32:
		return 1<<32 - 1, true
	}
	return 0, false
}

// CheckKeys refuses PULSE_LABEL_KEY_INVALID when a key of a binding on
// the integer-coded field (type ft, largest value max) is not the
// canonical decimal spelling of a value the field can store — text, a
// leading zero, a sign, a fraction or a value past max could never label
// a stored integer, and dropping it silently would hide the mistake.
// Keys are checked in sorted order so the refusal names the same key on
// every run. scope prefixes the message ("spss", "export", "labels[0]");
// option is the request knob echoed in details ("--labels", "labels").
func CheckKeys(scope, option, field string, ft encoding.FieldType, max uint64, keys []string) *errors.CodedError {
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	for _, k := range sorted {
		n, err := strconv.ParseUint(k, 10, 64)
		if err == nil && strconv.FormatUint(n, 10) == k && n <= max {
			continue
		}
		return errors.NewCodedErrorWithDetails(errors.PULSE_LABEL_KEY_INVALID,
			scope+": the value-label binding on "+strconv.Quote(field)+", a "+ft.String()+
				" field, has the key "+strconv.Quote(k)+", which is not the plain decimal spelling of a value from 0 to "+
				strconv.FormatUint(max, 10)+"; the field's stored integers are its codes, so the key could never label one",
			map[string]any{"field": field, "type": ft.String(), "key": k, "max": max, "option": option})
	}
	return nil
}
