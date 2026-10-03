// Package mergegate is the one pure mergeability rule shared by the
// engine and the no-execute validators. processing.CanMergeRequest*
// (the parallel shard / decode reducers' gate) and
// processing.ChainRefusal (ProcessChain's per-stage gate) call it with
// an adapter over the runtime ExtensionRegistry; the chain validator in
// internal/descriptor calls it with an adapter over the read-only
// ExtensionsSnapshot. Both therefore refuse the same requests with the
// same code, message and details.
//
// The package imports only types, encoding and errors so that both
// sides can reach it: internal/descriptor may not import
// internal/processing (TestPredictNoExecutionImports), and the engine
// does not import the descriptor layer.
package mergegate

import (
	"fmt"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Extensions answers the embedder-registered half of the rule. Each
// method reports ok=false for a name that is not a registered
// extension of that category, in which case the built-in per-type
// facts in package types decide. An implementation may also answer
// ok=true, false for a built-in name its instance hides, which refuses
// it exactly as an unregistered name is refused. An implementation must be safe to
// call on its zero / nil value (no extensions).
type Extensions interface {
	// Aggregator reports an extension aggregator's DECLARED Mergeable
	// flag.
	Aggregator(name string) (mergeable, ok bool)
	// Grouper reports an extension grouper's DECLARED Mergeable flag.
	Grouper(name string) (mergeable, ok bool)
	// Filterer reports an extension filterer's streamability.
	Filterer(name string) (streamable, ok bool)
	// Attribute reports whether an extension attribute is row-local
	// (streamable and not two-pass).
	Attribute(name string) (rowLocal, ok bool)
}

// None is the Extensions value for a built-in-only gate.
type None struct{}

func (None) Aggregator(string) (bool, bool) { return false, false }
func (None) Grouper(string) (bool, bool)    { return false, false }
func (None) Filterer(string) (bool, bool)   { return false, false }
func (None) Attribute(string) (bool, bool)  { return false, false }

// MergeRefusal returns "" when req's online state is mergeable across
// input partitions, else the reason it is not. A request merges iff it
// has at least one aggregator; no windows, features, regressions,
// tests or post-tests; only row-local attributes (ATTR_FORMULA,
// ATTR_DATE_PART, row_local extensions); only mergeable groupers
// (built-in Mergeable() or a declared extension); only streamable
// filterers; and only mergeable aggregators, none of them a built-in
// one over a decimal128 field (the built-in decimal fold is
// buffered-only — an extension aggregator reads decimals itself).
// schema may be nil (the decimal check is then skipped); a nil ext is
// None.
func MergeRefusal(req *types.Request, schema *encoding.Schema, ext Extensions) string {
	if ext == nil {
		ext = None{}
	}
	if req == nil {
		return "the request is nil"
	}
	if len(req.Aggregations) == 0 {
		return "it has no aggregator"
	}
	if len(req.Windows) > 0 || len(req.Features) > 0 ||
		len(req.Regressions) > 0 || len(req.Tests) > 0 ||
		len(req.PostTests) > 0 {
		return "windows, features, tests, post-tests and regressions are excluded"
	}
	for _, attr := range req.Attributes {
		if attr == nil {
			return "an attribute slot is nil"
		}
		// The extension answer is consulted FIRST, like every other
		// category below: an adapter may claim a built-in name (an
		// instance feature set reports a hidden one as known-and-not
		// row-local), and it must then refuse exactly as an
		// unregistered name does rather than pass on the built-in list.
		rowLocal, ok := ext.Attribute(string(attr.Type))
		if !ok {
			switch attr.Type {
			case types.ATTR_FORMULA, types.ATTR_DATE_PART:
				rowLocal = true
			}
		}
		if rowLocal {
			continue
		}
		return fmt.Sprintf("attribute %s is not row-local (two-pass and buffered attributes are excluded)", attr.Type)
	}
	for _, g := range req.Groups {
		if g == nil {
			return "a group slot is nil"
		}
		mergeable, ok := ext.Grouper(string(g.Type))
		if !ok {
			mergeable = g.Type.Mergeable()
		}
		if !mergeable {
			return fmt.Sprintf("grouper %s is not mergeable", g.Type)
		}
	}
	for _, f := range req.Filterers {
		if f == nil {
			return "a filterer slot is nil"
		}
		streamable, ok := ext.Filterer(string(f.Type))
		if !ok {
			streamable = f.Type.Streamable()
		}
		if !streamable {
			return fmt.Sprintf("filterer %s is not streamable", f.Type)
		}
	}
	for _, agg := range req.Aggregations {
		if agg == nil {
			return "an aggregation slot is nil"
		}
		mergeable, extension := ext.Aggregator(string(agg.Type))
		if !extension {
			mergeable = agg.Type.Mergeable()
		}
		if !mergeable {
			return fmt.Sprintf("aggregator %s is not mergeable", agg.Type)
		}
		if extension || schema == nil {
			continue
		}
		if f := schema.Field(agg.Field); f != nil && f.Type.IsDecimal() {
			return fmt.Sprintf("aggregator %s targets decimal128 field %q (the built-in decimal fold is buffered-only)", agg.Type, agg.Field)
		}
	}
	return ""
}

// ChainRefusal is the v1 ProcessChain stage gate: nil when req passes
// MergeRefusal and every aggregator emits one scalar per output row
// (EmitsScalar: AGG_MODE is excluded), else a PULSE_CHAIN_NOT_MERGEABLE "chain stage is not mergeable:
// <reason>" with details {stage_index, stage_name}.
func ChainRefusal(req *types.Request, schema *encoding.Schema, ext Extensions, stageIndex int, stageName string) error {
	reason := MergeRefusal(req, schema, ext)
	if reason == "" {
		for _, agg := range req.Aggregations {
			if !EmitsScalar(agg.Type) {
				reason = fmt.Sprintf("aggregator %s emits a non-scalar value", agg.Type) + nonScalarNote(ext)
				break
			}
		}
	}
	if reason == "" {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_CHAIN_NOT_MERGEABLE,
		"chain stage is not mergeable: "+reason,
		map[string]any{"stage_index": stageIndex, "stage_name": stageName})
}

// EmitsScalar reports whether aggregator t's Finalize produces a
// single float64 cell a downstream chain stage can read: every
// mergeable aggregator except those in nonScalarAggregators.
// AGG_MODE_COUNT is admitted — it emits the modal count, one float64
// per row, and its per-value count partials merge exactly — and so is
// AGG_FREQUENCY, whose one value's row count is two summed counters.
func EmitsScalar(t types.AggregationType) bool {
	for _, n := range nonScalarAggregators {
		if t == n {
			return false
		}
	}
	return true
}

// nonScalarAggregators are the mergeable built-ins the chain gate
// refuses as non-scalar.
var nonScalarAggregators = []types.AggregationType{types.AGG_MODE}

// hider is the optional half of Extensions an instance-scoped adapter
// implements: Hidden reports a built-in name the instance does not
// offer, so refusal prose can leave it out.
type hider interface {
	Hidden(name string) bool
}

// nonScalarNote is the chain refusal's parenthetical naming the
// non-scalar aggregators — only those the instance offers (the
// refusal never names a hidden operator). With every one offered it is
// " (AGG_MODE is excluded)".
func nonScalarNote(ext Extensions) string {
	h, _ := ext.(hider)
	var named []string
	for _, t := range nonScalarAggregators {
		if h == nil || !h.Hidden(string(t)) {
			named = append(named, string(t))
		}
	}
	switch len(named) {
	case 0:
		return ""
	case 1:
		return " (" + named[0] + " is excluded)"
	}
	return " (" + strings.Join(named[:len(named)-1], ", ") + " and " + named[len(named)-1] + " are excluded)"
}

// StageJoinRefusal is the ProcessChain rule that only stage 0 may
// join: nil for stage 0 or a stage without Joins, else a
// PULSE_CHAIN_STAGE_JOIN with details {count, stage, stage_name}. A
// later stage reads the previous stage's output rows, not a cohort, so
// there is no left side to join — the runtime used to drop the slot
// silently.
func StageJoinRefusal(req *types.Request, stageIndex int, stageName string) error {
	if stageIndex == 0 || req == nil || len(req.Joins) == 0 {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_CHAIN_STAGE_JOIN,
		"only chain stage 0 may carry Joins; a later stage reads the previous stage's output",
		map[string]any{"count": len(req.Joins), "stage": stageIndex, "stage_name": stageName})
}
