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

// Scorers are the package's row-scorer factories keyed by type.
// REG_MMM_BAYES scores pre-adstocked channels over its draws
// (bayes_score.go).
var Scorers = map[types.RegressionType]extend.RegressionScorerFactory{
	types.REG_MMM_BAYES: BayesScorer,
	types.REG_MMM_HILL:  Scorer,
}

// BoundScorers are the scorers that read ATTR_REG_PREDICT's params.bound
// (the whole model's lower / upper), keyed by type: REG_MMM_BAYES's
// credible bound of its mean prediction. The registration adapter
// prefers one over the type's Scorers entry.
var BoundScorers = map[types.RegressionType]func(extend.FittedRegression, string) (extend.RowScorer, error){
	types.REG_MMM_BAYES: BayesBoundScorer,
}
