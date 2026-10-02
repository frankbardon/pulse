package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/types"
)

// The isExtension* helpers report whether a name resolves through the
// embedder-registered ExtensionsSnapshot reachable via PredictOptions.
// Predict consults them after the built-in is-known check so a custom
// operator does not surface as "unknown type" at validation time.
//
// All helpers are nil-receiver-safe: opts may be nil, opts.Extensions
// may be nil, and the snapshot's per-category slices may be empty.

func isExtensionWindowType(opts *PredictOptions, t types.WindowType) bool {
	snap := extensionsFromOpts(opts)
	if snap == nil {
		return false
	}
	return snapshotHasName(snap.Windows, string(t))
}

func isExtensionFeatureType(opts *PredictOptions, t types.FeatureType) bool {
	snap := extensionsFromOpts(opts)
	if snap == nil {
		return false
	}
	return snapshotHasName(snap.Features, string(t))
}

func isExtensionTestType(opts *PredictOptions, t types.TestType) bool {
	snap := extensionsFromOpts(opts)
	if snap == nil {
		return false
	}
	return snapshotHasName(snap.Tests, string(t))
}

// attributeTwoPass reports whether an attribute takes the runtime's
// two-pass streaming drive: the built-in two-pass set (mirrors
// processing.requiresTwoPass, which predict cannot import) or an
// extension attribute whose snapshot Mode is two_pass. Two-pass
// attributes do not compose with groupers, features, regressions or
// tier-1 tests on the streaming path, so every such gate in
// computeStreamable asks this one helper.
func attributeTwoPass(opts *PredictOptions, t types.AttributeType) bool {
	switch t {
	case types.ATTR_ZSCORE, types.ATTR_TSCORE, types.ATTR_NORMALIZED,
		types.ATTR_REG_FITTED, types.ATTR_REG_RESIDUAL, types.ATTR_REG_LEVERAGE:
		return true
	}
	snap := extensionsFromOpts(opts)
	if snap == nil {
		return false
	}
	for _, m := range snap.Attributes {
		if m.Name == string(t) {
			return m.Mode == "two_pass"
		}
	}
	return false
}

// firstTwoPassAttribute returns the first attribute on req that takes
// the two-pass drive, or "" when there is none.
func firstTwoPassAttribute(req *types.Request, opts *PredictOptions) types.AttributeType {
	for _, attr := range req.Attributes {
		if attr != nil && attributeTwoPass(opts, attr.Type) {
			return attr.Type
		}
	}
	return ""
}

// streamableWithOverlay reports whether (category, name) is streamable,
// preferring the extensions overlay when it carries an entry for the
// name and falling back to the built-in `builtin` value otherwise.
// Used by computeStreamable so a custom-registered streamable operator
// does not falsely surface as non-streamable.
func streamableWithOverlay(opts *PredictOptions, category, name string, builtin bool) bool {
	if v, ok := extensionStreamable(opts, category, name); ok {
		return v
	}
	return builtin
}

// extensionStreamable consults the snapshot's per-category Streamable
// flags. Returns (streamable, true) when the name is overlay-known
// and (false, false) otherwise so the caller can fall back to the
// built-in type method.
func extensionStreamable(opts *PredictOptions, category, name string) (bool, bool) {
	snap := extensionsFromOpts(opts)
	if snap == nil {
		return false, false
	}
	var metas []descriptor.OperatorMeta
	switch category {
	case "aggregator":
		metas = snap.Aggregators
	case "attribute":
		metas = snap.Attributes
	case "filterer":
		metas = snap.Filterers
	case "grouper":
		metas = snap.Groupers
	case "window":
		metas = snap.Windows
	case "feature":
		metas = snap.Features
	case "test":
		metas = snap.Tests
	default:
		return false, false
	}
	for _, m := range metas {
		if m.Name == name {
			return m.Streamable, true
		}
	}
	return false, false
}
