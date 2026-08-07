package template

import (
	"cmp"
	stderrors "errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/frankbardon/pulse/errors"
)

// templateExt is the only file extension the store recognises. Everything
// else under a template directory — a README, an editor swap file, a
// checked-in .gitignore — is skipped silently, because a template directory
// is a directory a human keeps files in, not a directory the store owns.
const templateExt = ".json"

// detailPath is the CodedError.Details key carrying the source file a
// store-raised template error refers to. It sits alongside
// errors.DetailTemplate: the name says which template failed, the path says
// which file to open.
const detailPath = "path"

// templateRescanInterval is how long the store trusts its snapshot of the
// configured roots before the next lookup re-walks them.
//
// One second is short enough that a file dropped into a template directory
// becomes renderable while its author is still looking at the screen, and
// long enough that a burst of lookups costs one walk rather than one per
// call. The accepted tradeoff is that a new file can be invisible for up
// to a second; Store.Reload is the answer for a caller who cannot tolerate
// that.
//
// It is deliberately a package constant rather than a configuration knob.
// A tunable here would be a dial nobody can set better than the store can,
// and it would buy a permanent public option to cover a case Reload
// already covers exactly.
const templateRescanInterval = time.Second

// Store is a name→template lookup built from an ordered list of directory
// roots. It is what makes a template a file rather than a Go literal.
//
// # Discovery
//
// Each configured root is walked with filepath.WalkDir and every *.json
// file under it becomes a template. Directories and non-.json files are
// skipped silently. A configured root that does not exist is skipped too —
// a layered setup routinely names an optional override directory that is
// simply absent — but a configured path that exists and is NOT a directory
// is an error, because that is a misconfiguration rather than an absence.
//
// # Naming
//
// A template's name is its path relative to its OWN root, minus the .json
// extension, forward-slash separated regardless of host. A file at
// templates/finance/revenue.json under the root templates/ is named
// finance/revenue. Subdirectories therefore namespace for free, and the
// root's own location never leaks into the name — which is what lets the
// same relative layout be served from two different roots.
//
// The derived name is authoritative and is stamped onto Template.Name. A
// document may repeat its own name, but a document claiming a name its path
// does not give it is a PULSE_TEMPLATE_INVALID: silently renaming an
// author's template is exactly the class of surprise this surface exists to
// remove.
//
// Within a single root, two files can never derive the same name — the
// mapping is invertible, since appending ".json" to a name recovers the
// relative path exactly. Collisions are therefore always cross-root.
//
// # Precedence
//
// Roots are an ordered precedence list and the FIRST root wins. A duplicate
// name across roots is not an error: the earlier root's entry answers
// lookups and the later ones are recorded as shadowed, surfacing in
// Summary.Shadows so a layered setup is visible rather than mysterious.
//
// Precedence is resolved at LOOKUP time, not baked in at construction. That
// is deliberate: hot reload adds files to the index after the fact, and a
// file dropped into a lower-precedence root must never be able to displace
// — or break — a name that already worked.
//
// # Validation
//
// Every discovered file is read, parsed, and declaration-validated during
// construction, so a malformed template fails at startup with the offending
// path named rather than lying in wait until someone renders it. Parse
// alone cannot name the file (a document whose bytes do not parse has no
// knowable name), so the store re-raises every parse fault with the path
// and the derived name attached, preserving the original code.
//
// # Hot reload
//
// The index is a cached snapshot, not a boot-time freeze. A lookup whose
// snapshot has aged past templateRescanInterval re-walks the configured
// roots first, so a template file dropped into a scanned directory becomes
// renderable without restarting the process — and a deleted one stops
// resolving. Precedence is re-resolved from the fresh index, so a file
// appearing in a higher-precedence root starts shadowing a lower one and
// removing it hands the name back.
//
// A rescan is a directory walk plus one os.Stat per candidate file. A file
// whose size AND modification time both match the parsed copy already held
// is reused as-is, so a steady-state rescan over a directory of unchanged
// templates costs syscalls and no JSON parsing at all.
//
// Reload forces the walk immediately, ignoring the interval, for callers
// who need determinism rather than eventual visibility.
//
// # Concurrency
//
// Every exported method is safe for concurrent use, and the zero-value nil
// *Store is usable: it lists nothing and reports every lookup as
// PULSE_TEMPLATE_NOT_FOUND. That keeps "no template directories configured"
// an ordinary answer rather than a nil check at every call site.
type Store struct {
	mu sync.RWMutex

	// dirs is the configured root list, in precedence order. Held as the
	// store's own copy so a caller mutating the slice it passed in cannot
	// change where the store looks.
	dirs []string

	// byName indexes every discovered file by its derived name. A name
	// maps to one candidate per root that carries it; the winner is
	// chosen at lookup, not at insert. Replaced wholesale by a rescan
	// rather than mutated in place.
	byName map[string][]*fileEntry

	// lastScan is when byName was last built from disk, on the store's
	// own clock. It is what the rescan interval is measured against, and
	// it is claimed BEFORE a walk starts so a burst of concurrent
	// lookups produces one walk rather than one each.
	lastScan time.Time

	// clock reads the current time. It is time.Now in every non-test
	// build; the seam exists so the interval can be exercised by moving
	// a fake clock instead of by sleeping, because a sleeping test for a
	// one-second interval is both slow and flaky.
	clock func() time.Time

	// scans counts completed walks and parses counts files actually read
	// and parsed by them. They are instrumentation: they let a test
	// assert that the interval really gated a walk, and that an
	// unchanged file really was not re-parsed, without inferring either
	// from timing.
	scans  uint64
	parses uint64
}

