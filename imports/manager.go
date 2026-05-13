package imports

import (
	"bytes"
	"context"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	perr "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	pformat "github.com/frankbardon/pulse/io/format"
	"github.com/spf13/afero"
)

// Env vars consulted by Manager construction. Both are optional;
// New(afs) uses DefaultImportsDir / DefaultTTL when they are unset.
const (
	envImportsDir = "PULSE_IMPORTS_DIR"
	envImportTTL  = "PULSE_IMPORT_TTL"
)

// Manager owns the managed-import pool: lazy mkdir of the imports
// directory, sidecar I/O, sliding-window touches, and sweeps. One
// Manager per Pulse instance; the root facade constructs and holds it.
//
// Two filesystems:
//   - afs: the rooted Pulse fs (typically BasePathFs(OsFs, PULSE_DATA_DIR)).
//     Used for the managed pool (target .pulse files + sidecars) and for
//     reading relative-path sources.
//   - srcFs: the source-reading fs. Used when SourcePath is absolute so
//     callers can import files from anywhere within the jail without
//     first copying them under PULSE_DATA_DIR. Defaults to
//     afero.NewOsFs() in production; MemMapFs in hermetic tests.
//
// jailRoot is the cleaned absolute root under which absolute source
// paths must resolve. Empty string means "no jail" (callers who
// explicitly pass their own SourceFS opt in to managing access
// themselves). Default in production is os.Getwd() at New time so an
// MCP server invocation can only reach files under the directory it
// was launched from.
type Manager struct {
	afs        afero.Fs
	srcFs      afero.Fs
	jailRoot   string
	importsDir string
	defaultTTL time.Duration
	now        func() time.Time
}

// Options configure Manager construction. Zero values are valid: the
// Manager uses the package defaults and reads env vars at New time.
type Options struct {
	// ImportsDir overrides the default relative directory. Honoured
	// before the PULSE_IMPORTS_DIR env var.
	ImportsDir string

	// DefaultTTL overrides the default TTL. Honoured before the
	// PULSE_IMPORT_TTL env var. Zero falls back to env, then to
	// the package DefaultTTL.
	DefaultTTL time.Duration

	// SourceFS is the filesystem used when SourcePath is absolute. When
	// nil, the Manager defaults to afero.NewOsFs() jailed to
	// SourceJailRoot. Set explicitly when callers want to manage
	// access themselves (e.g., a pre-chrooted MemMapFs); doing so
	// disables the jail check — the explicit fs IS the boundary.
	SourceFS afero.Fs

	// SourceJailRoot constrains the default SourceFS to a directory.
	// Empty string defaults to os.Getwd() at construction so an MCP
	// server invocation reaches only files under the directory it was
	// launched from. Ignored when SourceFS is set explicitly.
	SourceJailRoot string

	// Now is the clock; injected for testing. Defaults to time.Now.
	Now func() time.Time
}

// New constructs a Manager rooted at the given afero.Fs. The Manager
// reads PULSE_IMPORTS_DIR and PULSE_IMPORT_TTL once at construction;
// later changes to those env vars do not take effect until a new
// Manager is created.
func New(afs afero.Fs, opts Options) (*Manager, error) {
	dir := opts.ImportsDir
	if dir == "" {
		dir = os.Getenv(envImportsDir)
	}
	if dir == "" {
		dir = DefaultImportsDir
	}

	ttl := opts.DefaultTTL
	if ttl == 0 {
		if raw := os.Getenv(envImportTTL); raw != "" {
			parsed, err := ParseTTL(raw)
			if err != nil {
				return nil, fmt.Errorf("imports.New: %w", err)
			}
			ttl = parsed
		}
	}
	if ttl == 0 {
		ttl = DefaultTTL
	}

	now := opts.Now
	if now == nil {
		now = time.Now
	}

	srcFs := opts.SourceFS
	var jail string
	switch {
	case srcFs != nil:
		// Explicit SourceFS: caller manages access boundaries; jail
		// disabled.
	case opts.SourceJailRoot != "":
		// Explicit jail root: always use OsFs with the jail, even when
		// afs is in-memory. This is the production path and the
		// "real-fs jail tests" path.
		srcFs = afero.NewOsFs()
		cleaned, err := filepath.Abs(filepath.Clean(opts.SourceJailRoot))
		if err != nil {
			return nil, fmt.Errorf("imports.New: normalising jail root %q: %w", opts.SourceJailRoot, err)
		}
		jail = cleaned
	default:
		if _, isMem := afs.(*afero.MemMapFs); isMem {
			// In-memory destination + no jail configured: reuse the
			// MemMapFs as srcFs so hermetic tests using absolute
			// paths (e.g. "/var/data/x.csv") still work without
			// touching the host. No jail in this branch.
			srcFs = afs
		} else {
			// Production default: OsFs jailed to the process working
			// directory. An MCP server or CLI invocation can only
			// reach files under the directory it was launched from.
			srcFs = afero.NewOsFs()
			cwd, err := os.Getwd()
			if err != nil {
				return nil, fmt.Errorf("imports.New: resolving default jail root: %w", err)
			}
			cleaned, err := filepath.Abs(filepath.Clean(cwd))
			if err != nil {
				return nil, fmt.Errorf("imports.New: normalising default jail root: %w", err)
			}
			jail = cleaned
		}
	}

	return &Manager{
		afs:        afs,
		srcFs:      srcFs,
		jailRoot:   jail,
		importsDir: dir,
		defaultTTL: ttl,
		now:        now,
	}, nil
}

