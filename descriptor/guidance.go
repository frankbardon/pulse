package descriptor

// Guided-analysis metadata model.
//
// These types describe WHAT a registered operator is for and HOW to read
// what it returns, in terms a non-statistician recognises. They are pure
// declarations: nothing in the engine reads them to execute a request.
// Two registries project from them — the closed intent taxonomy (the
// kinds of question an analysis can answer, served by pulse.Intents and
// the manifest's top-level intents list) and the glossary (served by
// pulse.Glossary) — plus a per-operator Purpose whose intent IDs ride
// each manifest entry's intents list.

// Domain is an audience domain a Purpose's use cases are written for.
// The set is closed: DomainSurvey, DomainOps, DomainScience and
// DomainHarness.
type Domain string

// The audience domains a Purpose.UseCases map may be keyed by.
const (
	// DomainSurvey is survey and market-research analysis: ratings,
	// segments, waves, weighted respondents.
	DomainSurvey Domain = "survey"
	// DomainOps is operational and business analytics: orders,
	// channels, funnels, service metrics.
	DomainOps Domain = "ops"
	// DomainScience is scientific and experimental analysis:
	// measurements, treatments, trials.
	DomainScience Domain = "science"
	// DomainHarness is automated callers — agent harnesses and
	// pipelines that author requests programmatically.
	DomainHarness Domain = "harness"
)

// Level grades how much statistical background an operator assumes, so
// guidance can prefer simpler tools first.
type Level string

// The levels a Purpose may declare.
const (
	// LevelBasic needs no statistics background (totals, counts, means).
	LevelBasic Level = "basic"
	// LevelIntermediate assumes familiarity with common tests and
	// their assumptions.
	LevelIntermediate Level = "intermediate"
	// LevelAdvanced assumes modelling background (regressions,
	// multivariate methods).
	LevelAdvanced Level = "advanced"
)

// FieldKind is the coarse family of a cohort field type, the vocabulary
// intent shapes are written in. Every registered field type maps to
// exactly one kind.
type FieldKind string

// The coarse field kinds.
const (
	// FieldKindNumeric covers the unsigned integer, float and
	// decimal128 field types.
	FieldKindNumeric FieldKind = "numeric"
	// FieldKindCategorical covers the categorical_* field types.
	FieldKindCategorical FieldKind = "categorical"
	// FieldKindDate covers the date and datetime field types.
	FieldKindDate FieldKind = "date"
	// FieldKindBool covers the packed_bool field type.
	FieldKindBool FieldKind = "bool"
	// FieldKindSet covers the set_* multi-select field types.
	FieldKindSet FieldKind = "set"
)

// RoleUnbounded is the Role.Max value meaning "no upper bound on how
// many fields may fill this role".
const RoleUnbounded = -1

// Role is one named slot in an intent Shape — "outcome", "group",
// "measures" — with the field kinds that may fill it and how many
// fields it takes.
type Role struct {
	// Name identifies the role within its shape (e.g. "outcome").
	Name string `json:"name"`
	// Kinds lists the field kinds that may fill the role; at least one.
	Kinds []FieldKind `json:"kinds"`
	// Min is the fewest fields the role takes; 0 marks it optional.
	Min int `json:"min"`
	// Max is the most fields the role takes, at least Min, or
	// RoleUnbounded for no upper bound.
	Max int `json:"max"`
}

// Shape is one data shape an intent applies to: the set of roles a
// cohort's fields must fill for the intent's question to be answerable.
// An intent may declare several alternative shapes.
type Shape struct {
	// Roles lists the shape's roles in authoring order.
	Roles []Role `json:"roles"`
}

// Intent is one entry in the closed intent taxonomy: a kind of question
// a non-statistician recognises in their own words.
type Intent struct {
	// ID is the stable identifier operator purposes and examples cite
	// (e.g. "compare_groups").
	ID string `json:"id"`
	// Label is a short human-readable name.
	Label string `json:"label"`
	// Analytic is true for a question answered by operators; false for
	// an intent that routes to tooling instead (prepare, simulate,
	// lookup).
	Analytic bool `json:"analytic"`
	// Sounds lists example phrasings of the question.
	Sounds []string `json:"sounds"`
	// Shapes lists the alternative data shapes the intent applies to.
	Shapes []Shape `json:"shapes"`
}

