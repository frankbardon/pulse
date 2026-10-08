package skills

import (
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"
)

// hide returns a keep predicate hiding exactly names.
func hide(names ...string) func(string) bool {
	return func(n string) bool { return !slices.Contains(names, n) }
}

func mustRender(t *testing.T, body string, keep func(string) bool) string {
	t.Helper()
	out, err := RenderFences(body, keep)
	if err != nil {
		t.Fatalf("RenderFences: %v", err)
	}
	return out
}

func TestRenderFences_Inline(t *testing.T) {
	body := "Use `A` or <!-- feature: TEST_WELCH -->`TEST_WELCH`<!-- /feature --> here.\n"
	if got, want := mustRender(t, body, hide("TEST_WELCH")), "Use `A` or here.\n"; got != want {
		t.Errorf("hidden inline:\n got %q\nwant %q", got, want)
	}
	if got, want := mustRender(t, body, nil), "Use `A` or `TEST_WELCH` here.\n"; got != want {
		t.Errorf("full render keeps the span and strips markers:\n got %q\nwant %q", got, want)
	}
	// A list item emptied by the cut is dropped, not left as a bare "-".
	item := "- a\n- <!-- feature: TEST_WELCH -->`TEST_WELCH` — Welch.<!-- /feature -->\n- b\n"
	if got, want := mustRender(t, item, hide("TEST_WELCH")), "- a\n- b\n"; got != want {
		t.Errorf("emptied item:\n got %q\nwant %q", got, want)
	}
}

func TestRenderFences_Block(t *testing.T) {
	body := "para\n\n<!-- feature: capability:crosstab -->\nCrosstab prose.\n<!-- /feature -->\n\nnext\n"
	if got, want := mustRender(t, body, hide("capability:crosstab")), "para\n\nnext\n"; got != want {
		t.Errorf("hidden block:\n got %q\nwant %q", got, want)
	}
	if got, want := mustRender(t, body, nil), "para\n\nCrosstab prose.\n\nnext\n"; got != want {
		t.Errorf("visible block:\n got %q\nwant %q", got, want)
	}
	// Indented markers are still block markers.
	ind := "a\n  <!-- feature: X_A -->\n  b\n  <!-- /feature -->\nc"
	if got, want := mustRender(t, ind, hide("X_A")), "a\nc"; got != want {
		t.Errorf("indented block:\n got %q\nwant %q", got, want)
	}
}

func TestRenderFences_TableRow(t *testing.T) {
	body := "| op | use |\n|---|---|\n| `AGG_SUM` | totals |\n<!-- feature: AGG_MEDIAN -->\n| `AGG_MEDIAN` | middle |\n<!-- /feature -->\n| `AGG_MAX` | top |\n"
	want := "| op | use |\n|---|---|\n| `AGG_SUM` | totals |\n| `AGG_MAX` | top |\n"
	if got := mustRender(t, body, hide("AGG_MEDIAN")); got != want {
		t.Errorf("hidden table row:\n got %q\nwant %q", got, want)
	}
	if got := mustRender(t, body, nil); got != strings.Replace(want, "| `AGG_MAX`", "| `AGG_MEDIAN` | middle |\n| `AGG_MAX`", 1) {
		t.Errorf("visible table row: %q", got)
	}
}

func TestRenderFences_CodeFence(t *testing.T) {
	body := "Example:\n\n<!-- feature: capability:compose -->\n```json\n{\"requests\": []}\n```\n<!-- /feature -->\n\nInline in code:\n```json\n{\"type\": \"AGG_SUM\"<!-- feature: AGG_MEDIAN -->, \"alt\": \"AGG_MEDIAN\"<!-- /feature -->}\n```\n"
	want := "Example:\n\nInline in code:\n```json\n{\"type\": \"AGG_SUM\"}\n```\n"
	if got := mustRender(t, body, hide("capability:compose", "AGG_MEDIAN")); got != want {
		t.Errorf("hidden code fence:\n got %q\nwant %q", got, want)
	}
	full := mustRender(t, body, nil)
	if strings.Count(full, "```") != 4 || !strings.Contains(full, `{"requests": []}`) || !strings.Contains(full, `"alt": "AGG_MEDIAN"`) {
		t.Errorf("visible code fence lost content: %q", full)
	}
}