// JailRoot returns the cleaned absolute root the Manager confines
// absolute source paths to, or the empty string when no jail is
// enforced (i.e., the caller passed an explicit Options.SourceFS).
func (m *Manager) JailRoot() string { return m.jailRoot }

// checkJail verifies p resolves under the Manager's jail root. Returns
// PULSE_IMPORT_SOURCE_FORBIDDEN when the path escapes. No-op when the
// Manager has no jail (caller-supplied SourceFS).
func (m *Manager) checkJail(p string) error {
	if m.jailRoot == "" {
		return nil
	}
	cleaned, err := filepath.Abs(filepath.Clean(p))
	if err != nil {
		return perr.NewCodedErrorWithDetails(perr.PULSE_IMPORT_SOURCE_FORBIDDEN,
			fmt.Sprintf("cannot normalise source path %q: %v", p, err),
			map[string]any{"source_path": p, "jail_root": m.jailRoot})
	}
	rel, err := filepath.Rel(m.jailRoot, cleaned)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return perr.NewCodedErrorWithDetails(perr.PULSE_IMPORT_SOURCE_FORBIDDEN,
			fmt.Sprintf("source path %q resolves outside the import jail %q", cleaned, m.jailRoot),
			map[string]any{"source_path": cleaned, "jail_root": m.jailRoot})
	}
	return nil
}

// ImportsDir returns the resolved imports directory (relative to the
// Pulse fs root). Stable across the Manager's lifetime.
func (m *Manager) ImportsDir() string { return m.importsDir }

// DefaultTTL returns the resolved default TTL. Stable across the
// Manager's lifetime.
func (m *Manager) DefaultTTL() time.Duration { return m.defaultTTL }

