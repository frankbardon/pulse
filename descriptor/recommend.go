package descriptor

import (
	"encoding/json"

	"github.com/frankbardon/pulse/types"
)

// RecommendRequest asks Recommend which analyses answer a question of
// one kind. Intent is an intent-taxonomy ID (Intents); an unknown ID is
// the coded error PULSE_RECOMMEND_INTENT_UNKNOWN.
//
// Without a Cohort the result is UNBOUND: shape-level request
// skeletons whose field slots hold "<placeholder>" strings, never
// predict-run. Fields (role hints) take effect only with a Cohort.
// Level, when set, ranks operators at that level first; otherwise the
// simplest suitable operator ranks first. Limit caps the
// recommendations (0 = the default, 10).
type RecommendRequest struct {
	Intent string        `json:"intent"`
	Cohort *types.Cohort `json:"cohort,omitempty"`
	Fields []string      `json:"fields,omitempty"`
	Level  Level         `json:"level,omitempty"`
	Limit  int           `json:"limit,omitempty"`
}

// RecommendResult is Recommend's answer. Recommendations is ranked,
// best first, and is an empty array (never null) when nothing on the
// instance serves the intent. RoutesTo then names the tooling that
// answers the question instead — a non-analytic intent (prepare,
// simulate, lookup) or an intent no operator serves yet. Shapes are the
// intent's field shapes: the roles a request's fields must fill.
// Truncated reports that Limit cut the list; CandidatesConsidered is
// how many recommendations were built before the cut.
type RecommendResult struct {
	Intent               string           `json:"intent"`
	Bound                bool             `json:"bound"`
	Shapes               []Shape          `json:"shapes,omitempty"`
	Recommendations      []Recommendation `json:"recommendations"`
	RoutesTo             []Alternative    `json:"routes_to,omitempty"`
	Truncated            bool             `json:"truncated"`
	CandidatesConsidered int              `json:"candidates_considered"`
}

// Recommendation is one ranked draft. Request is the draft request as
// wire JSON: unbound, every value the caller must supply is a
// "<placeholder>" string (or a one-element list of one) named after its
// wire key, listed in Placeholders. Needs names the parameters only the
// caller can choose. Alternatives come from the operator's not-for
// guidance and FollowUps from its follow-up guidance; neither names an
// operator or capability the instance hides.
type Recommendation struct {
	Operator     string          `json:"operator"`
	Category     string          `json:"category"`
	Level        Level           `json:"level,omitempty"`
	Why          string          `json:"why"`
	Bound        bool            `json:"bound"`
	Request      json.RawMessage `json:"request"`
	Placeholders []string        `json:"placeholders,omitempty"`
	Needs        []RecommendNeed `json:"needs,omitempty"`
	Alternatives []Alternative   `json:"alternatives,omitempty"`
	FollowUps    []Alternative   `json:"follow_ups,omitempty"`
}

// RecommendNeed is one parameter a draft cannot fill from the schema:
// Param is its wire key, Why says what to supply.
type RecommendNeed struct {
	Param string `json:"param"`
	Why   string `json:"why"`
}
