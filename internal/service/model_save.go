package service

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// model_save.go writes model files — a RegressionSpec.Save after the
// request's run (saveRegressionModels) and a regression FitSpec.Save
// after the Compose fit pre-pass (saveFitModel) — and population files
// (a vector FitSpec.Save, saveFitPopulation) and SEM fit files (a SEM
// FitSpec.Save, saveFitSEM). The format and the path rule are
// internal/model's (shared with predict); the write is here, through the
// instance afero.Fs, atomically.

// modelTempPattern names a save's staging file beside its target. It
// ends in ".tmp" — never ".json" — so a crash-orphaned temp file is
// never mistaken for a table by a *.json directory loader.
const modelTempPattern = ".*.tmp"

// saveRegressionModels writes every RegressionSpec.Save of req from
// resp's full-precision results (before any `return` shaping) and
// appends a PULSE_MODEL_OVERWRITTEN warning per replaced file. The
// paths passed model.CheckRegressionSaves before dispatch.
func (s *Service) saveRegressionModels(ctx context.Context, req *types.Request, resp *types.Response) error {
	if resp == nil {
		return nil
	}
	var prov *model.Provenance
	for i, spec := range req.Regressions {
		if spec == nil || spec.Save == "" {
			continue
		}
		if i >= len(resp.Regressions) || resp.Regressions[i] == nil {
			return model.WriteFailed(spec.Save, spec.Name, "result", fmt.Errorf("regression %d produced no result", i))
		}
		res := resp.Regressions[i]
		if res.Error != nil {
			// A fit that failed on its data has no model to write: no
			// file, and its PULSE_SLOT_FAILED warning names the slot
			// (under Strict the run already failed).
			continue
		}
		// The regression's name; an unnamed one is named after its
		// file ("models/mmm.pulse-model.json" → "mmm").
		name := spec.Name
		if name == "" {
			name = strings.TrimSuffix(filepath.Base(spec.Save), model.FileSuffix)
		}
		if prov == nil {
			p := s.modelProvenance(req)
			prov = &p
		}
		overwrote, err := s.writeModel(ctx, spec.Save, name, spec, res, *prov)
		if err != nil {
			return err
		}
		if overwrote {
			w := model.Overwritten(spec.Save, name)
			w.Details["regression"] = i
			resp.Warnings = append(resp.Warnings, &types.ResponseWarning{
				Code: string(w.Code), Message: w.Message, Details: w.Details,
			})
		}
	}
	return nil
}

// saveFitModel writes fit's model file (FitSpec.Save) under the fit's
// name from its full-precision result. It returns the
// PULSE_MODEL_OVERWRITTEN warning (details.fit = i) when it replaced a
// file, nil otherwise.
func (s *Service) saveFitModel(ctx context.Context, i int, fit types.FitSpec, res *types.RegressionResult) (*types.ResponseWarning, error) {
	if fit.Save == "" {
		return nil, nil
	}
	overwrote, err := s.writeModel(ctx, fit.Save, fit.Name, fit.Request.Regressions[0], res, s.modelProvenance(fit.Request))
	if err != nil || !overwrote {
		return nil, err
	}
	w := model.Overwritten(fit.Save, fit.Name)
	w.Details["fit"] = i
	return &types.ResponseWarning{Code: string(w.Code), Message: w.Message, Details: w.Details}, nil
}

// writeModel encodes the model file and writes it to path atomically;
// overwrote reports whether a file was replaced.
func (s *Service) writeModel(ctx context.Context, path, name string, spec *types.RegressionSpec, res *types.RegressionResult, prov model.Provenance) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	f, err := model.NewFile(name, spec, res, prov)
	if err != nil {
		return false, err
	}
	data, err := f.Encode()
	if err != nil {
		return false, err
	}
	return writeModelFile(s.fs.Fs(), path, name, data, model.WriteFailed)
}

