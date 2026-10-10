// Package model is the one bound-model namespace: the in-memory form of
// a fitted regression a request reads by name, the set those names
// resolve in, and the structural rules a ComposedRequest `fits` list
// must satisfy.
//
// It is a leaf over the standard library plus types and errors
// (TestModel_ImportBoundary), so the runtime (internal/service, the
// engine) and the no-execute predict path (internal/descriptor) share
// ONE rule set and ONE resolver — predict refuses exactly what the
// runtime refuses, with the same code, message and details.
package model

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// InterceptKey is the RegressionResult.Coefficients / StdErrors key of
// the intercept term (the engine's synthesized "(intercept)").
const InterceptKey = "(intercept)"

// Bound is one fitted regression bound under a name: everything a
// scorer needs, at full float64 precision, in the fit's own predictor
// order. Built from the engine's RegressionResult before any rounding
// (a `return` precision applies to the wire only, never to a Bound).
type Bound struct {
	// Name is the bound name (the `fits` entry name).
	Name string

	// Type is the regression operator that produced the fit.
	Type types.RegressionType

	// Target is the fit's response field.
	Target string

	// Predictors are the predictor columns in fit order: the spec's
	// predictors, or — under Selection — the predictors the selection
	// retained. The list is authoritative: a scorer never infers a
	// column the fit did not use (an absent one-hot column is the
	// reference level).
	Predictors []string

	// Terms are the predictor columns the fit was offered, in spec
	// order — Predictors without Selection, the candidate superset
	// under it. A scoring reference may name any of them in `terms` (a
	// candidate the selection dropped scores 0) and a scoring frame must
	// hold all of them, so predict, which sees only the spec, judges a
	// reference exactly as the runtime does.
	Terms []string

	// Intercept is the fitted intercept.
	Intercept float64

	// Coefficients are the fitted coefficients aligned with Predictors
	// (REG_BAYES_LINEAR: the posterior mean).
	Coefficients []float64

	// InterceptStdErr is the intercept's standard error (NaN when the
	// fit reports none).
	InterceptStdErr float64

	// StdErrors are the coefficients' standard errors aligned with
	// Predictors (NaN where the fit reports none).
	StdErrors []float64

	// Family and Link are the GLM error family and link ("" otherwise).
	Family string
	Link   string

	// Vcov is the coefficient covariance matrix when the fit computed
	// it (RegressionSpec.Vcov), nil otherwise.
	Vcov *types.MatrixValues

	// Result is the fit's full RegressionResult (shared, read-only).
	Result *types.RegressionResult
}

// Bind builds the Bound for a finished fit: spec is the regression as
// the fit ran it, res its result. A coefficient the result lacks for a
// fitted predictor is an engine invariant violation (PROCESSING_INTERNAL).
func Bind(name string, spec *types.RegressionSpec, res *types.RegressionResult) (*Bound, error) {
	if spec == nil || res == nil {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			fmt.Sprintf("fit %q produced no regression result", name),
			map[string]any{"name": name})
	}
	predictors := spec.Predictors
	if spec.Selection != "" {
		predictors = res.SelectedFeatures
	}
	predictors = append([]string(nil), predictors...)
	intercept, ok := res.Coefficients[InterceptKey]
	if !ok {
		return nil, missingCoefficient(name, InterceptKey)
	}
	b := &Bound{
		Name:            name,
		Type:            res.Type,
		Target:          spec.Target,
		Predictors:      predictors,
		Terms:           append([]string(nil), spec.Predictors...),
		Intercept:       intercept,
		Coefficients:    make([]float64, len(predictors)),
		InterceptStdErr: stdErr(res, InterceptKey),
		StdErrors:       make([]float64, len(predictors)),
		Family:          res.Family,
		Link:            res.Link,
		Vcov:            res.Vcov,
		Result:          res,
	}
	for i, p := range predictors {
		c, ok := res.Coefficients[p]
		if !ok {
			return nil, missingCoefficient(name, p)
		}
		b.Coefficients[i] = c
		b.StdErrors[i] = stdErr(res, p)
	}
	return b, nil
}

func stdErr(res *types.RegressionResult, key string) float64 {
	if v, ok := res.StdErrors[key]; ok {
		return v
	}
	return math.NaN()
}

func missingCoefficient(name, term string) error {
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
		fmt.Sprintf("fit %q has no coefficient for %q", name, term),
		map[string]any{"name": name, "term": term})
}

// Set is the request's bound-model namespace: every name a reference may
// resolve. The zero value and nil are empty sets. A Set is built once,
// before any slot runs, and only read afterwards — safe for concurrent
// readers.
type Set struct {
	byName map[string]*Bound
}

// Add binds b under b.Name; a name already bound is
// PULSE_MODEL_NAME_COLLISION.
func (s *Set) Add(b *Bound) error {
	if _, dup := s.byName[b.Name]; dup {
		return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_NAME_COLLISION,
			fmt.Sprintf("model name %q is bound twice", b.Name),
			map[string]any{"name": b.Name})
	}
	if s.byName == nil {
		s.byName = map[string]*Bound{}
	}
	s.byName[b.Name] = b
	return nil
}

// Lookup resolves name; an unbound name is PULSE_MODEL_UNKNOWN with the
// bound names under details.available. Nil-receiver-safe.
func (s *Set) Lookup(name string) (*Bound, error) {
	if s != nil {
		if b, ok := s.byName[name]; ok {
			return b, nil
		}
	}
	return nil, UnknownModel(name, s.Names())
}

// UnknownModel is the PULSE_MODEL_UNKNOWN refusal for name against the
// bound names available — the one constructor the runtime resolver and
// predict share.
func UnknownModel(name string, available []string) *errors.CodedError {
	if available == nil {
		available = []string{}
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_UNKNOWN,
		fmt.Sprintf("no fit or model named %q is bound in this request", name),
		map[string]any{"name": name, "available": available})
}

// Names returns the bound names, sorted. Nil-receiver-safe.
func (s *Set) Names() []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.byName))
	for n := range s.byName {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Len is the number of bound models. Nil-receiver-safe.
func (s *Set) Len() int {
	if s == nil {
		return 0
	}
	return len(s.byName)
}

// setKey carries a request's bound-model Set on a context.
type setKey struct{}

// WithSet returns ctx carrying set (a nil set leaves ctx unchanged).
func WithSet(ctx context.Context, set *Set) context.Context {
	if set == nil {
		return ctx
	}
	return context.WithValue(ctx, setKey{}, set)
}

// SetFrom returns the bound-model Set ctx carries, or nil.
func SetFrom(ctx context.Context) *Set {
	s, _ := ctx.Value(setKey{}).(*Set)
	return s
}
