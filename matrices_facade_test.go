package pulse

import (
	"context"
	stderrors "errors"
	"slices"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestProcess_MatricesRefuseLikePredict: through the facade, a matrix
// spec Process refuses is refused by Predict with the same code, a good
// one runs, predicts valid, and references its vector (no
// PULSE_VECTOR_UNREFERENCED for it), and the projected run equals the
// unprojected one.
func TestProcess_MatricesRefuseLikePredict(t *testing.T) {
	fsys := afero.NewMemMapFs()
	var rows [][]string
	for i := 0; i < 30; i++ {
		rows = append(rows, []string{
			jsonNumber(1.5 + float64((i*7)%11)),
			jsonNumber(2.25*float64(i%5) + 0.5),
			jsonNumber(float64((i * 13) % 17)),
			[]string{"north", "south"}[i%2],
		})
	}
	createTestPulseFile(t, fsys, "m.pulse", []string{"a", "b", "c", "region"}, rows)
	ctx := context.Background()
	build := func(m ...types.MatrixSpec) *types.Request {
		return &types.Request{
			Cohort:   &types.Cohort{Filename: "m.pulse"},
			Vectors:  []types.VectorSpec{{Name: "ab", Fields: []string{"a", "b"}}, {Name: "spare", Fields: []string{"c"}}},
			Matrices: m,
		}
	}
	for _, disable := range []bool{false, true} {
		p, err := New(Options{FS: fsys, DisableProjection: disable})
		if err != nil {
			t.Fatal(err)
		}
		resp, err := p.Process(ctx, build(types.MatrixSpec{Type: types.MAT_COVARIANCE, Vector: "ab"}))
		if err != nil {
			t.Fatalf("Process(disableProjection=%v): %v", disable, err)
		}
		if len(resp.Matrices) != 1 || !slices.Equal(resp.Matrices[0].Primary.RowKeys, []string{"a", "b"}) {
			t.Fatalf("matrices = %+v", resp.Matrices)
		}
		var unref []string
		for _, w := range resp.Warnings {
			if w.Code == string(perr.PULSE_VECTOR_UNREFERENCED) {
				unref = append(unref, w.Details["name"].(string))
			}
		}
		if !slices.Equal(unref, []string{"spare"}) {
			t.Errorf("unreferenced = %v, want [spare]", unref)
		}
	}

	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	// Projected decode reads the members (a real decode, not a unit
	// probe): the matrix over the projected columns equals the one an
	// unprojected instance computes.
	q, err := New(Options{FS: fsys, DisableProjection: true})
	if err != nil {
		t.Fatal(err)
	}
	inline := &types.Request{
		Cohort:       &types.Cohort{Filename: "m.pulse"},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "region", Label: "n"}},
		Matrices:     []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Name: "abc", Fields: []string{"a", "b", "c"}}},
	}
	pr, err := p.Process(ctx, inline)
	if err != nil {
		t.Fatal(err)
	}
	ur, err := q.Process(ctx, inline)
	if err != nil {
		t.Fatal(err)
	}
	if v := pr.Matrices[0].Primary.Values[0][0]; v != v || v == 0 {
		t.Fatalf("projected variance of a = %v, want a real spread", v)
	}
	for r := range pr.Matrices[0].Primary.Values {
		if !slices.Equal(pr.Matrices[0].Primary.Values[r], ur.Matrices[0].Primary.Values[r]) {
			t.Errorf("projected row %d %v != unprojected %v", r, pr.Matrices[0].Primary.Values[r], ur.Matrices[0].Primary.Values[r])
		}
	}
	if res, err := p.Predict(ctx, inline); err != nil || !res.Valid {
		t.Errorf("predict of a good matrix request: valid=%v err=%v", res != nil && res.Valid, err)
	}

	for _, c := range []struct {
		name string
		spec types.MatrixSpec
		code perr.Code
	}{
		{"undefined vector", types.MatrixSpec{Type: types.MAT_COVARIANCE, Vector: "nope"}, perr.PULSE_VECTOR_UNKNOWN},
		{"vector and fields", types.MatrixSpec{Type: types.MAT_COVARIANCE, Vector: "ab", Fields: []string{"a"}}, perr.SERVICE_VALIDATION},
		{"categorical inline member", types.MatrixSpec{Type: types.MAT_COVARIANCE, Fields: []string{"region"}}, perr.PULSE_VECTOR_MEMBER_TYPE},
		{"unknown type", types.MatrixSpec{Type: "MAT_NOPE", Vector: "ab"}, perr.SERVICE_VALIDATION},
	} {
		t.Run(c.name, func(t *testing.T) {
			req := build(c.spec)
			_, err := p.Process(ctx, req)
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Code != c.code {
				t.Fatalf("Process err = %v, want %s", err, c.code)
			}
			data, rerr := afero.ReadFile(fsys, "m.pulse")
			if rerr != nil {
				t.Fatal(rerr)
			}
			env, _ := p.PredictBytes(ctx, data, req)
			if env == nil || len(env.Errors) == 0 || env.Errors[0].Code != string(c.code) {
				t.Errorf("predict errors = %v, want %s first", env.Errors, c.code)
			}
		})
	}
}