// saveFitPopulation writes vector fit i's population file
// (FitSpec.Save) under the fit's name from its closed state. It returns
// the PULSE_MODEL_OVERWRITTEN warning (details.fit = i) when it
// replaced a file, nil otherwise.
func (s *Service) saveFitPopulation(ctx context.Context, i int, fit types.FitSpec, vf *model.VectorFit) (*types.ResponseWarning, error) {
	if fit.Save == "" {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := model.NewPopulationFile(fit.Name, vf, s.modelProvenance(fit.Request))
	if err != nil {
		return nil, err
	}
	data, err := f.Encode()
	if err != nil {
		return nil, err
	}
	overwrote, err := writeModelFile(s.fs.Fs(), fit.Save, fit.Name, data, model.PopulationWriteFailed)
	if err != nil || !overwrote {
		return nil, err
	}
	w := model.PopulationOverwritten(fit.Save, fit.Name)
	w.Details["fit"] = i
	return &types.ResponseWarning{Code: string(w.Code), Message: w.Message, Details: w.Details}, nil
}

// saveFitSEM writes SEM fit i's SEM fit file (FitSpec.Save) under the
// fit's name from its bound state. It returns the
// PULSE_MODEL_OVERWRITTEN warning (details.fit = i) when it replaced a
// file, nil otherwise.
func (s *Service) saveFitSEM(ctx context.Context, i int, fit types.FitSpec, sf *model.SEMFit) (*types.ResponseWarning, error) {
	if fit.Save == "" {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := model.NewSEMFile(fit.Name, sf, s.modelProvenance(fit.Request))
	if err != nil {
		return nil, err
	}
	data, err := f.Encode()
	if err != nil {
		return nil, err
	}
	overwrote, err := writeModelFile(s.fs.Fs(), fit.Save, fit.Name, data, model.SEMWriteFailed)
	if err != nil || !overwrote {
		return nil, err
	}
	w := model.SEMOverwritten(fit.Save, fit.Name)
	w.Details["fit"] = i
	return &types.ResponseWarning{Code: string(w.Code), Message: w.Message, Details: w.Details}, nil
}

// writeModelFile writes data — a model, population or SEM fit file — to path
// via temp + fsync + rename (the shard-admin atomicWrite pattern),
// creating the parent directory. A failure at any stage is
// PULSE_MODEL_WRITE_FAILED (built by failed: model.WriteFailed,
// model.PopulationWriteFailed or model.SEMWriteFailed) and leaves path as it was — absent, or
// its previous content — with the temp file removed.
func writeModelFile(fsys afero.Fs, path, name string, data []byte, failed func(path, name, stage string, cause error) *errors.CodedError) (overwrote bool, err error) {
	fail := func(stage string, cause error) (bool, error) {
		return false, failed(path, name, stage, cause)
	}
	info, statErr := fsys.Stat(path)
	switch {
	case statErr == nil && info.IsDir():
		return fail("stat", fmt.Errorf("%s is a directory", path))
	case statErr == nil:
		overwrote = true
	case !os.IsNotExist(statErr):
		return fail("stat", statErr)
	}
	dir := filepath.Dir(path)
	if err := fsys.MkdirAll(dir, 0o755); err != nil {
		return fail("mkdir", err)
	}
	// Staged BESIDE path, never in "" (afero.TempFile maps "" to
	// os.TempDir(), outside a BasePathFs root) — see atomicWrite.
	tmp, err := afero.TempFile(fsys, dir, filepath.Base(path)+modelTempPattern)
	if err != nil {
		return fail("create", err)
	}
	tmpName := tmp.Name()
	abort := func(stage string, cause error) (bool, error) {
		_ = tmp.Close()
		_ = fsys.Remove(tmpName)
		return fail(stage, cause)
	}
	if _, err := tmp.Write(data); err != nil {
		return abort("write", err)
	}
	// fsync BEFORE the rename: a rename landing while the bytes are only
	// in the page cache publishes a name a crash can still truncate.
	if err := tmp.Sync(); err != nil {
		return abort("sync", err)
	}
	if err := tmp.Close(); err != nil {
		_ = fsys.Remove(tmpName)
		return fail("close", err)
	}
	if err := fsys.Rename(tmpName, path); err != nil {
		_ = fsys.Remove(tmpName)
		return fail("rename", err)
	}
	return overwrote, nil
}

// modelProvenance is the informational origin a model file records:
// the training cohort path, its content fingerprint (omitted when the
// file cannot be stat'd or hashed — never a refusal) and the training
// request's canonical hash.
func (s *Service) modelProvenance(req *types.Request) model.Provenance {
	var p model.Provenance
	if req == nil {
		return p
	}
	p.RequestDigest = req.Hash()
	if req.Cohort == nil {
		return p
	}
	path := resolveCohortPath(req.Cohort)
	p.Cohort = path
	fsys := s.fs.Fs()
	size, mod, err := statCohortFile(fsys, path)
	if err != nil {
		return p
	}
	fp, err := s.cohortFingerprint(fsys, path, size, mod, true)
	if err != nil {
		return p
	}
	p.Fingerprint = "sha256:" + hex.EncodeToString(fp[:])
	return p
}
