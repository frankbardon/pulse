package descriptor

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// validateAggregationParams reports a built-in aggregation slot missing
// a param its factory requires, with the runtime's code and message, so
// predict refuses what Process would refuse at operator construction.
// Today that is AGG_FREQUENCY without params.value: the slot once named
// the modal count, so the message also points at AGG_MODE_COUNT —
// unless the instance hides it (a refusal never names a hidden
// feature). Walks every aggregation slot a Request carries: the
// top-level list, the crosstab cell and its margin aggregations.
func validateAggregationParams(env *descriptor.Envelope, req *types.Request, inst *InstanceSnapshot) {
	if req == nil {
		return
	}
	check := func(agg *types.Aggregation, slot string) {
		if agg == nil || opRoute(inst, agg.Type) != types.AGG_FREQUENCY {
			return
		}
		if frequencyValuePresent(agg.Params) {
			return
		}
		env.AddError(string(errors.PROCESSING_CONFIG), frequencyValueMissingMessage(inst),
			map[string]any{"aggregator": string(types.AGG_FREQUENCY), "param": "value", "slot": slot})
	}
	for i, a := range req.Aggregations {
		check(a, "aggregations["+strconv.Itoa(i)+"]")
	}
	if req.Crosstab != nil {
		check(req.Crosstab.Cell, "crosstab.cell")
		for i, a := range req.Crosstab.MarginAggregations {
			check(a, "crosstab.margin_aggregations["+strconv.Itoa(i)+"]")
		}
	}
}

// frequencyValueMissingMessage mirrors the runtime refusal
// (internal/processing/aggregator_frequency.go) verbatim; the hidden
// AGG_MODE_COUNT clause drops out exactly as the runtime's
// ScopeRefusal drops it.
func frequencyValueMissingMessage(inst *InstanceSnapshot) string {
	msg := string(types.AGG_FREQUENCY) + " requires params.value: it returns the count of rows equal to that value."
	if !inst.Hidden(string(types.AGG_MODE_COUNT)) {
		msg += " The count of the field's most common value is " + string(types.AGG_MODE_COUNT) + "."
	}
	return msg
}

// frequencyValuePresent mirrors the runtime's params.value decode: a
// non-empty JSON string or a JSON number. Absent, null, "", a bool, an
// object or an array read as missing. Malformed params JSON is left to
// the runtime's own parse refusal.
func frequencyValuePresent(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var p struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return true
	}
	v := strings.TrimSpace(string(p.Value))
	if v == "" || v == "null" {
		return false
	}
	var s string
	if err := json.Unmarshal(p.Value, &s); err == nil {
		return s != ""
	}
	var n json.Number
	return json.Unmarshal(p.Value, &n) == nil
}
