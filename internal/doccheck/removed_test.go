package doccheck

import (
	"strings"
	"testing"
)

func TestWordCount(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, text, needle string
		want               int
	}{
		{"bare", "use ValidateChain here", "ValidateChain", 1},
		{"prefix of a longer identifier", "ValidateChainFromBytes", "ValidateChain", 0},
		{"suffix of a longer identifier", "xValidateChain", "ValidateChain", 0},
		{"underscore joins", "PROCESSING_REGRESSION_NOT_IMPLEMENTED_X", "PROCESSING_REGRESSION_NOT_IMPLEMENTED", 0},
		{"punctuation bounds", "`FromExt`, (FromExt).", "FromExt", 2},
		{"selector bounds the bare name", "processing.ApplyOverlays()", "ApplyOverlays", 1},
		{"qualified needle", "io.InferSchema vs InferSchema", "io.InferSchema", 1},
		{"phrase", "speaks MCP version 1.0.0.", "MCP version 1.0.0", 1},
		{"phrase then digit", "MCP version 1.0.01", "MCP version 1.0.0", 0},
		{"non-ASCII neighbour joins", "éFromExt", "FromExt", 0},
		{"empty needle", "x", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := WordCount(tc.text, tc.needle); got != tc.want {
				t.Errorf("WordCount(%q, %q) = %d, want %d", tc.text, tc.needle, got, tc.want)
			}
		})
	}
}

func TestCheckRemovedNames(t *testing.T) {
	t.Parallel()
	deny := []DeniedName{
		{Name: "ValidateChain", Reason: "deleted"},
		{Name: "MCP version 1.0.0", Reason: "pre-1.0 MCP"},
	}
	vs := CheckRemovedNames(map[string]string{
		"docs/src/b.md": "ValidateChain twice: ValidateChain",
		"docs/src/a.md": "Speaks MCP version 1.0.0 and calls ValidateChain.",
		"skill clean":   "ValidateChainWithOptions is fine",
		"manifest":      "nothing here",
	}, deny)
	var got []string
	for _, v := range vs {
		got = append(got, v.Path+"|"+v.Entry)
		if !strings.Contains(v.Message, v.Entry) {
			t.Errorf("message %q does not name %q", v.Message, v.Entry)
		}
	}
	want := "docs/src/a.md|MCP version 1.0.0,docs/src/a.md|ValidateChain,docs/src/b.md|ValidateChain"
	if strings.Join(got, ",") != want {
		t.Errorf("violations = %v, want %s", got, want)
	}

	// Reconciled against a baseline: a listed hit passes; an unlisted one is
	// new; an entry whose hit is gone is stale.
	allow, err := Parse("removed-names.txt", strings.NewReader(
		"docs/src/a.md\tValidateChain\tbaseline\n"+
			"docs/src/gone.md\tValidateChain\tbaseline\n"))
	if err != nil {
		t.Fatal(err)
	}
	res := allow.Reconcile(vs)
	if len(res.New) != 2 || len(res.Stale) != 1 || res.Stale[0].Path != "docs/src/gone.md" {
		t.Errorf("reconcile: new %v, stale %v", res.New, res.Stale)
	}
}

// TestRemovedNamesImportAlias: a qualified io.<Name> entry also matches
// the public io package's import alias, pio.<Name>, and reports the
// denied Name as the entry; an unqualified or unaliased entry gains no
// spelling.
func TestRemovedNamesImportAlias(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		want string
	}{
		{"io.InferSchema", "io.InferSchema,pio.InferSchema"},
		{"service.Cohort", "service.Cohort"},
		{"ValidateChain", "ValidateChain"},
		{"MCP version 1.0.0", "MCP version 1.0.0"},
	} {
		if got := strings.Join(DeniedName{Name: tc.name}.Spellings(), ","); got != tc.want {
			t.Errorf("Spellings(%q) = %s, want %s", tc.name, got, tc.want)
		}
	}
	vs := CheckRemovedNames(map[string]string{
		"docs/src/alias.md": "schema := pio.InferSchema(r)",
		"docs/src/plain.md": "io.NewConvertJob is gone",
		"docs/src/clean.md": "xpio.InferSchema and spio.NewConvertJob are other names",
	}, []DeniedName{
		{Name: "io.InferSchema", Reason: "internal"},
		{Name: "io.NewConvertJob", Reason: "removed"},
	})
	var got []string
	for _, v := range vs {
		got = append(got, v.Path+"|"+v.Entry)
	}
	if want := "docs/src/alias.md|io.InferSchema,docs/src/plain.md|io.NewConvertJob"; strings.Join(got, ",") != want {
		t.Errorf("violations = %v, want %s", got, want)
	}
	if len(vs) > 0 && !strings.Contains(vs[0].Message, `as "pio.InferSchema"`) {
		t.Errorf("message %q does not name the alias spelling that hit", vs[0].Message)
	}
}
