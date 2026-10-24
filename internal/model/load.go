package model

import (
	stderrors "errors"
	"fmt"
	"io/fs"
	"maps"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// load.go is the `models` load: a Request (or FacetRequest) entry
// {name, path} binds a saved model file under name, in the namespace a
// Compose `fits` list binds. The rule set is the runtime's and
// predict's alike — CheckModelRefs (static: the entries and their names
// against what is already bound), then LoadModelFiles (each file read
// and decoded, in order) — so both refuse the same request with the
// same code, message and details. The read itself is the caller's: the
// runtime reads through the instance afero.Fs, predict through
// PredictOptions.ModelLoader.

// The Source slots.
const (
	SlotFits   = "fits"
	SlotModels = "models"
)

// Source names where a bound name came from: the `fits` entry or the
// `models` entry (with its file path) that bound it.
type Source struct {
	// Slot is SlotFits, SlotModels, SlotPopulations or SlotRegistered
	// ("" when unknown).
	Slot string
	// Index is the 0-based entry index in that slot.
	Index int
	// Path is a `models` entry's file path ("" for a fit).
	Path string
}

// FitSource is the Source of fits[i].
func FitSource(i int) Source { return Source{Slot: SlotFits, Index: i} }

// ModelSource is the Source of models[j] loading path.
func ModelSource(j int, path string) Source {
	return Source{Slot: SlotModels, Index: j, Path: path}
}

// String is "fits[i]", "models[j]" or "populations[k]" — or, for a
// population the instance registered (SlotRegistered), "the
// instance's registered populations".
func (s Source) String() string {
	if s.Slot == SlotRegistered {
		return "the instance's registered populations"
	}
	return fmt.Sprintf("%s[%d]", s.Slot, s.Index)
}

// detail is s as an error-details value: {slot, index[, path]} — a
// registered population {slot: "registered"}, with no index.
func (s Source) detail() map[string]any {
	if s.Slot == SlotRegistered {
		return map[string]any{"slot": s.Slot}
	}
	d := map[string]any{"slot": s.Slot, "index": s.Index}
	if s.Path != "" {
		d["path"] = s.Path
	}
	return d
}

// NameCollision is the PULSE_MODEL_NAME_COLLISION refusal of a `models`
// entry (second) whose name first already binds — details.name and
// details.sources (both bindings, first then second).
func NameCollision(name string, first, second Source) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_NAME_COLLISION,
		fmt.Sprintf("model name %q is bound twice: by %s and by %s", name, first, second),
		map[string]any{"name": name, "sources": []any{first.detail(), second.detail()}})
}

// The PULSE_MODEL_REF_INVALID reasons beyond the shared path reasons
// (ReasonSavePathEmpty, ReasonSaveSuffix, ReasonSaveNameMissing,
// ReasonSaveEscapesRoot — the one model-file path rule).
const ReasonRefNameEmpty = "name_empty"

// The PULSE_MODEL_FILE_UNREADABLE reasons.
const (
	ReasonFileMissing    = "missing"
	ReasonFileUnreadable = "unreadable"
)

// ReadFunc reads a model file's bytes at a path relative to the
// instance filesystem root.
type ReadFunc func(path string) ([]byte, error)

