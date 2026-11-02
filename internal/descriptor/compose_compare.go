package descriptor

import (
	"slices"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/regcompare"
)

// comparisonSummaries is predict's static view of the batch's
// nested-model comparisons (nil without any), read once they passed
// regcompare.Check — the resolved test, distribution and tested terms
// the runtime's results report.
func comparisonSummaries(plans []regcompare.Plan) []descriptor.ComparisonSummary {
	if len(plans) == 0 {
		return nil
	}
	out := make([]descriptor.ComparisonSummary, len(plans))
	for i, p := range plans {
		out[i] = descriptor.ComparisonSummary{
			Name:         p.Name,
			Restricted:   p.Restricted,
			Full:         p.Full,
			Test:         p.Test,
			Distribution: p.Distribution,
			Terms:        slices.Clone(p.Terms),
		}
	}
	return out
}
