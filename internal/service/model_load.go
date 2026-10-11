package service

import (
	"context"
	"fmt"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// model_load.go loads `models` entries: a Request's (or FacetRequest's)
// saved model files, bound by name beside whatever the context already
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

// loadModels runs the `models` rule over refs against outer (the set
// already bound; nil for none) and returns outer plus every loaded
// model. No refs returns outer unchanged.
func (s *Service) loadModels(outer *model.Set, refs []types.ModelRef) (*model.Set, error) {
	if len(refs) == 0 {
		return outer, nil
	}
	if cerr := model.CheckModelRefs(refs, outer); cerr != nil {
		return nil, cerr
	}
	files, cerr := model.LoadModelFiles(refs, s.ReadModelFile)
	if cerr != nil {
		return nil, cerr
	}
	return outer.WithModels(refs, files)
}

// loadModelFiles is the read half alone, for a pre-pass whose static
// rule already ran (model.CheckComposeBatch / CheckChainSaves).
func (s *Service) loadModelFiles(refs []types.ModelRef) ([]*model.File, error) {
	files, cerr := model.LoadModelFiles(refs, s.ReadModelFile)
	if cerr != nil {
		return nil, cerr
	}
	return files, nil
}

// bindRequestModels returns ctx carrying the request's bound-model set:
// the set ctx already carries plus refs' loaded models. A context a
// pre-pass already bound (withModelsBound) is returned unchanged.
func (s *Service) bindRequestModels(ctx context.Context, refs []types.ModelRef) (context.Context, error) {
	if len(refs) == 0 || ctx.Value(modelsBoundKey{}) != nil {
		return ctx, nil
	}
	set, err := s.loadModels(model.SetFrom(ctx), refs)
	if err != nil {
		return ctx, err
	}
	return model.WithSet(ctx, set), nil
}
