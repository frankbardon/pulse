package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	iio "github.com/frankbardon/pulse/internal/io"
	"github.com/spf13/afero"
)

// TestExportSPSS_StrictRefusesAnInexactU64: `pulse export spss --strict` is
// the CLI spelling of Options.Strict / ExportJob.Strict. A u64 past 2^53 —
// which no SPSS double holds exactly — warns PULSE_SPSS_INTEGER_INEXACT and
// exports without it; with it the export fails with that code (exit 1, the
// code on the --json envelope) and writes no .sav.
func TestExportSPSS_StrictRefusesAnInexactU64(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cohort := filepath.Join(dir, "c.pulse")
	b, err := iio.NewCohortBuild(context.Background(), afero.NewOsFs(), cohort, &encoding.Schema{Fields: []encoding.Field{
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

	run := func(args ...string) (runtimeWarningEnvelope, error) {
		t.Helper()
		root := ExportCommand()
		var out bytes.Buffer
		root.Writer = &out
		err := root.Run(context.Background(), append([]string{"export", "spss", "-i", cohort, "--json"}, args...))
		var env runtimeWarningEnvelope
		if jerr := json.Unmarshal(out.Bytes(), &env); jerr != nil {
			t.Fatalf("decoding the envelope %q: %v", out.String(), jerr)
		}
		return env, err
	}

	lenient := filepath.Join(dir, "lenient.sav")
	env, err := run("-o", lenient)
	if err != nil || len(env.Errors) != 0 {
		t.Fatalf("export spss without --strict: %v %v", err, env.Errors)
	}
	if len(entriesWithCode(env.Warnings, string(perrors.PULSE_SPSS_INTEGER_INEXACT))) == 0 {
		t.Errorf("warnings = %v, want PULSE_SPSS_INTEGER_INEXACT", env.Warnings)
	}
	if _, err := os.Stat(lenient); err != nil {
		t.Errorf("the lenient export wrote no .sav: %v", err)
	}

	strict := filepath.Join(dir, "strict.sav")
	env, err = run("-o", strict, "--strict")
	wantEnvelopeFatal(t, err)
	if len(entriesWithCode(env.Errors, string(perrors.PULSE_SPSS_INTEGER_INEXACT))) == 0 {
		t.Errorf("errors = %v, want PULSE_SPSS_INTEGER_INEXACT", env.Errors)
	}
	if _, err := os.Stat(strict); !os.IsNotExist(err) {
		t.Errorf("the refused export left a .sav behind (stat err %v)", err)
	}
}
