package types

import (
	"bytes"
	"encoding/json"
)

// Params is a request's named parameters (Request.Params): constants
// every request-scoped expression reads, read-only, as params.<name>
// (ATTR_FORMULA, FILTER_EXPRESSION, a total() argument, a regression
// holdout, OVERLAY_FORMULA). A value is a number, a string, a bool or a
// list of ONE of those kinds; null, an object, a nested list or a mixed
// list is refused (PULSE_PARAMS_INVALID), as is a name that is not an
// identifier.
//
// Numbers stay exact: decoding keeps each JSON number as a json.Number
// (its text), and the expression binds an integer as an integer, so a
// u64 id beyond 2^53 compares exactly. A request built in Go may hold
// native Go numbers (int, uint64, float64, ...) or json.Number; both
// bind as the JSON text they marshal to.
type Params map[string]any

// UnmarshalJSON decodes a JSON object, keeping every number as a
// json.Number (never a float64) so no integer loses precision before
// the expression binds it.
func (p *Params) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*p = nil
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return err
	}
	*p = Params(m)
	return nil
}
