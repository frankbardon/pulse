package pulse

import (
	"context"
	"encoding/json"
	"io/fs"
	"path"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/afero"

	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/doccheck"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
)

// removedNamesAllowlist is TestNoRemovedNamesInDocs's shrink-only baseline
// (internal/doccheck): path = the served source, entry = the denied name.
const removedNamesAllowlist = "removed-names"

// removedNames is the removed-name denylist: spellings that existed before
// v1 (at v0.39.0, or only in pre-1.0 plans) and are gone, so served text
// must not use them. Seeded by `go run ./internal/tools/removednames`
// (v0.39.0 → HEAD) and pruned by the owner: a name still exported by some
// package, still a method or still a live string is never listed (it
// would flag correct prose). Add an entry when you remove a public name
// whose spelling is distinctive.
var removedNames = []doccheck.DeniedName{
	// Public names deleted at v1 (sweep of v0.39.0).
	{Name: "ApplyOverlays", Reason: "test-only processing helper, removed in U02 with no replacement; drive overlays through Request.Overlays or (*Pulse).ApplySeriesOverlays"},
	{Name: "CompileDateRanges", Reason: "processing helper removed in U02; range tables are validated at pulse.New"},
	{Name: "DateRangeSet", Reason: "processing type removed in U02; use pulse.DateRangeSpec"},
	{Name: "CategoricalAggregationIssues", Reason: "descriptor helper removed in U02; predict reports the issue"},
	{Name: "InspectFromBytes", Reason: "descriptor function replaced by (*Pulse).InspectBytes in U02 (#182)"},
	{Name: "PredictFromBytes", Reason: "descriptor function replaced by (*Pulse).PredictBytes in U02 (#182)"},
	{Name: "NormalizeRequest", Reason: "descriptor function removed in U02; predict and process normalise the request"},
	{Name: "ValidateChain", Reason: "test-only descriptor wrapper deleted in U34 (#194); use (*Pulse).PredictChain"},
	{Name: "ValidateChainFromBytes", Reason: "test-only descriptor wrapper deleted in U34 (#194); use (*Pulse).PredictChain"},
	{Name: "DistributionRegistration", Reason: "pulse type retired in U34 (#197); synthetic-data distributions are not an extension category"},
	{Name: "FromExt", Reason: "io/format function replaced in U02 by io.FormatFromPath"},
	{Name: "SupportedImport", Reason: "io/format function replaced in U02 by io.Formats() and Format.CanRead()"},
	{Name: "PromoteAdd", Reason: "encoding decimal helper removed before v1"},
	{Name: "PromoteMul", Reason: "encoding decimal helper removed before v1"},
	{Name: "PromoteDiv", Reason: "encoding decimal helper removed before v1"},
	{Name: "ValidatePrecisionScale", Reason: "encoding decimal helper removed before v1"},
	{Name: "PROCESSING_REGRESSION_NOT_IMPLEMENTED", Reason: "error code removed before v1: every regression operator is implemented"},
	// Pre-1.0 spellings that never shipped or were replaced.
	{Name: "OVERLAY_CORR_PVALUE", Reason: "planned overlay dropped in favour of MatrixSpec.multiplicity"},
	{Name: "MCP version 1.0.0", Reason: "pre-1.0 MCP server version; the server reports pulse.Version()"},
	// Migration-guide removals (docs/roadmap/v1.0.0-api-and-release/03-embedder-migration.md).
	{Name: "service.Cohort", Reason: "removed with (*Pulse).Service() in U02; use the facade methods"},
	{Name: "io.NewConvertJob", Reason: "removed in U02; build &io.ConvertJob{} directly"},
	{Name: "io.InferSchema", Reason: "moved internal in U02; inference runs inside io.ImportJob"},
	{Name: "mmmpulse.Extensions", Reason: "the media-mix pack is registered by the embedder, not reached through a pack-level Extensions value"},
}

// removedNamesSkipDocs are docs/src pages the scan skips: the generated
// coverage matrix names every public identifier verbatim, so a mention
// scan over it is vacuous.
var removedNamesSkipDocs = map[string]bool{
	"docs/src/contract/coverage.md": true,
}

