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
	if q.Intent != "" {
		if err := s.checkIntent("examples search", q.Intent); err != nil {
			return nil, err
		}
	}
	visible := s.intentVisible
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

// intentVisible reports whether the instance ontology keeps the intent
// node id — false for an ID outside the taxonomy or one the feature
// profile prunes.
func (s *InstanceSnapshot) intentVisible(id string) bool {
	return s.Ontology().Has(OntologyID(descriptor.OntologyNodeIntent, id))
}

// checkIntent is the ONE intent resolver of the intent-scoped discovery
// surfaces (example search, skills for an intent): an ID outside the
// taxonomy, or one the instance hides, is PULSE_RECOMMEND_INTENT_UNKNOWN —
// the code pulse_recommend raises — with details.intent and
// details.valid (the instance's visible intents, declaration order).
// surface prefixes the message.
func (s *InstanceSnapshot) checkIntent(surface, intent string) error {
	if isIntentID(intent) && s.intentVisible(intent) {
		return nil
	}
	valid := []string{}
	for _, id := range IntentIDs() {
		if s.intentVisible(id) {
			valid = append(valid, id)
		}
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_RECOMMEND_INTENT_UNKNOWN,
		surface+": unknown intent "+strconv.Quote(intent),
		map[string]any{"intent": intent, "valid": valid})
}

func isIntentID(id string) bool {
	for _, in := range intentRegistry {
		if in.ID == id {
			return true
		}
	}
	return false
}