// Open imports the source described by spec and returns a Result.
// Pulse-native sources short-circuit: no copy, no sidecar, Managed=false.
// All other formats produce a managed handle in the imports pool with
// a sidecar. Each successful Open opportunistically sweeps expired
// handles before returning, so the pool self-cleans without a daemon.
func (m *Manager) Open(ctx context.Context, spec Spec) (*Result, error) {
	// Resolve format. Explicit Format wins; otherwise we sniff from the
	// path extension. Inline imports require an explicit Format.
	format := spec.Format
	if format == "" {
		if spec.InlineBytes != nil {
			return nil, perr.NewCodedError(perr.PULSE_IMPORT_FORMAT_UNKNOWN,
				"inline imports require an explicit format")
		}
		format = pformat.FromExt(spec.SourcePath)
	}
	if format == "" {
		return nil, perr.NewCodedErrorWithDetails(perr.PULSE_IMPORT_FORMAT_UNKNOWN,
			fmt.Sprintf("cannot detect import format from %q; pass an explicit format hint", spec.SourcePath),
			map[string]any{"source_path": spec.SourcePath})
	}

	// Pulse-native sources have two paths:
	//   - Relative path (lives inside PULSE_DATA_DIR): pass through with
	//     no copy, no sidecar. Downstream tools address it by its
	//     original relative path.
	//   - Absolute path (lives outside the rooted fs): copy into the
	//     managed pool so downstream tools can address it through the
	//     rooted fs. Sidecar written so the copy participates in the
	//     normal TTL lifecycle.
	if format == pformat.Pulse {
		if spec.InlineBytes != nil {
			return nil, perr.NewCodedError(perr.PULSE_IMPORT_FORMAT_UNKNOWN,
				"inline pulse-format imports are not supported; write the bytes to disk and import by path")
		}
		if filepath.IsAbs(spec.SourcePath) {
			return m.openPulseAbsoluteCopy(ctx, spec)
		}
		exists, err := afero.Exists(m.afs, spec.SourcePath)
		if err != nil {
			return nil, perr.NewCodedErrorWithDetails(perr.PULSE_IMPORT_SOURCE_MISSING,
				err.Error(), map[string]any{"source_path": spec.SourcePath})
		}
		if !exists {
			return nil, perr.NewCodedErrorWithDetails(perr.PULSE_IMPORT_SOURCE_MISSING,
				fmt.Sprintf("source file %q not found", spec.SourcePath),
				map[string]any{"source_path": spec.SourcePath})
		}
		// Best-effort sweep on every Open keeps the pool tidy even
		// when callers never hit a convertible-format import path.
		_, _ = m.Sweep(ctx)
		return &Result{
			Handle:  deriveHandle(spec.Handle, spec.SourcePath),
			Path:    spec.SourcePath,
			Format:  format,
			Managed: false,
		}, nil
	}

	// Convertible format. Materialise reader, run ImportJob, write
	// sidecar, return managed Result.
	handle := deriveHandle(spec.Handle, spec.SourcePath)
	if err := validateHandle(handle); err != nil {
		return nil, err
	}
	target := m.handlePath(handle)
	sidecarPath := target + SidecarSuffix

	exists, err := afero.Exists(m.afs, target)
	if err != nil {
		return nil, fmt.Errorf("imports.Open: checking target: %w", err)
	}
	if exists && !spec.Overwrite {
		return nil, perr.NewCodedErrorWithDetails(perr.PULSE_IMPORT_HANDLE_EXISTS,
			fmt.Sprintf("managed-import handle %q already exists", handle),
			map[string]any{"handle": handle, "path": target})
	}

	if err := m.ensureImportsDir(); err != nil {
		return nil, err
	}

	reader, err := m.openReader(spec, format)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	job := pio.NewImportJob(reader, target)
	job.FS = m.afs
	report, err := job.Run(ctx)
	if err != nil {
		return nil, err
	}

	ttl := spec.TTL
	if ttl == 0 {
		ttl = m.defaultTTL
	}
	now := m.now()
	sidecar := Sidecar{
		Handle:       handle,
		Format:       pformat.Pulse,
		SourcePath:   spec.SourcePath,
		SourceFormat: format,
		ImportedAt:   now,
		TTLSeconds:   ttlSeconds(ttl),
		RowsImported: report.RowsImported,
	}
	if ttl > 0 {
		sidecar.ExpiresAt = now.Add(ttl)
	}
	if err := m.writeSidecar(sidecarPath, sidecar); err != nil {
		return nil, err
	}

	_, _ = m.Sweep(ctx)

	res := &Result{
		Handle:       handle,
		Path:         target,
		Format:       pformat.Pulse,
		Managed:      true,
		RowsImported: report.RowsImported,
		ImportedAt:   now,
		TTLSeconds:   sidecar.TTLSeconds,
		Schema:       report.Schema,
	}
	if !sidecar.ExpiresAt.IsZero() {
		exp := sidecar.ExpiresAt
		res.ExpiresAt = &exp
	}
	return res, nil
}

// Drop removes a managed handle (and its sidecar) from the pool.
// Returns PULSE_IMPORT_SOURCE_MISSING when the handle is unknown.
// Drop is a no-op for non-managed paths.
func (m *Manager) Drop(_ context.Context, handle string) error {
	if err := validateHandle(handle); err != nil {
		return err
	}
	target := m.handlePath(handle)
	sidecarPath := target + SidecarSuffix

	tExists, _ := afero.Exists(m.afs, target)
	sExists, _ := afero.Exists(m.afs, sidecarPath)
	if !tExists && !sExists {
		return perr.NewCodedErrorWithDetails(perr.PULSE_IMPORT_SOURCE_MISSING,
			fmt.Sprintf("managed-import handle %q not found", handle),
			map[string]any{"handle": handle})
	}
	if tExists {
		if err := m.afs.Remove(target); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("imports.Drop: removing %s: %w", target, err)
		}
	}
	if sExists {
		if err := m.afs.Remove(sidecarPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("imports.Drop: removing %s: %w", sidecarPath, err)
		}
	}
	return nil
}

