package template_test

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/template"
)

// This file is the per-file degradation contract: what the store does when
// a template file that was fine becomes malformed while the process runs.
//
// The policy, stated once here so no test in this file has to imply it:
//
//  1. A template that PARSED ONCE and whose file later breaks keeps serving
//     its last-good parse. On any machine where a human edits templates in
//     place, a partially-written file is the normal transient state, and
//     killing the catalog over it would be worse than serving content that
//     is seconds stale.
//  2. Brokenness is visible rather than silent: the listing marks that entry
//     Broken and carries the fault, so an operator can find the bad file
//     without rendering all of them.
//  3. Reload does NOT return an error for a broken file. An error there
//     would mask an otherwise-healthy catalog behind one keystroke.
//  4. A file malformed on its FIRST appearance has no last-good parse, so
//     that name resolves to PULSE_TEMPLATE_INVALID naming the path, and it
//     is listed as broken rather than as something fetchable.
//  5. Construction is the exception and stays fail-fast: at startup a broken
//     template is a deploy error the operator should see immediately.
//     TestNewStore_MalformedFileFailsConstruction holds that line.

// brokenJSON is a half-written editor save: the object is never closed, so
// the bytes cannot parse and the document therefore has no knowable name.
// It is the exact shape the story is about.
const brokenJSON = `{"target": "request", "body": {`

// breakFile overwrites path with content and pushes its modification time
// forward, so the rewrite is detectable regardless of which half of the
// store's size+mtime identity pair happened to move.
func breakFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
	touch(t, path, time.Second)
}

