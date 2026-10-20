package extend

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// Regression is the value a RegressionFactory returns: a fresh model
// for one regression slot of one request. It implements one of the two
// forms below, matching the registration's Streamable declaration —
// StreamingRegression when it declares Streamable: true,
// BufferedRegression when it declares Streamable: false (implementing
// both is fine). pulse.New probes the factory and refuses a value that
// does not implement the declared form with
// PULSE_EXTENSION_STREAMABLE_MISMATCH.
//
// The engine owns row admission: a model sees only the rows whose
// target and every predictor (RegressionSpec.Target / .Predictors) are
// present — listwise deletion happens before the model — and, on a
// weighted slot of a WeightAware registration, only rows whose weight
// is valid and positive, each reporting it through Record.Weight. The
// engine counts those rows (n_obs) and writes the weighted floor
// (sum_weights, n_eff); the model never does.
type Regression interface{}

// StreamingRegression is the single-pass form: UpdateRow once per
// admitted row, then Finalize once. Finalize MUST be safe to call with
// no prior UpdateRow (the empty-input case; return a coded
// PROCESSING_REGRESSION_INSUFFICIENT_DATA to fail the slot alone).
//
// Merge is required: it folds other — a model built by the SAME factory
// from the SAME spec, the embedder's own value, so a type assertion to
// the concrete type succeeds — into the receiver, which then finalizes
// the merged state. A streaming value without Merge is not a
// StreamingRegression and is refused at pulse.New. Regressions run on
// the serial arm today, so the engine does not call Merge yet; the
// requirement keeps the contract open for a parallel fold without a
// breaking change.
//
// When the request cannot stream (a grouper, a feature, a two-pass
// attribute, a Resample / Selection modifier or a robust covariance on
// the slot) the engine still drives a StreamingRegression: it feeds the
// buffered rows through UpdateRow, in record order, then Finalize.
type StreamingRegression interface {
	UpdateRow(rec Record) error
	Merge(other StreamingRegression) error
	Finalize() (RegressionOutput, error)
}

// BufferedRegression is the buffered form: Fit sees every admitted row
// at once and may iterate over them as often as it needs (an iterative
// or nonlinear fit). rows is valid only for the duration of the call.
type BufferedRegression interface {
	Fit(rows Rows) (RegressionOutput, error)
}

// RegressionFactory builds a fresh Regression for one regression slot
// of one request from the slot's spec and the cohort schema. During
// probe-validation at pulse.New it is called against a small synthetic
// schema (an f64 target "probe_y" and predictors "probe_x1",
// "probe_x2", plus a weight column) and must tolerate it.
type RegressionFactory func(spec *types.RegressionSpec, schema *encoding.Schema) (Regression, error)

// RegressionOutput is what a regression model returns: a narrow result
// the engine maps onto types.RegressionResult. Every engine-owned
// figure — the slot's name and type, n_obs, the weighted floor
// (sum_weights, n_eff), multiplicity-adjusted p-values, advisories and
// a per-slot failure — is the engine's; the model returns only what it
// estimated.
type RegressionOutput struct {
	// Coefficients is the fitted coefficient table, in the order the
	// model reports it (a vcov's row / column order). At least one is
	// required; each needs a non-empty Name, listed once. A nonlinear
	// parameter is a coefficient too, named by the model.
	Coefficients []RegressionCoefficient

	// Fit carries optional whole-fit statistics from the fixed set.
	Fit RegressionFit

	// Vcov is the coefficient covariance, square over Coefficients in
	// their order. It is read only when the registration declares Vcov
	// and the spec sets vcov: true — then it is required — and is the
	// result's vcov (its correlation is derived). Otherwise it is
	// ignored.
	Vcov [][]float64

	// Outputs holds the model's own named extras (types.RegressionResult
	// Outputs). Every key must be declared in the registration's
	// OutputSchema; an undeclared key is
	// PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH. A non-finite float is
	// null on the wire. Nil adds none.
	Outputs map[string]any
}

// RegressionCoefficient is one coefficient: its estimate and, when the
// model computes them, its standard error, test statistic and p-value.
// A nil figure is absent: it is JSON null on the wire, its key kept, so
// the coefficient table stays rectangular. Only a coefficient carrying
// a p-value joins a multiplicity family. Figure builds a value.
type RegressionCoefficient struct {
	Name      string
	Estimate  float64
	StdErr    *float64
	Statistic *float64
	PValue    *float64
}

// RegressionFit is the optional whole-fit statistics, each from the
// fixed set types.RegressionResult carries. A nil figure is not
// reported (absent on the wire, like a built-in that does not compute
// it); a non-finite one is null.
type RegressionFit struct {
	R2             *float64
	AdjR2          *float64
	ResidualStdErr *float64
	DF             *float64
	Deviance       *float64
	NullDeviance   *float64
	PseudoR2       *float64
	// Iterations is an iterative fit's iteration count
	// (converged_iters); zero is not reported.
	Iterations int
}

// Figure returns a pointer to v, for the optional figures of
// RegressionCoefficient and RegressionFit.
func Figure(v float64) *float64 { return &v }

// ScoreRow is the row a RowScorer reads: one numeric column by name,
// ok false when the column is null or absent on the row.
type ScoreRow interface {
	NumericValue(name string) (float64, bool)
}

// RowScorer scores rows against one fitted regression model — what
// ATTR_REG_PREDICT evaluates per row. It must be immutable once built
// and safe for concurrent use: one scorer serves every worker.
type RowScorer interface {
	// Columns returns the columns Score reads, each one of the fit's
	// predictors (a scorer reading any other column is refused with
	// PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH, reason
	// scorer_columns), so predict checks the scoring frame exactly as
	// the runtime reads it.
	Columns() []string
	// Score is row's score; ok=false is a null score (a column it reads
	// is null or absent on row, or the model has no figure for it).
	Score(row ScoreRow) (float64, bool)
}

// FittedRegression is what a RegressionScorerFactory builds a scorer
// from: the fitted model as a saved model file keeps it, so a fit and
// the same fit reloaded from its file hand the factory equal values and
// score identically.
type FittedRegression struct {
	// Type is the regression model's registered name.
	Type types.RegressionType
	// Target is the fit's response field.
	Target string
	// Predictors are the fit's predictor columns, in spec order.
	Predictors []string
	// Coefficients are the fitted estimates, keyed by the coefficient
	// names RegressionOutput.Coefficients carried.
	Coefficients map[string]float64
	// Outputs are the fit's RegressionOutput.Outputs in their JSON form
	// — a number is a float64, an array []any, an object
	// map[string]any, a non-finite float nil — the form a saved model
	// file reads back. Nil when the fit filled none. A figure the
	// scorer needs beyond the coefficients (a slot param it read, say)
	// belongs here: params are not part of a saved model.
	Outputs map[string]any
}

// RegressionScorerFactory builds a fitted model's RowScorer
// (RegressionRegistration.Scorer). It reads only fit — never the
// request or the cohort — so a saved model scores on a later request
// from its file alone. A returned error fails the scoring slot's
// request.
type RegressionScorerFactory func(fit FittedRegression) (RowScorer, error)
