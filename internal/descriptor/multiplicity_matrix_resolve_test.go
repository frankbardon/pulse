package descriptor

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// multiplicity_matrix_resolve_test.go pins the matrix surface of the
// one multiplicity resolver (U28 E3-S4): a MatrixSpec's own block, its
// families (`matrix` default, `request`), the implied params.inference
// membership rule, the refusals and the `request`-family conflict with
// the tests.

func mspec(typ types.MatrixType, params string, m *types.Multiplicity) types.MatrixSpec {
	s := types.MatrixSpec{Type: typ, Fields: []string{"a", "b", "c"}, Multiplicity: m}
	if params != "" {
		s.Params = json.RawMessage(params)
	}
	return s
}

func TestResolveMultiplicity_Matrices(t *testing.T) {
	t.Parallel()

	type want struct {
		method types.MultiplicityMethod
		family types.MultiplicityFamily
		alpha  float64
		member bool
		name   string
	}
	cases := []struct {
		name string
		req  *types.Request
		def  *types.Multiplicity
		want []want
	}{
		{
			name: "own block implies inference; matrix family default",
			req:  &types.Request{Matrices: []types.MatrixSpec{mspec(types.MAT_CORRELATION, "", mult(mHolm, "", 0))}},
			want: []want{{mHolm, fMatrix, 0.05, true, "MAT_CORRELATION"}},
		},
		{
			name: "own request family and alpha",
			req:  &types.Request{Matrices: []types.MatrixSpec{mspec(types.MAT_PARTIAL_CORRELATION, "", mult(mBH, fRequest, 0.01))}},
			want: []want{{mBH, fRequest, 0.01, true, "MAT_PARTIAL_CORRELATION"}},
		},
		{
			name: "inherited block reaches a spec under params.inference only",
			req: &types.Request{Multiplicity: mult(mHolm, "", 0), Matrices: []types.MatrixSpec{
				{Name: "on", Type: types.MAT_CORRELATION, Fields: []string{"a", "b"}, Params: json.RawMessage(`{"inference": true}`)},
				{Name: "off", Type: types.MAT_CORRELATION, Fields: []string{"a", "b"}},
				{Name: "cov", Type: types.MAT_COVARIANCE, Fields: []string{"a", "b"}},
			}},
			want: []want{{mHolm, fMatrix, 0.05, true, "on"}, {mHolm, fMatrix, 0.05, false, "off"}, {mHolm, fMatrix, 0.05, false, "cov"}},
		},
		{
			name: "instance default reaches an inferential spec",
			req:  &types.Request{Matrices: []types.MatrixSpec{mspec(types.MAT_CORRELATION, `{"inference": true, "method": "kendall"}`, nil)}},
			def:  mult(mBonf, "", 0.1),
			want: []want{{mBonf, fMatrix, 0.1, true, "MAT_CORRELATION"}},
		},
		{
			name: "inherited request family is offered",
			req:  &types.Request{Multiplicity: mult(mBH, fRequest, 0), Matrices: []types.MatrixSpec{mspec(types.MAT_CORRELATION, `{"inference": true}`, nil)}},
			want: []want{{mBH, fRequest, 0.05, true, "MAT_CORRELATION"}},
		},
		{
			name: "inherited overlay families fall back to matrix",
			req:  &types.Request{Matrices: []types.MatrixSpec{mspec(types.MAT_CORRELATION, `{"inference": true}`, nil)}},
			def:  mult(mHolm, fRow, 0),
			want: []want{{mHolm, fMatrix, 0.05, true, "MAT_CORRELATION"}},
		},
		{
			name: "inherited compose falls back to matrix",
			req:  &types.Request{Matrices: []types.MatrixSpec{mspec(types.MAT_CORRELATION, `{"inference": true}`, nil)}},
			def:  mult(mHolm, fCompose, 0),
			want: []want{{mHolm, fMatrix, 0.05, true, "MAT_CORRELATION"}},
		},
		{
			name: "own none opts out (inference still implied, no member)",
			req:  &types.Request{Multiplicity: mult(mHolm, "", 0), Matrices: []types.MatrixSpec{mspec(types.MAT_CORRELATION, "", mult(mNone, "", 0))}},
			want: []want{{mNone, fMatrix, 0.05, false, "MAT_CORRELATION"}},
		},
		{
			name: "unknown type is not judged here",
			req:  &types.Request{Matrices: []types.MatrixSpec{mspec("MAT_NOT_A_TYPE", "", mult(mHolm, "", 0))}},
			want: []want{{mHolm, fMatrix, 0.05, false, "MAT_NOT_A_TYPE"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plan, err := ResolveMultiplicity(c.req, c.def, nil)
			if err != nil {
				t.Fatalf("unexpected refusal: %v", err)
			}
			if len(plan.Matrices) != len(c.want) {
				t.Fatalf("matrices resolved %d, want %d", len(plan.Matrices), len(c.want))
			}
			for i, w := range c.want {
				g := plan.Matrices[i]
				if g.Method != w.method || g.Family != w.family || g.Alpha != w.alpha || g.Member != w.member || g.Name != w.name {
					t.Errorf("matrix %d = %+v, want %+v", i, g, w)
				}
				if g.Slot != "matrices["+string(rune('0'+i))+"]" {
					t.Errorf("matrix %d slot = %q", i, g.Slot)
				}
			}
			anyMember := false
			for _, w := range c.want {
				anyMember = anyMember || w.member
			}
			if plan.Active() != anyMember {
				t.Errorf("Active() = %v, want %v", plan.Active(), anyMember)
			}
		})
	}
}

// TestResolveMultiplicity_MatrixRefusals: every refusal a matrix spec's
// block can draw, plus the matrix family refused off a matrix spec.
func TestResolveMultiplicity_MatrixRefusals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		req    *types.Request
		slot   string
		key    string
		reason string
	}{
		{"row on a matrix spec", &types.Request{Matrices: []types.MatrixSpec{mspec(types.MAT_CORRELATION, "", mult("", fRow, 0))}},
			"matrices[0].multiplicity", "family", multReasonMatrixSpec},
		{"column on a matrix spec", &types.Request{Matrices: []types.MatrixSpec{mspec(types.MAT_CORRELATION, "", mult("", fColumn, 0))}},
			"matrices[0].multiplicity", "family", multReasonMatrixSpec},
		{"layer on a matrix spec", &types.Request{Matrices: []types.MatrixSpec{mspec(types.MAT_PARTIAL_CORRELATION, "", mult("", fLayer, 0))}},
			"matrices[0].multiplicity", "family", multReasonMatrixSpec},
		{"compose on a matrix spec", &types.Request{Matrices: []types.MatrixSpec{mspec(types.MAT_CORRELATION, "", mult("", fCompose, 0))}},
			"matrices[0].multiplicity", "family", multReasonMatrixSpec},
		{"block on a matrix with no p-values", &types.Request{Matrices: []types.MatrixSpec{
			mspec(types.MAT_CORRELATION, "", nil), mspec(types.MAT_PCA, "", mult(mHolm, "", 0))}},
			"matrices[1].multiplicity", "type", multReasonMatrixType},
		{"bad alpha on a matrix spec", &types.Request{Matrices: []types.MatrixSpec{mspec(types.MAT_CORRELATION, "", mult("", "", 2))}},
			"matrices[0].multiplicity", "alpha", multReasonAlphaRange},
		{"matrix family on a test", &types.Request{Tests: []*types.Test{ttest(mult("", fMatrix, 0))}},
			"tests[0].multiplicity", "family", multReasonMatrixFamily},
		{"matrix family on an overlay", &types.Request{Overlays: []types.OverlaySpec{ov(matrixKind, mult("", fMatrix, 0))}},
			"overlays[0].multiplicity", "family", multReasonMatrixFamily},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ResolveMultiplicity(c.req, nil, nil)
			wantCoded(t, err, errors.PULSE_MULTIPLICITY_INVALID, map[string]any{
				"slot": c.slot, "key": c.key, "reason": c.reason,
			})
		})
	}
	t.Run("valid list names the inference operators", func(t *testing.T) {
		_, err := ResolveMultiplicity(&types.Request{Matrices: []types.MatrixSpec{mspec(types.MAT_COVARIANCE, "", mult(mHolm, "", 0))}}, nil, nil)
		wantCoded(t, err, errors.PULSE_MULTIPLICITY_INVALID, map[string]any{
			"value": "MAT_COVARIANCE", "valid": []string{"MAT_CORRELATION", "MAT_PARTIAL_CORRELATION", "MAT_SEM"},
		})
	})
	t.Run("matrix spec offers matrix and request", func(t *testing.T) {
		_, err := ResolveMultiplicity(&types.Request{Matrices: []types.MatrixSpec{mspec(types.MAT_CORRELATION, "", mult("", fRow, 0))}}, nil, nil)
		wantCoded(t, err, errors.PULSE_MULTIPLICITY_INVALID, map[string]any{
			"valid": []string{"request", "matrix"},
		})
	})
}

