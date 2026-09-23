package descriptor

import (
	"slices"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// TestCapabilities_AnalyticsListsMatchNumericPredicate pins the two
// analytics declaration lists to encoding.FieldType.IsNumericForAnalytics,
// in BOTH directions.
//
// The predicate is the claim the engine actually honours: every
// aggregator in these lists reads its column through
// Record.NumericValue, which answers for any type the decoder writes
// into the float64 values map. `datetime` (type byte 17, epoch seconds)
// satisfies the predicate and decodes into that map, so a declaration
// that omitted it understated what the engine does — an LLM reading
// `accepts_types` was told AGG_SUM could not sum a column it sums fine.
//
// The reverse direction matters just as much: a name in the list that
// the predicate refuses would promise arithmetic on a column
// NumericValue declines to answer for (every set_* rung, which returns
// ok=false by design).
func TestCapabilities_AnalyticsListsMatchNumericPredicate(t *testing.T) {
	names := registeredFieldTypeNames(t)
	if len(names) == 0 {
		t.Fatal("no registered field types")
	}
	for _, name := range names {
		ft, ok := encoding.ParseFieldType(name)
		if !ok {
			t.Fatalf("registered type name %q does not round-trip through ParseFieldType", name)
		}
		want := ft.IsNumericForAnalytics()

		if got := slices.Contains(numericFieldTypesAnalytics, name); got != want {
			t.Errorf("numericFieldTypesAnalytics contains(%q)=%v, IsNumericForAnalytics()=%v",
				name, got, want)
		}

		wantNoDecimal := want && !ft.IsDecimal()
		if got := slices.Contains(numericFieldTypesAnalyticsNoDecimal, name); got != wantNoDecimal {
			t.Errorf("numericFieldTypesAnalyticsNoDecimal contains(%q)=%v, want %v",
				name, got, wantNoDecimal)
		}
	}
}

// TestCapabilities_DateAndDateTimeDeclaredTogether states the asymmetry
// rule directly rather than by way of the predicate. `date` (epoch days)
// and `datetime` (epoch seconds) are both temporal ordinals carried as
// unsigned integers, and the analytics layer reads them through the same
// float64 channel. An operator that declares one and not the other is
// making a distinction the runtime does not make.
//
// Scoped to the analytics aggregators: this is deliberately NOT asserted
// over every capability table, because the window / attribute / feature
// families run their own narrower gate (predict_window.isNumericType)
// and closing that gap is a separate decision, not a doc fix.
func TestCapabilities_DateAndDateTimeDeclaredTogether(t *testing.T) {
	analytics := map[string]bool{
		string(types.AGG_SUM): true, string(types.AGG_AVERAGE): true,
		string(types.AGG_MIN): true, string(types.AGG_MAX): true,
		string(types.AGG_STDDEV): true, string(types.AGG_VARIANCE): true,
		string(types.AGG_RANGE): true, string(types.AGG_MEDIAN): true,
		string(types.AGG_PERCENTILE): true, string(types.AGG_ZSCORE): true,
		string(types.AGG_SKEWNESS): true, string(types.AGG_KURTOSIS): true,
		string(types.AGG_DISTINCT_SUM): true, string(types.AGG_WEIGHTED_MEAN): true,
		string(types.AGG_CI_LOWER): true, string(types.AGG_CI_UPPER): true,
	}
	for _, op := range aggregatorCapabilities() {
		if !analytics[op.Name] {
			continue
		}
		hasDate := slices.Contains(op.AcceptsTypes, "date")
		hasDateTime := slices.Contains(op.AcceptsTypes, "datetime")
		if hasDate != hasDateTime {
			t.Errorf("aggregator %s declares date=%v datetime=%v — both are temporal ordinals read through the same numeric channel",
				op.Name, hasDate, hasDateTime)
		}
	}
}

// TestCapabilities_VarianceStddevDeclareDecimal pins the decimal claim
// for the two aggregators that have a decimal implementation but used to
// declare the no-decimal list.
//
// processing/aggregator_decimal.go computes variance and stddev in
// decimal128 two-pass form (mean at max(scale, MinDecimalScale), then
// Σ(x−μ)² at twice that scale, with Decimal128.Sqrt for stddev) and
// drops to decimalVarianceFloat64 only when an intermediate would
// overflow — a fallback that reports FellBack so the orchestrator can
// warn PULSE_DECIMAL_PRECISION_LOSS. predict has always permitted the
// pairing (decimalSupportedAggregations). The manifest was the only
// surface claiming otherwise.
func TestCapabilities_VarianceStddevDeclareDecimal(t *testing.T) {
	decimal := encoding.FieldTypeDecimal128.String()
	want := map[string]bool{
		string(types.AGG_VARIANCE): true,
		string(types.AGG_STDDEV):   true,
	}
	seen := map[string]bool{}
	for _, op := range aggregatorCapabilities() {
		if !want[op.Name] {
			continue
		}
		seen[op.Name] = true
		if !slices.Contains(op.AcceptsTypes, decimal) {
			t.Errorf("aggregator %s does not declare %q, but predict permits it (decimalSupportedAggregations) and processing has a decimal implementation",
				op.Name, decimal)
		}
		if !decimalSupportedAggregations[types.AggregationType(op.Name)] {
			t.Errorf("aggregator %s declares %q but predict would warn PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL",
				op.Name, decimal)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("aggregator %s missing from aggregatorCapabilities()", name)
		}
	}
}

// TestManifest_CommandsIncludeWiden asserts `widen` reaches the
// LLM-bootstrap blob. The leaf is mounted in buildApp() and documented
// in docs/src/cli/flags.md, but commands() never listed it, so a client
// that treats the manifest as the command catalogue could not discover
// the only operation that changes a column's set rung in place.
func TestManifest_CommandsIncludeWiden(t *testing.T) {
	m := BuildManifest()
	for _, c := range m.Commands {
		if c.Name != "widen" {
			continue
		}
		if c.Description == "" {
			t.Error("widen command has an empty description")
		}
		if c.Annotations == (CommandAnnotations{}) {
			t.Error("widen command has empty annotations")
		}
		if c.Annotations.Streamable {
			t.Error("widen is a whole-file rewrite, not a streamable command")
		}
		return
	}
	t.Fatal("manifest commands do not include \"widen\"")
}