// Touch bumps the sliding-window expiry for the managed handle whose
// .pulse file sits at the given path. No-op when the path is outside
// the imports directory, when no sidecar exists, or when the import is
// pinned. Safe to call from any operation that reads a managed handle
// (Inspect / Predict / Process / Sample / Facet / Ask).
func (m *Manager) Touch(_ context.Context, p string) error {
	if !m.isInsideImportsDir(p) {
		return nil
	}
	sidecarPath := p + SidecarSuffix
	exists, err := afero.Exists(m.afs, sidecarPath)
	if err != nil || !exists {
		return nil
	}
	sidecar, err := m.readSidecar(sidecarPath)
	if err != nil {
		return nil
	}
	if sidecar.Pinned() {
		return nil
	}
	sidecar.ExpiresAt = m.now().Add(time.Duration(sidecar.TTLSeconds) * time.Second)
	return m.writeSidecar(sidecarPath, sidecar)
}

// Sweep deletes every managed handle whose sidecar has expired against
// the Manager's clock. Returns the list of handles that were swept (in
// alphabetical order) and the first error encountered, if any. A
// fs.ErrNotExist on the imports dir is not an error; an empty pool
// returns ([], nil).
func (m *Manager) Sweep(_ context.Context) ([]string, error) {
	entries, err := m.listSidecars()
	if err != nil {
		return nil, err
	}
	now := m.now()
	var swept []string
	for _, sc := range entries {
		if sc.Pinned() {
			continue
		}
		if sc.ExpiresAt.IsZero() || sc.ExpiresAt.After(now) {
			continue
		}
		_ = m.afs.Remove(m.handlePath(sc.Handle))
		_ = m.afs.Remove(m.handlePath(sc.Handle) + SidecarSuffix)
		swept = append(swept, sc.Handle)
	}
	sort.Strings(swept)
	return swept, nil
}

// List returns the current state of the managed-imports pool. Sweep is
// not invoked; expired entries are flagged via Entry.Expired so callers
// can render them. Results are sorted by handle name.
func (m *Manager) List(_ context.Context) ([]Entry, error) {
	entries, err := m.listSidecars()
	if err != nil {
		return nil, err
	}
	now := m.now()
	out := make([]Entry, 0, len(entries))
	for _, sc := range entries {
		e := Entry{
			Handle:  sc.Handle,
			Path:    m.handlePath(sc.Handle),
			Sidecar: sc,
			Pinned:  sc.Pinned(),
			Now:     now,
		}
		if !sc.Pinned() && !sc.ExpiresAt.IsZero() {
			if sc.ExpiresAt.Before(now) {
				e.Expired = true
				e.ExpiresIn = "expired"
			} else {
				e.ExpiresIn = FormatTTL(sc.ExpiresAt.Sub(now).Round(time.Second))
			}
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Handle < out[j].Handle })
	return out, nil
}

// Resolve returns the managed .pulse path for the given handle, or
// PULSE_IMPORT_SOURCE_MISSING when no such handle exists. Used by the
// CLI when callers want to address a handle by name rather than by
// path.
func (m *Manager) Resolve(_ context.Context, handle string) (string, error) {
	if err := validateHandle(handle); err != nil {
		return "", err
	}
	target := m.handlePath(handle)
	exists, _ := afero.Exists(m.afs, target)
	if !exists {
		return "", perr.NewCodedErrorWithDetails(perr.PULSE_IMPORT_SOURCE_MISSING,
			fmt.Sprintf("managed-import handle %q not found", handle),
			map[string]any{"handle": handle})
	}
	return target, nil
}

// IsManagedPath reports whether the given path lives inside the
// imports directory. Used by callers that want to gate behaviour on
// "is this a managed handle?" without round-tripping through Resolve.
func (m *Manager) IsManagedPath(p string) bool {
	return m.isInsideImportsDir(p)
}

