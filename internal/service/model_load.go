package service

import (
	"context"
	"fmt"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/internal/model"
)

// model_load.go loads `models` and `populations` entries: a Request's
// (or FacetRequest's) saved model files and a Request's saved
// population files, bound by name beside whatever the context already
// binds (a Compose slot's fits). The rules are internal/model's
// (CheckModelRefs, LoadModelFiles — shared with predict); the read is
// here, through the instance afero.Fs.
//
// A Compose batch and a chain load every entry in their pre-pass,
// before any fit, slot or stage runs (so a refusal leaves nothing
// written and every load sees the files as they were before the run),
// and hand each request a context already carrying its final set —
// marked, so the request's own Process does not load again.

// modelsBoundKey marks a context whose bound-model set already holds
// the request's own `models` (a Compose or chain pre-pass loaded them).
type modelsBoundKey struct{}

// withModelsBound returns ctx carrying set as the request's final
// bound-model set (nil binds nothing), marked loaded.
func withModelsBound(ctx context.Context, set *model.Set) context.Context {
	return context.WithValue(model.WithSet(ctx, set), modelsBoundKey{}, true)
}

// SetPopulations installs the instance's registered populations
// (model.RegisteredSet; nil for none): the outermost layer of every
// request's bound-model set. Called once, by pulse.New.
func (s *Service) SetPopulations(set *model.Set) { s.populations = set }

// populationCatalog is the registered populations as the static rule's
// outer Catalog — nil (not a typed nil) when none are registered, so a
// rule judging against it is exactly the one judging against nothing.
func (s *Service) populationCatalog() model.Catalog {
	if s.populations == nil {
		return nil
	}
	return s.populations
}

// ReadModelFile reads a model file's bytes at path (relative to the
// instance filesystem root) — the read every `models` load uses, and
// the facade's predict loader (PredictOptions.ModelLoader).
func (s *Service) ReadModelFile(path string) ([]byte, error) {
	fsys := s.fs.Fs()
	// A directory is unreadable as a file on every backend (an
	// in-memory one would read it as empty).
	if info, err := fsys.Stat(path); err != nil {
		return nil, err
	} else if info.IsDir() {
		return nil, fmt.Errorf("%s is a directory", path)
	}
	return afero.ReadFile(fsys, path)
}

// loadModels runs the load rule over refs — the request's `models` and
// `populations` (model.CheckRefs) — against outer (the set already
// bound; nil for none) and returns outer plus every loaded model and
// population. No refs returns outer unchanged.
func (s *Service) loadModels(outer *model.Set, refs model.Refs) (*model.Set, error) {
	if refs.Empty() {
		return outer, nil
	}
	if cerr := model.CheckRefs(refs, outer); cerr != nil {
		return nil, cerr
	}
	loaded, cerr := model.LoadFiles(refs, s.ReadModelFile)
	if cerr != nil {
		return nil, cerr
	}
	return outer.WithLoaded(refs, loaded)
}

// loadModelFiles is the read half alone, for a pre-pass whose static
// rule already ran (model.CheckComposeBatch / CheckChainSaves).
func (s *Service) loadModelFiles(refs model.Refs) (*model.Loaded, error) {
	loaded, cerr := model.LoadFiles(refs, s.ReadModelFile)
	if cerr != nil {
		return nil, cerr
	}
	return loaded, nil
}

// bindRequestModels returns ctx carrying the request's bound-model set:
// the set ctx already carries — else the instance's registered
// populations — plus refs' loaded models and populations. A context a
// pre-pass already bound (withModelsBound) is returned unchanged.
func (s *Service) bindRequestModels(ctx context.Context, refs model.Refs) (context.Context, error) {
	if ctx.Value(modelsBoundKey{}) != nil {
		return ctx, nil
	}
	outer := model.SetFrom(ctx)
	if outer == nil {
		outer = s.populations
	}
	if refs.Empty() {
		return model.WithSet(ctx, outer), nil
	}
	set, err := s.loadModels(outer, refs)
	if err != nil {
		return ctx, err
	}
	return model.WithSet(ctx, set), nil
}
