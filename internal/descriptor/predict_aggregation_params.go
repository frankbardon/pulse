package descriptor

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/ddofspec"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/orderedcat"
	"github.com/frankbardon/pulse/internal/percentilespec"
	"github.com/frankbardon/pulse/types"
)

// validateAggregationParams reports a built-in aggregation slot missing
// a param its factory requires, with the runtime's code and message, so
// predict refuses what Process would refuse at operator construction.
// That is AGG_STDDEV / AGG_VARIANCE with a params.ddof other than 0 / 1
// (internal/ddofspec, the constructors' rule), AGG_PERCENTILE with a
// params.percentile missing (absent or null — required, D33), outside
// [0, 100] or not a number (internal/percentilespec, likewise), and AGG_FREQUENCY
// without params.value: the slot once named
// the modal count, so the message also points at AGG_MODE_COUNT —
// unless the instance hides it (a refusal never names a hidden
// feature). Walks every aggregation slot a Request carries: the
// top-level list, the crosstab cell and its margin aggregations.
//
// A slot whose field the accepted-types table (decimal rule included)
// or the ordered-categorical rule already refuses gets no ddof /
// percentile finding: the runtime raises that field refusal before its
// constructor reads the param (the decimal dispatch, orderedFactory and
// numericReader all run first), so predict's first finding for the
// slot is the runtime's (U66 E9-S5; validateFieldAcceptance /
// validateOrderedAggregations report it).
func validateAggregationParams(env *descriptor.Envelope, req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot) {
	if req == nil {
		return
	}
	check := func(agg *types.Aggregation, slot string) {
		if agg == nil {
			return
		}
		// AGG_STDDEV / AGG_VARIANCE params.ddof: the constructors' own
		// rule (internal/ddofspec), on every field type — the decimal
		// dispatch resolves it too.
		if route := opRoute(inst, agg.Type); ddofspec.Applies(route) {
			if fieldRuleRefuses(route, agg, schema) {
				return
			}
			if _, cerr := ddofspec.Resolve(route, agg.Params); cerr != nil {
				addCodedError(env, cerr)
			}
			return
		}
		// AGG_PERCENTILE params.percentile: the constructors' own rule
		// (internal/percentilespec) — the plain, weighted and
		// ordered-categorical arms all read it.
		if route := opRoute(inst, agg.Type); percentilespec.Applies(route) {
			if fieldRuleRefuses(route, agg, schema) {
				return
			}
			if _, cerr := percentilespec.Resolve(agg.Params); cerr != nil {
				addCodedError(env, cerr)
			}
			return
		}
		if opRoute(inst, agg.Type) != types.AGG_FREQUENCY {
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
		for i, cell := range req.Crosstab.CellAggregations() {
			check(cell, req.Crosstab.CellSlotPath(i))
		}
		for i, a := range req.Crosstab.MarginAggregations {
			check(a, "crosstab.margin_aggregations["+strconv.Itoa(i)+"]")
		}
	}
}

// fieldRuleRefuses reports whether the runtime refuses agg's field
// before its constructor reads a param: the ordered-categorical rule
// (internal/orderedcat) for a slot requesting the ordered reading, the
// accepted-types table (internal/fieldaccept, decimal rule first)
// otherwise — the order of the runtime's orderedFactory / numericReader
// and decimal dispatch. A field the schema does not carry is the
// field-reference walk's (or the ordered rule's) finding, not judged
// here by the table.
func fieldRuleRefuses(op types.AggregationType, agg *types.Aggregation, schema *encoding.Schema) bool {
	if schema == nil {
		return false
	}
	if orderedcat.Requested(op, agg.Params) {
		_, cerr := orderedcat.Resolve(op, agg.Field, agg.Params, schema)
		return cerr != nil
	}
	return fieldaccept.Check(string(op), fieldaccept.SlotField, agg.Field, schema) != nil
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
