package pulse

import (
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/io/spss"
	"github.com/frankbardon/pulse/internal/service"
)

// sidecarWeightVariable returns the weighting variable recorded in the
// SPSS metadata sidecar beside the cohort at path (cohort.pulse.spss.json),
// or "" when there is nothing to suggest. It feeds inspect's and
// predict's suggested_weight and nothing else — a suggestion is never
// applied.
//
// Every failure is silent, because a suggestion is optional metadata
// and no read of it may fail or warn the operation it decorates: no
// sidecar, an unreadable or malformed one, a STALE one (its cohort
// fingerprint no longer matches — the same spss.LoadSidecar rule
// `pulse export spss` refuses with), an anchor path (a sidecar sits
// beside a whole cohort file, never inside an archive), or an instance
// hiding capability:weighting all yield "". The sidecar is metadata,
// never a record; it is read through the instance's afero.Fs.
func (p *Pulse) sidecarWeightVariable(path string) string {
	if path == "" || !p.svc.InstanceSnapshot().Enabled(descx.FeatureWeighting) {
		return ""
	}
	if _, _, anchored := service.SplitAnchorPath(path); anchored {
		return ""
	}
	res, err := spss.LoadSidecar(p.fsys, path, spss.WriterOptions{})
	if err != nil || res == nil || res.Document == nil || res.Document.Payload.Weight == nil {
		return ""
	}
	return res.Document.Payload.Weight.Variable
}
