package descriptor

import (
	"strconv"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/examples"
)

// ExamplesSearch runs one example search for the instance: the visible
// library, the synonym tier fed by the instance's alias index (KnownAs)
// and the Sounds of the intents its ontology keeps, and the optional
// intent filter. An intent outside the taxonomy, or one the instance
// hides, is PULSE_RECOMMEND_INTENT_UNKNOWN — the code pulse_recommend
// raises — whose details.valid lists the instance's intents. A hidden
// operator's aliases and a hidden intent's Sounds never expand a query,
// and a hidden intent is dropped from every summary. Never nil on
// success.
func (s *InstanceSnapshot) ExamplesSearch(q examples.Query) ([]examples.ExampleSummary, error) {
	g := s.Ontology()
	visible := func(id string) bool {
		return g.Has(OntologyID(descriptor.OntologyNodeIntent, id))
	}
	if q.Intent != "" && (!isIntentID(q.Intent) || !visible(q.Intent)) {
		valid := []string{}
		for _, id := range IntentIDs() {
			if visible(id) {
				valid = append(valid, id)
			}
		}
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_RECOMMEND_INTENT_UNKNOWN,
			"examples search: unknown intent "+strconv.Quote(q.Intent),
			map[string]any{"intent": q.Intent, "valid": valid})
	}
	opts := examples.Options{Aliases: s.KnownAs(), Sounds: map[string]string{}}
	for _, in := range intentRegistry {
		if !visible(in.ID) {
			continue
		}
		for _, snd := range in.Sounds {
			opts.Sounds[FoldAlias(snd)] = in.ID
		}
	}
	if s.Scoped() {
		opts.KeepIntent = visible
	}
	return s.Discovery().SearchExamples(q, opts), nil
}

func isIntentID(id string) bool {
	for _, in := range intentRegistry {
		if in.ID == id {
			return true
		}
	}
	return false
}
