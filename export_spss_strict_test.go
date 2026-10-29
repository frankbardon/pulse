package pulse

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	iio "github.com/frankbardon/pulse/internal/io"
	pio "github.com/frankbardon/pulse/io"
)

// TestExport_StrictRefusesAnInexactU64: Options.Strict reaches the export as
// ExportJob.Strict, so a u64 past 2^53 bound for a `.sav` fails Export with
// PULSE_SPSS_INTEGER_INEXACT instead of warning; the same instance without
// Strict exports and warns.
func TestExport_StrictRefusesAnInexactU64(t *testing.T) {
	t.Parallel()

	fsys := afero.NewMemMapFs()
	b, err := iio.NewCohortBuild(context.Background(), fsys, "c.pulse", &encoding.Schema{Fields: []encoding.Field{
		{Name: "key", Type: encoding.FieldTypeU64, Description: "Account key"},
	}}, iio.CohortBuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []uint64{1, 1<<53 + 1} {
		if err := b.Append([]any{v}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}

	for _, strict := range []bool{false, true} {
		p, err := New(Options{FS: fsys, Strict: strict})
		if err != nil {
			t.Fatal(err)
		}
		w, err := pio.NewWriterToBuffer(pio.FormatSPSS, pio.WriterOptions{})
		if err != nil {
			t.Fatal(err)
		}
		rep, err := p.Export(context.Background(), pio.NewExportJob("c.pulse", w))
		if strict {
			var ce *perrors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != perrors.PULSE_SPSS_INTEGER_INEXACT {
				t.Errorf("Strict Export = %v, want PULSE_SPSS_INTEGER_INEXACT", err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("Export: %v", err)
		}
		found := false
		for _, w := range rep.TargetWarnings {
			found = found || w.Code == perrors.PULSE_SPSS_INTEGER_INEXACT
		}
		if !found {
			t.Errorf("TargetWarnings = %v, want PULSE_SPSS_INTEGER_INEXACT", rep.TargetWarnings)
		}
	}
}