// fileEntry is one discovered template file: the parsed document plus the
// provenance needed to arbitrate precedence, to tell a caller where the
// document came from, and to decide on the next rescan whether the file
// needs re-reading.
//
// An entry is immutable once built. A rescan that finds a file unchanged
// carries the SAME entry pointer into the new index rather than rebuilding
// it, so two snapshots share entries and nothing may write to one after
// construction.
type fileEntry struct {
	// name is the derived lookup name.
	name string

	// path is the source file, as reachable from the process's working
	// directory (root-relative roots yield root-relative paths).
	path string

	// rootIndex is the position of the owning root in the configured
	// list. It is the primary precedence key: lower wins.
	rootIndex int

	// size and modTime are the file's identity as of the scan that
	// parsed it. A rescan re-reads the file only when one of them moves.
	// The pair is deliberate: mtime granularity is coarse on some
	// filesystems, so a rewrite inside a single tick can leave mtime
	// untouched and is caught by the length instead.
	size    int64
	modTime time.Time

	// tmpl is the parsed, declaration-validated document, with Name
	// already stamped from the derived name.
	tmpl *Template
}

// NewStore walks dirs in order and returns a store over every *.json
// template found beneath them. Roots earlier in the slice take precedence
// over later ones.
//
// Empty and blank root entries are skipped, as are roots that do not exist.
// A root that exists but is not a directory, an unreadable file, and any
// document that fails declaration validation are all errors — construction
// is the fail-fast point, so nothing malformed survives into the index.
//
// An empty dirs slice yields a usable empty store rather than an error:
// "no template directories configured" is a normal deployment, not a fault.
func NewStore(dirs []string) (*Store, error) {
	s := &Store{dirs: slices.Clone(dirs), clock: time.Now}
	res, err := scanDirs(s.dirs, nil)
	if err != nil {
		return nil, err
	}
	s.byName = res.byName
	s.scans = 1
	s.parses = res.parsed
	s.lastScan = s.now()
	return s, nil
}

// Get returns the template registered under name. Lookup is exact and
// case-sensitive, and the name is the derived one — "finance/revenue", not
// "finance/revenue.json" and not the absolute path.
//
// When two roots carry the same name the earlier root's template is
// returned; the shadowed ones are reachable through List. An unregistered
// name — including every name on a nil store — is PULSE_TEMPLATE_NOT_FOUND
// carrying the requested name under errors.DetailTemplate.
//
// A lookup is also the store's clock: when the cached snapshot has aged
// past templateRescanInterval the configured roots are re-walked first, so
// a template added to a directory after startup resolves here without a
// restart. Reload forces that walk immediately.
//
// The returned template is the store's own copy and must be treated as
// read-only; rendering never mutates it.
func (s *Store) Get(name string) (*Template, error) {
	if s == nil {
		return nil, notFound(name)
	}
	s.refreshQuietly()
	s.mu.RLock()
	defer s.mu.RUnlock()

	winner, _ := s.winnerLocked(name)
	if winner == nil {
		return nil, notFound(name)
	}
	return winner.tmpl, nil
}

