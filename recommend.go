package pulse

import (
	"context"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/guide"
)

// Recommend turns a question kind into ranked draft requests. req.Intent
// is an intent-taxonomy ID (Intents); an unknown one is the coded error
// PULSE_RECOMMEND_INTENT_UNKNOWN, whose details.valid lists every ID.
//
// Without a cohort the answer is UNBOUND (Bound false): for each
// operator the instance offers that serves the intent, a request
// skeleton whose caller-supplied values are "<placeholder>" strings
// named after their wire keys, a why sentence from the operator's
// purpose, the intent's field shapes, and the alternatives and
// follow-ups its guidance declares. Nothing is predict-run. A
// non-analytic intent (prepare, simulate, lookup) or one no operator
// serves yet returns an empty Recommendations list plus RoutesTo, the
// tooling that answers it instead — that is not an error.
//
// Under a feature profile no hidden operator or capability appears in
// a recommendation, an alternative, a follow-up or a route. Recommend
// reads only in-memory guidance; ctx is accepted for symmetry with the
// cohort-bound mode.
func (p *Pulse) Recommend(ctx context.Context, req descriptor.RecommendRequest) (*descriptor.RecommendResult, error) {
	_ = ctx
	if req.Cohort != nil {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			"recommend: cohort-bound recommendations are not available yet; omit cohort for unbound skeletons",
			map[string]any{"field": "cohort"})
	}
	return guide.Recommend(p.svc.InstanceSnapshot(), req)
}
