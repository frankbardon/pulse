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
// predict-run. With a Cohort it is BOUND: drafts over the cohort's own
// fields, each predict-validated (header, schema and sidecar only —
// never a record). Fields are role hints and take effect only with a
// Cohort: each must name a cohort field whose kind the intent takes,
// and pins the first role it fits. Level, when set, ranks operators at
// that level first; otherwise the simplest suitable operator ranks
// first. Limit caps the recommendations (0 = the default, 10).
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
// Bound is true when a Cohort was given. Truncated reports that Limit
// cut the list; CandidatesConsidered is how many recommendations
// survived (bound: passed predict) before the cut.
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
// wire JSON: every value the caller must supply is a "<placeholder>"
// string (or a one-element list of one) named after its wire key,
// listed in Placeholders. Bound is true only for a cohort-bound draft
// with nothing left to supply, which passed predict as written; a
// bound-mode draft that still needs a value only the caller can choose
// (a success value, a reference mean, a model family) has its fields
// bound, Bound false and Needs naming each such parameter. Advisories
// are the predict advisories the draft raises over the cohort.
// Alternatives come from the operator's not-for guidance and FollowUps
// from its follow-up guidance; neither names an operator or capability
// the instance hides.
type Recommendation struct {
	Operator     string          `json:"operator"`
	Category     string          `json:"category"`
	Level        Level           `json:"level,omitempty"`
	Why          string          `json:"why"`
	Bound        bool            `json:"bound"`
	Request      json.RawMessage `json:"request"`
	Placeholders []string        `json:"placeholders,omitempty"`
	Needs        []RecommendNeed `json:"needs,omitempty"`
	Advisories   []Advisory      `json:"advisories,omitempty"`
	Alternatives []Alternative   `json:"alternatives,omitempty"`
	FollowUps    []Alternative   `json:"follow_ups,omitempty"`
}

// RecommendNeed is one parameter a draft cannot fill from the schema:
// Param is its wire key, Why says what to supply.
type RecommendNeed struct {
	Param string `json:"param"`
	Why   string `json:"why"`
}
