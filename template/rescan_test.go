package template_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/template"
)

// touch pushes a file's modification time forward by d. Every rescan test
// that edits a file calls it, because the change check pairs size with
// mtime and a test must not depend on which of the two happened to move on
// the host filesystem.
func touch(t *testing.T, path string, d time.Duration) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s): %v", path, err)
	}
	at := info.ModTime().Add(d)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatalf("Chtimes(%s): %v", path, err)
	}
}

// reload forces a rescan and fails the test on error.
func reload(t *testing.T, s *template.Store) {
	t.Helper()
	if err := s.Reload(); err != nil {
		t.Fatalf("Reload() = %v, want nil error", err)
	}
}

// description resolves name and returns the loaded document's description,
// which every fixture in this file uses as its content marker. It is the
// content assertion the story asks for: "did the store pick the file up?"
// is answered by reading what it serves, not by trusting a heuristic.
func description(t *testing.T, s *template.Store, name string) string {
	t.Helper()
	got, err := s.Get(name)
	if err != nil {
		t.Fatalf("Get(%q) = %v, want the template", name, err)
	}
	return got.Description
}

// TestStore_ReloadPicksUpDirectoryChanges is the hot-reload contract, one
// subtest per lifecycle event a template directory sees after startup.
//
// Every case forces the rescan with Reload rather than waiting out
// templateRescanInterval. A sleeping test would be slow in the good case
// and flaky in the bad one, and Reload exists precisely so neither is
// necessary.
func TestStore_ReloadPicksUpDirectoryChanges(t *testing.T) {
	tests := []struct {
		name string
		// mutate changes the directory after the store is built.
		mutate func(t *testing.T, dir string)
		// wantDesc is the description "revenue" must serve after the
		// reload, or "" when the name must no longer resolve at all.
		wantDesc string
		// wantNames is the full listing after the reload.
		wantNames []string
	}{
		{
			name:      "no change at all",
			mutate:    func(*testing.T, string) {},
			wantDesc:  "original",
			wantNames: []string{"revenue"},
		},
		{
			name: "a file added after construction becomes renderable",
			mutate: func(t *testing.T, dir string) {
				writeFile(t, dir, "finance/new.json", validTemplate("brand new"))
			},
			wantDesc:  "original",
			wantNames: []string{"finance/new", "revenue"},
		},
		{
			name: "a modified file serves its new content",
			mutate: func(t *testing.T, dir string) {
				path := writeFile(t, dir, "revenue.json", validTemplate("edited in place"))
				touch(t, path, time.Second)
			},
			wantDesc:  "edited in place",
			wantNames: []string{"revenue"},
		},
		{
			name: "a deleted file stops resolving",
			mutate: func(t *testing.T, dir string) {
				if err := os.Remove(filepath.Join(dir, "revenue.json")); err != nil {
					t.Fatalf("Remove: %v", err)
				}
			},
			wantDesc:  "",
			wantNames: nil,
		},
		{
			name: "a whole subtree added at once",
			mutate: func(t *testing.T, dir string) {
				writeFile(t, dir, "a/b/c.json", validTemplate("deep"))
				writeFile(t, dir, "a/b/d.json", validTemplate("deeper sibling"))
			},
			wantDesc:  "original",
			wantNames: []string{"a/b/c", "a/b/d", "revenue"},
		},
		{
			name: "a non-JSON file added is still ignored",
			mutate: func(t *testing.T, dir string) {
				writeFile(t, dir, "README.md", "# not a template")
				writeFile(t, dir, "revenue.json.swp", "half-written editor state")
			},
			wantDesc:  "original",
			wantNames: []string{"revenue"},
		},
		{
			name: "a file deleted and replaced under the same name",
			mutate: func(t *testing.T, dir string) {
				path := filepath.Join(dir, "revenue.json")
				if err := os.Remove(path); err != nil {
					t.Fatalf("Remove: %v", err)
				}
				writeFile(t, dir, "revenue.json", validTemplate("replaced"))
				touch(t, path, time.Second)
			},
			wantDesc:  "replaced",
			wantNames: []string{"revenue"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "revenue.json", validTemplate("original"))

			s := newStore(t, dir)
			if got := description(t, s, "revenue"); got != "original" {
				t.Fatalf("before the change, Get(revenue).Description = %q, want %q", got, "original")
			}

			tt.mutate(t, dir)
			reload(t, s)

			if tt.wantDesc == "" {
				got, err := s.Get("revenue")
				if err == nil {
					t.Fatalf("Get(revenue) = %v, want PULSE_TEMPLATE_NOT_FOUND after the file was deleted", got)
				}
				if !perr.HasCode(err, perr.PULSE_TEMPLATE_NOT_FOUND) {
					t.Fatalf("Get(revenue) = %v, want PULSE_TEMPLATE_NOT_FOUND", err)
				}
			} else if got := description(t, s, "revenue"); got != tt.wantDesc {
				t.Errorf("Get(revenue).Description = %q, want %q", got, tt.wantDesc)
			}

			got := listedNames(s)
			if len(got) != len(tt.wantNames) {
				t.Fatalf("List() = %v, want %v", got, tt.wantNames)
			}
			for i := range tt.wantNames {
				if got[i] != tt.wantNames[i] {
					t.Fatalf("List() = %v, want %v", got, tt.wantNames)
				}
			}
		})
	}
}

