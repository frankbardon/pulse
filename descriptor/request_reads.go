package descriptor

// ReadsRoot names the request root a read map was taken over: the four
// document kinds an analysis is written as.
type ReadsRoot string

const (
	// ReadsRootRequest is a single Request (Process).
	ReadsRootRequest ReadsRoot = "request"
	// ReadsRootComposed is a ComposedRequest (Compose).
	ReadsRootComposed ReadsRoot = "composed"
	// ReadsRootChain is a ChainRequest (ProcessChain).
	ReadsRootChain ReadsRoot = "chain"
	// ReadsRootFacet is a FacetRequest (Facet).
	ReadsRootFacet ReadsRoot = "facet"
)

// RequestReads is a request document's read map: for every component
// slot that reads data, where it sits and what it reads — taken from
// the document alone, with no instance run, no cohort and no compile.
// An editor drawing a wire from each component to the columns and
// request parameters it depends on, a linter flagging a slot that
// reads a column no step produces, or an agent planning which columns
// a draft analysis needs reads the map instead of re-deriving Pulse's
// slot list and expression scoping.
type RequestReads struct {
	// Root is the document kind the map was taken over.
	Root ReadsRoot `json:"root"`
	// Slots are the reading slots, sorted by Path (array indices
	// numerically) then Key. Never nil.
	Slots []SlotReads `json:"slots"`
}

// SlotReads is what one slot of a request document reads.
//
// Path and Key locate the value in the document's JSON shape: Path is
// the component holding the slot (`filterers[0]`, `matrices[0]`,
// `requests[2].filterers[0]`, `optimize[0]`; "" for a slot on the
// document root or on a root request) and Key the dotted key path to
// the value inside it (`expression`, `field`, `params.group`,
// `weight.field`, `covariance.cluster`). A list-valued slot (`fields`,
// `predictors`, `partition_by`, a window's or test's `order_by`, a
// join's `on`) is one entry under the list's key.
//
// Columns are names the slot resolves against the request's column
// namespace, in first-read order without repeats: a cohort field, a
// label an earlier step computes (a feature, an attribute, a window, an
// aggregation), or a vector name (a `vector` slot, or an expression
// naming one). Which of these a name is, is the caller's resolution: a
// name an ATTR_FORMULA labels `period` read by a MAT_SEM
// `params.group` is the column that attribute computes. An inline
// `fields` entry is listed as written, a glob entry included.
type SlotReads struct {
	// Path is the JSON path of the component holding the slot.
	Path string `json:"path"`
	// Key is the dotted key path of the value inside the component.
	Key string `json:"key"`
	// Request is the JSON path of the request whose `params` the slot
	// resolves `params.<name>` against ("" for the document root — a
	// Request's, a Compose batch's, a chain's or a facet's own
	// `params`): a Compose sub-request, fit request or banner request,
	// or a chain stage's request, inherits the root's `params` beneath
	// its own.
	Request string `json:"request"`
	// Columns are the column-namespace names the slot reads. Never nil.
	Columns []string `json:"columns"`
	// Params are the request-parameter names the slot reads
	// (`params.<name>` in an expression, or a `"params.<name>"` optimize
	// bound). Never nil.
	Params []string `json:"params"`
	// Variables are the names an optimize objective or constraint reads:
	// the block's decision variables, never columns (no record field is
	// in reach of an optimize block).
	Variables []string `json:"variables,omitempty"`
	// Funcs are the functions an expression slot calls by name, as
	// ExprReads.Funcs.
	Funcs []string `json:"funcs,omitempty"`
	// UsesEnv reports an expression slot's use of $env; Columns is then
	// a lower bound, as ExprReads.UsesEnv.
	UsesEnv bool `json:"uses_env,omitempty"`
	// Error is the slot's own failure to read — an expression that does
	// not parse (PULSE_FORMULA_COMPILE_ERROR), a value of the wrong
	// JSON shape (SERVICE_VALIDATION, details.path / key) — with
	// Columns and Params empty. Nil when the slot read cleanly. One bad
	// slot never fails the map.
	Error *EnvelopeEntry `json:"error,omitempty"`
}
