package descriptor

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
)

// predict_models.go is predict's half of a `models` / `populations`
// load: the runtime's
// rule (model.CheckModelRefs, then model.LoadModelFiles) over the model
// files read through PredictOptions.ModelLoader — the model file and
// the cohort header + schema are all predict reads — so a bad entry is
// refused with the runtime's code, message and details before
// execution, and the loaded models' terms reach the scoring checks
// (ATTR_REG_PREDICT, coef()) exactly as the runtime binds them.

// ModelFileReader reads a model file's bytes at a path relative to the
// instance filesystem root (PredictOptions.ModelLoader; the runtime's
// Service satisfies it).
type ModelFileReader interface {
	ReadModelFile(path string) ([]byte, error)
}

// loadModelFiles is model.LoadFiles (the request's `models` and
// `populations`) through opts' ModelLoader (nil: every entry is
// PULSE_MODEL_FILE_UNREADABLE). No refs reads nothing.
func (o *PredictOptions) loadModelFiles(refs model.Refs) (*model.Loaded, *errors.CodedError) {
	if refs.Empty() {
		return nil, nil
	}
	var read model.ReadFunc
	if o != nil && o.ModelLoader != nil {
		read = o.ModelLoader.ReadModelFile
	}
	return model.LoadFiles(refs, read)
}

// predictModels loads refs against outer (the names already bound — a
// Compose slot's fits; nil for none) and returns the catalog the
// request's references resolve against: outer plus every loaded model
// and population. On a refusal the catalog is outer alone and unloaded
// names every entry, so a reference to one is not also reported
// unknown (dropUnloadedUnknowns).
func predictModels(refs model.Refs, outer model.Catalog, opts *PredictOptions) (cat model.Catalog, unloaded map[string]bool, cerr *errors.CodedError) {
	if refs.Empty() {
		return outer, nil, nil
	}
	if cerr = model.CheckRefs(refs, outer); cerr == nil {
		var loaded *model.Loaded
		if loaded, cerr = opts.loadModelFiles(refs); cerr == nil {
			return model.CatalogWithLoaded(outer, refs, loaded), nil, nil
		}
	}
	unloaded = make(map[string]bool, len(refs.Models)+len(refs.Populations))
	for _, r := range refs.Models {
		unloaded[r.Name] = true
	}
	for _, r := range refs.Populations {
		unloaded[r.Name] = true
	}
	return outer, unloaded, cerr
}

// loadedCatalog is predict's catalog for refs whose static rule already
// passed (a Compose or chain pre-pass): outer plus the loaded files.
func loadedCatalog(outer model.Catalog, refs model.Refs, loaded *model.Loaded) model.Catalog {
	if refs.Empty() {
		return outer
	}
	return model.CatalogWithLoaded(outer, refs, loaded)
}

// dropUnloadedUnknowns drops every PULSE_MODEL_UNKNOWN refusal naming a
// `models` entry that failed to load: the load refusal already reports
// it. ces is filtered in place.
func dropUnloadedUnknowns(ces []*errors.CodedError, unloaded map[string]bool) []*errors.CodedError {
	if len(unloaded) == 0 {
		return ces
	}
	out := ces[:0]
	for _, ce := range ces {
		if ce.Code == errors.PULSE_MODEL_UNKNOWN {
			if n, _ := ce.Details["name"].(string); unloaded[n] {
				continue
			}
		}
		out = append(out, ce)
	}
	return out
}
