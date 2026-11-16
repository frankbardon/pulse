package extend

import (
	"encoding/json"

	"github.com/frankbardon/pulse/types"
)

// PostFitFunc runs one ComposedRequest.PostFit entry of a post-fit
// operator (pulse.PostFitRegistration): a computation over a Compose
// batch's FINISHED fits that returns named scalars and named tables.
// It runs once per entry, after the batch's optimize blocks and before
// its nested-model comparisons.
//
// The operator reads no record: in is its whole world — the fits the
// entry names, the batch's slot results, every optimize block's
// solution and the entry's params. Every accessor returns a copy, so
// nothing it returns aliases a result, and in is valid only for the
// duration of the call.
//
// It must be deterministic. The output is held to the registration's
// declared Outputs on every run (PULSE_EXTENSION_POSTFIT_OUTPUT_MISMATCH,
// fatal). A returned error fails the entry alone: a *errors.CodedError
// keeps its code, any other error is PULSE_POSTFIT_FAILED; the entry
// keeps its label, type and error and warns PULSE_SLOT_FAILED (under
// Strict the batch fails with the entry's code). A panic is recovered
// as PROCESSING_PANIC, like any other extension panic at run time.
type PostFitFunc func(in PostFitInput) (PostFitOutput, error)

// PostFitInput is what a post-fit operator reads.
type PostFitInput interface {
	// Label is the entry's label.
	Label() string
	// Params is a copy of the entry's params object (nil when the entry
	// carries none). It has passed the registration's Params check.
	Params() json.RawMessage
	// Fits are the fit names the entry names, in its order.
	Fits() []string
	// Fit is the named fit's handle, nil for a name the entry does not
	// name. Every named fit ran successfully (an entry naming a failed
	// fit fails before the operator runs).
	Fit(name string) PostFitFit
	// Result reads one ungrouped aggregation value off a finished slot,
	// exactly as the result(slot, aggregation) expression reader does:
	// a finite number, else its PULSE_RESULT_INVALID refusal.
	Result(slot, aggregation string) (float64, error)
	// Optimizations are the batch's optimize block solutions, in
	// declaration order (empty without any).
	Optimizations() []PostFitSolution
}

// PostFitFit is a read-only handle on one finished regression fit, at
// full float64 precision (no `return` precision applies to it).
type PostFitFit interface {
	// Name is the fit's `fits` name; Type its regression operator;
	// Target its response field.
	Name() string
	Type() types.RegressionType
	Target() string
	// Coefficients are the fitted coefficients by term (the
	// intercept is "(intercept)"); StdErrors their standard errors
	// (absent terms report none).
	Coefficients() map[string]float64
	StdErrors() map[string]float64
	// Vcov is the coefficient covariance matrix when the fit computed
	// one (RegressionSpec.Vcov), nil otherwise.
	Vcov() *types.MatrixValues
	// AdstockChannels names the channels the fit carries an adstock
	// record for, sorted (empty for a fit without adstock).
	AdstockChannels() []string
	// Adstock is one channel's adstock record.
	Adstock(channel string) (PostFitAdstock, bool)
	// Draws are a REG_MMM_BAYES fit's raw posterior draws — present
	// only when the fit ran with params.return_draws.
	Draws() (PostFitDraws, bool)
}

// PostFitAdstock is a media-mix fit's adstock record for one channel.
type PostFitAdstock struct {
	// Kernel is geometric, delayed, weibull_cdf or weibull_pdf.
	Kernel string
	// Decay, Peak, Shape and Scale are the kernel's parameters; NaN
	// for one the kernel does not have.
	Decay, Peak, Shape, Scale float64
	// MaxLag is the last lag the kernel weighs.
	MaxLag int
	// Normalize reports weights divided by their sum.
	Normalize bool
	// Weights are the kernel's weights for lags 0..MaxLag.
	Weights []float64
}

// PostFitDraws are a fit's pooled posterior draws: Draws[j] holds every
// draw of Names[j], chains in order, all of one length (a null draw is
// NaN), and CredibleLevel is the level the fit reports at.
type PostFitDraws struct {
	Names         []string
	Draws         [][]float64
	CredibleLevel float64
}

// PostFitSolution is one optimize block's solution.
type PostFitSolution struct {
	// Name is the block's name.
	Name string
	// Failed reports a block that failed on its numbers; ErrorCode is
	// its code, and the figures below are zero.
	Failed    bool
	ErrorCode string
	// Objective is the objective at the optimum.
	Objective float64
	// Converged and Status are the solver's verdict.
	Converged bool
	Status    string
	// Variables are the decision variables at the optimum, by name.
	Variables map[string]float64
}

// PostFitOutput is what a post-fit operator returns: exactly the
// scalars and tables its registration's Outputs declares — every
// declared one present, nothing else. A scalar may be NaN (null on the
// wire). A table's Columns are its declared column names in order and
// each row holds one cell per column: a float64 in a float column, a
// string in a string column, or nil (null) in either.
type PostFitOutput struct {
	Scalars map[string]float64
	Tables  map[string]*types.PostFitTable
}

// PostFitColumnKind is a declared table column's cell kind.
type PostFitColumnKind string

const (
	// PostFitFloat cells are float64 (or nil).
	PostFitFloat PostFitColumnKind = "float"
	// PostFitString cells are strings (or nil).
	PostFitString PostFitColumnKind = "string"
)

// PostFitSchema declares a post-fit operator's output: its scalar names
// and its tables. Names are non-empty and unique within their list (a
// scalar and a table may not share one); at least one scalar or table.
type PostFitSchema struct {
	Scalars []string
	Tables  []PostFitTableSchema
}

// PostFitTableSchema declares one output table: its name and its
// columns (at least one, names unique).
type PostFitTableSchema struct {
	Name    string
	Columns []PostFitColumn
}

// PostFitColumn declares one table column.
type PostFitColumn struct {
	Name string
	Kind PostFitColumnKind
}
