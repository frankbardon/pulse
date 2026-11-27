package pulse

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/spf13/afero"
)

// ListCohortsOptions scopes ListCohorts. The zero value lists the whole
// data root.
type ListCohortsOptions struct {
	// Dir limits the walk to one directory under the data root, given
	// relative to it ("imports", "waves/2026"). Empty walks the whole
	// root. An absolute path or one that climbs out of the root ("..")
	// is SERVICE_VALIDATION; a directory that does not exist is
	// SERVICE_RESOURCE.
	Dir string
}

// CohortListEntry is one cohort ListCohorts found: what a caller needs
// to pick a cohort and address it, read from its header and schema
// only.
type CohortListEntry struct {
	// Path is the cohort's path relative to the data root, '/'-separated:
	// the value a request's cohort filename (or Inspect's path) takes.
	Path string `json:"path"`
	// RecordCount is the record count Inspect reports (derived from the
	// file length, or the per-shard sum of a shard archive). Zero when
	// Error is set.
	RecordCount int64 `json:"record_count"`
	// Fields lists every field in schema order: name, type name and
	// nullable flag. Empty, never nil, when Error is set.
	Fields []CohortListField `json:"fields"`
	// ShardArchive is true when the file is a shard archive; ShardCount
	// is then its shard count (omitted otherwise).
	ShardArchive bool `json:"shard_archive"`
	ShardCount   int  `json:"shard_count,omitempty"`
	// Managed is true when the cohort is a managed import; Handle is
	// then its handle name (omitted otherwise).
	Managed bool   `json:"managed"`
	Handle  string `json:"handle,omitempty"`
	// Warnings carries the coded warnings Inspect raises for the file
	// (a truncated payload tail's ENCODING_INVALID). Omitted when none.
	Warnings []*descriptor.EnvelopeEntry `json:"warnings,omitempty"`
	// Error is set when the file carries the .pulse suffix but its
	// header or schema cannot be read (ENCODING_INVALID for a bad
	// header, SERVICE_RESOURCE for an unreadable file): the entry is
	// still listed so a broken cohort is visible. Omitted otherwise.
	Error *descriptor.EnvelopeEntry `json:"error,omitempty"`
}

// CohortListField is one field of a CohortListEntry.
type CohortListField struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
}

// ListCohorts lists the .pulse cohorts under the data root (or under
// opts.Dir): every file whose name ends in ".pulse", in every
// subdirectory, sorted by path. It reads each file's header and schema
// (a shard archive's central directory, schema entry and shard
// headers) and never a record — the same bytes Inspect reads.
//
// What is skipped: every file without the .pulse suffix (sidecars —
// .idx, .blooms, .indexes.json, .spss.json, .meta.json, .append —
// profiles and anything else), the entries inside a shard archive
// (its _schema.pulse and shards are part of the one listed archive),
// and every file or directory whose name starts with "." (hidden, or a
// temp file an atomic write has not yet renamed). Symbolic links are
// not followed.
//
// Managed imports are listed with the other cohorts, flagged Managed
// with their Handle. Listing is not a use of a managed import: it
// does not slide the handle's expiry (Imports reports the expiry).
//
// A .pulse file whose header cannot be read is still listed, with its
// coded Error and no fields; only an invalid opts.Dir, a missing
// directory or a cancelled ctx fails the call. Never nil on success.
func (p *Pulse) ListCohorts(ctx context.Context, opts ListCohortsOptions) ([]CohortListEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := listCohortsRoot(opts.Dir)
	if err != nil {
		return nil, err
	}
	if root != "" {
		info, err := p.fsys.Stat(root)
		if err != nil || !info.IsDir() {
			return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_RESOURCE,
				"list cohorts: no such directory under the data root: "+opts.Dir,
				map[string]any{"dir": opts.Dir})
		}
	}

	managed := map[string]string{}
	if p.imports != nil {
		if entries, err := p.imports.List(ctx); err == nil {
			for _, e := range entries {
				managed[cleanListPath(e.Path)] = e.Handle
			}
		}
	}

	var paths []string
	walkErr := afero.Walk(p.fsys, root, func(walked string, info os.FileInfo, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil || info == nil {
			return nil // an unreadable entry is not a cohort
		}
		if walked != root && strings.HasPrefix(info.Name(), ".") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Mode().IsRegular() && strings.HasSuffix(info.Name(), ".pulse") {
			paths = append(paths, cleanListPath(walked))
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Strings(paths)

	out := make([]CohortListEntry, 0, len(paths))
	for _, rel := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entry := p.listCohort(rel)
		if h, ok := managed[rel]; ok {
			entry.Managed, entry.Handle = true, h
		}
		out = append(out, entry)
	}
	return out, nil
}

// listCohortsRoot validates ListCohortsOptions.Dir and returns the walk
// root ("" for the whole data root).
func listCohortsRoot(dir string) (string, error) {
	if dir == "" {
		return "", nil
	}
	slashed := filepath.ToSlash(dir)
	clean := path.Clean(slashed)
	if path.IsAbs(slashed) || filepath.IsAbs(dir) || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			"list cohorts: dir must be a path relative to the data root, inside it: "+dir,
			map[string]any{"dir": dir})
	}
	if clean == "." {
		return "", nil
	}
	return clean, nil
}

// cleanListPath spells a walked or pool path relative to the data root,
// '/'-separated, with no leading slash.
func cleanListPath(p string) string {
	return strings.TrimPrefix(path.Clean("/"+filepath.ToSlash(p)), "/")
}

// listCohort reads one cohort's header and schema into its entry.
func (p *Pulse) listCohort(rel string) CohortListEntry {
	entry := CohortListEntry{Path: rel, Fields: []CohortListField{}}
	src, err := p.openCohortSource(rel)
	if err != nil {
		ce := cohortOpenError("list cohorts: reading "+rel, rel, err)
		entry.Error = &descriptor.EnvelopeEntry{Code: string(ce.Code), Message: ce.Message, Details: ce.Details}
		return entry
	}
	defer src.close()
	env := descx.InspectWith(src.rs, &descriptor.InspectOptions{DictionaryLimit: 1}, descx.InspectMetadata{
		Instance: p.svc.InstanceSnapshot(),
	})
	if len(env.Errors) > 0 {
		entry.Error = env.Errors[0]
		return entry
	}
	if len(env.Warnings) > 0 {
		entry.Warnings = env.Warnings
	}
	res, ok := env.Data.(*descriptor.InspectResult)
	if !ok {
		entry.Error = &descriptor.EnvelopeEntry{Code: string(errors.PROCESSING_INTERNAL), Message: "list cohorts: inspect returned an unexpected type"}
		return entry
	}
	entry.RecordCount = res.RecordCount
	for _, f := range res.Fields {
		entry.Fields = append(entry.Fields, CohortListField{Name: f.Name, Type: f.Type, Nullable: f.Nullable})
	}
	if len(res.Shards) > 0 {
		entry.ShardArchive, entry.ShardCount = true, len(res.Shards)
	}
	return entry
}