// TestNoRemovedNamesInDocs (U32 E2-S3, PRD FR-14–FR-16): no served text
// names a removed or pre-1.0 spelling from removedNames, matched as a
// whole word. Served text is docs/src/** (the generated guide included,
// the coverage page skipped), the repo-root README / CONTRIBUTING /
// SECURITY, and what a default instance serves: every skill body and the
// skill metadata, every MCP tool description, the manifest's string values
// (descriptions and guidance), every error code's message and fixup
// hints, and every example. docs/roadmap/**, CLAUDE.md, .claude/reference/
// and tests are history or contributor notes, not served, and are out of
// scope.
//
// Today's hits are baselined in the shrink-only allowlist
// internal/doccheck/allowlists/removed-names.txt (path = source, entry =
// name): a hit it does not list fails, and so does an entry whose hit is
// gone.
func TestNoRemovedNamesInDocs(t *testing.T) {
	t.Parallel()

	osFS := afero.NewOsFs()
	sources := removedNameSources(t, osFS)
	if len(sources) < 500 {
		t.Fatalf("only %d served sources — a surface walk is broken", len(sources))
	}
	seen := map[string]bool{}
	for _, d := range removedNames {
		if d.Name == "" || d.Reason == "" || seen[d.Name] {
			t.Errorf("denylist entry %+v: name and reason are required, names are unique", d)
		}
		seen[d.Name] = true
	}

	allow, err := doccheck.Load(osFS, doccheck.AllowlistFile(removedNamesAllowlist))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range allow.Reconcile(doccheck.CheckRemovedNames(sources, removedNames)).Problems(allow.File) {
		t.Error(p)
	}
}

// removedNameSources gathers the served text TestNoRemovedNamesInDocs
// scans, keyed by a stable label: the repo path for a file, else
// "<surface> <name>".
func removedNameSources(t *testing.T, osFS afero.Fs) map[string]string {
	t.Helper()
	out := map[string]string{}

	for _, f := range []string{"README.md", "CONTRIBUTING.md", "SECURITY.md"} {
		b, err := afero.ReadFile(osFS, f)
		if err != nil {
			t.Fatal(err)
		}
		out[f] = string(b)
	}
	err := afero.Walk(osFS, "docs/src", func(p string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || path.Ext(p) != ".md" || removedNamesSkipDocs[p] {
			return nil
		}
		b, err := afero.ReadFile(osFS, p)
		if err != nil {
			return err
		}
		out[p] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	p, err := New(Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range p.Skills() {
		body, ok := p.Skill(s.Name)
		if !ok {
			t.Fatalf("listed skill %q has no body", s.Name)
		}
		meta, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		out["skill "+s.Name] = body + "\n" + strings.Join(jsonStrings(t, meta), "\n")
	}
	for _, m := range toolmeta.Meta() {
		out["tool "+m.Name] = m.Description
	}

	raw, err := json.Marshal(p.Manifest(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	var sections map[string]json.RawMessage
	if err := json.Unmarshal(raw, &sections); err != nil {
		t.Fatal(err)
	}
	for k, v := range sections {
		out["manifest "+k] = strings.Join(jsonStrings(t, v), "\n")
	}

	for _, c := range perrors.AllCodes() {
		md, ok := perrors.MetadataFor(c)
		if !ok {
			continue // TestCodesHaveFixups owns a missing entry
		}
		text := []string{md.Message}
		for _, f := range md.Fixups {
			text = append(text, f.Hint)
		}
		out["error "+string(c)] = strings.Join(text, "\n")
	}

	for _, e := range p.ExamplesSearch("", nil, "") {
		ex, ok := p.ExampleGet(e.Name)
		if !ok {
			t.Fatalf("listed example %q has no body", e.Name)
		}
		b, err := json.Marshal(ex)
		if err != nil {
			t.Fatal(err)
		}
		out["example "+e.Name] = strings.Join(jsonStrings(t, b), "\n")
	}
	return out
}

// jsonStrings returns every string value of a JSON document (object keys
// excluded), sorted.
func jsonStrings(t *testing.T, raw []byte) []string {
	t.Helper()
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var out []string
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case string:
			out = append(out, v)
		case []any:
			for _, e := range v {
				walk(e)
			}
		case map[string]any:
			for _, e := range v {
				walk(e)
			}
		}
	}
	walk(doc)
	sort.Strings(out)
	return out
}
