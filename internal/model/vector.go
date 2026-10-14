package model

import (
	"fmt"
	"sort"
	"sync"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// VectorBasis names how a vector fit's Σw became the sample size N* its
// covariance divides by (the weighting rule every fitted vector
// operator shares): Σw unweighted or under frequency weights, Kish's
// n_eff under probability weights.
type VectorBasis string

// The vector fit bases.
const (
	VectorBasisUnweighted  VectorBasis = "unweighted"
	VectorBasisFrequency   VectorBasis = "frequency"
	VectorBasisProbability VectorBasis = "probability"
)

// VectorFit is one fitted vector state bound under a name: the complete
// listwise co-moment of a member battery — everything a fitted vector
// operator (ATTR_MAHALANOBIS, ATTR_PC_SCORE, ATTR_FACTOR_SCORE, a fitted
// ATTR_VEC_DISTANCE metric) decomposes — at full precision, in Labels
// order. Derived blocks (covariance, correlation, Cholesky factor,
// eigen-decomposition, loadings, rotation) are never stored: every
// consumer recomputes them from these figures by the one deterministic
// arithmetic, so a score against a bound VectorFit is bit-identical to
// the score its own fit_on fold would give on the same rows.
//
// The figures mirror linalg.ListwiseState (this package stays a leaf
// over the standard library, types and errors; the engine converts).
type VectorFit struct {
	// Name is the bound name.
	Name string

	// Labels are the member labels in axis order — the names a
	// consuming slot's members are aligned to (by label, never by
	// position). Members are the fit's member columns, informational.
	Labels  []string
	Members []string

	// Basis is the weight basis the fit was taken under.
	Basis VectorBasis

	// N counts the listwise-complete fit rows with a valid weight (zero
	// weights included), NMass those with w > 0, NWeightInvalid the rows
	// skipped for an invalid weight.
	N, NMass, NWeightInvalid int64

	// SumWeights and SumWeightsSq are Σw and Σw² over the rows N counts.
	SumWeights, SumWeightsSq float64

	// Mean is the stored (weighted) mean, axis order.
	Mean []float64

	// M2 is the raw weighted co-moment Σw(xi−mi)(xj−mj), a full
	// symmetric p × p matrix in axis order.
	M2 [][]float64

	// Operator is the battery the fit was taken from — the fit
	// request's MAT_* type or fitted attribute type ("" when unknown).
	Operator string

	// Source is where the name was bound (the zero value when unknown).
	Source Source
}

// P is the member count.
func (v *VectorFit) P() int { return len(v.Labels) }

// NStar is the fit's N*: Kish's n_eff (Σw)²/Σw² under a probability
// basis (0 with no mass), Σw otherwise — the same arithmetic the fold
// uses (linalg.CoMoment.NEff / W).
func (v *VectorFit) NStar() float64 {
	if v.Basis == VectorBasisProbability {
		if v.SumWeightsSq == 0 {
			return 0
		}
		return v.SumWeights * v.SumWeights / v.SumWeightsSq
	}
	return v.SumWeights
}

// AddVector binds v under v.Name. The namespace is shared with the
// bound regressions: a name already bound as either kind is
// PULSE_MODEL_NAME_COLLISION.
func (s *Set) AddVector(v *VectorFit) error {
	if s.bound(v.Name) {
		return nameCollision(v.Name)
	}
	if s.vectors == nil {
		s.vectors = map[string]*VectorFit{}
	}
	s.vectors[v.Name] = v
	return nil
}

// LookupVector resolves name to a bound vector fit. An unbound name is
// PULSE_MODEL_UNKNOWN with the bound vector fits under
// details.available; a name bound as a regression is
// PULSE_MODEL_KIND_MISMATCH (KindMismatch: expected vector, kind
// regression). Nil-receiver-safe.
func (s *Set) LookupVector(name string) (*VectorFit, error) {
	if s != nil {
		if v, ok := s.vectors[name]; ok {
			return v, nil
		}
		if _, ok := s.byName[name]; ok {
			return nil, KindMismatch(name, types.FitKindVector, types.FitKindRegression)
		}
	}
	ce := UnknownModel(name, s.VectorNames())
	ce.Message = fmt.Sprintf("no vector fit named %q is bound in this request", name)
	return nil, ce
}

// VectorNames returns the bound vector fit names, sorted.
// Nil-receiver-safe.
func (s *Set) VectorNames() []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.vectors))
	for n := range s.vectors {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// bound reports whether name is bound as either kind.
func (s *Set) bound(name string) bool {
	if s == nil {
		return false
	}
	_, reg := s.byName[name]
	_, vec := s.vectors[name]
	return reg || vec
}

func nameCollision(name string) error {
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_NAME_COLLISION,
		fmt.Sprintf("model name %q is bound twice", name),
		map[string]any{"name": name})
}

