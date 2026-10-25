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

	// InterceptCILower / InterceptCIUpper are the intercept's
	// confidence bounds and CILower / CIUpper the coefficients' aligned
	// with Predictors, at ConfLevel (RegressionResult.CILower /
	// CIUpper, full precision); NaN where the fit reports none.
	InterceptCILower float64
	InterceptCIUpper float64
	CILower          []float64
	CIUpper          []float64

	// ConfLevel is the intervals' coverage (0 when the fit reports no
	// intervals).
	ConfLevel float64

	// Family and Link are the GLM error family and link ("" otherwise).
	Family string
	Link   string

	// Vcov is the coefficient covariance matrix when the fit computed
	// it (RegressionSpec.Vcov), nil otherwise.
	Vcov *types.MatrixValues

	// Result is the fit's full RegressionResult (shared, read-only).
	Result *types.RegressionResult

	// Source is where the name was bound: a `fits` entry or a `models`
	// entry (the zero value when unknown). A name collision names both
	// bindings' sources.
	Source Source
}

// Bind builds the Bound for a finished fit: spec is the regression as
// the fit ran it, res its result. A coefficient a built-in's result
// lacks for the intercept or a fitted predictor is an engine invariant
// violation (PROCESSING_INTERNAL); an extension regression's (a type
// outside types.AllRegressionTypes) is NaN, its own scorer reading
// Result.Coefficients by its own names.
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
	// An extension regression names its own coefficients (a nonlinear
	// parameter is one): it need not have an intercept or one
	// coefficient per predictor — its Scorer reads Result.Coefficients
	// itself. A missing figure is NaN; a built-in's is an invariant
	// violation.
	extension := !BuiltinRegression(res.Type)
	intercept, ok := res.Coefficients[InterceptKey]
	if !ok {
		if !extension {
			return nil, missingCoefficient(name, InterceptKey)
		}
		intercept = math.NaN()
	}
	b := &Bound{
		Name:             name,
		Type:             res.Type,
		Target:           spec.Target,
		Predictors:       predictors,
		Terms:            append([]string(nil), spec.Predictors...),
		Intercept:        intercept,
		Coefficients:     make([]float64, len(predictors)),
		InterceptStdErr:  stdErr(res, InterceptKey),
		StdErrors:        make([]float64, len(predictors)),
		InterceptCILower: lookupOrNaN(res.CILower, InterceptKey),
		InterceptCIUpper: lookupOrNaN(res.CIUpper, InterceptKey),
		CILower:          make([]float64, len(predictors)),
		CIUpper:          make([]float64, len(predictors)),
		ConfLevel:        res.ConfLevel,
		Family:           res.Family,
		Link:             res.Link,
		Vcov:             res.Vcov,
		Result:           res,
	}
	for i, p := range predictors {
		c, ok := res.Coefficients[p]
		if !ok {
			if !extension {
				return nil, missingCoefficient(name, p)
			}
			c = math.NaN()
		}
		b.Coefficients[i] = c
		b.StdErrors[i] = stdErr(res, p)
		b.CILower[i] = lookupOrNaN(res.CILower, p)
		b.CIUpper[i] = lookupOrNaN(res.CIUpper, p)
	}
	return b, nil
}

func stdErr(res *types.RegressionResult, key string) float64 {
	return lookupOrNaN(res.StdErrors, key)
}

// lookupOrNaN reads a per-term result map; an absent term is NaN.
func lookupOrNaN(m map[string]float64, key string) float64 {
	if v, ok := m[key]; ok {
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
	// vectors are the bound vector fits (vector.go), in the same
	// namespace: a name is bound as one kind or the other.
	vectors map[string]*VectorFit
	// sems are the bound SEM fits (sem.go), in the same namespace.
	sems map[string]*SEMFit
	// capture receives the vector fit a fit request's battery closes
	// (WithCapture); nil on every other set.
	capture *VectorCapture
	// records are the resolved {record} comparison references
	// (record.go), keyed by vectors.ComparisonReference.RecordKey —
	// not names: they share no namespace with the fits.
	records map[string]RecordBinding
	// failed are the Compose fits that failed on their data
	// (failed.go): bound names with a Signature and no model.
	failed map[string]failedFit
}

// Add binds b under b.Name; a name already bound is
// PULSE_MODEL_NAME_COLLISION.
func (s *Set) Add(b *Bound) error {
	if s.bound(b.Name) {
		return nameCollision(b.Name)
	}
	if s.byName == nil {
		s.byName = map[string]*Bound{}
	}
	s.byName[b.Name] = b
	return nil
}

// Lookup resolves name to a bound regression; an unbound name is
// PULSE_MODEL_UNKNOWN with the bound regressions under
// details.available, a vector fit's name PULSE_MODEL_KIND_MISMATCH.
// Nil-receiver-safe.
func (s *Set) Lookup(name string) (*Bound, error) {
	if s != nil {
		if b, ok := s.byName[name]; ok {
			return b, nil
		}
		if _, ok := s.vectors[name]; ok {
			return nil, KindMismatch(name, types.FitKindRegression, types.FitKindVector)
		}
		if _, ok := s.sems[name]; ok {
			return nil, KindMismatch(name, types.FitKindRegression, types.FitKindSEM)
		}
		if f, ok := s.failed[name]; ok {
			if k := f.sig.EffectiveKind(); k != types.FitKindRegression {
				return nil, KindMismatch(name, types.FitKindRegression, k)
			}
			return nil, FailedFitRefusal(name, f.err)
		}
	}
	return nil, UnknownModel(name, s.RegressionNames())
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

// Names returns every bound name, both kinds, sorted — the one
// namespace (RegressionNames / VectorNames list one kind each).
// Nil-receiver-safe.
func (s *Set) Names() []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, s.Len())
	for n := range s.byName {
		out = append(out, n)
	}
	for n := range s.vectors {
		out = append(out, n)
	}
	for n := range s.sems {
		out = append(out, n)
	}
	for n := range s.failed {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// RegressionNames returns the bound regression names, sorted.
// Nil-receiver-safe.
func (s *Set) RegressionNames() []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.byName))
	for n := range s.byName {
		out = append(out, n)
	}
	out = append(out, s.failedOfKind(types.FitKindRegression)...)
	sort.Strings(out)
	return out
}

// Len is the number of bound names, both kinds. Nil-receiver-safe.
func (s *Set) Len() int {
	if s == nil {
		return 0
	}
	return len(s.byName) + len(s.vectors) + len(s.sems) + len(s.failed)
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
