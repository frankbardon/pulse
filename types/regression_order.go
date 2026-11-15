package types

// RegressionOrder is RegressionSpec.Order: the row order a regression
// slot is fitted in. The engine enforces it before any fit, on the
// buffered path (an ordered slot never streams): the rows arrive sorted
// by Field — within each Partition series when Partition is set, the
// series in ascending partition-key order — and, under Gaps "refuse"
// (the default and only policy), the rows the fit admits (target, every
// predictor, Field and Partition present) must form contiguous series:
// a repeated Field value inside one series, or a step between
// consecutive values that is not the cadence (the smallest positive
// step over every series), fails the slot alone with
// PROCESSING_REGRESSION_ORDER (details.reason "duplicate" or "gap"). A
// row whose Field or Partition is null cannot be placed and drops
// listwise. Gated by capability:regression_order. Absent is
// byte-identical.
type RegressionOrder struct {
	// Field names the ordering field: an unsigned integer (a period
	// index), date (epoch days) or datetime (epoch seconds) cohort field
	// or derived column. A calendar-irregular cadence (months on a date
	// field) reads as gaps — order such a series by an integer period
	// index instead.
	Field string `json:"field"`

	// Partition, optional, names the field whose values split the rows
	// into independent series (a panel: several markets per period) —
	// a categorical, unsigned integer, date, datetime or packed_bool
	// field. Each series is ordered and checked on its own; ragged
	// starts and ends across series are fine. Empty: one series.
	Partition string `json:"partition,omitempty"`

	// Gaps is the gap policy: "refuse" (OrderGapsRefuse, the default
	// when empty) refuses a gap or a repeat in any series.
	Gaps OrderGaps `json:"gaps,omitempty"`
}

// OrderGaps is RegressionOrder.Gaps, the gap policy of an ordered
// regression slot.
type OrderGaps string

// OrderGapsRefuse refuses a gap or a repeated value in any series (the
// default).
const OrderGapsRefuse OrderGaps = "refuse"

// AllOrderGaps returns every gap policy in declaration order.
func AllOrderGaps() []OrderGaps {
	return []OrderGaps{OrderGapsRefuse}
}
