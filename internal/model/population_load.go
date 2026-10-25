package model

import (
	stderrors "errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// population_load.go is the `populations` load: a Request entry
// {name, path} binds a saved population file under name, as a vector
// fit, in the one namespace `models` entries and a Compose `fits` list
// bind. It mirrors load.go — the rule set is the runtime's and
// predict's alike (CheckRefs, then LoadFiles), so both refuse the same
// request with the same code, message and details — and the codes are
// the model file's (PULSE_MODEL_REF_INVALID, _FILE_UNREADABLE,
// _FILE_INVALID, _VERSION_UNSUPPORTED, _NAME_COLLISION), each naming
// the entry under details.population.

// SlotPopulations is the Source slot of a `populations` entry.
const SlotPopulations = "populations"

// PopulationSource is the Source of populations[k] loading path.
func PopulationSource(k int, path string) Source {
	return Source{Slot: SlotPopulations, Index: k, Path: path}
}

// Refs are one request's file loads: its `models` and `populations`
// entries. A FacetRequest carries models only.
type Refs struct {
	Models      []types.ModelRef
	Populations []types.PopulationRef
}

// RequestRefs returns req's loads (the zero Refs for nil).
func RequestRefs(req *types.Request) Refs {
	if req == nil {
		return Refs{}
	}
	return Refs{Models: req.Models, Populations: req.Populations}
}

// Empty reports whether r loads nothing.
func (r Refs) Empty() bool { return len(r.Models) == 0 && len(r.Populations) == 0 }

// Loaded is a request's decoded files, index-aligned with its Refs.
type Loaded struct {
	Models      []*File
	Populations []*PopulationFile
	// SEMs are the `models` entries that load a SEM fit file
	// (.pulse-sem.json), index-aligned with Models (nil where the entry
	// is a model file; nil when none is).
	SEMs []*SEMFile
}

// CheckRefs is the static rule over a request's loads: its `models`
// (CheckModelRefs against outer), then its `populations`, entry by
// entry in order, the first fault winning — a blank name
// (PULSE_MODEL_REF_INVALID name_empty), a path the population save-path
// rule refuses (PULSE_MODEL_REF_INVALID, the path reason), then a name a
// `models` entry, an earlier population or outer already binds
// (PULSE_MODEL_NAME_COLLISION, details.sources). No file is read.
func CheckRefs(refs Refs, outer Catalog) *errors.CodedError {
	if cerr := CheckModelRefs(refs.Models, outer); cerr != nil {
		return cerr
	}
	seen := make(map[string]Source, len(refs.Models)+len(refs.Populations))
	for j, r := range refs.Models {
		seen[r.Name] = ModelSource(j, r.Path)
	}
	for k, r := range refs.Populations {
		if strings.TrimSpace(r.Name) == "" {
			return populationRefInvalid(k, r, ReasonRefNameEmpty, "has no name")
		}
		if reason, what := checkFilePath(r.Path, PopulationSuffix); reason != "" {
			return populationRefInvalid(k, r, reason, "path "+what)
		}
		src := PopulationSource(k, r.Path)
		if prior, dup := seen[r.Name]; dup {
			return NameCollision(r.Name, prior, src)
		}
		if outer != nil {
			if sig, bound := outer.Signature(r.Name); bound {
				return NameCollision(r.Name, sig.Source, src)
			}
		}
		seen[r.Name] = src
	}
	return nil
}

// LoadFiles reads and decodes a request's loads (refs passed
// CheckRefs): every `models` file (LoadModelFiles), then every
// population file in order — a read error PULSE_MODEL_FILE_UNREADABLE
// (missing / unreadable), a decode fault DecodePopulationFile's
// PULSE_MODEL_FILE_INVALID / _VERSION_UNSUPPORTED, each gaining
// details.population (the entry index) and details.name. The first
// fault wins. Nothing to load returns nil.
func LoadFiles(refs Refs, read ReadFunc) (*Loaded, *errors.CodedError) {
	if refs.Empty() {
		return nil, nil
	}
	models, sems, cerr := loadModelEntries(refs.Models, read)
	if cerr != nil {
		return nil, cerr
	}
	out := &Loaded{Models: models, SEMs: sems}
	if len(refs.Populations) > 0 {
		out.Populations = make([]*PopulationFile, len(refs.Populations))
	}
	for k, r := range refs.Populations {
		var (
			data []byte
			err  error
		)
		if read == nil {
			err = stderrors.New("no population file reader")
		} else {
			data, err = read(r.Path)
		}
		if err != nil {
			reason := ReasonFileUnreadable
			if stderrors.Is(err, fs.ErrNotExist) {
				reason = ReasonFileMissing
			}
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_FILE_UNREADABLE,
				fmt.Sprintf("populations[%d]: population file %s cannot be read (%s)", k, r.Path, reason),
				map[string]any{"population": k, "name": r.Name, "path": r.Path, "reason": reason})
		}
		f, derr := DecodePopulationFile(r.Path, data)
		if derr != nil {
			var ce *errors.CodedError
			if !stderrors.As(derr, &ce) {
				ce = errors.WrapCodedError(derr, errors.PULSE_MODEL_FILE_INVALID, "decoding population file "+r.Path)
			}
			details := make(map[string]any, len(ce.Details)+2)
			for key, v := range ce.Details {
				details[key] = v
			}
			details["population"] = k
			details["name"] = r.Name
			return nil, errors.NewCodedErrorWithDetails(ce.Code, fmt.Sprintf("populations[%d]: %s", k, ce.Message), details)
		}
		out.Populations[k] = f
	}
	return out, nil
}

