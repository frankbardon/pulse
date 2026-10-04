package io

import (
	"context"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

func TestCheckSchemaDescriptions(t *testing.T) {
	ok := &encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeU8, Description: strings.Repeat("x", encoding.MaxDescriptionBytes)},
	}}
	if err := checkSchemaDescriptions(ok); err != nil {
		t.Fatalf("a description of exactly the cap is legal: %v", err)
	}
	long := &encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeU8},
		{Name: "b", Type: encoding.FieldTypeU8, Description: strings.Repeat("x", encoding.MaxDescriptionBytes+1)},
	}}
	err := checkSchemaDescriptions(long)
	if !perr.HasCode(err, perr.PULSE_IMPORT_DESCRIPTION_TOO_LONG) {
		t.Fatalf("err = %v, want PULSE_IMPORT_DESCRIPTION_TOO_LONG", err)
	}
	if ce, _ := err.(*perr.CodedError); ce == nil || ce.Details["field"] != "b" {
		t.Fatalf("details must name the field: %#v", err)
	}
}

// panicRowsReader fails the test if the row pass starts.
type panicRowsReader struct {
	t      *testing.T
	header []string
}

func (r *panicRowsReader) ReadHeader() ([]string, error) { return r.header, nil }
func (r *panicRowsReader) ReadRows(context.Context, func([]string) error) error {
	r.t.Fatal("row pass ran although the explicit schema was refused")
	return nil
}
func (r *panicRowsReader) Close() error { return nil }

// An over-long description on a user-authored schema is refused before
// the row pass, with the schema writer's own code, and writes nothing.
func TestImport_ExplicitDescriptionTooLong_RefusedBeforeRows(t *testing.T) {
	fs := afero.NewMemMapFs()
	job := &ImportJob{
		Source: &panicRowsReader{t: t, header: []string{"a"}},
		Target: "out.pulse",
		FS:     fs,
		Schema: &encoding.Schema{Fields: []encoding.Field{
			{Name: "a", Type: encoding.FieldTypeU8, Description: strings.Repeat("d", encoding.MaxDescriptionBytes+1)},
		}},
	}
	_, err := job.Run(context.Background())
	if !perr.HasCode(err, perr.PULSE_IMPORT_DESCRIPTION_TOO_LONG) {
		t.Fatalf("err = %v, want PULSE_IMPORT_DESCRIPTION_TOO_LONG", err)
	}
	if exists, _ := afero.Exists(fs, "out.pulse"); exists {
		t.Fatal("a refused import must not write the target")
	}
}
