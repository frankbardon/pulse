package model

import (
	"fmt"
	"sort"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// sem.go is the SEM fit kind (types.FitKindSEM): a Compose `fits` entry
// whose request holds exactly one MAT_SEM matrix binds the fitted
// structural equation model under its name — the model description and
// options it was fitted with, the member labels a consuming slot aligns
// to, and each group's parameter matrices at the estimate, at full
// precision. ATTR_SEM_SCORE's params.fit scores rows from it, row-local.
//
// The figures are plain data (this package stays a leaf over the
// standard library, types and errors): the engine rebuilds the parsed
// model from Model and the options and each group's scorer from its
// matrices, by the one deterministic arithmetic an inline fit's scorer
// uses — so a score against a bound SEMFit is bit-identical to the
// score ATTR_SEM_SCORE's inline fit gives on the same fit rows, and a
// saved fit (whose file holds these same figures) scores like the fit
// that wrote it.

// SEMFit is one fitted SEM bound under a name.
type SEMFit struct {
	// Name is the bound name.
	Name string

	// Operator is the battery the fit was taken from (MAT_SEM).
	Operator string

	// Model is the model description fitted.
	Model string

	// Labels are the member labels in member order (the model's observed
	// variables are named by them); Members the member columns,
	// informational.
	Labels  []string
	Members []string

	// Observed and Inner are the parsed model's variable orders — the
	// rows and columns of the stored matrices; Latents the latent
	// variables in Inner order. A rebuilt model must reproduce them.
	Observed []string
	Inner    []string
	Latents  []string

	// Estimator is the fit's estimator ("ml", or an ordinal "dwls" /
	// "wlsmv"). Lavaan reports the fit followed lavaan's conventions
	// (Conventions "lavaan"); the model rebuild needs it only for a
	// bootstrapped covariance-only fit.
	Estimator string
	Lavaan    bool

	// Ordered are an ordinal fit's ordered variables in Observed order
	// (nil for an ML fit) and Categories[k] the k-th one's category
	// values ascending (category code = index; pooled over every group).
	Ordered    []string
	Categories [][]float64

	// MeanStructure is params.meanstructure; Group, GroupReference,
	// GroupEqual and GroupPartial the multi-group knobs ("" / nil
	// ungrouped). Solver and Information are the solver settings.
	MeanStructure  bool
	Group          string
	GroupReference string
	GroupEqual     []string
	GroupPartial   []string
	Solver         string
	Information    string

	// Basis is the weight basis the fit was taken under (unweighted or
	// frequency).
	Basis VectorBasis

	// N counts the listwise-complete fit rows; SumWeights is Σw over
	// them.
	N          int64
	SumWeights float64

	// DoF and NParams are the fit's degrees of freedom and free
	// parameter count; Converged / Status / Iterations its solver's
	// stop; NearestPD reports the fit used the nearest positive-definite
	// substitute, whose minimum eigenvalue MinEigenvalue is.
	DoF           float64
	NParams       int
	Converged     bool
	Status        string
	Iterations    int
	NearestPD     bool
	MinEigenvalue float64

	// Groups are the fit's groups in level order (one, Level "",
	// ungrouped).
	Groups []SEMGroup

	// Source is where the name was bound (the zero value when unknown).
	Source Source
}

// SEMGroup is one group's parameters at the estimate.
type SEMGroup struct {
	// Level is the group's level label ("" ungrouped).
	Level string

	// N counts the group's fit rows; SumWeights is Σw over them.
	N          int64
	SumWeights float64

	// Lambda (Observed × Inner), Beta and Psi (Inner × Inner), Theta
	// (Observed × Observed), Nu (Observed), Alpha (Inner), fixed values
	// included; SampleMean the group's fit-row mean over Observed.
	Lambda, Beta, Psi, Theta [][]float64
	Nu, Alpha, SampleMean    []float64

	// Thresholds are an ordinal fit's raw thresholds per Ordered
	// variable (nil for an ML fit).
	Thresholds [][]float64
}

// Ordinal reports whether f is an ordinal (DWLS / WLSMV) fit.
func (f *SEMFit) Ordinal() bool { return f != nil && len(f.Ordered) > 0 }

// Grouped reports whether the fit is a joint multi-group fit.
func (f *SEMFit) Grouped() bool { return f != nil && f.Group != "" }

// Conventions is "lavaan" or "semopy".
func (f *SEMFit) Conventions() string {
	if f.Lavaan {
		return "lavaan"
	}
	return "semopy"
}

// Signature is f's static face: kind sem, its member labels.
func (f *SEMFit) Signature() Signature {
	return Signature{Name: f.Name, Kind: types.FitKindSEM, Members: append([]string(nil), f.Labels...), Source: f.Source, Ordinal: f.Ordinal()}
}

// Result is f's ComposedResponse.Fits summary: every stored figure, at
// full precision (deep copies).
func (f *SEMFit) Result() *types.SEMFitResult {
	out := &types.SEMFitResult{
		Operator:       f.Operator,
		Model:          f.Model,
		Labels:         cloneStrings(f.Labels),
		Members:        cloneStrings(f.Members),
		Observed:       cloneStrings(f.Observed),
		Inner:          cloneStrings(f.Inner),
		Latents:        cloneStrings(f.Latents),
		Estimator:      f.Estimator,
		Conventions:    f.Conventions(),
		Ordered:        cloneStrings(f.Ordered),
		MeanStructure:  f.MeanStructure,
		Group:          f.Group,
		GroupReference: f.GroupReference,
		GroupEqual:     cloneStrings(f.GroupEqual),
		GroupPartial:   cloneStrings(f.GroupPartial),
		Solver:         f.Solver,
		Information:    f.Information,
		Basis:          string(f.Basis),
		N:              f.N,
		SumWeights:     f.SumWeights,
		DoF:            f.DoF,
		NParams:        f.NParams,
		Converged:      f.Converged,
		Status:         f.Status,
		Iterations:     f.Iterations,
		Groups:         make([]types.SEMFitGroup, len(f.Groups)),
	}
	if out.Latents == nil {
		out.Latents = []string{}
	}
	if f.NearestPD {
		v := f.MinEigenvalue
		out.NearestPDMinEigenvalue = &v
	}
	if f.Categories != nil {
		out.Categories = cloneRows(f.Categories)
	}
	for g, gr := range f.Groups {
		out.Groups[g] = types.SEMFitGroup{
			Level: gr.Level, N: gr.N, SumWeights: gr.SumWeights,
			Lambda: cloneRows(gr.Lambda), Beta: cloneRows(gr.Beta), Psi: cloneRows(gr.Psi), Theta: cloneRows(gr.Theta),
			Nu: cloneFloats(gr.Nu), Alpha: cloneFloats(gr.Alpha), SampleMean: cloneFloats(gr.SampleMean),
		}
		if gr.Thresholds != nil {
			out.Groups[g].Thresholds = cloneRows(gr.Thresholds)
		}
	}
	return out
}

func cloneStrings(s []string) []string {
	if s == nil {
		return nil
	}
	return append([]string{}, s...)
}

func cloneFloats(s []float64) []float64 {
	if s == nil {
		return []float64{}
	}
	return append([]float64{}, s...)
}

func cloneRows(m [][]float64) [][]float64 {
	out := make([][]float64, len(m))
	for i, r := range m {
		out[i] = cloneFloats(r)
	}
	return out
}

// AddSEM binds f under f.Name. The namespace is shared with every other
// kind: a name already bound is PULSE_MODEL_NAME_COLLISION.
func (s *Set) AddSEM(f *SEMFit) error {
	if s.bound(f.Name) {
		return nameCollision(f.Name)
	}
	if s.sems == nil {
		s.sems = map[string]*SEMFit{}
	}
	s.sems[f.Name] = f
	return nil
}

// LookupSEM resolves name to a bound SEM fit. An unbound name is
// PULSE_MODEL_UNKNOWN with the bound SEM fits under details.available;
// a name bound as another kind is PULSE_MODEL_KIND_MISMATCH (expected
// sem). A failed SEM fit is its own refusal (FailedFitRefusal) — a
// reader asks FailedOfKind first. Nil-receiver-safe.
func (s *Set) LookupSEM(name string) (*SEMFit, error) {
	if s != nil {
		if f, ok := s.sems[name]; ok {
			return f, nil
		}
		if k, ok := s.kindOf(name); ok {
			if k != types.FitKindSEM {
				return nil, KindMismatch(name, types.FitKindSEM, k)
			}
			if f, failed := s.failed[name]; failed {
				return nil, FailedFitRefusal(name, f.err)
			}
		}
	}
	return nil, unknownSEM(name, s.SEMNames())
}

// kindOf is the kind name is bound as (false when unbound).
func (s *Set) kindOf(name string) (types.FitKind, bool) {
	if s == nil {
		return "", false
	}
	if _, ok := s.byName[name]; ok {
		return types.FitKindRegression, true
	}
	if _, ok := s.vectors[name]; ok {
		return types.FitKindVector, true
	}
	if _, ok := s.sems[name]; ok {
		return types.FitKindSEM, true
	}
	if f, ok := s.failed[name]; ok {
		return f.sig.EffectiveKind(), true
	}
	return "", false
}

// SEMNames returns the bound SEM fit names (failed ones included),
// sorted. Nil-receiver-safe.
func (s *Set) SEMNames() []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.sems))
	for n := range s.sems {
		out = append(out, n)
	}
	out = append(out, s.failedOfKind(types.FitKindSEM)...)
	sort.Strings(out)
	return out
}

