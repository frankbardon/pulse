package pulse

import (
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/io/spss"
	"github.com/frankbardon/pulse/internal/service"
)

// sidecarFacts is what predict and inspect read from the SPSS metadata
// sidecar beside a cohort: the recorded weighting variable and every
// variable's measure level. Both are metadata, never records.
type sidecarFacts struct {
	// weightVariable feeds suggested_weight (and the weight advisory);
	// "" when there is nothing to suggest or capability:weighting is
	// hidden.
	weightVariable string
	// measures maps a field name to its SPSS measure level, for the
	// measure-level advisories; nil when there is no usable sidecar.
	measures map[string]string
}

// sidecarWeightVariable returns the weighting variable recorded in the
// SPSS metadata sidecar beside the cohort at path (cohort.pulse.spss.json),
// or "" when there is nothing to suggest. It feeds inspect's and
// predict's suggested_weight and nothing else — a suggestion is never
// applied.
func (p *Pulse) sidecarWeightVariable(path string) string {
	return p.sidecarFacts(path).weightVariable
}

// sidecarFacts reads the SPSS metadata sidecar beside the cohort at path.
//
// Every failure is silent, because sidecar facts are optional metadata
// and no read of them may fail or warn the operation they decorate: no
// sidecar, an unreadable or malformed one, a STALE one (its cohort
// fingerprint no longer matches — the same spss.LoadSidecar rule
// `pulse export spss` refuses with) or an anchor path (a sidecar sits
// beside a whole cohort file, never inside an archive) all yield the
// zero value. An instance hiding capability:weighting gets no weight
// variable but keeps the measure levels. The sidecar is read through
// the instance's afero.Fs.
func (p *Pulse) sidecarFacts(path string) sidecarFacts {
	if path == "" {
		return sidecarFacts{}
	}
	if _, _, anchored := service.SplitAnchorPath(path); anchored {
		return sidecarFacts{}
	}
	res, err := spss.LoadSidecar(p.fsys, path, spss.WriterOptions{})
	if err != nil || res == nil || res.Document == nil {
		return sidecarFacts{}
	}
	var out sidecarFacts
	if w := res.Document.Payload.Weight; w != nil && p.svc.InstanceSnapshot().Enabled(descx.FeatureWeighting) {
		out.weightVariable = w.Variable
	}
	for _, v := range res.Document.Payload.Variables {
		if v.Measure == "" {
			continue
		}
		if out.measures == nil {
			out.measures = map[string]string{}
		}
		out.measures[v.Name] = v.Measure
	}
	return out
}
