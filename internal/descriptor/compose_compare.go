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
			Unresolved:   unresolvedSummary(p.Unresolved),
		}
	}
	return out
}

// unresolvedSummary is a sweep-bound plan's undecided part on the wire
// (nil when the plan resolved over every sweep slot).
func unresolvedSummary(u *regcompare.Unresolved) *descriptor.ComparisonUnresolved {
	if u == nil {
		return nil
	}
	out := &descriptor.ComparisonUnresolved{Slots: u.Slots, Varies: slices.Clone(u.Varies)}
	for _, r := range u.Refused {
		out.Refused = append(out.Refused, descriptor.ComparisonSlotRefusal{Slot: r.Slot, Label: r.Label, Reason: r.Reason})
	}
	return out
}
