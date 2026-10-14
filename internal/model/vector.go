package model

import (
	"fmt"
	"sort"

	"github.com/frankbardon/pulse/errors"
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

// LookupVector resolves name to a bound vector fit. An unbound name —
// or one bound as a regression, which has no vector state — is
// PULSE_MODEL_UNKNOWN with the bound vector fits under
// details.available (and details.kind "regression" for the latter).
// Nil-receiver-safe.
func (s *Set) LookupVector(name string) (*VectorFit, error) {
	if s != nil {
		if v, ok := s.vectors[name]; ok {
			return v, nil
		}
	}
	ce := UnknownModel(name, s.VectorNames())
	ce.Message = fmt.Sprintf("no vector fit named %q is bound in this request", name)
	if s != nil {
		if _, ok := s.byName[name]; ok {
			ce.Message = fmt.Sprintf("%q is a bound regression, not a vector fit; params.fit names a vector fit", name)
			ce.Details["kind"] = "regression"
		}
	}
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