// TestStore_BreakageAfterStartupIsPerFile is the core of E3-S2, one subtest
// per way a file can go bad after the store was built. Every case asserts
// the same three things: the name keeps serving its last-good parse, the
// listing says it is broken, and the reload that discovered it returned nil.
func TestStore_BreakageAfterStartupIsPerFile(t *testing.T) {
	tests := []struct {
		name string
		// content is what "revenue.json" is rewritten to after the store
		// has already parsed it successfully.
		content string
	}{
		{
			name:    "truncated mid-save",
			content: brokenJSON,
		},
		{
			name:    "not JSON at all",
			content: "\x00\x01 half-flushed editor buffer",
		},
		{
			name:    "empty file",
			content: "",
		},
		{
			name:    "valid JSON with a typo'd wrapper key",
			content: `{"target":"request","varaibles":[],"body":{"cohort":{"filename":"s.pulse"}}}`,
		},
		{
			name:    "target removed",
			content: `{"body":{"cohort":{"filename":"s.pulse"}}}`,
		},
		{
			name:    "marker names an undeclared variable",
			content: `{"target":"request","variables":[{"name":"metric","type":"field"}],"body":{"aggregations":[{"$var":"metrc"}]}}`,
		},
		{
			name:    "document renamed to disagree with its path",
			content: `{"name":"something-else","target":"request","body":{"cohort":{"filename":"s.pulse"}}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeFile(t, dir, "revenue.json", validTemplate("original"))
			writeFile(t, dir, "sibling.json", validTemplate("sibling"))

			s := newStore(t, dir)
			if got := description(t, s, "revenue"); got != "original" {
				t.Fatalf("Get(revenue).Description = %q, want %q before the break", got, "original")
			}

			breakFile(t, path, tt.content)

			// (3) The reload that discovers the break reports nothing.
			if err := s.Reload(); err != nil {
				t.Fatalf("Reload() = %v, want nil — a single broken file must not fail the reload", err)
			}

			// (1) The name keeps serving what it last parsed.
			got, err := s.Get("revenue")
			if err != nil {
				t.Fatalf("Get(revenue) = %v, want the last-good template — a template that parsed once must survive its file breaking", err)
			}
			if got.Description != "original" {
				t.Errorf("Get(revenue).Description = %q, want %q — the last-good parse, not the broken bytes", got.Description, "original")
			}

			// (2) The listing says so, and names the file to open.
			sum, ok := summaryFor(s, "revenue")
			if !ok {
				t.Fatalf("List() = %v, want an entry for revenue", listedNames(s))
			}
			if !sum.Broken {
				t.Error("Summary(revenue).Broken = false, want true — serving last-good silently would leave the operator no way to find the bad file")
			}
			if sum.Error == "" {
				t.Error("Summary(revenue).Error is empty, want the parse fault")
			}
			if !strings.Contains(sum.Error, path) {
				t.Errorf("Summary(revenue).Error = %q, want it to name the offending file %q", sum.Error, path)
			}
			if sum.Path != path {
				t.Errorf("Summary(revenue).Path = %q, want %q", sum.Path, path)
			}
			// The projection still describes the last-good document, so a
			// caller mid-form-build is not left holding an empty shell.
			if sum.Target != template.TargetRequest {
				t.Errorf("Summary(revenue).Target = %q, want the last-good %q", sum.Target, template.TargetRequest)
			}

			// The sibling in the same directory is provably untouched.
			if got := description(t, s, "sibling"); got != "sibling" {
				t.Errorf("Get(sibling).Description = %q, want %q — one broken file must not reach its neighbours", got, "sibling")
			}
			if sib, ok := summaryFor(s, "sibling"); !ok || sib.Broken {
				t.Errorf("Summary(sibling) = %+v, want a healthy entry", sib)
			}

			// Exactly one file is known broken.
			if n := s.BrokenCount(); n != 1 {
				t.Errorf("BrokenCount() = %d, want 1", n)
			}
		})
	}
}

// TestStore_BrokenFileRepairsOnNextReload is the other half of the cycle.
// Serving last-good is only defensible if the store climbs back out on its
// own: an operator who fixes the typo must not have to restart, and the
// broken marker must clear rather than latch.
func TestStore_BrokenFileRepairsOnNextReload(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "revenue.json", validTemplate("original"))
	writeFile(t, dir, "sibling.json", validTemplate("sibling"))

	s := newStore(t, dir)

	// Break it.
	breakFile(t, path, brokenJSON)
	reload(t, s)

	if got := description(t, s, "revenue"); got != "original" {
		t.Fatalf("Get(revenue).Description = %q, want the last-good %q", got, "original")
	}
	if sum, _ := summaryFor(s, "revenue"); !sum.Broken {
		t.Fatal("Summary(revenue).Broken = false after the break, want true")
	}
	if n := s.BrokenCount(); n != 1 {
		t.Fatalf("BrokenCount() = %d after the break, want 1", n)
	}

	// A rescan over a file that is still the same broken bytes must not
	// re-read them. Otherwise one unrepaired file becomes a JSON parse
	// every rescan interval for as long as the process runs.
	before := s.ParseCount()
	for range 5 {
		reload(t, s)
	}
	if got := s.ParseCount(); got != before {
		t.Errorf("ParseCount() = %d after 5 reloads over an unchanged broken file, want %d — a file already diagnosed must not be re-parsed", got, before)
	}
	if n := s.BrokenCount(); n != 1 {
		t.Errorf("BrokenCount() = %d, want 1 — repeated rescans must not accumulate faults", n)
	}

	// Repair it, with different content so the assertion reads what was
	// served rather than trusting the reload to have fired.
	breakFile(t, path, validTemplate("repaired"))
	reload(t, s)

	if got := description(t, s, "revenue"); got != "repaired" {
		t.Errorf("Get(revenue).Description = %q, want %q — the repair must be picked up", got, "repaired")
	}
	sum, ok := summaryFor(s, "revenue")
	if !ok {
		t.Fatalf("List() = %v, want an entry for revenue", listedNames(s))
	}
	if sum.Broken {
		t.Errorf("Summary(revenue).Broken = true after the repair, want false — the broken state must clear, not latch")
	}
	if sum.Error != "" {
		t.Errorf("Summary(revenue).Error = %q after the repair, want empty", sum.Error)
	}
	if n := s.BrokenCount(); n != 0 {
		t.Errorf("BrokenCount() = %d after the repair, want 0", n)
	}
	if got := description(t, s, "sibling"); got != "sibling" {
		t.Errorf("Get(sibling).Description = %q, want %q throughout", got, "sibling")
	}
}

// TestStore_MalformedOnFirstAppearanceHasNoLastGood is the "never was
// valid" case the story asks to be decided and encoded. A file dropped in
// already broken has nothing to fall back on, so the fault surfaces at
// lookup — and it must surface as PULSE_TEMPLATE_INVALID naming the path,
// not as PULSE_TEMPLATE_NOT_FOUND, which would send an operator looking for
// a file they can see sitting in the directory.
func TestStore_MalformedOnFirstAppearanceHasNoLastGood(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "healthy.json", validTemplate("healthy"))

	s := newStore(t, dir)

	path := writeFile(t, dir, "newcomer.json", brokenJSON)
	touch(t, path, time.Second)
	reload(t, s)

	got, err := s.Get("newcomer")
	if err == nil {
		t.Fatalf("Get(newcomer) = %+v, want PULSE_TEMPLATE_INVALID — there is no last-good parse to serve", got)
	}
	if !perr.HasCode(err, perr.PULSE_TEMPLATE_INVALID) {
		t.Fatalf("Get(newcomer) = %v, want PULSE_TEMPLATE_INVALID", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("Get(newcomer) = %q, want the offending path %q named", err.Error(), path)
	}
	ce := codedError(t, err)
	if p := ce.Details["path"]; p != path {
		t.Errorf("details[path] = %v, want %q", p, path)
	}
	if n := ce.Details[perr.DetailTemplate]; n != "newcomer" {
		t.Errorf("details[%q] = %v, want %q — the path names the template even when the bytes do not parse",
			perr.DetailTemplate, n, "newcomer")
	}

	// It is listed — an invisible broken file is a file nobody fixes — but
	// listed in a shape that cannot be mistaken for something fetchable:
	// broken, with no declaration to project.
	sum, ok := summaryFor(s, "newcomer")
	if !ok {
		t.Fatalf("List() = %v, want the broken newcomer visible", listedNames(s))
	}
	if !sum.Broken {
		t.Error("Summary(newcomer).Broken = false, want true")
	}
	if sum.Target != "" {
		t.Errorf("Summary(newcomer).Target = %q, want empty — a file that never parsed has no declaration to report", sum.Target)
	}
	if len(sum.Variables) != 0 {
		t.Errorf("Summary(newcomer).Variables = %v, want none", sum.Variables)
	}
	if sum.Path != path {
		t.Errorf("Summary(newcomer).Path = %q, want %q", sum.Path, path)
	}

	// And the healthy neighbour is entirely unaffected.
	if got := description(t, s, "healthy"); got != "healthy" {
		t.Errorf("Get(healthy).Description = %q, want %q", got, "healthy")
	}
	if sum, _ := summaryFor(s, "healthy"); sum.Broken {
		t.Error("Summary(healthy).Broken = true, want false")
	}

	// Repairing a file that never parsed promotes it exactly like any
	// other change — there is no separate "was never valid" latch.
	breakFile(t, path, validTemplate("fixed on arrival"))
	reload(t, s)
	if got := description(t, s, "newcomer"); got != "fixed on arrival" {
		t.Errorf("Get(newcomer).Description = %q, want %q", got, "fixed on arrival")
	}
	if sum, _ := summaryFor(s, "newcomer"); sum.Broken {
		t.Error("Summary(newcomer).Broken = true after the repair, want false")
	}
}

// TestStore_BreakageIsScopedToOneNameAcrossManySiblings scales the sibling
// claim past a single neighbour. A directory of ten templates with one bad
// file must lose exactly one name's freshness, and the listing must point at
// exactly one file.
func TestStore_BreakageIsScopedToOneNameAcrossManySiblings(t *testing.T) {
	dir := t.TempDir()
	names := []string{
		"alpha", "beta", "finance/cost", "finance/revenue", "gamma",
		"deep/nested/margin", "delta", "epsilon", "zeta", "eta",
	}
	paths := make(map[string]string, len(names))
	for _, name := range names {
		paths[name] = writeFile(t, dir, name+".json", validTemplate(name))
	}

	s := newStore(t, dir)

	const victim = "finance/revenue"
	breakFile(t, paths[victim], brokenJSON)
	reload(t, s)

	if n := s.BrokenCount(); n != 1 {
		t.Fatalf("BrokenCount() = %d, want 1", n)
	}
	summaries := s.List()
	if len(summaries) != len(names) {
		t.Fatalf("List() has %d entries, want %d — a broken file must not drop out of the catalog", len(summaries), len(names))
	}
	broken := 0
	for _, sum := range summaries {
		if !sum.Broken {
			continue
		}
		broken++
		if sum.Name != victim {
			t.Errorf("Summary(%q).Broken = true, want only %q broken", sum.Name, victim)
		}
	}
	if broken != 1 {
		t.Errorf("%d listed entries are broken, want exactly 1", broken)
	}

	// Every name, the broken one included, still resolves to its content.
	for _, name := range names {
		if got := description(t, s, name); got != name {
			t.Errorf("Get(%q).Description = %q, want %q", name, got, name)
		}
	}
}

// TestStore_BrokenFileShadowedByAHealthyOverride covers the interaction
// between per-file degradation and cross-root precedence.
//
// Precedence is resolved from parses, not from files: a broken file in a
// lower-precedence root is shadowed by the healthy override above it and
// must not mark the served entry broken, because the entry an operator can
// actually reach is fine. The reverse — the WINNING file breaking — must
// mark it, because that is the file being served.
func TestStore_BrokenFileShadowedByAHealthyOverride(t *testing.T) {
	high, low := t.TempDir(), t.TempDir()
	highPath := writeFile(t, high, "shared.json", validTemplate("from high"))
	lowPath := writeFile(t, low, "shared.json", validTemplate("from low"))

	s := newStore(t, high, low)
	if got := description(t, s, "shared"); got != "from high" {
		t.Fatalf("Get(shared).Description = %q, want %q", got, "from high")
	}

	// The SHADOWED file breaks. Nothing an operator can reach changed.
	breakFile(t, lowPath, brokenJSON)
	reload(t, s)

	if got := description(t, s, "shared"); got != "from high" {
		t.Errorf("Get(shared).Description = %q, want %q — the winning file is untouched", got, "from high")
	}
	sum, ok := summaryFor(s, "shared")
	if !ok {
		t.Fatal("List() carries no summary for shared")
	}
	if sum.Broken {
		t.Errorf("Summary(shared).Broken = true, want false — the broken file is shadowed and serves nobody")
	}
	if sum.Path != highPath {
		t.Errorf("Summary(shared).Path = %q, want %q", sum.Path, highPath)
	}
	// It is still tracked internally, so repairing it restores shadowing.
	if n := s.BrokenCount(); n != 1 {
		t.Errorf("BrokenCount() = %d, want 1 — the shadowed fault is known even though it is not listed", n)
	}

	// Now the WINNING file breaks too. The store falls back to its own
	// last-good parse — not to the lower root, which is also broken.
	breakFile(t, highPath, brokenJSON)
	reload(t, s)

	if got := description(t, s, "shared"); got != "from high" {
		t.Errorf("Get(shared).Description = %q, want the winner's last-good %q", got, "from high")
	}
	sum, ok = summaryFor(s, "shared")
	if !ok {
		t.Fatal("List() carries no summary for shared")
	}
	if !sum.Broken {
		t.Error("Summary(shared).Broken = false, want true once the SERVING file is the broken one")
	}
	if !strings.Contains(sum.Error, highPath) {
		t.Errorf("Summary(shared).Error = %q, want it to name the serving file %q", sum.Error, highPath)
	}

	// Repairing both restores the ordinary shadowing report.
	breakFile(t, highPath, validTemplate("high repaired"))
	breakFile(t, lowPath, validTemplate("low repaired"))
	reload(t, s)

	if got := description(t, s, "shared"); got != "high repaired" {
		t.Errorf("Get(shared).Description = %q, want %q", got, "high repaired")
	}
	sum, _ = summaryFor(s, "shared")
	if sum.Broken {
		t.Error("Summary(shared).Broken = true after both repairs, want false")
	}
	if len(sum.Shadows) != 1 || sum.Shadows[0] != lowPath {
		t.Errorf("Summary(shared).Shadows = %v, want [%s] — shadowing must come back once the loser parses again", sum.Shadows, lowPath)
	}
	if n := s.BrokenCount(); n != 0 {
		t.Errorf("BrokenCount() = %d, want 0", n)
	}
}

// TestStore_BrokenFileDeletedStopsBeingReported is the deletion half of the
// lifecycle: a fault is snapshot state, not an accumulating log, so a file
// that stops existing stops being reported and its name goes back to being
// genuinely not-found.
func TestStore_BrokenFileDeletedStopsBeingReported(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "keeper.json", validTemplate("keeper"))
	path := writeFile(t, dir, "revenue.json", validTemplate("original"))

	s := newStore(t, dir)

	breakFile(t, path, brokenJSON)
	reload(t, s)
	if sum, _ := summaryFor(s, "revenue"); !sum.Broken {
		t.Fatal("Summary(revenue).Broken = false, want true after the break")
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	reload(t, s)

	if _, err := s.Get("revenue"); !perr.HasCode(err, perr.PULSE_TEMPLATE_NOT_FOUND) {
		t.Errorf("Get(revenue) = %v, want PULSE_TEMPLATE_NOT_FOUND — a deleted file is gone, not broken", err)
	}
	if _, ok := summaryFor(s, "revenue"); ok {
		t.Errorf("List() = %v, want revenue absent once its file is deleted", listedNames(s))
	}
	if n := s.BrokenCount(); n != 0 {
		t.Errorf("BrokenCount() = %d, want 0 — faults are snapshot state, not an accumulating log", n)
	}
	if got := description(t, s, "keeper"); got != "keeper" {
		t.Errorf("Get(keeper).Description = %q, want %q", got, "keeper")
	}
}

// TestStore_UnreadableFileDegradesLikeABrokenOne pins the filesystem-fault
// path through the same policy. A permission bit flipping on one file after
// startup is the same class of event as a bad edit — one file's problem —
// and it must not empty the catalog either. The fault keeps its own code:
// telling an operator their JSON is invalid when the real cause is a
// permission bit sends them to the wrong place.
func TestStore_UnreadableFileDegradesLikeABrokenOne(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "revenue.json", validTemplate("original"))
	writeFile(t, dir, "sibling.json", validTemplate("sibling"))

	s := newStore(t, dir)

	// The content changes so the store must re-read it, and only then
	// discovers it cannot.
	breakFile(t, path, validTemplate("edited but unreadable"))
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("this user bypasses file permissions; the unreadable-file path is untestable here")
	}

	if err := s.Reload(); err != nil {
		t.Fatalf("Reload() = %v, want nil — one unreadable file is one file's problem", err)
	}

	if got := description(t, s, "revenue"); got != "original" {
		t.Errorf("Get(revenue).Description = %q, want the last-good %q", got, "original")
	}
	sum, ok := summaryFor(s, "revenue")
	if !ok {
		t.Fatalf("List() = %v, want an entry for revenue", listedNames(s))
	}
	if !sum.Broken {
		t.Error("Summary(revenue).Broken = false, want true")
	}
	if !strings.Contains(sum.Error, path) {
		t.Errorf("Summary(revenue).Error = %q, want it to name %q", sum.Error, path)
	}
	if got := description(t, s, "sibling"); got != "sibling" {
		t.Errorf("Get(sibling).Description = %q, want %q", got, "sibling")
	}
}

// TestStore_ConcurrentRenderDuringBrokenRescan is the -race guard for this
// story specifically: readers resolving a HEALTHY template while writers
// repeatedly break and repair a neighbour underneath them.
//
// The property is not merely "no data race". It is that the healthy name
// answers correctly on every single lookup, and that the broken name never
// answers with anything except its last-good parse — no torn read, no
// panic, and no window in which a good template is missing because a bad one
// was mid-rewrite.
func TestStore_ConcurrentRenderDuringBrokenRescan(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stable.json", validTemplate("stable"))
	victim := writeFile(t, dir, "victim.json", validTemplate("victim original"))

	s := newStore(t, dir)

	const readers = 12
	const writers = 4
	const iterations = 40

	var wg sync.WaitGroup
	errs := make(chan error, (readers+writers)*iterations)

	for g := range readers {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for range iterations {
				switch g % 3 {
				case 0:
					// The untouched template must resolve every time,
					// whatever the rescan is doing to its neighbour.
					got, err := s.Get("stable")
					if err != nil {
						errs <- err
						return
					}
					if got.Description != "stable" {
						t.Errorf("Get(stable).Description = %q, want %q", got.Description, "stable")
						return
					}
				case 1:
					// The victim is being broken and repaired underneath
					// this reader. It parsed once, so it must never fail
					// and must never serve anything but a real parse.
					got, err := s.Get("victim")
					if err != nil {
						errs <- err
						return
					}
					if got.Description != "victim original" && got.Description != "victim repaired" {
						t.Errorf("Get(victim).Description = %q, want one of the two parseable fixtures", got.Description)
						return
					}
				default:
					for _, sum := range s.List() {
						if sum.Name == "" || sum.Path == "" {
							t.Errorf("List() returned an incomplete summary %+v", sum)
							return
						}
						if sum.Broken && sum.Error == "" {
							t.Errorf("List() marked %q broken with no fault text", sum.Name)
							return
						}
					}
				}
			}
		}(g)
	}

	for g := range writers {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range iterations {
				if (g+i)%2 == 0 {
					_ = os.WriteFile(victim, []byte(brokenJSON), 0o644)
				} else {
					_ = os.WriteFile(victim, []byte(validTemplate("victim repaired")), 0o644)
				}
				if err := s.Reload(); err != nil {
					errs <- err
					return
				}
			}
		}(g)
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent lookup during a broken rescan failed: %v", err)
	}
}

// TestStore_ConstructionStaysFailFast is the guard rail on the split. Every
// case above is about POST-startup breakage; at startup a broken template is
// a deploy error, and degrading there would let a process come up quietly
// serving a catalog its author never finished.
func TestStore_ConstructionStaysFailFast(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "healthy.json", validTemplate("healthy"))
	path := writeFile(t, dir, "broken.json", brokenJSON)

	got, err := template.NewStore([]string{dir})
	if err == nil {
		t.Fatalf("NewStore() = %v, want construction to fail on a malformed file", got)
	}
	if !perr.HasCode(err, perr.PULSE_TEMPLATE_INVALID) {
		t.Fatalf("NewStore() = %v, want PULSE_TEMPLATE_INVALID", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("NewStore() = %q, want the offending path %q named", err.Error(), path)
	}
	if got != nil {
		t.Errorf("NewStore() returned a store alongside the error: %v", got)
	}
}

// TestStore_UnnameableFileIsSkippedAfterStartup covers the one file shape
// that has no name to record a fault under: a bare ".json". Construction
// rejects it, where an operator is watching. After startup there is no name
// anybody could ever look it up by, so it is skipped like a README rather
// than being reported under a key that does not exist.
func TestStore_UnnameableFileIsSkippedAfterStartup(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "revenue.json", validTemplate("original"))

	s := newStore(t, dir)

	path := writeFile(t, dir, ".json", validTemplate("unnameable"))
	touch(t, path, time.Second)

	if err := s.Reload(); err != nil {
		t.Fatalf("Reload() = %v, want nil", err)
	}
	if got := listedNames(s); len(got) != 1 || got[0] != "revenue" {
		t.Errorf("List() = %v, want [revenue] — an unnameable file has no key to be reported under", got)
	}
	if n := s.BrokenCount(); n != 0 {
		t.Errorf("BrokenCount() = %d, want 0", n)
	}
	if got := description(t, s, "revenue"); got != "original" {
		t.Errorf("Get(revenue).Description = %q, want %q", got, "original")
	}

	// Construction is still the strict path for the same file.
	if _, err := template.NewStore([]string{dir}); !perr.HasCode(err, perr.PULSE_TEMPLATE_INVALID) {
		t.Errorf("NewStore() = %v, want PULSE_TEMPLATE_INVALID for a file with no derivable name", err)
	}
}
