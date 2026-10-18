// Package aucspec holds AGG_AUC's configuration rules, shared by the
// runtime constructor (internal/processing) and predict
// (internal/descriptor) so the two refuse the same request with the
// same code, message and details by construction.
//
// It is a leaf: it imports only the public errors / encoding / types
// packages and internal/fieldaccept, never internal/processing or
// internal/service, so internal/descriptor may import it without
// breaking TestPredictNoExecutionImports.
package aucspec

import (
	"math"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/types"
)

// Name is the operator's wire name.
const Name = string(types.AGG_AUC)

// Params is AGG_AUC's typed params decode. Both sides decode into this
// one type, so a decode error quotes the same Go type on both.
type Params struct {
	// Outcome names the binary outcome column (0/1 numeric or
	// packed_bool). Required.
	Outcome string `json:"outcome"`
	// Positive is the outcome value counted as the event: 0 or 1,
	// default 1.
	Positive *float64 `json:"positive,omitempty"`
}

// PositiveValue is the event value: params.positive, or 1.
func (p Params) PositiveValue() float64 {
	if p.Positive == nil {
		return 1
	}
	return *p.Positive
}

// ConfigError is the slot's configuration refusal, in the runtime
// constructor's order: a missing outcome, a positive other than 0 / 1,
// then the accepted-types table on the score (field) and the outcome.
// A name the schema does not carry is the field-reference rule's
// refusal, never this one's; a nil schema judges no type.
func ConfigError(field string, p Params, schema *encoding.Schema) error {
	if p.Outcome == "" {
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			Name+" requires params.outcome (the binary 0/1 outcome column scored against the score in field)",
			map[string]any{"aggregation": Name, "field": field})
	}
	if p.Positive != nil && *p.Positive != 0 && *p.Positive != 1 {
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			Name+": params.positive must be 0 or 1 (the outcome value counted as the event), got "+
				strconv.FormatFloat(*p.Positive, 'g', -1, 64),
			map[string]any{"aggregation": Name, "field": field, "positive": *p.Positive})
	}
	if schema == nil {
		return nil
	}
	if cerr := fieldaccept.Check(Name, fieldaccept.SlotField, field, schema); cerr != nil {
		return cerr
	}
	if cerr := fieldaccept.Check(Name, fieldaccept.SlotOutcome, p.Outcome, schema); cerr != nil {
		return cerr
	}
	return nil
}

// NotBinary is the run-time refusal of an outcome value other than 0
// or 1 (NaN included). Predict cannot raise it: it reads no record.
// A non-finite value is null in details (JSON has no NaN); the message
// names it.
func NotBinary(outcome string, value float64) error {
	var v any = value
	if math.IsNaN(value) || math.IsInf(value, 0) {
		v = nil
	}
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_AUC_OUTCOME_NOT_BINARY,
		Name+": outcome "+strconv.Quote(outcome)+" holds "+strconv.FormatFloat(value, 'g', -1, 64)+
			"; the outcome must be 0 or 1 on every scored row (recode it, or filter the other rows out)",
		map[string]any{"aggregation": Name, "outcome": outcome, "value": v})
}