// CheckModelRefs is the static rule over a `models` list, judged entry by
// entry in order, the first fault winning: a blank name
// (PULSE_MODEL_REF_INVALID name_empty), a path the save-path rule
// refuses (PULSE_MODEL_REF_INVALID, the path reason), then a name an
// earlier entry or outer already binds (PULSE_MODEL_NAME_COLLISION,
// details.sources). outer is what the request already sees bound — a
// Compose slot's fits; nil for none. No file is read.
func CheckModelRefs(refs []types.ModelRef, outer Catalog) *errors.CodedError {
	seen := make(map[string]Source, len(refs))
	for j, r := range refs {
		if strings.TrimSpace(r.Name) == "" {
			return refInvalid(j, r, ReasonRefNameEmpty, "has no name")
		}
		if reason, what := checkModelPath(r.Path); reason != "" {
			return refInvalid(j, r, reason, "path "+what)
		}
		src := ModelSource(j, r.Path)
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

// LoadModelFiles reads and decodes every `models` entry in order (the
// refs passed CheckModelRefs): a read error is
// PULSE_MODEL_FILE_UNREADABLE (missing / unreadable), a decode fault
// DecodeFile's PULSE_MODEL_FILE_INVALID / _VERSION_UNSUPPORTED — every
// refusal gaining details.model (the entry index) and details.name. The
// first fault wins. The files are index-aligned with refs.
func LoadModelFiles(refs []types.ModelRef, read ReadFunc) ([]*File, *errors.CodedError) {
	if len(refs) == 0 {
		return nil, nil
	}
	out := make([]*File, len(refs))
	for j, r := range refs {
		var (
			data []byte
			err  error
		)
		if read == nil {
			err = stderrors.New("no model file reader")
		} else {
			data, err = read(r.Path)
		}
		if err != nil {
			reason := ReasonFileUnreadable
			if stderrors.Is(err, fs.ErrNotExist) {
				reason = ReasonFileMissing
			}
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_FILE_UNREADABLE,
				fmt.Sprintf("models[%d]: model file %s cannot be read (%s)", j, r.Path, reason),
				map[string]any{"model": j, "name": r.Name, "path": r.Path, "reason": reason})
		}
		f, derr := DecodeFile(r.Path, data)
		if derr != nil {
			var ce *errors.CodedError
			if !stderrors.As(derr, &ce) {
				ce = errors.WrapCodedError(derr, errors.PULSE_MODEL_FILE_INVALID, "decoding model file "+r.Path)
			}
			details := make(map[string]any, len(ce.Details)+2)
			for k, v := range ce.Details {
				details[k] = v
			}
			details["model"] = j
			details["name"] = r.Name
			return nil, errors.NewCodedErrorWithDetails(ce.Code, fmt.Sprintf("models[%d]: %s", j, ce.Message), details)
		}
		out[j] = f
	}
	return out, nil
}

// WithModels returns a new Set holding s's bindings plus every loaded
// file bound under its entry's name (Source models[j]). refs passed
// CheckModelRefs against s, so no name collides; files are
// LoadModelFiles' result. s is never mutated; nil s is empty.
func (s *Set) WithModels(refs []types.ModelRef, files []*File) (*Set, error) {
	out := &Set{byName: make(map[string]*Bound, s.Len()+len(refs))}
	if s != nil {
		out.records, out.failed = s.records, s.failed
		if len(s.sems) > 0 {
			out.sems = maps.Clone(s.sems)
		}
		for k, b := range s.byName {
			out.byName[k] = b
		}
		for k, v := range s.vectors {
			if out.vectors == nil {
				out.vectors = make(map[string]*VectorFit, len(s.vectors))
			}
			out.vectors[k] = v
		}
	}
	for j, r := range refs {
		b, err := files[j].Bind(r.Name)
		if err != nil {
			return nil, err
		}
		b.Source = ModelSource(j, r.Path)
		if err := out.Add(b); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// CatalogWithModels is predict's WithModels: outer's signatures plus
// every loaded file's, under its entry's name — the static view of the
// names the runtime binds. outer may be nil.
func CatalogWithModels(outer Catalog, refs []types.ModelRef, files []*File) FitCatalog {
	out := FitCatalog{}
	if outer != nil {
		for _, n := range outer.Names() {
			if sig, ok := outer.Signature(n); ok {
				out[n] = sig
			}
		}
	}
	for j, r := range refs {
		if j >= len(files) || files[j] == nil {
			continue
		}
		sig := Signature{
			Name:   r.Name,
			Type:   files[j].Type,
			Target: files[j].Target,
			Terms:  append([]string(nil), files[j].Terms...),
			Source: ModelSource(j, r.Path),
			Kind:   types.FitKindRegression,
		}
		if !BuiltinRegression(files[j].Type) {
			// The file names the fit's coefficients, so predict judges a
			// coef() term exactly as the bound model does.
			sig.Coefficients, sig.CoefficientsKnown = sortedKeys(files[j].Coefficients), true
		}
		out[r.Name] = sig
	}
	return out
}

func refInvalid(j int, r types.ModelRef, reason, what string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_REF_INVALID,
		fmt.Sprintf("models[%d]: model entry %s", j, what),
		map[string]any{"model": j, "name": r.Name, "path": r.Path, "reason": reason})
}

// RegisteredFunc reports whether this instance registers the extension
// regression t and its feature profile does not hide it.
type RegisteredFunc func(t types.RegressionType) bool

// CheckModelTypes refuses a loaded model file (LoadModelFiles' result,
// index-aligned with refs) whose type is an extension regression this
// instance does not register — or registers but hides under its feature
// profile (registered reports false for both; nil registers none):
// PULSE_MODEL_REGRESSION_UNREGISTERED with details.model, name, path
// and type. Without the registration nothing can score the model. A
// built-in type always passes. The first fault wins; the runtime and
// predict run it right after the load.
func CheckModelTypes(refs []types.ModelRef, files []*File, registered RegisteredFunc) *errors.CodedError {
	for j, f := range files {
		if f == nil || BuiltinRegression(f.Type) || (registered != nil && registered(f.Type)) {
			continue
		}
		r := refs[j]
		return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_REGRESSION_UNREGISTERED,
			fmt.Sprintf("models[%d]: model file %s holds a %s fit, a regression this instance does not register (or its feature profile hides), so nothing can score it", j, r.Path, f.Type),
			map[string]any{"model": j, "name": r.Name, "path": r.Path, "type": string(f.Type)})
	}
	return nil
}

// CheckLoadedTypes is CheckModelTypes over a request's loaded files
// (LoadFiles' result; nil loads nothing).
func CheckLoadedTypes(refs Refs, l *Loaded, registered RegisteredFunc) *errors.CodedError {
	if l == nil {
		return nil
	}
	return CheckModelTypes(refs.Models, l.Models, registered)
}