func TestRenderFences_AndList(t *testing.T) {
	body := "a\n<!-- feature: TEST_WELCH, capability:crosstab -->\nboth\n<!-- /feature -->\nz"
	for _, tc := range []struct {
		hidden []string
		want   string
	}{
		{nil, "a\nboth\nz"},
		{[]string{"TEST_WELCH"}, "a\nz"},
		{[]string{"capability:crosstab"}, "a\nz"},
		{[]string{"TEST_WELCH", "capability:crosstab"}, "a\nz"},
	} {
		if got := mustRender(t, body, hide(tc.hidden...)); got != tc.want {
			t.Errorf("hide %v: got %q want %q", tc.hidden, got, tc.want)
		}
	}
	fs, err := ParseFences(body)
	if err != nil || len(fs) != 1 || !slices.Equal(fs[0].Names, []string{"TEST_WELCH", "capability:crosstab"}) || !fs[0].Block || fs[0].Line != 2 {
		t.Errorf("ParseFences = %+v, %v", fs, err)
	}
}

func TestRenderFences_Errors(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		line int
		msg  string
	}{
		"nested block":        {"<!-- feature: A_B -->\n<!-- feature: C_D -->\nx\n<!-- /feature -->\n<!-- /feature -->", 2, "nested"},
		"inline inside block": {"<!-- feature: A_B -->\nx <!-- feature: C_D -->y<!-- /feature -->\n<!-- /feature -->", 2, "nested"},
		"nested inline":       {"x <!-- feature: A_B -->y <!-- feature: C_D -->z<!-- /feature --><!-- /feature -->", 1, "nested"},
		"stray closer":        {"x\n<!-- /feature -->", 2, "no open fence"},
		"stray inline closer": {"x <!-- /feature --> y", 1, "no open fence"},
		"unclosed block":      {"a\n<!-- feature: A_B -->\nx", 2, "never closed"},
		"inline across lines": {"x <!-- feature: A_B -->y\nz<!-- /feature -->", 1, "not closed on its line"},
		"empty name":          {"<!-- feature: -->\nx\n<!-- /feature -->", 1, "empty feature name"},
		"empty entry in list": {"<!-- feature: A_B, -->\nx\n<!-- /feature -->", 1, "empty feature name"},
	} {
		_, err := RenderFences(tc.body, nil)
		var fe *FenceError
		if !errors.As(err, &fe) || fe.Line != tc.line || !strings.Contains(fe.Msg, tc.msg) {
			t.Errorf("%s: err = %v, want *FenceError line %d containing %q", name, err, tc.line, tc.msg)
		}
		if _, perr := ParseFences(tc.body); perr == nil {
			t.Errorf("%s: ParseFences accepted it", name)
		}
	}
}

// TestRenderFences_NoMarkersIsIdentity pins the profile-free contract: a
// body without fences renders byte-identically, blank runs included.
func TestRenderFences_NoMarkersIsIdentity(t *testing.T) {
	body := "a\n\n\nb <!-- a plain comment -->\n\n"
	if got := mustRender(t, body, hide("X")); got != body {
		t.Errorf("got %q want %q", got, body)
	}
}

// TestSkillFences_EmbeddedPackParses: every embedded skill's fences are
// well formed, and Get serves each with the markers stripped while Raw
// serves the embedded bytes. (Fence NAMES are checked against the
// feature table in internal/descriptor — TestSkillFences_NamesAreFeatures.)
func TestSkillFences_EmbeddedPackParses(t *testing.T) {
	entries, err := fs.ReadDir(content, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".md")
		if !ok {
			continue
		}
		raw, _ := Raw(name)
		data, _ := fs.ReadFile(content, e.Name())
		if raw != string(data) {
			t.Errorf("Raw(%s) is not the embedded file", name)
		}
		if _, err := ParseFences(raw); err != nil {
			t.Errorf("skills/%s.md: %v", name, err)
			continue
		}
		got, _ := Get(name)
		if fenceMarker.MatchString(got) {
			t.Errorf("Get(%s) still carries a fence marker", name)
		}
		// RenderFull: fences kept and stripped, then any generated-section
		// marker substituted (removed here — no renderer in this binary).
		want := RenderFull(raw)
		if got != want {
			t.Errorf("Get(%s) is not the full-instance render", name)
		}
	}
}
