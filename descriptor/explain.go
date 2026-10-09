package descriptor

import (
	"github.com/frankbardon/pulse/types"
)

// ExplainDetail selects how much an Explain result says. The empty
// value is ExplainTerse.
type ExplainDetail string

const (
	// ExplainTerse (the default) returns the one-sentence summary plus
	// the structured parts: steps, findings, inferred defaults,
	// advisories and caveats.
	ExplainTerse ExplainDetail = "terse"
	// ExplainFull adds the narrative sentences, the glossary terms the
	// operators link and their declared follow-ups.
	ExplainFull ExplainDetail = "full"
)

// ExplainMode is what an Explain result describes.
type ExplainMode string

const (
	// ExplainModeRequest describes what a request will do, before it
	// runs.
	ExplainModeRequest ExplainMode = "request"
	// ExplainModeResponse reads what a response found.
	ExplainModeResponse ExplainMode = "response"
)

// ExplainRoot names which root an Explain result describes. Request
// mode's spellings are the request-template targets; response mode's
// name the result type read.
type ExplainRoot string

const (
	ExplainRootRequest  ExplainRoot = "request"
	ExplainRootComposed ExplainRoot = "composed"
	ExplainRootChain    ExplainRoot = "chain"
	ExplainRootFacet    ExplainRoot = "facet"
	ExplainRootSample   ExplainRoot = "sample"
	// ExplainRootResponse is response mode over a Process Response.
	ExplainRootResponse ExplainRoot = "response"
	// ExplainRootComposedResponse is response mode over a Compose
	// ComposedResponse.
	ExplainRootComposedResponse ExplainRoot = "composed_response"
	// ExplainRootChainResponse is response mode over a ProcessChain
	// ChainResponse.
	ExplainRootChainResponse ExplainRoot = "chain_response"
	// ExplainRootFacetResult is response mode over a Facet FacetResult.
	ExplainRootFacetResult ExplainRoot = "facet_result"
)

// ExplainRequest asks Explain to describe a request, or read a
// response, in plain words. Detail is terse (the default) or full; any
// other value is SERVICE_VALIDATION.
//
// Request mode: exactly one request root is set — Request, Composed,
// Chain, Facet or Sample; none, or more than one, is
// SERVICE_VALIDATION. Request mode never runs the request. When the
// root names a cohort, Explain predicts it — header, schema and sidecar
// only, never a record — to name the operators smart defaults would
// infer and the advisories the request raises.
//
// Response mode: exactly one result root is set — Response,
// ComposedResponse, ChainResponse or FacetResult — and the request
// that produced it may ride beside it as its companion: Request beside
// a Response, Composed beside a ComposedResponse, Chain beside a
// ChainResponse, Facet beside a FacetResult. Two result roots, or any
// other request root beside a result, is SERVICE_VALIDATION. A result
// alone does not say which operator produced an aggregation or which
// field weighted it, so without the companion those are described by
// count only and a caveat says the reading is partial. A ChainResponse
// that echoes its normalized_request needs no companion: the echo is
// read first. A FacetResult's overlays are tied to the field they
// decorate through the companion's overlay params (or the result's
// only field). Response mode reads no cohort.
type ExplainRequest struct {
	Request  *types.Request         `json:"request,omitempty"`
	Composed *types.ComposedRequest `json:"composed,omitempty"`
	Chain    *types.ChainRequest    `json:"chain,omitempty"`
	Facet    *types.FacetRequest    `json:"facet,omitempty"`
	Sample   *types.SampleRequest   `json:"sample,omitempty"`
	Response *types.Response        `json:"response,omitempty"`

	ComposedResponse *types.ComposedResponse `json:"composed_response,omitempty"`
	ChainResponse    *types.ChainResponse    `json:"chain_response,omitempty"`
	FacetResult      *types.FacetResult      `json:"facet_result,omitempty"`

	Detail ExplainDetail `json:"detail,omitempty"`
}

