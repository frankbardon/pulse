package regordinal

import (
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// Factories are the package's regression factories keyed by type: the
// model-regression wiring (internal/regadapt) reads them, and the
// profile-dependency handler scan attributes each type to its factory
// through this map.
var Factories = map[types.RegressionType]extend.RegressionFactory{
	types.REG_ORDINAL: Factory,
}
