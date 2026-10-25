package model

import (
	"maps"
	"sort"

	"github.com/frankbardon/pulse/types"
)

// population_registered.go is the instance's registered populations:
// the population files an embedder hands pulse.New
// (Options.Extensions.Populations) or a populations directory supplies
// (Options.PopulationsDir / PULSE_POPULATIONS_DIR), decoded once at New
// and frozen. They are the OUTERMOST layer of the one fit namespace:
// every request — a plain Process, every Compose fit request and slot,
// every chain stage, a facet, a materialize — sees them bound as vector
// fits, beneath whatever the request itself binds. A request name that
// a registered population already binds (a `fits`, `models` or
// `populations` entry) is PULSE_MODEL_NAME_COLLISION, never a shadow:
// one name, one fit. Predict judges the same rule against the
// registered names and labels the instance snapshot carries.

// SlotRegistered is the Source slot of a registered population.
const SlotRegistered = "registered"

// RegisteredSource is the Source of a registered population.
func RegisteredSource() Source { return Source{Slot: SlotRegistered} }

// RegisteredSet binds every registered population file as a vector fit
// under its registered name (Source RegisteredSource), the set every
// request's own bindings layer over. No files returns nil (binds
// nothing). Names are bound in sorted order; the map keys are unique,
// so no name collides.
func RegisteredSet(files map[string]*PopulationFile) (*Set, error) {
	if len(files) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	out := &Set{}
	for _, n := range names {
		v := files[n].VectorFit(n)
		v.Source = RegisteredSource()
		if err := out.AddVector(v); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// RegisteredSignature is predict's static face of a registered
// population: kind vector, its member labels in axis order — the same
// signature the runtime's RegisteredSet answers.
func RegisteredSignature(name string, labels []string) Signature {
	return Signature{Name: name, Kind: types.FitKindVector, Members: append([]string(nil), labels...), Source: RegisteredSource()}
}

// Clone returns a new Set holding s's bindings (nil s: an empty set);
// s is never mutated, and the bound fits themselves are shared
// read-only. It carries no capture.
func (s *Set) Clone() *Set {
	out := &Set{}
	if s == nil {
		return out
	}
	if len(s.byName) > 0 {
		out.byName = make(map[string]*Bound, len(s.byName))
		for k, b := range s.byName {
			out.byName[k] = b
		}
	}
	if len(s.vectors) > 0 {
		out.vectors = make(map[string]*VectorFit, len(s.vectors))
		for k, v := range s.vectors {
			out.vectors[k] = v
		}
	}
	if len(s.sems) > 0 {
		out.sems = maps.Clone(s.sems)
	}
	// Resolved records and failed fits are read-only too: shared.
	out.records = s.records
	if len(s.failed) > 0 {
		out.failed = maps.Clone(s.failed)
	}
	return out
}

// WithinCatalog is inner layered over outer: outer's signatures plus
// inner's. A nil outer returns inner unchanged (a Compose batch on an
// instance with no registered populations judges exactly the fits
// catalog it always did). inner's names never collide with outer's —
// CheckComposeBatch refuses that first.
func WithinCatalog(outer Catalog, inner FitCatalog) Catalog {
	if outer == nil {
		return inner
	}
	out := CatalogWithModels(outer, nil, nil)
	for n, sig := range inner {
		out[n] = sig
	}
	return out
}
