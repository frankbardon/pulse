package pulse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/template"
)

// touchTmpl pushes a template file's modification time forward, so a
// rewrite is detectable regardless of the host filesystem's mtime
// granularity. The store's change check pairs size with mtime; a test must
// not depend on which of the two happened to move.
func touchTmpl(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s): %v", path, err)
	}
	at := info.ModTime().Add(time.Second)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatalf("Chtimes(%s): %v", path, err)
	}
}

// renderedDescription resolves name through the facade and returns the
// loaded document's description. Every fixture here carries its content
// marker there, so "did the engine pick the change up?" is answered by
// reading what it serves rather than by trusting a heuristic to have
// fired.
func renderedDescription(t *testing.T, p *Pulse, name string) string {
	t.Helper()
	got, err := p.GetTemplate(name)
	if err != nil {
		t.Fatalf("GetTemplate(%q) = %v, want the template", name, err)
	}
	return got.Description
}

// TestReloadTemplates is the hot-reload contract at the facade: a template
// directory is live after pulse.New, not a boot-time snapshot.
//
// Every case forces the rescan with ReloadTemplates rather than waiting out
// the store's interval. That is what the method is for — a test that slept
// past a one-second gate would be slow when it passed and flaky when the
// machine was busy.
func TestReloadTemplates(t *testing.T) {
	t.Run("a file added after New becomes renderable", func(t *testing.T) {
		dir := t.TempDir()
		writeTmpl(t, dir, "revenue.json", tmplDoc("original"))

		p := newPulse(t, Options{TemplateDirs: []string{dir}})

		if _, err := p.RenderTemplateRequest("finance/new", map[string]any{"metric": "amount"}); err == nil {
			t.Fatal("RenderTemplateRequest(finance/new) succeeded before the file existed")
		} else if !perr.HasCode(err, perr.PULSE_TEMPLATE_NOT_FOUND) {
			t.Fatalf("RenderTemplateRequest(finance/new) = %v, want PULSE_TEMPLATE_NOT_FOUND", err)
		}

		writeTmpl(t, dir, "finance/new.json", facadeDoc("brand new", "request", requestBody))

		if err := p.ReloadTemplates(); err != nil {
			t.Fatalf("ReloadTemplates() = %v, want nil error", err)
		}

		req, err := p.RenderTemplateRequest("finance/new", map[string]any{"metric": "amount"})
		if err != nil {
			t.Fatalf("RenderTemplateRequest(finance/new) = %v, want the rendered request — a file dropped in after New must render without a restart", err)
		}
		if len(req.Aggregations) != 1 || req.Aggregations[0].Field != "amount" {
			t.Fatalf("rendered request = %+v, want one aggregation over \"amount\"", req)
		}
		if got := renderedDescription(t, p, "finance/new"); got != "brand new" {
			t.Errorf("GetTemplate(finance/new).Description = %q, want %q", got, "brand new")
		}

		// The listing sees it too — a caller building a picker must not
		// need a restart either.
		names := make([]string, 0, 2)
		for _, sum := range p.ListTemplates() {
			names = append(names, sum.Name)
		}
		if len(names) != 2 || names[0] != "finance/new" || names[1] != "revenue" {
			t.Errorf("ListTemplates() names = %v, want [finance/new revenue]", names)
		}
	})

	t.Run("a modified file renders its new content", func(t *testing.T) {
		dir := t.TempDir()
		path := writeTmpl(t, dir, "revenue.json", tmplDoc("original"))

		p := newPulse(t, Options{TemplateDirs: []string{dir}})
		if got := renderedDescription(t, p, "revenue"); got != "original" {
			t.Fatalf("GetTemplate(revenue).Description = %q, want %q", got, "original")
		}

		// The rewrite changes the body as well as the description, so the
		// assertion below reads rendered content rather than metadata.
		const edited = `{
  "description": "edited after startup",
  "target": "request",
  "variables": [{"name": "metric", "type": "field", "required": true}],
  "body": {"cohort": {"filename": "other.pulse"},
    "aggregations": [{"type": "AGG_MEAN", "field": {"$var": "metric"}}]}
}`
		if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		touchTmpl(t, path)

		if err := p.ReloadTemplates(); err != nil {
			t.Fatalf("ReloadTemplates() = %v, want nil error", err)
		}

		if got := renderedDescription(t, p, "revenue"); got != "edited after startup" {
			t.Errorf("GetTemplate(revenue).Description = %q, want %q", got, "edited after startup")
		}
		req, err := p.RenderTemplateRequest("revenue", map[string]any{"metric": "amount"})
		if err != nil {
			t.Fatalf("RenderTemplateRequest(revenue) = %v", err)
		}
		if req.Cohort == nil || req.Cohort.Filename != "other.pulse" {
			t.Errorf("rendered cohort = %+v, want the edited file's \"other.pulse\"", req.Cohort)
		}
		if len(req.Aggregations) != 1 || req.Aggregations[0].Type != "AGG_MEAN" {
			t.Errorf("rendered aggregations = %+v, want the edited file's AGG_MEAN", req.Aggregations)
		}
	})

	t.Run("a deleted file stops resolving", func(t *testing.T) {
		dir := t.TempDir()
		path := writeTmpl(t, dir, "revenue.json", tmplDoc("original"))
		writeTmpl(t, dir, "keeper.json", tmplDoc("still here"))

		p := newPulse(t, Options{TemplateDirs: []string{dir}})
		if got := renderedDescription(t, p, "revenue"); got != "original" {
			t.Fatalf("GetTemplate(revenue).Description = %q, want %q", got, "original")
		}

		if err := os.Remove(path); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		if err := p.ReloadTemplates(); err != nil {
			t.Fatalf("ReloadTemplates() = %v, want nil error", err)
		}

		_, err := p.GetTemplate("revenue")
		codedErr(t, err, perr.PULSE_TEMPLATE_NOT_FOUND)

		_, err = p.RenderTemplateRequest("revenue", map[string]any{"metric": "amount"})
		codedErr(t, err, perr.PULSE_TEMPLATE_NOT_FOUND)

		// Deleting one template must not take its neighbours with it.
		if got := renderedDescription(t, p, "keeper"); got != "still here" {
			t.Errorf("GetTemplate(keeper).Description = %q, want %q", got, "still here")
		}
	})

	t.Run("shadowing is re-resolved across roots", func(t *testing.T) {
		high, low := t.TempDir(), t.TempDir()
		writeTmpl(t, low, "shared.json", tmplDoc("from low"))

		p := newPulse(t, Options{TemplateDirs: []string{high, low}})
		if got := renderedDescription(t, p, "shared"); got != "from low" {
			t.Fatalf("GetTemplate(shared).Description = %q, want %q", got, "from low")
		}

		override := writeTmpl(t, high, "shared.json", tmplDoc("from high"))
		if err := p.ReloadTemplates(); err != nil {
			t.Fatalf("ReloadTemplates() = %v", err)
		}
		if got := renderedDescription(t, p, "shared"); got != "from high" {
			t.Fatalf("GetTemplate(shared).Description = %q, want %q — an override dropped into the higher-precedence root must start winning", got, "from high")
		}

		if err := os.Remove(override); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		if err := p.ReloadTemplates(); err != nil {
			t.Fatalf("ReloadTemplates() = %v", err)
		}
		if got := renderedDescription(t, p, "shared"); got != "from low" {
			t.Errorf("GetTemplate(shared).Description = %q, want %q — withdrawing the override must restore the lower root", got, "from low")
		}
	})

	t.Run("an engine with no template directories is a no-op", func(t *testing.T) {
		t.Setenv(envTemplatesDir, "")

		p := newPulse(t, Options{})
		if err := p.ReloadTemplates(); err != nil {
			t.Fatalf("ReloadTemplates() on an unconfigured engine = %v, want nil", err)
		}
		if got := p.ListTemplates(); len(got) != 0 {
			t.Errorf("ListTemplates() = %v, want empty", got)
		}
		if _, err := p.GetTemplate("anything"); !perr.HasCode(err, perr.PULSE_TEMPLATE_NOT_FOUND) {
			t.Errorf("GetTemplate() = %v, want PULSE_TEMPLATE_NOT_FOUND", err)
		}

		// A configured but empty root is the same story: nothing to walk,
		// nothing to fail.
		q := newPulse(t, Options{TemplateDirs: []string{t.TempDir()}})
		if err := q.ReloadTemplates(); err != nil {
			t.Errorf("ReloadTemplates() over an empty root = %v, want nil", err)
		}
	})

	t.Run("a malformed file appearing later is listed broken, not returned as an error", func(t *testing.T) {
		dir := t.TempDir()
		writeTmpl(t, dir, "revenue.json", tmplDoc("original"))

		p := newPulse(t, Options{TemplateDirs: []string{dir}})

		path := writeTmpl(t, dir, "broken.json", `{"target": "request", "body": {`)
		touchTmpl(t, path)

		// E3-S2: a per-file document fault must NOT come back from the
		// reload. Returning it here would tell the caller its whole
		// catalog failed over one file it may not even use.
		if err := p.ReloadTemplates(); err != nil {
			t.Fatalf("ReloadTemplates() = %v, want nil — one malformed file must not mask an otherwise-healthy catalog", err)
		}

		// It surfaces through the listing instead, naming the file to open.
		var broken *template.Summary
		listing := p.ListTemplates()
		for i := range listing {
			if listing[i].Name == "broken" {
				broken = &listing[i]
				break
			}
		}
		if broken == nil {
			t.Fatal("ListTemplates() carries no entry for the malformed file; a broken file nobody can see is a broken file nobody fixes")
		}
		if !broken.Broken {
			t.Error("ListTemplates() entry for the malformed file has Broken = false, want true")
		}
		if !strings.Contains(broken.Error, path) {
			t.Errorf("Summary.Error = %q, want it to name the offending file %q", broken.Error, path)
		}
		if broken.Target != "" {
			t.Errorf("Summary.Target = %q, want empty — the file never parsed, so it must not look fetchable", broken.Target)
		}

		// A file that never parsed has no last-good to serve, so asking
		// for it by name is the fault, with its path.
		_, err := p.GetTemplate("broken")
		ce := codedErr(t, err, perr.PULSE_TEMPLATE_INVALID)
		if got := ce.Details["path"]; got != path {
			t.Errorf("details[path] = %v, want %q", got, path)
		}

		// And the healthy template is entirely unaffected.
		if got := renderedDescription(t, p, "revenue"); got != "original" {
			t.Errorf("GetTemplate(revenue).Description = %q, want %q", got, "original")
		}
	})

	t.Run("a template that breaks after New keeps rendering its last-good content", func(t *testing.T) {
		dir := t.TempDir()
		path := writeTmpl(t, dir, "revenue.json", facadeDoc("original", "request", requestBody))
		writeTmpl(t, dir, "sibling.json", tmplDoc("sibling"))

		p := newPulse(t, Options{TemplateDirs: []string{dir}})

		req, err := p.RenderTemplateRequest("revenue", map[string]any{"metric": "amount"})
		if err != nil {
			t.Fatalf("RenderTemplateRequest(revenue) = %v", err)
		}
		if len(req.Aggregations) != 1 || req.Aggregations[0].Field != "amount" {
			t.Fatalf("rendered request = %+v, want one aggregation over \"amount\"", req)
		}

		// The file is broken mid-edit.
		if err := os.WriteFile(path, []byte(`{"target": "request", "body": {`), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		touchTmpl(t, path)
		if err := p.ReloadTemplates(); err != nil {
			t.Fatalf("ReloadTemplates() = %v, want nil", err)
		}

		// It still renders — the last-good parse. A running system keeps
		// running through a half-written save.
		req, err = p.RenderTemplateRequest("revenue", map[string]any{"metric": "amount"})
		if err != nil {
			t.Fatalf("RenderTemplateRequest(revenue) = %v, want the last-good render", err)
		}
		if len(req.Aggregations) != 1 || req.Aggregations[0].Field != "amount" {
			t.Errorf("rendered request = %+v, want the last-good aggregation over \"amount\"", req)
		}
		if got := renderedDescription(t, p, "sibling"); got != "sibling" {
			t.Errorf("GetTemplate(sibling).Description = %q, want %q", got, "sibling")
		}

		// Repairing it swaps the new content in and clears the marker.
		const repaired = `{
  "description": "repaired",
  "target": "request",
  "variables": [{"name": "metric", "type": "field", "required": true}],
  "body": {"cohort": {"filename": "other.pulse"},
    "aggregations": [{"type": "AGG_MEAN", "field": {"$var": "metric"}}]}
}`
		if err := os.WriteFile(path, []byte(repaired), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		touchTmpl(t, path)
		if err := p.ReloadTemplates(); err != nil {
			t.Fatalf("ReloadTemplates() = %v, want nil", err)
		}

		req, err = p.RenderTemplateRequest("revenue", map[string]any{"metric": "amount"})
		if err != nil {
			t.Fatalf("RenderTemplateRequest(revenue) = %v after the repair", err)
		}
		if req.Cohort == nil || req.Cohort.Filename != "other.pulse" {
			t.Errorf("rendered cohort = %+v, want the repaired file's \"other.pulse\"", req.Cohort)
		}
		for _, sum := range p.ListTemplates() {
			if sum.Broken {
				t.Errorf("ListTemplates() still reports %q broken after the repair", sum.Name)
			}
		}
	})

	t.Run("a directory removed wholesale degrades to not-found", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(root, "templates")
		writeTmpl(t, dir, "revenue.json", tmplDoc("original"))

		p := newPulse(t, Options{TemplateDirs: []string{dir}})
		if got := renderedDescription(t, p, "revenue"); got != "original" {
			t.Fatalf("GetTemplate(revenue).Description = %q, want %q", got, "original")
		}

		if err := os.RemoveAll(dir); err != nil {
			t.Fatalf("RemoveAll: %v", err)
		}
		// A root that has gone missing is an absent layer, not a fault —
		// the same rule that lets an optional override directory not
		// exist at startup.
		if err := p.ReloadTemplates(); err != nil {
			t.Fatalf("ReloadTemplates() = %v, want nil — a vanished root is an absent layer, not an error", err)
		}
		if _, err := p.GetTemplate("revenue"); !perr.HasCode(err, perr.PULSE_TEMPLATE_NOT_FOUND) {
			t.Errorf("GetTemplate(revenue) = %v, want PULSE_TEMPLATE_NOT_FOUND", err)
		}
	})
}
