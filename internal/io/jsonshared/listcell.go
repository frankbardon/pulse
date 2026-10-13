package jsonshared

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"github.com/frankbardon/pulse/internal/iocore"
)

// ListCellOf reads one decoded JSON value (decoded with UseNumber) as a
// typed numeric list (iocore.ListAwareReader): an absent key or JSON
// null is iocore.ListNull, an array of numbers and nulls is
// iocore.ListValues (a null element is NaN), an array holding a string,
// boolean or a number float64 cannot hold is iocore.ListNonNumeric at
// that element, and any non-array value is iocore.ListNotList. The
// elements are the decoded JSON numbers — never the pipe-joined text
// ValueToString renders the same cell as. A quoted number ("3") is a
// string, not a number.
func ListCellOf(v any, present bool, dst *iocore.ListCell) {
	if !present || v == nil {
		dst.Reset(iocore.ListNull)
		return
	}
	arr, ok := v.([]any)
	if !ok {
		dst.Reset(iocore.ListNotList)
		dst.BadValue = ValueToString(v)
		return
	}
	dst.Reset(iocore.ListValues)
	for e, el := range arr {
		switch x := el.(type) {
		case nil:
			dst.Append(math.NaN(), true)
			continue
		case json.Number:
			if f, err := strconv.ParseFloat(x.String(), 64); err == nil {
				dst.Append(f, false)
				continue
			}
		case float64:
			dst.Append(x, false)
			continue
		}
		dst.State = iocore.ListNonNumeric
		dst.BadIndex = e
		if s, isStr := el.(string); isStr {
			dst.BadValue = strconv.Quote(s)
		} else {
			dst.BadValue = fmt.Sprintf("%v", el)
		}
		return
	}
}
