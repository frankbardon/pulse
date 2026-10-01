package pulse

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/imports"
	"github.com/frankbardon/pulse/internal/spsssidecar"
	"github.com/spf13/afero"
)

// CohortArtifacts returns every Pulse-owned sidecar that currently
// exists beside the cohort, sorted, excluding the cohort itself:
//
//   - cohort.pulse.<keyhash>.idx — each sidecar point-lookup index
//   - cohort.pulse.indexes.json  — the index discovery manifest
//   - cohort.pulse.spss.json     — the SPSS metadata sidecar
//   - cohort.pulse.meta.json     — the managed-import handle metadata
//
// It is the answer to "what must travel with this cohort": moving or
// copying the cohort together with every returned path keeps Lookup
// hitting and SPSS export faithful at the new location — provided the
// move carries the cohort's modification time across (a rename or
// `cp -p` does; a plain copy does not). Both sidecars fingerprint the
// cohort's size and mtime, and the SPSS sidecar refuses a cohort whose
// mtime moved (PULSE_SPSS_SIDECAR_STALE). Only files that
// exist are returned, each spelled relative to the same root as cohort
// (the cohort's directory joined with the sidecar's name); a cohort with
// none returns an empty, non-nil slice.
//
// Nothing is opened or validated — a stale or corrupt sidecar is still
// listed, because it still has to move with (or be deleted beside) its
// cohort. Discovery is by the owners' own naming rules, never a
// "<cohort>.*" glob, so an unrelated file that merely shares the
// cohort's name as a prefix is not swept up. A missing cohort is a
// SERVICE_RESOURCE error.
func (p *Pulse) CohortArtifacts(ctx context.Context, cohort string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := p.fsys.Stat(cohort); err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("cohort artifacts: cohort not found: %s", cohort))
	}

	dir := filepath.Dir(cohort)
	base := filepath.Base(cohort)
	exact := map[string]bool{
		filepath.Base(encoding.IndexManifestPath(cohort)): true, // .indexes.json
		filepath.Base(spsssidecar.Path(cohort)):           true, // .spss.json
		base + imports.SidecarSuffix:                      true, // .meta.json
	}
	idxName := regexp.MustCompile(`^` + regexp.QuoteMeta(base) + `\.[0-9a-f]{16}\.idx$`)

	entries, err := afero.ReadDir(p.fsys, dir)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("cohort artifacts: listing %s", dir))
	}
	out := []string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if exact[name] || idxName.MatchString(name) {
			out = append(out, filepath.Join(dir, name))
		}
	}
	sort.Strings(out)
	return out, nil
}
