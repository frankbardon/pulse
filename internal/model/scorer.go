package model

import (
	"fmt"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// scorer.go is the ONE scoring path every bound regression goes
// through (ATTR_REG_PREDICT): a RowScorer scores one row, a
// ScorerFactory builds a bound model's RowScorer for one scoring
// reference, and a ScorerSource resolves a regression type to its
// factory. The built-ins — REG_OLS, REG_GLM (inverse link on the
// response scale) and REG_BAYES_LINEAR (posterior mean) — are
// implementations like any other (regression.BuiltinScorer); a Bound
// never evaluates a predictor itself.
//
// This package stays a leaf: it declares the contract, the engine
// supplies the implementations.

// Row is the row a RowScorer reads: one numeric column by name, ok
// false when the column is null or absent on the row.
type Row interface {
	NumericValue(name string) (float64, bool)
}

// RowScorer scores rows against one bound model. It is immutable once
// built and safe for concurrent use.
type RowScorer interface {
	// Columns returns the columns Score reads, in the model's order.
	Columns() []string
	// Score is row's score; ok=false is a null score (a column it reads
	// is null or absent, or the reference has no figure to score with).
	Score(row Row) (float64, bool)
}

// ScorerFactory builds b's RowScorer for one scoring reference's
// params. The caller has run CheckScore on the reference.
type ScorerFactory func(b *Bound, p ScoreParams) (RowScorer, error)

// ScorerSource resolves a regression type to its ScorerFactory; false
// when the type has none (it cannot be scored).
type ScorerSource func(t types.RegressionType) (ScorerFactory, bool)

// Scorer builds b's RowScorer for p through src: b's type's factory, or
// PULSE_MODEL_SCORE_INVALID reason type_unsupported (CheckScore's
// refusal) when src resolves none.
func (b *Bound) Scorer(src ScorerSource, p ScoreParams) (RowScorer, error) {
	if b == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL, "Scorer called on a nil model")
	}
	var f ScorerFactory
	ok := false
	if src != nil {
		f, ok = src(b.Type)
	}
	if !ok || f == nil {
		return nil, typeUnsupported(b.Name, b.Type)
	}
	return f(b, p)
}

// typeUnsupported is the PULSE_MODEL_SCORE_INVALID refusal of a fit
// whose type has no scorer.
func typeUnsupported(fit string, t types.RegressionType) *errors.CodedError {
	return scoreInvalid(fit, ReasonTypeUnsupported,
		fmt.Sprintf("cannot score a %s fit; it scores least-squares, generalized-linear and Bayesian-linear fits", t),
		map[string]any{"type": string(t)})
}