// CheckVectorFit is the static rule over one vector-fit reference (a
// fitted attribute's or a MAT_* slot's params.fit) against cat — the
// runtime's Set or predict's FitCatalog, so both refuse the same name
// the same way: an unbound name is PULSE_MODEL_UNKNOWN listing the
// bound vector fits; a regression name is PULSE_MODEL_KIND_MISMATCH.
func CheckVectorFit(cat Catalog, name string) *errors.CodedError {
	var sig Signature
	ok := false
	if cat != nil {
		sig, ok = cat.Signature(name)
	}
	if !ok {
		ce := UnknownModel(name, NamesOfKind(cat, types.FitKindVector))
		ce.Message = fmt.Sprintf("no vector fit named %q is bound in this request", name)
		return ce
	}
	if sig.Kind != types.FitKindVector {
		return KindMismatch(name, types.FitKindVector, sig.EffectiveKind())
	}
	return nil
}

// KindMismatch is the PULSE_MODEL_KIND_MISMATCH refusal of a reference
// to name, bound as kind, where a fit of kind expected is read: a
// regression consumer (ATTR_REG_PREDICT, coef()) naming a vector fit,
// or a vector consumer (a fitted attribute's or MAT_* slot's
// params.fit) naming a regression. One namespace, two kinds: details
// carry name, fit (the same name), kind and expected.
func KindMismatch(name string, expected, kind types.FitKind) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_KIND_MISMATCH,
		fmt.Sprintf("%q is a bound %s fit; this reference reads a %s fit", name, kind, expected),
		map[string]any{"name": name, "fit": name, "kind": string(kind), "expected": string(expected)})
}

// NamesOfKind returns cat's bound names of kind, sorted (nil-safe for a
// nil interface).
func NamesOfKind(cat Catalog, kind types.FitKind) []string {
	if cat == nil {
		return nil
	}
	out := []string{}
	for _, n := range cat.Names() {
		if sig, ok := cat.Signature(n); ok && sig.EffectiveKind() == kind {
			out = append(out, n)
		}
	}
	return out
}

// VectorCapture receives the vector fit a Compose vector-fit request's
// battery closes: the one MAT_* slot or fitted attribute (FitKindOf)
// offers its closed listwise state at finalize, and the fit pre-pass
// binds it under the fit's name. It rides the fit request's own Set
// (WithCapture) — the namespace every engine arm already receives — so
// whichever arm runs the request (buffered, streaming, two-pass, the
// shard reducers) offers through the one handle. Safe for concurrent
// offers.
type VectorCapture struct {
	mu   sync.Mutex
	fits []*VectorFit
}

// Offer records a closed vector fit. Nil-receiver-safe (a set without a
// capture ignores the offer).
func (c *VectorCapture) Offer(v *VectorFit) {
	if c == nil || v == nil {
		return
	}
	c.mu.Lock()
	c.fits = append(c.fits, v)
	c.mu.Unlock()
}

// Fits returns the offered fits in offer order.
func (c *VectorCapture) Fits() []*VectorFit {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*VectorFit(nil), c.fits...)
}

// WithCapture returns a copy of s (nil s: an empty set) carrying c; s
// is never mutated.
func (s *Set) WithCapture(c *VectorCapture) *Set {
	out := &Set{capture: c}
	if s != nil {
		out.byName, out.vectors = s.byName, s.vectors
	}
	return out
}

// Capture returns the vector capture s carries (nil for none).
// Nil-receiver-safe.
func (s *Set) Capture() *VectorCapture {
	if s == nil {
		return nil
	}
	return s.capture
}