// TestStore_ReloadSkipsUnchangedFiles is the cost contract. A rescan is a
// walk plus a stat per file; a file whose size and modification time both
// match the copy already parsed is carried over, not re-read. Without this,
// a one-second rescan interval would turn a directory of fifty templates
// into fifty JSON parses a second forever.
//
// The assertion is a parse counter rather than a timing measurement,
// because the claim is about work not done and timing cannot prove that.
func TestStore_ReloadSkipsUnchangedFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "revenue.json", validTemplate("one"))
	writeFile(t, dir, "finance/cost.json", validTemplate("two"))
	writeFile(t, dir, "deep/nested/margin.json", validTemplate("three"))

	s := newStore(t, dir)

	const files = 3
	if got := s.ParseCount(); got != files {
		t.Fatalf("after construction ParseCount() = %d, want %d — every file is parsed once at startup", got, files)
	}

	for i := range 5 {
		reload(t, s)
		if got := s.ParseCount(); got != files {
			t.Fatalf("after reload %d ParseCount() = %d, want %d — an unchanged file must not be re-parsed", i+1, got, files)
		}
	}
	if got := s.ScanCount(); got != 1+5 {
		t.Errorf("ScanCount() = %d, want 6 — each Reload must actually walk", got)
	}

	// One file changes: exactly that file is re-parsed, and the two that
	// did not change are still carried over.
	path := writeFile(t, dir, "finance/cost.json", validTemplate("two, revised at a different length"))
	touch(t, path, time.Second)
	reload(t, s)

	if got := s.ParseCount(); got != files+1 {
		t.Errorf("ParseCount() = %d, want %d — the changed file and only the changed file is re-parsed", got, files+1)
	}
	if got := description(t, s, "finance/cost"); got != "two, revised at a different length" {
		t.Errorf("Get(finance/cost).Description = %q, want the revised content", got)
	}
	if got := description(t, s, "revenue"); got != "one" {
		t.Errorf("Get(revenue).Description = %q, want the untouched content", got)
	}
}

