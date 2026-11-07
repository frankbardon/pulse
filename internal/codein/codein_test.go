package codein_test

import (
	stderrors "errors"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/codein"
)

func coded(t *testing.T, err error) *errors.CodedError {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("want a coded error, got %v", err)
	}
	return ce
}

func TestParseCodes(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name, raw string
		want      []string
		msg       string // refusal message substring; "" accepts
	}{
		{"absent", "", nil, `requires params with a non-empty "codes" list`},
		{"no_key", `{}`, nil, `non-empty "codes" list`},
		{"empty", `{"codes":[]}`, nil, `non-empty "codes" list`},
		{"not_list", `{"codes":"a"}`, nil, "parsing ATTR_CODE_IN params"},
		{"fraction", `{"codes":[1.5]}`, nil, "JSON string or a JSON integer, got 1.5"},
		{"exponent", `{"codes":[1e2]}`, nil, "got 1e2"},
		{"bool", `{"codes":[true]}`, nil, "got true"},
		{"null", `{"codes":[null]}`, nil, "got null"},
		{"bare_minus", `{"codes":[-]}`, nil, "parsing ATTR_CODE_IN params"},
		{"deduped", `{"codes":["4", 4, "5", "4", 5, -1]}`, []string{"4", "5", "-1"}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := codein.ParseCodes([]byte(c.raw))
			if c.msg == "" {
				if err != nil || !reflect.DeepEqual(got, c.want) {
					t.Fatalf("got %q, %v; want %q", got, err, c.want)
				}
				return
			}
			ce := coded(t, err)
			if ce.Code != errors.PROCESSING_CONFIG || !strings.Contains(ce.Message, c.msg) {
				t.Fatalf("got %s %q, want PROCESSING_CONFIG containing %q", ce.Code, ce.Message, c.msg)
			}
		})
	}
}

func TestCodeText(t *testing.T) {
	t.Parallel()

	for raw, want := range map[string]string{`"a"`: "a", ` 7 `: "7", `-3`: "-3"} {
		if got, ok := codein.CodeText([]byte(raw)); !ok || got != want {
			t.Errorf("%s: got %q %v, want %q", raw, got, ok, want)
		}
	}
	for _, raw := range []string{``, `  `, `-`, `"unterminated`, `1.0`, `{}`} {
		if got, ok := codein.CodeText([]byte(raw)); ok {
			t.Errorf("%s: accepted as %q", raw, got)
		}
	}
}

func TestCheck(t *testing.T) {
	t.Parallel()

	if got, err := codein.Check("g", encoding.FieldTypeCategoricalU8, []string{"x"}); err != nil || got != nil {
		t.Errorf("categorical: got %v, %v", got, err)
	}
	if got, err := codein.Check("n", encoding.FieldTypeU8, []string{"0", "255"}); err != nil || !reflect.DeepEqual(got, []uint64{0, 255}) {
		t.Errorf("u8: got %v, %v", got, err)
	}
	if ce := coded(t, mustErr(codein.Check("f", encoding.FieldTypeF64, []string{"1"}))); ce.Code != errors.PULSE_FIELD_TYPE_UNSUPPORTED {
		t.Errorf("f64: code %s", ce.Code)
	}
	for ft, maxV := range codein.IntegerMax {
		over := strconv.FormatUint(maxV, 10) + "0"
		ce := coded(t, mustErr(codein.Check("n", ft, []string{"1", over})))
		want := map[string]any{"attribute": codein.Name, "field": "n", "type": ft.String(), "code": over, "max": maxV}
		if ce.Code != errors.PROCESSING_CONFIG || !reflect.DeepEqual(ce.Details, want) ||
			!strings.HasSuffix(ce.Message, "in 0.."+strconv.FormatUint(maxV, 10)) {
			t.Errorf("%s: %s %q %#v", ft, ce.Code, ce.Message, ce.Details)
		}
	}
}

func mustErr(_ []uint64, err error) error { return err }

func TestMissingAndUnknownField(t *testing.T) {
	t.Parallel()

	d := encoding.NewDictionary()
	if _, err := d.Add("a"); err != nil {
		t.Fatal(err)
	}
	if got := codein.Missing(d, []string{"z", "a", "b"}); !reflect.DeepEqual(got, []string{"b", "z"}) {
		t.Errorf("missing %q", got)
	}
	if got := codein.Missing(d, []string{"a"}); got != nil {
		t.Errorf("none missing: %q", got)
	}
	if got := codein.Missing(nil, []string{"b", "a"}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("no dictionary: %q", got)
	}
	if ce := coded(t, codein.UnknownField("d")); ce.Code != errors.PROCESSING_CONFIG || ce.Message != "ATTR_CODE_IN: unknown field d" {
		t.Errorf("unknown field: %s %q", ce.Code, ce.Message)
	}
}

// TestCodeIn_ImportBoundary: predict (internal/descriptor, no-execute)
// and the runtime both import this package, so its intra-module
// imports are pinned to the public errors / encoding / types and the
// two shared leaves — never the engine or the orchestration layer.
func TestCodeIn_ImportBoundary(t *testing.T) {
	t.Parallel()

	const mod = "github.com/frankbardon/pulse"
	allowed := map[string]bool{
		mod + "/errors": true, mod + "/encoding": true, mod + "/types": true,
		mod + "/internal/fieldaccept": true, mod + "/internal/paramread": true,
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if strings.Contains(path, ".") && !allowed[path] {
				t.Errorf("%s imports %q; internal/codein may import only errors, encoding, types, internal/fieldaccept and internal/paramread", name, path)
			}
		}
	}
}
