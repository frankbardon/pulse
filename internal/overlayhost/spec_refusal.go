package overlayhost

import (
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// The Ref / Scope rule of every crosstab (MATRIX host) kind on
// Request.Overlays, shared by predict (internal/descriptor) and the one
// runtime MATRIX dispatcher (internal/processing applyOverlaysPlanned —
// the fused and buffered crosstab exits, a Compose slot's own
// Request.Overlays and a ProcessChain stage's all fold through it), so
// both raise the same code, message and details. Before this table the
// runtime re-checked neither for most kinds: a spec predict refused
// (a set Ref on an implicit-margin kind, an unsupported scope, an
// unknown margin axis) ran silently in Process.

// refRule is what a crosstab kind demands of OverlaySpec.Ref.
type refRule int

const (
	// refEmpty: every Ref arm must be unset (implicit-margin and
	// intra-matrix kinds read the host table inline).
	refEmpty refRule = iota
	// refMargin: Ref.Margin must be set and name a known MarginAxis
	// (the axis-margin family), and every other Ref arm must be unset —
	// no margin kind reads one, so a populated arm would be silently
	// ignored.
	refMargin
	// refPrior: Ref.Prior or an empty Ref, Ref.Prior.Lag 0 — the
	// inferential prior-period kinds on a no-column crosstab, judged
	// exactly as their SERIES rule (seriesRules, seriesPrior) so one
	// spec answers the same on both hosts.
	refPrior
)

// specRule is one kind's Ref / Scope rule.
type specRule struct {
	ref refRule
	// why completes the refEmpty message's parenthesis.
	why string
	// scopes is the supported scope set, in message order.
	scopes []types.OverlayScope
}

var (
	scopeMatrix    = []types.OverlayScope{types.OverlayScopeMatrix}
	scopeRow       = []types.OverlayScope{types.OverlayScopeRow}
	scopeColumn    = []types.OverlayScope{types.OverlayScopeColumn}
	scopeCell      = []types.OverlayScope{types.OverlayScopeCell}
	scopeRowColumn = []types.OverlayScope{types.OverlayScopeRow, types.OverlayScopeColumn}
	scopeGroup     = []types.OverlayScope{types.OverlayScopeGroup}
)

const pairwiseWhy = "intra-matrix pairwise: the test compares two slots of the host matrix along the scope axis"

// specRules is the per-kind rule table. Every Process MATRIX kind with a
// Ref / Scope contract has a row; OVERLAY_FORMULA has none (it carries
// no Ref / Scope contract on either side).
var specRules = map[types.OverlayKind]specRule{
	types.OverlayKindChiSqMatrix:     {refEmpty, "implicit-margin: the χ² test uses the host's row / column / grand margins inline", scopeMatrix},
	types.OverlayKindChiSqRow:        {refEmpty, "implicit-margin: the per-row χ² test uses the host's row / column / grand margins inline", scopeRow},
	types.OverlayKindChiSqCol:        {refEmpty, "implicit-margin: the per-column χ² test uses the host's row / column / grand margins inline", scopeColumn},
	types.OverlayKindFisherExactCell: {refEmpty, "implicit-margin: the per-cell 2×2 Fisher's exact test reads the host's row + column margins inline", scopeCell},
	types.OverlayKindStdResidual:     {refEmpty, "implicit-margin: the adjusted residuals read the table's own row / column / grand totals", scopeCell},
	types.OverlayKindCorrespondence:  {refEmpty, "implicit-margin: the analysis reads the whole table", scopeMatrix},
	types.OverlayKindMarkov:          {refEmpty, "implicit-margin: the chain reads the whole table", scopeMatrix},
	types.OverlayKindRake:            {refEmpty, "implicit-margin: the fit reads the whole table", scopeMatrix},
	types.OverlayKindSymmetry:        {refEmpty, "implicit-margin: the test reads the whole table", scopeMatrix},
	types.OverlayKindRetention:       {refEmpty, "implicit-margin: the rates read the whole table", scopeRow},

	types.OverlayKindPairwiseProbitT:           {refEmpty, pairwiseWhy, scopeRowColumn},
	types.OverlayKindPairwisePropZ:             {refEmpty, pairwiseWhy, scopeRowColumn},
	types.OverlayKindPairwiseTwoMeansZ:         {refEmpty, pairwiseWhy, scopeRowColumn},
	types.OverlayKindPairwiseWeightedTwoMeansZ: {refEmpty, pairwiseWhy, scopeRowColumn},
	types.OverlayKindPairwiseWelchT:            {refEmpty, pairwiseWhy, scopeRowColumn},

	types.OverlayKindDeltaVsMargin:  {refMargin, "", scopeCell},
	types.OverlayKindIndexVsMargin:  {refMargin, "", scopeCell},
	types.OverlayKindShareOfRow:     {refMargin, "", scopeCell},
	types.OverlayKindShareOfCol:     {refMargin, "", scopeCell},
	types.OverlayKindShareOfTotal:   {refMargin, "", scopeCell},
	types.OverlayKindZScoreVsMargin: {refMargin, "", scopeCell},

	types.OverlayKindTVsPrior:     {refPrior, "", scopeGroup},
	types.OverlayKindZVsPrior:     {refPrior, "", scopeGroup},
	types.OverlayKindPropZVsPrior: {refPrior, "", scopeGroup},
}

// lockedMarginAxes is the one MarginAxis an axis-locked refMargin kind
// accepts; a refMargin kind absent here accepts any known axis.
// OVERLAY_SHARE_OF_TOTAL is grand-axis by definition but documents its
// axis as ignored, so it stays unlocked.
var lockedMarginAxes = map[types.OverlayKind]types.MarginAxis{
	types.OverlayKindShareOfRow: types.MarginAxisRow,
	types.OverlayKindShareOfCol: types.MarginAxisColumn,
}

// knownMarginAxes is the MarginAxis set a refMargin kind accepts.
var knownMarginAxes = map[types.MarginAxis]bool{
	types.MarginAxisRow:    true,
	types.MarginAxisColumn: true,
	types.MarginAxisGrand:  true,
}

// CrosstabSpecKinds returns every kind CrosstabSpecRefusal judges, in no
// particular order (tests pin it against the MATRIX handler table).
func CrosstabSpecKinds() []types.OverlayKind {
	out := make([]types.OverlayKind, 0, len(specRules))
	for k := range specRules {
		out = append(out, k)
	}
	return out
}

// CrosstabSpecScopes returns the scopes kind supports on a crosstab host
// (nil for a kind without a rule).
func CrosstabSpecScopes(kind types.OverlayKind) []types.OverlayScope {
	return append([]types.OverlayScope(nil), specRules[kind].scopes...)
}

// CrosstabSpecMarginAxis returns the one MarginAxis an axis-locked
// refMargin kind accepts (OVERLAY_SHARE_OF_ROW → row, OVERLAY_SHARE_OF_COL
// → column), or "" when the kind accepts any known axis or has no rule.
func CrosstabSpecMarginAxis(kind types.OverlayKind) types.MarginAxis {
	return lockedMarginAxes[kind]
}

// CrosstabSpecRefusal is the Ref / Scope refusal of the crosstab (MATRIX
// host) spec at index of Request.Overlays (index < 0 leaves it out of
// details). route is the kind every decision keys on (the empty route of
// a hidden kind has no rule); spec.Kind is what message and details
// name. nil when the spec passes or route has no rule. Judged in order:
//
//   - refEmpty kinds: any Ref arm set → PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE
//     {index, kind}, "must leave Ref empty (<why>)".
//   - refMargin kinds: Ref.Margin unset → PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE
//     {index, kind}; any other Ref arm set beside it (Sibling, Prior,
//     Stage, Matrix, …) → the same code, {index, kind}; an unknown
//     Margin.Axis → the same code, {index, kind, axis};
//     on an axis-locked kind (OVERLAY_SHARE_OF_ROW / _COL) any other
//     known axis → the same code, {index, kind, axis}.
//   - a scope outside the kind's set → PULSE_OVERLAY_SCOPE_UNSUPPORTED
//     {index, kind, scope}, "(supports: a, b)".
//
// OVERLAY_SHARE_OF_TOTAL's row is its MATRIX dispatch only; its SERIES
// dispatch keeps its own rule, so call this on a crosstab host only.
func CrosstabSpecRefusal(spec *types.OverlaySpec, route types.OverlayKind, index int) *errors.CodedError {
	rule, ok := specRules[route]
	if !ok || spec == nil {
		return nil
	}
	kind := string(spec.Kind)
	details := func(extra ...string) map[string]any {
		d := map[string]any{"kind": kind}
		if index >= 0 {
			d["index"] = index
		}
		for i := 0; i+1 < len(extra); i += 2 {
			d[extra[i]] = extra[i+1]
		}
		return d
	}
	switch rule.ref {
	case refPrior:
		// The SERIES rule verbatim: same code, message and details on
		// both hosts.
		return SeriesSpecRefusal(spec, route, index)
	case refEmpty:
		if spec.Ref != (types.OverlayRef{}) {
			return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
				"overlay "+kind+" must leave Ref empty ("+rule.why+")", details())
		}
	case refMargin:
		if spec.Ref.Margin == nil {
			return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
				"overlay "+kind+" requires Ref.Margin (axis-margin reference)", details())
		}
		other := spec.Ref
		other.Margin = nil
		if other != (types.OverlayRef{}) {
			return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
				"overlay "+kind+" reads only Ref.Margin (axis-margin reference); leave every other Ref arm empty", details())
		}
		if axis := spec.Ref.Margin.Axis; !knownMarginAxes[axis] {
			return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
				"overlay "+kind+" Ref.Margin.Axis is not a known MarginAxis: "+string(axis), details("axis", string(axis)))
		}
		if axis := spec.Ref.Margin.Axis; lockedMarginAxes[route] != "" && axis != lockedMarginAxes[route] {
			return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
				"overlay "+kind+" Ref.Margin.Axis must be "+string(lockedMarginAxes[route])+" (the kind is "+string(lockedMarginAxes[route])+"-axis-locked), got: "+string(axis), details("axis", string(axis)))
		}
	}
	for _, s := range rule.scopes {
		if spec.Scope == s {
			return nil
		}
	}
	names := make([]string, len(rule.scopes))
	for i, s := range rule.scopes {
		names[i] = string(s)
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED,
		"overlay "+kind+" does not support scope "+string(spec.Scope)+" (supports: "+strings.Join(names, ", ")+")",
		details("scope", string(spec.Scope)))
}