// List returns one summary per registered name, sorted by name, so the
// order is deterministic across runs and platforms.
//
// A shadowed entry gets no summary of its own — it is not renderable, and a
// listing whose entries cannot all be fetched would be a trap. It surfaces
// instead on the winner's Summary.Shadows, which names the source paths the
// winning entry takes precedence over. That is what makes "my override is
// not taking effect" answerable from the listing alone.
//
// Like Get, a listing rescans first when the cached snapshot has aged past
// templateRescanInterval, so a listing reflects the directories rather than
// the moment the process started.
//
// Returns nil for a nil store.
func (s *Store) List() []Summary {
	if s == nil {
		return nil
	}
	s.refreshQuietly()
	s.mu.RLock()
	defer s.mu.RUnlock()

	names := slices.Sorted(maps.Keys(s.byName))
	out := make([]Summary, 0, len(names))
	for _, name := range names {
		winner, shadowed := s.winnerLocked(name)
		if winner == nil {
			continue
		}
		paths := make([]string, 0, len(shadowed))
		for _, e := range shadowed {
			paths = append(paths, e.path)
		}
		out = append(out, winner.tmpl.Summarize(winner.path, paths))
	}
	return out
}

// Dirs returns the configured roots in precedence order, as a copy. It
// answers "where is this store looking?" for diagnostics and for the
// rescan that layers on top of it.
func (s *Store) Dirs() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.dirs)
}

// Reload re-walks every configured root immediately and swaps in the
// result, ignoring the rescan interval. It is the determinism escape
// hatch: lookups pick new and changed files up on their own within
// templateRescanInterval, and Reload is for the caller who needs the
// answer now rather than shortly.
//
// It is also what makes hot reload testable without sleeping. A test that
// waited out a one-second interval would be slow in the good case and
// flaky in the bad one; a test that calls Reload asserts the same
// behaviour deterministically.
//
// Returns whatever the walk raises — an unreadable directory, an
// unreadable file, or a document that no longer parses. A failed walk
// leaves the previous index in place: a store that emptied itself because
// one file went briefly bad would take every other template down with it.
//
// A nil store has nothing to walk and returns nil, so "no template
// directories configured" needs no nil check at the call site.
func (s *Store) Reload() error {
	if s == nil {
		return nil
	}
	return s.refresh(true)
}

// refreshQuietly is the lookup-path rescan: it rescans when the snapshot
// has aged out and discards any fault the walk raises.
//
// Swallowing is the point. A lookup for one template must not fail because
// an unrelated file in the directory is mid-save — the previous index is
// still a complete, valid answer, and failing here would convert one
// operator's editor keystroke into an outage for every other template.
// Reload is the path that reports.
func (s *Store) refreshQuietly() {
	_ = s.refresh(false)
}

// refresh re-walks the configured roots and swaps in the resulting index.
// When force is false the walk is skipped while the snapshot is still
// inside templateRescanInterval.
func (s *Store) refresh(force bool) error {
	if !force && s.isFresh() {
		return nil
	}

	s.mu.Lock()
	now := s.now()
	if !force && now.Sub(s.lastScan) < templateRescanInterval {
		// Another goroutine claimed the walk between the fast-path check
		// and this lock.
		s.mu.Unlock()
		return nil
	}
	// Claim the walk before releasing the lock. The walk itself runs
	// unlocked — it is directory I/O and JSON parsing, and holding every
	// reader off for its duration would make the rescan the very stall
	// the interval exists to prevent — so stamping lastScan up front is
	// what stops a burst of concurrent lookups from each starting one.
	s.lastScan = now
	dirs := slices.Clone(s.dirs)
	prev := indexByPath(s.byName)
	s.mu.Unlock()

	res, err := scanDirs(dirs, prev)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.byName = res.byName
	s.scans++
	s.parses += res.parsed
	s.mu.Unlock()
	return nil
}

// isFresh reports whether the cached snapshot is still inside the rescan
// interval. It is the read-locked fast path: the steady state is "no walk
// needed", and answering that under the write lock would serialise every
// concurrent lookup behind a decision that reads one timestamp.
func (s *Store) isFresh() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.now().Sub(s.lastScan) < templateRescanInterval
}

// now reads the store's clock. Callers hold the lock. The nil guard keeps
// a zero-value Store — which no constructor produces, but which the type's
// exported shape permits — from panicking on its first lookup.
func (s *Store) now() time.Time {
	if s.clock == nil {
		return time.Now()
	}
	return s.clock()
}

// indexByPath re-keys an index by source file so a rescan can ask "is this
// exact file still the one I parsed?" of each candidate it walks. Callers
// hold at least the read lock.
func indexByPath(byName map[string][]*fileEntry) map[string]*fileEntry {
	out := make(map[string]*fileEntry, len(byName))
	for _, entries := range byName {
		for _, e := range entries {
			out[e.path] = e
		}
	}
	return out
}

