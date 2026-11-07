package vectors

import (
	"encoding/json"

	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// ExtInputs returns the column names extension operator name of
// category ("aggregator") declares it reads for params raw — its
// registration's FieldInputs hook. Nil for a built-in name, an
// operator without a hook, or a hook that panics. Predict wraps the
// read-only extensions snapshot (descriptor.ExtensionsSnapshot), the
// runtime its extension registry (processing.ExtensionRegistry): both
// hand the hook the same params, an expression-capable param's
// expression replaced by the number 0 (extscalar.StandIns).
type ExtInputs func(category, name string, raw json.RawMessage) []string

// Classifier is the instance view the windows-first classification
// reads — the one value every site asking "do the windows run over the
// records?" holds (WindowsFirst, WindowsOverRecords, ComputedColumns
// and every resolver over it, PostWindowAttributes,
// RegressionWindowReads, WindowsFirstReads), so predict and the runtime
// classify a request alike. The zero value is the extension-free,
// model-free, identity-route view.
type Classifier struct {
	// Route is the instance's view of an attribute type (nil: the
	// identity) — a hidden type routes to "" and moves nowhere. Escape
	// analysis sees a Classifier as one location, so Route reaches the
	// heap with Cat: hand it a func built once, never a per-call
	// closure.
	Route func(types.AttributeType) types.AttributeType
	// Cat is the bound models an ATTR_REG_PREDICT or params.fit
	// attribute reads its terms from (nil: none).
	Cat model.Catalog
	// Ext is the extension FieldInputs hooks (nil: none): an extension
	// aggregator naming a window label through one reads that label
	// exactly as a built-in's field or column param does.
	Ext ExtInputs
}
