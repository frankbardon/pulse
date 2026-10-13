package descriptor

import (
	"bytes"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/returnplan"
	"github.com/frankbardon/pulse/types"
)

// PredictedMatrixAuxiliaryKeys exposes, to the external runtime parity
// test (return_aux_parity_test.go, package descriptor_test — it imports
// the root package, which an in-package test cannot), the predict size
// model's decision for matrices[*].auxiliary: per matrix spec, in spec
// order, the auxiliary keys PredictResult.Sizes counts under req's
// effective `return` plan (the same matrixAuxiliaryNodes tree and
// sizeNode.shaped walk), sorted. A spec whose auxiliary is shaped away
// entirely is an empty list. cohort is the .pulse file's bytes.
func PredictedMatrixAuxiliaryKeys(cohort []byte, req *types.Request) ([][]string, error) {
	r := bytes.NewReader(cohort)
	v, err := encoding.ReadHeader(r)
	if err != nil {
		return nil, err
	}
	schema, err := encoding.ReadSchema(r, v)
	if err != nil {
		return nil, err
	}
	plan, err := ResolveReturn(req, nil)
	if err != nil {
		return nil, err
	}
	mult, err := ResolveMultiplicity(req, nil, nil)
	if err != nil {
		return nil, err
	}
	result := &descriptor.PredictResult{}
	plans := predictMatrices(result, req, schema, nil, nil, -1, 0, -1)
	elem := []returnplan.Segment{returnplan.Key("matrices"), returnplan.Elem()}
	out := make([][]string, len(plans))
	for i, m := range plans {
		member := mult != nil && i < len(mult.Matrices) && mult.Matrices[i].Member
		aux := key("auxiliary", matrixAuxiliaryNodes(m, result.Matrices[i], member)...)
		aux.omitEmpty = true
		keys := []string{}
		if len(aux.kids) > 0 && (plan == nil || aux.shaped(plan, elem, nil) > 0) {
			auxPath := append(append([]returnplan.Segment(nil), elem...), returnplan.Key("auxiliary"))
			for _, k := range aux.kids {
				if plan == nil || k.shaped(plan, auxPath, aux.kids) > 0 {
					keys = append(keys, k.seg.Name)
				}
			}
		}
		out[i] = sortedStrings(keys)
	}
	return out, nil
}

func sortedStrings(s []string) []string {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
	return s
}
