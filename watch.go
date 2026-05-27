package pulse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/afero"
)

// ChangeKind identifies the kind of change a Watch / WatchDir emits.
type ChangeKind int

const (
	// ChangeCreated reports the appearance of a new file in the
	// watched scope. Hash is populated when the file is readable.
	ChangeCreated ChangeKind = iota
	// ChangeModified reports an in-place content change on an
	// already-tracked path. Hash carries the post-change content
	// hash.
	ChangeModified
	// ChangeRemoved reports that a previously-tracked path is gone.
	// Hash is empty.
	ChangeRemoved
	// ChangeRenamed reports an atomic write that promoted a temp
	// file to the watched path. Coalesces a Removed-or-Created
	// pair into a single Renamed event aimed at the destination.
	ChangeRenamed
)

// String returns a stable lowercase identifier for the kind.
func (k ChangeKind) String() string {
	switch k {
	case ChangeCreated:
		return "created"
	case ChangeModified:
		return "modified"
	case ChangeRemoved:
		return "removed"
	case ChangeRenamed:
		return "renamed"
	}
	return "unknown"
}

// ChangeEvent is one observed mutation against a watched .pulse path
// (or any file, when WatchDir is rooted at a non-.pulse directory).
type ChangeEvent struct {
	Path      string
	Kind      ChangeKind
	Hash      string
	Timestamp time.Time
}

// WatchOptions tunes the watcher cadence. Zero values fall back to the
// defaults documented on each field.
type WatchOptions struct {
	// PollInterval is the stat-poll cadence. Defaults to 250ms.
	// Network filesystems should pass a larger interval (recommend
	// 30s).
	PollInterval time.Duration

	// CoalesceWindow caps how long the watcher waits for a quick
	// follow-up modify before emitting a single event. Defaults to
	// 100ms; pass 0 to disable coalescing entirely.
	CoalesceWindow time.Duration

	// HashPrefixBytes caps how many leading bytes the hash digest
	// reads from each file. Defaults to 64 KiB — enough to cover
	// the .pulse header + schema for any realistic cohort. Pass a
	// negative value to hash the entire file.
	HashPrefixBytes int

	// Recursive controls whether WatchDir descends into
	// subdirectories. Ignored by Watch.
	Recursive bool

	// Suffix narrows the watched fileset by suffix (case-sensitive).
	// Empty matches every file. WatchDir defaults this to ".pulse"
	// to keep traffic focused on cohort files; callers can override
	// with an empty string to watch every file.
	Suffix string
}

// defaultPollInterval is the watch loop cadence when WatchOptions
// leaves PollInterval at zero.
const defaultPollInterval = 250 * time.Millisecond

// defaultCoalesceWindow is the gap inside which two rapid mutations
// collapse to a single Modified / Renamed event.
const defaultCoalesceWindow = 100 * time.Millisecond

// defaultHashPrefixBytes is the leading-byte cap for the change-event
// hash. Sized for the .pulse header + schema; callers that need full-
// file hashing override via WatchOptions.HashPrefixBytes.
const defaultHashPrefixBytes = 64 * 1024

// Watch observes the cohort path on the Pulse instance's filesystem
// and returns a channel of ChangeEvent records. Closing the context
// drains and closes the channel.
func (p *Pulse) Watch(ctx context.Context, target string) <-chan ChangeEvent {
	return p.WatchWithOptions(ctx, target, WatchOptions{})
}

// WatchWithOptions is the configurable variant of Watch. PollInterval
// and CoalesceWindow control responsiveness vs CPU; HashPrefixBytes
// caps the per-event hash size.
func (p *Pulse) WatchWithOptions(ctx context.Context, target string, opts WatchOptions) <-chan ChangeEvent {
	out := make(chan ChangeEvent, 8)
	fs := p.dataFS()
	w := newWatcher(fs, []string{target}, false, opts)
	go w.run(ctx, out)
	return out
}

// WatchDir observes every file under dir (and subdirectories when
// recursive=true) for change events, filtered to WatchOptions.Suffix
// (defaults to ".pulse"). Closing the context drains and closes the
// channel.
func (p *Pulse) WatchDir(ctx context.Context, dir string, recursive bool) <-chan ChangeEvent {
	return p.WatchDirWithOptions(ctx, dir, WatchOptions{Recursive: recursive})
}

// WatchDirWithOptions is the configurable variant of WatchDir.
func (p *Pulse) WatchDirWithOptions(ctx context.Context, dir string, opts WatchOptions) <-chan ChangeEvent {
	if opts.Suffix == "" {
		opts.Suffix = ".pulse"
	}
	out := make(chan ChangeEvent, 8)
	fs := p.dataFS()
	w := newWatcher(fs, []string{dir}, true, opts)
	go w.run(ctx, out)
	return out
}