// openReader builds the tabular Reader for an Open call. Absolute
// SourcePaths go through srcFs (unrooted OsFs by default) so callers
// can import files from anywhere on the host without first copying
// them under PULSE_DATA_DIR; relative paths continue to resolve
// against the rooted Pulse fs. Inline byte imports are not yet wired.
func (m *Manager) openReader(spec Spec, format string) (pio.Reader, error) {
	if spec.InlineBytes != nil {
		return nil, fmt.Errorf("imports: InlineBytes path not yet wired (open issue: add format.NewReaderFromBytes)")
	}
	readFs := m.afs
	if filepath.IsAbs(spec.SourcePath) {
		if err := m.checkJail(spec.SourcePath); err != nil {
			return nil, err
		}
		readFs = m.srcFs
	}
	exists, err := afero.Exists(readFs, spec.SourcePath)
	if err != nil {
		return nil, perr.NewCodedErrorWithDetails(perr.PULSE_IMPORT_SOURCE_MISSING,
			err.Error(), map[string]any{"source_path": spec.SourcePath})
	}
	if !exists {
		return nil, perr.NewCodedErrorWithDetails(perr.PULSE_IMPORT_SOURCE_MISSING,
			fmt.Sprintf("source file %q not found", spec.SourcePath),
			map[string]any{"source_path": spec.SourcePath})
	}
	return pformat.NewReader(format, readFs, spec.SourcePath, pformat.ReaderOptions{Sheet: spec.Sheet})
}

// openPulseAbsoluteCopy handles the pulse-passthrough case when the
// source lives outside the rooted Pulse fs. We must copy: the rooted
// destination fs can't address absolute external paths via the normal
// MCP / CLI tools, so leaving the file in place would produce a handle
// nothing downstream could open. The copy lands in the managed pool
// with a full sidecar so it participates in the normal TTL lifecycle
// — pinned imports here behave the same as anywhere else.
func (m *Manager) openPulseAbsoluteCopy(ctx context.Context, spec Spec) (*Result, error) {
	if err := m.checkJail(spec.SourcePath); err != nil {
		return nil, err
	}
	exists, err := afero.Exists(m.srcFs, spec.SourcePath)
	if err != nil {
		return nil, perr.NewCodedErrorWithDetails(perr.PULSE_IMPORT_SOURCE_MISSING,
			err.Error(), map[string]any{"source_path": spec.SourcePath})
	}
	if !exists {
		return nil, perr.NewCodedErrorWithDetails(perr.PULSE_IMPORT_SOURCE_MISSING,
			fmt.Sprintf("source file %q not found", spec.SourcePath),
			map[string]any{"source_path": spec.SourcePath})
	}
	handle := deriveHandle(spec.Handle, spec.SourcePath)
	if err := validateHandle(handle); err != nil {
		return nil, err
	}
	target := m.handlePath(handle)
	sidecarPath := target + SidecarSuffix

	already, err := afero.Exists(m.afs, target)
	if err != nil {
		return nil, fmt.Errorf("imports.Open: checking target: %w", err)
	}
	if already && !spec.Overwrite {
		return nil, perr.NewCodedErrorWithDetails(perr.PULSE_IMPORT_HANDLE_EXISTS,
			fmt.Sprintf("managed-import handle %q already exists", handle),
			map[string]any{"handle": handle, "path": target})
	}
	if err := m.ensureImportsDir(); err != nil {
		return nil, err
	}

	body, err := afero.ReadFile(m.srcFs, spec.SourcePath)
	if err != nil {
		return nil, perr.NewCodedErrorWithDetails(perr.PULSE_IMPORT_SOURCE_MISSING,
			err.Error(), map[string]any{"source_path": spec.SourcePath})
	}
	if err := afero.WriteFile(m.afs, target, body, 0o644); err != nil {
		return nil, fmt.Errorf("imports: writing managed copy %s: %w", target, err)
	}

	ttl := spec.TTL
	if ttl == 0 {
		ttl = m.defaultTTL
	}
	now := m.now()
	sidecar := Sidecar{
		Handle:       handle,
		Format:       pformat.Pulse,
		SourcePath:   spec.SourcePath,
		SourceFormat: pformat.Pulse,
		ImportedAt:   now,
		TTLSeconds:   ttlSeconds(ttl),
	}
	if ttl > 0 {
		sidecar.ExpiresAt = now.Add(ttl)
	}
	if err := m.writeSidecar(sidecarPath, sidecar); err != nil {
		return nil, err
	}

	_, _ = m.Sweep(ctx)

	res := &Result{
		Handle:     handle,
		Path:       target,
		Format:     pformat.Pulse,
		Managed:    true,
		ImportedAt: now,
		TTLSeconds: sidecar.TTLSeconds,
	}
	if !sidecar.ExpiresAt.IsZero() {
		exp := sidecar.ExpiresAt
		res.ExpiresAt = &exp
	}
	return res, nil
}

