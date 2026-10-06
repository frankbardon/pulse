package pulse

import (
	"context"
	stderrors "errors"
	"slices"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestProcess_VectorsResolveBeforeAnyRecord: through the facade, a bad
// vector is refused by Process with the predict code, and a good one
// runs and carries one PULSE_VECTOR_UNREFERENCED warning per vector no
// operator references (none can yet), leaving the data unchanged.
func TestProcess_VectorsResolveBeforeAnyRecord(t *testing.T) {
	fsys := parityFS(t)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	build := func(v ...types.VectorSpec) *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: parityCohort},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "age", Label: "s"}},
			Vectors:      v,
		}
	}

	plain, err := p.Process(ctx, build())
	if err != nil {
		t.Fatal(err)
	}
	resp, err := p.Process(ctx, build(
		types.VectorSpec{Name: "a", Fields: []string{"age"}},
		types.VectorSpec{Name: "b", Pattern: "^ag"},
	))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	var names []string
	for _, w := range resp.Warnings {
		if w.Code == string(perr.PULSE_VECTOR_UNREFERENCED) {
			names = append(names, w.Details["name"].(string))
		}
	}
	if !slices.Equal(names, []string{"a", "b"}) {
		t.Errorf("unreferenced warnings = %v, want [a b]", names)
	}
	if plain.Data[0]["s"] != resp.Data[0]["s"] {
		t.Errorf("vectors moved the data: %v vs %v", plain.Data, resp.Data)
	}
	for _, w := range plain.Warnings {
		if w.Code == string(perr.PULSE_VECTOR_UNREFERENCED) {
			t.Errorf("a vector-free request warns: %+v", w)
		}
	}

	for _, c := range []struct {
		name string
		spec types.VectorSpec
		code perr.Code
	}{
		{"categorical member", types.VectorSpec{Name: "v", Fields: []string{"region"}}, perr.PULSE_VECTOR_MEMBER_TYPE},
		{"empty", types.VectorSpec{Name: "v", Pattern: "^nothing$"}, perr.PULSE_VECTOR_EMPTY},
	} {
		t.Run(c.name, func(t *testing.T) {
			req := build(c.spec)
			_, err := p.Process(ctx, req)
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Code != c.code {
				t.Fatalf("Process err = %v, want %s", err, c.code)
			}
			res, perrr := p.Predict(ctx, req)
			if perrr != nil {
				t.Fatal(perrr)
			}
			if res.Valid {
				t.Errorf("predict accepts what Process refuses")
			}
			env, _ := p.PredictBytes(ctx, readCohortBytes(&parityHost{p: p, cohort: parityCohort}), req)
			if env == nil || len(env.Errors) == 0 || env.Errors[0].Code != string(c.code) {
				t.Errorf("predict errors = %v, want %s", env.Errors, c.code)
			}
		})
	}
}
