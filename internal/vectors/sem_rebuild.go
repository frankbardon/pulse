package vectors

import (
	"fmt"
	"slices"

	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/sem"
)

// RebuildSEMModel re-specifies a bound or loaded SEM fit's model from
// its description and options — one model for every group (a group's
// own model differs only in the fixed values and labels its matrices
// already hold) — and refuses one that does not reproduce the fit's
// stored variable orders (Observed, Inner, Latents): the stored
// matrices would be read misaligned. It is the ONE rebuild: the
// engine's scorer build (ATTR_SEM_SCORE params.fit) and the load-time
// check over a saved SEM fit file (model.CheckLoadedSEM, runtime and
// predict alike) both call it.
func RebuildSEMModel(sf *model.SEMFit) (*sem.Model, error) {
	opt := sem.ModelOptions{MeanStructure: sf.MeanStructure, GroupEqual: sf.GroupEqual, GroupPartial: sf.GroupPartial, Lavaan: sf.Lavaan}
	newModel := sem.NewModelWithOptions
	if sf.Grouped() {
		newModel = sem.NewGroupModel
	}
	m, err := newModel(sf.Model, opt)
	if err != nil {
		return nil, err
	}
	if !slices.Equal(m.Observed, sf.Observed) || !slices.Equal(m.Inner, sf.Inner) || !slices.Equal(m.Latents(), sf.Latents) {
		return nil, fmt.Errorf("its model specifies observed %v, inner %v and latents %v, the fit stores %v, %v and %v",
			m.Observed, m.Inner, m.Latents(), sf.Observed, sf.Inner, sf.Latents)
	}
	return m, nil
}

// CheckSEMRebuild is RebuildSEMModel as a model.SEMRebuildFunc.
func CheckSEMRebuild(sf *model.SEMFit) error {
	_, err := RebuildSEMModel(sf)
	return err
}