// TestStore_RescanIntervalGatesTheWalk pins the interval itself. A lookup
// inside the interval must not walk, and the first lookup after it must.
//
// The clock is faked rather than slept on: the property under test is "the
// gate consults the elapsed time", and moving a fake clock asserts that
// directly and instantly. Sleeping past a one-second interval would assert
// the same thing an order of magnitude slower, and would go red on a busy
// machine for reasons that have nothing to do with the store.
func TestStore_RescanIntervalGatesTheWalk(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "revenue.json", validTemplate("original"))

	s := newStore(t, dir)

	base := time.Now()
	clock := base
	s.SetClock(func() time.Time { return clock })

	start := s.ScanCount()

	// Inside the interval: any number of lookups, no walk.
	for range 10 {
		if _, err := s.Get("revenue"); err != nil {
			t.Fatalf("Get(revenue) = %v", err)
		}
		s.List()
	}
	if got := s.ScanCount(); got != start {
		t.Fatalf("ScanCount() = %d after 10 lookups inside the interval, want %d — the snapshot is still fresh", got, start)
	}

	// A file added inside the interval is therefore not visible yet. That
	// is the accepted tradeoff, stated as a test so it stays a decision
	// rather than becoming a surprise.
	writeFile(t, dir, "added.json", validTemplate("added"))
	if _, err := s.Get("added"); !perr.HasCode(err, perr.PULSE_TEMPLATE_NOT_FOUND) {
		t.Fatalf("Get(added) = %v inside the interval, want PULSE_TEMPLATE_NOT_FOUND — the snapshot has not aged out", err)
	}

	// Past the interval: the next lookup walks, and exactly one walk
	// serves every lookup that follows it.
	clock = base.Add(template.RescanInterval + time.Millisecond)
	if got := description(t, s, "added"); got != "added" {
		t.Errorf("Get(added).Description = %q, want %q — the first lookup past the interval must rescan", got, "added")
	}
	if got := s.ScanCount(); got != start+1 {
		t.Fatalf("ScanCount() = %d, want %d — one aged-out lookup means exactly one walk", got, start+1)
	}
	for range 10 {
		if _, err := s.Get("added"); err != nil {
			t.Fatalf("Get(added) = %v", err)
		}
	}
	if got := s.ScanCount(); got != start+1 {
		t.Fatalf("ScanCount() = %d, want %d — the walk re-anchored the snapshot", got, start+1)
	}

	// Reload ignores the gate entirely: no clock movement, still a walk.
	reload(t, s)
	if got := s.ScanCount(); got != start+2 {
		t.Errorf("ScanCount() = %d after Reload(), want %d — Reload must not consult the interval", got, start+2)
	}
}

// TestStore_ReloadReresolvesShadowing asserts precedence is recomputed from
// the fresh index rather than frozen at construction. A file appearing in a
// higher-precedence root must start winning, and removing it must hand the
// name back to the root underneath — otherwise a layered deployment could
// only be re-layered by restarting.
func TestStore_ReloadReresolvesShadowing(t *testing.T) {
	high, low := t.TempDir(), t.TempDir()
	lowPath := writeFile(t, low, "shared.json", validTemplate("from low"))

	s := newStore(t, high, low)

	if got := description(t, s, "shared"); got != "from low" {
		t.Fatalf("Get(shared).Description = %q, want %q — only the low root carries it", got, "from low")
	}
	if sum, ok := summaryFor(s, "shared"); !ok || len(sum.Shadows) != 0 {
		t.Fatalf("Summary(shared).Shadows = %v, want none", sum.Shadows)
	}

	// An override appears in the higher-precedence root.
	highPath := writeFile(t, high, "shared.json", validTemplate("from high"))
	reload(t, s)

	if got := description(t, s, "shared"); got != "from high" {
		t.Fatalf("Get(shared).Description = %q, want %q — the new higher-precedence file must shadow the lower one", got, "from high")
	}
	sum, ok := summaryFor(s, "shared")
	if !ok {
		t.Fatal("List() carries no summary for shared")
	}
	if sum.Path != highPath {
		t.Errorf("Summary.Path = %q, want %q", sum.Path, highPath)
	}
	if len(sum.Shadows) != 1 || sum.Shadows[0] != lowPath {
		t.Errorf("Summary.Shadows = %v, want [%s]", sum.Shadows, lowPath)
	}

	// The override is withdrawn: the lower root answers again, and the
	// name never stops resolving.
	if err := os.Remove(highPath); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	reload(t, s)

	if got := description(t, s, "shared"); got != "from low" {
		t.Fatalf("Get(shared).Description = %q, want %q — removing the override must restore the lower root", got, "from low")
	}
	if sum, ok := summaryFor(s, "shared"); !ok || len(sum.Shadows) != 0 {
		t.Errorf("Summary(shared).Shadows = %v, want none once the override is gone", sum.Shadows)
	}
}

