package regmmm

import (
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// Factories are the package's regression factories keyed by type: the
// root package's model-regression registrations read them (and the
// profile-dependency handler scan attributes each type to its factory
// through this map).
var Factories = map[types.RegressionType]extend.RegressionFactory{
	types.REG_MMM_BAYES: BayesFactory,
	types.REG_MMM_HILL:  Factory,
}

// Scorers are the package's row-scorer factories keyed by type: a model
// regression without one (REG_MMM_BAYES, whose adstocked prediction
// needs the history) is absent.
var Scorers = map[types.RegressionType]extend.RegressionScorerFactory{
	types.REG_MMM_HILL: Scorer,
}
