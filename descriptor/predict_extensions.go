package descriptor

import (
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
	var metas []OperatorMeta
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