// winnerLocked resolves precedence for name, returning the winning entry
// and the entries it shadows, both ordered by root position. Callers must
// hold at least the read lock.
//
// The ordering is recomputed here rather than being relied on from insert
// order: precedence has to hold for an index that gained entries after
// construction, and a rule that only holds for a freshly built index is a
// rule that breaks the first time a file is dropped in.
func (s *Store) winnerLocked(name string) (winner *fileEntry, shadowed []*fileEntry) {
	candidates := s.byName[name]
	if len(candidates) == 0 {
		return nil, nil
	}
	ordered := slices.Clone(candidates)
	slices.SortStableFunc(ordered, func(a, b *fileEntry) int {
		if c := cmp.Compare(a.rootIndex, b.rootIndex); c != 0 {
			return c
		}
		return cmp.Compare(a.path, b.path)
	})
	return ordered[0], ordered[1:]
}

// scanResult is one completed walk of the configured roots: the index it
// produced, plus how many files it had to actually read and parse rather
// than carry over unchanged.
type scanResult struct {
	byName map[string][]*fileEntry

	// parsed counts files read from disk and parsed by this walk. It is
	// the measure of what a rescan cost — in the steady state it is zero,
	// which is the whole point of pairing the walk with a change check.
	parsed uint64
}

// scanDirs walks every root in order and builds the name index. Roots are
// visited in precedence order so the index is built in that order too, but
// nothing downstream depends on it — rootIndex is what precedence reads.
//
// prev is the previous walk's index keyed by path, or nil for the first
// walk. Any file in it whose size and modification time still match is
// carried into the new index without being re-read.
func scanDirs(dirs []string, prev map[string]*fileEntry) (scanResult, error) {
	res := scanResult{byName: make(map[string][]*fileEntry)}
	seenPaths := make(map[string]struct{})

	for i, dir := range dirs {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		info, err := os.Stat(dir)
		if err != nil {
			if os.IsNotExist(err) {
				// An absent root is an absent layer, not a fault: a
				// layered setup routinely names an optional override
				// directory nobody has created yet.
				continue
			}
			return scanResult{}, ioError(dir, "reading template directory", err)
		}
		if !info.IsDir() {
			return scanResult{}, errors.NewCodedErrorWithDetails(errors.DATA_FILE,
				"template directory "+strconv.Quote(dir)+" is not a directory; "+
					"each configured template root must be a directory of *.json template files",
				map[string]any{detailPath: dir})
		}
		if err := scanDir(dir, i, prev, &res, seenPaths); err != nil {
			return scanResult{}, err
		}
	}
	return res, nil
}

// scanDir walks one root, appending every valid template it finds. The walk
// stops at the first fault: a template directory holding a broken document
// is a directory whose owner needs to know now.
func scanDir(dir string, rootIndex int, prev map[string]*fileEntry, res *scanResult, seenPaths map[string]struct{}) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return ioError(path, "walking template directory", walkErr)
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), templateExt) {
			return nil
		}
		name, err := templateName(dir, path)
		if err != nil {
			return err
		}
		if _, dup := seenPaths[path]; dup {
			// The same root listed twice would otherwise shadow itself
			// with its own files, reporting a phantom collision.
			return nil
		}
		seenPaths[path] = struct{}{}

		// os.Stat rather than the walk entry's own Info: WalkDir does not
		// follow symlinks, so d.Info() describes the LINK, and a template
		// symlinked into a directory would then look unchanged however
		// often its target was edited. The reuse decision needs the
		// identity of the bytes ReadFile will actually return.
		info, err := os.Stat(path)
		if err != nil {
			return ioError(path, "reading template file", err)
		}

		entry := unchangedEntry(prev, path, name, rootIndex, info)
		if entry == nil {
			if entry, err = loadEntry(path, name, rootIndex, info); err != nil {
				return err
			}
			res.parsed++
		}
		res.byName[name] = append(res.byName[name], entry)
		return nil
	})
}

// unchangedEntry returns the previous walk's entry for path when the file
// on disk still looks like the one already parsed, and nil when it must be
// read again.
//
// This is what keeps a steady-state rescan a syscall cost rather than a
// parse cost: fifty unchanged templates re-walk in fifty stats and zero
// JSON parses.
//
// Size is paired with modification time deliberately. mtime granularity is
// coarse on some filesystems, so a rewrite inside one tick can leave mtime
// untouched — a rewrite that also changes the file's length is caught by
// the size instead. A rewrite that moves NEITHER is the documented blind
// spot of the heuristic, and the reason Reload exists.
//
// The name and root are compared too. Neither can change for a fixed path
// while the configured roots are fixed, but reusing an entry whose
// provenance had drifted would mislabel a template rather than merely
// serve it stale, so the check is cheap insurance.
func unchangedEntry(prev map[string]*fileEntry, path, name string, rootIndex int, info os.FileInfo) *fileEntry {
	old, ok := prev[path]
	if !ok {
		return nil
	}
	if old.name != name || old.rootIndex != rootIndex {
		return nil
	}
	if old.size != info.Size() || !old.modTime.Equal(info.ModTime()) {
		return nil
	}
	return old
}