// TestResolveMultiplicity_MatrixJoinsRequestConflict: a `request`-family
// matrix spec must agree with the request's tests on one method.
func TestResolveMultiplicity_MatrixJoinsRequestConflict(t *testing.T) {
	t.Parallel()

	req := &types.Request{
		Tests:    []*types.Test{ttest(mult(mHolm, "", 0))},
		Matrices: []types.MatrixSpec{mspec(types.MAT_CORRELATION, "", mult(mBH, fRequest, 0))},
	}
	_, err := ResolveMultiplicity(req, nil, nil)
	wantCoded(t, err, errors.PULSE_MULTIPLICITY_CONFLICT, map[string]any{
		"family": "request", "methods": []string{"bh", "holm"}, "slots": []string{"matrices[0]", "tests[0]"},
	})
	// The default `matrix` family is its own: no conflict.
	req.Matrices[0].Multiplicity = mult(mBH, "", 0)
	if _, err := ResolveMultiplicity(req, nil, nil); err != nil {
		t.Fatalf("matrix family conflicted with the tests: %v", err)
	}
}

// TestResolveComposeMultiplicity_MatrixSlot: a Compose slot's matrix
// spec resolves inside the slot's plan, `compose` refused on it.
func TestResolveComposeMultiplicity_MatrixSlot(t *testing.T) {
	t.Parallel()

	composed := &types.ComposedRequest{Requests: []*types.Request{
		{Matrices: []types.MatrixSpec{mspec(types.MAT_CORRELATION, "", mult(mHolm, "", 0))}},
	}}
	plan, err := ResolveComposeMultiplicity(composed, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Requests[0].Matrices; len(got) != 1 || !got[0].Member || got[0].Family != fMatrix {
		t.Fatalf("slot matrices = %+v", got)
	}
	composed.Requests[0].Matrices[0].Multiplicity = mult(mHolm, fCompose, 0)
	_, err = ResolveComposeMultiplicity(composed, nil, nil)
	wantCoded(t, err, errors.PULSE_MULTIPLICITY_INVALID, map[string]any{
		"slot": "matrices[0].multiplicity", "reason": multReasonMatrixSpec, "request": 0,
	})
	// An inherited compose family falls back to matrix.
	composed.Requests[0].Matrices[0].Multiplicity = mult(mHolm, "", 0)
	composed.Multiplicity = mult("", fCompose, 0)
	plan, err = ResolveComposeMultiplicity(composed, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Requests[0].Matrices[0].Family; got != fMatrix {
		t.Errorf("inherited compose family resolved to %s, want matrix", got)
	}
}

// TestPredict_MatrixManyTestsAdvisory: a 5-member inferential matrix
// emits 10 uncorrected p-values and draws PULSE_ADVISORY_MANY_TESTS;
// a block on the spec corrects them and silences it.
func TestPredict_MatrixManyTestsAdvisory(t *testing.T) {
	t.Parallel()

	fields := []encoding.Field{}
	members := []string{"a", "b", "c", "d", "e"}
	for _, m := range members {
		fields = append(fields, encoding.Field{Name: m, Type: encoding.FieldTypeF64, Description: "Measured score " + m})
	}
	cohort := buildTestPulseFile(t, &encoding.Schema{Fields: fields})
	spec := types.MatrixSpec{Type: types.MAT_CORRELATION, Fields: members, Params: json.RawMessage(`{"inference": true}`)}
	has := func(req *types.Request) (bool, *descriptor.PValueCount) {
		res := predictFromBytes(cohort, req, &PredictOptions{}).Data.(*descriptor.PredictResult)
		for _, a := range res.Advisories {
			if a.Code == string(errors.PULSE_ADVISORY_MANY_TESTS) {
				return true, res.PValues
			}
		}
		return false, res.PValues
	}
	if got, pv := has(&types.Request{Matrices: []types.MatrixSpec{spec}}); !got || pv.Uncorrected != 10 {
		t.Errorf("uncorrected matrix: advisory %v, p-values %+v; want the advisory over 10", got, pv)
	}
	spec.Multiplicity = mult(mBH, "", 0)
	if got, pv := has(&types.Request{Matrices: []types.MatrixSpec{spec}}); got || pv.Uncorrected != 0 || pv.Total != 10 {
		t.Errorf("corrected matrix: advisory %v, p-values %+v; want none, 10 corrected", got, pv)
	}
}

// TestResolveMultiplicity_SEMOwnBlockOnly: MAT_SEM's own block makes it
// a member over its estimates' p_value column (twins p_adjusted /
// significant_adjusted); a request-level block or an instance default
// never reaches it, and a method none keeps it out.
func TestResolveMultiplicity_SEMOwnBlockOnly(t *testing.T) {
	t.Parallel()

	own, err := ResolveMultiplicity(&types.Request{Matrices: []types.MatrixSpec{mspec(types.MAT_SEM, "", mult(mHolm, "", 0))}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	rm := own.Matrices[0]
	if !rm.Member || rm.PValueKey != SEMPValueKey || rm.PValueColumn != SEMPValueColumn || rm.Family != types.MultiplicityFamilyMatrix {
		t.Errorf("own block: %+v", rm)
	}
	for name, c := range map[string]struct {
		req *types.Request
		def *types.Multiplicity
	}{
		"request block":    {&types.Request{Multiplicity: mult(mHolm, "", 0), Matrices: []types.MatrixSpec{mspec(types.MAT_SEM, "", nil)}}, nil},
		"instance default": {&types.Request{Matrices: []types.MatrixSpec{mspec(types.MAT_SEM, "", nil)}}, mult(mBH, "", 0)},
		"own none":         {&types.Request{Matrices: []types.MatrixSpec{mspec(types.MAT_SEM, "", mult(mNone, "", 0))}}, mult(mBH, "", 0)},
	} {
		plan, err := ResolveMultiplicity(c.req, c.def, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if plan != nil && len(plan.Matrices) > 0 && plan.Matrices[0].Member {
			t.Errorf("%s: MAT_SEM is a member", name)
		}
	}
}
