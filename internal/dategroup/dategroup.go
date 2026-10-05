// Package dategroup is the one reading of GROUP_DATE's params, shared by
// the operator factory (internal/processing) and predict
// (internal/descriptor) so both refuse the same request with the same
// code and message. It decides what a request MEANS — the component, the
// fiscal offset, the week start — and never buckets a value: the
// calendar arithmetic itself lives in internal/temporal.
//
// Leaf: stdlib + the public errors / encoding packages + temporal.
package dategroup

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/temporal"
)

// Operator is the type name every refusal names.
const Operator = "GROUP_DATE"

// DefaultComponent is the component a GROUP_DATE without one buckets by.
const DefaultComponent = "month"

// ComponentHour is the sub-day component: the local wall-clock hour.
const ComponentHour = "hour"

// componentList is the component set in the order refusals name it.
const componentList = "year, quarter, month, week, day, hour, day_of_week"

var validComponents = map[string]bool{
	"year": true, "quarter": true, "month": true,
	"week": true, "day": true, ComponentHour: true, "day_of_week": true,
}

// fiscalComponents lists the only components that meaningfully accept a
// fiscal_offset. Month / week / day / hour / day_of_week buckets do not
// shift under a fiscal calendar.
var fiscalComponents = map[string]bool{"year": true, "quarter": true}

// params is the wire shape of GROUP_DATE params.
type params struct {
	Component    string  `json:"component"`
	FiscalOffset *int    `json:"fiscal_offset,omitempty"`
	WeekStart    *string `json:"week_start,omitempty"`
}

// Spec is a validated GROUP_DATE configuration.
type Spec struct {
	// Component is the calendar component (DefaultComponent when unset).
	Component string
	// FiscalOffset: 0 = calendar; non-zero = the FY starts at month
	// (((offset%12)+12)%12)+1 (year / quarter only).
	FiscalOffset int
	// WeekStart is the weekday a `week` bucket begins on; time.Monday
	// (the default) is the ISO week.
	WeekStart time.Weekday
}

// DatedWeeks reports whether `week` buckets key by the local date of
// the week's first day (`2026-03-01`) rather than the ISO `YYYY-Www`
// label: a week start other than Monday has no ISO numbering.
func (s Spec) DatedWeeks() bool {
	return s.Component == "week" && s.WeekStart != time.Monday
}

// Parse validates raw GROUP_DATE params. Refusals, in order, are all
// PROCESSING_CONFIG: malformed JSON, an unknown component, a
// fiscal_offset outside [-11, 11], a fiscal_offset on a component other
// than year / quarter, a week_start that is not a lowercase English day
// name, and a week_start on a component other than week.
func Parse(raw json.RawMessage) (Spec, error) {
	spec := Spec{Component: DefaultComponent, WeekStart: time.Monday}
	var p params
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return Spec{}, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("invalid GROUP_DATE params: %v", err))
		}
		if p.Component != "" {
			spec.Component = p.Component
		}
		if p.FiscalOffset != nil {
			spec.FiscalOffset = *p.FiscalOffset
		}
	}
	if !validComponents[spec.Component] {
		return Spec{}, errors.NewCodedError(errors.PROCESSING_CONFIG,
			fmt.Sprintf("invalid date group component %q: must be one of %s", spec.Component, componentList))
	}
	if spec.FiscalOffset < -11 || spec.FiscalOffset > 11 {
		return Spec{}, errors.NewCodedError(errors.PROCESSING_CONFIG,
			fmt.Sprintf("invalid GROUP_DATE fiscal_offset %d: must be in range [-11, 11]", spec.FiscalOffset))
	}
	if spec.FiscalOffset != 0 && !fiscalComponents[spec.Component] {
		return Spec{}, errors.NewCodedError(errors.PROCESSING_CONFIG,
			fmt.Sprintf("GROUP_DATE fiscal_offset only applies to component=year or component=quarter, got %q", spec.Component))
	}
	if p.WeekStart != nil {
		wd, ok := temporal.ParseWeekday(*p.WeekStart)
		if !ok {
			return Spec{}, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("invalid GROUP_DATE week_start %q: must be one of monday, tuesday, wednesday, thursday, friday, saturday, sunday", *p.WeekStart))
		}
		if spec.Component != "week" {
			return Spec{}, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("GROUP_DATE week_start only applies to component=week, got %q", spec.Component))
		}
		spec.WeekStart = wd
	}
	return spec, nil
}

// CheckField refuses an `hour` component over field unless schema
// declares it `datetime`: a `date` carries no time of day, a
// non-temporal column is read as epoch days, and a field absent from
// the schema (derived) is read as epoch days too — each would put every
// row in hour 00 rather than fail. A nil schema (the probe paths, which
// build an operator with no cohort behind it) is not judged. Every other
// component keeps GROUP_DATE's permissive field posture.
func CheckField(spec Spec, field string, schema *encoding.Schema) error {
	if spec.Component != ComponentHour || schema == nil {
		return nil
	}
	f := schema.Field(field)
	if f != nil && f.Type == encoding.FieldTypeDateTime {
		return nil
	}
	got := "absent from the schema (a derived field is read as epoch days)"
	if f != nil {
		got = "of type " + f.Type.String()
	}
	return errors.NewCodedError(errors.PROCESSING_CONFIG,
		fmt.Sprintf("GROUP_DATE component=hour requires a datetime field; field %q is %s", field, got))
}
