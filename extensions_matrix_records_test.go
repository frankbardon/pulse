package pulse

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// extRecordsReg registers MAT_ACME_REC: the ddof-1 covariance as its
// primary (through Square), the same matrix written upper by hand as
// auxiliary.tri, and a rectangular auxiliary.rect whose one column is
// named col.
func extRecordsReg(col string) MatrixOpRegistration {
	return MatrixOpRegistration{
		Name:        "MAT_ACME_REC",
		Description: "Covariance with a hand-written upper twin and a one-column rectangle (records test operator).",
		Factory: func(*types.MatrixSpec, *encoding.Schema) (extend.MatrixOperator, error) {
			return probeFuncMatrixOp(func(in *extend.MatrixInput) (extend.MatrixOutput, error) {
				cov := in.CoMoment.Cov(1)
				p := len(in.Members)
				tri := &types.MatrixValues{Kind: types.MatrixKindSquareSymmetric, Encoding: types.MatrixEncodingUpper,
					RowKeys: in.Members, ColumnKeys: in.Members, Values: make([][]float64, p)}
				for r := range p {
					for c := r; c < p; c++ {
						tri.Values[r] = append(tri.Values[r], cov.At(r, c))
					}
				}
				return extend.MatrixOutput{Primary: in.Square(cov.At), Auxiliary: map[string]*types.MatrixValues{
					"tri":  tri,
					"rect": in.Rect([]string{col}, func(r, _ int) float64 { return cov.At(r, r) }),
				}}, nil
			}), nil
		},
		Streamable: true,
		OutputKeys: descriptor.MatrixOutputKeys{Primary: "covariance", Auxiliary: []string{"rect", "tri"}},
	}
}

// TestExtensionMatrix_Records: encoding records stamps every part an
// extension returns — its upper-written one expanded, its rectangle
// too — with MAT_COVARIANCE's full bits; a column the operator names
// "key" fails the slot alone.
func TestExtensionMatrix_Records(t *testing.T) {
	fsys := extMatrixCohort(t)
	ctx := context.Background()
	p := extMatrixPulse(t, fsys, Options{}, extRecordsReg("variance"))
	want, err := p.Process(ctx, extMatrixRequest(types.MAT_COVARIANCE, "", false, false))
	if err != nil {
		t.Fatal(err)
	}
	req := extMatrixRequest("MAT_ACME_REC", "", false, false)
	req.Matrices[0].Encoding = types.MatrixEncodingRecords
	got, err := p.Process(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	m := got.Matrices[0]
	if m.Error != nil {
		t.Fatalf("slot failed: %+v", m.Error)
	}
	tri := m.Auxiliary["tri"]
	for name, v := range map[string]*types.MatrixValues{"primary": m.Primary, "tri": tri, "rect": m.Auxiliary["rect"]} {
		if v.Encoding != types.MatrixEncodingRecords {
			t.Errorf("%s encoding %q, want records", name, v.Encoding)
		}
	}
	full := want.Matrices[0].Primary
	for name, v := range map[string]*types.MatrixValues{"primary": m.Primary, "tri": tri} {
		for r := range full.Values {
			for c, x := range full.Values[r] {
				if y := v.Values[r][c]; math.Float64bits(x) != math.Float64bits(y) {
					t.Errorf("%s[%d][%d] = %v, MAT_COVARIANCE %v", name, r, c, y, x)
				}
			}
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(b); strings.Contains(s, `"values"`) || !strings.Contains(s, `"columns":["variance"]`) {
		t.Errorf("records wire %s", s)
	}

	p = extMatrixPulse(t, fsys, Options{}, extRecordsReg(types.MatrixRecordKey))
	got, err = p.Process(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if e := got.Matrices[0].Error; e == nil || e.Details["reason"] != "records_key_collision" {
		t.Fatalf("a key column: slot error %+v, want records_key_collision", e)
	}
}