// dataFS returns the Pulse instance's filesystem, falling back to the
// OS afero when callers built Pulse without a custom FS.
func (p *Pulse) dataFS() afero.Fs {
	if p != nil {
		if fs := p.Fs(); fs != nil {
			return fs
		}
	}
	return afero.NewOsFs()
}

// fileState captures the per-poll snapshot the watcher diffs against
// to derive ChangeEvent records.
type fileState struct {
	size    int64
	mod     time.Time
	hash    string
	exists  bool
	pending bool
	pendAt  time.Time
}

// watcher orchestrates the polling loop. dirMode means the roots are
// directories to walk; otherwise each root is a single file path.
type watcher struct {
	fs      afero.Fs
	roots   []string
	dirMode bool
	opts    WatchOptions
	state   map[string]*fileState
}

func newWatcher(fs afero.Fs, roots []string, dirMode bool, opts WatchOptions) *watcher {
	if opts.PollInterval <= 0 {
		opts.PollInterval = defaultPollInterval
	}
	if opts.CoalesceWindow < 0 {
		opts.CoalesceWindow = 0
	} else if opts.CoalesceWindow == 0 {
		opts.CoalesceWindow = defaultCoalesceWindow
	}
	if opts.HashPrefixBytes == 0 {
		opts.HashPrefixBytes = defaultHashPrefixBytes
	}
	return &watcher{
		fs:      fs,
		roots:   roots,
		dirMode: dirMode,
		opts:    opts,
		state:   make(map[string]*fileState),
	}
}

// run drives the polling loop until ctx cancels. Events are emitted on
// out; out is closed before the goroutine returns. The watcher does NOT
// pre-seed its state map — files that already exist at watch-start time
// surface as ChangeCreated on the first tick. Callers that prefer the
// alternative "ignore pre-existing files" semantics can drain the first
// tick before treating events as actionable.
func (w *watcher) run(ctx context.Context, out chan<- ChangeEvent) {
	defer close(out)
	ticker := time.NewTicker(w.opts.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			events := w.tick()
			for _, e := range events {
				select {
				case <-ctx.Done():
					return
				case out <- e:
				}
			}
		}
	}
}

