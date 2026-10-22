package processing

import (
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// matrix_records.go is MatrixSpec.encoding "records": every
// matrix of the result — primary and each auxiliary, rectangular ones
// included, built-in or extension — is stamped records in the full
// layout, and the wire writes it as row records (types.MatrixValues
// MarshalJSON / Records). The finalizers render a records spec exactly
// as a full one (only "upper" trims rows), so the numbers are the full
// encoding's by construction; stamping happens once, here, after the
// finalizer, so no operator needs to know about it.

// recordsEncode stamps every matrix of res records, each a copy (a
// finalizer may hand back a matrix it shares). An upper layout (an
// extension that wrote its own) is expanded first. It returns the
// slot failure a data-derived column named types.MatrixRecordKey
// causes (a bucket label, an extension's column key) — the member
// names are refused before the run (vectors.recordsKeyRefusal).
func recordsEncode(res *types.MatrixResult) *types.SlotError {
	stamp := func(part string, v *types.MatrixValues) (*types.MatrixValues, *types.SlotError) {
		if v == nil {
			return nil, nil
		}
		for _, c := range v.ColumnKeys {
			if c == types.MatrixRecordKey {
				return nil, &types.SlotError{
					Code: string(errors.SERVICE_VALIDATION),
					Message: "matrix " + res.Name + ": " + part + " has a column named " + strconv.Quote(c) +
						", which encoding \"records\" reserves for each row's key; use full or upper",
					Details: map[string]any{"matrix": res.Name, "part": part, "reason": "records_key_collision"},
				}
			}
		}
		out := *v
		if v.Encoding == types.MatrixEncodingUpper && v.Kind == types.MatrixKindSquareSymmetric {
			out.Values = ExpandMatrixValues(v)
		}
		out.Encoding = types.MatrixEncodingRecords
		return &out, nil
	}
	p, se := stamp("primary", res.Primary)
	if se != nil {
		return se
	}
	res.Primary = p
	if res.Auxiliary != nil {
		aux := make(map[string]*types.MatrixValues, len(res.Auxiliary))
		for k, v := range res.Auxiliary {
			s, se := stamp("auxiliary."+k, v)
			if se != nil {
				return se
			}
			aux[k] = s
		}
		res.Auxiliary = aux
	}
	return nil
}
