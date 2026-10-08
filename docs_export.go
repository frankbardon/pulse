package pulse

import (
	"bufio"
	"bytes"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/docgen"
)

// docsExportMarker is the file ExportReference writes at the export
// root. It marks the directory as Pulse-owned — a later export may
// overwrite it in place — and lists every file the export wrote, so the
// next export deletes the ones it no longer renders.
const docsExportMarker = ".pulse-docs-export"

// docsExportMarkerHeader opens the marker file; every following
// non-blank line is a slash-separated path the export wrote.
const docsExportMarkerHeader = "# Written by pulse docs export. Pulse owns the files listed below:\n# a re-export overwrites them and deletes the ones it no longer renders.\n"

// ExportReferenceOptions tunes one ExportReference call. The zero value
// exports the full reference: catalog, reading pages, glossary and the
// skill pack.
type ExportReferenceOptions struct {
	// OmitSkills leaves out skills.md and the skills/ tree; no exported
	// page then links to a skill. The zero value includes the skills.
	OmitSkills bool
}

// ExportReference writes the instance's analysis reference — the
// operator catalog, the "Reading your results" pages, the glossary and
// (unless opts.OmitSkills) every skill as the instance serves it — as a
// deterministic Markdown tree under dir on fsys, with an mdBook
// SUMMARY.md fragment at its root. A nil fsys writes through the
// instance filesystem (Fs). Two exports of the same instance are
// byte-identical.
//
// The export is instance-scoped exactly like Ontology, Skills and Skill:
// under a feature profile no page, row, link or prose line names a
// hidden operator, MCP tool or a hidden capability's slot token.
//
// Overwrite rule: the export writes the marker file .pulse-docs-export
// at dir. A missing or empty dir, or one carrying the marker, is
// written in place, and every file a previous export recorded in the
// marker that this export no longer renders is deleted (with any
// directory left empty by it); files the marker does not list are never
// touched. A non-empty dir without the marker is refused with
// PULSE_DOCS_EXPORT_DIR_NOT_EMPTY before anything is written. A
// filesystem failure is a DATA_FILE error naming the path.
func (p *Pulse) ExportReference(fsys afero.Fs, dir string, opts ExportReferenceOptions) error {
	if fsys == nil {
		fsys = p.fsys
	}
	files := docgen.Render(p.svc.InstanceSnapshot(), docgen.Options{OmitSkills: opts.OmitSkills})

	previous, err := docsExportPrecheck(fsys, dir)
	if err != nil {
		return err
	}
	written := make(map[string]bool, len(files))
	for _, f := range files {
		written[f.Path] = true
		if err := docsExportWriteFile(fsys, dir, f.Path, f.Body); err != nil {
			return err
		}
	}
	for _, rel := range previous {
		if written[rel] {
			continue
		}
		if err := docsExportRemoveStale(fsys, dir, rel); err != nil {
			return err
		}
	}
	return docsExportWriteFile(fsys, dir, docsExportMarker, docsExportMarkerBody(files))
}

// docsExportPrecheck applies the overwrite rule to dir and returns the
// file list a previous export recorded in its marker (nil for a missing
// or empty dir).
func docsExportPrecheck(fsys afero.Fs, dir string) ([]string, error) {
	info, err := fsys.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, docsExportFileError(err, dir, "stat the export directory")
	}
	if !info.IsDir() {
		return nil, docsExportFileError(nil, dir, "the export path exists and is not a directory")
	}
	entries, err := afero.ReadDir(fsys, dir)
	if err != nil {
		return nil, docsExportFileError(err, dir, "read the export directory")
	}
	if len(entries) == 0 {
		return nil, nil
	}
	marker := filepath.Join(dir, docsExportMarker)
	body, err := afero.ReadFile(fsys, marker)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_DOCS_EXPORT_DIR_NOT_EMPTY,
				"docs export: "+dir+" is not empty and carries no "+docsExportMarker+" marker; pass an empty or previously exported directory",
				map[string]any{"path": dir, "marker": docsExportMarker})
		}
		return nil, docsExportFileError(err, marker, "read the export marker")
	}
	return docsExportMarkerPaths(body), nil
}

// docsExportMarkerBody is the marker file for an export of files.
func docsExportMarkerBody(files []docgen.File) []byte {
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	sort.Strings(paths)
	var b bytes.Buffer
	b.WriteString(docsExportMarkerHeader)
	for _, p := range paths {
		b.WriteString(p)
		b.WriteByte('\n')
	}
	return b.Bytes()
}

// docsExportMarkerPaths parses a marker body into the recorded paths.
// A line that is a comment, blank, absolute, or escapes the export root
// is ignored: the marker can only ever name a file under dir.
func docsExportMarkerPaths(body []byte) []string {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.Contains(line, `\`) {
			continue
		}
		clean := path.Clean(line)
		if clean != line || path.IsAbs(clean) || clean == "." || clean == docsExportMarker ||
			clean == ".." || strings.HasPrefix(clean, "../") {
			continue
		}
		out = append(out, clean)
	}
	return out
}

func docsExportWriteFile(fsys afero.Fs, dir, rel string, body []byte) error {
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := fsys.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return docsExportFileError(err, filepath.Dir(p), "create the directory")
	}
	if err := afero.WriteFile(fsys, p, body, 0o644); err != nil {
		return docsExportFileError(err, p, "write the file")
	}
	return nil
}

// docsExportRemoveStale deletes one previously exported file and then
// every directory between it and dir the deletion left empty.
func docsExportRemoveStale(fsys afero.Fs, dir, rel string) error {
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := fsys.Remove(p); err != nil && !os.IsNotExist(err) {
		return docsExportFileError(err, p, "delete the stale file")
	}
	for d := path.Dir(rel); d != "." && d != "/"; d = path.Dir(d) {
		dp := filepath.Join(dir, filepath.FromSlash(d))
		entries, err := afero.ReadDir(fsys, dp)
		if err != nil || len(entries) > 0 {
			break
		}
		if err := fsys.Remove(dp); err != nil {
			return docsExportFileError(err, dp, "delete the empty directory")
		}
	}
	return nil
}

func docsExportFileError(err error, p, what string) error {
	msg := "docs export: cannot " + what + ": " + p
	if err != nil {
		msg += ": " + err.Error()
	} else {
		msg = "docs export: " + what + ": " + p
	}
	ce := errors.WrapCodedError(err, errors.DATA_FILE, msg)
	ce.Details = map[string]any{"path": p}
	return ce
}
