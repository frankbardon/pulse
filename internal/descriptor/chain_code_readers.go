package descriptor

import (
	"encoding/json"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// codeReaderParamSlots are the aggregator slots a params key names, in
// the constructors' order; a slot is judged only where the
// accepted-types table rows it for the operator.
var codeReaderParamSlots = []struct {
	slot fieldaccept.Slot
	key  string
}{
	{fieldaccept.SlotNumerator, "numerator_field"},
	{fieldaccept.SlotDenominator, "denominator_field"},
	{fieldaccept.SlotPrediction, "prediction_field"},
	{fieldaccept.SlotOutcome, "outcome"},
}

// ChainStageCodeReaderRefusals is the accepted-types rule for the
// aggregator slots of a chain stage >= 1 that read a FORWARDED CODE
// column (U66 #446). The stage's synthesised input schema types every
// forwarded aggregation column f64, so stage 0's refusal of a numeric
// reader over a categorical field (fieldaccept, the numericReader
// wrapper) never fired on AGG_SUM over an upstream AGG_MODE-over-
// categorical column: it summed dictionary indexes. Here a column in
// codes (vectors.AggregationKinds.ChainCodeColumns) is judged as the
// type it is a code of (vectors.CodeFieldTypes) — so a categorical code
// is refused by every row that refuses a categorical (the analytics
// readers, MIN / MAX included: D1), a packed_bool code stays legal for
// SUM / AVERAGE as a packed_bool field is (D2), and the code-safe rows
// (nonSetValue, COUNT / NULL_COUNT, extension operators: untabled) are
// untouched. Every other column is left to the schema-typed rules.
//
// One refusal per slot, PULSE_FIELD_TYPE_UNSUPPORTED (fieldaccept.
// Refusal's code, message and details, plus details.forwarded_code).
// Predict (ValidateChainWithOptions) reports each; the runtime
// (runChainStageChild, after the limits pre-flight) raises the first —
// both located details.stage by the caller. A hidden operator is walked
// as never registered (opRoute). Nil codes: nil.
func ChainStageCodeReaderRefusals(req *types.Request, schema *encoding.Schema, codes map[string]encoding.FieldType, inst *InstanceSnapshot) []*errors.CodedError {
	if req == nil || len(codes) == 0 {
		return nil
	}
	typeOf := vectors.CodeFieldTypes(schema, codes)
	if typeOf == nil {
		return nil
	}
	var out []*errors.CodedError
	judge := func(op string, slot fieldaccept.Slot, field string) {
		if _, isCode := codes[field]; !isCode || field == "" {
			return
		}
		ft, ok := typeOf(field)
		if !ok {
			return
		}
		if cerr := fieldaccept.Refusal(op, slot, field, ft); cerr != nil {
			details := make(map[string]any, len(cerr.Details)+1)
			for k, v := range cerr.Details {
				details[k] = v
			}
			details["forwarded_code"] = true
			out = append(out, errors.NewCodedErrorWithDetails(cerr.Code,
				cerr.Message+" (a code an earlier chain stage forwarded: count it instead)", details))
		}
	}
	for _, a := range req.Aggregations {
		if a == nil {
			continue
		}
		op := string(opRoute(inst, a.Type))
		if op == "" {
			continue
		}
		if _, ok := fieldaccept.Lookup(op, fieldaccept.SlotField); ok {
			judge(op, fieldaccept.SlotField, a.Field)
		}
		var params map[string]json.RawMessage
		for _, ps := range codeReaderParamSlots {
			if _, ok := fieldaccept.Lookup(op, ps.slot); !ok {
				continue
			}
			if params == nil && json.Unmarshal(a.Params, &params) != nil {
				break
			}
			var field string
			if json.Unmarshal(params[ps.key], &field) == nil {
				judge(op, ps.slot, field)
			}
		}
	}
	return out
}
