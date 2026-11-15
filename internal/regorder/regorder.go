// Package regorder is the one rule set of a regression slot's row order
// (types.RegressionSpec.Order), shared by predict
// (internal/descriptor) and the engine (internal/processing/regression,
// internal/regadapt): the spec's shape and field types (Check), the
// order an order-requiring model needs (RequiredRefusal) and the series
// rule the engine applies to the admitted rows (CheckSeries). It reads
// no record and imports neither the engine nor the service.
package regorder

import (
	"fmt"
	"slices"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Slot is the spec key every refusal names (details.slot).
const Slot = "order"

// The details.reason values of the spec refusals (PROCESSING_CONFIG).
const (
	// ReasonFieldMissing: order is set without order.field.
	ReasonFieldMissing = "field_missing"
	// ReasonGapsInvalid: order.gaps is not a known policy.
	ReasonGapsInvalid = "gaps_invalid"
	// ReasonPartitionIsField: order.partition names order.field.
	ReasonPartitionIsField = "partition_is_field"
	// ReasonFieldType: order.field is not an unsigned integer, date or
	// datetime field.
	ReasonFieldType = "field_type"
	// ReasonPartitionType: order.partition is not a categorical,
	// unsigned integer, date, datetime or packed_bool field.
	ReasonPartitionType = "partition_type"
	// ReasonOrderRequired: the model requires an order and the spec
	// sets none.
	ReasonOrderRequired = "order_required"
)

// The details.reason values of the data refusal
// (PROCESSING_REGRESSION_ORDER).
const (
	ReasonDuplicate = "duplicate"
	ReasonGap       = "gap"
)

// Gaps is spec's effective gap policy: order.gaps, OrderGapsRefuse when
// empty.
func Gaps(o *types.RegressionOrder) types.OrderGaps {
	if o == nil || o.Gaps == "" {
		return types.OrderGapsRefuse
	}
	return o.Gaps
}

// config is a PROCESSING_CONFIG refusal of spec's order naming reason.
func config(spec *types.RegressionSpec, reason, msg string, extra map[string]any) *errors.CodedError {
	details := map[string]any{"regression": string(spec.Type), "slot": Slot, "reason": reason}
	if spec.Name != "" {
		details["name"] = spec.Name
	}
	for k, v := range extra {
		details[k] = v
	}
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
		"regression "+string(spec.Type)+" order: "+msg, details)
}

// OrderFieldType reports whether t can order a regression's rows: an
// unsigned integer (a period index), date or datetime field.
func OrderFieldType(t encoding.FieldType) bool {
	switch t {
	case encoding.FieldTypeU4, encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32, encoding.FieldTypeU64,
		encoding.FieldTypeDate, encoding.FieldTypeDateTime:
		return true
	}
	return false
}

// PartitionFieldType reports whether t can key a series: a
// categorical, unsigned integer, date, datetime or packed_bool field.
func PartitionFieldType(t encoding.FieldType) bool {
	return t.IsCategorical() || OrderFieldType(t) || t == encoding.FieldTypePackedBool
}

// Check is the spec rule of spec's order — nil when spec sets none or
// the order is well formed: order.field is required, order.gaps a known
// policy, order.partition another field than order.field, and, for a name the schema carries (a derived
// column is not judged; nil schema judges none), order.field an
// unsigned integer, date or datetime field and order.partition a
// categorical, unsigned integer, date, datetime or packed_bool field.
// An unknown name is the field-reference walk's refusal.
func Check(spec *types.RegressionSpec, schema *encoding.Schema) *errors.CodedError {
	if spec == nil || spec.Order == nil {
		return nil
	}
	o := spec.Order
	if o.Field == "" {
		return config(spec, ReasonFieldMissing, "order.field must name the field that orders the rows (an unsigned integer period index, a date or a datetime)", nil)
	}
	if !slices.Contains(types.AllOrderGaps(), Gaps(o)) {
		return config(spec, ReasonGapsInvalid, fmt.Sprintf("order.gaps %q is not a gap policy (refuse)", o.Gaps), map[string]any{"gaps": string(o.Gaps)})
	}
	if o.Partition == o.Field && o.Partition != "" {
		return config(spec, ReasonPartitionIsField, "order.partition names order.field "+o.Field+"; the partition is the field that splits the rows into series (a market), not the one that orders them", map[string]any{"field": o.Field})
	}
	if schema == nil {
		return nil
	}
	if f := schema.Field(o.Field); f != nil && !OrderFieldType(f.Type) {
		return config(spec, ReasonFieldType, "order.field "+o.Field+" is a "+f.Type.String()+" field; order the rows by an unsigned integer period index, a date or a datetime",
			map[string]any{"field": o.Field, "field_type": f.Type.String()})
	}
	if o.Partition != "" {
		if f := schema.Field(o.Partition); f != nil && !PartitionFieldType(f.Type) {
			return config(spec, ReasonPartitionType, "order.partition "+o.Partition+" is a "+f.Type.String()+" field; key the series by a categorical, unsigned integer, date, datetime or packed_bool field",
				map[string]any{"field": o.Partition, "field_type": f.Type.String()})
		}
	}
	return nil
}