func (m *Manager) handlePath(handle string) string {
	return path.Join(m.importsDir, handle+".pulse")
}

func (m *Manager) ensureImportsDir() error {
	exists, err := afero.DirExists(m.afs, m.importsDir)
	if err != nil {
		return fmt.Errorf("imports: stat dir: %w", err)
	}
	if exists {
		return nil
	}
	if err := m.afs.MkdirAll(m.importsDir, 0o755); err != nil {
		return fmt.Errorf("imports: creating dir %s: %w", m.importsDir, err)
	}
	return nil
}

// listSidecars walks the imports directory once, decoding every
// sidecar it finds. Missing directory returns ([], nil). Malformed
// sidecars are skipped silently (the caller cannot act on them anyway;
// a future diagnostic surface could surface them via warnings).
func (m *Manager) listSidecars() ([]Sidecar, error) {
	exists, err := afero.DirExists(m.afs, m.importsDir)
	if err != nil || !exists {
		return nil, nil
	}
	infos, err := afero.ReadDir(m.afs, m.importsDir)
	if err != nil {
		return nil, fmt.Errorf("imports: reading dir %s: %w", m.importsDir, err)
	}
	var out []Sidecar
	for _, info := range infos {
		name := info.Name()
		if !strings.HasSuffix(name, SidecarSuffix) {
			continue
		}
		sc, err := m.readSidecar(path.Join(m.importsDir, name))
		if err != nil {
			continue
		}
		out = append(out, sc)
	}
	return out, nil
}

func (m *Manager) readSidecar(p string) (Sidecar, error) {
	body, err := afero.ReadFile(m.afs, p)
	if err != nil {
		return Sidecar{}, err
	}
	var sc Sidecar
	if err := stdjson.Unmarshal(body, &sc); err != nil {
		return Sidecar{}, err
	}
	return sc, nil
}

func (m *Manager) writeSidecar(p string, sc Sidecar) error {
	var buf bytes.Buffer
	enc := stdjson.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(&sc); err != nil {
		return fmt.Errorf("imports: marshalling sidecar: %w", err)
	}
	return afero.WriteFile(m.afs, p, buf.Bytes(), 0o644)
}

func (m *Manager) isInsideImportsDir(p string) bool {
	clean := filepath.ToSlash(path.Clean(p))
	dir := filepath.ToSlash(path.Clean(m.importsDir)) + "/"
	return strings.HasPrefix(clean, dir)
}

// deriveHandle returns the explicit handle when set, otherwise the
// source basename with extension stripped (and a "pulse" suffix in the
// rare edge case where the source already named itself "pulse").
func deriveHandle(explicit, sourcePath string) string {
	if explicit != "" {
		return explicit
	}
	base := filepath.Base(sourcePath)
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext)
}

// validateHandle rejects names that would escape the imports directory
// or collide with the sidecar suffix. Allowed: ASCII letters, digits,
// dash, underscore, dot (single-segment).
func validateHandle(handle string) error {
	if handle == "" {
		return perr.NewCodedError(perr.PULSE_IMPORT_FORMAT_UNKNOWN,
			"managed-import handle is required and cannot be empty")
	}
	if strings.ContainsAny(handle, "/\\") {
		return perr.NewCodedErrorWithDetails(perr.PULSE_IMPORT_FORMAT_UNKNOWN,
			fmt.Sprintf("managed-import handle %q must not contain path separators", handle),
			map[string]any{"handle": handle})
	}
	if strings.HasPrefix(handle, ".") {
		return perr.NewCodedErrorWithDetails(perr.PULSE_IMPORT_FORMAT_UNKNOWN,
			fmt.Sprintf("managed-import handle %q must not start with a dot", handle),
			map[string]any{"handle": handle})
	}
	return nil
}

// ttlSeconds maps a Duration to the sidecar's int64 wire format. Pinned
// (negative) durations write 0 to signal "no expiry"; the receiver
// distinguishes pinned from "default zero" via the absence of
// ExpiresAt.
func ttlSeconds(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return int64(d / time.Second)
}
