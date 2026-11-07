package descriptor

import (
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/codein"
	"github.com/frankbardon/pulse/types"
)

// ATTR_CODE_IN predict checks. The slot rules — the params.codes
// parse, the accepted field types, the integer range and the
// unknown-field refusal — are internal/codein's, which the runtime
// constructor (internal/processing/attribute_code_in.go) calls too, so
// the two refuse with the same code, message and details by
// construction; TestCodeIn_PredictRefusalsMatchRuntime
// (internal/integration) pins the order. target / predictors are the
// field-reference walk's general refusal
// (model.ForeignRegressionSlotRefusal), not this file's.

// validateCodeIn reports, per ATTR_CODE_IN slot, the first refusal the
// runtime's constructor would raise (params.codes shape, field type,
// integer code range) and otherwise — on a
// categorical field only — one PULSE_ATTR_CODE_NOT_IN_DICTIONARY entry
// naming every code absent from the dictionary (a warning; an error
// under Strict). A missing or unknown Field is the field-reference
// rule's refusal, not this one's. A slot whose type the instance hides
// routes to "" and is skipped, exactly like a never-registered name.
func validateCodeIn(env *descriptor.Envelope, req *types.Request, schema *encoding.Schema, projected map[string]bool, opts *PredictOptions) {
	if req == nil || schema == nil {
		return
	}
	for _, attr := range req.Attributes {
		if attr == nil || opRoute(opts.Instance, attr.Type) != types.ATTR_CODE_IN || attr.Field == "" {
			continue
		}
		missing, err := codeInPredictCheck(attr, schema, projected)
		if err != nil {
			addCodedError(env, err)
			continue
		}
		if len(missing) == 0 {
			continue
		}
		entry := &descriptor.EnvelopeEntry{
			Code: string(errors.PULSE_ATTR_CODE_NOT_IN_DICTIONARY),
			Message: "ATTR_CODE_IN: " + strconv.Itoa(len(missing)) + " code(s) on categorical field " + strconv.Quote(attr.Field) +
				" are absent from its dictionary and match no row: " + codeInQuoteJoin(missing),
			Details: map[string]any{
				"attribute":     string(types.ATTR_CODE_IN),
				"field":         attr.Field,
				"missing_codes": missing,
			},
		}
		if opts.Strict {
			env.Errors = append(env.Errors, entry)
		} else {
			env.Warnings = append(env.Warnings, entry)
		}
	}
}

// codeInPredictCheck mirrors newCodeInAttribute's checks in order. It
// returns the runtime's refusal, or — for an accepted categorical slot —
// the sorted codes the dictionary lacks (every code when the field has
// no dictionary). An integer slot never reports missing codes. A
// derived column (in projected, not in the schema) is the constructor's
// "unknown field" refusal; any other name the schema does not carry
// returns nothing — the field-reference rule judges it.
func codeInPredictCheck(attr *types.Attribute, schema *encoding.Schema, projected map[string]bool) ([]string, error) {
	codes, err := codein.ParseCodes(attr.Params)
	if err != nil {
		return nil, err
	}
	f := schema.Field(attr.Field)
	if f == nil {
		if projected[attr.Field] {
			// A derived column (an earlier attribute's label, a feature
			// output): the field-reference rule admits it, but the
			// constructor resolves against the schema only.
			return nil, codein.UnknownField(attr.Field)
		}
		return nil, nil
	}
	if f.Type.IsVector() {
		return nil, nil // the field-reference walk's refusal (vectorScalarRefusals)
	}
	if _, cerr := codein.Check(attr.Field, f.Type, codes); cerr != nil {
		return nil, cerr
	}
	if f.Type.IsCategorical() {
		return codein.Missing(f.Dictionary, codes), nil
	}
	return nil, nil
}

// codeInQuoteJoin renders codes as a comma-separated list of Go-quoted
// strings for a message.
func codeInQuoteJoin(codes []string) string {
	q := make([]string, len(codes))
	for i, c := range codes {
		q[i] = strconv.Quote(c)
	}
	return strings.Join(q, ", ")
}