// loadEntry reads, parses, and declaration-validates one template file,
// stamping the derived name and the file identity a later rescan compares
// against.
//
// The identity recorded is the one observed BEFORE the read. A file
// rewritten between the stat and the read is therefore stamped with a size
// and mtime that no longer match the disk, so the next walk sees a
// mismatch and re-reads it — the race resolves itself one rescan later
// rather than latching stale content.
func loadEntry(path, name string, rootIndex int, info os.FileInfo) (*fileEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, ioError(path, "reading template file", err)
	}
	tmpl, err := Parse(data)
	if err != nil {
		return nil, loadError(path, name, err)
	}
	if tmpl.Name != "" && tmpl.Name != name {
		return nil, invalidFile(path, name,
			"template file declares name "+strconv.Quote(tmpl.Name)+
				" but its path names it "+strconv.Quote(name)+
				"; the path is authoritative, so drop the `name` key or move the file")
	}
	tmpl.Name = name

	return &fileEntry{
		name:      name,
		path:      path,
		rootIndex: rootIndex,
		size:      info.Size(),
		modTime:   info.ModTime(),
		tmpl:      tmpl,
	}, nil
}

// templateName derives a template's lookup name from its path relative to
// its own root: the extension is dropped and separators are normalised to
// forward slashes, so the same layout yields the same names on every host.
//
// A file whose relative path is nothing but the extension (".json", or
// "finance/.json") yields no name, and a template with no name cannot be
// looked up — Validate deliberately does not require Name, because naming
// is the store's job, which makes the store the layer that has to reject it.
func templateName(root, path string) (string, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", ioError(path, "resolving template path against its root", err)
	}
	name := strings.TrimSuffix(filepath.ToSlash(rel), templateExt)
	if name == "" || strings.HasSuffix(name, "/") {
		return "", invalidFile(path, "",
			"template file has no derivable name; a template's name is its path relative to "+
				"its directory root minus the "+strconv.Quote(templateExt)+" extension, and this path leaves nothing")
	}
	return name, nil
}

// notFound builds the lookup miss, naming the requested template.
func notFound(name string) error {
	return errors.NewCodedErrorWithDetails(errors.PULSE_TEMPLATE_NOT_FOUND,
		"template "+strconv.Quote(name)+" is not registered; templates are named by their path "+
			"relative to a configured template directory, minus the "+strconv.Quote(templateExt)+
			" extension (a file at <dir>/finance/revenue.json is named \"finance/revenue\")",
		map[string]any{errors.DetailTemplate: name})
}

// loadError re-raises a Parse or Validate fault with the source file
// attached, preserving the original code and details.
//
// This is the store's core obligation to the caller. Parse reports a
// malformed document with no template detail — the bytes did not parse, so
// the document's own name is unknowable — and the store is the layer that
// knows both the file it read and the name that file derives. Without this,
// a broken document in a directory of fifty produces an error nobody can
// act on.
func loadError(path, name string, err error) error {
	code := errors.PULSE_TEMPLATE_INVALID
	message := err.Error()
	details := map[string]any{}

	var ce *errors.CodedError
	if stderrors.As(err, &ce) {
		code = ce.Code
		message = ce.Message
		maps.Copy(details, ce.Details)
	}
	details[errors.DetailTemplate] = name
	details[detailPath] = path

	out := errors.NewCodedErrorWithDetails(code, "template file "+path+": "+message, details)
	out.Cause = err
	return out
}

// invalidFile builds a PULSE_TEMPLATE_INVALID for a fault the store itself
// detects — one the document's own bytes cannot express, so Parse never
// sees it.
func invalidFile(path, name, message string) error {
	return errors.NewCodedErrorWithDetails(errors.PULSE_TEMPLATE_INVALID,
		"template file "+path+": "+message,
		map[string]any{
			errors.DetailTemplate: name,
			detailPath:            path,
		})
}

// ioError builds a DATA_FILE for a filesystem fault. Filesystem faults stay
// outside the PULSE_TEMPLATE_* family on purpose: an unreadable file is not
// an invalid template, and telling an operator their document failed
// validation when the real fault is a permission bit sends them to the
// wrong place.
func ioError(path, action string, err error) error {
	out := errors.NewCodedErrorWithDetails(errors.DATA_FILE,
		action+" "+strconv.Quote(path)+": "+err.Error(),
		map[string]any{detailPath: path})
	out.Cause = err
	return out
}
