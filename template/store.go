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
	// chosen at lookup, not at insert.
	byName map[string][]*fileEntry
}

// fileEntry is one discovered template file: the parsed document plus the
// provenance needed to arbitrate precedence and to tell a caller where the
// document came from.
type fileEntry struct {
	// name is the derived lookup name.
	name string

	// path is the source file, as reachable from the process's working
	// directory (root-relative roots yield root-relative paths).
	path string

	// rootIndex is the position of the owning root in the configured
	// list. It is the primary precedence key: lower wins.
	rootIndex int

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
	s := &Store{dirs: slices.Clone(dirs)}
	byName, err := scanDirs(s.dirs)
	if err != nil {
		return nil, err
	}
	s.byName = byName
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
// The returned template is the store's own copy and must be treated as
// read-only; rendering never mutates it.
func (s *Store) Get(name string) (*Template, error) {
	if s == nil {
		return nil, notFound(name)
	}
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
// Returns nil for a nil store.
func (s *Store) List() []Summary {
	if s == nil {
		return nil
	}
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

// scanDirs walks every root in order and builds the name index. Roots are
// visited in precedence order so the index is built in that order too, but
// nothing downstream depends on it — rootIndex is what precedence reads.
func scanDirs(dirs []string) (map[string][]*fileEntry, error) {
	byName := make(map[string][]*fileEntry)
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
			return nil, ioError(dir, "reading template directory", err)
		}
		if !info.IsDir() {
			return nil, errors.NewCodedErrorWithDetails(errors.DATA_FILE,
				"template directory "+strconv.Quote(dir)+" is not a directory; "+
					"each configured template root must be a directory of *.json template files",
				map[string]any{detailPath: dir})
		}
		if err := scanDir(dir, i, byName, seenPaths); err != nil {
			return nil, err
		}
	}
	return byName, nil
}

// scanDir walks one root, appending every valid template it finds. The walk
// stops at the first fault: a template directory holding a broken document
// is a directory whose owner needs to know now.
func scanDir(dir string, rootIndex int, byName map[string][]*fileEntry, seenPaths map[string]struct{}) error {
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
		data, err := os.ReadFile(path)
		if err != nil {
			return ioError(path, "reading template file", err)
		}
		tmpl, err := Parse(data)
		if err != nil {
			return loadError(path, name, err)
		}
		if tmpl.Name != "" && tmpl.Name != name {
			return invalidFile(path, name,
				"template file declares name "+strconv.Quote(tmpl.Name)+
					" but its path names it "+strconv.Quote(name)+
					"; the path is authoritative, so drop the `name` key or move the file")
		}
		tmpl.Name = name

		seenPaths[path] = struct{}{}
		byName[name] = append(byName[name], &fileEntry{
			name:      name,
			path:      path,
			rootIndex: rootIndex,
			tmpl:      tmpl,
		})
		return nil
	})
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