func unknownSEM(name string, available []string) *errors.CodedError {
	ce := UnknownModel(name, available)
	ce.Message = fmt.Sprintf("no SEM fit named %q is bound in this request", name)
	return ce
}

// CheckSEMFit is the static rule over one SEM-fit reference
// (ATTR_SEM_SCORE's params.fit) against cat — the runtime's Set or
// predict's FitCatalog, so both refuse the same name the same way: an
// unbound name is PULSE_MODEL_UNKNOWN listing the bound SEM fits; a
// name of another kind is PULSE_MODEL_KIND_MISMATCH.
func CheckSEMFit(cat Catalog, name string) *errors.CodedError {
	var sig Signature
	ok := false
	if cat != nil {
		sig, ok = cat.Signature(name)
	}
	if !ok {
		return unknownSEM(name, NamesOfKind(cat, types.FitKindSEM))
	}
	if k := sig.EffectiveKind(); k != types.FitKindSEM {
		return KindMismatch(name, types.FitKindSEM, k)
	}
	return nil
}

// IsSEMFitRequest reports whether req is a SEM fit's request: no
// regression and exactly one battery, a MAT_SEM matrix.
func IsSEMFitRequest(req *types.Request) bool {
	return req != nil && len(req.Regressions) == 0 && VectorBatteries(req) == 1 &&
		len(req.Matrices) == 1 && req.Matrices[0].Type == types.MAT_SEM
}