// RequiredRefusal is the refusal of a slot whose model requires an
// order (a registration's RequiresOrder) but whose spec sets none.
func RequiredRefusal(spec *types.RegressionSpec) *errors.CodedError {
	return config(spec, ReasonOrderRequired, "the model reads its rows in order (it carries state from one row to the next) and needs order {field[, partition]} naming the field that orders them",
		map[string]any{"requires": Slot})
}

// Series is one ordered series of a slot's admitted rows: its partition
// key (nil on an unpartitioned order; a label string or a number
// otherwise) and its order values, ascending.
type Series struct {
	Partition any
	Values    []int64
}

// CheckSeries is the gap policy over a slot's admitted rows, each series
// already sorted ascending: a repeated value inside one series is
// PROCESSING_REGRESSION_ORDER reason duplicate, and — the cadence being
// the smallest positive step over every series — a step that is not the
// cadence is reason gap. The first offence in series order, then value
// order, is reported. Nil when every series is contiguous (or holds
// fewer than two rows).
func CheckSeries(spec *types.RegressionSpec, series []Series) *errors.CodedError {
	if spec == nil || spec.Order == nil || Gaps(spec.Order) != types.OrderGapsRefuse {
		return nil
	}
	var cadence int64
	for _, s := range series {
		for i := 1; i < len(s.Values); i++ {
			step := s.Values[i] - s.Values[i-1]
			if step == 0 {
				return dataRefusal(spec, s.Partition, ReasonDuplicate,
					fmt.Sprintf("order.field %s repeats %d%s; an ordered fit reads one row per period%s", spec.Order.Field, s.Values[i], inSeries(s.Partition), panelHint(spec)),
					map[string]any{"value": s.Values[i]})
			}
			if cadence == 0 || step < cadence {
				cadence = step
			}
		}
	}
	for _, s := range series {
		for i := 1; i < len(s.Values); i++ {
			if step := s.Values[i] - s.Values[i-1]; step != cadence {
				return dataRefusal(spec, s.Partition, ReasonGap,
					fmt.Sprintf("order.field %s jumps from %d to %d%s, but the series step is %d: the periods must be contiguous (a filtered or null row leaves a gap an order-dependent model would carry across)",
						spec.Order.Field, s.Values[i-1], s.Values[i], inSeries(s.Partition), cadence),
					map[string]any{"from": s.Values[i-1], "to": s.Values[i], "step": cadence})
			}
		}
	}
	return nil
}

func inSeries(p any) string {
	if p == nil {
		return ""
	}
	return fmt.Sprintf(" in series %v", p)
}

func panelHint(spec *types.RegressionSpec) string {
	if spec.Order.Partition != "" {
		return ""
	}
	return " (for a panel — several markets per period — name the market field in order.partition)"
}

// dataRefusal is the PROCESSING_REGRESSION_ORDER refusal (the slot fails
// alone).
func dataRefusal(spec *types.RegressionSpec, partition any, reason, msg string, extra map[string]any) *errors.CodedError {
	details := map[string]any{"regression": string(spec.Type), "slot": Slot, "reason": reason, "field": spec.Order.Field}
	if spec.Name != "" {
		details["name"] = spec.Name
	}
	if partition != nil {
		details["partition"] = partition
	}
	for k, v := range extra {
		details[k] = v
	}
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_REGRESSION_ORDER,
		"regression "+string(spec.Type)+": "+msg, details)
}
