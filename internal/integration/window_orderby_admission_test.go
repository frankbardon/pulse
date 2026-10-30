package integration_test

import (
	"context"
	stderrors "errors"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestWindowOrderBy_AdmitsAllButSetAndVector pins the claim every
// op-win-* skill makes about its `order_by` key: every registered field
// type is admitted EXCEPT set_* (PULSE_WINDOW_INVALID) and vec_*
// (PULSE_VECTOR_FIELD_NOT_SCALAR). It walks the whole type-byte space,
// so a new field type is pressed the day it lands, and for each type it
// asserts predict and the runtime reach the same verdict with the same
// code. The expectation is derived from the type's own predicates, not
// from the orderability helper the engine uses, so the skill wording and
// the engine cannot drift together unnoticed.
func TestWindowOrderBy_AdmitsAllButSetAndVector(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	walked := 0
	for b := 0; b < 256; b++ {
		ft := encoding.FieldType(b)
		if !ft.IsKnown() {
			break
		}
		walked++
		var want errors.Code
		switch {
		case ft.IsSet():
			want = errors.PULSE_WINDOW_INVALID
		case ft.IsVector():
			want = errors.PULSE_VECTOR_FIELD_NOT_SCALAR
		}
		t.Run(ft.String(), func(t *testing.T) {
			path := "ob_" + ft.String() + ".pulse"
			writeOrderByCohort(t, p, path, ft)
			req := &types.Request{Cohort: &types.Cohort{Filename: path},
				Windows: []*types.Window{{Type: types.WIN_ROW_NUMBER, Label: "rn", OrderBy: []types.OrderKey{{Field: "k"}}}},
				Sort:    []types.OrderKey{{Field: "id"}}}

			_, rtErr := p.Process(ctx, req)
			var rtCode errors.Code
			if rtErr != nil {
				var ce *errors.CodedError
				if !stderrors.As(rtErr, &ce) {
					t.Fatalf("runtime error is uncoded: %v", rtErr)
				}
				rtCode = ce.Code
			}
			env := predictEnvelope(t, p, fs, path, req)
			var prCode errors.Code
			if len(env.Errors) > 0 {
				prCode = errors.Code(env.Errors[0].Code)
			}

			if rtCode != want {
				t.Errorf("runtime verdict = %q (%v), want %q", rtCode, rtErr, want)
			}
			if prCode != want {
				t.Errorf("predict verdict = %q (%+v), want %q", prCode, env.Errors, want)
			}
		})
	}
	if walked == 0 {
		t.Fatal("walked no field types; the type-byte walk is broken")
	}
}

// writeOrderByCohort writes a three-row cohort {id u8, k <ft> nullable}
// with k null on every row, so any field type builds without a value
// encoder of its own; the order_by verdict depends only on the type.
func writeOrderByCohort(t *testing.T, p *pulse.Pulse, path string, ft encoding.FieldType) {
	t.Helper()
	k := encoding.Field{Name: "k", Type: ft, Nullable: true}
	if ft.HasDictionary() {
		d := encoding.NewDictionary()
		if _, err := d.Add("a"); err != nil {
			t.Fatal(err)
		}
		k.Dictionary = d
	}
	if ft.IsDecimal() {
		k.Precision, k.Scale = 18, 2
	}
	sch := encoding.Schema{Fields: []encoding.Field{{Name: "id", Type: encoding.FieldTypeU8}, k}}
	if ft.IsVector() {
		sch.Vectors = []encoding.VectorField{{FieldIndex: 1, Dim: 2}}
	}
	b, err := p.NewCohortBuilder(context.Background(), path, sch, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatalf("builder for %s: %v", ft, err)
	}
	for i := 1; i <= 3; i++ {
		if err := b.Append(pulse.CohortRow{uint64(i), nil}); err != nil {
			t.Fatalf("append %s row %d: %v", ft, i, err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatalf("close %s: %s", ft, fmt.Sprint(err))
	}
}