// tick polls the watched scope once and returns the resulting events.
func (w *watcher) tick() []ChangeEvent {
	current := make(map[string]*fileState, len(w.state))
	for _, p := range w.discover() {
		st, ok := w.statHash(p)
		if !ok {
			continue
		}
		current[p] = st
	}

	now := time.Now()
	var events []ChangeEvent

	// Detect Created / Modified by walking current entries.
	keys := make([]string, 0, len(current))
	for p := range current {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	for _, p := range keys {
		st := current[p]
		prev, had := w.state[p]
		if !had {
			events = append(events, ChangeEvent{
				Path:      p,
				Kind:      ChangeCreated,
				Hash:      st.hash,
				Timestamp: now,
			})
			w.state[p] = st
			continue
		}
		if prev.size != st.size || !prev.mod.Equal(st.mod) || prev.hash != st.hash {
			// Coalesce a rapid modify (rename-from-temp commonly shows
			// up as a Removed-then-Created pair already collapsed by
			// the second-pass logic below; here we only need to hold
			// off if a previous modify is still inside the coalesce
			// window).
			if w.opts.CoalesceWindow > 0 && prev.pending && now.Sub(prev.pendAt) < w.opts.CoalesceWindow {
				st.pending = true
				st.pendAt = prev.pendAt
				w.state[p] = st
				continue
			}
			st.pending = true
			st.pendAt = now
			w.state[p] = st
			events = append(events, ChangeEvent{
				Path:      p,
				Kind:      ChangeModified,
				Hash:      st.hash,
				Timestamp: now,
			})
			continue
		}
		// Stable: clear any pending flag.
		st.pending = false
		st.pendAt = time.Time{}
		w.state[p] = st
	}

	// Detect Removed by walking previous state.
	var gone []string
	for p := range w.state {
		if _, ok := current[p]; !ok {
			gone = append(gone, p)
		}
	}
	sort.Strings(gone)
	for _, p := range gone {
		events = append(events, ChangeEvent{
			Path:      p,
			Kind:      ChangeRemoved,
			Hash:      "",
			Timestamp: now,
		})
		delete(w.state, p)
	}

	// Coalesce a temp-rename: when a path appears in this tick whose
	// basename matches an earlier-removed sibling within the coalesce
	// window, retag the Created as Renamed and suppress the Removed.
	events = coalesceRename(events, w.opts.CoalesceWindow)

	return events
}

// coalesceRename folds Removed(tempPath) + Created(targetPath) into a
// single Renamed(targetPath) when the temp basename matches the
// canonical "<target>.tmp" / "<target>~" / "<target>.swp" / "<target>.partial"
// pattern, or when the two paths share a directory and the timestamps
// fall inside the coalesce window.
func coalesceRename(events []ChangeEvent, window time.Duration) []ChangeEvent {
	if len(events) < 2 || window <= 0 {
		return events
	}
	out := make([]ChangeEvent, 0, len(events))
	consumed := make([]bool, len(events))
	for i, e := range events {
		if consumed[i] {
			continue
		}
		if e.Kind == ChangeCreated {
			// Look for a recent Removed sibling that is plausibly the
			// temp file being promoted into place.
			for j := 0; j < len(events); j++ {
				if i == j || consumed[j] {
					continue
				}
				cand := events[j]
				if cand.Kind != ChangeRemoved {
					continue
				}
				if filepath.Dir(cand.Path) != filepath.Dir(e.Path) {
					continue
				}
				if !looksLikeTempSibling(cand.Path, e.Path) {
					continue
				}
				if absDuration(e.Timestamp.Sub(cand.Timestamp)) > window {
					continue
				}
				consumed[j] = true
				out = append(out, ChangeEvent{
					Path:      e.Path,
					Kind:      ChangeRenamed,
					Hash:      e.Hash,
					Timestamp: e.Timestamp,
				})
				consumed[i] = true
				break
			}
		}
		if !consumed[i] {
			out = append(out, e)
		}
	}
	return out
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// looksLikeTempSibling reports whether tempPath is a canonical
// scratch-file companion of targetPath. Covers the common rename-from-
// temp patterns used by editors and atomic-write libraries.
func looksLikeTempSibling(tempPath, targetPath string) bool {
	tempBase := path.Base(tempPath)
	targetBase := path.Base(targetPath)
	if tempBase == targetBase {
		return false
	}
	// Strip common temp extensions / suffixes.
	for _, suffix := range []string{".tmp", ".swp", ".partial", "~"} {
		if strings.HasSuffix(tempBase, suffix) {
			stripped := strings.TrimSuffix(tempBase, suffix)
			if stripped == targetBase {
				return true
			}
		}
	}
	// Editor temp files sometimes prefix with "." or end with random
	// digits; tolerate a strict prefix match.
	if strings.HasPrefix(tempBase, ".") && strings.HasSuffix(tempBase, targetBase) {
		return true
	}
	if strings.HasPrefix(tempBase, targetBase+".") {
		return true
	}
	return false
}

// discover returns the canonical path list to inspect this tick. In
// file mode the roots ARE the paths. In dir mode we walk each root
// once per tick.
func (w *watcher) discover() []string {
	if !w.dirMode {
		out := make([]string, 0, len(w.roots))
		out = append(out, w.roots...)
		return out
	}
	var out []string
	for _, root := range w.roots {
		w.walk(root, &out)
	}
	return out
}

// walk descends dir, appending matching file paths to out. Honors
// Suffix and Recursive options.
func (w *watcher) walk(dir string, out *[]string) {
	entries, err := afero.ReadDir(w.fs, dir)
	if err != nil {
		return
	}
	for _, ent := range entries {
		full := filepath.Join(dir, ent.Name())
		if ent.IsDir() {
			if w.opts.Recursive {
				w.walk(full, out)
			}
			continue
		}
		if w.opts.Suffix != "" && !strings.HasSuffix(ent.Name(), w.opts.Suffix) {
			continue
		}
		*out = append(*out, full)
	}
}

// statHash captures the size, mtime, and content-prefix hash for path.
// Returns (state, false) when the path is absent or unreadable so the
// caller treats the file as gone for this tick.
func (w *watcher) statHash(path string) (*fileState, bool) {
	info, err := w.fs.Stat(path)
	if err != nil {
		if os.IsNotExist(err) || errors.Is(err, os.ErrNotExist) {
			return nil, false
		}
		return nil, false
	}
	if info.IsDir() {
		return nil, false
	}
	hash := w.hashPrefix(path)
	return &fileState{
		size:   info.Size(),
		mod:    info.ModTime(),
		hash:   hash,
		exists: true,
	}, true
}

// hashPrefix returns the SHA-256 hex of the first HashPrefixBytes of
// path (or the whole file when HashPrefixBytes < 0). On any error we
// return an empty hash so the caller still emits an event but with no
// content fingerprint.
func (w *watcher) hashPrefix(path string) string {
	f, err := w.fs.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if w.opts.HashPrefixBytes < 0 {
		if _, err := io.Copy(h, f); err != nil {
			return ""
		}
	} else {
		if _, err := io.CopyN(h, f, int64(w.opts.HashPrefixBytes)); err != nil && !errors.Is(err, io.EOF) {
			return ""
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}
