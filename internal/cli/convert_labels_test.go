package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	perrors "github.com/frankbardon/pulse/errors"
)

func runConvertCLI(args ...string) (string, error) {
	root := ConvertCommand()
	var buf bytes.Buffer
	root.Writer = &buf
	err := root.Run(context.Background(), append([]string{"convert"}, args...))
	return buf.String(), err
}

// convertLabelsFixture writes a CSV source and a PULSE_LABEL_TABLES_DIR
// holding a text-keyed and an integer-keyed table, and points the process
// at the directory.
func convertLabelsFixture(t *testing.T) (dir, src string) {
	t.Helper()
	dir = t.TempDir()
	tables := filepath.Join(dir, "tables")
	if err := os.MkdirAll(tables, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"regions.json": `{"NE":"Northeast","SO":"South"}`,
		"counts.json":  `{"1":"Single","2":"Pair"}`,
	} {
		if err := os.WriteFile(filepath.Join(tables, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PULSE_LABEL_TABLES_DIR", tables)
	src = filepath.Join(dir, "in.csv")
	if err := os.WriteFile(src, []byte("region,n,score\nNE,1,1.5\nSO,2,2.5\nWE,3,3.5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, src
}

// TestConvert_LabelsFlagUsesDirLoadedTables: `pulse convert --labels`
// resolves its bindings against a PULSE_LABEL_TABLES_DIR table and labels
// the converted rows (replace and augment, a CSV target and a .sav one),
// and `pulse convert predict --labels` accepts the same bindings.
func TestConvert_LabelsFlagUsesDirLoadedTables(t *testing.T) {
	// serial: t.Setenv mutates the process environment

	dir, src := convertLabelsFixture(t)
	for _, tc := range []struct{ binding, want string }{
		{"region=regions", "region,n,score\nNortheast,1,1.5\nSouth,2,2.5\nWE,3,3.5\n"},
		{"n=counts:augment", "region,n,n_label,score\nNE,1,Single,1.5\nSO,2,Pair,2.5\nWE,3,,3.5\n"},
	} {
		out := filepath.Join(dir, "out.csv")
		if _, err := runConvertCLI(src, out, "--labels", tc.binding); err != nil {
			t.Fatalf("convert --labels %s: %v", tc.binding, err)
		}
		got, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != tc.want {
			t.Errorf("--labels %s wrote\n%s\nwant\n%s", tc.binding, got, tc.want)
		}
		if _, err := runConvertCLI("predict", src, out, "--labels", tc.binding); err != nil {
			t.Errorf("convert predict --labels %s: %v", tc.binding, err)
		}
	}

	// A .sav target is labelled on the row stream too: read it back.
	sav := filepath.Join(dir, "out.sav")
	if _, err := runConvertCLI(src, sav, "--labels", "region=regions"); err != nil {
		t.Fatalf("convert to .sav --labels: %v", err)
	}
	back := filepath.Join(dir, "back.csv")
	if _, err := runConvertCLI(sav, back); err != nil {
		t.Fatalf("convert .sav back: %v", err)
	}
	got, err := os.ReadFile(back)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "Northeast") || !strings.Contains(string(got), "South") {
		t.Errorf(".sav target not labelled; read back\n%s", got)
	}
	if _, err := runConvertCLI("predict", src, sav, "--labels", "region=regions"); err != nil {
		t.Errorf("convert predict to .sav --labels: %v", err)
	}
}

// TestConvert_LabelsFlagRefusalsMatchPredict: every label refusal `pulse
// convert --labels` raises — an unregistered table, an unknown field, a
// field bound twice, a float field, an integer field's text keys — is
// raised by `pulse convert predict --labels` with the same code, and the
// refused convert leaves no target behind. Under --json the code rides
// the envelope with exit status 1.
func TestConvert_LabelsFlagRefusalsMatchPredict(t *testing.T) {
	// serial: t.Setenv mutates the process environment

	dir, src := convertLabelsFixture(t)
	for _, tc := range []struct {
		bindings []string
		want     perrors.Code
	}{
		{[]string{"region=no_such_table"}, perrors.PULSE_LABEL_TABLE_UNKNOWN},
		{[]string{"nope=regions"}, perrors.PULSE_LABEL_FIELD_UNKNOWN},
		{[]string{"region=regions", "region=regions:augment"}, perrors.PULSE_LABEL_DUPLICATE_BINDING},
		{[]string{"score=counts"}, perrors.PULSE_LABEL_FIELD_NOT_CATEGORICAL},
		{[]string{"n=regions"}, perrors.PULSE_LABEL_KEY_INVALID},
	} {
		var flags []string
		for _, b := range tc.bindings {
			flags = append(flags, "--labels", b)
		}
		bad := filepath.Join(dir, "bad.csv")
		_, err := runConvertCLI(append([]string{src, bad}, flags...)...)
		wantCode(t, err, tc.want)
		if _, serr := os.Stat(bad); !os.IsNotExist(serr) {
			t.Errorf("%v: a refused convert left %s behind", tc.bindings, bad)
		}
		_, err = runConvertCLI(append([]string{"predict", src, bad}, flags...)...)
		wantCode(t, err, tc.want)

		for _, leaf := range [][]string{{src, bad, "--json"}, {"predict", src, bad, "--json"}} {
			out, err := runConvertCLI(append(leaf, flags...)...)
			if status, _ := ExitStatus(err); status != ExitFatal {
				t.Errorf("%v %v: exit status = %d, want %d (err %v)", leaf, tc.bindings, status, ExitFatal, err)
			}
			var env struct {
				Errors []struct {
					Code string `json:"code"`
				} `json:"errors"`
			}
			if jerr := json.Unmarshal([]byte(out), &env); jerr != nil {
				t.Fatalf("%v: envelope %q: %v", leaf, out, jerr)
			}
			if len(env.Errors) != 1 || env.Errors[0].Code != string(tc.want) {
				t.Errorf("%v %v: envelope errors = %+v, want one %s", leaf, tc.bindings, env.Errors, tc.want)
			}
		}
	}
}