// WithLoaded returns a new Set holding s's bindings plus every loaded
// model (WithModels) and every loaded population bound as a vector fit
// under its entry's name (Source populations[k]). refs passed CheckRefs
// against s, so no name collides; l is LoadFiles' result. s is never
// mutated; nil s is empty; nothing loaded returns s.
func (s *Set) WithLoaded(refs Refs, l *Loaded) (*Set, error) {
	if refs.Empty() {
		return s, nil
	}
	if l == nil {
		l = &Loaded{}
	}
	out, err := s.WithModels(refs.Models, l.Models)
	if err != nil {
		return nil, err
	}
	if s != nil && out.capture == nil {
		out.capture = s.capture
	}
	for j, r := range refs.Models {
		if j >= len(l.SEMs) || l.SEMs[j] == nil {
			continue
		}
		sf := l.SEMs[j].SEMFit(r.Name)
		sf.Source = ModelSource(j, r.Path)
		if err := out.AddSEM(sf); err != nil {
			return nil, err
		}
	}
	for k, r := range refs.Populations {
		if k >= len(l.Populations) || l.Populations[k] == nil {
			continue
		}
		v := l.Populations[k].VectorFit(r.Name)
		v.Source = PopulationSource(k, r.Path)
		if err := out.AddVector(v); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// CatalogWithLoaded is predict's WithLoaded: outer's signatures plus
// every loaded model's (CatalogWithModels) and every loaded
// population's — kind vector, its member labels — under its entry's
// name. outer may be nil.
func CatalogWithLoaded(outer Catalog, refs Refs, l *Loaded) FitCatalog {
	if l == nil {
		l = &Loaded{}
	}
	out := CatalogWithModels(outer, refs.Models, l.Models)
	for j, r := range refs.Models {
		if j >= len(l.SEMs) || l.SEMs[j] == nil {
			continue
		}
		out[r.Name] = Signature{
			Name:    r.Name,
			Kind:    types.FitKindSEM,
			Members: append([]string(nil), l.SEMs[j].Labels...),
			Source:  ModelSource(j, r.Path),
		}
	}
	for k, r := range refs.Populations {
		if k >= len(l.Populations) || l.Populations[k] == nil {
			continue
		}
		out[r.Name] = Signature{
			Name:    r.Name,
			Kind:    types.FitKindVector,
			Members: append([]string(nil), l.Populations[k].Labels...),
			Source:  PopulationSource(k, r.Path),
		}
	}
	return out
}

func populationRefInvalid(k int, r types.PopulationRef, reason, what string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_REF_INVALID,
		fmt.Sprintf("populations[%d]: population entry %s", k, what),
		map[string]any{"population": k, "name": r.Name, "path": r.Path, "reason": reason})
}