// ExplainResult is Explain's answer. Summary is one sentence. Findings
// is always an array (empty in request mode); every other list is
// omitted when empty. Sentences, GlossaryRefs and FollowUps appear only
// under ExplainFull.
//
// Request mode: Steps lists what the request does, slot by slot.
// DefaultsApplied names each operator smart defaults inferred (Path is
// predict's, prefixed by the slot's place in a Compose or chain root),
// Advisories the fit-for-purpose notes predict raises. Valid is
// predict's verdict — nil when nothing was checked (no cohort, or a
// Sample root) — and Refusals predict's errors as it reported them
// (details.slot names a Compose slot). Caveats say what Explain could
// not check, which codes predict refuses with and, in full detail, the
// assumptions the request's tests and models make.
//
// Response mode: Findings reads each test, post-test, regression,
// overlay layer, matrix and aggregation the response reports, and
// Summary counts them with the run's record counts. A ComposedResponse
// is read slot by slot (each finding's Slot under "responses[i].") and
// then its batch overlays; a ChainResponse stage by stage
// ("stages[i].") and then its whole-chain overlays; a FacetResult field
// by field ("fields.<name>", "additive.<name>"), each followed by the
// overlay layers that decorate it. The many-tests caveat counts the
// uncorrected p-values of the whole result, across slots, stages,
// facet fields and overlays. Valid, Steps,
// DefaultsApplied, Advisories and Refusals stay empty. Caveats carry
// what the reading cannot vouch for — uncorrected p-values from more
// than one test, the alpha a regression or overlay was read at, a
// partial reading without the request — and stay in terse output.
//
// Under a feature profile no hidden operator or capability is named:
// a step naming one says the instance does not offer it, and every
// sentence passes the instance's prose scrub.
type ExplainResult struct {
	Mode            ExplainMode      `json:"mode"`
	Root            ExplainRoot      `json:"root"`
	Detail          ExplainDetail    `json:"detail"`
	Summary         string           `json:"summary"`
	Valid           *bool            `json:"valid,omitempty"`
	Steps           []ExplainStep    `json:"steps,omitempty"`
	Findings        []ExplainFinding `json:"findings"`
	DefaultsApplied []DefaultApplied `json:"defaults_applied,omitempty"`
	Advisories      []Advisory       `json:"advisories,omitempty"`
	Refusals        []EnvelopeEntry  `json:"refusals,omitempty"`
	Sentences       []string         `json:"sentences,omitempty"`
	GlossaryRefs    []string         `json:"glossary_refs,omitempty"`
	FollowUps       []Alternative    `json:"follow_ups,omitempty"`
	Caveats         []string         `json:"caveats,omitempty"`
}

// ExplainStep is one thing a request does. Slot is its wire path
// ("aggregations[0]", "requests[1].tests[0]", "weight"). Operator is
// the operator the slot runs — the inferred one when Defaulted — and
// is empty when the slot names none or names one the instance hides.
// Fields are the cohort fields the step reads. Text says what the step
// does, from the slot's structure and the operator's plain purpose.
type ExplainStep struct {
	Slot      string   `json:"slot"`
	Operator  string   `json:"operator,omitempty"`
	Fields    []string `json:"fields,omitempty"`
	Defaulted bool     `json:"defaulted,omitempty"`
	Text      string   `json:"text"`
}

// Verdict is a finding's closed reading. A non-significant result is
// "no evidence of" a difference, an association or an effect — never
// evidence of none.
type Verdict string

const (
	VerdictEvidenceOfDifference    Verdict = "evidence_of_difference"
	VerdictNoEvidenceOfDifference  Verdict = "no_evidence_of_difference"
	VerdictEvidenceOfAssociation   Verdict = "evidence_of_association"
	VerdictNoEvidenceOfAssociation Verdict = "no_evidence_of_association"
	VerdictEvidenceOfEffect        Verdict = "evidence_of_effect"
	VerdictNoEvidenceOfEffect      Verdict = "no_evidence_of_effect"
	VerdictDescriptive             Verdict = "descriptive"
	VerdictNotComputable           Verdict = "not_computable"
)

// Verdicts returns the closed verdict set in declaration order.
func Verdicts() []Verdict {
	return []Verdict{
		VerdictEvidenceOfDifference, VerdictNoEvidenceOfDifference,
		VerdictEvidenceOfAssociation, VerdictNoEvidenceOfAssociation,
		VerdictEvidenceOfEffect, VerdictNoEvidenceOfEffect,
		VerdictDescriptive, VerdictNotComputable,
	}
}

// ExplainFinding is one result a response reports, read in plain
// terms. Subject names what was measured; Operator is the operator
// that produced it — empty when the response alone cannot say (an
// aggregation read without its request) or the instance hides it.
// StrengthBand appears only when the figure's Interpretation has
// bands, and then always with Convention, the banding's source.
// Numbers carries the figures the verdict rests on, keyed by their
// Interpretation path where one exists ("statistic", "p_value",
// "details.effect_size.cohens_d") and by component key otherwise
// ("n", "n_null"); an undefined figure is null. Multiplicity names the
// correction an adjusted verdict used.
//
// The verdict reads the operator's own alpha (TestResult.alpha, never
// an assumed 0.05) and, when a correction ran, significant_adjusted
// instead of the raw p-value. A regression and an uncorrected overlay
// carry no alpha of their own: they are read against 0.05 and a caveat
// says so. A result whose p-value is undefined is not_computable.
//
// Slot is the wire path of the result read ("tests[0]",
// "regressions[0].coefficients.hours", "responses[1].overlays[0]",
// "stages[2].aggregations[0]", "fields.region"); an aggregation is
// named by its request slot.
type ExplainFinding struct {
	Slot         string               `json:"slot,omitempty"`
	Subject      string               `json:"subject"`
	Operator     string               `json:"operator,omitempty"`
	Verdict      Verdict              `json:"verdict"`
	StrengthBand string               `json:"strength_band,omitempty"`
	Convention   string               `json:"convention,omitempty"`
	Numbers      map[string]*float64  `json:"numbers"`
	Multiplicity *ExplainMultiplicity `json:"multiplicity,omitempty"`
}

// ExplainMultiplicity is the multiple-comparison correction behind an
// adjusted verdict: the method, the family it pooled and the family
// size m.
type ExplainMultiplicity struct {
	Method string `json:"method"`
	Family string `json:"family"`
	M      int    `json:"m"`
}