// Alternative is one "not for" or "follow-up" entry in a Purpose: the
// situation in plain words, and the operator or feature it points to.
type Alternative struct {
	// When describes the situation in plain words.
	When string `json:"when"`
	// Use names the better choice: a bare registered operator name
	// (e.g. "TEST_WELCH") or a "<kind>:<name>" feature spelling with
	// kind one of capability, operator, io_format or mcp_extra.
	Use string `json:"use"`
}

// Purpose is an operator's plain-language guidance: what it is for, the
// intents it answers, when to reach for something else, and what it
// assumes.
type Purpose struct {
	// Plain is the one sentence a developer reads first.
	Plain string `json:"plain"`
	// Intents lists the intent IDs the operator answers; at least one.
	Intents []string `json:"intents"`
	// Questions lists natural-language questions the operator answers.
	Questions []string `json:"questions"`
	// UseCases maps an audience domain to a one-line use case.
	UseCases map[Domain]string `json:"use_cases,omitempty"`
	// NotFor lists the situations where another choice fits better.
	NotFor []Alternative `json:"not_for,omitempty"`
	// FollowUps lists the natural next steps once the operator has run:
	// when to take one, and what to run. Optional; each Use resolves
	// exactly as a NotFor Use does.
	FollowUps []Alternative `json:"follow_ups,omitempty"`
	// Assumptions lists the operator's assumptions as plain sentences.
	Assumptions []string `json:"assumptions,omitempty"`
	// Level grades the background the operator assumes.
	Level Level `json:"level,omitempty"`
	// Glossary lists the glossary term IDs the guidance relies on.
	Glossary []string `json:"glossary,omitempty"`
}

// Band is one labelled interval of an output value, half-open
// [Min, Max). A nil Min or Max is an open end.
type Band struct {
	// Min is the inclusive lower bound; nil means unbounded below.
	Min *float64 `json:"min,omitempty"`
	// Max is the exclusive upper bound; nil means unbounded above.
	Max *float64 `json:"max,omitempty"`
	// Label names the band (e.g. "small", "moderate").
	Label string `json:"label"`
}

// Interpretation says how to read one output field of an operator's
// result.
type Interpretation struct {
	// Field is the normalised output path (e.g. "statistic",
	// "p_value", "details.effect_size.cohens_d").
	Field string `json:"field"`
	// Means says what the value measures, in plain words.
	Means string `json:"means,omitempty"`
	// Bands labels value ranges; Convention is required when set.
	Bands []Band `json:"bands,omitempty"`
	// Abs is true when Bands apply to the absolute value.
	Abs bool `json:"abs,omitempty"`
	// Convention attributes the Bands (e.g. "Cohen (1988)") — bands are
	// a labelled convention, never authoritative.
	Convention string `json:"convention,omitempty"`
	// Sign maps "+" / "-" to what each direction means.
	Sign map[string]string `json:"sign,omitempty"`
	// Caveats lists plain-language warnings about the value.
	Caveats []string `json:"caveats,omitempty"`
	// Shared names a shared rule set this field follows instead of
	// declaring its own (e.g. "p-value").
	Shared string `json:"shared,omitempty"`
}

// Term is one glossary entry.
type Term struct {
	// ID is the stable identifier purposes and other terms cite
	// (e.g. "p-value").
	ID string `json:"id"`
	// Short is a one-sentence definition.
	Short string `json:"short"`
	// WhyCare says why the reader should care, in plain words.
	WhyCare string `json:"why_care,omitempty"`
	// SeeAlso lists related term IDs.
	SeeAlso []string `json:"see_also,omitempty"`
	// Jargon marks a term that must be glossary-linked wherever it
	// appears in plain-language guidance.
	Jargon bool `json:"jargon,omitempty"`
	// Forms lists the surface spellings that count as the term
	// appearing in prose (e.g. "p-value", "p value").
	Forms []string `json:"forms,omitempty"`
}
