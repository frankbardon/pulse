package descriptor

import (
	"bytes"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// ATTR_CODE_IN predict checks. The runtime rules live in
// internal/processing/attribute_code_in.go (newCodeInAttribute,
// parseCodeInCodes, codeInCodeText); predict may not import that
// package, so the rules are re-derived here from the schema alone —
// same order, same code, same message, same details — and
// TestCodeIn_PredictRefusalsMatchRuntime (root package) pins the two
// copies together.

// codeInPredictIntegerMax mirrors processing.codeInIntegerMax: the
// largest value each accepted unsigned-integer field type can hold.
var codeInPredictIntegerMax = map[encoding.FieldType]uint64{
	encoding.FieldTypeU4:  15,
	encoding.FieldTypeU8:  math.MaxUint8,
	encoding.FieldTypeU16: math.MaxUint16,
	encoding.FieldTypeU32: math.MaxUint32,
	encoding.FieldTypeU64: math.MaxUint64,
}

// validateCodeIn reports, per ATTR_CODE_IN slot, the first refusal the
// runtime's constructor would raise (target / predictors, params.codes
// shape, field type, integer code range) and otherwise — on a
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
	if attr.Target != "" || len(attr.Predictors) > 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			"ATTR_CODE_IN does not take target or predictors (those are regression-attribute slots); name the output column with label",
			map[string]any{
				"attribute":  string(types.ATTR_CODE_IN),
				"field":      attr.Field,
				"target":     attr.Target,
				"predictors": attr.Predictors,
			})
	}
	codes, err := codeInPredictCodes(attr.Params)
	if err != nil {
		return nil, err
	}
	f := schema.Field(attr.Field)
	if f == nil {
		if projected[attr.Field] {
			// A derived column (an earlier attribute's label, a feature
			// output): the field-reference rule admits it, but the
			// constructor resolves against the schema only.
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				"ATTR_CODE_IN: unknown field "+attr.Field)
		}
		return nil, nil
	}
	if !f.Type.IsCategorical() {
		if _, ok := codeInPredictIntegerMax[f.Type]; !ok {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				"ATTR_CODE_IN: field "+strconv.Quote(attr.Field)+" is "+f.Type.String()+
					"; it accepts only categorical_u8/u16/u32 and u4/u8/u16/u32/u64 fields",
				map[string]any{
					"attribute": string(types.ATTR_CODE_IN),
					"field":     attr.Field,
					"type":      f.Type.String(),
				})
		}
	}
	if f.Type.IsCategorical() {
		var missing []string
		for _, c := range codes {
			if f.Dictionary != nil {
				if _, ok := f.Dictionary.IDFor(c); ok {
					continue
				}
			}
			missing = append(missing, c)
		}
		sort.Strings(missing)
		return missing, nil
	}
	maxV := codeInPredictIntegerMax[f.Type]
	for _, c := range codes {
		n, perr := strconv.ParseUint(c, 10, 64)
		if perr != nil || n > maxV {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				"ATTR_CODE_IN: code "+strconv.Quote(c)+" can never match "+f.Type.String()+" field "+strconv.Quote(attr.Field)+
					"; codes on an integer field must be whole numbers in 0.."+strconv.FormatUint(maxV, 10),
				map[string]any{
					"attribute": string(types.ATTR_CODE_IN),
					"field":     attr.Field,
					"type":      f.Type.String(),
					"code":      c,
					"max":       maxV,
				})
		}
	}
	return nil, nil
}

// codeInPredictCodes mirrors processing.parseCodeInCodes: params.codes
// decoded to code strings, deduped in first-seen order. A JSON integer
// is carried as its literal text (3 and "3" are one code); any other
// JSON kind is refused, as is a missing or empty list.
func codeInPredictCodes(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"ATTR_CODE_IN requires params with a non-empty \"codes\" list")
	}
	var p struct {
		Codes []json.RawMessage `json:"codes"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, errors.WrapCodedError(err, errors.PROCESSING_CONFIG, "parsing ATTR_CODE_IN params")
	}
	if len(p.Codes) == 0 {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"ATTR_CODE_IN requires params with a non-empty \"codes\" list")
	}
	seen := make(map[string]bool, len(p.Codes))
	out := make([]string, 0, len(p.Codes))
	for _, rc := range p.Codes {
		c, ok := codeInPredictCodeText(rc)
		if !ok {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				"ATTR_CODE_IN: each params.codes entry must be a JSON string or a JSON integer, got "+string(bytes.TrimSpace(rc)),
				map[string]any{"attribute": string(types.ATTR_CODE_IN), "code": string(bytes.TrimSpace(rc))})
		}
		if seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out, nil
}

// codeInPredictCodeText mirrors processing.codeInCodeText: a JSON
// string's value, or a JSON integer's literal (optionally signed)
// digits; ok is false for every other JSON kind.
func codeInPredictCodeText(rc json.RawMessage) (string, bool) {
	t := bytes.TrimSpace(rc)
	if len(t) == 0 {
		return "", false
	}
	if t[0] == '"' {
		var s string
		if err := json.Unmarshal(t, &s); err != nil {
			return "", false
		}
		return s, true
	}
	digits := t
	if digits[0] == '-' {
		digits = digits[1:]
	}
	if len(digits) == 0 {
		return "", false
	}
	for _, b := range digits {
		if b < '0' || b > '9' {
			return "", false
		}
	}
	return string(t), true
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