// TestStore_ReloadOnNilStoreIsANoOp covers the unconfigured shape. A nil
// store has no directories to walk, so forcing a rescan is a no-op rather
// than a fault — which is what lets the facade forward the call without a
// nil check.
func TestStore_ReloadOnNilStoreIsANoOp(t *testing.T) {
	var s *template.Store
	if err := s.Reload(); err != nil {
		t.Errorf("(*Store)(nil).Reload() = %v, want nil", err)
	}
	if got := s.List(); len(got) != 0 {
		t.Errorf("(*Store)(nil).List() = %v, want empty after Reload()", got)
	}

	// A store built over zero roots is the same story: nothing to walk,
	// nothing to fail.
	empty := newStore(t)
	if err := empty.Reload(); err != nil {
		t.Errorf("Reload() on a store with no roots = %v, want nil", err)
	}
	if got := empty.List(); len(got) != 0 {
		t.Errorf("List() = %v, want empty", got)
	}
}

// TestStore_ReloadReportsWalkFaults asserts Reload is the reporting path.
// Lookups swallow a rescan fault on purpose — one file mid-save must not
// fail an unrelated template — so the explicit call is where an operator
// finds out, and it must not silently succeed.
func TestStore_ReloadReportsWalkFaults(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "revenue.json", validTemplate("original"))

	s := newStore(t, dir)

	path := writeFile(t, dir, "broken.json", `{"target": "request", "body": {`)
	touch(t, path, time.Second)

	err := s.Reload()
	if err == nil {
		t.Fatal("Reload() = nil error, want the walk fault for a malformed file")
	}
	if !perr.HasCode(err, perr.PULSE_TEMPLATE_INVALID) {
		t.Fatalf("Reload() = %v, want PULSE_TEMPLATE_INVALID", err)
	}

	// The failed walk left the previous index in place rather than
	// emptying the store: one bad file must not take the catalog down.
	// E3-S2 narrows this to per-file degradation.
	if got := description(t, s, "revenue"); got != "original" {
		t.Errorf("Get(revenue).Description = %q, want %q — a failed walk must not discard the last good index", got, "original")
	}
	if _, err := s.Get("revenue"); err != nil {
		t.Errorf("Get(revenue) = %v after a failed reload, want the last good template", err)
	}
}

// TestStore_ConcurrentLookupDuringRescan is the -race guard for the rescan
// path: readers resolving names while writers force walks that add,
// change, and remove files underneath them. Every lookup must return a
// coherent answer or a clean not-found — never a torn index and never a
// panic.
func TestStore_ConcurrentLookupDuringRescan(t *testing.T) {
	high, low := t.TempDir(), t.TempDir()
	writeFile(t, low, "shared.json", validTemplate("from low"))
	writeFile(t, low, "stable.json", validTemplate("stable"))

	s := newStore(t, high, low)

	const readers = 12
	const reloaders = 4
	const iterations = 40

	var wg sync.WaitGroup
	errs := make(chan error, (readers+reloaders)*iterations)

	for g := range readers {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for range iterations {
				switch g % 3 {
				case 0:
					// "shared" is being shadowed and un-shadowed
					// underneath this reader; either answer is
					// correct, a torn one is not.
					got, err := s.Get("shared")
					if err != nil {
						errs <- err
						return
					}
					if got.Description != "from low" && got.Description != "from high" {
						t.Errorf("Get(shared).Description = %q, want one of the two fixtures", got.Description)
						return
					}
				case 1:
					// "stable" is never touched: it must resolve on
					// every single lookup, whatever the rescan is doing.
					got, err := s.Get("stable")
					if err != nil {
						errs <- err
						return
					}
					if got.Description != "stable" {
						t.Errorf("Get(stable).Description = %q, want %q", got.Description, "stable")
						return
					}
				default:
					for _, sum := range s.List() {
						if sum.Name == "" || sum.Path == "" {
							t.Errorf("List() returned an incomplete summary %+v", sum)
							return
						}
					}
				}
			}
		}(g)
	}

	for g := range reloaders {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			path := filepath.Join(high, "shared.json")
			for i := range iterations {
				if (g+i)%2 == 0 {
					_ = os.WriteFile(path, []byte(validTemplate("from high")), 0o644)
				} else {
					_ = os.Remove(path)
				}
				// A fault is legitimate here — a reader goroutine may
				// be mid-write when the walk stats the file — so the
				// return value is deliberately not asserted. What is
				// asserted is that nothing races or panics.
				_ = s.Reload()
			}
		}(g)
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent lookup during rescan failed: %v", err)
	}
}
