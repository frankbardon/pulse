// Package targetencode is the one reading of FEAT_TARGET_ENCODE's
// params, shared by the operator (internal/processing/feature) and
// predict (internal/descriptor) so both refuse the same request with
// the same code and decide the same streaming verdict. It decides what
// a request MEANS and never averages an outcome.
//
// The params rules, in the order they are judged (after the params
// object decoded):
//
//   - target is required (the numeric outcome averaged per category);
//   - smoothing must be >= 0;
//   - fit_on, when present, must be a non-blank expression (its compile
//     is the field-reference walk's, the one every expression gets);
//   - folds is 0 (off) or an integer in [2, MaxFolds];
//   - rng_seed is read only with folds: alone it is refused, as it
//     would seed nothing.
//
// Every refusal is PROCESSING_CONFIG on both sides.
//
// Leaf: stdlib + the public errors / encoding packages + exprident.
package targetencode

import (
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprident"
)

// MaxFolds caps params.folds: the out-of-fold state is one per-category
// tally per fold.
const MaxFolds = 100

// Params is FEAT_TARGET_ENCODE's params wire shape.
//
//	{"target": "default", "smoothing": 10, "fit_on": "split == 0", "folds": 5, "rng_seed": 7}
type Params struct {
	// Target is the numeric outcome averaged per category.
	Target string `json:"target"`
	// Smoothing is the additive prior weight toward the global mean.
	Smoothing float64 `json:"smoothing"`
	// FitOn is a boolean expression choosing the training rows: only
	// they feed the category and global means; every row is encoded.
	// Empty fits on every row (the leakage-prone default).
	FitOn string `json:"fit_on"`
	// Folds, when >= 2, encodes each training row out-of-fold: from the
	// training rows outside its own fold.
	Folds int `json:"folds"`
	// RngSeed seeds the fold assignment.
	RngSeed *int64 `json:"rng_seed"`
}

// Seed is the fold-assignment seed (0 when unset).
func (p Params) Seed() int64 {
	if p.RngSeed == nil {
		return 0
	}
	return *p.RngSeed
}

// Check returns the first params rule p breaks, or nil. The message is
// the operator's own ("FEAT_TARGET_ENCODE: ..."), raised verbatim by the
// constructor and by predict.
func Check(p Params) *errors.CodedError {
	refuse := func(msg string) *errors.CodedError {
		return errors.NewCodedError(errors.PROCESSING_CONFIG, "FEAT_TARGET_ENCODE: "+msg)
	}
	switch {
	case p.Target == "":
		return refuse("'target' is required in params")
	case p.Smoothing < 0:
		return refuse("'smoothing' must be >= 0")
	case p.FitOn != "" && strings.TrimSpace(p.FitOn) == "":
		return refuse("'fit_on' must be a non-blank boolean expression (omit it to fit on every row)")
	case p.Folds < 0 || p.Folds == 1 || p.Folds > MaxFolds:
		return refuse("'folds' must be 0 (off) or an integer from 2 to " + strconv.Itoa(MaxFolds) + "; got " + strconv.Itoa(p.Folds))
	case p.RngSeed != nil && p.Folds == 0:
		return refuse("'rng_seed' seeds the fold assignment and is only valid with 'folds'")
	}
	return nil
}

// Streamable reports whether a FEAT_TARGET_ENCODE over field with p can
// run on the streaming feature drive. Its pre-pass reads field, target
// and every name fit_on reads from the RAW record, before any feature
// has emitted, so each must be a stored schema field: a name an earlier
// feature derives (a split column, a log) is absent there, and the
// request routes buffered, where features apply in order. A fit_on that
// does not parse, or reads $env, is judged unstreamable too. A nil
// schema knows no field.
func Streamable(field string, p Params, schema *encoding.Schema) bool {
	stored := func(name string) bool { return schema != nil && schema.Field(name) != nil }
	if field != "" && !stored(field) {
		return false
	}
	if p.Target != "" && !stored(p.Target) {
		return false
	}
	if p.FitOn == "" {
		return true
	}
	names, usesEnv, parsed := exprident.Free(p.FitOn)
	if !parsed || usesEnv {
		return false
	}
	for _, n := range names {
		if !stored(n) {
			return false
		}
	}
	return true
}
